package main

import (
	elevio "Driver-go/elevio"
	"context"
	"log"
	"time"

	"elevator/common"
	"elevator/elevfsm"
)

const (
	netOfflineTimeout = 3 * time.Second

	ELEV_EXECUTABLE = "elev_algo"
)

func fsmThread(
	ctx context.Context,
	config common.Config,
	input common.ElevInputDevice,
	assignerOutputCh <-chan common.ElevInput,
	elevServicedCh chan<- common.Snapshot,
	elevUpdateCh chan<- common.Snapshot,
	netWorldView2Ch <-chan common.Snapshot, // network -> fsm
) {
	log.Printf("fsmThread started (self=%s)", config.SelfKey)

	selfKey := config.SelfKey

	inputPollRateMs := 25
	confirmTimeoutMs := 1500

	//maybe remove
	elevfsm.ConLoad("elevator.con",
		elevfsm.ConVal("inputPollRate_ms", &inputPollRateMs, "%d"),
		elevfsm.ConVal("requestConfirmTimeout_ms", &confirmTimeoutMs, "%d"),
	)
	elevator := elevfsm.Elevator{}
	output := common.ElevOutputDevice{}
	elevator, output = elevfsm.FsmInit(&elevator, &output)

	initFloor := input.FloorSensor()
	if initFloor == -1 {
		elevfsm.FsmOnInitBetweenFloors(&elevator, &output)
	} else {
		// Initialize FSM floor state immediately to avoid floor=-1 in request handling.
		elevfsm.FsmOnFloorArrival(&elevator, &output, initFloor, false)
	}

	lastNetPack := time.Now()
	lastAssignerPack := time.Now()
	lastFloorUpdate := time.Now()
	initializedFromNetwork := false
	stuckWarned := false
	networkConnected := false

	ticker := time.NewTicker(time.Duration(inputPollRateMs) * time.Millisecond)
	defer ticker.Stop()

	prevButtons := [common.N_FLOORS][common.N_BUTTONS]bool{}
	prevFloor := -1

	for {
		select {
		case <-ctx.Done():
			return

		case snap := <-netWorldView2Ch:
			now := time.Now()
			networkConnected = true
			lastNetPack = now
			if !initializedFromNetwork {
				log.Printf("fsmThread: received first network snapshot; initializing state from network")
				//TODO add init here
				initializedFromNetwork = true
			}

			log.Printf("fsmThread: net snapshot hall=%d cab_self=%d", elevfsm.CountHall(snap.HallRequests), elevfsm.CountCabFromSnapshot(snap, selfKey))

			elevfsm.ServiceLights(output, snap, selfKey, networkConnected)

		case task := <-assignerOutputCh:
			elevfsm.ApplyAssigner(&elevator, task)

			log.Printf("fsmThread: assigner update")
			now := time.Now()
			lastAssignerPack = now

		case <-ticker.C:
			now := time.Now()
			timeSinceLastNet := now.Sub(lastNetPack)
			if timeSinceLastNet > netOfflineTimeout {
				if networkConnected {
					log.Printf("fsmThread: network connection lost (last packet %.1fs ago); entering offline mode", timeSinceLastNet.Seconds())
					networkConnected = false
				}
			}
			timeSinceLastAssigner := now.Sub(lastAssignerPack)
			if timeSinceLastAssigner > netOfflineTimeout {
				if lastAssignerPack != (time.Time{}) {
					log.Printf("fsmThread: assigner connection lost (last packet %.1fs ago)", timeSinceLastAssigner.Seconds())
				}
			}

			newOrder := false
			changedServiced := false

			// Request buttons (edge-detected)
			// Request buttons
			for f := 0; f < common.N_FLOORS; f++ {
				for b := 0; b < common.N_BUTTONS; b++ {
					v := input.RequestButton(f, elevio.ButtonType(b)) != 0
					if v && !prevButtons[f][b] {
						newOrder = newOrder || elevfsm.FsmOnRequestButtonPress(&elevator, &output, f, elevio.ButtonType(b), networkConnected)
					}
					prevButtons[f][b] = v
				}
			}

			// Floor sensor
			f := input.FloorSensor()
			if f != -1 && f != prevFloor {
				changedServiced = elevfsm.FsmOnFloorArrival(&elevator, &output, f, networkConnected)
				changedServiced = true
				log.Printf("fsmThread: serviced requests at floor %d", prevFloor)

				prevFloor = f
				lastFloorUpdate = now
				stuckWarned = false
			}
			if f == -1 && now.Sub(lastFloorUpdate) > 5*time.Second && !stuckWarned { //TODO: Make this also trigger if not moving from idle
				log.Printf("fsmThread: warning: floor sensor reports -1 for >5s (possible hardware/sensor issue)")
				stuckWarned = true
			}

			// Timer
			if elevfsm.Timer_timedOut() != 0 {
				elevfsm.Timer_stop()
				elevfsm.FsmOnDoorTimeout(&elevator, &output)
			}

			// Inject confirmed requests

			if changedServiced {
				snap := elevfsm.BuildServicedSnapshot(&elevator, selfKey, &prevButtons)
				select {
				case elevServicedCh <- snap:
				default:
				}
			}
			if newOrder {
				snap := elevfsm.BuildUpdateSnapshot(&elevator, selfKey, &prevButtons)
				select {
				case elevUpdateCh <- snap:
				default:
				}
			}
		}
	}
}
