package elevnetwork

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
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
	return &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{ALPN},
		MinVersion:         tls.VersionTLS13,
	}
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

func Dial(ctx context.Context, addr string, conf *quic.Config) (*quic.Conn, error) {
	conn, err := quic.DialAddr(ctx, addr, ClientTLSConfig(), conf)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	return conn, nil
}

func ReadDatagrams(ctx context.Context, conn *quic.Conn, frameSize int, handler func([]byte)) error {
	if conn == nil {
		return fmt.Errorf("conn is nil")
	}
	for {
		dgram, err := conn.ReceiveDatagram(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if frameSize > 0 && len(dgram) > frameSize {
			continue
		}
		frame := make([]byte, len(dgram))
		copy(frame, dgram)
		handler(frame)
	}
}

func WriteDatagram(conn *quic.Conn, payload []byte, frameSize int) error {
	if conn == nil {
		return fmt.Errorf("conn is nil")
	}
	if frameSize <= 0 {
		frameSize = FrameSize
	}
	if len(payload) > frameSize {
		return fmt.Errorf("payload too large: %d > %d", len(payload), frameSize)
	}
	return conn.SendDatagram(payload)
}

func Close(conn *quic.Conn, reason string) {
	if conn == nil {
		return
	}
	if reason == "" {
		reason = "bye"
	}
	_ = conn.CloseWithError(0, reason)
}
