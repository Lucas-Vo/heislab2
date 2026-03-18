package elevfsm

import (
	"elevator/common"
	"elevator/elevhw"
	"time"
)

type RequestManager struct {
	selfKey string

	initFromNetwork bool
	distributedMode bool
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
	rm.distributedMode = false
	rm.coherent = snapshot.Coherent
	for key, alive := range snapshot.Alive {
		if key != rm.selfKey && alive {
			if _, hasState := snapshot.States[key]; hasState {
				rm.distributedMode = true
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

func (rm *RequestManager) HandleAssignment(assignment common.HallAssignment) (hallAssignment common.HallAssignment) {
	previousAssignment := rm.assignedHall
	rm.assignedHall = assignment

	for floor := range previousAssignment {
		if previousAssignment[floor][0] && !rm.assignedHall[floor][0] {
			rm.localRequests[floor][elevhw.BT_HallUp] = false
			rm.deliveredRequests[floor][elevhw.BT_HallUp] = false
			hallAssignment[floor][elevhw.BT_HallUp] = true
		}
		if previousAssignment[floor][1] && !rm.assignedHall[floor][1] {
			rm.localRequests[floor][elevhw.BT_HallDown] = false
			rm.deliveredRequests[floor][elevhw.BT_HallDown] = false
			hallAssignment[floor][elevhw.BT_HallDown] = true
		}
	}
	return hallAssignment
}

func (rm *RequestManager) HandleNewRequests(edgePresses common.Requests, currentFloor int, now time.Time) (newCabRequests common.Requests, newHallRequests common.Requests) {
	for floor := range elevhw.N_FLOORS {
		for button := range elevhw.ButtonType(elevhw.N_BUTTONS) {
			if !edgePresses[floor][button] {
				continue
			}
			wasInactive := !rm.localRequests[floor][button] && !rm.netRequests[floor][button]
			rm.localRequests[floor][button] = true
			switch button {
			case elevhw.BT_Cab:
				newCabRequests[floor][button] = true
				rm.deliveredRequests[floor][button] = true
			case elevhw.BT_HallUp, elevhw.BT_HallDown:
				newHallRequests[floor][button] = true
				if wasInactive {
					rm.deliveredRequests[floor][button] = false
				}
			}
		}
	}
	return newCabRequests, newHallRequests
}

func (rm *RequestManager) GetReadyRequests() (readyRequests common.Requests) {
	for floor := range elevhw.N_FLOORS {
		if rm.localRequests[floor][elevhw.BT_Cab] && !rm.deliveredRequests[floor][elevhw.BT_Cab] {
			readyRequests[floor][elevhw.BT_Cab] = true
			rm.deliveredRequests[floor][elevhw.BT_Cab] = true
		}

		for button := 0; button < 2; button++ {
			// only transfer request if it has not been delivered to elevator before
			if rm.deliveredRequests[floor][button] {
				continue
			}
			// must be in local mode or coherent
			if rm.distributedMode && !rm.coherent {
				continue
			}

			hallActive := rm.netRequests[floor][button] || (rm.localRequests[floor][button] && !rm.distributedMode)

			// hall must be active and assigned
			if !hallActive || !rm.assignedHall[floor][button] {
				continue
			}

			rm.localRequests[floor][button] = true
			rm.deliveredRequests[floor][button] = true
			readyRequests[floor][button] = true
		}
	}
	return readyRequests
}

func (rm *RequestManager) ClearServicedRequests(floor int, serviced common.Requests) {
	if floor < 0 || floor >= elevhw.N_FLOORS {
		return
	}
	for button := range elevhw.ButtonType(elevhw.N_BUTTONS) {
		if serviced[floor][button] {
			rm.localRequests[floor][button] = false
			rm.netRequests[floor][button] = false
			rm.deliveredRequests[floor][button] = true
		}
	}
}

func (rm *RequestManager) GetLocalRequests() common.Requests { return rm.localRequests }

func (rm *RequestManager) GetNetRequests() common.Requests { return rm.netRequests }

func (rm *RequestManager) InDistributedMode() bool { return rm.distributedMode }
