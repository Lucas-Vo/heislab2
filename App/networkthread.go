// networkthread.go
package main

import (
	"context"
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
	selfKey := cfg.SelfKey

	wv, incoming := elevnetwork.NewWorldView(ctx, cfg, 4242)

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
			wv.MergeLocal(ns)

		case frame := <-incoming:
			kind, becameReady := wv.MergeRemote(frame)
			if kind == common.UpdateRequests && becameReady { // todo, will cab recovery work without these lines?
				wv.PublishAll(netSnap1Ch, netSnap2Ch)
			}

		case <-contactTimer.C:
			log.Printf("networkThread: forcing ready")
			wv.ForceReady()

		case <-ticker.C:
			wv.BroadcastRequests()
			if wv.Ready() {
				wv.PublishAll(netSnap1Ch, netSnap2Ch)
			}

		case <-elevatorErrorTimer.C:
			snap := wv.GetSnapshot()
			if snap.States[selfKey].Behavior != "idle" {
				if wv.SelfAlive() {
					wv.SetSelfAlive(false)
					log.Printf("No behavior change detected for 4 seconds, marking Elevator as stale")
					wv.PublishAll(netSnap1Ch, netSnap2Ch)
				}
			} else {
				if !wv.SelfAlive() {
					wv.SetSelfAlive(true)
					wv.PublishAll(netSnap1Ch, netSnap2Ch)
				}
				elevatorErrorTimer.Reset(4 * time.Second)
			}
		}
	}
}
