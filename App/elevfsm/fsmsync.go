package elevfsm

import (
	"elevator/common"
	"time"
)

const (
	NET_OFFLINE_TIMEOUT = 5 * time.Second
	NEW_REQUEST_TIMEOUT = 200 * time.Millisecond
)

type CallSlot struct {
	Local       bool
	Net         bool
	Injected    bool
	Confirmed   bool
	RequestedAt time.Time
}

type CallMatrix [common.N_FLOORS][common.N_BUTTONS]CallSlot

type FsmSync struct {
	selfKey string

	initFromNetwork bool
	lastNetSeen     time.Time
	hasAlivePeer    bool
	coherent        bool

	assignedHall [common.N_FLOORS][2]bool
	calls        CallMatrix
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
		sync.calls[floor][common.BT_HallUp].Net = snapshot.HallRequests[floor][common.BT_HallUp]
		sync.calls[floor][common.BT_HallDown].Net = snapshot.HallRequests[floor][common.BT_HallDown]
		sync.calls[floor][common.BT_Cab].Net = false
	}
	if state, ok := snapshot.States[sync.selfKey]; ok {
		sync.initFromNetwork = true
		for floor := 0; floor < common.N_FLOORS && floor < len(state.CabRequests); floor++ {
			sync.calls[floor][common.BT_Cab].Net = state.CabRequests[floor]
		}
	}

	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			call := &sync.calls[floor][button]
			if call.Net {
				call.Confirmed = true
				if button == common.BT_Cab {
					call.RequestedAt = time.Time{}
					call.Local = true
				}
				continue
			}
			if call.Confirmed {
				call.Local = false
				call.Injected = false
			}
			call.Confirmed = false
		}
	}
}

func (sync *FsmSync) HandleAssignerTask(task common.ElevInput) (toClear Requests) {
	previousAssignment := sync.assignedHall
	sync.assignedHall = task.HallTask

	for floor := range previousAssignment {
		if previousAssignment[floor][0] && !sync.assignedHall[floor][0] {
			sync.clearCallState(floor, common.BT_HallUp)
			toClear[floor][common.BT_HallUp] = true
		}
		if previousAssignment[floor][1] && !sync.assignedHall[floor][1] {
			sync.clearCallState(floor, common.BT_HallDown)
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
			call := &sync.calls[floor][button]
			call.RequestedAt = now
			call.Local = true
			allowImmediateHallInject := !distributedOnline
			if button == common.BT_Cab || (currentFloor == floor && allowImmediateHallInject) {
				toInject[floor][button] = true
				sync.markInjected(floor, button)
			}
		}
	}
	return toInject
}

func (sync *FsmSync) ReadyInjects(now time.Time, online bool) (toInject Requests) {
	distributedOnline := online && sync.hasAlivePeer
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			call := &sync.calls[floor][button]
			if call.Injected || !sync.callReadyForInjection(call, floor, button, now, online, distributedOnline) {
				continue
			}
			toInject[floor][button] = true
			sync.markInjected(floor, button)
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
			// Ensure lamps and local world-view reflect service immediately.
			sync.clearCallState(floor, button)
		}
	}
}

func (sync *FsmSync) BuildSnapshot( //TODO: Change the callsCleared to the thing that we fill both serviced and requests with
	floor int,
	kind common.UpdateKind,
	callsCleared Requests,
	online bool,
	behavior string,
	direction string,
) common.Snapshot {
	outCalls := sync.localRequests()
	if online {
		outCalls = sync.netRequests()
	}
	if kind == common.UpdateRequests {
		for f := range common.N_FLOORS {
			if sync.calls[f][common.BT_HallUp].Local && !sync.calls[f][common.BT_HallUp].Confirmed {
				outCalls[f][common.BT_HallUp] = true
			}
			if sync.calls[f][common.BT_HallDown].Local && !sync.calls[f][common.BT_HallDown].Confirmed {
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
		HallRequests: common.GetHallCalls(outCalls),
		States: map[string]common.ElevState{
			sync.selfKey: {
				Behavior:    behavior,
				Floor:       floor,
				Direction:   direction,
				CabRequests: common.GetCabCalls(sync.localRequests()),
			},
		},
		UpdateKind: kind,
	}
}

func (sync *FsmSync) NetworkOnline(now time.Time) bool {
	return !sync.lastNetSeen.IsZero() && now.Sub(sync.lastNetSeen) < NET_OFFLINE_TIMEOUT
}

func (sync *FsmSync) HasAlivePeer() bool {
	return sync.hasAlivePeer
}

func (sync *FsmSync) IsInitFromNetwork() bool {
	return sync.initFromNetwork
}

func (sync *FsmSync) markInjected(floor int, button common.ButtonType) {
	call := &sync.calls[floor][button]
	call.Injected = true
	call.RequestedAt = time.Time{}
	call.Local = true
}

func (sync *FsmSync) GetLocalCab() [common.N_FLOORS]bool {
	return common.GetCabCalls(sync.localRequests())
}

func (sync *FsmSync) GetLocalHall() [common.N_FLOORS][2]bool {
	return common.GetHallCalls(sync.localRequests())
}

func (sync *FsmSync) GetNetHall() [common.N_FLOORS][2]bool {
	return common.GetHallCalls(sync.netRequests())
}

func (sync *FsmSync) callReadyForInjection(
	call *CallSlot,
	floor int,
	button common.ButtonType,
	now time.Time,
	online bool,
	distributedOnline bool,
) bool {
	if !sync.callActiveForMode(call, button, online, distributedOnline) {
		return false
	}

	if distributedOnline && button != common.BT_Cab {
		return sync.coherent && sync.assignedHall[floor][button]
	}
	if online {
		return button == common.BT_Cab || sync.assignedHall[floor][button]
	}
	if call.RequestedAt.IsZero() {
		return true
	}
	return now.Sub(call.RequestedAt) >= NEW_REQUEST_TIMEOUT
}

func (sync *FsmSync) callActiveForMode(call *CallSlot, button common.ButtonType, online bool, distributedOnline bool) bool {
	if button == common.BT_Cab {
		return call.Local
	}
	if distributedOnline {
		return sync.coherent && call.Net
	}
	if online {
		return call.Net || call.Local
	}
	return call.Local
}

func (sync *FsmSync) clearCallState(floor int, button common.ButtonType) {
	call := &sync.calls[floor][button]
	call.Local = false
	call.Net = false
	call.Injected = false
	call.Confirmed = false
	call.RequestedAt = time.Time{}
}

func (sync *FsmSync) localRequests() (out Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			out[floor][button] = sync.calls[floor][button].Local
		}
	}
	return out
}

func (sync *FsmSync) netRequests() (out Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			out[floor][button] = sync.calls[floor][button].Net
		}
	}
	return out
}
