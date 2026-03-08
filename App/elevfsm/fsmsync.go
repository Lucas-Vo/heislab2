package elevfsm

import (
	"elevator/common"
	"time"
)

const netOfflineTimeout = 5 * time.Second
const confirmTimeout = 200 * time.Millisecond

type FsmSync struct {
	selfKey string

	initFromNetwork bool
	lastNetSeen     time.Time
	hasAlivePeer    bool
	coherent        bool

	assignedHall [common.N_FLOORS][2]bool
	calls        [common.N_FLOORS][common.N_BUTTONS]common.CallInfo
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

	selfState, selfStateExists := snapshot.States[sync.selfKey]

	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			c := &sync.calls[floor][button]

			if button == common.BT_Cab {
				if !sync.initFromNetwork && selfState.CabRequests[floor] {
					c.State = common.CallPending
					c.Injected = true
					c.Time = time.Time{}
				}
				continue
			}

			if snapshot.HallRequests[floor][button] {
				c.State = common.CallConfirmed
				c.Injected = true
				c.Time = time.Time{}
			} else if c.State == common.CallConfirmed {
				c.State = common.CallNone
				c.Injected = false
				c.Time = time.Time{}
			}
		}
	}

	if selfStateExists {
		sync.initFromNetwork = true
	}
}

func (sync *FsmSync) HandleAssignerTask(task common.ElevInput) (toClear Requests) {
	previousAssignment := sync.assignedHall
	sync.assignedHall = task.HallTask

	for floor := range previousAssignment {
		if previousAssignment[floor][0] && !sync.assignedHall[floor][0] {
			c := &sync.calls[floor][common.BT_HallUp]
			c.State = common.CallNone
			c.Time = time.Time{}
			c.Injected = false
			toClear[floor][common.BT_HallUp] = true
		}
		if previousAssignment[floor][1] && !sync.assignedHall[floor][1] {
			c := &sync.calls[floor][common.BT_HallDown]
			c.State = common.CallNone
			c.Time = time.Time{}
			c.Injected = false
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
			c := &sync.calls[floor][button]
			c.State = common.CallPending
			c.Injected = false
			c.Time = now
			allowImmediateHallInject := !distributedOnline
			if button == common.BT_Cab || (currentFloor == floor && allowImmediateHallInject) {
				toInject[floor][button] = true
				c.Injected = true
				c.Time = time.Time{}
			}
		}
	}
	return toInject
}

func (sync *FsmSync) ReadyInjects(now time.Time, online bool) (toInject Requests) {
	distributedOnline := online && sync.hasAlivePeer
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			c := &sync.calls[floor][button]
			injectTimedOut := now.Sub(c.Time) >= time.Duration(confirmTimeout) && !c.Time.IsZero()
			if c.State == common.CallNone || c.Injected || injectTimedOut {
				continue
			}

			shouldInject := false
			if sync.coherent && distributedOnline && button != common.BT_Cab { //TODO: fix this entire logic wth is this
				shouldInject = sync.assignedHall[floor][button]
			} else if online {
				shouldInject = button == common.BT_Cab || sync.assignedHall[floor][button]
			} 

			if shouldInject {
				toInject[floor][button] = true
				c.Injected = true
				c.Time = time.Time{}
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
			c := &sync.calls[floor][button]
			c.State = common.CallNone
			c.Time = time.Time{}
			c.Injected = false
		}
	}
}

func (sync *FsmSync) BuildSnapshot(
	floor int, updateKind common.UpdateKind,
	callsCleared Requests, online bool,
	behavior string, direction string,
) common.Snapshot {
	outCalls := [common.N_FLOORS][common.N_BUTTONS]bool{}

	for f := range common.N_FLOORS {
		for b := range common.ButtonType(common.N_BUTTONS) {
			c := &sync.calls[f][b]

			switch updateKind {
			case common.UpdateRequests:
				if b != common.BT_Cab && c.State == common.CallPending {
					outCalls[f][b] = true
				}
			case common.UpdateServiced:
				if callsCleared[f][b] {
					outCalls[f][b] = false
				} else {
					outCalls[f][b] = true
				}
			}

			if b == common.BT_Cab && c.State != common.CallNone {
				outCalls[f][b] = true
			}
		}
	}
	return common.Snapshot{
		HallRequests: common.GetHallCalls(outCalls),
		States: map[string]common.ElevState{
			sync.selfKey: {
				Behavior:    behavior,
				Floor:       floor,
				Direction:   direction,
				CabRequests: common.GetCabCalls(outCalls),
			},
		},
		UpdateKind: updateKind,
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

func (sync *FsmSync) GetLocalCab() [common.N_FLOORS]bool {
	var out [common.N_FLOORS]bool
	for f := 0; f < common.N_FLOORS; f++ {
		if sync.calls[f][common.BT_Cab].State != common.CallNone {
			out[f] = true
		}
	}
	return out
}

func (sync *FsmSync) GetLocalHall() [common.N_FLOORS][2]bool {
	var out [common.N_FLOORS][2]bool
	for f := 0; f < common.N_FLOORS; f++ {
		for b := 0; b < 2; b++ {
			if sync.calls[f][b].State != common.CallNone {
				out[f][b] = true
			}
		}
	}
	return out
}

func (sync *FsmSync) GetNetHall() [common.N_FLOORS][2]bool {
	var out [common.N_FLOORS][2]bool
	for f := 0; f < common.N_FLOORS; f++ {
		for b := 0; b < 2; b++ {
			if sync.calls[f][b].State == common.CallConfirmed {
				out[f][b] = true
			}
		}
	}
	return out
}
