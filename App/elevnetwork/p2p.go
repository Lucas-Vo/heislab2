package elevnetwork

// imports
import (
	"context"
	"elevator/common"
	"encoding/binary"
	"fmt"
	"log"
	"sync"
	"time"

	quic "github.com/quic-go/quic-go"
)

// consts
const (
	helloMagic uint32 = 0x48454C4F // "HELO"
)

// exported types
type IncomingFrame struct {
	FromID int
	Frame  []byte
}

type PeerManager struct {
	selfID    int
	frameSize int

	mu    sync.RWMutex
	peers map[int]*peer
}

// non-exported types

type peer struct {
	id     int
	conn   *quic.Conn
	stream *quic.Stream
	sender *QUICSender
}

/* exported functions */

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

func StartP2P(
	ctx context.Context,
	cfg common.Config,
) (pm *PeerManager, incoming <-chan IncomingFrame) {
	// Prefer cfg.SelfID if present; fall back to detection
	selfID := cfg.SelfID
	if selfID == 0 {
		id, err := cfg.DetectSelfID()
		if err != nil {
			log.Fatalf("DetectSelfID: %v", err)
		}
		selfID = id
	}

	peers, _, err := cfg.PeerAddrs()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Self=%d peers=%v", selfID, peers)

	quicConf := &quic.Config{
		KeepAlivePeriod: 2 * time.Second,
	}

	pm = newPeerManager(selfID, QUIC_FRAME_SIZE)

	incomingFrames := make(chan IncomingFrame, 64)

	go runListener(ctx, cfg, quicConf, pm, incomingFrames)

	// Dial rule: only dial higher IDs
	for peerID, peerAddr := range peers {
		if selfID < peerID {
			go dialLoop(ctx, pm, peerID, peerAddr, quicConf)
		}
	}

	return pm, incomingFrames
}

func newPeerManager(selfID int, frameSize int) *PeerManager {
	if frameSize <= 0 {
		frameSize = QUIC_FRAME_SIZE
	}
	return &PeerManager{
		selfID:    selfID,
		frameSize: frameSize,
		peers:     make(map[int]*peer),
	}
}

func (pm *PeerManager) shouldKeep(outbound bool, peerID int) bool {
	if pm.selfID < peerID {
		return outbound
	}
	return !outbound
}

func (pm *PeerManager) addOrReject(p *peer) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	existing := pm.peers[p.id]
	if existing == nil {
		pm.peers[p.id] = p
		return
	}

	_ = p.sender.Close() // close newcomer
}

// DialPeer performs the HELLO exchange and registers the peer in the manager.
func (pm *PeerManager) dialPeer(ctx context.Context, peerAddr string, quicConf *quic.Config) error {
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

	p := &peer{
		id:     peerID,
		conn:   s.Conn(),
		stream: s.Stream(),
		sender: s,
	}
	pm.addOrReplace(p)
	return nil
}

// HandleIncomingConn accepts the first stream, does HELLO exchange, registers peer,
// then continuously reads frames from that stream and calls onFrame.
func (pm *PeerManager) handleIncomingConn(
	ctx context.Context,
	conn *quic.Conn,
	onFrame func(fromPeerID int, frame []byte),
) {
	// Accept a stream from the dialer.
	st, err := conn.AcceptStream(ctx)
	if err != nil {
		return
	}

	helloDone := make(chan int, 1)

	go readIncomingStream(
		ctx,
		st,
		pm.frameSize,
		func(id int) {
			helloDone <- id
		},
		onFrame,
	)

	var peerID int
	select {
	case peerID = <-helloDone:

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
	pm.addOrReplace(&peer{
		id:     peerID,
		conn:   conn,
		stream: st,
		sender: s,
	})

	// Reader goroutine continues until stream closes.
}

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

// SendTo sends to one peer if connected.
func (pm *PeerManager) sendTo(peerID int, payload []byte, timeout time.Duration) error {
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
func (pm *PeerManager) sendToAll(payload []byte, timeout time.Duration) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for _, p := range pm.peers {
		if p == nil || p.sender == nil {
			continue
		}
		_, _ = p.sender.SendFixed(payload, pm.frameSize, timeout)
	}
}

// Listener
func runListener(ctx context.Context, cfg common.Config, quicConf *quic.Config, pm *PeerManager, incomingFrames chan<- IncomingFrame) {
	err := ListenQUIC(ctx, cfg.ListenAddr(), quicConf, func(conn *quic.Conn) {
		pm.handleIncomingConn(ctx, conn, func(from int, frame []byte) {
			select {
			case incomingFrames <- IncomingFrame{FromID: from, Frame: frame}:
			default:
			}
		})
	})
	if err != nil && ctx.Err() == nil {
		log.Printf("ListenQUIC error: %v", err)
	}
}

func dialLoop(ctx context.Context, pm *PeerManager, id int, addr string, conf *quic.Config) {
	for ctx.Err() == nil {
		if err := pm.dialPeer(ctx, addr, conf); err == nil {
			log.Printf("Connected (dial) to elev-%d at %s", id, addr)
			return
		}
		time.Sleep(1 * time.Second)
	}
}

func readIncomingStream(
	ctx context.Context,
	st *quic.Stream,
	frameSize int,
	onHello func(peerID int),
	onFrame func(peerID int, frame []byte),
) {
	var peerID int

	_ = ReadFixedFramesQUIC(ctx, st, frameSize, func(frame []byte) {
		// Expect HELLO first
		if peerID == 0 {
			if id, ok := decodeHelloFrame(frame); ok {
				peerID = id
				if onHello != nil {
					onHello(peerID)
				}
			}
			// Ignore everything until HELLO
			return
		}

		// Normal application frames
		if onFrame != nil {
			onFrame(peerID, frame)
		}
	})
}
