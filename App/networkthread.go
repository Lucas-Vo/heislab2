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
	elevUpdateCh <-chan common.Snapshot,
	netSnap1Ch chan<- common.Snapshot,
	netSnap2Ch chan<- common.Snapshot,
) {
	// merge serviced and request channels
	selfKey := cfg.SelfKey

	pm := &elevnetwork.PeerManager{}
	pm.NewPeerManager(cfg)
	incomingPacket := pm.StartP2P(ctx, cfg, 4242)

	wv := elevnetwork.NewWorldView(pm, cfg)

	wv.Relay(elevnetwork.MakeEmptyNetMsg(selfKey, common.UpdateRequests)) // Send an initial empty msg to prompt others to respond with their state, so we can populate our world view faster. This also starts the contact timer, which will force us ready after a timeout if we don't get any responses.

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

		case ns := <-elevUpdateCh:
			wv.SetSelfAlive(true)
			elevatorErrorTimer.Reset(4 * time.Second)

			wv.ApplyUpdate(selfKey, ns)
			if ns.UpdateKind == common.UpdateRequests {
				if !wv.IsReady() {
					continue
				}
			}
			wv.Broadcast(ns.UpdateKind)

		case in := <-incomingPacket:
			var msg elevnetwork.NetMsg
			if err := json.Unmarshal(common.TrimZeros(in), &msg); err != nil {
				continue
			}

			if !wv.ShouldAcceptMsg(msg) {
				continue
			}

			becameReady := wv.ApplyUpdate(msg.Origin, msg.Snapshot)

			if msg.Snapshot.UpdateKind == common.UpdateRequests && becameReady {
				wv.PublishWorld(netSnap2Ch)
			}

			wv.Relay(msg)

		case <-contactTimer.C:
			log.Printf("networkThread: initial contact timeout; forcing ready")
			wv.ForceReady()

		case <-ticker.C:
			// Periodically broadcast state
			if !wv.IsReady() {
				continue
			}
			wv.Broadcast(common.UpdateRequests)

			// Publish to Assigner and Elevator Control
			// Publishing will be handled when elevator liveness changes (see elevatorErrorTimer.C)
		case <-elevatorErrorTimer.C:
			// Re-evaluate elevator liveness and notify other components when it changes.
			if wv.Snapshot().States[selfKey].Behavior != "idle" { //TODO: why do we have "EB_Idle" as well as "idle"????? maybe we should just have "idle" and then have the assigner decide when to switch to "EB_Idle" based on the snapshot?
				if wv.IsSelfAlive() {
					// Transition from alive -> stale
					wv.SetSelfAlive(false)
					log.Printf("No behavior change detected for 4 seconds, marking Elevator as stale")
					// Notify assigner and elevator control of the updated world view
					wv.PublishWorld(netSnap1Ch)
					wv.PublishWorld(netSnap2Ch)
				}
			} else {
				// Behavior is idle; keep or restore alive state and reset timer
				if !wv.IsSelfAlive() {
					// Transition from stale -> alive
					wv.SetSelfAlive(true)
					wv.PublishWorld(netSnap1Ch)
					wv.PublishWorld(netSnap2Ch)
				}
				elevatorErrorTimer.Reset(4 * time.Second)
			}
		}
	}
}
