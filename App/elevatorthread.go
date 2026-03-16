package main

import (
	"log"
	"time"

	"elevator/common"
	"elevator/elevfsm"
)

const (
	POLL_RATE_MS = 25 * time.Millisecond
	IDLE_RATE_MS = 1 * time.Second
)

func elevatorThread(
	config common.Config,
	hallAssignmentCh <-chan common.HallAssignment, // assigner -> elev
	elevUpdateNetCh chan<- common.Snapshot, // elev -> network
	netUpdateElevCh <-chan common.Snapshot, // network -> elev
) {
	sync := elevfsm.InitRequestManager(config)
	elevator := elevfsm.NewElevator()

	//dummy
	behavior, direction := elevator.MotionStrings()
	initialSnapshot := common.BuildSnapshot(config.SelfKey, common.UK_Requests, common.Requests{},
		elevator.GetPrevFloor(), behavior, direction, sync.GetNetRequests(), sync.GetLocalRequests())
	select {
	case elevUpdateNetCh <- initialSnapshot:
	default:
	}

	ticker := time.NewTicker(POLL_RATE_MS)
	defer ticker.Stop()

	idleTicker := time.NewTicker(IDLE_RATE_MS)
	defer idleTicker.Stop()

	for {
		now := time.Now()
		select {
		case networkSnapshot := <-netUpdateElevCh:
			sync.HandleNetworkSnapshot(networkSnapshot, now)

			if networkSnapshot.Coherent {
				elevator.SetLights(sync.GetNetRequests())
			}

		case hallAssignment := <-hallAssignmentCh:
			revokedRequests := sync.HandleAssignment(hallAssignment)
			elevator.RevokeRequest(revokedRequests)

		case <-ticker.C:
			buttonPresses, newButtonPressed := elevator.PollButtonPresses()
			newCabRequests, newHallRequests := sync.HandleButtonPresses(buttonPresses, elevator.GetFloor(), now)
			elevator.ApplyNewRequests(newCabRequests)
			if !sync.HasAlivePeer() {
				elevator.ApplyNewRequests(newHallRequests)
			}

			elevStateChange, servicedRequests, isServiced := elevator.ElevUpdate(now)

			elevator.ApplyNewRequests(sync.TransferReadyRequests())
			sync.ClearServicedRequests(elevator.GetPrevFloor(), servicedRequests)

			if isServiced {
				behavior, direction := elevator.MotionStrings()
				snapshot := common.BuildSnapshot(config.SelfKey, common.UK_Serviced, servicedRequests,
					elevator.GetPrevFloor(), behavior, direction, sync.GetNetRequests(), sync.GetLocalRequests())
				elevUpdateNetCh <- snapshot

			} else if elevStateChange || newButtonPressed {
				behavior, direction := elevator.MotionStrings()
				snapshot := common.BuildSnapshot(config.SelfKey, common.UK_Requests, common.Requests{},
					elevator.GetPrevFloor(), behavior, direction, sync.GetNetRequests(), sync.GetLocalRequests())
				select {
				case elevUpdateNetCh <- snapshot:
				default:
					log.Printf("fsmThread: elevSnapNetCh is full, skipping snapshot update")
				}
			}
		case <-idleTicker.C:
			if !elevator.IsIdle() {
				continue
			}
			behavior, direction := elevator.MotionStrings()
			snapshot := common.BuildSnapshot(config.SelfKey, common.UK_Requests, common.Requests{},
				elevator.GetPrevFloor(), behavior, direction, sync.GetNetRequests(), sync.GetLocalRequests())
			select {
			case elevUpdateNetCh <- snapshot:
			default:
				log.Printf("fsmThread: elevSnapNetCh is full, skipping snapshot update")
			}
		}
	}
}
