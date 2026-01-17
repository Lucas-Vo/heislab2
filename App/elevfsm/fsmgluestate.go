package elevfsm

import (
	"context"
	"log"
	"time"

	"elevator/common"
)

type GlueUpdateKind int

const (
	GlueNewRequests GlueUpdateKind = iota
	GlueServiced
)

type FsmGlueState struct {
	selfKey string

	hallRequests [][2]bool
	states       map[string]common.ElevState

	assignedHall [][2]bool
}

func NewFsmGlueState(cfg common.Config) *FsmGlueState {
	s := &FsmGlueState{
		selfKey:      cfg.SelfKey,
		hallRequests: make([][2]bool, common.N_FLOORS),
		states:       make(map[string]common.ElevState),
		assignedHall: make([][2]bool, common.N_FLOORS),
	}
	s.states[s.selfKey] = common.ElevState{
		Behavior:    "idle",
		Floor:       0,
		Direction:   "stop",
		CabRequests: make([]bool, common.N_FLOORS),
	}
	return s
}

func (s *FsmGlueState) SelfKey() string { return s.selfKey }

func (s *FsmGlueState) TryLoadSnapshot(ctx context.Context, snapshotFromNetwork <-chan common.NetworkState, timeout time.Duration) {
	if snapshotFromNetwork == nil {
		return
	}
	t := time.NewTimer(timeout)
	defer t.Stop()

	select {
	case snap := <-snapshotFromNetwork:
		if snap.HallRequests != nil {
			s.hallRequests = cloneHall(snap.HallRequests)
		}
		if snap.States != nil {
			s.states = cloneStates(snap.States)
		}
		if _, ok := s.states[s.selfKey]; !ok {
			s.states[s.selfKey] = common.ElevState{
				Behavior:    "idle",
				Floor:       0,
				Direction:   "stop",
				CabRequests: make([]bool, common.N_FLOORS),
			}
		}
		log.Printf("FSM loaded snapshot from network")
	case <-t.C:
		log.Printf("FSM snapshot timeout; continuing without snapshot")
	case <-ctx.Done():
		return
	}
}

func (s *FsmGlueState) ApplyAssignerTask(task common.ElevInput) {
	if task.HallTask != nil {
		s.assignedHall = cloneHall(task.HallTask)
	}
}

func (s *FsmGlueState) SetHallRequest(f int, up bool, down bool) {
	if f < 0 || f >= len(s.hallRequests) {
		return
	}
	s.hallRequests[f][0] = up
	s.hallRequests[f][1] = down
}

func (s *FsmGlueState) SetHallButton(f int, isUp bool, v bool) {
	if f < 0 || f >= len(s.hallRequests) {
		return
	}
	if isUp {
		s.hallRequests[f][0] = v
	} else {
		s.hallRequests[f][1] = v
	}
}

func (s *FsmGlueState) SetCabRequest(f int, v bool) {
	st := s.states[s.selfKey]
	if st.CabRequests == nil {
		st.CabRequests = make([]bool, common.N_FLOORS)
	}
	if f >= 0 && f < len(st.CabRequests) {
		st.CabRequests[f] = v
	}
	s.states[s.selfKey] = st
}

func (s *FsmGlueState) SetFloor(f int) {
	st := s.states[s.selfKey]
	st.Floor = f
	s.states[s.selfKey] = st
}

// Heuristic placeholder until your FSM exposes served events precisely:
func (s *FsmGlueState) ClearAtCurrentFloorIfAny() bool {
	st := s.states[s.selfKey]
	f := st.Floor
	if f < 0 || f >= len(s.hallRequests) {
		return false
	}

	changed := false
	if s.assignedHall != nil && f < len(s.assignedHall) {
		if s.assignedHall[f][0] || s.assignedHall[f][1] {
			s.assignedHall[f][0] = false
			s.assignedHall[f][1] = false
			changed = true
		}
	}
	if s.hallRequests[f][0] || s.hallRequests[f][1] {
		s.hallRequests[f][0] = false
		s.hallRequests[f][1] = false
		changed = true
	}
	return changed
}

func (s *FsmGlueState) Snapshot() common.NetworkState {
	return common.NetworkState{
		HallRequests: cloneHall(s.hallRequests),
		States:       cloneStates(s.states),
	}
}

func cloneHall(in [][2]bool) [][2]bool {
	if in == nil {
		return nil
	}
	out := make([][2]bool, len(in))
	copy(out, in)
	return out
}

func cloneStates(in map[string]common.ElevState) map[string]common.ElevState {
	out := make(map[string]common.ElevState, len(in))
	for k, st := range in {
		out[k] = common.CopyElevState(st)
	}
	return out
}
