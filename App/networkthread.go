package main

import (
	"elevator/common"
	"elevator/elevnetwork"
	"log"
	"time"
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
	worldView := elevnetwork.InitWorldView(config)
	peerNetwork := elevnetwork.InitPeerNetwork(config)

	worldView.CalculateAlivePeers(time.Now())

	// send initial dummy snapshot
	initialSnapshot := worldView.GetLocalSnapshot()
	initialSnapshot.UpdateKind = common.UK_Requests
	peerNetwork.SendSnapshot(initialSnapshot, worldView.GetSelfAlive())

	localPublishTicker := time.NewTicker(LOCAL_PUBLISH_PERIOD)
	defer localPublishTicker.Stop()

	heartbeatTicker := time.NewTicker(BROADCAST_PERIOD)
	defer heartbeatTicker.Stop()

	startupTimer := time.NewTimer(INITIAL_CONTACT_TIMEOUT)
	defer startupTimer.Stop()

	elevatorErrorTimer := time.NewTimer(ELEVATOR_ERROR_TIMEOUT)
	defer elevatorErrorTimer.Stop()

	for {
		now := time.Now()
		select {
		case elevatorSnapshot := <-elevUpdateNetCh:
			elevatorErrorTimer.Reset(ELEVATOR_ERROR_TIMEOUT)
			worldView.HandleLocalSnapshot(elevatorSnapshot, now)

		case incomingMsg := <-peerNetwork.PeerMessageCh():
			isFiltered := worldView.HandleRemoteMsg(incomingMsg, now)
			// resend serviced request if incoherent
			if isFiltered {
				snapshot := worldView.GetLocalSnapshot()
				snapshot.UpdateKind = common.UK_Serviced
				peerNetwork.SendSnapshot(snapshot, worldView.GetSelfAlive())
			}
		case peerUpdate := <-peerNetwork.PeerUpdateCh():
			worldView.HandlePeerUpdate(peerUpdate)

		case <-localPublishTicker.C:
			worldView.CalculateAlivePeers(now)
			lastSnapshot, ok := peerNetwork.LastSentSnapshot()
			isCoherent := false
			if ok {
				isCoherent = worldView.SnapshotsAreCoherent(lastSnapshot)
			}
			worldView.PublishLocally(netUpdateAssignerCh, netUpdateElevCh, isCoherent)
			//broadcast more often if incoherent
			if !isCoherent {
				snapshot := worldView.GetLocalSnapshot()
				peerNetwork.SendSnapshot(snapshot, worldView.GetSelfAlive())
			}

		case <-heartbeatTicker.C:
			snapshot := worldView.GetLocalSnapshot()
			peerNetwork.SendSnapshot(snapshot, worldView.GetSelfAlive())

		case <-elevatorErrorTimer.C:
			if worldView.GetSelfAlive() {
				worldView.SetSelfAlive(false)
				log.Printf("networkThread: No behavior change detected, marking Elevator as dead")
			}

		case <-startupTimer.C:
			log.Printf("networkThread: forcing end of startup phase")
			worldView.EndStartupPeriod()
		}
	}
}
