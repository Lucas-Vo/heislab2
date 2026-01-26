package elevfsm

import (
	"context"
	"log"
	"time"

	"elevator/common"
)

// FsmGlueState is a "glue layer" between:
//
//  1. The local elevator FSM (which is driven by events like "request button pressed",
//     "arrived at floor", "door timeout").
//
//  2. The network / assigner layer (which wants a serializable snapshot of:
//     - all hall requests in the building
//     - per-elevator states (at least our own floor + cab requests)
//     - alive/liveness map
//     - the hall tasks assigned to THIS elevator)
//
// This struct is intentionally kept separate from the FSM's internal elevator.requests matrix.
// The FSM remains the authority for motion/door decisions, while GlueState is the authority
// for what we publish to the network and for what we consider "served" externally.
type FsmGlueState struct {
	// Unique identifier for *this* elevator instance (e.g., hostname, IP:port, UUID, etc.)
	selfKey string

	// hallRequests[f][0] == hall-up request at floor f exists in the building
	// hallRequests[f][1] == hall-down request at floor f exists in the building
	//
	// This is the "building-wide" hall request matrix we publish/receive via NetworkState.
	hallRequests [][2]bool

	// states[elevatorKey] = elevator state for each known elevator in the system.
	//
	// For selfKey, we primarily care about:
	// - Floor (so we can clear requests at current floor)
	// - CabRequests (so we can publish which cab requests we've received)
	//
	// For other elevators, we keep their states for the network snapshot / assigner.
	states map[string]common.ElevState

	// alive[elevatorKey] = whether that elevator is believed alive by the network layer.
	// This is optional but included in NetworkState, so we store it here.
	alive map[string]bool

	// assignedHall[f][0] == THIS elevator is assigned hall-up at floor f
	// assignedHall[f][1] == THIS elevator is assigned hall-down at floor f
	//
	// This comes from assignerOutput (ElevInput.HallTask). We use it to:
	// - decide what hall calls to clear as "served" at the current floor
	//   (i.e., only clear what we were assigned, not what some other elevator was assigned)
	assignedHall [][2]bool
}

// NewFsmGlueState creates a glue state with sane defaults.
//
// It initializes:
// - hallRequests to N_FLOORS length
// - states map with an entry for selfKey
// - alive map marking selfKey true
// - assignedHall to N_FLOORS length
func NewFsmGlueState(cfg common.Config) *FsmGlueState {
	s := &FsmGlueState{
		selfKey:      cfg.SelfKey,
		hallRequests: make([][2]bool, common.N_FLOORS),
		states:       make(map[string]common.ElevState),
		alive:        make(map[string]bool),
		assignedHall: make([][2]bool, common.N_FLOORS),
	}

	// Ensure "self" exists in the states map.
	// We don't rely on the network having already published an entry for us.
	s.states[s.selfKey] = common.ElevState{
		Behavior:    "idle",
		Floor:       0,
		Direction:   "stop",
		CabRequests: make([]bool, common.N_FLOORS),
	}

	// Mark self alive by default.
	s.alive[s.selfKey] = true

	return s
}

// SelfKey returns the elevator's identifier key.
func (s *FsmGlueState) SelfKey() string { return s.selfKey }

// TryLoadSnapshot attempts to read exactly one NetworkState from snapshotFromNetwork,
// within the given timeout.
//
// This is useful on startup so the elevator can "join" an existing distributed system
// without starting from empty hallRequests/states/alive.
//
// Important detail:
// We call MergeNetworkSnapshotNoSelfCab() so we do NOT overwrite s.states[self].CabRequests,
// because that array is also used to represent locally-received cab button presses
// that we broadcast outward as "received".
// (The snapshot is authoritative for what we should *execute*, but we still want to publish
// what we have *received* locally.)
func (s *FsmGlueState) TryLoadSnapshot(
	ctx context.Context,
	snapshotFromNetwork <-chan common.NetworkState,
	timeout time.Duration,
) {
	// If the channel is nil, we can't read from it.
	if snapshotFromNetwork == nil {
		return
	}

	// Timer for snapshot load timeout.
	t := time.NewTimer(timeout)
	defer t.Stop()

	select {
	case snap := <-snapshotFromNetwork:
		// Merge snapshot into glue state.
		s.MergeNetworkSnapshotNoSelfCab(snap)

		// Make sure self exists even if snapshot didn't include it.
		if _, ok := s.states[s.selfKey]; !ok {
			s.states[s.selfKey] = common.ElevState{
				Behavior:    "idle",
				Floor:       0,
				Direction:   "stop",
				CabRequests: make([]bool, common.N_FLOORS),
			}
		}

		// Ensure alive map exists and mark self alive.
		if s.alive == nil {
			s.alive = make(map[string]bool)
		}
		s.alive[s.selfKey] = true

		log.Printf("FSM loaded snapshot from network")

	case <-t.C:
		// No snapshot arrived before timeout; continue with defaults.
		log.Printf("FSM snapshot timeout; continuing without snapshot")

	case <-ctx.Done():
		// If context cancelled during startup, exit.
		return
	}
}

// ApplyAssignerTask stores which hall calls THIS elevator is assigned to serve.
// task.HallTask is expected to be [N_FLOORS][2]bool-like in the slice form.
func (s *FsmGlueState) ApplyAssignerTask(task common.ElevInput) {
	if task.HallTask != nil {
		s.assignedHall = cloneHall(task.HallTask)
	}
}

// MergeNetworkSnapshotNoSelfCab merges the network's view of the world into the glue state,
// but deliberately does NOT overwrite s.states[self].CabRequests.
//
// Why?
//   - We use s.states[self].CabRequests to publish "cab requests received locally".
//   - If we overwrote it every time the network snapshot arrives, we'd erase those local
//     "received" records until the network eventually mirrors them back.
//   - Meanwhile, snapshotFromNetwork is authoritative for what cab requests we should EXECUTE,
//     and that execution is handled elsewhere (edge-detected in fsmThread).
func (s *FsmGlueState) MergeNetworkSnapshotNoSelfCab(snap common.NetworkState) {
	// Update building-wide hall requests if provided.
	if snap.HallRequests != nil {
		s.hallRequests = cloneHall(snap.HallRequests)
	}

	// Update liveness map if provided.
	if snap.Alive != nil {
		s.alive = cloneAlive(snap.Alive)
	}

	// Update other elevators' states (and optionally self non-cab fields).
	if snap.States != nil {
		for k, st := range snap.States {
			if k == s.selfKey {
				// For self, keep CabRequests unchanged.
				cur := s.states[s.selfKey]

				// If you want the network to override self's floor/behavior/direction too,
				// you could merge them here, e.g.:
				// cur.Behavior = st.Behavior
				// cur.Direction = st.Direction
				// cur.Floor = st.Floor

				s.states[s.selfKey] = cur
			} else {
				// For other elevators, copy the state fully.
				s.states[k] = common.CopyElevState(st)
			}
		}
	}

	// Ensure self exists after merge.
	if _, ok := s.states[s.selfKey]; !ok {
		s.states[s.selfKey] = common.ElevState{
			Behavior:    "idle",
			Floor:       0,
			Direction:   "stop",
			CabRequests: make([]bool, common.N_FLOORS),
		}
	}

	// Ensure alive map exists and mark self alive.
	if s.alive == nil {
		s.alive = make(map[string]bool)
	}
	s.alive[s.selfKey] = true
}

// SetHallRequest sets both hall directions at a floor explicitly.
// up controls the hall-up request at floor f,
// down controls the hall-down request at floor f.
func (s *FsmGlueState) SetHallRequest(f int, up bool, down bool) {
	if f < 0 || f >= len(s.hallRequests) {
		return
	}
	s.hallRequests[f][0] = up
	s.hallRequests[f][1] = down
}

// SetHallButton sets one hall direction at a floor.
// isUp selects hall-up if true, hall-down if false.
// v is the value to set.
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

// SetCabRequest sets the "received" cab request for selfKey at floor f to v.
func (s *FsmGlueState) SetCabRequest(f int, v bool) {
	// Get current self state or create default if missing.
	st, ok := s.states[s.selfKey]
	if !ok {
		st = common.ElevState{
			Behavior:    "idle",
			Floor:       0,
			Direction:   "stop",
			CabRequests: make([]bool, common.N_FLOORS),
		}
	}

	// Ensure CabRequests slice exists and has expected length.
	if st.CabRequests == nil {
		st.CabRequests = make([]bool, common.N_FLOORS)
	}

	// Bounds check and set.
	if f >= 0 && f < len(st.CabRequests) {
		st.CabRequests[f] = v
	}

	// Write back.
	s.states[s.selfKey] = st
}

// SetFloor updates self's current floor in the glue state.
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

// ClearAtCurrentFloorIfAny clears the requests we consider "served" at the current floor.
//
// We clear:
// - self cab request at current floor (if any)
// - hall requests at current floor ONLY for directions assigned to this elevator
//
// This prevents clearing hall requests that were assigned to other elevators.
//
// Returns true if anything changed (so the caller can publish a "serviced" snapshot).
func (s *FsmGlueState) ClearAtCurrentFloorIfAny() bool {
	// Need self state to know current floor.
	st, ok := s.states[s.selfKey]
	if !ok {
		return false
	}
	f := st.Floor

	// If floor invalid, do nothing.
	if f < 0 || f >= common.N_FLOORS {
		return false
	}

	changed := false

	// 1) Clear self cab request at current floor.
	if st.CabRequests != nil && f < len(st.CabRequests) && st.CabRequests[f] {
		st.CabRequests[f] = false
		s.states[s.selfKey] = st
		changed = true
	}

	// 2) Clear hall requests at current floor ONLY for directions assigned to us.
	if s.assignedHall != nil && f < len(s.assignedHall) {
		for d := 0; d < 2; d++ {
			if s.assignedHall[f][d] {
				// Clear our assignment flag for that direction.
				s.assignedHall[f][d] = false

				// Clear the building-wide hall request for that direction too.
				if s.hallRequests != nil && f < len(s.hallRequests) && s.hallRequests[f][d] {
					s.hallRequests[f][d] = false
				}

				changed = true
			}
		}
	}

	return changed
}

// Snapshot returns a deep-copied NetworkState safe to share across goroutines.
//
// We clone:
// - hallRequests slice
// - states map (deep copy each ElevState)
// - alive map
func (s *FsmGlueState) Snapshot() common.NetworkState {
	return common.NetworkState{
		HallRequests: cloneHall(s.hallRequests),
		States:       cloneStates(s.states),
		Alive:        cloneAlive(s.alive),
	}
}

// cloneHall deep-copies a hall request slice.
func cloneHall(in [][2]bool) [][2]bool {
	if in == nil {
		return nil
	}
	out := make([][2]bool, len(in))
	copy(out, in)
	return out
}

// cloneAlive deep-copies an alive map.
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

// cloneStates deep-copies a map of elevator states.
// It uses common.CopyElevState to ensure CabRequests is copied too.
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
