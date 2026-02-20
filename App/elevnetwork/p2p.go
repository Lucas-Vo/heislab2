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
	helloMagic        uint32 = 0x48454C4F // "HELO"
	helloTimeout             = 2 * time.Second
	openStreamTimeout        = 2 * time.Second
	contextTimeout           = 4 * time.Second
	incomingBufSize          = QUIC_FRAME_SIZE
)

type PeerManager struct {
	selfID    int
	frameSize int

	mu    sync.RWMutex
	peers map[int]*peer

	incoming <-chan []byte
	quicConf *quic.Config
}

type peer struct {
	id       int
	outbound bool // true if we dialed and opened the stream

	conn   *quic.Conn
	stream *quic.Stream

	once sync.Once // ensure only one reader started per peer
}

func (pm *PeerManager) NewPeerManager(cfg common.Config) {

	quicConf := &quic.Config{
		KeepAlivePeriod:      2 * time.Second,
		HandshakeIdleTimeout: 3 * time.Second,
		MaxIdleTimeout:       6 * time.Second,
	}

	pm.selfID = cfg.SelfID
	pm.frameSize = QUIC_FRAME_SIZE
	pm.peers = make(map[int]*peer)
	incoming := make(chan []byte, incomingBufSize)
	pm.incoming = incoming
	pm.quicConf = quicConf

}

func (pm *PeerManager) StartP2P(ctx context.Context, cfg common.Config, port int) <-chan []byte {
	peers, selfID, err := cfg.PeerAddrsForPort(port)
	if err != nil {
		log.Fatal(err)
	}
	listenAddr := cfg.ListenAddrForPort(port)
	log.Printf("[p2p port=%d] Self=%d peers=%v listen=%s", port, selfID, peers, listenAddr)

	go pm.runListener(ctx, listenAddr)

	// Dial only peers with higher ID
	for peerID, peerAddr := range peers {
		if pm.selfID < peerID {
			go pm.dialLoop(ctx, peerID, peerAddr)
		}
	}

	return pm.incoming
}

// Listener
func (pm *PeerManager) runListener(ctx context.Context, listenAddr string) {
	err := ListenQUIC(ctx, listenAddr, pm.quicConf, func(conn *quic.Conn) {
		log.Printf("ACCEPT conn from %v", conn.RemoteAddr())
		pm.handleIncomingConn(ctx, conn)
	})
	if err != nil && ctx.Err() == nil {
		log.Printf("ListenQUIC error: %v", err)
	}
}

// dialLoop keeps the connection alive: connect -> wait for close -> reconnect.
func (pm *PeerManager) dialLoop(ctx context.Context, id int, addr string) {
	backoff := 200 * time.Millisecond
	for ctx.Err() == nil {

		if pm.peers[id] != nil && pm.peers[id].conn != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		log.Printf("dialLoop: attempting elev-%d at %s", id, addr)

		peerID, conn, stream, err := pm.dialPeerOnce(ctx, addr, pm.quicConf)
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
			CloseQUIC(conn, stream, "bye")
			pm.removePeerByConn(conn)
			return
		case <-conn.Context().Done():
			CloseQUIC(conn, stream, "bye")
			pm.removePeerByConn(conn)
			log.Printf("dialLoop: connection to elev-%d ended, reconnecting", id)
			time.Sleep(300 * time.Millisecond)
		}
	}
}

func (pm *PeerManager) dialPeerOnce(ctx context.Context, peerAddr string, quicConf *quic.Config) (int, *quic.Conn, *quic.Stream, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()

	conn, st, err := DialQUIC(attemptCtx, peerAddr, quicConf, openStreamTimeout)
	if err != nil {
		return 0, nil, nil, err
	}
	fail := func(e error) (int, *quic.Conn, *quic.Stream, error) {
		CloseQUIC(conn, st, "dial failed")
		return 0, nil, nil, e
	}
	if st == nil {
		return fail(fmt.Errorf("stream is nil"))
	}

	peerID, err := pm.exchangeHello(st, true)
	if err != nil {
		return fail(err)
	}

	p := &peer{id: peerID, outbound: true, conn: conn, stream: st}
	if pm.addOrReject(p) {
		pm.startReader(ctx, p)
	}

	return peerID, conn, st, nil
}

func (pm *PeerManager) handleIncomingConn(ctx context.Context, conn *quic.Conn) {
	st, err := conn.AcceptStream(ctx)
	if err != nil {
		return
	}

	peerID, err := pm.exchangeHello(st, false)
	if err != nil {
		_ = st.Close()
		_ = conn.CloseWithError(0, "hello failed")
		return
	}

	log.Printf("SERVER got HELLO peerID=%d from %v", peerID, conn.RemoteAddr())

	p := &peer{id: peerID, outbound: false, conn: conn, stream: st}
	if pm.addOrReject(p) {
		pm.startReader(ctx, p)
	}

	// Cleanup when inbound connection closes.
	go func(c *quic.Conn) {
		<-c.Context().Done()
		pm.removePeerByConn(c)
	}(conn)
}

// Start exactly one read loop for a peer; pushes frames onto pm.incoming.
func (pm *PeerManager) startReader(ctx context.Context, p *peer) {
	p.once.Do(func() {
		go func(peerID int, st *quic.Stream) {
			ReadFixedFramesQUIC(ctx, st, pm.frameSize, func(frame []byte) {})
			pm.removePeerByConn(p.conn)
		}(p.id, p.stream)
	})
}

func (pm *PeerManager) sendToAll(payload []byte, timeout time.Duration) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for _, p := range pm.peers {
		if p == nil || p.stream == nil {
			continue
		}
		_, _ = WriteFixedFrameQUIC(p.stream, payload, pm.frameSize, timeout)
	}
}

func (pm *PeerManager) addOrReject(p *peer) bool {
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
		return true
	}

	// If we already have a connection for this peer, decide deterministically which to keep.
	keepNew := false

	if pm.selfID < p.id {
		keepNew = p.outbound
	}
	keepNew = !p.outbound

	if keepNew {
		CloseQUIC(existing.conn, existing.stream, "replaced")
		pm.peers[p.id] = p
		return true
	} else {
		CloseQUIC(p.conn, p.stream, "rejected")
	}
	return false
}

func (pm *PeerManager) removePeerByConn(conn *quic.Conn) {
	if conn == nil {
		return
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	for id, p := range pm.peers {
		if p != nil && p.conn == conn {
			delete(pm.peers, id)
			return
		}
	}
}

func (pm *PeerManager) exchangeHello(st *quic.Stream, outbound bool) (int, error) {
	if st == nil {
		return 0, fmt.Errorf("stream is nil")
	}
	if outbound {
		if err := writeHelloFrame(st, pm.selfID, pm.frameSize, helloTimeout); err != nil {
			return 0, fmt.Errorf("send hello: %w", err)
		}
		peerID, err := readHelloFrame(st, pm.frameSize, helloTimeout)
		if err != nil {
			return 0, fmt.Errorf("read hello: %w", err)
		}
		return peerID, nil
	}

	peerID, err := readHelloFrame(st, pm.frameSize, helloTimeout)
	if err != nil {
		return 0, fmt.Errorf("read hello: %w", err)
	}
	if err := writeHelloFrame(st, pm.selfID, pm.frameSize, helloTimeout); err != nil {
		return 0, fmt.Errorf("send hello: %w", err)
	}
	return peerID, nil
}

func readHelloFrame(st *quic.Stream, frameSize int, timeout time.Duration) (int, error) {
	if frameSize <= 0 {
		frameSize = QUIC_FRAME_SIZE
	}
	if d, ok := interface{}(st).(interface{ SetReadDeadline(time.Time) error }); ok && timeout > 0 {
		_ = d.SetReadDeadline(time.Now().Add(timeout))
		defer d.SetReadDeadline(time.Time{})
	}

	buf := make([]byte, frameSize)
	if _, err := io.ReadFull(st, buf); err != nil {
		return 0, err
	}

	if len(buf) < 8 || binary.BigEndian.Uint32(buf[0:4]) != helloMagic {
		return 0, fmt.Errorf("invalid hello")
	}
	peerID := int(binary.BigEndian.Uint32(buf[4:8]))
	if peerID <= 0 {
		return 0, fmt.Errorf("invalid hello")
	}
	return peerID, nil
}

func writeHelloFrame(st *quic.Stream, selfID int, frameSize int, timeout time.Duration) error {
	if frameSize <= 0 {
		frameSize = QUIC_FRAME_SIZE
	}
	frame := make([]byte, 8)
	binary.BigEndian.PutUint32(frame[0:4], helloMagic)
	binary.BigEndian.PutUint32(frame[4:8], uint32(selfID))
	_, err := WriteFixedFrameQUIC(st, frame, frameSize, timeout)
	return err
}
