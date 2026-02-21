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
	helloMagic           uint32 = 0x48454C4F // "HELO"
	helloTimeout                = 2 * time.Second
	openStreamTimeout           = 2 * time.Second
	dialTimeout                 = 4 * time.Second
	writeTimeout                = 150 * time.Millisecond
	incomingBufSize             = 128
	KeepAlivePeriod             = 2 * time.Second
	HandshakeIdleTimeout        = 3 * time.Second
	MaxIdleTimeout              = 6 * time.Second
)

type Manager struct {
	selfID    int
	frameSize int
	quicConf  *quic.Config
	mu        sync.RWMutex
	peers     map[int]*peer
	incoming  chan []byte
}

type peer struct {
	conn   *quic.Conn
	stream *quic.Stream
}

func NewPeerManager() *Manager {
	return &Manager{
		frameSize: FrameSize,
		quicConf: &quic.Config{
			KeepAlivePeriod:      KeepAlivePeriod,
			HandshakeIdleTimeout: HandshakeIdleTimeout,
			MaxIdleTimeout:       MaxIdleTimeout,
		},
		peers:    make(map[int]*peer),
		incoming: make(chan []byte, incomingBufSize),
	}
}

func (m *Manager) Start(ctx context.Context, cfg common.Config, port int) <-chan []byte {
	peers, selfID, err := cfg.PeerAddrsForPort(port)
	if err != nil {
		log.Fatal(err)
	}
	m.selfID = selfID
	listenAddr := cfg.ListenAddrForPort(port)
	log.Printf("[p2p port=%d] self=%d peers=%v listen=%s", port, selfID, peers, listenAddr)

	go m.listen(ctx, listenAddr)
	for peerID, peerAddr := range peers {
		if selfID < peerID {
			go m.dialLoop(ctx, peerID, peerAddr)
		}
	}
	return m.incoming
}

func (m *Manager) Broadcast(payload []byte) {
	m.mu.RLock()
	peers := make([]*peer, 0, len(m.peers))
	for _, p := range m.peers {
		if p != nil && p.stream != nil {
			peers = append(peers, p)
		}
	}
	m.mu.RUnlock()
	for _, p := range peers {
		_, _ = WriteFixedFrame(p.stream, payload, m.frameSize, writeTimeout)
	}
}

func (m *Manager) listen(ctx context.Context, addr string) {
	err := Listen(ctx, addr, m.quicConf, func(conn *quic.Conn) {
		m.handleIncoming(ctx, conn)
	})
	if err != nil && ctx.Err() == nil {
		log.Printf("p2p listen error: %v", err)
	}
}

func (m *Manager) dialLoop(ctx context.Context, id int, addr string) {
	backoff := 200 * time.Millisecond
	for ctx.Err() == nil {
		if m.hasPeer(id) {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		peerID, conn, st, err := m.dialOnce(ctx, addr)
		if err != nil {
			log.Printf("dial to %d (%s) failed: %v", id, addr, err)
			time.Sleep(backoff)
			if backoff < 2*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = 200 * time.Millisecond
		if !m.register(peerID, conn, st) {
			Close(conn, st, "duplicate")
			continue
		}
		m.startReader(ctx, conn, st)

		select {
		case <-ctx.Done():
			Close(conn, st, "bye")
			m.removeByConn(conn)
			return
		case <-conn.Context().Done():
			m.removeByConn(conn)
			time.Sleep(300 * time.Millisecond)
		}
	}
}

func (m *Manager) dialOnce(ctx context.Context, addr string) (int, *quic.Conn, *quic.Stream, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, st, err := Dial(attemptCtx, addr, m.quicConf, openStreamTimeout)
	if err != nil {
		return 0, nil, nil, err
	}
	peerID, err := m.exchangeHello(st, true)
	if err != nil {
		Close(conn, st, "hello failed")
		return 0, nil, nil, err
	}
	return peerID, conn, st, nil
}

func (m *Manager) handleIncoming(ctx context.Context, conn *quic.Conn) {
	st, err := conn.AcceptStream(ctx)
	if err != nil {
		return
	}
	peerID, err := m.exchangeHello(st, false)
	if err != nil {
		Close(conn, st, "hello failed")
		return
	}
	if !m.register(peerID, conn, st) {
		Close(conn, st, "duplicate")
		return
	}
	m.startReader(ctx, conn, st)
	go func(c *quic.Conn) {
		<-c.Context().Done()
		m.removeByConn(c)
	}(conn)
}

func (m *Manager) startReader(ctx context.Context, conn *quic.Conn, st *quic.Stream) {
	go func() {
		_ = ReadFixedFrames(ctx, st, m.frameSize, func(frame []byte) {
			select {
			case m.incoming <- frame:
			case <-ctx.Done():
			}
		})
		m.removeByConn(conn)
	}()
}

func (m *Manager) register(id int, conn *quic.Conn, st *quic.Stream) bool {
	if id <= 0 || id == m.selfID {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.peers[id]; ok && existing != nil && existing.conn != nil {
		select {
		case <-existing.conn.Context().Done():
		default:
			return false
		}
	}
	m.peers[id] = &peer{conn: conn, stream: st}
	return true
}

func (m *Manager) hasPeer(id int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := m.peers[id]
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

func (m *Manager) removeByConn(conn *quic.Conn) {
	if conn == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.peers {
		if p != nil && p.conn == conn {
			delete(m.peers, id)
			return
		}
	}
}

func (m *Manager) exchangeHello(st *quic.Stream, outbound bool) (int, error) {
	if st == nil {
		return 0, fmt.Errorf("stream is nil")
	}
	if outbound {
		if err := writeHello(st, m.selfID, m.frameSize); err != nil {
			return 0, err
		}
		return readHello(st, m.frameSize)
	}
	peerID, err := readHello(st, m.frameSize)
	if err != nil {
		return 0, err
	}
	if err := writeHello(st, m.selfID, m.frameSize); err != nil {
		return 0, err
	}
	return peerID, nil
}

func readHello(st *quic.Stream, frameSize int) (int, error) {
	if frameSize <= 0 {
		frameSize = FrameSize
	}
	if d, ok := interface{}(st).(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = d.SetReadDeadline(time.Now().Add(helloTimeout))
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

func writeHello(st *quic.Stream, selfID int, frameSize int) error {
	if frameSize <= 0 {
		frameSize = FrameSize
	}
	frame := make([]byte, 8)
	binary.BigEndian.PutUint32(frame[0:4], helloMagic)
	binary.BigEndian.PutUint32(frame[4:8], uint32(selfID))
	_, err := WriteFixedFrame(st, frame, frameSize, helloTimeout)
	return err
}
