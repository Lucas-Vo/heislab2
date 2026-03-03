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

	assignedHall [common.N_FLOORS][2]bool
	netCalls     Requests
	localCalls   Requests

	callTimestamp [common.N_FLOORS][common.N_BUTTONS]time.Time
	injected      Requests
	confirmed     Requests
}

func NewFsmSync(config common.Config) *FsmSync {
	return &FsmSync{
		selfKey:      config.SelfKey,
		assignedHall: [common.N_FLOORS][2]bool{},
	}
}

func (sync *FsmSync) HandleNetworkSnapshot(snapshot common.Snapshot, now time.Time) {
	sync.lastNetSeen = now

	for floor := range common.N_FLOORS {
		sync.netCalls[floor][0] = snapshot.HallRequests[floor][0]
		sync.netCalls[floor][1] = snapshot.HallRequests[floor][1]
	}
	if sync.fetchSelfFromSnapshot(&snapshot) {
		sync.initFromNetwork = true
	}

	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			wasConfirmed := sync.confirmed[floor][button]
			netCallActive := sync.netCalls[floor][button]

			if netCallActive {
				sync.callTimestamp[floor][button] = time.Time{}
				sync.confirmed[floor][button] = true
				if button == common.BT_Cab {
					sync.localCalls[floor][button] = true
				}
				continue
			}

			sync.confirmed[floor][button] = false
			if wasConfirmed {
				sync.localCalls[floor][button] = false
				sync.injected[floor][button] = false
			}
		}
	}
}

func (sync *FsmSync) HandleAssignerTask(task common.ElevInput, elevator *Elevator) {
	previousAssignment := sync.assignedHall
	sync.assignedHall = task.HallTask

	for floor := range previousAssignment {
		if previousAssignment[floor][0] && !sync.assignedHall[floor][0] {
			sync.callTimestamp[floor][common.BT_HallUp] = time.Time{}
			sync.injected[floor][common.BT_HallUp] = false
			sync.confirmed[floor][common.BT_HallUp] = false
			sync.localCalls[floor][common.BT_HallUp] = false
			elevator.ClearRequest(floor, common.BT_HallUp)
		}
		if previousAssignment[floor][1] && !sync.assignedHall[floor][1] {
			sync.callTimestamp[floor][common.BT_HallDown] = time.Time{}
			sync.injected[floor][common.BT_HallDown] = false
			sync.confirmed[floor][common.BT_HallDown] = false
			sync.localCalls[floor][common.BT_HallDown] = false
			elevator.ClearRequest(floor, common.BT_HallDown)
		}
	}
}

func (sync *FsmSync) HandleLocalButtonPresses(edgePresses Requests, now time.Time, elevator *Elevator) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if !edgePresses[floor][button] {
				continue
			}
			sync.callTimestamp[floor][button] = now
			sync.localCalls[floor][button] = true
			if elevator.FloorSensor() == floor {
				sync.inject(floor, button, elevator)
			}
		}
	}
}

func (sync *FsmSync) InjectReadyRequests(now time.Time, confirmTimeout time.Duration, online bool, elevator *Elevator) {
	calls := sync.localCalls
	if online {
		calls = sync.netCalls
	}

	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if !calls[floor][button] || sync.injected[floor][button] {
				continue
			}

			callTimestamp := sync.callTimestamp[floor][button]
			timedOut := callTimestamp.IsZero() || now.Sub(callTimestamp) >= confirmTimeout
			shouldInject := (!online && timedOut) || (online && (button == common.BT_Cab || sync.assignedHall[floor][button]))

			if shouldInject {
				sync.inject(floor, button, elevator)
				continue
			}

			if online && button != common.BT_Cab && !sync.assignedHall[floor][button] && !callTimestamp.IsZero() {
				sync.callTimestamp[floor][button] = time.Time{}
			}
		}
	}
}

func (sync *FsmSync) ClearServicedRequests(floor int, serviced Requests, online bool) {
	if floor < 0 || floor >= common.N_FLOORS {
		return
	}

	for button := range common.ButtonType(common.N_BUTTONS) {
		if serviced[floor][button] && sync.injected[floor][button] {
			sync.localCalls[floor][button] = false
			if !online {
				sync.injected[floor][button] = false
			}
		}
	}
}

func (sync *FsmSync) SetLights(online bool, elevator *Elevator) {
	if online {
		elevator.SetRequestLights(sync.netCalls)
		return
	}
	elevator.SetRequestLights(sync.localCalls)
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
	if kind == common.UpdateServiced && online {
		outCalls = sync.netCalls
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
	if sync.lastNetSeen.IsZero() {
		return false
	}
	return now.Sub(sync.lastNetSeen) < netOfflineTimeout
}

func (sync *FsmSync) IsInitFromNetwork() bool {
	return sync.initFromNetwork
}

func (sync *FsmSync) fetchSelfFromSnapshot(snapshot *common.Snapshot) bool {
	for floor := range common.N_FLOORS {
		sync.netCalls[floor][common.BT_Cab] = false
	}
	if snapshot.States == nil {
		return false
	}

	state, found := snapshot.States[sync.selfKey]
	if !found {
		return false
	}

	for floor := 0; floor < common.N_FLOORS && floor < len(state.CabRequests); floor++ {
		sync.netCalls[floor][common.BT_Cab] = state.CabRequests[floor]
	}
	return true
}

func (sync *FsmSync) inject(floor int, button common.ButtonType, elevator *Elevator) {
	elevator.InjectRequest(floor, button)
	sync.injected[floor][button] = true
	sync.callTimestamp[floor][button] = time.Time{}
	sync.localCalls[floor][button] = true
}
