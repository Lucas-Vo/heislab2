package elevnetwork

import (
	"context"
	"elevator/common"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	quic "github.com/quic-go/quic-go"
)

const (
	helloMagic uint32 = 0x48454C4F // "HELO"
)

type IncomingFrame struct {
	FromID int
	Frame  []byte
}

type PeerManager struct {
	selfID    int
	frameSize int

	mu    sync.RWMutex
	peers map[int]*peer

	incoming chan<- IncomingFrame
}

type peer struct {
	id     int
	conn   *quic.Conn
	stream *quic.Stream
	sender *QUICSender
}

func (pm *PeerManager) ConnectedPeerIDs() []int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	out := make([]int, 0, len(pm.peers))
	for id := range pm.peers {
		out = append(out, id)
	}
	return out
}

func StartP2P(ctx context.Context, cfg common.Config) (pm *PeerManager, incoming chan IncomingFrame) {
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
	log.Printf("Self=%d peers=%v listen=%s", selfID, peers, cfg.ListenAddr())

	quicConf := &quic.Config{
		KeepAlivePeriod: 2 * time.Second,
	}

	pm = newPeerManager(selfID, QUIC_FRAME_SIZE)

	incomingFrames := make(chan IncomingFrame, 64)
	pm.incoming = incomingFrames

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

// addOrReplace replaces any existing peer entry. It closes the old one (best effort).
func (pm *PeerManager) addOrReplace(p *peer) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	existing := pm.peers[p.id]
	if existing != nil {
		// Close old connection best-effort (nil-safe).
		if existing.sender != nil {
			_ = existing.sender.Close()
		} else if existing.conn != nil {
			_ = existing.conn.CloseWithError(0, "replaced")
		}
	}

	pm.peers[p.id] = p
	log.Printf("peer %d registered (remote=%v). connected=%v", p.id, safeRemote(p.conn), pm.connectedIDsLocked())
}

func (pm *PeerManager) connectedIDsLocked() []int {
	out := make([]int, 0, len(pm.peers))
	for id := range pm.peers {
		out = append(out, id)
	}
	return out
}

func safeRemote(c *quic.Conn) any {
	if c == nil {
		return nil
	}
	return c.RemoteAddr()
}

// dialPeerOnce: connect + HELLO exchange + register peer.
// Returns peerID and a sender whose conn/stream stays open for later use.
func (pm *PeerManager) dialPeerOnce(ctx context.Context, peerAddr string, quicConf *quic.Config) (int, *QUICSender, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	s, err := NewQUICSender(attemptCtx, peerAddr, quicConf, 2*time.Second)
	if err != nil {
		return 0, nil, err
	}
	fail := func(e error) (int, *QUICSender, error) { _ = s.Close(); return 0, nil, e }

	if _, err := s.SendFixed(encodeHelloFrame(pm.selfID), pm.frameSize, 2*time.Second); err != nil {
		return fail(fmt.Errorf("send hello: %w", err))
	}

	st := s.Stream()
	if st == nil {
		return fail(fmt.Errorf("stream is nil"))
	}

	if d, ok := interface{}(st).(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = d.SetReadDeadline(time.Now().Add(2 * time.Second))
		defer d.SetReadDeadline(time.Time{}) // clear
	}

	buf := make([]byte, pm.frameSize)
	if _, err := io.ReadFull(st, buf); err != nil {
		return fail(fmt.Errorf("read hello: %w", err))
	}

	peerID, ok := decodeHelloFrame(buf)
	if !ok || peerID <= 0 {
		return fail(fmt.Errorf("invalid HELLO from %s", peerAddr))
	}

	pm.addOrReplace(&peer{id: peerID, conn: s.Conn(), stream: st, sender: s})

	go pm.readLoop(ctx, peerID, st)

	return peerID, s, nil
}

func (pm *PeerManager) readLoop(ctx context.Context, peerID int, st *quic.Stream) {
	_ = ReadFixedFramesQUIC(ctx, st, pm.frameSize, func(frame []byte) {
		ch := pm.incoming
		if ch == nil {
			return
		}
		select {
		case ch <- IncomingFrame{FromID: peerID, Frame: frame}:
		default:
		}
	})
}

// dialLoop keeps the connection alive: connect -> wait for close -> reconnect.
func dialLoop(ctx context.Context, pm *PeerManager, id int, addr string, conf *quic.Config) {
	backoff := 200 * time.Millisecond

	for ctx.Err() == nil {
		log.Printf("dialLoop: attempting elev-%d at %s", id, addr)

		peerID, sender, err := pm.dialPeerOnce(ctx, addr, conf)
		if err != nil {
			log.Printf("dial to elev-%d (%s) failed: %v", id, addr, err)
			time.Sleep(backoff)
			if backoff < 2*time.Second {
				backoff *= 2
			}
			continue
		}

		backoff = 200 * time.Millisecond
		log.Printf("Connected (dial) to elev-%d at %s (peerID=%d)", id, addr, peerID)

		// Block until this connection is closed, then loop and reconnect.
		// We don't need perfect detection; just avoid exiting the goroutine.
		select {
		case <-ctx.Done():
			_ = sender.Close()
			return
		case <-sender.Conn().Context().Done():
			// Connection ended (peer closed / network issue)
			_ = sender.Close()
			log.Printf("dialLoop: connection to elev-%d ended, reconnecting", id)
			time.Sleep(300 * time.Millisecond)
		}
	}
}

// Listener
func runListener(ctx context.Context, cfg common.Config, quicConf *quic.Config, pm *PeerManager, incomingFrames chan<- IncomingFrame) {
	err := ListenQUIC(ctx, cfg.ListenAddr(), quicConf, func(conn *quic.Conn) {
		log.Printf("ACCEPT conn from %v", conn.RemoteAddr())

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

// handleIncomingConn does synchronous HELLO then reads application frames forever.
func (pm *PeerManager) handleIncomingConn(ctx context.Context, conn *quic.Conn, onFrame func(fromPeerID int, frame []byte)) {
	st, err := conn.AcceptStream(ctx)
	if err != nil {
		return
	}

	// Read exactly one HELLO frame with deadline
	if d, ok := interface{}(st).(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = d.SetReadDeadline(time.Now().Add(2 * time.Second))
		defer d.SetReadDeadline(time.Time{}) // clear
	}

	buf := make([]byte, pm.frameSize)
	if _, err := io.ReadFull(st, buf); err != nil {
		_ = st.Close()
		_ = conn.CloseWithError(0, "hello read failed")
		return
	}

	peerID, ok := decodeHelloFrame(buf)
	if !ok {
		_ = st.Close()
		_ = conn.CloseWithError(0, "bad hello")
		return
	}

	log.Printf("SERVER got HELLO peerID=%d from %v", peerID, conn.RemoteAddr())

	// Send our HELLO back
	_, _ = WriteFixedFrameQUIC(st, encodeHelloFrame(pm.selfID), pm.frameSize, 2*time.Second)

	// Register
	s := &QUICSender{conn: conn, stream: st}
	pm.addOrReplace(&peer{id: peerID, conn: conn, stream: st, sender: s})

	// Now read application frames forever
	_ = ReadFixedFramesQUIC(ctx, st, pm.frameSize, func(frame []byte) {
		if onFrame != nil {
			onFrame(peerID, frame)
		}
	})
}

// HELLO encoding/decoding
func encodeHelloFrame(selfID int) []byte {
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

// SendTo / SendToAll
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
