package elevfsm

import (
	"elevator/common"
	"log"
	"time"
)

const (
	netOnlineTimeout   = 5 * time.Second
	confirmTimeout     = 200 * time.Millisecond
	defaultDoorOpenDur = 3 * time.Second
)

type ServicedAt struct { //TODO: maybe use the BTuttonType here instead of bools, but this is more explicit and easier to read
	HallUp   bool
	HallDown bool
	Cab      bool
}

type TickUpdates struct {
	HasServiced bool
	Serviced    common.Snapshot
	HasRequests bool
	Requests    common.Snapshot
}

type FsmSync struct {
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

	elevator *Elevator

	previousRequests [common.N_FLOORS][common.N_BUTTONS]int
	prevObstructed   bool
	timerPaused      bool
	doorTimerEnd     time.Time
	doorTimerActive  bool
	announceDir      common.MotorDirection
	prevFloor        int
	prevDirection    common.MotorDirection
	prevBehaviour    ElevatorBehaviour
	doorOpenDuration time.Duration
}

func NewFsmSync(cfg common.Config) *FsmSync {
	s := &FsmSync{
		selfKey:          cfg.SelfKey,
		prevFloor:        -1,
		doorOpenDuration: defaultDoorOpenDur,
		elevator:         ElevatorInit(),
	}
	s.lastNetSeen = time.Now()
	return s
}

func (s *FsmSync) Initialize(input common.ElevInputDevice, now time.Time) common.Snapshot {
	s.lastNetSeen = now
	if floor := input.FloorSensor(); floor != -1 {
		s.elevator.OnFloorArrival(floor)
		s.prevFloor = floor
	} else {
		s.elevator.OnInitBetweenFloors()
		s.prevFloor = -1
	}
	s.prevDirection = s.elevator.GetDirection()
	s.prevBehaviour = s.elevator.GetBehaviour()
	return s.buildSnapshot(s.prevFloor, common.UpdateRequests, ServicedAt{}, now)
}

func (s *FsmSync) HandleNetwork(snap common.Snapshot, now time.Time) {
	s.applyNetworkSnapshot(snap, now)
	s.refreshOutputs(now)
}

func (s *FsmSync) HandleAssigner(task common.ElevInput, now time.Time) {
	s.applyAssigner(task)
	s.refreshOutputs(now)
}

func (s *FsmSync) Tick(input common.ElevInputDevice, now time.Time) (out TickUpdates) {
	elevStateChange := false

	for f := range common.N_FLOORS {
		for b := range common.N_BUTTONS {
			v := input.RequestButton(f, common.ButtonType(b))
			if v != 0 && v != s.previousRequests[f][b] {
				atFloor := input.FloorSensor() == f
				s.markLocalPress(f, common.ButtonType(b), now, atFloor)
				elevStateChange = true
			}
			s.previousRequests[f][b] = v
		}
	}

	newBehaviour := s.elevator.GetBehaviour()
	newDirection := s.elevator.GetDirection()
	newFloor := input.FloorSensor()
	doorJustClosed := s.prevBehaviour == EB_DoorOpen && newBehaviour != EB_DoorOpen
	if newFloor != s.prevFloor || newBehaviour != s.prevBehaviour || newDirection != s.prevDirection {
		elevStateChange = true
	}
	if newFloor != -1 && newFloor != s.prevFloor {
		s.elevator.OnFloorArrival(newFloor)
		s.prevFloor = newFloor
	}

	obstructed := input.Obstruction() != 0
	if s.elevator.GetBehaviour() == EB_DoorOpen {
		if obstructed {
			if !s.timerPaused {
				s.doorTimerActive = false
				s.timerPaused = true
			}
		} else if s.timerPaused || s.prevObstructed || s.shouldRestartDoorTimerOnCurrentFloor() {
			s.startDoorTimer(now)
		}
	} else {
		s.timerPaused = false
	}
	s.prevObstructed = obstructed

	if s.prevBehaviour != newBehaviour && newBehaviour == EB_DoorOpen {
		s.announceDir = s.chooseAnnounceDir(s.prevFloor, s.elevator.GetDirection())
		s.startDoorTimer(now)
	}
	s.prevBehaviour = newBehaviour
	s.prevDirection = newDirection

	servicedCall := ServicedAt{}
	if s.doorTimerActive && now.After(s.doorTimerEnd) {
		servicedCall = s.handleDoorTimerExpiry(now)
	}
	if doorJustClosed && s.prevFloor != -1 {
		stale := s.staleServicedHallAtFloor(s.prevFloor, now)
		servicedCall.HallUp = servicedCall.HallUp || stale.HallUp
		servicedCall.HallDown = servicedCall.HallDown || stale.HallDown
	}

	s.refreshOutputs(now)

	if !s.hasNetSelf {
		return out
	}
	if servicedCall.HallUp || servicedCall.HallDown || servicedCall.Cab {
		out.HasServiced = true
		out.Serviced = s.buildSnapshot(s.prevFloor, common.UpdateServiced, servicedCall, now)
	}
	if elevStateChange {
		out.HasRequests = true
		out.Requests = s.buildSnapshot(s.prevFloor, common.UpdateRequests, ServicedAt{}, now)
	}
	return out
}

func (s *FsmSync) isOnline(now time.Time) bool {
	return now.Sub(s.lastNetSeen) < netOnlineTimeout
}

func (s *FsmSync) refreshOutputs(now time.Time) {
	s.tryInjectAll(now)
	s.applyLights(now)
}

func (s *FsmSync) applyAssigner(task common.ElevInput) {
	previousAssignment := s.assignedHall
	s.assignedHall = task.HallTask
	s.hasAssigner = true
	s.cancelUnassigned(previousAssignment)
}

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

	s.callTimestamp[f][btn] = time.Time{}
	s.injected[f][btn] = false
	s.confirmed[f][btn] = false
	s.localCalls[f][btn] = false
	s.elevator.requests[f][btn] = false
}

func (s *FsmSync) applyNetworkSnapshot(snap common.Snapshot, now time.Time) {
	s.hasNet = true
	s.lastNetSeen = now

	for f := range common.N_FLOORS {
		s.netCalls[f][0] = snap.HallRequests[f][0]
		s.netCalls[f][1] = snap.HallRequests[f][1]
	}
	if s.copyCabFromSnapshot(&snap) {
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

func (s *FsmSync) copyCabFromSnapshot(snapshot *common.Snapshot) bool {
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

func (s *FsmSync) markLocalPress(f int, btn common.ButtonType, now time.Time, atFloor bool) {
	s.markPending(f, btn, now)
	s.localCalls[f][btn] = true
	if atFloor {
		s.elevator.OnRequestButtonPress(f, btn)
	}
}

func (s *FsmSync) shouldRestartDoorTimerOnCurrentFloor() bool {
	if s.prevFloor < 0 || s.prevFloor >= common.N_FLOORS {
		return false
	}
	moving := s.elevator.GetDirection() != common.MD_Stop
	return (s.previousRequests[s.prevFloor][common.BT_HallUp] != 0 && moving) ||
		(s.previousRequests[s.prevFloor][common.BT_HallDown] != 0 && moving) ||
		s.previousRequests[s.prevFloor][common.BT_Cab] != 0
}

func (s *FsmSync) hallRequestsAtFloor(floor int) (up bool, down bool) {
	if floor < 0 || floor >= common.N_FLOORS {
		return false, false
	}
	return s.elevator.requests[floor][common.BT_HallUp], s.elevator.requests[floor][common.BT_HallDown]
}

func (s *FsmSync) chooseAnnounceDir(floor int, fallback common.MotorDirection) common.MotorDirection {
	up, down := s.hallRequestsAtFloor(floor)
	if up && !down {
		return common.MD_Up
	}
	if down && !up {
		return common.MD_Down
	}
	if up && down {
		if fallback == common.MD_Up || fallback == common.MD_Down {
			return fallback
		}
		return common.MD_Up
	}
	return fallback
}

func (s *FsmSync) markPending(f int, btn common.ButtonType, now time.Time) {
	s.callTimestamp[f][btn] = now
}

func (s *FsmSync) inject(f int, btn common.ButtonType) {
	s.elevator.OnRequestButtonPress(f, btn)
	s.injected[f][btn] = true
	s.callTimestamp[f][btn] = time.Time{}
	s.localCalls[f][btn] = true
}

func (s *FsmSync) tryInjectAll(now time.Time) {
	online := s.isOnline(now)
	calls := s.localCalls
	if online && s.hasNet {
		calls = s.netCalls
	}
	for f := range common.N_FLOORS {
		for btn := range common.ButtonType(common.N_BUTTONS) {
			if !calls[f][btn] || s.injected[f][btn] {
				continue
			}
			callTimestamp := s.callTimestamp[f][btn]
			timedOut := callTimestamp.IsZero() || now.Sub(callTimestamp) >= confirmTimeout
			shouldInject := (!online && timedOut) || (online && (btn == common.BT_Cab || (s.hasAssigner && s.assignedHall[f][btn])))
			if shouldInject {
				s.inject(f, btn)
			} else if online && s.hasAssigner && btn != common.BT_Cab && !s.assignedHall[f][btn] && !callTimestamp.IsZero() {
				log.Printf("fsmThread: hall f=%d btn=%v assigned elsewhere", f, btn)
				s.callTimestamp[f][btn] = time.Time{}
			}
		}
	}
}

func (s *FsmSync) startDoorTimer(now time.Time) {
	s.doorTimerEnd = now.Add(s.doorOpenDuration)
	s.doorTimerActive = true
	s.timerPaused = false
}

func (s *FsmSync) handleDoorTimerExpiry(now time.Time) (servicedCall ServicedAt) {
	s.doorTimerActive = false
	s.timerPaused = false
	upReq, downReq := s.hallRequestsAtFloor(s.prevFloor)

	switch {
	case s.announceDir == common.MD_Up && upReq:
		servicedCall = s.clearAtFloor(s.prevFloor, common.MD_Up, true, now)
		if downReq {
			s.announceDir = common.MD_Down
			s.startDoorTimer(now)
		} else {
			s.elevator.OnDoorTimeout()
		}
	case s.announceDir == common.MD_Down && downReq:
		servicedCall = s.clearAtFloor(s.prevFloor, common.MD_Down, true, now)
		if upReq {
			s.announceDir = common.MD_Up
			s.startDoorTimer(now)
		} else {
			s.elevator.OnDoorTimeout()
		}
	case upReq || downReq:
		s.announceDir = s.chooseAnnounceDir(s.prevFloor, s.elevator.GetDirection())
		servicedCall = s.clearAtFloor(s.prevFloor, s.announceDir, true, now)
		if s.announceDir == common.MD_Up && downReq {
			s.announceDir = common.MD_Down
			s.startDoorTimer(now)
		} else if s.announceDir == common.MD_Down && upReq {
			s.announceDir = common.MD_Up
			s.startDoorTimer(now)
		} else {
			s.elevator.OnDoorTimeout()
		}
	default:
		servicedCall = s.clearAtFloor(s.prevFloor, common.MD_Stop, true, now)
		s.elevator.OnDoorTimeout()
	}
	return servicedCall
}

func (s *FsmSync) clearAtFloor(floor int, announceDir common.MotorDirection, clearCab bool, now time.Time) (servicedAt ServicedAt) {
	online := s.isOnline(now)
	s.elevator.floor = floor
	*s.elevator, servicedAt = requests_clearAtCurrentFloorDir(*s.elevator, announceDir, clearCab)
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
	return servicedAt
}

func (s *FsmSync) staleServicedHallAtFloor(floor int, now time.Time) (servicedAt ServicedAt) {
	online := s.isOnline(now)
	if floor < 0 || floor >= common.N_FLOORS {
		return servicedAt
	}
	calls := s.localCalls
	if online && s.hasNet {
		calls = s.netCalls
	}
	if calls[floor][common.BT_HallUp] && s.injected[floor][common.BT_HallUp] && !s.elevator.requests[floor][common.BT_HallUp] {
		servicedAt.HallUp = true
		s.localCalls[floor][common.BT_HallUp] = false
		if !online {
			s.injected[floor][common.BT_HallUp] = false
		}
	}
	if calls[floor][common.BT_HallDown] && s.injected[floor][common.BT_HallDown] && !s.elevator.requests[floor][common.BT_HallDown] {
		servicedAt.HallDown = true
		s.localCalls[floor][common.BT_HallDown] = false
		if !online {
			s.injected[floor][common.BT_HallDown] = false
		}
	}
	return servicedAt
}

func (s *FsmSync) buildSnapshot(floor int, kind common.UpdateKind, callsCleared ServicedAt, now time.Time) common.Snapshot {
	online := s.isOnline(now)
	outCalls := s.localCalls
	if kind == common.UpdateServiced && online && s.hasNet {
		outCalls = s.netCalls
	}
	if kind == common.UpdateServiced && floor >= 0 && floor < len(outCalls) {
		if callsCleared.HallUp {
			outCalls[floor][common.BT_HallUp] = false
		}
		if callsCleared.HallDown {
			outCalls[floor][common.BT_HallDown] = false
		}
	}
	behavior, direction := s.elevator.GetMotionStrings()
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

func (s *FsmSync) applyLights(now time.Time) {
	online := s.isOnline(now)
	calls := s.localCalls
	if online && s.hasNet {
		calls = s.netCalls
	}
	for floor := range common.N_FLOORS {
		for btn := range common.ButtonType(common.N_BUTTONS) {
			s.elevator.SwitchLight(floor, btn, calls[floor][btn])
		}
	}
}
