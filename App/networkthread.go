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
	config common.Config,
	elevUpdateNetCh <-chan common.Snapshot, // elev -> net
	netUpdateAssignerCh chan<- common.Snapshot, // net -> assigner
	netUpdateElevCh chan<- common.Snapshot, // net -> elev
) {
	wv := elevnetwork.InitWorldView(config)
	network := elevnetwork.InitPeerNetwork(config)

	wv.CalculateAlive(time.Now())

	initialSnapshot := common.DeepCopySnapshot(wv.SnapshotForBroadcast())
	initialSnapshot.UpdateKind = common.UK_Requests
	network.SendSnapshot(initialSnapshot, wv.GetSelfAlive())

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
		case elevatorSnapshot := <-elevUpdateNetCh:
			elevatorErrorTimer.Reset(ELEVATOR_ERROR_TIMEOUT)
			wv.HandleLocal(elevatorSnapshot, now)

		case msg := <-network.Incoming():
			filteredHalls, isFiltered := wv.HandleRemote(msg, now)
			if isFiltered { // resend serviced request if inchoerent
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
			if !isCoherent { //broadcast more often if incoherent
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
