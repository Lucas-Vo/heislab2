// networkthread.go
package main

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"elevator/common"
	"elevator/elevnetwork"
)

const INITIAL_CONTACT_TIMEOUT = 3 * time.Second

func networkThread(
	ctx context.Context,
	cfg common.Config,
	elevServicedCh <-chan common.Snapshot,
	elevRequestCh <-chan common.Snapshot,
	netSnap1Ch chan<- common.Snapshot,
	netSnap2Ch chan<- common.Snapshot,
) {
	selfKey := cfg.SelfKey

	pmReq, incomingReq := elevnetwork.StartP2P(ctx, cfg, 4242)
	pmSvc, incomingSvc := elevnetwork.StartP2P(ctx, cfg, 4243)

	tx := elevnetwork.NewMuxTransport(pmReq, pmSvc)
	wv := elevnetwork.NewWorldView(tx, cfg)

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	contactTimer := time.NewTimer(INITIAL_CONTACT_TIMEOUT)
	defer contactTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case ns := <-elevRequestCh:
			wv.ApplyUpdate(selfKey, ns, elevnetwork.UpdateRequests)
			if wv.IsReady() {
				wv.Broadcast(elevnetwork.UpdateRequests)
			}

		case ns := <-elevServicedCh:
			wv.ApplyUpdate(selfKey, ns, elevnetwork.UpdateServiced)
			if wv.IsReady() {
				wv.Broadcast(elevnetwork.UpdateServiced)
			}

		case in := <-incomingReq:
			var msg elevnetwork.NetMsg
			if err := json.Unmarshal(common.TrimZeros(in), &msg); err != nil {
				continue
			}

			if !wv.ShouldAcceptMsg(msg) {
				continue
			}

			wv.ApplyUpdate(msg.Origin, msg.Snapshot, elevnetwork.UpdateRequests)
			wv.Relay(elevnetwork.UpdateRequests, msg)

		case in := <-incomingSvc:
			var msg elevnetwork.NetMsg
			if err := json.Unmarshal(common.TrimZeros(in), &msg); err != nil {
				continue
			}

			if !wv.ShouldAcceptMsg(msg) {
				continue
			}

			wv.ApplyUpdate(msg.Origin, msg.Snapshot, elevnetwork.UpdateServiced)
			wv.Relay(elevnetwork.UpdateServiced, msg)

		case <-contactTimer.C:
			wv.ForceReady()

		case <-ticker.C:
			// Periodically broadcast state
			wv.Broadcast(elevnetwork.UpdateRequests)

			// Publish to Assigner and Elevator Control
			if wv.IsCoherent() {
				snap := wv.SnapshotCopy()
				if len(snap.States) == 0 {
					log.Printf("networkThread: coherent snapshot has no states; withholding publish")
					break
				}
				wv.PublishWorld(netSnap1Ch)
				wv.PublishWorld(netSnap2Ch)
			}
		}
	}
}
