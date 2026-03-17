package main

import (
	"log"
	"time"

	"elevator/common"
	"elevator/elevnetwork"
)

const (
	localPublishPeriod  = 50 * time.Millisecond
	broadcastPeriod     = 1 * time.Second
	initialContactDelay = 5 * time.Second
	elevatorDeadTimeout = 6 * time.Second
)

// networkThread owns peer communication and the merged world view.
func networkThread(
	config common.Config,
	elevUpdateNetCh <-chan common.Snapshot,
	netUpdateAssignerCh chan<- common.Snapshot,
	netUpdateElevCh chan<- common.Snapshot,
) {
	wv := elevnetwork.InitWorldView(config)
	network := elevnetwork.InitNetwork(config)

	wv.CalculateAlive(time.Now())

	initialSnapshot := common.DeepCopySnapshot(wv.SnapshotForBroadcast())
	initialSnapshot.UpdateKind = common.UpdateRequests
	network.SendSnapshot(initialSnapshot, wv.GetSelfAlive())

	localTicker := time.NewTicker(localPublishPeriod)
	defer localTicker.Stop()

	broadcastTicker := time.NewTicker(broadcastPeriod)
	defer broadcastTicker.Stop()

	startupTimer := time.NewTimer(initialContactDelay)
	defer startupTimer.Stop()

	elevatorErrorTimer := time.NewTimer(elevatorDeadTimeout)
	defer elevatorErrorTimer.Stop()

	for {
		now := time.Now()
		select {
		case elevatorSnapshot := <-elevUpdateNetCh:
			elevatorErrorTimer.Reset(elevatorDeadTimeout)
			wv.HandleLocal(elevatorSnapshot, now)

		case msg := <-network.Incoming():
			filteredHalls, isFiltered := wv.HandleRemote(msg, now)
			if isFiltered {
				snapshot := wv.SnapshotForResend(filteredHalls)
				network.SendSnapshot(snapshot, wv.GetSelfAlive())
			}
		case peerUpdate := <-network.PeerUpdates():
			wv.HandlePeerUpdate(peerUpdate, now)

		case <-localTicker.C:
			wv.CalculateAlive(now)
			snapshot, ok := network.LastSentSnapshot()
			isCoherent := false
			if ok {
				isCoherent = wv.SnapshotsAreCoherent(snapshot)
			}
			wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh, isCoherent)
			if !isCoherent {
				snapshot := wv.SnapshotForBroadcast()
				network.SendSnapshot(snapshot, wv.GetSelfAlive())
			}

		case <-broadcastTicker.C:
			snapshot := wv.SnapshotForBroadcast()
			network.SendSnapshot(snapshot, wv.GetSelfAlive())

		case <-elevatorErrorTimer.C:
			if wv.GetSelfAlive() {
				wv.SetSelfAlive(false)
				log.Printf("networkThread: No behavior change detected, marking Elevator as dead")
			}

		case <-startupTimer.C:
			log.Printf("networkThread: forcing end of startup phase")
			wv.EndStartupPeriod()
		}
	}
}
