// networkthread.go
package main

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"elevator/common"
	"elevator/elevnetwork"
)

func networkThread(
	ctx context.Context,
	cfg common.Config,
	elevalgoServiced <-chan common.NetworkState,
	elevalgoLaManana <-chan common.NetworkState,
	networkStateOfTheWorld chan<- common.NetworkState,
	theWorldIsReady chan<- bool,
	snapshotToFSM chan<- common.NetworkState,
) {

	selfKey := cfg.SelfKey
	peers, pm, incomingFrames := elevnetwork.StartP2P(ctx, cfg)

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
			wv.ApplyUpdateAndPublish(selfKey, ns, elevnetwork.UpdateNewRequests, theWorldIsReady, networkStateOfTheWorld)
			wv.Broadcast(ns)

		case ns := <-elevalgoServiced:
			wv.ApplyUpdateAndPublish(selfKey, ns, elevnetwork.UpdateServiced, theWorldIsReady, networkStateOfTheWorld)
			wv.Broadcast(ns)

		case in := <-incomingFrames:
			var ns common.NetworkState
			if err := json.Unmarshal(common.TrimZeros(in.Frame), &ns); err != nil {
				continue
			}
			fromKey := strconv.Itoa(in.FromID)

			wv.ApplyUpdateAndPublish(fromKey, ns, elevnetwork.UpdateFromPeer, theWorldIsReady, networkStateOfTheWorld)

		case <-ticker.C:
			if !snapshotSent {
				snapshotSent = wv.MaybeSendSnapshotToFSM(snapshotToFSM)
			}
		}
	}
}
