package main

import (
	"context"
	"log"
	"time"

	"elevator/common"
	"elevator/elevfsm"
)

func fsmThread(
	ctx context.Context,
	cfg common.Config,
	elevInputDevice common.ElevInputDevice,
	assignerOutputCh <-chan common.ElevInput,
	elevUpdateCh chan<- common.Snapshot,
	netWorldView2Ch <-chan common.Snapshot, // network -> fsm
) {
	log.Printf("fsmThread started (self=%s)", cfg.SelfKey)

	// Initialize FSM state and output device before any events are handled.

	inputPollRateMs := 25

	sync := elevfsm.NewFsmSync(cfg)

	var previousRequests [common.N_FLOORS][common.N_BUTTONS]int

	confirmTimeout := 200 * time.Millisecond
	prevObstructed := false
	timerPaused := false

	// Local timer state so this thread uses only the standard `time` package
	// instead of package-level helper functions.
	var doorTimerEnd time.Time
	var doorTimerActive bool
	var servicedCall elevfsm.ServicedAt
	// Seed floor state if the sensor is already at a floor; otherwise start moving to find one.
	prevFloor := -1
	if newFloor := elevInputDevice.FloorSensor(); newFloor != -1 {
		sync.Elevator.OnFloorArrival(newFloor)
		prevFloor = newFloor
	} else {
		sync.Elevator.OnInitBetweenFloors()
	}
	prevDirection := sync.Elevator.GetDirection()
	prevBehaviour := sync.Elevator.GetBehaviour()
	initialSnap := sync.BuildSnapshot(prevFloor, common.UpdateRequests, servicedCall, false)

	select {
	case elevUpdateCh <- initialSnap:
	default:
	}

	ticker := time.NewTicker(time.Duration(inputPollRateMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case snap := <-netWorldView2Ch:
			now := time.Now()
			online := !sync.Offline(now)

			sync.ApplyNetworkSnapshot(snap, now)

			sync.TryInjectAll(now, confirmTimeout, online)
			sync.ApplyLights(online)

		case task := <-assignerOutputCh:
			now := time.Now()
			online := !sync.Offline(now)

			sync.ApplyAssigner(task)

			sync.TryInjectAll(now, confirmTimeout, online)
			sync.ApplyLights(online)

		case <-ticker.C:
			now := time.Now()
			online := !sync.Offline(now) //TODO: Change name of online

			elevStateChange := false

			// Request buttons (edge-detected)
			for f := range common.N_FLOORS {
				for b := range common.N_BUTTONS {
					v := elevInputDevice.RequestButton(f, common.ButtonType(b))
					if v != 0 && v != previousRequests[f][b] {
						sync.OnLocalPress(f, common.ButtonType(b), now)
						elevStateChange = true
						if elevInputDevice.FloorSensor() == f {
							sync.Elevator.OnRequestButtonPress(f, common.ButtonType(b))
						}
					}
					previousRequests[f][b] = v
				}
			}
			newBehaviour := sync.Elevator.GetBehaviour()
			newDirection := sync.Elevator.GetDirection()
			newFloor := elevInputDevice.FloorSensor()
			if newFloor != prevFloor || newBehaviour != prevBehaviour || newDirection != prevDirection {
				elevStateChange = true
			}
			if newFloor != -1 && newFloor != prevFloor {
				sync.Elevator.OnFloorArrival(newFloor)
				prevFloor = newFloor
			}

			// Obstruction handling: keep door open while obstructed; restart timer when cleared.
			obstructed := elevInputDevice.Obstruction() != 0
			if sync.Elevator.GetBehaviour() == elevfsm.EB_DoorOpen {
				if obstructed {
					if !timerPaused {
						// stop local timer
						doorTimerActive = false
						timerPaused = true
					}
				} else if timerPaused || prevObstructed ||
					(previousRequests[prevFloor][common.BT_HallUp] != 0 && sync.Elevator.GetDirection() != common.MD_Stop) ||
					(previousRequests[prevFloor][common.BT_HallDown] != 0 && sync.Elevator.GetDirection() != common.MD_Stop) ||
					previousRequests[prevFloor][common.BT_Cab] != 0 {
					// start local timer using doorOpenDuration (seconds)
					d := time.Duration(3 * time.Second)
					doorTimerEnd = now.Add(d)
					doorTimerActive = true
					timerPaused = false
				}
			} else {
				timerPaused = false
			}
			prevObstructed = obstructed

			// Inject confirmed requests
			sync.TryInjectAll(now, confirmTimeout, online)

			sync.ApplyLights(online)

			if prevBehaviour != newBehaviour && newBehaviour == elevfsm.EB_DoorOpen {
				// start door timer when entering DoorOpen
				d := time.Duration(3 * time.Second)
				doorTimerEnd = now.Add(d)
				doorTimerActive = true
				timerPaused = false
			}
			if doorTimerActive && now.After(doorTimerEnd) {
				// stop timer
				doorTimerActive = false
				timerPaused = false
				arrivalDirn := sync.Elevator.GetDirection()
				sync.Elevator.OnDoorTimeout()
				servicedCall = sync.ClearAtFloor(sync.Elevator, prevFloor, arrivalDirn, online)
			} //TODO: Maybe this door functionality can be put in a helper function to help readability for the fsmthread

			prevBehaviour = newBehaviour
			prevDirection = newDirection
			if !sync.HasNetSelf() {
				continue
			}
			if servicedCall.HallUp || servicedCall.HallDown || servicedCall.Cab {
				snapshot := sync.BuildSnapshot(prevFloor, common.UpdateServiced, servicedCall, online)
				servicedCall = elevfsm.ServicedAt{HallUp: false, HallDown: false, Cab: false}
				select {
				case elevUpdateCh <- snapshot:
				default:
				}
			}
			if elevStateChange {
				snapshot := sync.BuildSnapshot(prevFloor, common.UpdateRequests, servicedCall, online)
				select {
				case elevUpdateCh <- snapshot:
				default:
				}
			}
		}
	}
}
