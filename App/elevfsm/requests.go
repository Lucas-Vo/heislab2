package elevfsm

import (
	"elevator/common"
)

type ElevatorBehaviour int

const (
	EB_Idle ElevatorBehaviour = iota
	EB_DoorOpen
	EB_Moving
)

type DirnBehaviourPair struct {
	dirn      common.MotorDirection
	behaviour ElevatorBehaviour
}

func requests_chooseDirection(requests common.Requests, floor int, dirn common.MotorDirection) DirnBehaviourPair {
	switch dirn {
	case common.MD_Up:
		if requests_above(requests, floor) != 0 {
			return DirnBehaviourPair{common.MD_Up, EB_Moving}
		} else if requests_at_floor(requests, floor) != 0 {
			return DirnBehaviourPair{common.MD_Down, EB_DoorOpen}
		} else if requests_below(requests, floor) != 0 {
			return DirnBehaviourPair{common.MD_Down, EB_Moving}
		} else {
			return DirnBehaviourPair{common.MD_Stop, EB_Idle}
		}

	case common.MD_Down:
		if requests_below(requests, floor) != 0 {
			return DirnBehaviourPair{common.MD_Down, EB_Moving}
		} else if requests_at_floor(requests, floor) != 0 {
			return DirnBehaviourPair{common.MD_Up, EB_DoorOpen}
		} else if requests_above(requests, floor) != 0 {
			return DirnBehaviourPair{common.MD_Up, EB_Moving}
		} else {
			return DirnBehaviourPair{common.MD_Stop, EB_Idle}
		}

	case common.MD_Stop:
		if requests_at_floor(requests, floor) != 0 {
			return DirnBehaviourPair{common.MD_Stop, EB_DoorOpen}
		} else if requests_above(requests, floor) != 0 {
			return DirnBehaviourPair{common.MD_Up, EB_Moving}
		} else if requests_below(requests, floor) != 0 {
			return DirnBehaviourPair{common.MD_Down, EB_Moving}
		} else {
			return DirnBehaviourPair{common.MD_Stop, EB_Idle}
		}

	default:
		return DirnBehaviourPair{common.MD_Stop, EB_Idle}
	}
}

func requests_shouldStop(requests common.Requests, floor int, dirn common.MotorDirection) int {
	switch dirn {
	case common.MD_Down:
		if requests[floor][common.BT_HallDown] ||
			requests[floor][common.BT_Cab] ||
			requests_below(requests, floor) == 0 {
			return 1
		}
		return 0

	case common.MD_Up:
		if requests[floor][common.BT_HallUp] ||
			requests[floor][common.BT_Cab] ||
			requests_above(requests, floor) == 0 {
			return 1
		}
		return 0

	case common.MD_Stop:
		fallthrough
	default:
		return 1
	}
}

func requests_clearAtFloorDir(requests common.Requests, floor int, announceDir common.MotorDirection, clearCab bool) (updated common.Requests, cleared common.Requests) {
	updated = requests
	if clearCab {
		if updated[floor][common.BT_Cab] {
			cleared[floor][common.BT_Cab] = true
		}
		updated[floor][common.BT_Cab] = false
	}

	switch announceDir {
	case common.MD_Up:
		if updated[floor][common.BT_HallUp] {
			updated[floor][common.BT_HallUp] = false
			cleared[floor][common.BT_HallUp] = true
		}
	case common.MD_Down:
		if updated[floor][common.BT_HallDown] {
			updated[floor][common.BT_HallDown] = false
			cleared[floor][common.BT_HallDown] = true
		}
	case common.MD_Stop:
		fallthrough
	default:
		// no hall clearing when direction isn't announced
	}
	return updated, cleared
}

func requests_clear(requests common.Requests, floor int, btn common.ButtonType) common.Requests {
	if floor < 0 || floor >= common.N_FLOORS {
		return requests
	}
	if btn < 0 || btn >= common.N_BUTTONS {
		return requests
	}
	requests[floor][btn] = false
	return requests
}

func requests_above(requests common.Requests, floor int) int {
	for f := floor + 1; f < common.N_FLOORS; f++ {
		for btn := range common.N_BUTTONS {
			if requests[f][btn] {
				return 1
			}
		}
	}
	return 0
}

func requests_below(requests common.Requests, floor int) int {
	for f := range floor {
		for btn := range common.N_BUTTONS {
			if requests[f][btn] {
				return 1
			}
		}
	}
	return 0
}

func requests_at_floor(requests common.Requests, floor int) int {
	for btn := range common.N_BUTTONS {
		if requests[floor][btn] {
			return 1
		}
	}
	return 0
}

func request_at(requests common.Requests, floor int, btn common.ButtonType) bool {
	if floor < 0 || floor >= common.N_FLOORS {
		return false
	}
	if btn < 0 || btn >= common.N_BUTTONS {
		return false
	}
	return requests[floor][btn]
}

func requests_hallRequestsAtFloor(requests common.Requests, floor int) (up bool, down bool) {
	if floor < 0 || floor >= common.N_FLOORS {
		return false, false
	}
	return request_at(requests, floor, common.BT_HallUp), request_at(requests, floor, common.BT_HallDown)
}
