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
	netCalls     common.Requests
	localCalls   common.Requests
	injected     common.Requests
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
	for floor := range common.N_FLOORS {
		sync.netCalls[floor][0] = snapshot.HallRequests[floor][0]
		sync.netCalls[floor][1] = snapshot.HallRequests[floor][1]
		sync.netCalls[floor][common.BT_Cab] = false
	}
	if state, ok := snapshot.States[sync.selfKey]; ok {
		sync.initFromNetwork = true
		for floor := 0; floor < common.N_FLOORS && floor < len(state.CabRequests); floor++ {
			sync.netCalls[floor][common.BT_Cab] = state.CabRequests[floor]
		}
	}

	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if sync.netCalls[floor][button] {
				if button == common.BT_Cab {
					sync.localCalls[floor][button] = true
				}
				continue
			}
			sync.localCalls[floor][button] = false
			sync.injected[floor][button] = false
		}
	}
}

func (sync *FsmSync) HandleAssignerTask(task common.ElevInput) (toClear common.Requests) {
	previousAssignment := sync.assignedHall
	sync.assignedHall = task.HallTask

	for floor := range previousAssignment {
		if previousAssignment[floor][0] && !sync.assignedHall[floor][0] {
			sync.injected[floor][common.BT_HallUp] = false
			sync.localCalls[floor][common.BT_HallUp] = false
			toClear[floor][common.BT_HallUp] = true
		}
		if previousAssignment[floor][1] && !sync.assignedHall[floor][1] {
			sync.injected[floor][common.BT_HallDown] = false
			sync.localCalls[floor][common.BT_HallDown] = false
			toClear[floor][common.BT_HallDown] = true
		}
	}
	return toClear
}

func (sync *FsmSync) HandleLocalButtonPresses(edgePresses common.Requests, currentFloor int, now time.Time) (toInject common.Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if !edgePresses[floor][button] {
				continue
			}
			sync.localCalls[floor][button] = true
			allowImmediateHallInject := !sync.hasAlivePeer
			if button == common.BT_Cab || (currentFloor == floor && allowImmediateHallInject) {
				toInject[floor][button] = true
				sync.markInjected(floor, button)
			}
		}
	}
	return toInject
}

func (sync *FsmSync) ReadyInjects(now time.Time) (toInject common.Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			callActive := sync.localCalls[floor][button]
			if sync.hasAlivePeer && button != common.BT_Cab {
				callActive = sync.coherent && sync.netCalls[floor][button]
			} else if button != common.BT_Cab {
				callActive = sync.netCalls[floor][button] || sync.localCalls[floor][button]
			}
			if !callActive || sync.injected[floor][button] {
				continue
			}

			shouldInject := button == common.BT_Cab || sync.assignedHall[floor][button]
			if sync.hasAlivePeer && button != common.BT_Cab {
				shouldInject = sync.coherent && sync.assignedHall[floor][button]
			}
			if shouldInject {
				toInject[floor][button] = true
				sync.markInjected(floor, button)
				continue
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
			sync.localCalls[floor][button] = false
			// Ensure lamps and local world-view reflect service immediately.
			sync.netCalls[floor][button] = false
			sync.injected[floor][button] = false
		}
	}
}

func (sync *FsmSync) BuildSnapshot(
	elevator *Elevator,
	kind common.UpdateKind,
	callsCleared common.Requests,
) common.Snapshot {
	outCalls := sync.netCalls
	if kind == common.UpdateRequests {
		for f := range common.N_FLOORS {
			if sync.localCalls[f][common.BT_HallUp] {
				outCalls[f][common.BT_HallUp] = true
			}
			if sync.localCalls[f][common.BT_HallDown] {
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
				CabRequests: common.GetCabCalls(sync.localCalls),
			},
		},
		UpdateKind: kind,
	}
}

func (sync *FsmSync) HasAlivePeer() bool { return sync.hasAlivePeer }

func (sync *FsmSync) IsInitFromNetwork() bool { return sync.initFromNetwork }

func (sync *FsmSync) markInjected(floor int, button common.ButtonType) {
	sync.injected[floor][button] = true
	sync.localCalls[floor][button] = true
}

func (sync *FsmSync) GetLocalCalls() [common.N_FLOORS][common.N_BUTTONS]bool {
	return sync.localCalls
}
