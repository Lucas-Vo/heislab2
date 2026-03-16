package elevfsm

import (
	"elevator/common"
	"time"
)

const (
	NET_OFFLINE_TIMEOUT = 3 * time.Second
	NEW_REQUEST_TIMEOUT = 200 * time.Millisecond
)

type Synchronizer struct {
	selfKey string

	initFromNetwork bool
	hasAlivePeer    bool
	coherent        bool

	assignedHall      [common.N_FLOORS][2]bool
	netRequests       common.Requests
	localRequests     common.Requests
	deliveredRequests common.Requests // prevents re-sending active requests to elevator each tick
}

func NewFsmSync(config common.Config) *Synchronizer {
	return &Synchronizer{selfKey: config.SelfKey}
}

func (sync *Synchronizer) HandleNetworkSnapshot(snapshot common.Snapshot, now time.Time) {
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
			sync.deliveredRequests[floor][button] = false
		}
	}
}

func (sync *Synchronizer) HandleAssignerTask(task common.ElevInput) (toRevoke common.Requests) {
	previousAssignment := sync.assignedHall
	sync.assignedHall = task.HallTask

	for floor := range previousAssignment {
		if previousAssignment[floor][0] && !sync.assignedHall[floor][0] {
			sync.localRequests[floor][common.BT_HallUp] = false
			sync.deliveredRequests[floor][common.BT_HallUp] = false
			toRevoke[floor][common.BT_HallUp] = true
		}
		if previousAssignment[floor][1] && !sync.assignedHall[floor][1] {
			sync.localRequests[floor][common.BT_HallDown] = false
			sync.deliveredRequests[floor][common.BT_HallDown] = false
			toRevoke[floor][common.BT_HallDown] = true
		}
	}
	return toRevoke
}

func (sync *Synchronizer) HandleButtonPresses(edgePresses common.Requests, currentFloor int, now time.Time) (newCabRequests common.Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if !edgePresses[floor][button] {
				continue
			}
			sync.localRequests[floor][button] = true
			if button == common.BT_Cab {
				newCabRequests[floor][button] = true
				sync.deliveredRequests[floor][button] = true
			}
		}
	}
	return newCabRequests
}

func (sync *Synchronizer) TransferReadyRequests() (toTransfer common.Requests) {
	for floor := range common.N_FLOORS {
		if sync.localRequests[floor][common.BT_Cab] && !sync.deliveredRequests[floor][common.BT_Cab] {
			toTransfer[floor][common.BT_Cab] = true
			sync.deliveredRequests[floor][common.BT_Cab] = true
		}

		for button := common.ButtonType(common.BT_HallUp); button <= common.BT_HallDown; button++ {
			if sync.deliveredRequests[floor][button] {
				continue
			}
			if sync.hasAlivePeer && !sync.coherent {
				continue
			}

			hallActive := sync.netRequests[floor][button] || sync.localRequests[floor][button]
			if sync.hasAlivePeer {
				hallActive = sync.netRequests[floor][button]
			}
			if !hallActive || !sync.assignedHall[floor][button] {
				continue
			}

			sync.localRequests[floor][button] = true
			sync.deliveredRequests[floor][button] = true
			toTransfer[floor][button] = true
		}
	}
	return toTransfer
}

func (sync *Synchronizer) ClearServicedRequests(floor int, serviced common.Requests) {
	if floor < 0 || floor >= common.N_FLOORS {
		return
	}
	for button := range common.ButtonType(common.N_BUTTONS) {
		if serviced[floor][button] {
			sync.localRequests[floor][button] = false
			sync.netRequests[floor][button] = false
			sync.deliveredRequests[floor][button] = false
		}
	}
}

func (sync *Synchronizer) BuildSnapshot(
	kind common.UpdateKind,
	requestsCleared common.Requests,
	floor int,
	behavior string,
	direction string,
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
	return common.Snapshot{
		HallRequests: common.GetHallRequests(outRequests),
		States: map[string]common.ElevState{
			sync.selfKey: {
				Behavior:    behavior,
				Floor:       floor,
				Direction:   direction,
				CabRequests: common.GetCabRequests(sync.localRequests),
			},
		},
		UpdateKind: kind,
	}
}

func (sync *Synchronizer) HasAlivePeer() bool { return sync.hasAlivePeer }

func (sync *Synchronizer) IsInitFromNetwork() bool { return sync.initFromNetwork }

func (sync *Synchronizer) GetNetRequests() [common.N_FLOORS][common.N_BUTTONS]bool {
	return sync.netRequests
}
