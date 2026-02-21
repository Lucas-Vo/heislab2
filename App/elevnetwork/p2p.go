package elevnetwork

import (
	"context"
	"elevator/common"
	"sync"
	"time"

	quic "github.com/quic-go/quic-go"
)

const (
	openStreamTimeout    = 2 * time.Second
	dialTimeout          = 4 * time.Second
	writeTimeout         = 150 * time.Millisecond
	incomingBufSize      = 128
	KeepAlivePeriod      = 2 * time.Second
	HandshakeIdleTimeout = 3 * time.Second
	MaxIdleTimeout       = 6 * time.Second
)

type Manager struct {
	frameSize int
	quicConf  *quic.Config
	mu        sync.RWMutex
	peers     map[string]*peer
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
		peers:    make(map[string]*peer),
		incoming: make(chan []byte, incomingBufSize),
	}
}

func (m *Manager) Start(ctx context.Context, cfg common.Config, port int) <-chan []byte {
	peers, selfID, err := cfg.PeerAddrsForPort(port)
	if err != nil {
		panic(err)
	}
	listenAddr := cfg.ListenAddrForPort(port)

	go m.listen(ctx, listenAddr)
	for peerID, peerAddr := range peers {
		if selfID < peerID {
			go m.dialLoop(ctx, peerAddr)
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
	_ = Listen(ctx, addr, m.quicConf, func(conn *quic.Conn) {
		m.handleIncoming(ctx, conn)
	})
}

func (m *Manager) dialLoop(ctx context.Context, addr string) {
	for ctx.Err() == nil {
		if m.hasPeer(addr) {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		conn, st, err := m.dialOnce(ctx, addr)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if !m.addPeer(addr, conn, st) {
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

func (m *Manager) dialOnce(ctx context.Context, addr string) (*quic.Conn, *quic.Stream, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	return Dial(attemptCtx, addr, m.quicConf, openStreamTimeout)
}

func (m *Manager) handleIncoming(ctx context.Context, conn *quic.Conn) {
	st, err := conn.AcceptStream(ctx)
	if err != nil {
		return
	}
	addr := conn.RemoteAddr().String()
	if !m.addPeer(addr, conn, st) {
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

func (m *Manager) addPeer(addr string, conn *quic.Conn, st *quic.Stream) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.peers[addr]; ok && existing != nil && existing.conn != nil {
		select {
		case <-existing.conn.Context().Done():
		default:
			return false
		}
	}
	m.peers[addr] = &peer{conn: conn, stream: st}
	return true
}

func (m *Manager) hasPeer(addr string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := m.peers[addr]
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
	for addr, p := range m.peers {
		if p != nil && p.conn == conn {
			delete(m.peers, addr)
			return
		}
	}
}
