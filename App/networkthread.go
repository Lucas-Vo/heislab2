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
	wv, incoming, peerUpdates := elevnetwork.InitWorldView(ctx, cfg)

	localTicker := time.NewTicker(LOCAL_PUBLISH_PERIOD)
	defer localTicker.Stop()

	broadcastTicker := time.NewTicker(BROADCAST_PERIOD)
	defer broadcastTicker.Stop()

	startupTimer := time.NewTimer(INITIAL_CONTACT_TIMEOUT)
	defer startupTimer.Stop()

	elevatorErrorTimer := time.NewTimer(ELEVATOR_ERROR_TIMEOUT)
	defer elevatorErrorTimer.Stop()

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
			if !wv.SnapshotsAreCoherent() {
				wv.BroadcastRequests()
			}

			wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh)

		case msg := <-incoming:
			msgToMerge, filteredHalls, isFiltered := wv.FilterRecentlyServicedHalls(msg, now)
			wv.MergeRemote(msgToMerge)
			if isFiltered {
				wv.ResendServicedHalls(filteredHalls)
			}

			wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh)

		case peerUpdate := <-peerUpdates:
			wv.HandlePeerUpdate(peerUpdate, now)
			wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh)

		case <-localTicker.C:
			wv.CalculateAlive(now)
			if !wv.SnapshotsAreCoherent() {
				wv.BroadcastRequests()
			}
			wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh)

		case <-broadcastTicker.C:
			wv.BroadcastRequests()

		case <-elevatorErrorTimer.C:
			if wv.GetSelfAlive() {
				wv.SetSelfAlive(false)
				log.Printf("No behavior change detected for 6 seconds, marking Elevator as stale")
				wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh)
			}

		case <-startupTimer.C:
			log.Printf("networkThread: forcing end of startup phase")
			wv.EndStartupPeriod()

		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
