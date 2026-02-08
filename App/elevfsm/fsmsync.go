package elevfsm

import (
	elevio "Driver-go/elevio"
	"elevator/common"
	"log"
	"time"
)

const netOfflineTimeout = 1 * time.Second

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
}

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

func (s *FsmSync) Offline(now time.Time) bool {
	return now.Sub(s.lastNetSeen) > netOfflineTimeout
}

func (s *FsmSync) LastNetSeen() time.Time {
	return s.lastNetSeen
}

func (s *FsmSync) AssignedHallCount() int {
	return countHall(s.assignedHall)
}

func (s *FsmSync) ApplyAssigner(task common.ElevInput) {
	if s.assignedHall == nil || len(s.assignedHall) != common.N_FLOORS {
		s.assignedHall = make([][2]bool, common.N_FLOORS)
	}
	prev := cloneHallSlice(s.assignedHall)
	copyHall(s.assignedHall, task.HallTask)
	s.hasAssigner = true
	s.cancelUnassigned(prev)
}

func (s *FsmSync) cancelUnassigned(prev [][2]bool) {
	for f := 0; f < common.N_FLOORS; f++ {
		if prev[f][0] && !s.assignedHall[f][0] {
			s.cancelHall(f, elevio.BT_HallUp, "unassigned")
		}
		if prev[f][1] && !s.assignedHall[f][1] {
			s.cancelHall(f, elevio.BT_HallDown, "unassigned")
		}
	}
}

func (s *FsmSync) cancelHall(f int, btn elevio.ButtonType, reason string) {
	if btn != elevio.BT_HallUp && btn != elevio.BT_HallDown {
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
	if btn == elevio.BT_HallUp {
		s.localHall[f][0] = false
	} else {
		s.localHall[f][1] = false
	}
	clearRequest(f, btn)
}

func (s *FsmSync) ApplyNetworkSnapshot(snap common.Snapshot, now time.Time) {
	s.hasNet = true
	s.lastNetSeen = now

	copyHall(s.netHall, snap.HallRequests)
	s.copyCabFromSnapshot(snap)

	for f := 0; f < common.N_FLOORS; f++ {
		// Hall up
		wasConfirmed := s.confirmed[f][elevio.BT_HallUp]
		if s.netHall[f][0] {
			s.pendingAt[f][elevio.BT_HallUp] = time.Time{}
			s.confirmed[f][elevio.BT_HallUp] = true
		} else {
			s.confirmed[f][elevio.BT_HallUp] = false
			if wasConfirmed {
				s.localHall[f][0] = false
				s.injected[f][elevio.BT_HallUp] = false
			}
		}

		// Hall down
		wasConfirmed = s.confirmed[f][elevio.BT_HallDown]
		if s.netHall[f][1] {
			s.pendingAt[f][elevio.BT_HallDown] = time.Time{}
			s.confirmed[f][elevio.BT_HallDown] = true
		} else {
			s.confirmed[f][elevio.BT_HallDown] = false
			if wasConfirmed {
				s.localHall[f][1] = false
				s.injected[f][elevio.BT_HallDown] = false
			}
		}

		// Cab
		wasConfirmed = s.confirmed[f][elevio.BT_Cab]
		if s.netCab[f] {
			s.pendingAt[f][elevio.BT_Cab] = time.Time{}
			s.confirmed[f][elevio.BT_Cab] = true
			s.localCab[f] = true
		} else {
			s.confirmed[f][elevio.BT_Cab] = false
			if wasConfirmed {
				s.localCab[f] = false
				s.injected[f][elevio.BT_Cab] = false
			}
		}
	}
}

func (s *FsmSync) copyCabFromSnapshot(snap common.Snapshot) {
	for i := 0; i < common.N_FLOORS; i++ {
		s.netCab[i] = false
	}
	if snap.States == nil {
		return
	}
	st, ok := snap.States[s.selfKey]
	if !ok || st.CabRequests == nil {
		return
	}
	for i := 0; i < common.N_FLOORS && i < len(st.CabRequests); i++ {
		s.netCab[i] = st.CabRequests[i]
	}
}

func (s *FsmSync) OnLocalPress(f int, btn elevio.ButtonType, now time.Time) {
	s.markPending(f, btn, now)

	switch btn {
	case elevio.BT_HallUp:
		s.localHall[f][0] = true
	case elevio.BT_HallDown:
		s.localHall[f][1] = true
	case elevio.BT_Cab:
		s.localCab[f] = true
	}
}

func (s *FsmSync) markPending(f int, btn elevio.ButtonType, now time.Time) {
	if s.pendingAt[f][btn].IsZero() {
		s.pendingAt[f][btn] = now
		log.Printf("fsmThread: pending request f=%d b=%s (local press)", f, common.ElevioButtonToString(btn))
	}
}

func (s *FsmSync) inject(f int, btn elevio.ButtonType, reason string) {
	if s.injected[f][btn] {
		return
	}
	log.Printf("fsmThread: inject request f=%d b=%s (%s)", f, common.ElevioButtonToString(btn), reason)
	Fsm_onRequestButtonPress(f, btn)
	s.injected[f][btn] = true
	s.pendingAt[f][btn] = time.Time{}

	switch btn {
	case elevio.BT_HallUp:
		s.localHall[f][0] = true
	case elevio.BT_HallDown:
		s.localHall[f][1] = true
	case elevio.BT_Cab:
		s.localCab[f] = true
	}
}

func (s *FsmSync) TryInjectOnline() {
	if !s.hasNet {
		return
	}
	for f := 0; f < common.N_FLOORS; f++ {
		if s.netCab[f] {
			s.inject(f, elevio.BT_Cab, "net-confirmed")
		}

		if s.hasAssigner {
			if s.netHall[f][0] && s.assignedHall[f][0] {
				s.inject(f, elevio.BT_HallUp, "net-confirmed")
			}
			if s.netHall[f][1] && s.assignedHall[f][1] {
				s.inject(f, elevio.BT_HallDown, "net-confirmed")
			}

			if s.netHall[f][0] && !s.assignedHall[f][0] && !s.pendingAt[f][elevio.BT_HallUp].IsZero() {
				log.Printf("fsmThread: hall up f=%d assigned elsewhere", f)
				s.pendingAt[f][elevio.BT_HallUp] = time.Time{}
			}
			if s.netHall[f][1] && !s.assignedHall[f][1] && !s.pendingAt[f][elevio.BT_HallDown].IsZero() {
				log.Printf("fsmThread: hall down f=%d assigned elsewhere", f)
				s.pendingAt[f][elevio.BT_HallDown] = time.Time{}
			}
		}
	}
}

func (s *FsmSync) TryInjectOffline(now time.Time, confirmTimeout time.Duration) {
	for f := 0; f < common.N_FLOORS; f++ {
		if s.localHall[f][0] {
			if s.readyToInject(f, elevio.BT_HallUp, now, confirmTimeout) {
				s.inject(f, elevio.BT_HallUp, "offline")
			}
		}
		if s.localHall[f][1] {
			if s.readyToInject(f, elevio.BT_HallDown, now, confirmTimeout) {
				s.inject(f, elevio.BT_HallDown, "offline")
			}
		}
		if s.localCab[f] {
			if s.readyToInject(f, elevio.BT_Cab, now, confirmTimeout) {
				s.inject(f, elevio.BT_Cab, "offline")
			}
		}
	}
}

func (s *FsmSync) readyToInject(f int, btn elevio.ButtonType, now time.Time, confirmTimeout time.Duration) bool {
	if s.injected[f][btn] {
		return false
	}
	if s.pendingAt[f][btn].IsZero() {
		return true
	}
	return now.Sub(s.pendingAt[f][btn]) >= confirmTimeout
}

func (s *FsmSync) ClearAtFloor(f int, online bool) ServicedAt {
	if f < 0 || f >= common.N_FLOORS {
		return ServicedAt{}
	}

	var cleared ServicedAt

	if s.injected[f][elevio.BT_Cab] {
		cleared.Cab = true
		s.localCab[f] = false
		if !online {
			s.injected[f][elevio.BT_Cab] = false
		}
	}
	if s.injected[f][elevio.BT_HallUp] {
		cleared.HallUp = true
		s.localHall[f][0] = false
		if !online {
			s.injected[f][elevio.BT_HallUp] = false
		}
	}
	if s.injected[f][elevio.BT_HallDown] {
		cleared.HallDown = true
		s.localHall[f][1] = false
		if !online {
			s.injected[f][elevio.BT_HallDown] = false
		}
	}

	return cleared
}

func (s *FsmSync) BuildUpdateSnapshot(floor int, behavior string, direction string) common.Snapshot {
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
	}
}

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
	}
}

func (s *FsmSync) LightsSnapshot(online bool) common.Snapshot {
	if online && s.hasNet {
		return common.Snapshot{
			HallRequests: cloneHallSlice(s.netHall),
			States: map[string]common.ElevState{
				s.selfKey: {CabRequests: cloneBoolSlice(s.netCab)},
			},
		}
	}

	if !online {
		return common.Snapshot{
			HallRequests: cloneHallSlice(s.localHall),
			States: map[string]common.ElevState{
				s.selfKey: {CabRequests: cloneBoolSlice(s.localCab)},
			},
		}
	}

	// Startup grace: keep lights off until we have a snapshot or go offline.
	emptyHall := make([][2]bool, common.N_FLOORS)
	emptyCab := make([]bool, common.N_FLOORS)
	return common.Snapshot{
		HallRequests: emptyHall,
		States: map[string]common.ElevState{
			s.selfKey: {CabRequests: emptyCab},
		},
	}
}

func (s *FsmSync) ApplyLights(snap common.Snapshot) {
	hall := cloneHallSlice(snap.HallRequests)
	cab := make([]bool, common.N_FLOORS)
	if snap.States != nil {
		if st, ok := snap.States[s.selfKey]; ok {
			cab = cloneBoolSlice(st.CabRequests)
		}
	}

	output := common.ElevioGetOutputDevice()
	for floor := 0; floor < common.N_FLOORS; floor++ {
		output.RequestButtonLight(floor, elevio.BT_HallUp, hall[floor][0])
		output.RequestButtonLight(floor, elevio.BT_HallDown, hall[floor][1])
		output.RequestButtonLight(floor, elevio.BT_Cab, cab[floor])
	}
}

func (s *FsmSync) MotionChanged(floor int, behavior string, direction string) bool {
	if s.reportedFloor != floor || s.reportedBehavior != behavior || s.reportedDirection != direction {
		s.reportedFloor = floor
		s.reportedBehavior = behavior
		s.reportedDirection = direction
		return true
	}
	return false
}

func copyHall(dst [][2]bool, src [][2]bool) {
	if dst == nil {
		return
	}
	for i := 0; i < len(dst); i++ {
		if src != nil && i < len(src) {
			dst[i] = src[i]
		} else {
			dst[i] = [2]bool{false, false}
		}
	}
}

func cloneHallSlice(in [][2]bool) [][2]bool {
	out := make([][2]bool, common.N_FLOORS)
	copyHall(out, in)
	return out
}

func cloneBoolSlice(in []bool) []bool {
	out := make([]bool, common.N_FLOORS)
	for i := 0; i < common.N_FLOORS; i++ {
		if in != nil && i < len(in) {
			out[i] = in[i]
		} else {
			out[i] = false
		}
	}
	return out
}

func countHall(hall [][2]bool) int {
	if hall == nil {
		return 0
	}
	n := 0
	for i := 0; i < len(hall) && i < common.N_FLOORS; i++ {
		if hall[i][0] {
			n++
		}
		if hall[i][1] {
			n++
		}
	}
	return n
}
