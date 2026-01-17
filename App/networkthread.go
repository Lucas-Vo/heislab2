// networkthread.go
package main

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"time"

	"elevator/common"
	"elevator/elevnetwork"

	quic "github.com/quic-go/quic-go"
)

type InFrame struct {
	from  int
	frame []byte
}

func networkthread(
	ctx context.Context,
	cfg common.Config,
	elevalgoServiced <-chan common.NetworkState,
	elevalgoLaManana <-chan common.NetworkState,
	networkStateOfTheWorld chan<- common.NetworkState,
	theWorldIsReady chan<- bool,
	snapshotToFSM chan<- common.NetworkState,
) {
	selfKey, peers, pm, incomingFrames := initp2p(cfg, ctx)

	wv := elevnetwork.NewWorldView(pm)

	// init seen-table (self + all peers)
	wv.MarkUnseen(selfKey)
	for id := range peers {
		wv.MarkUnseen(strconv.Itoa(id))
	}

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	snapshotSent := false

	for {
		select {
		case <-ctx.Done():
			return

		case ns := <-elevalgoLaManana:
			wv.ApplyUpdate(selfKey, ns, elevnetwork.UpdateNewRequests)
			wv.MarkReadyIfCoherent(theWorldIsReady)
			wv.PublishWorld(networkStateOfTheWorld)
			wv.Broadcast(ns)

		case ns := <-elevalgoServiced:
			wv.ApplyUpdate(selfKey, ns, elevnetwork.UpdateServiced)
			wv.MarkReadyIfCoherent(theWorldIsReady)
			wv.PublishWorld(networkStateOfTheWorld)
			wv.Broadcast(ns)

		case in := <-incomingFrames:
			var ns common.NetworkState
			if err := json.Unmarshal(common.TrimZeros(in.frame), &ns); err != nil {
				continue
			}
			fromKey := strconv.Itoa(in.from)

			wv.ApplyUpdate(fromKey, ns, elevnetwork.UpdateFromPeer)
			wv.MarkReadyIfCoherent(theWorldIsReady)
			wv.PublishWorld(networkStateOfTheWorld)

		case <-ticker.C:
			if !snapshotSent {
				snapshotSent = wv.MaybeSendSnapshotToFSM(snapshotToFSM)
			}
		}
	}
}

func initp2p(
	cfg common.Config,
	ctx context.Context,
) (myID string, myPeers map[int]string, peerManager *elevnetwork.PeerManager, incoming <-chan InFrame) {
	selfID, err := cfg.DetectSelfID()
	if err != nil {
		log.Fatalf("DetectSelfID: %v", err)
	}
	selfKey := strconv.Itoa(selfID)
	log.Printf("Self detected as elevator %d", selfID)

	peers, _, err := cfg.PeerAddrs()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Configured peers: %v", peers)

	quicConf := &quic.Config{
		KeepAlivePeriod: 2 * time.Second,
	}

	pm := elevnetwork.NewPeerManager(selfID, elevnetwork.QUIC_FRAME_SIZE)

	incomingFrames := make(chan InFrame, 64)

	// Listener
	go func() {
		err := elevnetwork.ListenQUIC(ctx, cfg.ListenAddr(), quicConf, func(conn *quic.Conn) {
			pm.HandleIncomingConn(ctx, conn, func(from int, frame []byte) {
				cp := make([]byte, len(frame))
				copy(cp, frame)
				select {
				case incomingFrames <- InFrame{from: from, frame: cp}:
				default:
					// drop if overloaded
				}
			})
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("ListenQUIC error: %v", err)
		}
	}()

	// Dial rule: only dial higher IDs
	for peerID, peerAddr := range peers {
		if selfID < peerID {
			go func(id int, addr string) {
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

	return selfKey, peers, pm, incomingFrames
}
