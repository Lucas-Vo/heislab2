package main

import (
	elevio "Driver-go/elevio"
	"context"
	"log"
	"time"

	"elevator/common"
	"elevator/elevfsm"
)

const netOfflineTimeout = 3 * time.Second

type fsmSync struct {
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

type servicedAt struct {
	hallUp   bool
	hallDown bool
	cab      bool
}

func newFsmSync(cfg common.Config) *fsmSync {
	s := &fsmSync{
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

func (s *fsmSync) offline(now time.Time) bool {
	return now.Sub(s.lastNetSeen) > netOfflineTimeout
}

func (s *fsmSync) applyAssigner(task common.ElevInput) {
	if s.assignedHall == nil || len(s.assignedHall) != common.N_FLOORS {
		s.assignedHall = make([][2]bool, common.N_FLOORS)
	}
	prev := cloneHallSlice(s.assignedHall)
	copyHall(s.assignedHall, task.HallTask)
	s.hasAssigner = true
	s.cancelUnassigned(prev)
}

func (s *fsmSync) cancelUnassigned(prev [][2]bool) {
	for f := 0; f < common.N_FLOORS; f++ {
		if prev[f][0] && !s.assignedHall[f][0] {
			s.cancelHall(f, elevio.BT_HallUp, "unassigned")
		}
		if prev[f][1] && !s.assignedHall[f][1] {
			s.cancelHall(f, elevio.BT_HallDown, "unassigned")
		}
	}
}

func (s *fsmSync) cancelHall(f int, btn elevio.ButtonType, reason string) {
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
	elevfsm.Fsm_clearRequest(f, btn)
}

func (s *fsmSync) applyNetworkSnapshot(snap common.Snapshot, now time.Time) {
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

func (s *fsmSync) copyCabFromSnapshot(snap common.Snapshot) {
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

func (s *fsmSync) onLocalPress(f int, btn elevio.ButtonType, now time.Time) {
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

func (s *fsmSync) markPending(f int, btn elevio.ButtonType, now time.Time) {
	if s.pendingAt[f][btn].IsZero() {
		s.pendingAt[f][btn] = now
		log.Printf("fsmThread: pending request f=%d b=%s (local press)", f, common.ElevioButtonToString(btn))
	}
}

func (s *fsmSync) inject(f int, btn elevio.ButtonType, reason string) {
	if s.injected[f][btn] {
		return
	}
	log.Printf("fsmThread: inject request f=%d b=%s (%s)", f, common.ElevioButtonToString(btn), reason)
	elevfsm.Fsm_onRequestButtonPress(f, btn)
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

func (s *fsmSync) tryInjectOnline() {
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

func (s *fsmSync) tryInjectOffline(now time.Time, confirmTimeout time.Duration) {
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

func (s *fsmSync) readyToInject(f int, btn elevio.ButtonType, now time.Time, confirmTimeout time.Duration) bool {
	if s.injected[f][btn] {
		return false
	}
	if s.pendingAt[f][btn].IsZero() {
		return true
	}
	return now.Sub(s.pendingAt[f][btn]) >= confirmTimeout
}

func (s *fsmSync) clearAtFloor(f int, online bool) servicedAt {
	if f < 0 || f >= common.N_FLOORS {
		return servicedAt{}
	}

	var cleared servicedAt

	if s.injected[f][elevio.BT_Cab] {
		cleared.cab = true
		s.localCab[f] = false
		if !online {
			s.injected[f][elevio.BT_Cab] = false
		}
	}
	if s.injected[f][elevio.BT_HallUp] {
		cleared.hallUp = true
		s.localHall[f][0] = false
		if !online {
			s.injected[f][elevio.BT_HallUp] = false
		}
	}
	if s.injected[f][elevio.BT_HallDown] {
		cleared.hallDown = true
		s.localHall[f][1] = false
		if !online {
			s.injected[f][elevio.BT_HallDown] = false
		}
	}

	return cleared
}

func (s *fsmSync) buildUpdateSnapshot(floor int, behavior string, direction string) common.Snapshot {
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

func (s *fsmSync) buildServicedSnapshot(floor int, behavior string, direction string, cleared servicedAt, online bool) common.Snapshot {
	baseHall := s.localHall
	if online && s.hasNet {
		baseHall = s.netHall
	}

	outHall := cloneHallSlice(baseHall)
	if floor >= 0 && floor < len(outHall) {
		if cleared.hallUp {
			outHall[floor][0] = false
		}
		if cleared.hallDown {
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

func (s *fsmSync) applyLights(online bool) {
	if online && s.hasNet {
		snap := common.Snapshot{
			HallRequests: cloneHallSlice(s.netHall),
			States: map[string]common.ElevState{
				s.selfKey: {CabRequests: cloneBoolSlice(s.netCab)},
			},
		}
		elevfsm.SetAllRequestLightsFromSnapshot(snap, s.selfKey)
		return
	}

	if !online {
		snap := common.Snapshot{
			HallRequests: cloneHallSlice(s.localHall),
			States: map[string]common.ElevState{
				s.selfKey: {CabRequests: cloneBoolSlice(s.localCab)},
			},
		}
		elevfsm.SetAllRequestLightsFromSnapshot(snap, s.selfKey)
		return
	}

	// Startup grace: keep lights off until we have a snapshot or go offline.
	emptyHall := make([][2]bool, common.N_FLOORS)
	emptyCab := make([]bool, common.N_FLOORS)
	snap := common.Snapshot{
		HallRequests: emptyHall,
		States: map[string]common.ElevState{
			s.selfKey: {CabRequests: emptyCab},
		},
	}
	elevfsm.SetAllRequestLightsFromSnapshot(snap, s.selfKey)
}

func (s *fsmSync) motionChanged(floor int, behavior string, direction string) bool {
	if s.reportedFloor != floor || s.reportedBehavior != behavior || s.reportedDirection != direction {
		s.reportedFloor = floor
		s.reportedBehavior = behavior
		s.reportedDirection = direction
		return true
	}
	return false
}

func fsmThread(
	ctx context.Context,
	cfg common.Config,
	input common.ElevInputDevice,
	assignerOutputCh <-chan common.ElevInput,
	elevServicedCh chan<- common.Snapshot,
	elevUpdateCh chan<- common.Snapshot,
	netWorldView2Ch <-chan common.Snapshot, // network -> fsm
) {
	log.Printf("fsmThread started (self=%s)", cfg.SelfKey)

	elevfsm.Fsm_init()

	inputPollRateMs := 25
	confirmTimeoutMs := 1500
	elevfsm.ConLoad("elevator.con",
		elevfsm.ConVal("inputPollRate_ms", &inputPollRateMs, "%d"),
		elevfsm.ConVal("requestConfirmTimeout_ms", &confirmTimeoutMs, "%d"),
	)

	initFloor := input.FloorSensor()
	if initFloor == -1 {
		elevfsm.Fsm_onInitBetweenFloors()
	} else {
		// Initialize FSM floor state immediately to avoid floor=-1 in request handling.
		elevfsm.Fsm_onFloorArrival(initFloor)
	}

	sync := newFsmSync(cfg)
	var prevReq [common.N_FLOORS][common.N_BUTTONS]int
	prevFloor := initFloor
	lastFloorSeen := time.Now()
	stuckWarned := false
	onlineKnown := false
	prevOnline := false

	ticker := time.NewTicker(time.Duration(inputPollRateMs) * time.Millisecond)
	defer ticker.Stop()

	behavior, direction := elevfsm.CurrentMotionStrings()
	for {
		select {
		case <-ctx.Done():
			return

		case snap := <-netWorldView2Ch:
			now := time.Now()
			sync.applyNetworkSnapshot(snap, now)
			//log.Printf("fsmThread: net snapshot hall=%d cab_self=%d", countHall(snap.HallRequests), countCabFromSnapshot(snap, cfg.SelfKey))

			online := !sync.offline(now)
			if online {
				sync.tryInjectOnline()
			}
			sync.applyLights(online)

		case task := <-assignerOutputCh:
			sync.applyAssigner(task)
			//log.Printf("fsmThread: assigner update assigned_hall=%d", countHall(sync.assignedHall))
			now := time.Now()
			if !sync.offline(now) {
				sync.tryInjectOnline()
			}

		case <-ticker.C:
			now := time.Now()
			online := !sync.offline(now)
			if !onlineKnown || online != prevOnline {
				state := "offline"
				if online {
					state = "online"
				}
				log.Printf("fsmThread: network %s (lastNetSeen=%s)", state, sync.lastNetSeen.Format(time.RFC3339Nano))
				prevOnline = online
				onlineKnown = true
			}
			changedNew := false
			changedServiced := false
			var cleared servicedAt

			// Request buttons (edge-detected)
			for f := 0; f < common.N_FLOORS; f++ {
				for b := 0; b < common.N_BUTTONS; b++ {
					v := input.RequestButton(f, elevio.ButtonType(b))
					if v != 0 && v != prevReq[f][b] {
						sync.onLocalPress(f, elevio.ButtonType(b), now)
						changedNew = true
					}
					prevReq[f][b] = v
				}
			}

			// Floor sensor
			f := input.FloorSensor()
			if f != -1 && f != prevFloor {
				elevfsm.Fsm_onFloorArrival(f)
				prevFloor = f
				lastFloorSeen = now
				stuckWarned = false
				changedNew = true
			}
			if f == -1 && now.Sub(lastFloorSeen) > 5*time.Second && !stuckWarned {
				log.Printf("fsmThread: warning: floor sensor reports -1 for >5s (possible hardware/sensor issue)")
				stuckWarned = true
			}

			// Timer
			if elevfsm.Timer_timedOut() != 0 {
				elevfsm.Timer_stop()
				elevfsm.Fsm_onDoorTimeout()

				cleared = sync.clearAtFloor(prevFloor, online)
				if cleared.hallUp || cleared.hallDown || cleared.cab {
					changedServiced = true
					log.Printf("fsmThread: serviced requests at floor %d", prevFloor)
				}
			}

			// Inject confirmed requests
			if online {
				sync.tryInjectOnline()
			} else {
				confirmTimeout := time.Duration(confirmTimeoutMs) * time.Millisecond
				sync.tryInjectOffline(now, confirmTimeout)
			}

			sync.applyLights(online)

			behavior, direction = elevfsm.CurrentMotionStrings()
			if sync.motionChanged(prevFloor, behavior, direction) {
				changedNew = true
			}

			if changedServiced {
				snap := sync.buildServicedSnapshot(prevFloor, behavior, direction, cleared, online)
				select {
				case elevServicedCh <- snap:
				default:
				}
			}
			if changedNew {
				snap := sync.buildUpdateSnapshot(prevFloor, behavior, direction)
				select {
				case elevUpdateCh <- snap:
				default:
				}
			}
		}
	}
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

func countCabFromSnapshot(snap common.Snapshot, selfKey string) int {
	if snap.States == nil {
		return 0
	}
	st, ok := snap.States[selfKey]
	if !ok || st.CabRequests == nil {
		return 0
	}
	n := 0
	for i := 0; i < len(st.CabRequests) && i < common.N_FLOORS; i++ {
		if st.CabRequests[i] {
			n++
		}
	}
	return n
}
