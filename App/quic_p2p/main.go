package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	quic "github.com/quic-go/quic-go"
)

// ======== App message types (binary, simple, not compressed) ========

type MsgType uint8

const (
	MsgPing MsgType = 1
	MsgAck  MsgType = 2
)

// Ping payload (fixed size 1+4+4+8=17 bytes):
//   type(1) | id(4) | seq(4) | tsNanos(8)
type Ping struct {
	ID  uint32
	Seq uint32
	Ts  int64
}

// Ack payload (fixed size 1+4+4+8=17 bytes):
//   type(1) | id(4) | seq(4) | pingTsNanos(8)
type Ack struct {
	ID     uint32
	Seq    uint32
	PingTs int64
}

func encodePing(p Ping) []byte {
	b := make([]byte, 1+4+4+8)
	b[0] = byte(MsgPing)
	binary.BigEndian.PutUint32(b[1:5], p.ID)
	binary.BigEndian.PutUint32(b[5:9], p.Seq)
	binary.BigEndian.PutUint64(b[9:17], uint64(p.Ts))
	return b
}

func decodePing(b []byte) (Ping, bool) {
	if len(b) != 17 || MsgType(b[0]) != MsgPing {
		return Ping{}, false
	}
	return Ping{
		ID:  binary.BigEndian.Uint32(b[1:5]),
		Seq: binary.BigEndian.Uint32(b[5:9]),
		Ts:  int64(binary.BigEndian.Uint64(b[9:17])),
	}, true
}

func encodeAck(a Ack) []byte {
	b := make([]byte, 1+4+4+8)
	b[0] = byte(MsgAck)
	binary.BigEndian.PutUint32(b[1:5], a.ID)
	binary.BigEndian.PutUint32(b[5:9], a.Seq)
	binary.BigEndian.PutUint64(b[9:17], uint64(a.PingTs))
	return b
}

func decodeAck(b []byte) (Ack, bool) {
	if len(b) != 17 || MsgType(b[0]) != MsgAck {
		return Ack{}, false
	}
	return Ack{
		ID:     binary.BigEndian.Uint32(b[1:5]),
		Seq:    binary.BigEndian.Uint32(b[5:9]),
		PingTs: int64(binary.BigEndian.Uint64(b[9:17])),
	}, true
}

// ======== TLS (self-signed) ========
// QUIC requires TLS. For lab use we generate a self-signed cert at startup.
// Clients will skip verification (InsecureSkipVerify) for simplicity.

func generateTLSConfig() (*tls.Config, *tls.Config, error) {
	// server cert
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, nil, err
	}

	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"gruppe67-quic-p2p"},
	}
	clientTLS := &tls.Config{
		InsecureSkipVerify: true, // lab only
		NextProtos:         []string{"gruppe67-quic-p2p"},
	}
	return serverTLS, clientTLS, nil
}

// ======== Main ========

func parsePeers(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func randU32() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:])
}

func main() {
	listen := flag.String("listen", "0.0.0.0:4242", "listen addr ip:port (UDP port for QUIC)")
	peersCSV := flag.String("peers", "", "comma-separated peers ip:port to dial (e.g. 10.100.23.35:4242,10.100.23.36:4242)")
	interval := flag.Duration("interval", 1*time.Second, "ping interval")
	flag.Parse()

	serverTLS, clientTLS, err := generateTLSConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "TLS config error:", err)
		os.Exit(1)
	}

	id := randU32()
	var seq uint32 = 0
	fmt.Printf("NodeID: %d (0x%08x)\n", id, id)

	// Start listener
	listener, err := quic.ListenAddr(*listen, serverTLS, &quic.Config{
		EnableDatagrams: true,
		// For harsh network tests, you can tweak timeouts later.
		// KeepAlivePeriod: 2 * time.Second,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Listen error:", err)
		os.Exit(1)
	}
	fmt.Println("Listening on:", *listen)

	// Accept incoming connections in background
	ctx := context.Background()
	go func() {
		for {
			conn, err := listener.Accept(ctx)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Accept error:", err)
				return
			}
			fmt.Println("Accepted:", conn.RemoteAddr())
			go receiveLoop(conn, id)
		}
	}()

	// Dial peers
	peers := parsePeers(*peersCSV)
	for _, p := range peers {
		go func(addr string) {
			for {
				conn, err := quic.DialAddr(ctx, addr, clientTLS, &quic.Config{
					EnableDatagrams: true,
				})
				if err != nil {
					fmt.Fprintln(os.Stderr, "Dial failed to", addr, ":", err)
					time.Sleep(1 * time.Second)
					continue
				}
				fmt.Println("Dialed:", addr)
				go receiveLoop(conn, id)
				sendLoop(conn, id, &seq, *interval)
				// If sendLoop exits, connection died; retry
				time.Sleep(1 * time.Second)
			}
		}(p)
	}

	// If no peers, just keep running as a listener
	select {}
}

func sendLoop(conn *quic.Conn, id uint32, seq *uint32, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()

	for range t.C {
		*seq++
		p := Ping{ID: id, Seq: *seq, Ts: time.Now().UnixNano()}
		if err := conn.SendDatagram(encodePing(p)); err != nil {
			fmt.Fprintln(os.Stderr, "SendDatagram error (connection likely dead):", err)
			_ = conn.CloseWithError(0, "send error")
			return
		}
	}
}

func receiveLoop(conn *quic.Conn, myID uint32) {
	for {
		b, err := conn.ReceiveDatagram(context.Background())
		if err != nil {
			// connection closed / timed out
			return
		}
		// Try Ping
		if p, ok := decodePing(b); ok {
			// Reply with Ack to sender
			a := Ack{ID: p.ID, Seq: p.Seq, PingTs: p.Ts}
			_ = conn.SendDatagram(encodeAck(a))
			continue
		}
		// Try Ack
		if a, ok := decodeAck(b); ok {
			// RTT estimate: only meaningful if PingTs came from us originally
			rtt := time.Since(time.Unix(0, a.PingTs))
			// a.ID is the original pinger ID
			if a.ID == myID {
				fmt.Printf("Ack for my ping seq=%d rtt~=%s from %s\n", a.Seq, rtt, conn.RemoteAddr())
			} else {
				fmt.Printf("Ack observed for nodeID=%d seq=%d from %s\n", a.ID, a.Seq, conn.RemoteAddr())
			}
			continue
		}
	}
}
