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

const INITIAL_CONTACT_TIMEOUT = 8 * time.Second

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

	elevatorErrorTimer := time.NewTimer(4 * time.Second)
	defer elevatorErrorTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case ns := <-elevRequestCh:
			wv.ApplyUpdate(selfKey, ns, elevnetwork.UpdateRequests)
			if !wv.IsReady() {
				continue
			}
			wv.SelfAlive = true
			elevatorErrorTimer.Reset(4 * time.Second)
			wv.Broadcast(elevnetwork.UpdateRequests)

		case ns := <-elevServicedCh:
			wv.ApplyUpdate(selfKey, ns, elevnetwork.UpdateServiced)
			wv.SelfAlive = true
			elevatorErrorTimer.Reset(4 * time.Second)

			wv.Broadcast(elevnetwork.UpdateServiced)

		case in := <-incomingReq:
			var msg elevnetwork.NetMsg
			if err := json.Unmarshal(common.TrimZeros(in), &msg); err != nil {
				continue
			}

			if !wv.ShouldAcceptMsg(msg) {
				continue
			}
			log.Printf("Cabs from other %v, that comes from peer nr (%v)", msg.Snapshot.States[msg.Origin].CabRequests, msg.Origin)

			becameReady := wv.ApplyUpdate(msg.Origin, msg.Snapshot, elevnetwork.UpdateRequests)
			if becameReady {
				// wv.PublishWorld(netSnap1Ch)
				wv.PublishWorld(netSnap2Ch)
			}
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
			log.Printf("networkThread: initial contact timeout; forcing ready")
			wv.ForceReady()

		case <-ticker.C:
			// Periodically broadcast state
			if !wv.IsReady() {
				continue
			}
			wv.Broadcast(elevnetwork.UpdateRequests)

			// Publish to Assigner and Elevator Control
			if wv.IsCoherent() {
				wv.PublishWorld(netSnap1Ch)
				wv.PublishWorld(netSnap2Ch)
			}
		case <-elevatorErrorTimer.C:
			log.Printf("No behavior change detected for 4 seconds, marking Elevator as stale")
			if wv.SnapshotCopy().States[selfKey].Behavior != "idle" {
				wv.SelfAlive = false // Stop until next behavior change
			}
		}
	}
}
