package elevnetwork

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math/big"
	"net"
	"time"

	quic "github.com/quic-go/quic-go"
)

const (
	QUIC_ALPN       = "networkmod-quic"
	QUIC_FRAME_SIZE = 1024 // fixed-size frames (stream framing, not datagrams)
)

func NewQUICServerTLSConfig() (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("rsa key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, fmt.Errorf("serial: %w", err)
	}

	certTmpl := &x509.Certificate{
		SerialNumber: serial,
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),

		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},

		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, certTmpl, certTmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create cert: %w", err)
	}

	cert := tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{QUIC_ALPN},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

func NewQUICClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{QUIC_ALPN},
		MinVersion:         tls.VersionTLS13,
	}
}

func ListenQUIC(
	ctx context.Context,
	listenAddr string,
	quicConf *quic.Config,
	connHandler func(conn *quic.Conn),
) error {
	tlsConf, err := NewQUICServerTLSConfig()
	if err != nil {
		return fmt.Errorf("server tls config: %w", err)
	}

	ln, err := quic.ListenAddr(listenAddr, tlsConf, quicConf)
	if err != nil {
		return fmt.Errorf("quic listen: %w", err)
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("quic accept: %w", err)
		}

		go connHandler(conn)
	}
}

func ReadFixedFramesQUIC(
	ctx context.Context,
	r io.Reader,
	frameSize int,
	handler func(frame []byte),
) error {
	if r == nil {
		return fmt.Errorf("reader is nil")
	}
	if frameSize <= 0 {
		frameSize = QUIC_FRAME_SIZE
	}

	buf := make([]byte, frameSize)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, err := io.ReadFull(r, buf)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return fmt.Errorf("read quic frame: %w", err)
		}

		frame := make([]byte, frameSize)
		copy(frame, buf)
		handler(frame)
	}
}

func WriteFixedFrameQUIC(
	w io.Writer,
	payload []byte,
	frameSize int,
	timeout time.Duration,
) (int, error) {
	if w == nil {
		return 0, fmt.Errorf("writer is nil")
	}
	if frameSize <= 0 {
		frameSize = QUIC_FRAME_SIZE
	}
	if len(payload) > frameSize {
		return 0, fmt.Errorf("payload too large: %d > %d", len(payload), frameSize)
	}

	frame := make([]byte, frameSize)
	copy(frame, payload) // zero-padding

	// If w supports deadlines (quic.Stream does), apply optional timeout.
	if d, ok := w.(interface{ SetWriteDeadline(time.Time) error }); ok && timeout > 0 {
		_ = d.SetWriteDeadline(time.Now().Add(timeout))
	}

	total := 0
	for total < frameSize {
		n, err := w.Write(frame[total:])
		total += n
		if err != nil {
			return total, fmt.Errorf("write quic frame: %w", err)
		}
		if n == 0 {
			return total, fmt.Errorf("write quic frame: wrote 0 bytes")
		}
	}
	return total, nil
}

type QUICSender struct {
	conn   *quic.Conn
	stream *quic.Stream
}

func NewQUICSender(
	ctx context.Context,
	remoteAddr string,
	quicConf *quic.Config,
	openStreamTimeout time.Duration,
) (*QUICSender, error) {
	tlsConf := NewQUICClientTLSConfig()

	conn, err := quic.DialAddr(ctx, remoteAddr, tlsConf, quicConf)
	if err != nil {
		return nil, fmt.Errorf("quic dial: %w", err)
	}

	stCtx := ctx
	var cancel context.CancelFunc
	if openStreamTimeout > 0 {
		stCtx, cancel = context.WithTimeout(ctx, openStreamTimeout)
		defer cancel()
	}

	stream, err := conn.OpenStreamSync(stCtx)
	if err != nil {
		_ = conn.CloseWithError(0, "open stream failed")
		return nil, fmt.Errorf("open stream: %w", err)
	}

	return &QUICSender{conn: conn, stream: stream}, nil
}

func (s *QUICSender) Close() error {
	if s == nil {
		return nil
	}
	if s.stream != nil {
		_ = s.stream.Close()
	}
	if s.conn != nil {
		return s.conn.CloseWithError(0, "bye")
	}
	return nil
}

func (s *QUICSender) Conn() *quic.Conn {
	if s == nil {
		return nil
	}
	return s.conn
}

func (s *QUICSender) Stream() *quic.Stream {
	if s == nil {
		return nil
	}
	return s.stream
}

func (s *QUICSender) LocalAddr() net.Addr {
	if s == nil || s.conn == nil {
		return nil
	}
	return s.conn.LocalAddr()
}

func (s *QUICSender) RemoteAddr() net.Addr {
	if s == nil || s.conn == nil {
		return nil
	}
	return s.conn.RemoteAddr()
}

func (s *QUICSender) SendFixed(payload []byte, frameSize int, timeout time.Duration) (int, error) {
	if s == nil || s.stream == nil {
		return 0, fmt.Errorf("quic sender is nil")
	}
	return WriteFixedFrameQUIC(s.stream, payload, frameSize, timeout)
}
