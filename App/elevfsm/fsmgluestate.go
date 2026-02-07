package elevfsm

import (
	"context"
	"log"
	"time"

	"elevator/common"
)

// FsmGlueState is a "glue layer" between:
//  1. the local elevator FSM
//  2. the network/assigner layer
type FsmGlueState struct {
	selfKey string

	// building-wide hall request matrix
	hallRequests [][2]bool

	// per-elevator states
	states map[string]common.ElevState

	// liveness map
	alive map[string]bool

	// which hall calls THIS elevator is assigned to serve
	assignedHall [][2]bool
}

func NewFsmGlueState(cfg common.Config) *FsmGlueState {
	s := &FsmGlueState{
		selfKey:      cfg.SelfKey,
		hallRequests: make([][2]bool, common.N_FLOORS),
		states:       make(map[string]common.ElevState),
		alive:        make(map[string]bool),
		assignedHall: make([][2]bool, common.N_FLOORS),
	}

	s.states[s.selfKey] = common.ElevState{
		Behavior:    "idle",
		Floor:       0,
		Direction:   "stop",
		CabRequests: make([]bool, common.N_FLOORS),
	}

	s.alive[s.selfKey] = true
	return s
}

func (s *FsmGlueState) SelfKey() string { return s.selfKey }

// UPDATED: now returns (snapshot, ok). Also uses MergeNetworkSnapshot (includes self cab requests).
func (s *FsmGlueState) TryLoadSnapshot(
	ctx context.Context,
	snapshotFromNetwork <-chan common.Snapshot,
	timeout time.Duration,
) (common.Snapshot, bool) {
	if snapshotFromNetwork == nil {
		return common.Snapshot{}, false
	}

	t := time.NewTimer(timeout)
	defer t.Stop()

	select {
	case snap := <-snapshotFromNetwork:
		s.MergeNetworkSnapshot(snap)

		if _, ok := s.states[s.selfKey]; !ok {
			s.states[s.selfKey] = common.ElevState{
				Behavior:    "idle",
				Floor:       0,
				Direction:   "stop",
				CabRequests: make([]bool, common.N_FLOORS),
			}
		}

		if s.alive == nil {
			s.alive = make(map[string]bool)
		}
		s.alive[s.selfKey] = true

		log.Printf("FSM loaded snapshot from network")
		return snap, true

	case <-t.C:
		log.Printf("FSM snapshot timeout; continuing without snapshot")
		return common.Snapshot{}, false

	case <-ctx.Done():
		return common.Snapshot{}, false
	}
}

func (s *FsmGlueState) ApplyAssignerTask(task common.ElevInput) {
	if task.HallTask != nil {
		s.assignedHall = cloneHall(task.HallTask)
	}
}

func (s *FsmGlueState) IsAssignedHall(f int, isUp bool) bool {
	if s.assignedHall == nil || f < 0 || f >= len(s.assignedHall) {
		return false
	}
	if isUp {
		return s.assignedHall[f][0]
	}
	return s.assignedHall[f][1]
}

// NEW: merges snapshot and ALSO merges self cab requests (so cab lamps can be driven by snapshots).
func (s *FsmGlueState) MergeNetworkSnapshot(snap common.Snapshot) {
	// First do the old merge (hall requests, alive, other elevators, self non-cab fields)
	s.MergeNetworkSnapshotNoSelfCab(snap)

	// Then additionally merge *self* cab requests from the snapshot (if present).
	if snap.States == nil {
		return
	}
	stSnap, ok := snap.States[s.selfKey]
	if !ok || stSnap.CabRequests == nil {
		return
	}

	cur := s.states[s.selfKey]
	if cur.CabRequests == nil {
		cur.CabRequests = make([]bool, common.N_FLOORS)
	}

	// Overwrite to match snapshot for self cab requests (lamp state should reflect snapshots).
	n := len(stSnap.CabRequests)
	if n > common.N_FLOORS {
		n = common.N_FLOORS
	}
	for i := 0; i < n; i++ {
		cur.CabRequests[i] = stSnap.CabRequests[i]
	}
	for i := n; i < common.N_FLOORS; i++ {
		cur.CabRequests[i] = false
	}

	s.states[s.selfKey] = cur
}

// Existing merge (does NOT overwrite self cab requests)
func (s *FsmGlueState) MergeNetworkSnapshotNoSelfCab(snap common.Snapshot) {
	if snap.HallRequests != nil {
		s.hallRequests = cloneHall(snap.HallRequests)
	}

	if snap.Alive != nil {
		s.alive = cloneAlive(snap.Alive)
	}

	if snap.States != nil {
		for k, st := range snap.States {
			if k == s.selfKey {
				cur := s.states[s.selfKey]
				// keep self cab requests unchanged here
				s.states[s.selfKey] = cur
			} else {
				s.states[k] = common.CopyElevState(st)
			}
		}
	}

	if _, ok := s.states[s.selfKey]; !ok {
		s.states[s.selfKey] = common.ElevState{
			Behavior:    "idle",
			Floor:       0,
			Direction:   "stop",
			CabRequests: make([]bool, common.N_FLOORS),
		}
	}

	if s.alive == nil {
		s.alive = make(map[string]bool)
	}
	s.alive[s.selfKey] = true
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
	st, ok := s.states[s.selfKey]
	if !ok {
		st = common.ElevState{
			Behavior:    "idle",
			Floor:       0,
			Direction:   "stop",
			CabRequests: make([]bool, common.N_FLOORS),
		}
	}
	if st.CabRequests == nil {
		st.CabRequests = make([]bool, common.N_FLOORS)
	}
	if f >= 0 && f < len(st.CabRequests) {
		st.CabRequests[f] = v
	}
	s.states[s.selfKey] = st
}

func (s *FsmGlueState) SetFloor(f int) {
	st, ok := s.states[s.selfKey]
	if !ok {
		st = common.ElevState{
			Behavior:    "idle",
			Floor:       0,
			Direction:   "stop",
			CabRequests: make([]bool, common.N_FLOORS),
		}
	}
	st.Floor = f
	s.states[s.selfKey] = st
}

func (s *FsmGlueState) ClearAtCurrentFloorIfAny() bool {
	st, ok := s.states[s.selfKey]
	if !ok {
		return false
	}
	f := st.Floor
	if f < 0 || f >= common.N_FLOORS {
		return false
	}

	changed := false

	// Clear self cab request at current floor.
	if st.CabRequests != nil && f < len(st.CabRequests) && st.CabRequests[f] {
		st.CabRequests[f] = false
		s.states[s.selfKey] = st
		changed = true
	}

	// Clear hall requests at current floor ONLY for directions assigned to us.
	if s.assignedHall != nil && f < len(s.assignedHall) {
		for d := 0; d < 2; d++ {
			if s.assignedHall[f][d] {
				s.assignedHall[f][d] = false
				if s.hallRequests != nil && f < len(s.hallRequests) && s.hallRequests[f][d] {
					s.hallRequests[f][d] = false
				}
				changed = true
			}
		}
	}

	return changed
}

func (s *FsmGlueState) Snapshot() common.Snapshot {
	return common.Snapshot{
		HallRequests: cloneHall(s.hallRequests),
		States:       cloneStates(s.states),
		Alive:        cloneAlive(s.alive),
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

func cloneAlive(in map[string]bool) map[string]bool {
	if in == nil {
		return nil
	}
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStates(in map[string]common.ElevState) map[string]common.ElevState {
	if in == nil {
		return nil
	}
	out := make(map[string]common.ElevState, len(in))
	for k, st := range in {
		out[k] = common.CopyElevState(st)
	}
	return out
}
