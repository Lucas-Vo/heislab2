package main

import (
	"elevator/common"
	"elevator/elevfsm"
	"log"
	"time"
)

const (
	POLL_RATE_MS     = 25 * time.Millisecond
	HEARTBEAT_RATE_S = 1 * time.Second
)

func elevatorThread(
	config common.Config,
	hallAssignmentCh <-chan common.HallAssignment, // assigner -> elev
	elevUpdateNetCh chan<- common.Snapshot, // elev -> network
	netUpdateElevCh <-chan common.Snapshot, // network -> elev
) {
	requestManager := elevfsm.InitRequestManager(config)
	elevator := elevfsm.InitElevator()

	// send initial dummy snapshot
	behavior, dirn := elevator.MotionToStrings()
	initialSnapshot := common.BuildSnapshot(config.SelfKey, common.UK_Requests, common.Requests{},
		elevator.GetPrevFloor(), behavior, dirn, requestManager.GetNetRequests(), requestManager.GetLocalRequests())
	select {
	case elevUpdateNetCh <- initialSnapshot:
	default:
	}

	pollLoop := time.NewTicker(POLL_RATE_MS)
	defer pollLoop.Stop()

	heartbeatTicker := time.NewTicker(HEARTBEAT_RATE_S)
	defer heartbeatTicker.Stop()

	for {
		now := time.Now()
		select {
		case networkSnapshot := <-netUpdateElevCh:
			requestManager.HandleNetworkSnapshot(networkSnapshot, now)

			if networkSnapshot.Coherent {
				elevator.SetLights(requestManager.GetNetRequests())
			}

		case hallAssignment := <-hallAssignmentCh:
			removeAssignments := requestManager.HandleAssignment(hallAssignment)
			elevator.RemoveAssignments(removeAssignments)

		case <-pollLoop.C:
			buttonPresses, isButtonPressed := elevator.PollButtonPresses()
			newCabRequests, newHallRequests := requestManager.HandleNewRequests(buttonPresses, elevator.GetFloor(), now)
			elevator.ApplyNewRequests(newCabRequests)
			if !requestManager.InDistributedMode() {
				elevator.ApplyNewRequests(newHallRequests)
			}

			elevStateChange, servicedRequests, isServiced := elevator.ElevUpdate(now)
			readyRequests := requestManager.GetReadyRequests()
			elevator.ApplyNewRequests(readyRequests)
			requestManager.ClearServicedRequests(elevator.GetPrevFloor(), servicedRequests)

			if isServiced {
				behavior, dirn := elevator.MotionToStrings()
				snapshot := common.BuildSnapshot(config.SelfKey, common.UK_Serviced, servicedRequests,
					elevator.GetPrevFloor(), behavior, dirn, requestManager.GetNetRequests(), requestManager.GetLocalRequests())
				select {
				case elevUpdateNetCh <- snapshot:
				default:
					log.Printf("fsmThread: elevSnapNetCh is full, skipping snapshot update")
				}

			} else if elevStateChange || isButtonPressed {
				behavior, dirn := elevator.MotionToStrings()
				snapshot := common.BuildSnapshot(config.SelfKey, common.UK_Requests, common.Requests{},
					elevator.GetPrevFloor(), behavior, dirn, requestManager.GetNetRequests(), requestManager.GetLocalRequests())
				select {
				case elevUpdateNetCh <- snapshot:
				default:
					log.Printf("fsmThread: elevSnapNetCh is full, skipping snapshot update")
				}
			}
		case <-heartbeatTicker.C:
			if !elevator.IsIdle() {
				continue
			}
			behavior, dirn := elevator.MotionToStrings()
			snapshot := common.BuildSnapshot(config.SelfKey, common.UK_Requests, common.Requests{},
				elevator.GetPrevFloor(), behavior, dirn, requestManager.GetNetRequests(), requestManager.GetLocalRequests())
			select {
			case elevUpdateNetCh <- snapshot:
			default:
				log.Printf("fsmThread: elevSnapNetCh is full, skipping snapshot update")
			}
		default:
			//Do nothing
		}
	}
}
