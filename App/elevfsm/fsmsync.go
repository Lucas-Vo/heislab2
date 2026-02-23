package elevfsm

import (
	"elevator/common"
	"log"
	"time"
)

// Allow a few missed snapshots before declaring offline.
const netOfflineTimeout = 5 * time.Second

type ServicedAt struct { //TODO: maybe use the BTuttonType here instead of bools, but this is more explicit and easier to read
	HallUp   bool
	HallDown bool
	Cab      bool
}

type FsmSync struct {
	cfg     common.Config
	selfKey string

	hasNet      bool
	hasNetSelf  bool
	lastNetSeen time.Time

	assignedHall [common.N_FLOORS][2]bool
	hasAssigner  bool

	netCalls   [common.N_FLOORS][common.N_BUTTONS]bool
	localCalls [common.N_FLOORS][common.N_BUTTONS]bool

	callTimestamp [common.N_FLOORS][common.N_BUTTONS]time.Time
	injected      [common.N_FLOORS][common.N_BUTTONS]bool
	confirmed     [common.N_FLOORS][common.N_BUTTONS]bool

	reportedFloor     int
	reportedBehavior  string
	reportedDirection string

	Elevator *Elevator
}

// NewFsmSync initializes a sync helper with empty local/net request state and a startup grace period.
func NewFsmSync(cfg common.Config) *FsmSync {
	s := &FsmSync{
		cfg:           cfg,
		selfKey:       cfg.SelfKey,
		assignedHall:  [common.N_FLOORS][2]bool{},
		reportedFloor: -1,
	}
	s.Elevator = ElevatorInit()
	// Start a short grace period before declaring offline.
	s.lastNetSeen = time.Now()
	return s
}

// Offline reports whether the network has been silent long enough to treat us as offline.
func (s *FsmSync) Offline(now time.Time) bool {
	return now.Sub(s.lastNetSeen) > netOfflineTimeout
}

// LastNetSeen returns the timestamp of the most recent network snapshot.
func (s *FsmSync) LastNetSeen() time.Time {
	return s.lastNetSeen
}

// HasNetSelf reports whether the latest snapshot included our own cab requests.
func (s *FsmSync) HasNetSelf() bool {
	return s.hasNetSelf
}

// ApplyAssigner stores hall assignments and cancels any previously assigned halls that were removed.
func (s *FsmSync) ApplyAssigner(task common.ElevInput) {
	previousAssignment := s.assignedHall
	s.assignedHall = task.HallTask
	s.hasAssigner = true
	s.cancelUnassigned(previousAssignment)
}

// cancelUnassigned clears local tracking for halls we no longer own after a new assignment.
func (s *FsmSync) cancelUnassigned(prev [common.N_FLOORS][2]bool) {
	for f := range prev {
		if prev[f][0] && !s.assignedHall[f][0] {
			s.cancelHall(f, common.BT_HallUp)
		}
		if prev[f][1] && !s.assignedHall[f][1] {
			s.cancelHall(f, common.BT_HallDown)
		}
	}
}

// cancelHall clears a specific hall request from local state and the FSM's request table.
func (s *FsmSync) cancelHall(f int, btn common.ButtonType) {
	if btn == common.BT_Cab {
		return
	}
	if f < 0 || f >= common.N_FLOORS {
		return
	}
	if btn < 0 || btn >= common.N_BUTTONS {
		return
	}
	if s.injected[f][btn] || !s.callTimestamp[f][btn].IsZero() || s.localCalls[f][btn] {
		log.Printf("fsmThread:  hall unassigned f=%d b=%s", f, common.ElevioButtonToString(btn))
	}
	s.callTimestamp[f][btn] = time.Time{}
	s.injected[f][btn] = false
	s.confirmed[f][btn] = false

	s.localCalls[f][btn] = false

	s.Elevator.requests[f][btn] = false
}

// ApplyNetworkSnapshot ingests a network snapshot and reconciles net vs local request state.
// Net hall/cab reflect the shared/global view, while local hall/cab reflect what we pressed or injected.
func (s *FsmSync) ApplyNetworkSnapshot(snap common.Snapshot, now time.Time) {
	s.hasNet = true
	s.lastNetSeen = now

	for f := range common.N_FLOORS {
		s.netCalls[f][0] = snap.HallRequests[f][0]
		s.netCalls[f][1] = snap.HallRequests[f][1]
	}
	if s.copyCabFromSnapshot(&snap) { //TODO: Does not explain shit
		s.hasNetSelf = true
	}
	for f := range common.N_FLOORS {
		for btn := range common.ButtonType(common.N_BUTTONS) {
			wasConfirmed := s.confirmed[f][btn]
			netCallActive := s.netCalls[f][btn]

			if netCallActive {
				s.callTimestamp[f][btn] = time.Time{}
				s.confirmed[f][btn] = true
				if btn == common.BT_Cab {
					s.localCalls[f][btn] = true
				}
				continue
			}
			s.confirmed[f][btn] = false
			if wasConfirmed {
				s.localCalls[f][btn] = false
				s.injected[f][btn] = false
			}
		}
	}
}

// copyCabFromSnapshot extracts our own cab requests from a snapshot (per-elevator state).
func (s *FsmSync) copyCabFromSnapshot(snapshot *common.Snapshot) bool { //TODO: should not use copy name for mutating internal attributes as we use copy for actual copying, making this not descriptive
	for floor := range common.N_FLOORS {
		s.netCalls[floor][common.BT_Cab] = false
	}
	if snapshot.States == nil {
		return false
	}
	state, found := snapshot.States[s.selfKey]
	if !found {
		return false
	}
	for floor := 0; floor < common.N_FLOORS && floor < len(state.CabRequests); floor++ {
		s.netCalls[floor][common.BT_Cab] = state.CabRequests[floor]
	}
	return true
}

// OnLocalPress records a local button press and marks it pending confirmation/injection.
func (s *FsmSync) OnLocalPress(f int, btn common.ButtonType, now time.Time) {
	s.markPending(f, btn, now)
	s.localCalls[f][btn] = true
}

// markPending starts the confirmation timer for a locally pressed request.
func (s *FsmSync) markPending(f int, btn common.ButtonType, now time.Time) {
	s.callTimestamp[f][btn] = now
	log.Printf("fsmThread: pending request f=%d b=%s (local press)", f, common.ElevioButtonToString(btn))
}

// inject forwards a request into the local FSM once it's confirmed or timed out.
// This bridges net-confirmed requests or offline fallback into the elevator's request table.
func (s *FsmSync) inject(f int, btn common.ButtonType) {
	log.Printf("fsmThread: inject request f=%d b=%s", f, common.ElevioButtonToString(btn))

	s.Elevator.OnRequestButtonPress(f, btn)

	s.injected[f][btn] = true
	s.callTimestamp[f][btn] = time.Time{}

	s.localCalls[f][btn] = true
}

func (s *FsmSync) TryInjectAll(now time.Time, confirmTimeout time.Duration, online bool) {
	calls := s.localCalls
	if online && s.hasNet {
		calls = s.netCalls
	}

	for f := range common.N_FLOORS {
		for btn := range common.ButtonType(common.N_BUTTONS) {
			// Skip if no request exists or if already injected (whether from net or local).
			if !calls[f][btn] || s.injected[f][btn] {
				continue
			}
			callTimestamp := s.callTimestamp[f][btn]
			timedOut := callTimestamp.IsZero() || now.Sub(callTimestamp) >= confirmTimeout

			shouldInject :=
				(!online && timedOut) || (online && (btn == common.BT_Cab || (s.hasAssigner && s.assignedHall[f][btn]))) //TODO: Make these logical statements look human
			if shouldInject {
				s.inject(f, btn)
			} else if online && s.hasAssigner &&
				btn != common.BT_Cab &&
				!s.assignedHall[f][btn] &&
				!callTimestamp.IsZero() {

				log.Printf("fsmThread: hall f=%d btn=%v assigned elsewhere", f, btn)
				s.callTimestamp[f][btn] = time.Time{}
			}
		}
	}
}

// ClearAtFloor clears injected requests serviced at a floor and returns which types were cleared.
// When online, keep injected flags until the network snapshot removes the requests.
// When offline, clear injected flags immediately.
func (s *FsmSync) ClearAtFloor(e *Elevator, floor int, arrivalDir common.MotorDirection, online bool) (servicedAt ServicedAt) {
	*e, servicedAt = requests_clearAtCurrentFloor(*e) //TODO: This is called quite weirdly and it sometimes skips floors and it is not clear why
	if servicedAt.Cab && s.injected[floor][common.BT_Cab] {
		s.localCalls[floor][common.BT_Cab] = false
		if !online {
			s.injected[floor][common.BT_Cab] = false
		}
	}
	if servicedAt.HallUp && s.injected[floor][common.BT_HallUp] {
		s.localCalls[floor][common.BT_HallUp] = false
		if !online {
			s.injected[floor][common.BT_HallUp] = false
		}
	}
	if servicedAt.HallDown && s.injected[floor][common.BT_HallDown] {
		s.localCalls[floor][common.BT_HallDown] = false
		if !online {
			s.injected[floor][common.BT_HallDown] = false
		}
	}
	// Let sync observe what changed and update network bookkeeping
	return servicedAt
}

func (s *FsmSync) BuildSnapshot(floor int, kind common.UpdateKind, callsCleared ServicedAt, online bool) common.Snapshot {
	// Choose base hall source
	baseCalls := s.localCalls
	if kind == common.UpdateServiced && online && s.hasNet {
		baseCalls = s.netCalls
	}

	outCalls := baseCalls
	// Apply servicing modification only when relevant
	if kind == common.UpdateServiced && floor >= 0 && floor < len(outCalls) {
		if callsCleared.HallUp {
			outCalls[floor][common.BT_HallUp] = false
		}
		if callsCleared.HallDown {
			outCalls[floor][common.BT_HallDown] = false
		}
	}
	behavior, direction := s.Elevator.GetMotionStrings()
	return common.Snapshot{
		HallRequests: common.GetHallSlice(outCalls),
		States: map[string]common.ElevState{
			s.selfKey: {
				Behavior:    behavior,
				Floor:       floor,
				Direction:   direction,
				CabRequests: common.GetCabSlice(s.localCalls),
			},
		},
		UpdateKind: kind,
	}
}

// ApplyLights drives the physical lamps from a snapshot's hall and cab requests.
func (s *FsmSync) ApplyLights(online bool) {
	calls := s.localCalls
	if online && s.hasNet {
		calls = s.netCalls
	}
	for floor := range common.N_FLOORS {
		for btn := range common.ButtonType(common.N_BUTTONS) {
			s.Elevator.SwitchLight(floor, btn, calls[floor][btn]) //TODO: my friends friend is not supposed to use my methods
		}
	}
}
