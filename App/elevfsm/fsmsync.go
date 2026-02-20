package elevfsm

import (
	"elevator/common"
	"log"
	"time"
)

// Allow a few missed snapshots before declaring offline.
const netOfflineTimeout = 5 * time.Second

type ServicedAt struct {
	HallUp   bool
	HallDown bool
	Cab      bool
}

type FsmSync struct {
	cfg     common.Config
	selfKey string

	netHall     [][2]bool
	netCab      []bool
	hasNet      bool
	hasNetSelf  bool
	lastNetSeen time.Time

	assignedHall [][2]bool
	hasAssigner  bool

	localHall [][2]bool
	localCab  []bool

	pendingAt [common.N_FLOORS][common.N_BUTTONS]time.Time
	injected  [common.N_FLOORS][common.N_BUTTONS]bool
	confirmed [common.N_FLOORS][common.N_BUTTONS]bool

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
		netHall:       make([][2]bool, common.N_FLOORS),
		netCab:        make([]bool, common.N_FLOORS),
		localHall:     make([][2]bool, common.N_FLOORS),
		localCab:      make([]bool, common.N_FLOORS),
		assignedHall:  make([][2]bool, common.N_FLOORS),
		reportedFloor: -1,
	}

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

// NetCabCopy returns a safe copy of cab requests from the network snapshot (global view).
func (s *FsmSync) NetCabCopy() []bool {
	return cloneBoolSlice(s.netCab)
}

// LocalCabCopy returns a safe copy of locally tracked cab requests (pressed/injected here).
func (s *FsmSync) LocalCabCopy() []bool {
	return cloneBoolSlice(s.localCab)
}

// ApplyAssigner stores hall assignments and cancels any previously assigned halls that were removed.
func (s *FsmSync) ApplyAssigner(task common.ElevInput) {
	if s.assignedHall == nil || len(s.assignedHall) != common.N_FLOORS {
		s.assignedHall = make([][2]bool, common.N_FLOORS)
	}
	prev := cloneHallSlice(s.assignedHall)
	copyHall(s.assignedHall, task.HallTask)
	s.hasAssigner = true
	s.cancelUnassigned(prev)
}

// cancelUnassigned clears local tracking for halls we no longer own after a new assignment.
func (s *FsmSync) cancelUnassigned(prev [][2]bool) {
	for f := range common.N_FLOORS {
		if prev[f][0] && !s.assignedHall[f][0] {
			s.cancelHall(f, common.BT_HallUp, "unassigned")
		}
		if prev[f][1] && !s.assignedHall[f][1] {
			s.cancelHall(f, common.BT_HallDown, "unassigned")
		}
	}
}

// cancelHall clears a specific hall request from local state and the FSM's request table.
func (s *FsmSync) cancelHall(f int, btn common.ButtonType, reason string) {
	if btn != common.BT_HallUp && btn != common.BT_HallDown {
		return
	}
	if f < 0 || f >= common.N_FLOORS {
		return
	}
	if s.injected[f][btn] || !s.pendingAt[f][btn].IsZero() || s.localHall[f][btn] {
		log.Printf("fsmThread: cancel hall f=%d b=%s (%s)", f, common.ElevioButtonToString(btn), reason)
	}
	s.pendingAt[f][btn] = time.Time{}
	s.injected[f][btn] = false
	s.confirmed[f][btn] = false
	if btn == common.BT_HallUp {
		s.localHall[f][0] = false
	} else {
		s.localHall[f][1] = false
	}
	if f < 0 || f >= common.N_FLOORS {
		return
	}
	if btn < 0 || btn >= common.N_BUTTONS {
		return
	}
	s.Elevator.requests[f][btn] = false
}

// ApplyNetworkSnapshot ingests a network snapshot and reconciles net vs local request state.
// Net hall/cab reflect the shared/global view, while local hall/cab reflect what we pressed or injected.
func (s *FsmSync) ApplyNetworkSnapshot(snap common.Snapshot, now time.Time) {
	s.hasNet = true
	s.lastNetSeen = now

	copyHall(s.netHall, snap.HallRequests)
	if s.copyCabFromSnapshot(snap) {
		s.hasNetSelf = true
	}

	for f := range common.N_FLOORS {
		// Hall up
		wasConfirmed := s.confirmed[f][common.BT_HallUp]
		if s.netHall[f][0] {
			s.pendingAt[f][common.BT_HallUp] = time.Time{}
			s.confirmed[f][common.BT_HallUp] = true
		} else {
			s.confirmed[f][common.BT_HallUp] = false
			if wasConfirmed {
				s.localHall[f][0] = false
				s.injected[f][common.BT_HallUp] = false
			}
		}

		// Hall down
		wasConfirmed = s.confirmed[f][common.BT_HallDown]
		if s.netHall[f][1] {
			s.pendingAt[f][common.BT_HallDown] = time.Time{}
			s.confirmed[f][common.BT_HallDown] = true
		} else {
			s.confirmed[f][common.BT_HallDown] = false
			if wasConfirmed {
				s.localHall[f][1] = false
				s.injected[f][common.BT_HallDown] = false
			}
		}

		// Cab
		wasConfirmed = s.confirmed[f][common.BT_Cab]
		if s.netCab[f] {
			s.pendingAt[f][common.BT_Cab] = time.Time{}
			s.confirmed[f][common.BT_Cab] = true
			s.localCab[f] = true
		} else {
			s.confirmed[f][common.BT_Cab] = false
			if wasConfirmed {
				s.localCab[f] = false
				s.injected[f][common.BT_Cab] = false
			}
		}
	}
}

// copyCabFromSnapshot extracts our own cab requests from a snapshot (per-elevator state).
func (s *FsmSync) copyCabFromSnapshot(snapshot common.Snapshot) bool {
	for floor := range common.N_FLOORS {
		s.netCab[floor] = false
	}
	if snapshot.States == nil {
		return false
	}
	state, found := snapshot.States[s.selfKey]
	if !found || state.CabRequests == nil {
		return false
	}
	for floor := 0; floor < common.N_FLOORS && floor < len(state.CabRequests); floor++ {
		s.netCab[floor] = state.CabRequests[floor]
	}
	return true
}

// OnLocalPress records a local button press and marks it pending confirmation/injection.
func (s *FsmSync) OnLocalPress(f int, btn common.ButtonType, now time.Time) {
	s.markPending(f, btn, now)

	switch btn {
	case common.BT_HallUp:
		s.localHall[f][0] = true
	case common.BT_HallDown:
		s.localHall[f][1] = true
	case common.BT_Cab:
		s.localCab[f] = true
	}
}

// markPending starts the confirmation timer for a locally pressed request.
func (s *FsmSync) markPending(f int, btn common.ButtonType, now time.Time) {
	if s.pendingAt[f][btn].IsZero() {
		s.pendingAt[f][btn] = now
		log.Printf("fsmThread: pending request f=%d b=%s (local press)", f, common.ElevioButtonToString(btn))
	}
}

// inject forwards a request into the local FSM once it's confirmed or timed out.
// This bridges net-confirmed requests or offline fallback into the elevator's request table.
func (s *FsmSync) inject(f int, btn common.ButtonType) {
	log.Printf("fsmThread: inject request f=%d b=%s", f, common.ElevioButtonToString(btn))

	Fsm_onRequestButtonPress(s.Elevator, f, btn)

	s.injected[f][btn] = true
	s.pendingAt[f][btn] = time.Time{}

	if btn == common.BT_Cab {
		s.localCab[f] = true
	} else {
		s.localHall[f][btn] = true
	}
}

func (s *FsmSync) TryInjectAll(now time.Time, confirmTimeout time.Duration, online bool) {
	var hall [][2]bool
	var cab []bool
	if online && s.hasNet {
		hall = cloneHallSlice(s.netHall)
		cab = cloneBoolSlice(s.netCab)
	} else {
		hall = cloneHallSlice(s.localHall)
		cab = cloneBoolSlice(s.localCab)
	}

	for f := range common.N_FLOORS {
		for btn := range common.ButtonType(common.N_BUTTONS) {

			// Skip if no request exists
			hasRequest := (btn == common.BT_Cab && cab[f]) ||
				(btn != common.BT_Cab && hall[f][btn])

			if !hasRequest || s.injected[f][btn] {
				continue
			}

			pending := s.pendingAt[f][btn]
			timedOut := pending.IsZero() ||
				now.Sub(pending) >= confirmTimeout

			shouldInject :=
				(!online && timedOut) || (online && (btn == common.BT_Cab || (s.hasAssigner && s.assignedHall[f][btn]))) //TODO: Make these logical statements look human

			if shouldInject {
				s.inject(f, btn)
			} else if online && s.hasAssigner &&
				btn != common.BT_Cab &&
				!s.assignedHall[f][btn] &&
				!pending.IsZero() {

				log.Printf("fsmThread: hall f=%d btn=%v assigned elsewhere", f, btn)
				s.pendingAt[f][btn] = time.Time{}
			}
		}
	}
}

// ClearAtFloor clears injected requests serviced at a floor and returns which types were cleared.
// When online, keep injected flags until the network snapshot removes the requests.
// When offline, clear injected flags immediately.
func (s *FsmSync) ClearAtFloor(f int, online bool, arrivalDirn common.MotorDirection) ServicedAt {
	if f < 0 || f >= common.N_FLOORS {
		return ServicedAt{}
	}

	var cleared ServicedAt

	if s.injected[f][common.BT_Cab] {
		cleared.Cab = true
		s.localCab[f] = false
		if !online {
			s.injected[f][common.BT_Cab] = false
		}
	}

	var clearUp, clearDown bool

	switch arrivalDirn {
	case common.MD_Up:
		clearUp = true
		if s.Elevator.floor == common.N_FLOORS-1 || (requests_above(*s.Elevator) == 0 && !s.Elevator.requests[s.Elevator.floor][common.BT_HallUp]) {
			clearDown = true
		}
	case common.MD_Down:
		clearDown = true
		if s.Elevator.floor == 0 || (requests_below(*s.Elevator) == 0 && !s.Elevator.requests[s.Elevator.floor][common.BT_HallDown]) {
			clearUp = true
		}
	case common.MD_Stop:
		clearUp, clearDown = true, true
	}

	// helper to apply hall clears concisely
	applyHallClear := func(btn common.ButtonType, idx int, mark *bool, setCleared func()) {
		if *mark && s.injected[f][btn] {
			setCleared()
			s.localHall[f][idx] = false
			if !online {
				s.injected[f][btn] = false
			}
		}
	}

	applyHallClear(common.BT_HallUp, 0, &clearUp, func() { cleared.HallUp = true })
	applyHallClear(common.BT_HallDown, 1, &clearDown, func() { cleared.HallDown = true })

	return cleared
}

// BuildUpdateSnapshot builds a snapshot based on local requests and current motion state.
func (s *FsmSync) BuildUpdateSnapshot(floor int, behavior string, direction string) common.Snapshot { //TODO: Make serviced and update snapshot the same shit
	return common.Snapshot{
		HallRequests: cloneHallSlice(s.localHall),
		States: map[string]common.ElevState{
			s.selfKey: {
				Behavior:    behavior,
				Floor:       floor,
				Direction:   direction,
				CabRequests: cloneBoolSlice(s.localCab),
			},
		},
		UpdateKind: common.UpdateRequests,
	}
}

// BuildServicedSnapshot builds a snapshot that clears serviced halls at a floor.
// Online uses the net hall view as a base; offline uses the local hall view.
func (s *FsmSync) BuildServicedSnapshot(floor int, behavior string, direction string, cleared ServicedAt, online bool) common.Snapshot {
	baseHall := s.localHall
	if online && s.hasNet {
		baseHall = s.netHall
	}

	outHall := cloneHallSlice(baseHall)
	if floor >= 0 && floor < len(outHall) {
		if cleared.HallUp {
			outHall[floor][0] = false
		}
		if cleared.HallDown {
			outHall[floor][1] = false
		}
	}

	return common.Snapshot{
		HallRequests: outHall,
		States: map[string]common.ElevState{
			s.selfKey: {
				Behavior:    behavior,
				Floor:       floor,
				Direction:   direction,
				CabRequests: cloneBoolSlice(s.localCab),
			},
		},
		UpdateKind: common.UpdateServiced,
	}
}
func (s *FsmSync) BuildSnapshot(floor int, behavior string, direction string, kind common.UpdateKind, cleared ServicedAt, online bool) common.Snapshot {

	// Choose base hall source
	baseHall := s.localHall
	if kind == common.UpdateServiced && online && s.hasNet {
		baseHall = s.netHall
	}

	outHall := cloneHallSlice(baseHall)

	// Apply servicing modification only when relevant
	if kind == common.UpdateServiced &&
		floor >= 0 && floor < len(outHall) {

		if cleared.HallUp {
			outHall[floor][0] = false
		}
		if cleared.HallDown {
			outHall[floor][1] = false
		}
	}

	return common.Snapshot{
		HallRequests: outHall,
		States: map[string]common.ElevState{
			s.selfKey: {
				Behavior:    behavior,
				Floor:       floor,
				Direction:   direction,
				CabRequests: cloneBoolSlice(s.localCab),
			},
		},
		UpdateKind: kind,
	}
}
// ApplyLights drives the physical lamps from a snapshot's hall and cab requests.
func (s *FsmSync) ApplyLights(online bool) {
	hall := make([][2]bool, common.N_FLOORS)
	cab := make([]bool, common.N_FLOORS)
	if online && s.hasNet {
		hall = cloneHallSlice(s.netHall)
		cab = cloneBoolSlice(s.netCab)
	} else if !online {
		hall = cloneHallSlice(s.localHall)
		cab = cloneBoolSlice(s.localCab)
	}

	output := common.ElevioGetOutputDevice()
	for floor := range common.N_FLOORS {
		output.RequestButtonLight(floor, common.BT_HallUp, hall[floor][0])
		output.RequestButtonLight(floor, common.BT_HallDown, hall[floor][1])
		output.RequestButtonLight(floor, common.BT_Cab, cab[floor])
	}
}

// MotionChanged reports whether motion state changed since the last report.
func (s *FsmSync) MotionChanged(floor int, behavior string, direction string) bool {
	if s.reportedFloor != floor || s.reportedBehavior != behavior || s.reportedDirection != direction {
		s.reportedFloor = floor
		s.reportedBehavior = behavior
		s.reportedDirection = direction
		return true
	}
	return false
}

// copyHall copies hall request slices, defaulting missing values to false.
func copyHall(dst [][2]bool, src [][2]bool) {
	if dst == nil {
		return
	}
	for i := range dst {
		if src != nil && i < len(src) {
			dst[i] = src[i]
		} else {
			dst[i] = [2]bool{false, false}
		}
	}
}

// cloneHallSlice deep-copies a hall request matrix to a fixed-size slice.
func cloneHallSlice(in [][2]bool) [][2]bool {
	copiedHall := make([][2]bool, common.N_FLOORS)
	copyHall(copiedHall, in)
	return copiedHall
}

// cloneBoolSlice deep-copies a cab request slice to a fixed-size slice.
func cloneBoolSlice(in []bool) []bool {
	copiedCab := make([]bool, common.N_FLOORS)
	for i := range common.N_FLOORS {
		if in != nil && i < len(in) {
			copiedCab[i] = in[i]
		} else {
			copiedCab[i] = false
		}
	}
	return copiedCab
}
