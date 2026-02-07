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
	fsmServicedCh chan<- common.Snapshot,
	fsmUpdateCh chan<- common.Snapshot,
	networkSnapshot2Ch <-chan common.Snapshot, // network -> fsm
) {
	log.Printf("fsmThread started (self=%s)", cfg.SelfKey)

	elevfsm.Fsm_init()

	inputPollRateMs := 25
	confirmTimeoutMs := 1500
	elevfsm.ConLoad("elevator.con",
		elevfsm.ConVal("inputPollRate_ms", &inputPollRateMs, "%d"),
		elevfsm.ConVal("requestConfirmTimeout_ms", &confirmTimeoutMs, "%d"),
	)

	if input.FloorSensor() == -1 {
		elevfsm.Fsm_onInitBetweenFloors()
	}

	glue := elevfsm.NewFsmGlueState(cfg)

	// Use a local channel var so we can nil it if it closes.
	netSnapCh := networkSnapshot2Ch

	// Try to load a startup snapshot (and sync lights from it if we got one).
	if snap, ok := glue.TryLoadSnapshot(ctx, netSnapCh, 2*time.Second); ok {
		elevfsm.SetAllRequestLightsFromSnapshot(snap, cfg.SelfKey)
	} else {
		// Ensure lights reflect whatever we have locally at startup (typically all off).
		elevfsm.SetAllRequestLightsFromSnapshot(glue.Snapshot(), cfg.SelfKey)
	}

	var prevReq [common.N_FLOORS][common.N_BUTTONS]int
	prevFloor := -1
	var pendingAt [common.N_FLOORS][common.N_BUTTONS]time.Time
	var injected [common.N_FLOORS][common.N_BUTTONS]bool
	assignerSeen := false
	var lastNetSnap common.Snapshot
	var hasNetSnap bool

	ticker := time.NewTicker(time.Duration(inputPollRateMs) * time.Millisecond)
	defer ticker.Stop()

	logPending := func(f int, b elevio.ButtonType, reason string) {
		log.Printf("fsmThread: pending request f=%d b=%s (%s)", f, common.ElevioButtonToString(b), reason)
	}
	injectReq := func(f int, b elevio.ButtonType, reason string) {
		if injected[f][b] {
			return
		}
		log.Printf("fsmThread: inject request f=%d b=%s (%s)", f, common.ElevioButtonToString(b), reason)
		elevfsm.Fsm_onRequestButtonPress(f, b)
		injected[f][b] = true
		pendingAt[f][b] = time.Time{}
	}
	clearInjectedIfSnapshotCleared := func(snap common.Snapshot) {
		// Hall
		if snap.HallRequests != nil {
			for f := 0; f < common.N_FLOORS; f++ {
				if f >= len(snap.HallRequests) {
					break
				}
				if !snap.HallRequests[f][0] {
					injected[f][elevio.BT_HallUp] = false
				}
				if !snap.HallRequests[f][1] {
					injected[f][elevio.BT_HallDown] = false
				}
			}
		}
		// Cab (self)
		if snap.States != nil {
			if st, ok := snap.States[cfg.SelfKey]; ok && st.CabRequests != nil {
				for f := 0; f < common.N_FLOORS; f++ {
					if f >= len(st.CabRequests) {
						break
					}
					if !st.CabRequests[f] {
						injected[f][elevio.BT_Cab] = false
					}
				}
			}
		}
	}
	countHall := func(snap common.Snapshot) int {
		if snap.HallRequests == nil {
			return 0
		}
		n := 0
		for f := 0; f < len(snap.HallRequests) && f < common.N_FLOORS; f++ {
			if snap.HallRequests[f][0] {
				n++
			}
			if snap.HallRequests[f][1] {
				n++
			}
		}
		return n
	}
	countCabSelf := func(snap common.Snapshot) int {
		if snap.States == nil {
			return 0
		}
		st, ok := snap.States[cfg.SelfKey]
		if !ok || st.CabRequests == nil {
			return 0
		}
		n := 0
		for f := 0; f < len(st.CabRequests) && f < common.N_FLOORS; f++ {
			if st.CabRequests[f] {
				n++
			}
		}
		return n
	}
	isHallReqNet := func(f int, isUp bool) bool {
		if !hasNetSnap || lastNetSnap.HallRequests == nil || f < 0 || f >= len(lastNetSnap.HallRequests) {
			return false
		}
		if isUp {
			return lastNetSnap.HallRequests[f][0]
		}
		return lastNetSnap.HallRequests[f][1]
	}
	isCabReqNet := func(f int) bool {
		if !hasNetSnap || lastNetSnap.States == nil {
			return false
		}
		st, ok := lastNetSnap.States[cfg.SelfKey]
		if !ok || st.CabRequests == nil || f < 0 || f >= len(st.CabRequests) {
			return false
		}
		return st.CabRequests[f]
	}
	tryConfirmAndInject := func(reason string) {
		if !hasNetSnap {
			return
		}
		for f := 0; f < common.N_FLOORS; f++ {
			// Hall up/down (only if assigned to us)
			for d := 0; d < 2; d++ {
				isUp := d == 0
				btn := elevio.BT_HallUp
				if !isUp {
					btn = elevio.BT_HallDown
				}
				if !glue.IsAssignedHall(f, isUp) {
					// If we have a pending local hall request but assignment says "not ours",
					// drop the pending to avoid serving another elevator's task.
					if assignerSeen && !pendingAt[f][btn].IsZero() && isHallReqNet(f, isUp) {
						log.Printf("fsmThread: dropping pending hall request f=%d b=%s (assigned elsewhere)", f, common.ElevioButtonToString(btn))
						pendingAt[f][btn] = time.Time{}
					}
					continue
				}
				if isHallReqNet(f, isUp) && !injected[f][btn] {
					injectReq(f, btn, reason)
				}
			}

			// Cab (self)
			if isCabReqNet(f) && !injected[f][elevio.BT_Cab] {
				injectReq(f, elevio.BT_Cab, reason)
			}
		}
	}

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

			lastNetSnap = snap
			hasNetSnap = true
			log.Printf("fsmThread: network snapshot received (hall=%d, cab_self=%d)", countHall(snap), countCabSelf(snap))

			// Merge snapshot into local view (includes self cab requests for lamp sync).
			glue.MergeNetworkSnapshot(snap)

			// Turn on/off lights based on the snapshot we just received:
			// - Hall lamps from snap.HallRequests
			// - Cab lamps from snap.States[self].CabRequests
			elevfsm.SetAllRequestLightsFromSnapshot(glue.Snapshot(), cfg.SelfKey)

			clearInjectedIfSnapshotCleared(snap)
			tryConfirmAndInject("network-confirmed")

		case task := <-assignerOutputCh:
			glue.ApplyAssignerTask(task)
			assignerSeen = true
			log.Printf("fsmThread: assigner task updated")

			tryConfirmAndInject("assigner-confirmed")

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
						switch elevio.ButtonType(b) {
						case elevio.BT_HallUp:
							glue.SetHallButton(f, true, true)
							changedNew = true
							if pendingAt[f][b].IsZero() && !injected[f][b] {
								pendingAt[f][b] = time.Now()
								logPending(f, elevio.ButtonType(b), "local hall press (awaiting network)")
							}
						case elevio.BT_HallDown:
							glue.SetHallButton(f, false, true)
							changedNew = true
							if pendingAt[f][b].IsZero() && !injected[f][b] {
								pendingAt[f][b] = time.Now()
								logPending(f, elevio.ButtonType(b), "local hall press (awaiting network)")
							}
						case elevio.BT_Cab:
							glue.SetCabRequest(f, true)
							changedNew = true
							if pendingAt[f][b].IsZero() && !injected[f][b] {
								pendingAt[f][b] = time.Now()
								logPending(f, elevio.ButtonType(b), "local cab press (awaiting network)")
							}
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
					log.Printf("fsmThread: serviced requests at floor %d", prevFloor)
				}
			}

			// Confirmed requests: inject when coherent snapshot agrees
			tryConfirmAndInject("network-confirmed")

			// Timeout fallback: if no confirmation in time, serve locally
			timeout := time.Duration(confirmTimeoutMs) * time.Millisecond
			now := time.Now()
			for f := 0; f < common.N_FLOORS; f++ {
				for b := 0; b < common.N_BUTTONS; b++ {
					if pendingAt[f][b].IsZero() || injected[f][b] {
						continue
					}
					if now.Sub(pendingAt[f][b]) < timeout {
						continue
					}

					btn := elevio.ButtonType(b)
					if (btn == elevio.BT_HallUp || btn == elevio.BT_HallDown) && !assignerSeen {
						// Don't fallback for hall requests before we have any assigner decision.
						// Reset timer to avoid log spam and keep waiting for assignment.
						log.Printf("fsmThread: timeout reached but no assigner yet for hall request f=%d b=%s", f, common.ElevioButtonToString(btn))
						pendingAt[f][b] = now
						continue
					}
					// If assigned elsewhere (and we have coherent assignment), do not fallback.
					if (btn == elevio.BT_HallUp || btn == elevio.BT_HallDown) && assignerSeen && hasNetSnap {
						isUp := btn == elevio.BT_HallUp
						if isHallReqNet(f, isUp) && !glue.IsAssignedHall(f, isUp) {
							log.Printf("fsmThread: timeout reached but hall request assigned elsewhere f=%d b=%s", f, common.ElevioButtonToString(btn))
							pendingAt[f][b] = time.Time{}
							continue
						}
					}

					injectReq(f, btn, "timeout-fallback")
				}
			}

			// If anything changed, sync lamps from our current glue snapshot
			// (so the FSM won't overwrite network-based lamps).
			if changedNew || changedServiced {
				snap := glue.Snapshot()
				elevfsm.SetAllRequestLightsFromSnapshot(snap, cfg.SelfKey)

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
