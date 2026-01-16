package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	// Adjust these imports to your actual module path:
	"elevator/common"
	"elevator/elevnetwork"

	quic "github.com/quic-go/quic-go"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ctrl+C
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		cancel()
	}()

	cfg := common.DefaultConfig()

	selfID, err := cfg.DetectSelfID()
	if err != nil {
		log.Fatalf("DetectSelfID: %v", err)
	}
	log.Printf("Self detected as elevator %d", selfID)

	peers, selfID, err := cfg.PeerAddrs()
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Configured peers: %v", peers)

	quicConf := &quic.Config{
		KeepAlivePeriod: 2 * time.Second,
		// (Defaults are fine otherwise for lab)
	}

	pm := elevnetwork.NewPeerManager(selfID, elevnetwork.QUIC_FRAME_SIZE)

	// Start listener on all interfaces.
	go func() {
		err := elevnetwork.ListenQUIC(ctx, cfg.ListenAddr(), quicConf, func(conn *quic.Conn) {
			pm.HandleIncomingConn(ctx, conn, func(from int, frame []byte) {
				// For demo, print incoming payload (trim zeros for readability).
				msg := string(trimZeros(frame))
				log.Printf("RECV from elev-%d: %q", from, msg)
			})
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("ListenQUIC error: %v", err)
			cancel()
		}
	}()

	// Dial rule: only dial peers with higher ID (prevents double connections).
	for peerID, peerAddr := range peers {
		if selfID < peerID {
			go func(id int, addr string) {
				// Retry loop for lab robustness.
				for ctx.Err() == nil {
					if err := pm.DialPeer(ctx, addr, quicConf); err == nil {
						log.Printf("Connected (dial) to elev-%d at %s", id, addr)
						return
					}
					time.Sleep(1 * time.Second)
				}
			}(peerID, peerAddr)
		}
	}

	// Ping loop: send every 5 seconds to ALL connected peers.
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("Shutting down")
			return
		case now := <-t.C:
			// Example: directed sends (what you asked for)
			// _ = pm.SendTo(1, []byte("hello 1"), 1*time.Second)

			payload := []byte(fmt.Sprintf("ping from elev-%d at %s", selfID, now.Format(time.RFC3339Nano)))

			// Send to all connected peers.
			pm.SendToAll(payload, 1*time.Second)

			log.Printf("SENT ping to peers currently connected: %v", pm.ConnectedPeerIDs())
		}
	}
}

func trimZeros(b []byte) []byte {
	// removes trailing 0 padding from fixed-size frames for printing
	i := len(b)
	for i > 0 && b[i-1] == 0 {
		i--
	}
	return b[:i]
}
