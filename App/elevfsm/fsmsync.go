package elevfsm

import (
	"elevator/common"
	"time"
)

const netOfflineTimeout = 5 * time.Second

type FsmSync struct {
	selfKey string

	initFromNetwork bool
	lastNetSeen     time.Time
	hasAlivePeer    bool
	coherent        bool

	assignedHall [common.N_FLOORS][2]bool
	netCalls     Requests
	localCalls   Requests
	callTime     [common.N_FLOORS][common.N_BUTTONS]time.Time
	injected     Requests
	confirmed    Requests
}

func NewFsmSync(config common.Config) *FsmSync {
	return &FsmSync{selfKey: config.SelfKey}
}

func (sync *FsmSync) HandleNetworkSnapshot(snapshot common.Snapshot, now time.Time) {
	sync.lastNetSeen = now
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
		sync.netCalls[floor][0], sync.netCalls[floor][1] = snapshot.HallRequests[floor][0], snapshot.HallRequests[floor][1]
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
				sync.confirmed[floor][button] = true
				if button == common.BT_Cab {
					sync.callTime[floor][button] = time.Time{}
					sync.localCalls[floor][button] = true
				}
				continue
			}
			if sync.confirmed[floor][button] {
				sync.localCalls[floor][button] = false
				sync.injected[floor][button] = false
			}
			sync.confirmed[floor][button] = false
		}
	}
}

func (sync *FsmSync) HandleAssignerTask(task common.ElevInput) (toClear Requests) {
	previousAssignment := sync.assignedHall
	sync.assignedHall = task.HallTask

	for floor := range previousAssignment {
		if previousAssignment[floor][0] && !sync.assignedHall[floor][0] {
			sync.callTime[floor][common.BT_HallUp] = time.Time{}
			sync.injected[floor][common.BT_HallUp] = false
			sync.confirmed[floor][common.BT_HallUp] = false
			sync.localCalls[floor][common.BT_HallUp] = false
			toClear[floor][common.BT_HallUp] = true
		}
		if previousAssignment[floor][1] && !sync.assignedHall[floor][1] {
			sync.callTime[floor][common.BT_HallDown] = time.Time{}
			sync.injected[floor][common.BT_HallDown] = false
			sync.confirmed[floor][common.BT_HallDown] = false
			sync.localCalls[floor][common.BT_HallDown] = false
			toClear[floor][common.BT_HallDown] = true
		}
	}
	return toClear
}

func (sync *FsmSync) HandleLocalButtonPresses(edgePresses Requests, currentFloor int, now time.Time, online bool) (toInject Requests) {
	distributedOnline := online && sync.hasAlivePeer
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if !edgePresses[floor][button] {
				continue
			}
			sync.callTime[floor][button] = now
			sync.localCalls[floor][button] = true
			allowImmediateHallInject := !distributedOnline
			if button == common.BT_Cab || (currentFloor == floor && allowImmediateHallInject) {
				toInject[floor][button] = true
				sync.markInjected(floor, button)
			}
		}
	}
	return toInject
}

func (sync *FsmSync) ReadyInjects(now time.Time, confirmTimeout time.Duration, online bool) (toInject Requests) {
	distributedOnline := online && sync.hasAlivePeer
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			callActive := sync.localCalls[floor][button]
			if distributedOnline && button != common.BT_Cab {
				callActive = sync.coherent && sync.netCalls[floor][button]
			} else if online && button != common.BT_Cab {
				callActive = sync.netCalls[floor][button] || sync.localCalls[floor][button]
			}
			if !callActive || sync.injected[floor][button] {
				continue
			}

			hasTimestamp := !sync.callTime[floor][button].IsZero()
			timedOut := hasTimestamp && now.Sub(sync.callTime[floor][button]) >= confirmTimeout
			shouldInject := (online && (button == common.BT_Cab || sync.assignedHall[floor][button])) ||
				(!online && (!hasTimestamp || timedOut))
			if distributedOnline && button != common.BT_Cab {
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

func (sync *FsmSync) ClearServicedRequests(floor int, serviced Requests, online bool) {
	if floor < 0 || floor >= common.N_FLOORS {
		return
	}
	for button := range common.ButtonType(common.N_BUTTONS) {
		if serviced[floor][button] {
			sync.localCalls[floor][button] = false
			// Ensure lamps and local world-view reflect service immediately.
			sync.netCalls[floor][button] = false
			sync.confirmed[floor][button] = false
			sync.injected[floor][button] = false
			sync.callTime[floor][button] = time.Time{}
		}
	}
}

func (sync *FsmSync) CallsForLights(online bool) Requests {
	calls := sync.localCalls
	if online && sync.hasAlivePeer {
		for floor := range common.N_FLOORS {
			if sync.coherent {
				calls[floor][common.BT_HallUp] = sync.netCalls[floor][common.BT_HallUp]
				calls[floor][common.BT_HallDown] = sync.netCalls[floor][common.BT_HallDown]
			} else {
				calls[floor][common.BT_HallUp] = false
				calls[floor][common.BT_HallDown] = false
			}
		}
	}
	for floor := range common.N_FLOORS {
		calls[floor][common.BT_Cab] = sync.localCalls[floor][common.BT_Cab]
	}
	return calls
}

func (sync *FsmSync) BuildSnapshot(
	floor int,
	kind common.UpdateKind,
	callsCleared Requests,
	online bool,
	behavior string,
	direction string,
) common.Snapshot {
	outCalls := sync.localCalls
	if online {
		outCalls = sync.netCalls
	}
	if kind == common.UpdateRequests {
		for f := range common.N_FLOORS {
			if sync.localCalls[f][common.BT_HallUp] && !sync.confirmed[f][common.BT_HallUp] {
				outCalls[f][common.BT_HallUp] = true
			}
			if sync.localCalls[f][common.BT_HallDown] && !sync.confirmed[f][common.BT_HallDown] {
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

	return common.Snapshot{
		HallRequests: common.GetHallSlice(outCalls),
		States: map[string]common.ElevState{
			sync.selfKey: {
				Behavior:    behavior,
				Floor:       floor,
				Direction:   direction,
				CabRequests: common.GetCabSlice(sync.localCalls),
			},
		},
		UpdateKind: kind,
	}
}

func (sync *FsmSync) NetworkOnline(now time.Time) bool {
	return !sync.lastNetSeen.IsZero() && now.Sub(sync.lastNetSeen) < netOfflineTimeout
}

func (sync *FsmSync) HasAlivePeer() bool {
	return sync.hasAlivePeer
}

func (sync *FsmSync) IsInitFromNetwork() bool {
	return sync.initFromNetwork
}

func (sync *FsmSync) markInjected(floor int, button common.ButtonType) {
	sync.injected[floor][button] = true
	sync.callTime[floor][button] = time.Time{}
	sync.localCalls[floor][button] = true
}

func (sync *FsmSync) GetLocalCab() [common.N_FLOORS]bool {
	return common.GetCabSlice(sync.localCalls)
}

func (sync *FsmSync) GetLocalHall() [common.N_FLOORS][2]bool {
	return common.GetHallSlice(sync.localCalls)
}
