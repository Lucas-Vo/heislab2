package elevfsm

import (
	"elevator/common"
	"log"
	"time"
)

// Allow a few missed snapshots before declaring offline.
const netOfflineTimeout = 5 * time.Second

const doorOpenDuration = 3 * time.Second

type FsmSync struct {
	config  common.Config
	selfKey string

	initFromNetwork bool
	lastNetSeen     time.Time

	assignedHall [common.N_FLOORS][2]bool

	netCalls   Requests
	localCalls Requests

	callTimestamp [common.N_FLOORS][common.N_BUTTONS]time.Time
	injected      Requests
	confirmed     Requests

	elevator *Elevator

	prevFloor     int
	prevBehaviour ElevatorBehaviour
	prevDirection common.MotorDirection

	doorTimerEnd time.Time
	announceDir  common.MotorDirection
}

// NewFsmSyncAndInit initializes sync, elevator IO and publishes an initial snapshot.
func NewFsmSyncAndInit(config common.Config, elevUpdateCh chan<- common.Snapshot) *FsmSync {
	s := &FsmSync{
		config:       config,
		selfKey:      config.SelfKey,
		assignedHall: [common.N_FLOORS][2]bool{},
		prevFloor:    -1,
	}
	s.elevator, s.prevFloor, s.prevDirection, s.prevBehaviour = elevatorInit("localhost:15657")
	s.lastNetSeen = time.Now()

	initialSnap := s.BuildSnapshot(s.prevFloor, common.UpdateRequests, Requests{}, time.Now())
	select {
	case elevUpdateCh <- initialSnap:
	default:
	}
	return s
}

func (s *FsmSync) HandleNetworkSnapshot(snap common.Snapshot, now time.Time, confirmTimeout time.Duration) {
	s.lastNetSeen = now

	for f := range common.N_FLOORS {
		s.netCalls[f][0] = snap.HallRequests[f][0]
		s.netCalls[f][1] = snap.HallRequests[f][1]
	}
	if s.fetchSelfFromSnapshot(&snap) {
		s.initFromNetwork = true
	}
	for f := range common.N_FLOORS {
		for btn := range common.ButtonType(common.N_BUTTONS) {
			s.syncRequestStateFromNetwork(f, btn)
		}

	}
	s.injectReadyRequests(now, confirmTimeout)
	s.updateButtonLights(now)
}

func (s *FsmSync) HandleAssignerTask(task common.ElevInput, now time.Time, confirmTimeout time.Duration) {
	previousAssignment := s.assignedHall
	s.assignedHall = task.HallTask
	s.cancelUnassignedHall(previousAssignment)
	s.injectReadyRequests(now, confirmTimeout)
	s.updateButtonLights(now)
}

func (s *FsmSync) Synchronize(now time.Time, confirmTimeout time.Duration) (elevStateChange bool, servicedFloor int, servicedCalls Requests) {
	servicedFloor, servicedCalls = -1, Requests{}
	newButtonPressed := s.localButtonPresses(now)

	newFloor, newBehaviour, newDirection, obstructed := s.elevator.PollSensors() //TODO: You dont tell me what todo

	if newFloor != s.prevFloor ||
		newBehaviour != s.prevBehaviour ||
		newDirection != s.prevDirection ||
		newButtonPressed {
		elevStateChange = true
	}
	if newFloor != -1 && s.prevFloor != newFloor {
		s.elevator.onFloorArrival(newFloor)
		s.prevFloor = newFloor
	}

	if obstructed && newBehaviour == EB_DoorOpen { //TODO: what about timing out the elevator?
		s.doorTimerEnd = now.Add(doorOpenDuration)
	}

	if s.prevBehaviour != newBehaviour && newBehaviour == EB_DoorOpen {
		arrivalDirn := s.elevator.dirn
		s.announceDir = s.elevator.chooseNewDirAtFloor(s.prevFloor, arrivalDirn)
		s.doorTimerEnd = now.Add(doorOpenDuration)
	}
	s.prevBehaviour, s.prevDirection = newBehaviour, newDirection

	if now.After(s.doorTimerEnd) && s.prevBehaviour == EB_DoorOpen {
		servicedFloor, servicedCalls = s.onDoorTimerExpiry(now)
	}

	s.injectReadyRequests(now, confirmTimeout)
	s.updateButtonLights(now)
	return elevStateChange, servicedFloor, servicedCalls
}

func (s *FsmSync) BuildSnapshot(floor int, kind common.UpdateKind, callsCleared Requests, now time.Time) common.Snapshot {
	online := s.isOnline(now)
	outCalls := s.localCalls
	if kind == common.UpdateServiced && online {
		outCalls = s.netCalls
	}

	if kind == common.UpdateServiced {
		for f := range common.N_FLOORS {
			if callsCleared[f][common.BT_HallUp] {
				outCalls[f][common.BT_HallUp] = false
			}
			if callsCleared[f][common.BT_HallDown] {
				outCalls[f][common.BT_HallDown] = false
			}
		}
	}
	behavior, direction := s.elevator.getMotionStrings()
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

func (s *FsmSync) cancelUnassignedHall(prev [common.N_FLOORS][2]bool) {
	for f := range prev {
		if prev[f][0] && !s.assignedHall[f][0] {
			s.callTimestamp[f][common.BT_HallUp] = time.Time{}
			s.injected[f][common.BT_HallUp] = false
			s.confirmed[f][common.BT_HallUp] = false
			s.localCalls[f][common.BT_HallUp] = false
			s.elevator.clearRequest(f, common.BT_HallUp)
		}
		if prev[f][1] && !s.assignedHall[f][1] {
			s.callTimestamp[f][common.BT_HallDown] = time.Time{}
			s.injected[f][common.BT_HallDown] = false
			s.confirmed[f][common.BT_HallDown] = false
			s.localCalls[f][common.BT_HallDown] = false
			s.elevator.clearRequest(f, common.BT_HallDown)
		}
	}
}

func (s *FsmSync) syncRequestStateFromNetwork(floor int, btn common.ButtonType) {
	wasConfirmed := s.confirmed[floor][btn]
	netCallActive := s.netCalls[floor][btn]

	if netCallActive {
		s.callTimestamp[floor][btn] = time.Time{}
		s.confirmed[floor][btn] = true
		if btn == common.BT_Cab {
			s.localCalls[floor][btn] = true
		}
		return
	}
	s.confirmed[floor][btn] = false
	if wasConfirmed {
		s.localCalls[floor][btn] = false
		s.injected[floor][btn] = false
	}
}

func (s *FsmSync) localButtonPresses(now time.Time) (newButtonPressed bool) {
	edgePresses, newButtonPressed := s.elevator.pollButtonPresses()
	currentFloor := s.elevator.floorSensor()
	for f := range common.N_FLOORS {
		for btn := range common.ButtonType(common.N_BUTTONS) {
			if !edgePresses[f][btn] {
				continue
			}
			s.callTimestamp[f][btn] = now
			s.localCalls[f][btn] = true
			if currentFloor == f {
				s.elevator.onRequestButtonPress(f, btn)
			}
		}
	}
	return newButtonPressed
}

func (s *FsmSync) injectReadyRequests(now time.Time, confirmTimeout time.Duration) { //TODO: WAYYY too ugly logic, needs refactor
	online := s.isOnline(now)
	calls := s.localCalls
	if online {
		calls = s.netCalls
	}

	for f := range common.N_FLOORS {
		for btn := range common.ButtonType(common.N_BUTTONS) {
			if !calls[f][btn] || s.injected[f][btn] {
				continue
			}
			callTimestamp := s.callTimestamp[f][btn]
			timedOut := callTimestamp.IsZero() || now.Sub(callTimestamp) >= confirmTimeout
			shouldInject :=
				(!online && timedOut) ||
					(online && (btn == common.BT_Cab || s.assignedHall[f][btn]))

			if shouldInject {
				s.inject(f, btn)
				continue
			}
			if online &&
				btn != common.BT_Cab &&
				!s.assignedHall[f][btn] &&
				!callTimestamp.IsZero() {

				log.Printf("fsmThread: hall f=%d btn=%v assigned elsewhere", f, btn)
				s.callTimestamp[f][btn] = time.Time{}
			}
		}
	}
}

func (s *FsmSync) clearServicedRequests(floor int, serviced Requests, now time.Time) {
	if floor < 0 || floor >= common.N_FLOORS {
		return
	}
	online := s.isOnline(now)
	for btn := range common.ButtonType(common.N_BUTTONS) {
		if serviced[floor][btn] && s.injected[floor][btn] {
			s.localCalls[floor][btn] = false
			if !online {
				s.injected[floor][btn] = false
			}
		}
	}
}

func (s *FsmSync) onDoorTimerExpiry(now time.Time) (servicedFloor int, servicedCalls Requests) {
	servicedFloor, servicedCalls = -1, Requests{}
	s.doorTimerEnd = now
	if s.prevFloor < 0 || s.prevFloor >= common.N_FLOORS {
		return servicedFloor, servicedCalls
	}

	servicedFloor = s.prevFloor
	servicedCalls, nextAnnouncedDir, resetDoorTimer := s.elevator.OnDoorClose(s.prevFloor, s.announceDir, true)
	s.announceDir = nextAnnouncedDir
	s.clearServicedRequests(s.prevFloor, servicedCalls, now)
	if resetDoorTimer {
		s.doorTimerEnd = now.Add(doorOpenDuration)
	}
	return servicedFloor, servicedCalls
}

func (s *FsmSync) updateButtonLights(now time.Time) {
	calls := s.localCalls
	if s.isOnline(now) {
		calls = s.netCalls
	}
	s.elevator.setRequestLights(calls)
}

func (s *FsmSync) fetchSelfFromSnapshot(snapshot *common.Snapshot) bool {
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

func (s *FsmSync) inject(f int, btn common.ButtonType) {
	s.elevator.onRequestButtonPress(f, btn)
	s.injected[f][btn] = true
	s.callTimestamp[f][btn] = time.Time{}
	s.localCalls[f][btn] = true
}

func (s *FsmSync) isOnline(now time.Time) bool {
	return now.Sub(s.lastNetSeen) < netOfflineTimeout
}

func (s *FsmSync) IsInitFromNetwork() bool {
	return s.initFromNetwork
}

func (s *FsmSync) GetCurrentFloor() int {
	return s.prevFloor
}
