// networkthread.go
package main

import (
	"log"
	"time"

	"elevator/common"
	"elevator/elevnetwork"
)

const (
	INITIAL_CONTACT_TIMEOUT = 5 * time.Second
	ELEVATOR_ERROR_TIMEOUT  = 6 * time.Second
	LOCAL_PUBLISH_PERIOD    = 50 * time.Millisecond
	BROADCAST_PERIOD        = 1 * time.Second
)

func networkThread(
	cfg common.Config,
	elevUpdateCh <-chan common.Snapshot,
	netUpdateAssignerCh chan<- common.Snapshot,
	netUpdateElevCh chan<- common.Snapshot,
) {
	wv, incoming, peerUpdates := elevnetwork.InitWorldView(cfg)

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
			log.Printf("JESPER 1")
			wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh)

		case msg := <-incoming:
			msgToMerge, filteredHalls, isFiltered := wv.FilterRecentlyServicedHalls(msg, now)
			wv.MergeRemote(msgToMerge)
			if isFiltered {
				wv.CalculateAlive(now)
				wv.ResendServicedHalls(filteredHalls)
			}
			log.Printf("JESPER 2")
			wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh)

		case peerUpdate := <-peerUpdates:
			wv.HandlePeerUpdate(peerUpdate, now)
			wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh)

		case <-localTicker.C:
			wv.CalculateAlive(now)
			if !wv.SnapshotsAreCoherent() {
				wv.BroadcastRequests()
			}

		case <-broadcastTicker.C:
			wv.BroadcastRequests()

		case <-elevatorErrorTimer.C:
			if wv.GetSelfAlive() {
				wv.SetSelfAlive(false)
				log.Printf("No behavior change detected for 6 seconds, marking Elevator as stale")
				log.Printf("JESPER 3")
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
