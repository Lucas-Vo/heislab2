package main

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"time"

	"elevator/common"
	"elevator/elevnetwork"
)

const INITIAL_CONTACT_TIMEOUT = 10 * time.Second

func networkThread(
	ctx context.Context,
	cfg common.Config,
	elevServicedCh <-chan common.Snapshot,
	elevRequestCh <-chan common.Snapshot,
	netSnap1Ch chan<- common.Snapshot,
	netSnap2Ch chan<- common.Snapshot,
) {
	selfKey := cfg.SelfKey
	pm, incomingFrames := elevnetwork.StartP2P(ctx, cfg)

	wv := elevnetwork.NewWorldView(pm, cfg)

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	contactTimer := time.NewTimer(INITIAL_CONTACT_TIMEOUT)
	defer contactTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case ns := <-elevRequestCh:
			if !wv.IsReady() {
				continue
			}
			wv.ApplyUpdate(selfKey, ns, elevnetwork.UpdateRequests)
			wv.BroadcastWorld(elevnetwork.UpdateRequests)

		case ns := <-elevServicedCh:
			if !wv.IsReady() {
				continue
			}
			wv.ApplyUpdate(selfKey, ns, elevnetwork.UpdateServiced)
			wv.BroadcastWorld(elevnetwork.UpdateServiced)

		// Incoming messages
		case in := <-incomingFrames:
			log.Printf("incoming framce received")
			var msg elevnetwork.NetMsg
			if err := json.Unmarshal(common.TrimZeros(in.Frame), &msg); err != nil {
				continue
			}

			fromKey := strconv.Itoa(in.FromID)
			if !wv.ShouldAcceptMsg(msg) {
				continue
			}

			wv.ApplyUpdate(fromKey, msg.Snapshot, msg.Kind)

			wv.RelayMsg(msg)

		case <-contactTimer.C:
			wv.ForceReady()

		// Publishing rule
		case <-ticker.C:
			if wv.IsCoherent() {
				wv.PublishWorld(netSnap1Ch)
				wv.PublishWorld(netSnap2Ch)
			}
		}
	}
}
