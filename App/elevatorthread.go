package main

import (
	"log"
	"time"

	"elevator/common"
	"elevator/elevfsm"
)

const (
	// POLL_RATE_MS is the local FSM polling period for buttons, floor sensor,
	// obstruction, and door timing.
	POLL_RATE_MS = 25 * time.Millisecond
	// IDLE_RATE_MS is the heartbeat period used while the local elevator is idle.
	IDLE_RATE_MS = 1 * time.Second
)

// elevatorThread owns the local Elevator FSM and all direct interaction with
// the simulator I/O.
//
// The thread is the only goroutine that mutates Elevator or Synchronizer. It
// publishes local state to the network thread, applies hall assignments from
// the assigner thread, and falls back to serving hall calls locally when no
// peer is alive.
func elevatorThread(
	config common.Config,
	assignerOutputCh <-chan common.ElevInput, // assigner -> elev
	elevUpdateNetCh chan<- common.Snapshot, // elev -> network
	netUpdateElevCh <-chan common.Snapshot, // network -> elev
) {
	sync := elevfsm.NewFsmSync(config)
	elevator := elevfsm.NewElevator()

	// Publish a best-effort initial snapshot so the network thread has a local
	// state to merge even before the first button press or state change.
	behavior, direction := elevator.MotionStrings()
	initialSnapshot := common.BuildSnapshot(config.SelfKey, common.UpdateRequests, common.Requests{},
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

		case task := <-assignerOutputCh:
			toRevoke := sync.HandleAssignerTask(task)
			elevator.RevokeRequest(toRevoke)

		case <-ticker.C:
			buttonPresses, newButtonPressed := elevator.PollButtonPresses()
			newCabRequests, newHallRequests := sync.HandleButtonPresses(buttonPresses, elevator.GetFloor(), now)
			elevator.ApplyNewRequests(newCabRequests)
			if !sync.HasAlivePeer() {
				// In single-elevator or disconnected mode, new hall calls are served
				// locally without waiting for a distributed assignment round.
				elevator.ApplyNewRequests(newHallRequests)
			}

			elevStateChange, servicedRequests, isServiced := elevator.UpdateFSM(now)

			elevator.ApplyNewRequests(sync.TransferReadyRequests())
			sync.ClearServicedRequests(elevator.GetPrevFloor(), servicedRequests)

			if isServiced {
				// A serviced snapshot clears shared hall calls through AND-merge.
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
			// Idle snapshots act as a heartbeat so peers can keep the local
			// elevator marked alive even when no requests are changing.
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
