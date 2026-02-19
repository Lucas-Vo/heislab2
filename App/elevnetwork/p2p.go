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

type PeerManager struct {
	selfID    int
	frameSize int

	mu    sync.RWMutex
	peers map[int]*peer

	incoming chan<- []byte
}

type peer struct {
	id       int
	outbound bool // true if we dialed and opened the stream

	conn   *quic.Conn
	stream *quic.Stream
	sender *QUICSender

	once sync.Once // ensure only one reader started per peer
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

func (pm *PeerManager) hasLivePeer(id int) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	p := pm.peers[id]
	if p == nil || p.conn == nil {
		return false
	}
	select {
	case <-p.conn.Context().Done():
		return false
	default:
		return true
	}
}
func StartP2P(ctx context.Context, cfg common.Config, port int) chan []byte {
	selfID := cfg.SelfID
	if selfID == 0 {
		id, err := cfg.DetectSelfID()
		if err != nil {
			log.Fatalf("DetectSelfID: %v", err)
		}
		selfID = id
	}

	peers, _, err := cfg.PeerAddrsForPort(port)
	if err != nil {
		log.Fatal(err)
	}
	listenAddr := cfg.ListenAddrForPort(port)
	log.Printf("[p2p port=%d] Self=%d peers=%v listen=%s", port, selfID, peers, listenAddr)

	quicConf := &quic.Config{
		KeepAlivePeriod:      2 * time.Second,
		HandshakeIdleTimeout: 3 * time.Second,
		MaxIdleTimeout:       6 * time.Second,
	}

	pm := newPeerManager(selfID, QUIC_FRAME_SIZE)

	incomingFrames := make(chan []byte, 64)
	pm.incoming = incomingFrames

	go runListener(ctx, listenAddr, quicConf, pm)

	// Dial only peers we should keep an outbound connection to (deterministic).
	for peerID, peerAddr := range peers {
		if selfID < peerID {
			go dialLoop(ctx, pm, peerID, peerAddr, quicConf)
		}
	}

	return incomingFrames
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
	if existing != nil && existing.conn != nil {
		select {
		case <-existing.conn.Context().Done():
			delete(pm.peers, p.id)
			existing = nil
			log.Printf("peer %d removed stale connection before add", p.id)
		default:
		}
	}
	if existing == nil {
		pm.peers[p.id] = p
		log.Printf("peer %d registered (outbound=%v remote=%v). connected=%v", p.id, p.outbound, safeRemote(p.conn), pm.connectedIDsLocked())
		return
	}

	// If we already have a connection for this peer, decide deterministically which to keep.
	keepNew := pm.shouldKeep(p.outbound, p.id)

	if keepNew {
		// Close old
		if existing.sender != nil {
			_ = existing.sender.Close()
		} else if existing.conn != nil {
			_ = existing.conn.CloseWithError(0, "replaced")
		}
		pm.peers[p.id] = p
		log.Printf("peer %d replaced (kept new outbound=%v remote=%v). connected=%v", p.id, p.outbound, safeRemote(p.conn), pm.connectedIDsLocked())
	} else {
		// Reject new
		if p.sender != nil {
			_ = p.sender.Close()
		} else if p.conn != nil {
			_ = p.conn.CloseWithError(0, "rejected")
		}
		log.Printf("peer %d rejected duplicate (new outbound=%v). keeping outbound=%v", p.id, p.outbound, existing.outbound)
	}
}

func (pm *PeerManager) removePeerByConn(conn *quic.Conn, reason string) {
	if conn == nil {
		return
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	for id, p := range pm.peers {
		if p != nil && p.conn == conn {
			delete(pm.peers, id)
			log.Printf("peer %d removed (%s). connected=%v", id, reason, pm.connectedIDsLocked())
			return
		}
	}
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

// Start exactly one read loop for a peer; pushes frames onto pm.incoming.
func (pm *PeerManager) startReader(ctx context.Context, p *peer) {
	p.once.Do(func() {
		log.Printf("startReader: peer=%d outbound=%v remote=%v", p.id, p.outbound, safeRemote(p.conn))

		go func(peerID int, st *quic.Stream) {
			nFrames := 0

			err := ReadFixedFramesQUIC(ctx, st, pm.frameSize, func(frame []byte) {
				nFrames++
				if nFrames <= 5 {
					log.Printf("startReader: peer=%d got frame #%d len=%d head=%q",
						peerID, nFrames, len(frame), string(frame[:min(16, len(frame))]))
				}

				ch := pm.incoming
				if ch == nil {
					log.Printf("startReader: peer=%d incoming channel is nil (dropping)", peerID)
					return
				}

				select {
				case ch <- frame:
				default:
					// channel full => drop
					log.Printf("startReader: peer=%d incoming channel FULL (dropping frame #%d)", peerID, nFrames)
				}
			})

			log.Printf("startReader: peer=%d exited after %d frames, err=%v", peerID, nFrames, err)
			pm.removePeerByConn(p.conn, "reader exited")
		}(p.id, p.stream)
	})
}

func (pm *PeerManager) dialPeerOnce(ctx context.Context, peerAddr string, quicConf *quic.Config) (int, *QUICSender, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	s, err := NewQUICSender(attemptCtx, peerAddr, quicConf, 2*time.Second)
	if err != nil {
		return 0, nil, err
	}
	fail := func(e error) (int, *QUICSender, error) { _ = s.Close(); return 0, nil, e }

	// Send our HELLO
	if _, err := s.SendFixed(encodeHelloFrame(pm.selfID), pm.frameSize, 2*time.Second); err != nil {
		return fail(fmt.Errorf("send hello: %w", err))
	}

	st := s.Stream()
	if st == nil {
		return fail(fmt.Errorf("stream is nil"))
	}

	// Read their HELLO
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

	p := &peer{id: peerID, outbound: true, conn: s.Conn(), stream: st, sender: s}
	pm.addOrReject(p)
	// Start reader only if the peer we just created is the one stored.
	pm.mu.RLock()
	stored := pm.peers[peerID]
	pm.mu.RUnlock()
	if stored == p {
		pm.startReader(ctx, p)
	}

	return peerID, s, nil
}

// dialLoop keeps the connection alive: connect -> wait for close -> reconnect.
func dialLoop(ctx context.Context, pm *PeerManager, id int, addr string, conf *quic.Config) {
	backoff := 200 * time.Millisecond
	for ctx.Err() == nil {
		if pm.hasLivePeer(id) {
			time.Sleep(500 * time.Millisecond)
			continue
		}
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

		select {
		case <-ctx.Done():
			_ = sender.Close()
			pm.removePeerByConn(sender.Conn(), "dial ctx done")
			return
		case <-sender.Conn().Context().Done():
			_ = sender.Close()
			pm.removePeerByConn(sender.Conn(), "dial conn done")
			log.Printf("dialLoop: connection to elev-%d ended, reconnecting", id)
			time.Sleep(300 * time.Millisecond)
		}
	}
}

// Listener
func runListener(ctx context.Context, listenAddr string, quicConf *quic.Config, pm *PeerManager) {
	err := ListenQUIC(ctx, listenAddr, quicConf, func(conn *quic.Conn) {
		log.Printf("ACCEPT conn from %v", conn.RemoteAddr())
		pm.handleIncomingConn(ctx, conn)
	})
	if err != nil && ctx.Err() == nil {
		log.Printf("ListenQUIC error: %v", err)
	}
}

// handleIncomingConn: accept stream, read HELLO, reply HELLO, register + start reader.
// (outbound=false)
func (pm *PeerManager) handleIncomingConn(ctx context.Context, conn *quic.Conn) {
	st, err := conn.AcceptStream(ctx)
	if err != nil {
		return
	}

	if d, ok := interface{}(st).(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = d.SetReadDeadline(time.Now().Add(2 * time.Second))
		defer d.SetReadDeadline(time.Time{})
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

	_, _ = WriteFixedFrameQUIC(st, encodeHelloFrame(pm.selfID), pm.frameSize, 2*time.Second)

	s := &QUICSender{conn: conn, stream: st}
	p := &peer{id: peerID, outbound: false, conn: conn, stream: st, sender: s}
	pm.addOrReject(p)

	pm.mu.RLock()
	stored := pm.peers[peerID]
	pm.mu.RUnlock()
	if stored == p {
		pm.startReader(ctx, p)
	}

	// Cleanup when inbound connection closes.
	go func(c *quic.Conn) {
		<-c.Context().Done()
		pm.removePeerByConn(c, "accept conn done")
	}(conn)
}

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
