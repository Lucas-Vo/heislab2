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
	var announceDir common.MotorDirection
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
	initialSnap := sync.BuildSnapshot(prevFloor, common.UpdateRequests, servicedCall, time.Now())

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

			sync.ApplyNetworkSnapshot(snap, now)

			sync.TryInjectAll(now, confirmTimeout)
			sync.ApplyLights(now)

		case task := <-assignerOutputCh:
			now := time.Now()

			sync.ApplyAssigner(task)

			sync.TryInjectAll(now, confirmTimeout)
			sync.ApplyLights(now)

		case <-ticker.C:
			now := time.Now()

			elevStateChange := false

			//// Edge-detected button presses -> local sync + immediate FSM inject at current floor
			for f := range common.N_FLOORS {
				for b := range common.N_BUTTONS {
					v := elevInputDevice.RequestButton(f, common.ButtonType(b))
					if v != 0 && v != previousRequests[f][b] {
						atFloor := elevInputDevice.FloorSensor() == f
						sync.OnLocalPress(f, common.ButtonType(b), now, atFloor)
						elevStateChange = true
					}
					previousRequests[f][b] = v
				}
			}

			//// Poll sensors and update FSM on floor/behaviour/direction changes
			newBehaviour := sync.Elevator.GetBehaviour()
			newDirection := sync.Elevator.GetDirection()
			newFloor := elevInputDevice.FloorSensor()
			doorJustClosed := prevBehaviour == elevfsm.EB_DoorOpen && newBehaviour != elevfsm.EB_DoorOpen
			if newFloor != prevFloor || newBehaviour != prevBehaviour || newDirection != prevDirection {
				elevStateChange = true
			}
			if newFloor != -1 && newFloor != prevFloor {
				sync.Elevator.OnFloorArrival(newFloor)
				prevFloor = newFloor
			}

			//// Door timer pause/resume for obstruction and at-floor activity
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

			//// Entering DoorOpen: announce direction, start timer
			if prevBehaviour != newBehaviour && newBehaviour == elevfsm.EB_DoorOpen {
				arrivalDirn := sync.Elevator.GetDirection()
				announceDir = sync.ChooseAnnounceDir(prevFloor, arrivalDirn)
				// start door timer when entering DoorOpen
				d := time.Duration(3 * time.Second)
				doorTimerEnd = now.Add(d)
				doorTimerActive = true
				timerPaused = false
			}
			prevBehaviour = newBehaviour
			prevDirection = newDirection

			//// Door timer expiry: clear announced hall call or close door
			if doorTimerActive && now.After(doorTimerEnd) {
				// stop timer
				doorTimerActive = false
				timerPaused = false
				upReq, downReq := sync.HallRequestsAtFloor(prevFloor)
				if announceDir == common.MD_Up && upReq {
					servicedCall = sync.ClearAtFloor(sync.Elevator, prevFloor, common.MD_Up, true, now)
					if downReq {
						announceDir = common.MD_Down
						d := time.Duration(3 * time.Second)
						doorTimerEnd = now.Add(d)
						doorTimerActive = true
						timerPaused = false
					} else {
						sync.Elevator.OnDoorTimeout()
					}
				} else if announceDir == common.MD_Down && downReq {
					servicedCall = sync.ClearAtFloor(sync.Elevator, prevFloor, common.MD_Down, true, now)
					if upReq {
						announceDir = common.MD_Up
						d := time.Duration(3 * time.Second)
						doorTimerEnd = now.Add(d)
						doorTimerActive = true
						timerPaused = false
					} else {
						sync.Elevator.OnDoorTimeout()
					}
				} else if upReq || downReq {
					arrivalDirn := sync.Elevator.GetDirection()
					announceDir = sync.ChooseAnnounceDir(prevFloor, arrivalDirn)
					servicedCall = sync.ClearAtFloor(sync.Elevator, prevFloor, announceDir, true, now)
					if announceDir == common.MD_Up && downReq {
						announceDir = common.MD_Down
						d := time.Duration(3 * time.Second)
						doorTimerEnd = now.Add(d)
						doorTimerActive = true
						timerPaused = false
					} else if announceDir == common.MD_Down && upReq {
						announceDir = common.MD_Up
						d := time.Duration(3 * time.Second)
						doorTimerEnd = now.Add(d)
						doorTimerActive = true
						timerPaused = false
					} else {
						sync.Elevator.OnDoorTimeout()
					}
				} else {
					servicedCall = sync.ClearAtFloor(sync.Elevator, prevFloor, common.MD_Stop, true, now)
					sync.Elevator.OnDoorTimeout()
				}
			} //TODO: Maybe this door functionality can be put in a helper function to help readability for the fsmthread

			//// Inject confirmed requests, update lights, and publish snapshots
			if doorJustClosed && prevFloor != -1 {
				staleServiced := sync.StaleServicedHallAtFloor(prevFloor, now)
				servicedCall.HallUp = servicedCall.HallUp || staleServiced.HallUp
				servicedCall.HallDown = servicedCall.HallDown || staleServiced.HallDown
			}
			sync.TryInjectAll(now, confirmTimeout)
			sync.ApplyLights(now)

			if !sync.HasNetSelf() {
				continue
			}
			if servicedCall.HallUp || servicedCall.HallDown || servicedCall.Cab {
				snapshot := sync.BuildSnapshot(prevFloor, common.UpdateServiced, servicedCall, now)
				servicedCall = elevfsm.ServicedAt{HallUp: false, HallDown: false, Cab: false}
				select {
				case elevUpdateCh <- snapshot:
				default:
				}
			}
			if elevStateChange {
				snapshot := sync.BuildSnapshot(prevFloor, common.UpdateRequests, servicedCall, now)
				select {
				case elevUpdateCh <- snapshot:
				default:
				}
			}
		}
	}
}
