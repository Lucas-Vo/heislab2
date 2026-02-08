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
	elevfsm.Fsm_init()

	inputPollRateMs := 25
	elevfsm.ConLoad("elevator.con",
		elevfsm.ConVal("inputPollRate_ms", &inputPollRateMs, "%d"),
	)
	confirmTimeout := 200 * time.Millisecond
	doorOpenDuration := elevfsm.DoorOpenDuration()

	initFloor, ok := initAtKnownFloor(ctx, input, time.Duration(inputPollRateMs)*time.Millisecond)
	if !ok {
		return
	}

	sync := elevfsm.NewFsmSync(cfg)
	var prevReq [common.N_FLOORS][common.N_BUTTONS]int
	prevFloor := initFloor
	lastFloorSeen := time.Now()
	stuckWarned := false
	onlineKnown := false
	prevOnline := false
	prevObstructed := false
	timerPaused := false

	ticker := time.NewTicker(time.Duration(inputPollRateMs) * time.Millisecond)
	defer ticker.Stop()

	behavior, direction := elevfsm.CurrentMotionStrings()

	for {
		select {
		case <-ctx.Done():
			return

		case snap := <-netWorldView2Ch:
			now := time.Now()
			sync.ApplyNetworkSnapshot(snap, now)
			log.Printf("fsmThread: net snapshot hall=%d cab_self=%d", countHall(snap.HallRequests), countCabFromSnapshot(snap, cfg.SelfKey))

			online := !sync.Offline(now)
			if online {
				sync.TryInjectOnline()
			}
			sync.ApplyLights(sync.LightsSnapshot(online))

		case task := <-assignerOutputCh:
			sync.ApplyAssigner(task)
			log.Printf("fsmThread: assigner update assigned_hall=%d", sync.AssignedHallCount())
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
					}
					prevReq[f][b] = v
				}
			}

			// Floor sensor
			f := input.FloorSensor()
			if f != -1 && f != prevFloor {
				elevfsm.Fsm_onFloorArrival(f)
				prevFloor = f
				lastFloorSeen = now
				stuckWarned = false
				changedNew = true
			}
			if f == -1 && now.Sub(lastFloorSeen) > 5*time.Second && !stuckWarned {
				log.Printf("fsmThread: warning: floor sensor reports -1 for >5s (possible hardware/sensor issue)")
				stuckWarned = true
			}

			// Obstruction handling: keep door open while obstructed; restart timer when cleared.
			obstructed := input.Obstruction() != 0
			if elevfsm.CurrentBehaviour() == elevfsm.EB_DoorOpen {
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
				elevfsm.Fsm_onDoorTimeout()

				cleared = sync.ClearAtFloor(prevFloor, online)
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

			behavior, direction = elevfsm.CurrentMotionStrings()
			if sync.MotionChanged(prevFloor, behavior, direction) {
				changedNew = true
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

func countHall(hall [][2]bool) int {
	if hall == nil {
		return 0
	}
	n := 0
	for i := 0; i < len(hall) && i < common.N_FLOORS; i++ {
		if hall[i][0] {
			n++
		}
		if hall[i][1] {
			n++
		}
	}
	return n
}

func countCabFromSnapshot(snap common.Snapshot, selfKey string) int {
	if snap.States == nil {
		return 0
	}
	st, ok := snap.States[selfKey]
	if !ok || st.CabRequests == nil {
		return 0
	}
	n := 0
	for i := 0; i < len(st.CabRequests) && i < common.N_FLOORS; i++ {
		if st.CabRequests[i] {
			n++
		}
	}
	return n
}

func initAtKnownFloor(ctx context.Context, input common.ElevInputDevice, poll time.Duration) (int, bool) {
	f := input.FloorSensor()
	if f != -1 {
		// Initialize FSM floor state immediately to avoid floor=-1 in request handling.
		elevfsm.Fsm_onFloorArrival(f)
		return f, true
	}

	// Between floors: drive down until a floor is detected.
	elevfsm.Fsm_onInitBetweenFloors()

	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return -1, false
		case <-ticker.C:
			f = input.FloorSensor()
			if f != -1 {
				elevfsm.Fsm_onFloorArrival(f)
				return f, true
			}
		}
	}
}
