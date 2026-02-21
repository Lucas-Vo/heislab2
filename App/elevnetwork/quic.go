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
	"time"

	quic "github.com/quic-go/quic-go"
)

const (
	ALPN      = "networkmod-quic"
	FrameSize = 1024
)

func ServerTLSConfig() (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("rsa key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, fmt.Errorf("serial: %w", err)
	}
	cert := &x509.Certificate{
		SerialNumber:          serial,
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create cert: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		NextProtos:   []string{ALPN},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

func ClientTLSConfig() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true, NextProtos: []string{ALPN}, MinVersion: tls.VersionTLS13}
}

func Listen(ctx context.Context, addr string, conf *quic.Config, onConn func(*quic.Conn)) error {
	tlsConf, err := ServerTLSConfig()
	if err != nil {
		return err
	}
	ln, err := quic.ListenAddr(addr, tlsConf, conf)
	if err != nil {
		return err
	}
	defer ln.Close()
	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go onConn(conn)
	}
}

func Dial(ctx context.Context, addr string, conf *quic.Config, openTimeout time.Duration) (*quic.Conn, *quic.Stream, error) {
	conn, err := quic.DialAddr(ctx, addr, ClientTLSConfig(), conf)
	if err != nil {
		return nil, nil, fmt.Errorf("dial: %w", err)
	}
	stCtx := ctx
	var cancel context.CancelFunc
	if openTimeout > 0 {
		stCtx, cancel = context.WithTimeout(ctx, openTimeout)
		defer cancel()
	}
	st, err := conn.OpenStreamSync(stCtx)
	if err != nil {
		_ = conn.CloseWithError(0, "open stream failed")
		return nil, nil, fmt.Errorf("open stream: %w", err)
	}
	return conn, st, nil
}

func ReadFixedFrames(ctx context.Context, r io.Reader, frameSize int, handler func([]byte)) error {
	if r == nil {
		return fmt.Errorf("reader is nil")
	}
	if frameSize <= 0 {
		frameSize = FrameSize
	}
	buf := make([]byte, frameSize)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if _, err := io.ReadFull(r, buf); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}
		frame := make([]byte, frameSize)
		copy(frame, buf)
		handler(frame)
	}
}

func WriteFixedFrame(w io.Writer, payload []byte, frameSize int, timeout time.Duration) (int, error) {
	if w == nil {
		return 0, fmt.Errorf("writer is nil")
	}
	if frameSize <= 0 {
		frameSize = FrameSize
	}
	if len(payload) > frameSize {
		return 0, fmt.Errorf("payload too large: %d > %d", len(payload), frameSize)
	}
	frame := make([]byte, frameSize)
	copy(frame, payload)
	if d, ok := w.(interface{ SetWriteDeadline(time.Time) error }); ok && timeout > 0 {
		_ = d.SetWriteDeadline(time.Now().Add(timeout))
	}
	written := 0
	for written < frameSize {
		n, err := w.Write(frame[written:])
		written += n
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, fmt.Errorf("write 0 bytes")
		}
	}
	return written, nil
}

func Close(conn *quic.Conn, stream *quic.Stream, reason string) {
	if stream != nil {
		_ = stream.Close()
	}
	if conn == nil {
		return
	}
	if reason == "" {
		reason = "bye"
	}
	_ = conn.CloseWithError(0, reason)
}
