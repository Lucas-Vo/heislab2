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

	pm, incomingPacket := elevnetwork.StartP2P(ctx, cfg, 4242)

	wv := elevnetwork.NewWorldView(pm, cfg)

	wv.Relay(elevnetwork.NetMsg{
		Origin:  selfKey,
		Counter: 0,
		Snapshot: common.Snapshot{
			UpdateKind: common.UpdateRequests,
			States:     make(map[string]common.ElevState),
		},
	}) // Send an initial empty msg to prompt others to respond with their state, so we can populate our world view faster. This also starts the contact timer, which will force us ready after a timeout if we don't get any responses.

	//TODO: Create a separate send nothing struct function
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
			wv.SelfAlive = true
			elevatorErrorTimer.Reset(4 * time.Second)

			wv.ApplyUpdate(selfKey, ns, ns.UpdateKind)
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

			becameReady := wv.ApplyUpdate(msg.Origin, msg.Snapshot, msg.Snapshot.UpdateKind)

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
			if wv.IsCoherent() || !wv.SelfAlive {
				log.Printf("IS COHERENT JJJJJJJJJJJJJJJJJJJJJJJJJJJ")
				wv.PublishWorld(netSnap1Ch) //TODO: Move this auta tha case <-elevatorErrorTimer.c block cuzzz ya don nned that if we take this a  naturale in tha ticker
				wv.PublishWorld(netSnap2Ch)
			}
		case <-elevatorErrorTimer.C:
			if wv.SnapshotCopy().States[selfKey].Behavior != "idle" { //TODO: why do we have "EB_Idle" as well as "idle"????? maybe we should just have "idle" and then have the assigner decide when to switch to "EB_Idle" based on the snapshot?
				wv.SelfAlive = false // Stop until next behavior change
				log.Printf("No behavior change detected for 4 seconds, marking Elevator as stale")
			} else {
				elevatorErrorTimer.Reset(4 * time.Second)
				wv.SelfAlive = true
			}
		}
	}
}
