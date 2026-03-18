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

func requests_chooseDirn(requests common.Requests, floor int, dirn elevhw.MotorDirection) DirnBehaviourPair {
	switch dirn {
	case elevhw.MD_Up:
		if requests_above(requests, floor) != 0 {
			return DirnBehaviourPair{elevhw.MD_Up, EB_Moving}
		} else if requests_at_floor(requests, floor) != 0 {
			return DirnBehaviourPair{elevhw.MD_Down, EB_DoorOpen}
		} else if requests_below(requests, floor) != 0 {
			return DirnBehaviourPair{elevhw.MD_Down, EB_Moving}
		} else {
			return DirnBehaviourPair{elevhw.MD_Stop, EB_Idle}
		}

	case elevhw.MD_Down:
		if requests_below(requests, floor) != 0 {
			return DirnBehaviourPair{elevhw.MD_Down, EB_Moving}
		} else if requests_at_floor(requests, floor) != 0 {
			return DirnBehaviourPair{elevhw.MD_Up, EB_DoorOpen}
		} else if requests_above(requests, floor) != 0 {
			return DirnBehaviourPair{elevhw.MD_Up, EB_Moving}
		} else {
			return DirnBehaviourPair{elevhw.MD_Stop, EB_Idle}
		}

	case elevhw.MD_Stop:
		if requests_at_floor(requests, floor) != 0 {
			return DirnBehaviourPair{elevhw.MD_Stop, EB_DoorOpen}
		} else if requests_above(requests, floor) != 0 {
			return DirnBehaviourPair{elevhw.MD_Up, EB_Moving}
		} else if requests_below(requests, floor) != 0 {
			return DirnBehaviourPair{elevhw.MD_Down, EB_Moving}
		} else {
			return DirnBehaviourPair{elevhw.MD_Stop, EB_Idle}
		}

	default:
		return DirnBehaviourPair{elevhw.MD_Stop, EB_Idle}
	}
}

func requests_shouldStop(requests common.Requests, floor int, dirn elevhw.MotorDirection) int {
	switch dirn {
	case elevhw.MD_Down:
		if requests[floor][elevhw.BT_HallDown] ||
			requests[floor][elevhw.BT_Cab] ||
			requests_below(requests, floor) == 0 {
			return 1
		}
		return 0

	case elevhw.MD_Up:
		if requests[floor][elevhw.BT_HallUp] ||
			requests[floor][elevhw.BT_Cab] ||
			requests_above(requests, floor) == 0 {
			return 1
		}
		return 0

	case elevhw.MD_Stop:
		fallthrough
	default:
		return 1
	}
}

func requests_shouldSwitchDirn(requests common.Requests, floor int, dirn elevhw.MotorDirection) bool {
	switch dirn {
	case elevhw.MD_Up:
		return requests_above(requests, floor) == 0
	case elevhw.MD_Down:
		return requests_below(requests, floor) == 0
	default:
		return true
	}
}

func requests_chooseNextAnnounceDirn(
	requests common.Requests,
	floor int,
	announceDir elevhw.MotorDirection,
	dirn elevhw.MotorDirection,
) (nextAnnounceDir elevhw.MotorDirection, serviceDir elevhw.MotorDirection) {
	requestsAboveFloor, requestsBelowFloor := requests_hallRequestsAtFloor(requests, floor)
	nextAnnounceDir = announceDir
	serviceDir = elevhw.MD_Stop

	if announceDir == elevhw.MD_Up && requestsAboveFloor {
		serviceDir = elevhw.MD_Up
		if requestsBelowFloor && requests_shouldSwitchDirn(requests, floor, dirn) {
			nextAnnounceDir = elevhw.MD_Down
		}
		return nextAnnounceDir, serviceDir
	}

	if announceDir == elevhw.MD_Down && requestsBelowFloor {
		serviceDir = elevhw.MD_Down
		if requestsAboveFloor && requests_shouldSwitchDirn(requests, floor, dirn) {
			nextAnnounceDir = elevhw.MD_Up
		}
		return nextAnnounceDir, serviceDir
	}

	if requestsAboveFloor || requestsBelowFloor {
		serviceDir = requests_chooseNewDirnAtFloor(requests, floor, dirn)
		nextAnnounceDir = serviceDir

		if serviceDir == elevhw.MD_Up && requestsBelowFloor && requests_shouldSwitchDirn(requests, floor, dirn) {
			nextAnnounceDir = elevhw.MD_Down
		}
		if serviceDir == elevhw.MD_Down && requestsAboveFloor && requests_shouldSwitchDirn(requests, floor, dirn) {
			nextAnnounceDir = elevhw.MD_Up
		}
		return nextAnnounceDir, serviceDir
	}

	nextAnnounceDir = elevhw.MD_Stop
	serviceDir = elevhw.MD_Stop
	return nextAnnounceDir, serviceDir
}

func requests_chooseNewDirnAtFloor(requests common.Requests, floor int, fallback elevhw.MotorDirection) elevhw.MotorDirection {
	up, down := requests_hallRequestsAtFloor(requests, floor)
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

func requests_clearAtFloorDir(requests common.Requests, floor int, announceDir elevhw.MotorDirection) (updated common.Requests, cleared common.Requests) {
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

func requests_above(requests common.Requests, floor int) int {
	for f := floor + 1; f < elevhw.N_FLOORS; f++ {
		for button := range elevhw.N_BUTTONS {
			if requests[f][button] {
				return 1
			}
		}
	}
	return 0
}

func requests_below(requests common.Requests, floor int) int {
	for f := range floor {
		for button := range elevhw.N_BUTTONS {
			if requests[f][button] {
				return 1
			}
		}
	}
	return 0
}

func requests_at_floor(requests common.Requests, floor int) int {
	for button := range elevhw.N_BUTTONS {
		if requests[floor][button] {
			return 1
		}
	}
	return 0
}

func request_at(requests common.Requests, floor int, button elevhw.ButtonType) bool {
	if floor < 0 || floor >= elevhw.N_FLOORS {
		return false
	}
	if button < 0 || button >= elevhw.N_BUTTONS {
		return false
	}
	return requests[floor][button]
}

func requests_hallRequestsAtFloor(requests common.Requests, floor int) (up bool, down bool) {
	if floor < 0 || floor >= elevhw.N_FLOORS {
		return false, false
	}
	return request_at(requests, floor, elevhw.BT_HallUp), request_at(requests, floor, elevhw.BT_HallDown)
}
