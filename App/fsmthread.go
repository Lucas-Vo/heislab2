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
	elevfsm.ConLoad("elevator.con",
		elevfsm.ConVal("inputPollRate_ms", &inputPollRateMs, "%d"),
	)

	sync := elevfsm.NewFsmSync(cfg)
	sync.Elevator = elevfsm.Fsm_init()

	var previousRequests [common.N_FLOORS][common.N_BUTTONS]int

	confirmTimeout := 200 * time.Millisecond //TODO make global or some shit
	doorOpenDuration := elevfsm.DoorOpenDuration(sync.Elevator)
	prevObstructed := false
	timerPaused := false

	// Seed floor state if the sensor is already at a floor; otherwise start moving to find one.
	prevFloor := -1
	if f := elevInputDevice.FloorSensor(); f != -1 {
		elevfsm.Fsm_onFloorArrival(sync.Elevator, f)
		prevFloor = f
	} else {
		elevfsm.Fsm_onInitBetweenFloors(sync.Elevator)
	}
	behavior, direction := elevfsm.CurrentMotionStrings(sync.Elevator)
	initialSnap := sync.BuildUpdateSnapshot(prevFloor, behavior, direction)

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

			var servicedCall elevfsm.ServicedAt

			// Request buttons (edge-detected)
			for f := range common.N_FLOORS {
				for b := 0; b < common.N_BUTTONS; b++ {
					v := elevInputDevice.RequestButton(f, common.ButtonType(b))
					if v != 0 && v != previousRequests[f][b] {
						sync.OnLocalPress(f, common.ButtonType(b), now)
						elevStateChange = true
						if elevInputDevice.FloorSensor() == f {
							elevfsm.Fsm_onRequestButtonPress(sync.Elevator, f, common.ButtonType(b))
						}
					}
					previousRequests[f][b] = v
				}
			}

			// Floor sensor
			f := elevInputDevice.FloorSensor()
			if f != -1 && f != prevFloor {
				elevfsm.Fsm_onFloorArrival(sync.Elevator, f)
				prevFloor = f
				elevStateChange = true
			}

			// Obstruction handling: keep door open while obstructed; restart timer when cleared.
			obstructed := elevInputDevice.Obstruction() != 0
			if elevfsm.CurrentBehaviour(sync.Elevator) == elevfsm.EB_DoorOpen && obstructed {
				elevfsm.Timer_start(doorOpenDuration)
				if obstructed {
					if !timerPaused {
						elevfsm.Timer_stop()
						timerPaused = true
					}
				} else if timerPaused || prevObstructed {
					elevfsm.Timer_start(doorOpenDuration)
					timerPaused = false
				}
			} else {
				timerPaused = false
			}
			prevObstructed = obstructed

			// Timer
			if elevfsm.Timer_timedOut() != 0 {
				elevfsm.Timer_stop()
				timerPaused = true
				arrivalDirn := elevfsm.CurrentDirection(sync.Elevator)
				elevfsm.Fsm_onDoorTimeout(sync.Elevator)

				servicedCall = sync.ClearAtFloor(prevFloor, online, arrivalDirn)
			}

			// Inject confirmed requests
			sync.TryInjectAll(now, confirmTimeout, online)

			sync.ApplyLights(online)

			behavior, direction = elevfsm.CurrentMotionStrings(sync.Elevator)
			if sync.MotionChanged(prevFloor, behavior, direction) {
				elevStateChange = true
			}

			if !sync.HasNetSelf() {
				continue
			}
			if servicedCall.HallUp || servicedCall.HallDown || servicedCall.Cab {
				snapshot := sync.BuildSnapshot(prevFloor, behavior, direction, common.UpdateServiced, servicedCall,  online)
				select {
				case elevUpdateCh <- snapshot:
				default:
				}
			}
			if elevStateChange {
				snapshot := sync.BuildSnapshot(prevFloor, behavior, direction, common.UpdateRequests, servicedCall,  online)
				select {
				case elevUpdateCh <- snapshot:
				default:
				}
			}

		}
	}
}
