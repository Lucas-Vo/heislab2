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

	assignedHall     [common.N_FLOORS][2]bool
	netRequests      common.Requests
	localRequests    common.Requests
	injectedRequests common.Requests
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
		sync.netRequests[floor][0] = snapshot.HallRequests[floor][0]
		sync.netRequests[floor][1] = snapshot.HallRequests[floor][1]
		sync.netRequests[floor][common.BT_Cab] = false
	}
	if state, ok := snapshot.States[sync.selfKey]; ok {
		sync.initFromNetwork = true
		for floor := 0; floor < common.N_FLOORS && floor < len(state.CabRequests); floor++ {
			sync.netRequests[floor][common.BT_Cab] = state.CabRequests[floor]
		}
	}

	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if sync.netRequests[floor][button] {
				if button == common.BT_Cab {
					sync.localRequests[floor][button] = true
				}
				continue
			}
			sync.localRequests[floor][button] = false
			sync.injectedRequests[floor][button] = false
		}
	}
}

func (sync *FsmSync) HandleAssignerTask(task common.ElevInput) (toRevoke common.Requests) {
	previousAssignment := sync.assignedHall
	sync.assignedHall = task.HallTask

	for floor := range previousAssignment {
		if previousAssignment[floor][0] && !sync.assignedHall[floor][0] {
			sync.injectedRequests[floor][common.BT_HallUp] = false
			sync.localRequests[floor][common.BT_HallUp] = false
			toRevoke[floor][common.BT_HallUp] = true
		}
		if previousAssignment[floor][1] && !sync.assignedHall[floor][1] {
			sync.injectedRequests[floor][common.BT_HallDown] = false
			sync.localRequests[floor][common.BT_HallDown] = false
			toRevoke[floor][common.BT_HallDown] = true
		}
	}
	return toRevoke
}

func (sync *FsmSync) HandleButtonPresses(edgePresses common.Requests, currentFloor int, now time.Time) (cabsToInject common.Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if !edgePresses[floor][button] {
				continue
			}
			sync.localRequests[floor][button] = true
			if button == common.BT_Cab {
				cabsToInject[floor][button] = true
				sync.markInjected(floor, button)
			}
		}
	}
	return cabsToInject
}

func (sync *FsmSync) ReadyInjects(now time.Time) (toInject common.Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			requestActive := sync.localRequests[floor][button]
			if sync.hasAlivePeer && button != common.BT_Cab {
				requestActive = sync.coherent && sync.netRequests[floor][button]
			} else if button != common.BT_Cab {
				requestActive = sync.netRequests[floor][button] || sync.localRequests[floor][button]
			}
			if !requestActive || sync.injectedRequests[floor][button] {
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
			sync.localRequests[floor][button] = false
			// Ensure lamps and local world-view reflect service immediately.
			sync.netRequests[floor][button] = false
			sync.injectedRequests[floor][button] = false
		}
	}
}

func (sync *FsmSync) BuildSnapshot(
	elevator *Elevator,
	kind common.UpdateKind,
	requestsCleared common.Requests,
) common.Snapshot {
	outRequests := sync.netRequests
	if kind == common.UpdateRequests {
		for f := range common.N_FLOORS {
			if sync.localRequests[f][common.BT_HallUp] {
				outRequests[f][common.BT_HallUp] = true
			}
			if sync.localRequests[f][common.BT_HallDown] {
				outRequests[f][common.BT_HallDown] = true
			}
		}
	}
	if kind == common.UpdateServiced {
		for floor := range common.N_FLOORS {
			if requestsCleared[floor][common.BT_HallUp] {
				outRequests[floor][common.BT_HallUp] = false
			}
			if requestsCleared[floor][common.BT_HallDown] {
				outRequests[floor][common.BT_HallDown] = false
			}
		}
	}
	behavior, direction := elevator.motionStrings()
	return common.Snapshot{
		HallRequests: common.GetHallRequests(outRequests),
		States: map[string]common.ElevState{
			sync.selfKey: {
				Behavior:    behavior,
				Floor:       elevator.GetPrevFloor(),
				Direction:   direction,
				CabRequests: common.GetCabRequests(sync.localRequests),
			},
		},
		UpdateKind: kind,
	}
}

func (sync *FsmSync) HasAlivePeer() bool { return sync.hasAlivePeer }

func (sync *FsmSync) IsInitFromNetwork() bool { return sync.initFromNetwork }

func (sync *FsmSync) markInjected(floor int, button common.ButtonType) {
	sync.injectedRequests[floor][button] = true
	sync.localRequests[floor][button] = true
}

func (sync *FsmSync) GetNetRequests() [common.N_FLOORS][common.N_BUTTONS]bool {
	return sync.netRequests
}
