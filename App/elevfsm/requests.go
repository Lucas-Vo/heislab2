package elevfsm

import (
	"elevator/common"
)

type DirnBehaviourPair struct {
	dirn      common.MotorDirection
	behaviour ElevatorBehaviour
}

func requests_above(e Elevator) int {
	for f := e.floor + 1; f < common.N_FLOORS; f++ {
		for btn := range common.N_BUTTONS {
			if e.requests[f][btn] {
				return 1
			}
		}
	}
	return 0
}

func requests_below(e Elevator) int {
	for f := range e.floor {
		for btn := range common.N_BUTTONS {
			if e.requests[f][btn] {
				return 1
			}
		}
	}
	return 0
}

func requests_here(e Elevator) int {
	for btn := range common.N_BUTTONS {
		if e.requests[e.floor][btn] {
			return 1
		}
	}
	return 0
}

func requests_chooseDirection(e Elevator) DirnBehaviourPair {
	switch e.dirn {
	case common.MD_Up:
		if requests_above(e) != 0 {
			return DirnBehaviourPair{common.MD_Up, EB_Moving}
		} else if requests_here(e) != 0 {
			return DirnBehaviourPair{common.MD_Down, EB_DoorOpen}
		} else if requests_below(e) != 0 {
			return DirnBehaviourPair{common.MD_Down, EB_Moving}
		} else {
			return DirnBehaviourPair{common.MD_Stop, EB_Idle}
		}

	case common.MD_Down:
		if requests_below(e) != 0 {
			return DirnBehaviourPair{common.MD_Down, EB_Moving}
		} else if requests_here(e) != 0 {
			return DirnBehaviourPair{common.MD_Up, EB_DoorOpen}
		} else if requests_above(e) != 0 {
			return DirnBehaviourPair{common.MD_Up, EB_Moving}
		} else {
			return DirnBehaviourPair{common.MD_Stop, EB_Idle}
		}

	case common.MD_Stop:
		if requests_here(e) != 0 {
			return DirnBehaviourPair{common.MD_Stop, EB_DoorOpen}
		} else if requests_above(e) != 0 {
			return DirnBehaviourPair{common.MD_Up, EB_Moving}
		} else if requests_below(e) != 0 {
			return DirnBehaviourPair{common.MD_Down, EB_Moving}
		} else {
			return DirnBehaviourPair{common.MD_Stop, EB_Idle}
		}

	default:
		return DirnBehaviourPair{common.MD_Stop, EB_Idle}
	}
}

// int requests_shouldStop(Elevator e)
func requests_shouldStop(e Elevator) int {
	switch e.dirn {
	case common.MD_Down:
		if e.requests[e.floor][common.BT_HallDown] ||
			e.requests[e.floor][common.BT_Cab] ||
			requests_below(e) == 0 {
			return 1
		}
		return 0

	case common.MD_Up:
		if e.requests[e.floor][common.BT_HallUp] ||
			e.requests[e.floor][common.BT_Cab] ||
			requests_above(e) == 0 {
			return 1
		}
		return 0

	case common.MD_Stop:
		fallthrough
	default:
		return 1
	}
}

func requests_clearAtCurrentFloorDir(e Elevator, announceDir common.MotorDirection, clearCab bool) (_ Elevator, cleared Requests) {
	f := e.floor
	if clearCab {
		if e.requests[f][common.BT_Cab] {
			cleared[f][common.BT_Cab] = true
		}
		e.requests[f][common.BT_Cab] = false
	}

	switch announceDir {
	case common.MD_Up:
		if e.requests[f][common.BT_HallUp] {
			e.requests[f][common.BT_HallUp] = false
			cleared[f][common.BT_HallUp] = true
		}
	case common.MD_Down:
		if e.requests[f][common.BT_HallDown] {
			e.requests[f][common.BT_HallDown] = false
			cleared[f][common.BT_HallDown] = true
		}
	case common.MD_Stop:
		fallthrough
	default:
		// no hall clearing when direction isn't announced
	}
	return e, cleared
}
