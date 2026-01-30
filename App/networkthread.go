package main

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"elevator/common"
	"elevator/elevnetwork"
)

const INITIAL_CONTACT_TIMEOUT = 5 * time.Second

func networkThread(
	ctx context.Context,
	cfg common.Config,
	fsmServicedCh <-chan common.NetworkState,
	fsmUpdateCh <-chan common.NetworkState,
	networkWorldViewAssignerCh chan<- common.NetworkState,
	networkWorldViewFSMCh chan<- common.NetworkState,
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

		// Local FSM updates (blocked until initial contact)
		case ns := <-fsmUpdateCh:
			if !wv.IsReady() {
				continue
			}
			wv.ApplyUpdate(selfKey, ns, elevnetwork.UpdateNewRequests)
			wv.BroadcastWorld(elevnetwork.UpdateNewRequests)

		case ns := <-fsmServicedCh:
			if !wv.IsReady() {
				continue
			}
			wv.ApplyUpdate(selfKey, ns, elevnetwork.UpdateServiced)
			wv.BroadcastWorld(elevnetwork.UpdateServiced)

		// Incoming messages
		case in := <-incomingFrames:
			var msg elevnetwork.NetMsg
			if err := json.Unmarshal(common.TrimZeros(in.Frame), &msg); err != nil {
				continue
			}

			fromKey := strconv.Itoa(in.FromID)
			// Dedupe
			if !wv.ShouldAcceptMsg(msg) {
				continue
			}

			// Apply (this will also mark readiness if fromKey != self)
			wv.ApplyUpdate(fromKey, msg.State, msg.Kind)

			wv.RelayMsg(msg)

		// Readiness timeout: allow local operation even if no peer was heard
		case <-contactTimer.C:
			wv.ForceReady()

		// Publishing rule
		case <-ticker.C:
			// Only publish when coherent among alive peers.
			// (You can choose to also require IsReady(), but coherency check already covers "have data".)
			if wv.IsCoherent() {
				wv.PublishWorld(networkWorldViewAssignerCh)
				wv.PublishWorld(networkWorldViewFSMCh)
			}
		}
	}
}
