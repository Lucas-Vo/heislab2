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
	assignerOutputCh <-chan common.ElevInput, // assigner -> elev
	elevUpdateNetCh chan<- common.Snapshot, // elev -> network
	netUpdateElevCh <-chan common.Snapshot, // network -> elev
) {
	sync := elevfsm.NewFsmSync(config)
	elevator := elevfsm.NewElevator()

	//dummy
	behavior, direction := elevator.MotionStrings()
	initialSnapshot := sync.BuildSnapshot(common.UpdateRequests, common.Requests{}, elevator.GetPrevFloor(), behavior, direction)
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

		case task := <-assignerOutputCh:
			toRevoke := sync.HandleAssignerTask(task)
			elevator.RevokeRequest(toRevoke)

		case <-ticker.C:
			buttonPresses, newButtonPressed := elevator.PollButtonPresses()
			newCabRequests := sync.HandleButtonPresses(buttonPresses, elevator.GetFloor(), now)
			elevator.ApplyNewRequests(newCabRequests)

			elevStateChange, servicedRequests, isServiced := elevator.UpdateFSM(now)

			elevator.ApplyNewRequests(sync.TransferReadyRequests())
			sync.ClearServicedRequests(elevator.GetPrevFloor(), servicedRequests)

			if isServiced {
				behavior, direction := elevator.MotionStrings()
				snapshot := sync.BuildSnapshot(common.UpdateServiced, servicedRequests, elevator.GetPrevFloor(), behavior, direction)
				elevUpdateNetCh <- snapshot

			} else if elevStateChange || newButtonPressed {
				behavior, direction := elevator.MotionStrings()
				snapshot := sync.BuildSnapshot(common.UpdateRequests, common.Requests{}, elevator.GetPrevFloor(), behavior, direction)
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
			snapshot := sync.BuildSnapshot(common.UpdateRequests, common.Requests{}, elevator.GetPrevFloor(), behavior, direction)
			select {
			case elevUpdateNetCh <- snapshot:
			default:
				log.Printf("fsmThread: elevSnapNetCh is full, skipping snapshot update")
			}
		}
	}
}
