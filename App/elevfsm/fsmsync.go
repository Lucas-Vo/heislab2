package elevfsm

import (
	"elevator/common"
	"time"
)

const (
	NET_OFFLINE_TIMEOUT = 3 * time.Second
	NEW_REQUEST_TIMEOUT = 200 * time.Millisecond
)

type FsmSync struct {
	selfKey string

	initFromNetwork bool
	hasAlivePeer    bool
	coherent        bool

	assignedHall [common.N_FLOORS][2]bool
	callTime     [common.N_FLOORS][common.N_BUTTONS]time.Time

	netRequests   common.Requests
	localRequests common.Requests
}

func NewFsmSync(config common.Config) *FsmSync {
	return &FsmSync{selfKey: config.SelfKey}
}

func (sync *FsmSync) HandleNetworkSnapshot(snapshot common.Snapshot, now time.Time) {
	sync.hasAlivePeer = false
	sync.coherent = snapshot.Coherent
	for key, alive := range snapshot.Alive {
		if key != sync.selfKey && alive {
			if _, hasState := snapshot.States[key]; hasState {
				sync.hasAlivePeer = true
				break
			}
		}
	}

	if state, ok := snapshot.States[sync.selfKey]; ok {
		sync.initFromNetwork = true
		for floor := 0; floor < common.N_FLOORS && floor < len(state.CabRequests); floor++ {
			sync.netRequests[floor][common.BT_Cab] = state.CabRequests[floor]
		}
	}

	if sync.hasAlivePeer {
		for floor := range common.N_FLOORS {
			sync.netRequests[floor][0] = snapshot.HallRequests[floor][0]
			sync.netRequests[floor][1] = snapshot.HallRequests[floor][1]
		}
	}

	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			sync.localRequests[floor][button] = sync.netRequests[floor][button]
		}
	}

}

func (sync *FsmSync) HandleAssignerTask(task common.ElevInput) (toClear common.Requests) {
	previousAssignment := sync.assignedHall
	sync.assignedHall = task.HallTask

	for floor := range previousAssignment {
		if previousAssignment[floor][0] && !sync.assignedHall[floor][0] {
			sync.localRequests[floor][common.BT_HallUp] = false
			toClear[floor][common.BT_HallUp] = true
		}
		if previousAssignment[floor][1] && !sync.assignedHall[floor][1] {
			sync.localRequests[floor][common.BT_HallDown] = false
			toClear[floor][common.BT_HallDown] = true
		}
	}
	return toClear
}

func (sync *FsmSync) HandleLocalButtonPresses(ButtonPresses common.Requests, currentFloor int, now time.Time) (toInject common.Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if !ButtonPresses[floor][button] {
				continue
			}
			sync.localRequests[floor][button] = true
			if button == common.BT_Cab || (currentFloor == floor && !sync.hasAlivePeer) {
				toInject[floor][button] = true
			}
		}
	}
	return toInject
}

func (sync *FsmSync) ReadyInjects(now time.Time) (toInject common.Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			callActive := sync.localRequests[floor][button]
			if button != common.BT_Cab {
				if sync.hasAlivePeer && sync.coherent {
					callActive = true
				} else {
					callActive = sync.netRequests[floor][button] || callActive
				}
			}
			if !callActive {
				continue
			}
			shouldInject := button == common.BT_Cab || sync.assignedHall[floor][button]
			if sync.hasAlivePeer && button != common.BT_Cab {
				shouldInject = sync.coherent && sync.assignedHall[floor][button]
			}
			if shouldInject {
				toInject[floor][button] = true
				sync.localRequests[floor][button] = true
			}
		}
	}
	return toInject
}

func (sync *FsmSync) ClearServicedRequests(floor int, serviced common.Requests) {
	if floor < 0 || floor >= common.N_FLOORS {
		return
	}
	for button := range common.ButtonType(common.N_BUTTONS) {
		if serviced[floor][button] {
			sync.localRequests[floor][button] = false
			sync.netRequests[floor][button] = false
		}
	}
}

func (sync *FsmSync) BuildSnapshot( //TODO: Change the callsCleared to the thing that we fill both serviced and requests with
	elevator *Elevator,
	kind common.UpdateKind,
	callsCleared common.Requests,
) common.Snapshot {
	outCalls := sync.netRequests
	if kind == common.UpdateRequests {
		for f := range common.N_FLOORS {
			if sync.localRequests[f][common.BT_HallUp] {
				outCalls[f][common.BT_HallUp] = true
			}
			if sync.localRequests[f][common.BT_HallDown] {
				outCalls[f][common.BT_HallDown] = true
			}
		}
	}
	if kind == common.UpdateServiced {
		for floor := range common.N_FLOORS {
			if callsCleared[floor][common.BT_HallUp] {
				outCalls[floor][common.BT_HallUp] = false
			}
			if callsCleared[floor][common.BT_HallDown] {
				outCalls[floor][common.BT_HallDown] = false
			}
		}
	}
	behavior, direction := elevator.motionStrings()
	return common.Snapshot{
		HallRequests: common.GetHallCalls(outCalls),
		States: map[string]common.ElevState{
			sync.selfKey: {
				Behavior:    behavior,
				Floor:       elevator.GetPrevFloor(),
				Direction:   direction,
				CabRequests: common.GetCabCalls(sync.localRequests),
			},
		},
		UpdateKind: kind,
	}
}

func (sync *FsmSync) HasAlivePeer() bool { return sync.hasAlivePeer }

func (sync *FsmSync) IsInitFromNetwork() bool { return sync.initFromNetwork }

func (sync *FsmSync) GetlocalRequests() [common.N_FLOORS][common.N_BUTTONS]bool {
	return sync.localRequests
}
