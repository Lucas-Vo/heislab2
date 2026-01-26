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

	assignerOutput <-chan common.ElevInput,
	elevalgoServiced chan<- common.NetworkState,
	elevalgoLaManana chan<- common.NetworkState,
	snapshotFromNetwork <-chan common.NetworkState,
) {
	log.Printf("fsmThread started (self=%s)", cfg.SelfKey)

	elevfsm.Fsm_init()

	inputPollRateMs := 25
	elevfsm.ConLoad("elevator.con",
		elevfsm.ConVal("inputPollRate_ms", &inputPollRateMs, "%d"),
	)

	if input.FloorSensor() == -1 {
		elevfsm.Fsm_onInitBetweenFloors()
	}

	glue := elevfsm.NewFsmGlueState(cfg)
	glue.TryLoadSnapshot(ctx, snapshotFromNetwork, 2*time.Second)

	// Edge-detect: physical buttons (requests received)
	var prevBtn [common.N_FLOORS][common.N_BUTTONS]int

	// Edge-detect: assigned hall tasks (requests we should take)
	prevAssigned := make([][2]bool, common.N_FLOORS)

	// Edge-detect: cab requests coming from snapshot (requests we should take)
	lastSnapCab := make([]bool, common.N_FLOORS)

	prevFloor := -1

	ticker := time.NewTicker(time.Duration(inputPollRateMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		// Hall tasks assigned to THIS elevator => drive FSM
		case task := <-assignerOutput:
			changedNew := false

			if task.HallTask != nil && len(task.HallTask) == common.N_FLOORS {
				for f := 0; f < common.N_FLOORS; f++ {
					if task.HallTask[f][0] && !prevAssigned[f][0] {
						elevfsm.Fsm_onRequestButtonPress(f, elevio.BT_HallUp)
						changedNew = true
					}
					if task.HallTask[f][1] && !prevAssigned[f][1] {
						elevfsm.Fsm_onRequestButtonPress(f, elevio.BT_HallDown)
						changedNew = true
					}
				}
				copy(prevAssigned, task.HallTask)

				// keep assignment so we can clear “served assigned hall calls”
				glue.ApplyAssignerTask(task)
			}

			if changedNew {
				select {
				case elevalgoLaManana <- glue.Snapshot():
				default:
				}
			}

		// Snapshot is authoritative for which CAB requests we should take => drive FSM
		case snap := <-snapshotFromNetwork:
			changedNew := false

			// Merge snapshot into glue for visibility but DON'T overwrite self cab "received" list
			glue.MergeNetworkSnapshotNoSelfCab(snap)

			// Drive FSM from snapshot's cab queue for self
			if snap.States != nil {
				if st, ok := snap.States[cfg.SelfKey]; ok && st.CabRequests != nil {
					for f := 0; f < common.N_FLOORS && f < len(st.CabRequests); f++ {
						snapCab := st.CabRequests[f]

						// rising edge => new cab order to execute
						if snapCab && !lastSnapCab[f] {
							elevfsm.Fsm_onRequestButtonPress(f, elevio.BT_Cab)
							changedNew = true
						}
						lastSnapCab[f] = snapCab
					}
				}
			}

			if changedNew {
				select {
				case elevalgoLaManana <- glue.Snapshot():
				default:
				}
			}

		// Poll hardware for "received" requests + floor/timer driving
		case <-ticker.C:
			changedNew := false
			changedServiced := false

			// 1) Poll buttons: record + publish received requests (no FSM injection here)
			for f := 0; f < common.N_FLOORS; f++ {
				for b := 0; b < common.N_BUTTONS; b++ {
					v := input.RequestButton(f, elevio.ButtonType(b))
					if v != 0 && v != prevBtn[f][b] {
						switch elevio.ButtonType(b) {
						case elevio.BT_HallUp:
							glue.SetHallButton(f, true, true)
							changedNew = true

						case elevio.BT_HallDown:
							glue.SetHallButton(f, false, true)
							changedNew = true

						case elevio.BT_Cab:
							glue.SetCabRequest(f, true) // received locally; snapshot decides when to take it
							changedNew = true
						}
					}
					prevBtn[f][b] = v
				}
			}

			// 2) Floor sensor drives FSM
			f := input.FloorSensor()
			if f != -1 && f != prevFloor {
				elevfsm.Fsm_onFloorArrival(f)
				glue.SetFloor(f)
				changedNew = true
			}
			prevFloor = f

			// 3) Door timer drives FSM; clear served in glue and publish serviced
			if elevfsm.Timer_timedOut() != 0 {
				elevfsm.Timer_stop()
				elevfsm.Fsm_onDoorTimeout()

				if glue.ClearAtCurrentFloorIfAny() {
					changedServiced = true
				}
			}

			if changedServiced {
				select {
				case elevalgoServiced <- glue.Snapshot():
				default:
				}
			}
			if changedNew {
				select {
				case elevalgoLaManana <- glue.Snapshot():
				default:
				}
			}
		}
	}
}
