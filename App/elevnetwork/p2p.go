package elevnetwork

import (
	"context"
	"elevator/common"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	quic "github.com/quic-go/quic-go"
)

const (
	dialTimeout          = 4 * time.Second
	incomingBufSize      = 128
	KeepAlivePeriod      = 4 * time.Second
	HandshakeIdleTimeout = 6 * time.Second
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
	addr string
	conn *quic.Conn
}

func NewPeerManager() *Manager {
	return &Manager{
		frameSize: FrameSize,
		quicConf: &quic.Config{
			KeepAlivePeriod:      KeepAlivePeriod,
			HandshakeIdleTimeout: HandshakeIdleTimeout,
			MaxIdleTimeout:       MaxIdleTimeout,
			EnableDatagrams:      true,
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
	listenAddr := fmt.Sprintf(":%d", port)

	go m.listen(ctx, listenAddr)
	for peerID, peerAddr := range peers {
		if shouldDialPeer(selfID, peerID) {
			go m.dialLoop(ctx, peerAddr)
		}
	}
	return m.incoming
}

func shouldDialPeer(selfID int, peerID int) bool {
	switch selfID {
	case 1:
		return peerID == 2
	case 2:
		return peerID == 3
	case 3:
		return peerID == 1
	default:
		return selfID < peerID
	}
}

func (m *Manager) Broadcast(payload []byte) {
	m.mu.RLock()
	peers := make([]*peer, 0, len(m.peers))
	for _, p := range m.peers {
		if p != nil && p.conn != nil {
			peers = append(peers, p)
		}
	}
	m.mu.RUnlock()
	for _, p := range peers {
		if err := WriteDatagram(p.conn, payload, m.frameSize); err != nil {
			if strings.Contains(err.Error(), "datagram support disabled") {
				cs := p.conn.ConnectionState()
				log.Printf(
					"p2p datagram disabled peer=%s local=%v remote=%v",
					p.addr,
					cs.SupportsDatagrams.Local,
					cs.SupportsDatagrams.Remote,
				)
			}
			log.Printf("p2p broadcast write failed peer=%s: %v", p.addr, err)
		}
	}
	log.Printf("BROADCAST")
}

func (m *Manager) listen(ctx context.Context, addr string) {
	if err := Listen(ctx, addr, m.quicConf, func(conn *quic.Conn) {
		m.handleIncoming(ctx, conn)
	}); err != nil && ctx.Err() == nil {
		log.Printf("p2p listen failed addr=%s: %v", addr, err)
	}
}

func (m *Manager) dialLoop(ctx context.Context, addr string) {
	for ctx.Err() == nil {
		if m.hasPeer(addr) {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		conn, err := m.dialOnce(ctx, addr)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("p2p dial failed addr=%s: %v", addr, err)
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}
		replaced := m.addPeer(addr, conn)
		if replaced != nil {
			log.Printf("p2p replacing existing connection peer=%s", normalizePeerAddr(addr))
			Close(replaced, "replaced")
		}
		m.startReader(ctx, conn)

		select {
		case <-ctx.Done():
			Close(conn, "bye")
			m.removeByConn(conn)
			return
		case <-conn.Context().Done():
			m.removeByConn(conn)
			time.Sleep(300 * time.Millisecond)
		}
	}
}

func (m *Manager) dialOnce(ctx context.Context, addr string) (*quic.Conn, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	return Dial(attemptCtx, addr, m.quicConf)
}

func (m *Manager) handleIncoming(ctx context.Context, conn *quic.Conn) {
	addr := conn.RemoteAddr().String()
	replaced := m.addPeer(addr, conn)
	if replaced != nil {
		log.Printf("p2p replacing existing connection peer=%s", normalizePeerAddr(addr))
		Close(replaced, "replaced")
	}
	m.startReader(ctx, conn)
	go func(c *quic.Conn) {
		<-c.Context().Done()
		m.removeByConn(c)
	}(conn)
}

func (m *Manager) startReader(ctx context.Context, conn *quic.Conn) {
	go func() {
		if err := ReadDatagrams(ctx, conn, m.frameSize, func(frame []byte) {
			select {
			case m.incoming <- frame:
			case <-ctx.Done():
			}
		}); err != nil && ctx.Err() == nil {
			log.Printf("p2p read failed remote=%s: %v", conn.RemoteAddr().String(), err)
		}
		m.removeByConn(conn)
	}()
}

func (m *Manager) addPeer(addr string, conn *quic.Conn) *quic.Conn {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := normalizePeerAddr(addr)
	var replaced *quic.Conn
	if existing, ok := m.peers[key]; ok && existing != nil && existing.conn != nil {
		select {
		case <-existing.conn.Context().Done():
		default:
			replaced = existing.conn
		}
	}
	m.peers[key] = &peer{addr: key, conn: conn}
	return replaced
}

func (m *Manager) hasPeer(addr string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := m.peers[normalizePeerAddr(addr)]
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

func normalizePeerAddr(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
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
