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
	INITIAL_CONTACT_TIMEOUT = 9 * time.Second
	ELEVATOR_ERROR_TIMEOUT  = 6 * time.Second
	LOCAL_PUBLISH_PERIOD    = 50 * time.Millisecond
	BROADCAST_PERIOD        = 1 * time.Second
)

func networkThread(
	ctx context.Context,
	cfg common.Config,
	elevUpdateCh <-chan common.Snapshot,
	netUpdateAssignerCh chan<- common.Snapshot,
	netUpdateElevCh chan<- common.Snapshot,
) {
	selfKey := cfg.SelfKey

	wv, incoming := elevnetwork.InitWorldView(ctx, cfg, 4242)

	localTicker := time.NewTicker(LOCAL_PUBLISH_PERIOD)
	defer localTicker.Stop()

	broadcastTicker := time.NewTicker(BROADCAST_PERIOD)
	defer broadcastTicker.Stop()

	startupTimer := time.NewTimer(INITIAL_CONTACT_TIMEOUT)
	defer startupTimer.Stop()

	elevatorErrorTimer := time.NewTimer(ELEVATOR_ERROR_TIMEOUT)
	defer elevatorErrorTimer.Stop()

	i := 0
	for {
		now := time.Now()
		select {
		case ns := <-elevUpdateCh:
			wv.SetSelfAlive(true)
			elevatorErrorTimer.Reset(ELEVATOR_ERROR_TIMEOUT)
			if ns.UpdateKind == common.UpdateServiced {
				wv.MarkRecentlyServicedHalls(ns, now)
			}
			wv.MergeLocal(ns)
			wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh)

		case frame := <-incoming:
			frameToMerge, filteredHalls, isFiltered := wv.FilterRecentlyServicedHalls(frame, now)
			wv.MergeRemote(frameToMerge)
			if isFiltered {
				wv.ResendServicedHalls(filteredHalls)
			}

			wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh)

		case <-localTicker.C:

			if !wv.SnapshotsAreCoherent() || !wv.JoinedNetwork() {
				wv.BroadcastRequests()
			}
			if i%10 == 0 {
				log.Printf("%v", wv.GetSnapshot().Alive)
			}
			i++
			if wv.JoinedNetwork() {
				wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh)
			}

		case <-broadcastTicker.C:
			if wv.JoinedNetwork() {
				wv.BroadcastRequests()
			}

		case <-elevatorErrorTimer.C:
			snap := wv.GetSnapshot()
			if snap.States[selfKey].Behavior != "idle" {
				if wv.SelfAlive() {
					wv.SetSelfAlive(false)
					log.Printf("No behavior change detected for 6 seconds, marking Elevator as stale")
					wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh)
				}
			} else {
				if !wv.SelfAlive() {
					wv.SetSelfAlive(true)
					wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh)
				}
				elevatorErrorTimer.Reset(ELEVATOR_ERROR_TIMEOUT)
			}
		case <-startupTimer.C:
			log.Printf("networkThread: forcing end of startup phase")
			wv.EndStartupPeriod()
		default:
			time.Sleep(10 * time.Millisecond)
		}

	}
}
