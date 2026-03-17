package main

import (
	"log"
	"time"

	"elevator/common"
	"elevator/elevfsm"
)

const (
	pollRate           = 25 * time.Millisecond
	idleSnapshotPeriod = 1 * time.Second
)

// elevatorThread owns the local controller loop.
func elevatorThread(
	config common.Config,
	assignerOutputCh <-chan common.ElevInput,
	elevUpdateNetCh chan<- common.Snapshot,
	netUpdateElevCh <-chan common.Snapshot,
) {
	sync := elevfsm.NewFsmSync(config)
	elevator := elevfsm.NewElevator()

	behavior, direction := elevator.MotionStrings()
	initialSnapshot := common.BuildSnapshot(config.SelfKey, common.UpdateRequests, common.Requests{},
		elevator.GetPrevFloor(), behavior, direction, sync.GetNetRequests(), sync.GetLocalRequests())
	select {
	case elevUpdateNetCh <- initialSnapshot:
	default:
	}

	ticker := time.NewTicker(pollRate)
	defer ticker.Stop()

	idleTicker := time.NewTicker(idleSnapshotPeriod)
	defer idleTicker.Stop()

	for {
		now := time.Now()
		select {
		case networkSnapshot := <-netUpdateElevCh:
			sync.HandleNetworkSnapshot(networkSnapshot)

			if networkSnapshot.Coherent {
				elevator.SetLights(sync.GetNetRequests())
			}

		case task := <-assignerOutputCh:
			toRevoke := sync.HandleAssignerTask(task)
			elevator.RevokeRequest(toRevoke)

		case <-ticker.C:
			buttonPresses, newButtonPressed := elevator.PollButtonPresses()
			newCabRequests, newHallRequests := sync.HandleButtonPresses(buttonPresses)
			elevator.ApplyNewRequests(newCabRequests)
			if !sync.HasAlivePeer() {
				elevator.ApplyNewRequests(newHallRequests)
			}

			elevStateChange, servicedRequests, isServiced := elevator.UpdateFSM(now)

			elevator.ApplyNewRequests(sync.TransferReadyRequests())
			sync.ClearServicedRequests(elevator.GetPrevFloor(), servicedRequests)

			if isServiced {
				behavior, direction := elevator.MotionStrings()
				snapshot := common.BuildSnapshot(config.SelfKey, common.UpdateServiced, servicedRequests,
					elevator.GetPrevFloor(), behavior, direction, sync.GetNetRequests(), sync.GetLocalRequests())
				elevUpdateNetCh <- snapshot

			} else if elevStateChange || newButtonPressed {
				behavior, direction := elevator.MotionStrings()
				snapshot := common.BuildSnapshot(config.SelfKey, common.UpdateRequests, common.Requests{},
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
			snapshot := common.BuildSnapshot(config.SelfKey, common.UpdateRequests, common.Requests{},
				elevator.GetPrevFloor(), behavior, direction, sync.GetNetRequests(), sync.GetLocalRequests())
			select {
			case elevUpdateNetCh <- snapshot:
			default:
				log.Printf("fsmThread: elevSnapNetCh is full, skipping snapshot update")
			}
		}
	}
}
