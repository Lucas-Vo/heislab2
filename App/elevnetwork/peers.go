package elevnetwork

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	quic "github.com/quic-go/quic-go"
)

const (
	helloMagic uint32 = 0x48454C4F // "HELO"
)

// encodeHelloFrame produces a fixed-size payload you send via WriteFixedFrameQUIC.
func encodeHelloFrame(selfID int) []byte {
	// We'll put a tiny header in the first bytes, rest zero-padded by WriteFixedFrameQUIC.
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b[0:4], helloMagic)
	binary.BigEndian.PutUint32(b[4:8], uint32(selfID))
	return b
}

func decodeHelloFrame(frame []byte) (peerID int, ok bool) {
	if len(frame) < 8 {
		return 0, false
	}
	if binary.BigEndian.Uint32(frame[0:4]) != helloMagic {
		return 0, false
	}
	id := int(binary.BigEndian.Uint32(frame[4:8]))
	if id <= 0 {
		return 0, false
	}
	return id, true
}

type Peer struct {
	id     int
	conn   *quic.Conn
	stream *quic.Stream
	sender *QUICSender
}

type PeerManager struct {
	selfID    int
	frameSize int

	mu    sync.RWMutex
	peers map[int]*Peer
}

func NewPeerManager(selfID int, frameSize int) *PeerManager {
	if frameSize <= 0 {
		frameSize = QUIC_FRAME_SIZE
	}
	return &PeerManager{
		selfID:    selfID,
		frameSize: frameSize,
		peers:     make(map[int]*Peer),
	}
}

// AddOrReplace registers a peer connection (dedupe). If a peer already exists, we keep the existing one
// and close the new one OR replace based on a deterministic rule.
//
// Here: keep the connection initiated by the lower ID to reduce churn.
// That means: if selfID < peerID, our outbound dial "wins"; else inbound "wins".
func (pm *PeerManager) AddOrReplace(p *Peer) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	existing := pm.peers[p.id]
	if existing == nil {
		pm.peers[p.id] = p
		return
	}

	// Determine which side "should" own the connection according to dial rule.
	// Dial rule is: only lower ID dials higher ID.
	// So if selfID < peerID, our outbound dial is expected; if selfID > peerID, inbound is expected.
	// We can't perfectly detect inbound/outbound here without extra metadata,
	// but we can approximate with: if we already have one, keep it to avoid flapping.
	// (This is safest for lab.)
	_ = p.sender.Close() // close newcomer
}

// SendTo sends to one peer if connected.
func (pm *PeerManager) SendTo(peerID int, payload []byte, timeout time.Duration) error {
	pm.mu.RLock()
	p := pm.peers[peerID]
	pm.mu.RUnlock()

	if p == nil || p.sender == nil {
		return fmt.Errorf("no connection to peer %d", peerID)
	}
	_, err := p.sender.SendFixed(payload, pm.frameSize, timeout)
	return err
}

// SendToAll sends to all connected peers.
func (pm *PeerManager) SendToAll(payload []byte, timeout time.Duration) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for id, p := range pm.peers {
		if p == nil || p.sender == nil {
			continue
		}
		_, _ = p.sender.SendFixed(payload, pm.frameSize, timeout)
		_ = id
	}
}

// ConnectedPeerIDs returns a snapshot list, useful for logging.
func (pm *PeerManager) ConnectedPeerIDs() []int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	out := make([]int, 0, len(pm.peers))
	for id := range pm.peers {
		out = append(out, id)
	}
	return out
}

// DialPeer performs the HELLO exchange and registers the peer in the manager.
func (pm *PeerManager) DialPeer(ctx context.Context, peerAddr string, quicConf *quic.Config) error {
	s, err := NewQUICSender(ctx, peerAddr, quicConf, 3*time.Second)
	if err != nil {
		return err
	}

	// HELLO: send our ID, then read their HELLO.
	_, err = s.SendFixed(encodeHelloFrame(pm.selfID), pm.frameSize, 2*time.Second)
	if err != nil {
		_ = s.Close()
		return fmt.Errorf("send hello: %w", err)
	}

	var peerID int
	readErr := ReadFixedFramesQUIC(ctx, s.Stream(), pm.frameSize, func(frame []byte) {
		if id, ok := decodeHelloFrame(frame); ok {
			peerID = id
		}
	})
	if readErr != nil {
		// readErr will often return nil if ctx canceled; but in dial path we expect one frame quickly.
	}

	if peerID == 0 {
		_ = s.Close()
		return fmt.Errorf("did not receive valid HELLO from %s", peerAddr)
	}

	p := &Peer{
		id:     peerID,
		conn:   s.Conn(),
		stream: s.Stream(),
		sender: s,
	}
	pm.AddOrReplace(p)
	return nil
}

// HandleIncomingConn accepts the first stream, does HELLO exchange, registers peer,
// then continuously reads frames from that stream and calls onFrame.
func (pm *PeerManager) HandleIncomingConn(
	ctx context.Context,
	conn *quic.Conn,
	onFrame func(fromPeerID int, frame []byte),
) {
	// Accept a stream from the dialer.
	st, err := conn.AcceptStream(ctx)
	if err != nil {
		return
	}

	// Read their HELLO first.
	var peerID int
	helloDone := make(chan struct{})

	go func() {
		_ = ReadFixedFramesQUIC(ctx, st, pm.frameSize, func(frame []byte) {
			// First valid hello sets peerID and signals done.
			if peerID == 0 {
				if id, ok := decodeHelloFrame(frame); ok {
					peerID = id
					close(helloDone)
					return
				}
				// Ignore junk until we see HELLO.
				return
			}
			// After HELLO, treat frames as app data.
			if onFrame != nil {
				cp := make([]byte, len(frame))
				copy(cp, frame)
				onFrame(peerID, cp)
			}
		})
	}()

	select {
	case <-helloDone:
	case <-time.After(3 * time.Second):
		_ = st.Close()
		_ = conn.CloseWithError(0, "hello timeout")
		return
	case <-ctx.Done():
		_ = st.Close()
		_ = conn.CloseWithError(0, "shutdown")
		return
	}

	// Send our HELLO back.
	_, _ = WriteFixedFrameQUIC(st, encodeHelloFrame(pm.selfID), pm.frameSize, 2*time.Second)

	// Register peer with a QUICSender built from this conn+stream.
	// (We wrap it so we can write using the same SendFixed path.)
	s := &QUICSender{conn: conn, stream: st}
	pm.AddOrReplace(&Peer{
		id:     peerID,
		conn:   conn,
		stream: st,
		sender: s,
	})

	// Reader goroutine continues until stream closes.
}
