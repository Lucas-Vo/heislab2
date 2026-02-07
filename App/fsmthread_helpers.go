package main

import (
	elevio "Driver-go/elevio"
	"log"
	"time"

	"elevator/common"
	"elevator/elevfsm"
)

type fsmPendingTracker struct {
	cfg          common.Config
	glue         *elevfsm.FsmGlueState
	pendingAt    [common.N_FLOORS][common.N_BUTTONS]time.Time
	injected     [common.N_FLOORS][common.N_BUTTONS]bool
	assignerSeen bool
	lastNetSnap  common.Snapshot
	hasNetSnap   bool
}

func newFsmPendingTracker(cfg common.Config, glue *elevfsm.FsmGlueState) *fsmPendingTracker {
	return &fsmPendingTracker{cfg: cfg, glue: glue}
}

func (p *fsmPendingTracker) SetAssignerSeen() {
	p.assignerSeen = true
}

func (p *fsmPendingTracker) UpdateNetSnap(snap common.Snapshot) {
	p.lastNetSnap = snap
	p.hasNetSnap = true
}

func (p *fsmPendingTracker) CountHall(snap common.Snapshot) int {
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

func (p *fsmPendingTracker) CountCabSelf(snap common.Snapshot) int {
	if snap.States == nil {
		return 0
	}
	st, ok := snap.States[p.cfg.SelfKey]
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

func (p *fsmPendingTracker) LogPending(f int, b elevio.ButtonType, reason string) {
	log.Printf("fsmThread: pending request f=%d b=%s (%s)", f, common.ElevioButtonToString(b), reason)
}

func (p *fsmPendingTracker) MarkPendingIfNeeded(f int, b elevio.ButtonType, reason string) {
	if p.pendingAt[f][b].IsZero() && !p.injected[f][b] {
		p.pendingAt[f][b] = time.Now()
		p.LogPending(f, b, reason)
	}
}

func (p *fsmPendingTracker) InjectReq(f int, b elevio.ButtonType, reason string) {
	if p.injected[f][b] {
		return
	}
	log.Printf("fsmThread: inject request f=%d b=%s (%s)", f, common.ElevioButtonToString(b), reason)
	elevfsm.Fsm_onRequestButtonPress(f, b)
	p.injected[f][b] = true
	p.pendingAt[f][b] = time.Time{}
}

func (p *fsmPendingTracker) ClearInjectedIfSnapshotCleared(snap common.Snapshot) {
	// Hall
	if snap.HallRequests != nil {
		for f := 0; f < common.N_FLOORS; f++ {
			if f >= len(snap.HallRequests) {
				break
			}
			if !snap.HallRequests[f][0] {
				p.injected[f][elevio.BT_HallUp] = false
			}
			if !snap.HallRequests[f][1] {
				p.injected[f][elevio.BT_HallDown] = false
			}
		}
	}
	// Cab (self)
	if snap.States != nil {
		if st, ok := snap.States[p.cfg.SelfKey]; ok && st.CabRequests != nil {
			for f := 0; f < common.N_FLOORS; f++ {
				if f >= len(st.CabRequests) {
					break
				}
				if !st.CabRequests[f] {
					p.injected[f][elevio.BT_Cab] = false
				}
			}
		}
	}
}

func (p *fsmPendingTracker) IsHallReqNet(f int, isUp bool) bool {
	if !p.hasNetSnap || p.lastNetSnap.HallRequests == nil || f < 0 || f >= len(p.lastNetSnap.HallRequests) {
		return false
	}
	if isUp {
		return p.lastNetSnap.HallRequests[f][0]
	}
	return p.lastNetSnap.HallRequests[f][1]
}

func (p *fsmPendingTracker) IsCabReqNet(f int) bool {
	if !p.hasNetSnap || p.lastNetSnap.States == nil {
		return false
	}
	st, ok := p.lastNetSnap.States[p.cfg.SelfKey]
	if !ok || st.CabRequests == nil || f < 0 || f >= len(st.CabRequests) {
		return false
	}
	return st.CabRequests[f]
}

func (p *fsmPendingTracker) TryConfirmAndInject(reason string) {
	if !p.hasNetSnap {
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
			if !p.glue.IsAssignedHall(f, isUp) {
				// If we have a pending local hall request but assignment says "not ours",
				// drop the pending to avoid serving another elevator's task.
				if p.assignerSeen && !p.pendingAt[f][btn].IsZero() && p.IsHallReqNet(f, isUp) {
					log.Printf("fsmThread: dropping pending hall request f=%d b=%s (assigned elsewhere)", f, common.ElevioButtonToString(btn))
					p.pendingAt[f][btn] = time.Time{}
				}
				continue
			}
			if p.IsHallReqNet(f, isUp) && !p.injected[f][btn] {
				p.InjectReq(f, btn, reason)
			}
		}

		// Cab (self)
		if p.IsCabReqNet(f) && !p.injected[f][elevio.BT_Cab] {
			p.InjectReq(f, elevio.BT_Cab, reason)
		}
	}
}

func (p *fsmPendingTracker) TimeoutFallback(timeout time.Duration) {
	now := time.Now()
	for f := 0; f < common.N_FLOORS; f++ {
		for b := 0; b < common.N_BUTTONS; b++ {
			if p.pendingAt[f][b].IsZero() || p.injected[f][b] {
				continue
			}
			if now.Sub(p.pendingAt[f][b]) < timeout {
				continue
			}

			btn := elevio.ButtonType(b)
			if (btn == elevio.BT_HallUp || btn == elevio.BT_HallDown) && !p.assignerSeen {
				// Don't fallback for hall requests before we have any assigner decision.
				// Reset timer to avoid log spam and keep waiting for assignment.
				log.Printf("fsmThread: timeout reached but no assigner yet for hall request f=%d b=%s", f, common.ElevioButtonToString(btn))
				p.pendingAt[f][b] = now
				continue
			}
			if (btn == elevio.BT_HallUp || btn == elevio.BT_HallDown) && !p.IsHallReqNet(f, btn == elevio.BT_HallUp) {
				// Require network confirmation for hall requests even on timeout fallback.
				log.Printf("fsmThread: timeout reached but hall request not confirmed by network f=%d b=%s", f, common.ElevioButtonToString(btn))
				p.pendingAt[f][b] = now
				continue
			}
			// If assigned elsewhere (and we have coherent assignment), do not fallback.
			if (btn == elevio.BT_HallUp || btn == elevio.BT_HallDown) && p.assignerSeen && p.hasNetSnap {
				isUp := btn == elevio.BT_HallUp
				if p.IsHallReqNet(f, isUp) && !p.glue.IsAssignedHall(f, isUp) {
					log.Printf("fsmThread: timeout reached but hall request assigned elsewhere f=%d b=%s", f, common.ElevioButtonToString(btn))
					p.pendingAt[f][b] = time.Time{}
					continue
				}
			}

			p.InjectReq(f, btn, "timeout-fallback")
		}
	}
}
