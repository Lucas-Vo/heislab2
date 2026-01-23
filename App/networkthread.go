// from incomingFrames   -> external lucas got updates
// from fsmUpdateCh -> filip has a new local request
// from fsmServicedCh -> filip has finished a request

// TODO:
// - make a smart alg for detecting when filip data is stale
// - rewrite ready logic, should only publish when worldview is coherent
// - verify that when you start existing again/reconnect that the last snapshot is uploaded

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
	fsmServicedCh <-chan common.NetworkState,
	fsmUpdateCh <-chan common.NetworkState,
	networkWorldViewAssignerCh chan<- common.NetworkState,
	networkWorldViewFSMCh chan<- common.NetworkState,
) {

	selfKey := cfg.SelfKey
	pm, incomingFrames := elevnetwork.StartP2P(ctx, cfg)

	wv := elevnetwork.NewWorldView(pm, cfg)
	wv.ExpectPeer(selfKey)

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case ns := <-fsmUpdateCh:
			wv.ApplyUpdateAndPublish(selfKey, ns, elevnetwork.UpdateNewRequests, networkWorldViewAssignerCh)
			wv.BroadcastLocal(elevnetwork.UpdateNewRequests, ns)

		case ns := <-fsmServicedCh:
			wv.ApplyUpdateAndPublish(selfKey, ns, elevnetwork.UpdateServiced, networkWorldViewAssignerCh)
			wv.BroadcastLocal(elevnetwork.UpdateServiced, ns)

		case in := <-incomingFrames:
			var msg elevnetwork.NetMsg
			if err := json.Unmarshal(common.TrimZeros(in.Frame), &msg); err != nil {
				continue
			}

			fromKey := strconv.Itoa(in.FromID)

			// Dynamic membership
			wv.ExpectPeer(fromKey)

			// Dedupe (prevents loops), then apply, then forward unchanged
			if !wv.ShouldAcceptMsg(msg) {
				continue
			}

			wv.ApplyUpdateAndPublish(fromKey, msg.State, msg.Kind, networkWorldViewAssignerCh)

			// Forward so 1->2->3 works even without 1-3
			wv.BroadcastMsg(msg)

		case <-ticker.C:
			// Only publish outward once the world is coherent/ready.
			if wv.IsReady() {
				wv.PublishWorld(networkWorldViewAssignerCh)
				wv.PublishWorld(networkWorldViewFSMCh)
			}
		}
	}
}
