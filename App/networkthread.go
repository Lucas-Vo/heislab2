// networkthread.go
package main

import (
	"context"
	"log"
	"time"

	"elevator/common"
	"elevator/elevnetwork"
)

const (
	INITIAL_CONTACT_TIMEOUT = 5 * time.Second
)

func networkThread(
	ctx context.Context,
	cfg common.Config,
	elevUpdateCh <-chan common.Snapshot,
	netSnap1Ch chan<- common.Snapshot,
	netSnap2Ch chan<- common.Snapshot,
) {
	selfKey := cfg.SelfKey

	wv, incoming := elevnetwork.InitWorldView(ctx, cfg, 4242)

	localTicker := time.NewTicker(100 * time.Millisecond)
	defer localTicker.Stop()

	broadcastTicker := time.NewTicker(2 * time.Second)
	defer broadcastTicker.Stop()

	contactTimer := time.NewTimer(INITIAL_CONTACT_TIMEOUT)
	defer contactTimer.Stop()

	elevatorErrorTimer := time.NewTimer(4 * time.Second)
	defer elevatorErrorTimer.Stop()

	for {
		select {
		case ns := <-elevUpdateCh:
			wv.SetSelfAlive(true)
			elevatorErrorTimer.Reset(4 * time.Second)
			if ns.UpdateKind == common.UpdateServiced {
				wv.TrackLocallyServicedHallRequests(ns, time.Now())
			}
			wv.MergeLocal(ns)
			wv.PublishLocally(netSnap1Ch, netSnap2Ch)

		case frame := <-incoming:
			frameToMerge, servicedHall, hasRecentlyServiced := wv.SuppressRecentlyServicedFromFrame(frame, time.Now())
			if hasRecentlyServiced {
				wv.ResendServicedHallRequests(servicedHall)
			}
			wv.MergeRemote(frameToMerge)
			wv.PublishLocally(netSnap1Ch, netSnap2Ch)

		case <-contactTimer.C:
			log.Printf("networkThread: forcing ready")
			wv.ForceReady()

		case <-localTicker.C:
			if !wv.SnapshotsAreCoherent() {
				wv.BroadcastRequests()
			}
			if wv.Ready() {
				wv.PublishLocally(netSnap1Ch, netSnap2Ch)
			}

		case <-broadcastTicker.C:
			wv.BroadcastRequests()

		case <-elevatorErrorTimer.C:
			snap := wv.GetSnapshot()
			if snap.States[selfKey].Behavior != "idle" {
				if wv.SelfAlive() {
					wv.SetSelfAlive(false)
					log.Printf("No behavior change detected for 6 seconds, marking Elevator as stale")
					wv.PublishLocally(netSnap1Ch, netSnap2Ch)
				}
			} else {
				if !wv.SelfAlive() {
					wv.SetSelfAlive(true)
					wv.PublishLocally(netSnap1Ch, netSnap2Ch)
				}
				elevatorErrorTimer.Reset(6 * time.Second)
			}
		}
	}
}
