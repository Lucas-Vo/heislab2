// networkthread.go
package main

import (
	"log"
	"time"

	"elevator/common"
	"elevator/elevnetwork"
)

const (
	LOCAL_PUBLISH_PERIOD    = 50 * time.Millisecond
	BROADCAST_PERIOD        = 1 * time.Second
	INITIAL_CONTACT_TIMEOUT = 5 * time.Second
	ELEVATOR_ERROR_TIMEOUT  = 6 * time.Second
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
			wv.MergeWorldView(cfg.SelfKey, ns)
			if !wv.SnapshotsAreCoherent() {
				wv.Broadcast()
			}

		case msg := <-incoming:
			msgToMerge, filteredHalls, isFiltered := wv.FilterRecentlyServicedHalls(msg, now)

			wv.MergeRemote(msgToMerge)
			if isFiltered {
				wv.CalculateAlive(now)
				wv.ResendServicedHalls(filteredHalls)
			}
			wv.MergeWorldView(msgToMerge.Origin, msgToMerge.Snapshot)

		case peerUpdate := <-peerUpdates:
			wv.HandlePeerUpdate(peerUpdate, now)

		case <-localTicker.C:
			wv.CalculateAlive(now)
			if !wv.SnapshotsAreCoherent() {
				wv.Broadcast()
			}
			wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh)

		case <-broadcastTicker.C:
			wv.Broadcast()

		case <-elevatorErrorTimer.C:
			if wv.GetSelfAlive() {
				wv.SetSelfAlive(false)
				log.Printf("networkThread: No behavior change detected, marking Elevator as stale")
			}

		case <-startupTimer.C:
			log.Printf("networkThread: forcing end of startup phase")
			wv.EndStartupPeriod()

		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
