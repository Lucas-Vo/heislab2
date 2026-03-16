// networkthread.go
package main

import (
	"Network-go/network/peers"
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
	incoming <-chan common.NetMsg,
	outgoing chan<- common.NetMsg,
	peerUpdates <-chan peers.PeerUpdate,
) {
	wv := elevnetwork.InitWorldView(config, outgoing)

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

		case msg := <-incoming:
			wv.HandleRemote(msg, now)
		case peerUpdate := <-peerUpdates:
			wv.HandlePeerUpdate(peerUpdate, now)

		case <-localTicker.C:
			wv.CalculateAlive(now)
			wv.PublishLocally(netUpdateAssignerCh, netUpdateElevCh)
			if !wv.SnapshotsAreCoherent() { //broadcast more often if we notice inchoherency
				wv.Broadcast()
			}

		case <-broadcastTicker.C:
			wv.Broadcast()

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
