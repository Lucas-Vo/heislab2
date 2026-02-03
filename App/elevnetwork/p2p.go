package elevnetwork

// imports
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

/* unexported helpers */

// AddOrReplace registers a peer connection (dedupe). If a peer already exists, we keep the existing one
// and close the new one OR replace based on a deterministic rule.
//
// Here: keep the connection initiated by the lower ID to reduce churn.
// That means: if selfID < peerID, our outbound dial "wins"; else inbound "wins".
func (pm *PeerManager) addOrReplace(p *peer) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	existing := pm.peers[p.id]
	if existing == nil {
		pm.peers[p.id] = p
		return
	}

	// Deterministic winner:
	// keep outbound connection from the lower ID.
	// That means:
	// - if we are lower than peer => we keep our outbound (the one we dialed)
	// - if we are higher => we keep inbound (the one they dialed)
	//
	// But we need to know whether `p` is outbound or inbound.
	// Easiest: add a flag on peer: outbound bool.

	// If you don't track direction, simplest safe approach: keep the newest and close the old:
	log.Printf("addOrReplace: peer=%d existing=%v new=%v", p.id, existing != nil, p.conn.RemoteAddr())
	_ = existing.sender.Close()
	pm.peers[p.id] = p
}

// DialPeer performs the HELLO exchange and registers the peer in the manager.
// DialPeer performs the HELLO exchange and registers the peer in the manager.
// It dials the remote QUIC endpoint, opens one bidirectional stream, sends our HELLO,
// then reads exactly one fixed-size HELLO frame back (with a deadline).
func (pm *PeerManager) dialPeer(ctx context.Context, peerAddr string, quicConf *quic.Config) error {
	s, err := NewQUICSender(ctx, peerAddr, quicConf, 3*time.Second)
	if err != nil {
		return err
	}

	// If anything fails after this point, close the connection/stream.
	fail := func(e error) error {
		_ = s.Close()
		return e
	}

	// 1) Send our HELLO.
	if _, err := s.SendFixed(encodeHelloFrame(pm.selfID), pm.frameSize, 2*time.Second); err != nil {
		return fail(fmt.Errorf("send hello: %w", err))
	}

	// 2) Read exactly one fixed-size frame back (peer's HELLO) with a read deadline.
	st := s.Stream()
	if st == nil {
		return fail(fmt.Errorf("stream is nil"))
	}
	if d, ok := interface{}(st).(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = d.SetReadDeadline(time.Now().Add(2 * time.Second))
	}

	buf := make([]byte, pm.frameSize)
	if _, err := io.ReadFull(st, buf); err != nil {
		return fail(fmt.Errorf("read hello: %w", err))
	}

	peerID, ok := decodeHelloFrame(buf)
	if !ok || peerID <= 0 {
		return fail(fmt.Errorf("invalid HELLO from %s", peerAddr))
	}

	// 3) Register this peer connection.
	p := &peer{
		id:     peerID,
		conn:   s.Conn(),
		stream: st,
		sender: s,
	}
	pm.addOrReplace(p)

	return nil
}

// HandleIncomingConn accepts the first stream, does HELLO exchange, registers peer,
// then continuously reads frames from that stream and calls onFrame.
func (pm *PeerManager) handleIncomingConn(ctx context.Context, conn *quic.Conn, onFrame func(fromPeerID int, frame []byte)) {
	st, err := conn.AcceptStream(ctx)
	if err != nil {
		return
	}

	// Read exactly one HELLO frame with a deadline
	if d, ok := interface{}(st).(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = d.SetReadDeadline(time.Now().Add(2 * time.Second))
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

	// Send our HELLO back
	_, _ = WriteFixedFrameQUIC(st, encodeHelloFrame(pm.selfID), pm.frameSize, 2*time.Second)

	// Register
	s := &QUICSender{conn: conn, stream: st}
	pm.addOrReplace(&peer{id: peerID, conn: conn, stream: st, sender: s})

	// Now read application frames forever
	_ = ReadFixedFramesQUIC(ctx, st, pm.frameSize, func(frame []byte) {
		onFrame(peerID, frame)
	})
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
		log.Printf("dialLoop: attempting elev-%d at %s", id, addr)

		if err := pm.dialPeer(ctx, addr, conf); err == nil {
			log.Printf("Connected (dial) to elev-%d at %s", id, addr)
			return
		} else {
			log.Printf("dial to elev-%d (%s) failed: %v", id, addr, err)
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

func readOneFixedFrameWithDeadline(
	ctx context.Context,
	st *quic.Stream,
	frameSize int,
	timeout time.Duration,
) ([]byte, error) {
	if frameSize <= 0 {
		frameSize = QUIC_FRAME_SIZE
	}
	if d, ok := interface{}(st).(interface{ SetReadDeadline(time.Time) error }); ok && timeout > 0 {
		_ = d.SetReadDeadline(time.Now().Add(timeout))
	}

	buf := make([]byte, frameSize)
	_, err := io.ReadFull(st, buf)
	if err != nil {
		return nil, err
	}
	return buf, nil
}
