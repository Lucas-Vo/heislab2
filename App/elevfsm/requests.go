// Based off of https://github.com/TTK4145/Project-resources/tree/master/elev_algo

package elevfsm

import (
	"elevator/common"
	"elevator/elevhw"
)

type ElevatorBehaviour int

const (
	EB_Idle ElevatorBehaviour = iota
	EB_DoorOpen
	EB_Moving
)

type DirnBehaviourPair struct {
	dirn      elevhw.MotorDirection
	behaviour ElevatorBehaviour
}

func requestsChooseDirn(requests common.Requests, floor int, dirn elevhw.MotorDirection) DirnBehaviourPair {
	switch dirn {
	case elevhw.MD_Up:
		if requestsAbove(requests, floor) {
			return DirnBehaviourPair{elevhw.MD_Up, EB_Moving}
		} else if requestsAtFloor(requests, floor) {
			return DirnBehaviourPair{elevhw.MD_Down, EB_DoorOpen}
		} else if requestsBelow(requests, floor) {
			return DirnBehaviourPair{elevhw.MD_Down, EB_Moving}
		} else {
			return DirnBehaviourPair{elevhw.MD_Stop, EB_Idle}
		}

	case elevhw.MD_Down:
		if requestsBelow(requests, floor) {
			return DirnBehaviourPair{elevhw.MD_Down, EB_Moving}
		} else if requestsAtFloor(requests, floor) {
			return DirnBehaviourPair{elevhw.MD_Up, EB_DoorOpen}
		} else if requestsAbove(requests, floor) {
			return DirnBehaviourPair{elevhw.MD_Up, EB_Moving}
		} else {
			return DirnBehaviourPair{elevhw.MD_Stop, EB_Idle}
		}

	case elevhw.MD_Stop:
		if requestsAtFloor(requests, floor) {
			return DirnBehaviourPair{elevhw.MD_Stop, EB_DoorOpen}
		} else if requestsAbove(requests, floor) {
			return DirnBehaviourPair{elevhw.MD_Up, EB_Moving}
		} else if requestsBelow(requests, floor) {
			return DirnBehaviourPair{elevhw.MD_Down, EB_Moving}
		} else {
			return DirnBehaviourPair{elevhw.MD_Stop, EB_Idle}
		}

	default:
		return DirnBehaviourPair{elevhw.MD_Stop, EB_Idle}
	}
}

func requestsShouldStop(requests common.Requests, floor int, dirn elevhw.MotorDirection) bool {
	switch dirn {
	case elevhw.MD_Down:
		if requests[floor][elevhw.BT_HallDown] ||
			requests[floor][elevhw.BT_Cab] ||
			!requestsBelow(requests, floor) {
			return true
		}
		return false

	case elevhw.MD_Up:
		if requests[floor][elevhw.BT_HallUp] ||
			requests[floor][elevhw.BT_Cab] ||
			!requestsAbove(requests, floor) {
			return true
		}
		return false

	case elevhw.MD_Stop:
		fallthrough
	default:
		return true
	}
}

func requestsNextAnnounceDirn(
	requests common.Requests,
	floor int,
	announceDir elevhw.MotorDirection,
	dirn elevhw.MotorDirection,
) (nextAnnounceDir elevhw.MotorDirection, serviceDir elevhw.MotorDirection) {
	requestsAboveFloor, requestsBelowFloor := requestsHallAtFloor(requests, floor)
	nextAnnounceDir = announceDir
	serviceDir = elevhw.MD_Stop

	if announceDir == elevhw.MD_Up && requestsAboveFloor {
		serviceDir = elevhw.MD_Up
		if requestsBelowFloor && requestsShouldSwitchDirn(requests, floor, dirn) {
			nextAnnounceDir = elevhw.MD_Down
		}
		return nextAnnounceDir, serviceDir
	}

	if announceDir == elevhw.MD_Down && requestsBelowFloor {
		serviceDir = elevhw.MD_Down
		if requestsAboveFloor && requestsShouldSwitchDirn(requests, floor, dirn) {
			nextAnnounceDir = elevhw.MD_Up
		}
		return nextAnnounceDir, serviceDir
	}

	if requestsAboveFloor || requestsBelowFloor {
		serviceDir = requestsNewDirnAtFloor(requests, floor, dirn)
		nextAnnounceDir = serviceDir

		if serviceDir == elevhw.MD_Up && requestsBelowFloor && requestsShouldSwitchDirn(requests, floor, dirn) {
			nextAnnounceDir = elevhw.MD_Down
		}
		if serviceDir == elevhw.MD_Down && requestsAboveFloor && requestsShouldSwitchDirn(requests, floor, dirn) {
			nextAnnounceDir = elevhw.MD_Up
		}
		return nextAnnounceDir, serviceDir
	}

	nextAnnounceDir = elevhw.MD_Stop
	serviceDir = elevhw.MD_Stop
	return nextAnnounceDir, serviceDir
}

func requestsNewDirnAtFloor(requests common.Requests, floor int, fallback elevhw.MotorDirection) elevhw.MotorDirection {
	up, down := requestsHallAtFloor(requests, floor)
	if up && !down {
		return elevhw.MD_Up
	}
	if down && !up {
		return elevhw.MD_Down
	}
	if up && down {
		if fallback == elevhw.MD_Up || fallback == elevhw.MD_Down {
			return fallback
		}
		return elevhw.MD_Up
	}
	return fallback
}

func requestsClearAtFloorDir(requests common.Requests, floor int, announceDir elevhw.MotorDirection) (updated common.Requests, cleared common.Requests) {
	updated = requests

	if updated[floor][elevhw.BT_Cab] {
		cleared[floor][elevhw.BT_Cab] = true
	}
	updated[floor][elevhw.BT_Cab] = false

	switch announceDir {
	case elevhw.MD_Up:
		if updated[floor][elevhw.BT_HallUp] {
			updated[floor][elevhw.BT_HallUp] = false
			cleared[floor][elevhw.BT_HallUp] = true
		}
	case elevhw.MD_Down:
		if updated[floor][elevhw.BT_HallDown] {
			updated[floor][elevhw.BT_HallDown] = false
			cleared[floor][elevhw.BT_HallDown] = true
		}
	case elevhw.MD_Stop:
		fallthrough
	default:
		// no hall clearing when direction isn't announced
	}
	return updated, cleared
}

func requestsAtFloor(requests common.Requests, floor int) bool {
	for button := range elevhw.N_BUTTONS {
		if requests[floor][button] {
			return true
		}
	}
	return false
}

func requestsShouldSwitchDirn(requests common.Requests, floor int, dirn elevhw.MotorDirection) bool {
	switch dirn {
	case elevhw.MD_Up:
		return !requestsAbove(requests, floor)
	case elevhw.MD_Down:
		return !requestsBelow(requests, floor)
	default:
		return true
	}
}

func requestsHallAtFloor(requests common.Requests, floor int) (up bool, down bool) {
	if floor < 0 || floor >= elevhw.N_FLOORS {
		return false, false
	}
	return requestAt(requests, floor, elevhw.BT_HallUp), requestAt(requests, floor, elevhw.BT_HallDown)
}

func requestsAbove(requests common.Requests, floor int) bool {
	for f := floor + 1; f < elevhw.N_FLOORS; f++ {
		for button := range elevhw.N_BUTTONS {
			if requests[f][button] {
				return true
			}
		}
	}
	return false
}

func requestsBelow(requests common.Requests, floor int) bool {
	for f := range floor {
		for button := range elevhw.N_BUTTONS {
			if requests[f][button] {
				return true
			}
		}
	}
	return false
}

func requestAt(requests common.Requests, floor int, button elevhw.ButtonType) bool {
	if floor < 0 || floor >= elevhw.N_FLOORS {
		return false
	}
	if button < 0 || button >= elevhw.N_BUTTONS {
		return false
	}
	return requests[floor][button]
}
