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
	fsmServicedCh chan<- common.NetworkState,
	fsmUpdateCh chan<- common.NetworkState,
	networkSnapshot2Ch <-chan common.NetworkState, // network -> fsm
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

	// Use a local channel var so we can nil it if it closes.
	netSnapCh := networkSnapshot2Ch

	// Try to load a startup snapshot (and sync lights from it if we got one).
	if snap, ok := glue.TryLoadSnapshot(ctx, netSnapCh, 2*time.Second); ok {
		elevfsm.SetAllRequestLightsFromNetworkState(snap, cfg.SelfKey)
	} else {
		// Ensure lights reflect whatever we have locally at startup (typically all off).
		elevfsm.SetAllRequestLightsFromNetworkState(glue.Snapshot(), cfg.SelfKey)
	}

	var prevReq [common.N_FLOORS][common.N_BUTTONS]int
	prevFloor := -1

	ticker := time.NewTicker(time.Duration(inputPollRateMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		// NEW: whenever we receive a network snapshot, update glue + lights.
		case snap, ok := <-netSnapCh:
			if !ok {
				netSnapCh = nil
				continue
			}

			// Merge snapshot into local view (includes self cab requests for lamp sync).
			glue.MergeNetworkSnapshot(snap)

			// Turn on/off lights based on the snapshot we just received:
			// - Hall lamps from snap.HallRequests
			// - Cab lamps from snap.States[self].CabRequests
			elevfsm.SetAllRequestLightsFromNetworkState(glue.Snapshot(), cfg.SelfKey)

		case task := <-assignerOutputCh:
			glue.ApplyAssignerTask(task)

			// optional: publish update so network/assigner sees we’re alive
			select {
			case fsmUpdateCh <- glue.Snapshot():
			default:
			}

		case <-ticker.C:
			changedNew := false
			changedServiced := false

			// Request buttons (edge-detected)
			for f := 0; f < common.N_FLOORS; f++ {
				for b := 0; b < common.N_BUTTONS; b++ {
					v := input.RequestButton(f, elevio.ButtonType(b))
					if v != 0 && v != prevReq[f][b] {
						elevfsm.Fsm_onRequestButtonPress(f, elevio.ButtonType(b))

						switch elevio.ButtonType(b) {
						case elevio.BT_HallUp:
							glue.SetHallButton(f, true, true)
							changedNew = true
						case elevio.BT_HallDown:
							glue.SetHallButton(f, false, true)
							changedNew = true
						case elevio.BT_Cab:
							glue.SetCabRequest(f, true)
							changedNew = true
						}
					}
					prevReq[f][b] = v
				}
			}

			// Floor sensor
			f := input.FloorSensor()
			if f != -1 && f != prevFloor {
				elevfsm.Fsm_onFloorArrival(f)
				glue.SetFloor(f)
				changedNew = true
			}
			prevFloor = f

			// Timer
			if elevfsm.Timer_timedOut() != 0 {
				elevfsm.Timer_stop()
				elevfsm.Fsm_onDoorTimeout()

				if glue.ClearAtCurrentFloorIfAny() {
					changedServiced = true
				}
			}

			// If anything changed, sync lamps from our current glue snapshot
			// (so the FSM won't overwrite network-based lamps).
			if changedNew || changedServiced {
				snap := glue.Snapshot()
				elevfsm.SetAllRequestLightsFromNetworkState(snap, cfg.SelfKey)

				// Publish FULL state to network thread
				if changedServiced {
					select {
					case fsmServicedCh <- snap:
					default:
					}
				}
				if changedNew {
					select {
					case fsmUpdateCh <- snap:
					default:
					}
				}
			}
		}
	}
}
