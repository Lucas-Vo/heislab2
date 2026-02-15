package main

import (
	elevio "Driver-go/elevio"
	"context"
	"log"
	"time"

	"elevator/common"
	"elevator/elevfsm"
)

func fsmThread(
	ctx context.Context,
	cfg common.Config,
	input common.ElevInputDevice,
	assignerOutputCh <-chan common.ElevInput,
	elevServicedCh chan<- common.Snapshot,
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

	var prevReq [common.N_FLOORS][common.N_BUTTONS]int

	confirmTimeout := 200 * time.Millisecond
	doorOpenDuration := elevfsm.DoorOpenDuration(sync.Elevator)
	onlineKnown := false
	prevOnline := false
	prevObstructed := false
	timerPaused := false

	// Seed floor state if the sensor is already at a floor; otherwise start moving to find one.
	prevFloor := -1
	if f := input.FloorSensor(); f != -1 {
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
			sync.ApplyNetworkSnapshot(snap, now)

			online := !sync.Offline(now)
			if online {
				sync.TryInjectOnline()
			}
			sync.ApplyLights(sync.LightsSnapshot(online))

			// log.Printf("fsmThread: netCab=%v localCab=%v", sync.NetCabCopy(), sync.LocalCabCopy())
			// log.Printf("==================================")
			// log.Printf("localhall: %v", sync.GetLocalHall())
			// log.Printf("nethall: %v", sync.GetNetHall())
			// log.Printf("localcab: %v", sync.GetLocalCab())
			// log.Printf("netcab: %v", sync.GetNetCab())
			// log.Printf("==================================")

		case task := <-assignerOutputCh:
			sync.ApplyAssigner(task)
			now := time.Now()
			if !sync.Offline(now) {
				sync.TryInjectOnline()
			}

		case <-ticker.C:
			now := time.Now()
			online := !sync.Offline(now)
			if !onlineKnown || online != prevOnline {
				state := "offline"
				if online {
					state = "online"
				}
				log.Printf("fsmThread: network %s (lastNetSeen=%s)", state, sync.LastNetSeen().Format(time.RFC3339Nano))
				prevOnline = online
				onlineKnown = true
			}
			changedNew := false
			changedServiced := false
			var cleared elevfsm.ServicedAt

			// Request buttons (edge-detected)
			for f := 0; f < common.N_FLOORS; f++ {
				for b := 0; b < common.N_BUTTONS; b++ {
					v := input.RequestButton(f, elevio.ButtonType(b))
					if v != 0 && v != prevReq[f][b] {
						sync.OnLocalPress(f, elevio.ButtonType(b), now)
						changedNew = true
						if input.FloorSensor() == f {
							elevfsm.Fsm_onRequestButtonPress(sync.Elevator, f, elevio.ButtonType(b))
						}
					}
					prevReq[f][b] = v
				}
			}

			// Floor sensor
			f := input.FloorSensor()
			if f != -1 && f != prevFloor {
				elevfsm.Fsm_onFloorArrival(sync.Elevator, f)
				prevFloor = f
				changedNew = true
			}

			// Obstruction handling: keep door open while obstructed; restart timer when cleared.
			obstructed := input.Obstruction() != 0
			if elevfsm.CurrentBehaviour(sync.Elevator) == elevfsm.EB_DoorOpen {
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
				arrivalDirn := elevfsm.CurrentDirection(sync.Elevator)
				elevfsm.Fsm_onDoorTimeout(sync.Elevator)

				cleared = sync.ClearAtFloor(prevFloor, online, arrivalDirn)
				if cleared.HallUp || cleared.HallDown || cleared.Cab {
					changedServiced = true
					log.Printf("fsmThread: serviced requests at floor %d", prevFloor)
				}
			}

			// Inject confirmed requests
			if online {
				sync.TryInjectOnline()
			} else {
				sync.TryInjectOffline(now, confirmTimeout)
			}

			sync.ApplyLights(sync.LightsSnapshot(online))

			behavior, direction = elevfsm.CurrentMotionStrings(sync.Elevator)
			if sync.MotionChanged(prevFloor, behavior, direction) {
				changedNew = true
			}

			if !sync.HasNetSelf() {
				continue
			}
			if changedServiced {
				snap := sync.BuildServicedSnapshot(prevFloor, behavior, direction, cleared, online)
				select {
				case elevServicedCh <- snap:
				default:
				}
			}
			if changedNew {
				snap := sync.BuildUpdateSnapshot(prevFloor, behavior, direction)
				select {
				case elevUpdateCh <- snap:
				default:
				}
			}
		}
	}
}
