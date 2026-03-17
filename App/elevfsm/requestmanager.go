package elevfsm

import (
	"elevator/common"
	"elevator/elevhw"
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
	deliveredRequests common.Requests
}

func InitRequestManager(config common.Config) *RequestManager {
	return &RequestManager{selfKey: config.SelfKey}
}

func (rm *RequestManager) HandleNetworkSnapshot(snapshot common.Snapshot, now time.Time) {
	rm.hasAlivePeer = false
	rm.coherent = snapshot.Coherent
	for key, alive := range snapshot.Alive {
		if key != rm.selfKey && alive {
			if _, hasState := snapshot.States[key]; hasState {
				rm.hasAlivePeer = true
				break
			}
		}
	}
	for floor := range elevhw.N_FLOORS {
		rm.netRequests[floor][0] = snapshot.HallRequests[floor][0]
		rm.netRequests[floor][1] = snapshot.HallRequests[floor][1]
		rm.netRequests[floor][elevhw.BT_Cab] = false
	}
	if state, ok := snapshot.States[rm.selfKey]; ok {
		rm.initFromNetwork = true
		for floor := 0; floor < elevhw.N_FLOORS && floor < len(state.CabRequests); floor++ {
			rm.netRequests[floor][elevhw.BT_Cab] = state.CabRequests[floor]
		}
	}

	for floor := range elevhw.N_FLOORS {
		for button := range elevhw.ButtonType(elevhw.N_BUTTONS) {
			if rm.netRequests[floor][button] {
				if button == elevhw.BT_Cab {
					rm.localRequests[floor][button] = true
				}
				continue
			}
			rm.localRequests[floor][button] = false
			rm.deliveredRequests[floor][button] = false
		}
	}
}

func (rm *RequestManager) HandleAssignment(assignment common.HallAssignment) (revokedRequests common.Requests) {
	previousAssignment := rm.assignedHall
	rm.assignedHall = assignment

	for floor := range previousAssignment {
		if previousAssignment[floor][0] && !rm.assignedHall[floor][0] {
			rm.netRequests[floor][elevhw.BT_HallUp] = false
			rm.localRequests[floor][elevhw.BT_HallUp] = false
			rm.deliveredRequests[floor][elevhw.BT_HallUp] = false
			revokedRequests[floor][elevhw.BT_HallUp] = true
		}
		if previousAssignment[floor][1] && !rm.assignedHall[floor][1] {
			rm.netRequests[floor][elevhw.BT_HallUp] = false
			rm.localRequests[floor][elevhw.BT_HallDown] = false
			rm.deliveredRequests[floor][elevhw.BT_HallDown] = false
			revokedRequests[floor][elevhw.BT_HallDown] = true
		}
	}
	return revokedRequests
}

func (rm *RequestManager) HandleButtonPresses(edgePresses common.Requests, currentFloor int, now time.Time) (newCabRequests common.Requests, newHallRequests common.Requests) {
	for floor := range elevhw.N_FLOORS {
		for button := range elevhw.ButtonType(elevhw.N_BUTTONS) {
			if !edgePresses[floor][button] {
				continue
			}
			rm.localRequests[floor][button] = true
			switch button {
			case elevhw.BT_Cab:
				newCabRequests[floor][button] = true
				rm.deliveredRequests[floor][button] = true
			case elevhw.BT_HallUp, elevhw.BT_HallDown:
				newHallRequests[floor][button] = true
			}
		}
	}
	return newCabRequests, newHallRequests
}

func (rm *RequestManager) TransferReadyRequests() (toTransfer common.Requests) {
	for floor := range elevhw.N_FLOORS {
		if rm.localRequests[floor][elevhw.BT_Cab] && !rm.deliveredRequests[floor][elevhw.BT_Cab] {
			toTransfer[floor][elevhw.BT_Cab] = true
			rm.deliveredRequests[floor][elevhw.BT_Cab] = true
		}

		for button := elevhw.ButtonType(elevhw.BT_HallUp); button <= elevhw.BT_HallDown; button++ {
			// only transfer request if it has not been delivered to elevator before
			if rm.deliveredRequests[floor][button] {
				continue
			}
			if rm.hasAlivePeer && !rm.coherent {
				continue
			}

			hallActive := rm.netRequests[floor][button] || rm.localRequests[floor][button]
			if rm.hasAlivePeer {
				hallActive = rm.netRequests[floor][button]
			}
			if !hallActive || !rm.assignedHall[floor][button] {
				continue
			}

			rm.localRequests[floor][button] = true
			rm.deliveredRequests[floor][button] = true
			toTransfer[floor][button] = true
		}
	}
	return toTransfer
}

func (rm *RequestManager) ClearServicedRequests(floor int, serviced common.Requests) {
	if floor < 0 || floor >= elevhw.N_FLOORS {
		return
	}
	for button := range elevhw.ButtonType(elevhw.N_BUTTONS) {
		if serviced[floor][button] {
			rm.localRequests[floor][button] = false
			rm.netRequests[floor][button] = false
			rm.deliveredRequests[floor][button] = false
		}
	}
}

func (rm *RequestManager) GetLocalRequests() common.Requests { return rm.localRequests }

func (rm *RequestManager) GetNetRequests() common.Requests { return rm.netRequests }

func (rm *RequestManager) HasAlivePeer() bool { return rm.hasAlivePeer }
