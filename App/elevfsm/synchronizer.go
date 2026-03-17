package elevfsm

import (
	"elevator/common"
	"time"
)

type RequestManager struct {
	selfKey string

	initFromNetwork bool
	hasAlivePeer    bool
	coherent        bool

	assignedHall      common.HallAssignment
	netRequests       common.Requests
	localRequests     common.Requests
	deliveredRequests common.Requests // prevents re-sending active requests to elevator each tick
}

func InitRequestManager(config common.Config) *RequestManager {
	return &RequestManager{selfKey: config.SelfKey}
}

func (sync *RequestManager) HandleNetworkSnapshot(snapshot common.Snapshot, now time.Time) {
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

func (sync *RequestManager) HandleAssignment(assignment common.HallAssignment) (revokedRequests common.Requests) {
	previousAssignment := sync.assignedHall
	sync.assignedHall = assignment

	for floor := range previousAssignment {
		if previousAssignment[floor][0] && !sync.assignedHall[floor][0] {
			sync.localRequests[floor][common.BT_HallUp] = false
			sync.deliveredRequests[floor][common.BT_HallUp] = false
			revokedRequests[floor][common.BT_HallUp] = true
		}
		if previousAssignment[floor][1] && !sync.assignedHall[floor][1] {
			sync.localRequests[floor][common.BT_HallDown] = false
			sync.deliveredRequests[floor][common.BT_HallDown] = false
			revokedRequests[floor][common.BT_HallDown] = true
		}
	}
	return revokedRequests
}

func (sync *RequestManager) HandleButtonPresses(edgePresses common.Requests, currentFloor int, now time.Time) (newCabRequests common.Requests, newHallRequests common.Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if !edgePresses[floor][button] {
				continue
			}
			sync.localRequests[floor][button] = true
			switch button {
			case common.BT_Cab:
				newCabRequests[floor][button] = true
				sync.deliveredRequests[floor][button] = true
			case common.BT_HallUp, common.BT_HallDown:
				newHallRequests[floor][button] = true
			}
		}
	}
	return newCabRequests, newHallRequests
}

func (sync *RequestManager) TransferReadyRequests() (toTransfer common.Requests) {
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

func (sync *RequestManager) ClearServicedRequests(floor int, serviced common.Requests) {
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

func (sync *RequestManager) HasAlivePeer() bool { return sync.hasAlivePeer }

func (sync *RequestManager) GetLocalRequests() common.Requests {
	return sync.localRequests
}

func (sync *RequestManager) GetNetRequests() common.Requests {
	return sync.netRequests
}
