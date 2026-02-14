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

	for {
		select {
		case <-ctx.Done():
			return

		case ns := <-elevRequestCh:
			if st, ok := ns.States[selfKey]; !ok || st.Behavior == "" {
				log.Printf("networkThread: elevRequestCh missing/empty self state (ok=%v behavior=%q floor=%d dir=%q)", ok, st.Behavior, st.Floor, st.Direction)
			}
			wv.ApplyUpdate(selfKey, ns, elevnetwork.UpdateRequests)
			wv.Broadcast(elevnetwork.UpdateRequests)

		case ns := <-elevServicedCh:
			if st, ok := ns.States[selfKey]; !ok || st.Behavior == "" {
				log.Printf("networkThread: elevServicedCh missing/empty self state (ok=%v behavior=%q floor=%d dir=%q)", ok, st.Behavior, st.Floor, st.Direction)
			}
			wv.ApplyUpdate(selfKey, ns, elevnetwork.UpdateServiced)
			wv.Broadcast(elevnetwork.UpdateServiced)

		case in := <-incomingReq:
			var msg elevnetwork.NetMsg
			if err := json.Unmarshal(common.TrimZeros(in), &msg); err != nil {
				continue
			}

			if !wv.ShouldAcceptMsg(msg) {
				continue
			}

			becameReady := wv.ApplyUpdate(msg.Origin, msg.Snapshot, elevnetwork.UpdateRequests)
			if becameReady {
				wv.PublishWorld(netSnap1Ch)
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
			wv.PublishWorld(netSnap1Ch)
			wv.PublishWorld(netSnap2Ch)

		case <-ticker.C:
			// Periodically broadcast state
			wv.Broadcast(elevnetwork.UpdateRequests)

			// Publish to Assigner and Elevator Control
			if wv.IsCoherent() && wv.IsReady() {
				wv.PublishWorld(netSnap1Ch)
				wv.PublishWorld(netSnap2Ch)
			}
		}
	}
}
