package elevfsm

import (
	"Driver-go/elevio"
	"elevator/common"
)

type DirnBehaviourPair struct {
	dirn      elevio.MotorDirection
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
	case elevio.MD_Up:
		if requests_above(e) != 0 {
			return DirnBehaviourPair{elevio.MD_Up, EB_Moving}
		} else if requests_here(e) != 0 {
			return DirnBehaviourPair{elevio.MD_Down, EB_DoorOpen}
		} else if requests_below(e) != 0 {
			return DirnBehaviourPair{elevio.MD_Down, EB_Moving}
		} else {
			return DirnBehaviourPair{elevio.MD_Stop, EB_Idle}
		}

	case elevio.MD_Down:
		if requests_below(e) != 0 {
			return DirnBehaviourPair{elevio.MD_Down, EB_Moving}
		} else if requests_here(e) != 0 {
			return DirnBehaviourPair{elevio.MD_Up, EB_DoorOpen}
		} else if requests_above(e) != 0 {
			return DirnBehaviourPair{elevio.MD_Up, EB_Moving}
		} else {
			return DirnBehaviourPair{elevio.MD_Stop, EB_Idle}
		}

	case elevio.MD_Stop:
		if requests_here(e) != 0 {
			return DirnBehaviourPair{elevio.MD_Stop, EB_DoorOpen}
		} else if requests_above(e) != 0 {
			return DirnBehaviourPair{elevio.MD_Up, EB_Moving}
		} else if requests_below(e) != 0 {
			return DirnBehaviourPair{elevio.MD_Down, EB_Moving}
		} else {
			return DirnBehaviourPair{elevio.MD_Stop, EB_Idle}
		}

	default:
		return DirnBehaviourPair{elevio.MD_Stop, EB_Idle}
	}
}

// int requests_shouldStop(Elevator e)
func requests_shouldStop(e Elevator) int {
	switch e.dirn {
	case elevio.MD_Down:
		if e.requests[e.floor][elevio.BT_HallDown] ||
			e.requests[e.floor][elevio.BT_Cab] ||
			requests_below(e) == 0 {
			return 1
		}
		return 0

	case elevio.MD_Up:
		if e.requests[e.floor][elevio.BT_HallUp] ||
			e.requests[e.floor][elevio.BT_Cab] ||
			requests_above(e) == 0 {
			return 1
		}
		return 0

	case elevio.MD_Stop:
		fallthrough
	default:
		return 1
	}
}

func requests_shouldClearImmediately(e Elevator, btn_floor int, btn_type elevio.ButtonType) int {
	switch e.config.clearRequestVariant {
	case CV_All:
		if e.floor == btn_floor {
			return 1
		}
		return 0

	case CV_InDirn:
		if e.floor == btn_floor &&
			((e.dirn == elevio.MD_Up && btn_type == elevio.BT_HallUp) ||
				(e.dirn == elevio.MD_Down && btn_type == elevio.BT_HallDown) ||
				e.dirn == elevio.MD_Stop ||
				btn_type == elevio.BT_Cab) {
			return 1
		}
		return 0

	default:
		return 0
	}
}

func requests_clearAtCurrentFloor(e Elevator) Elevator {
	switch e.config.clearRequestVariant {
	case CV_All:
		for btn := elevio.ButtonType(0); btn < common.N_BUTTONS; btn++ {
			e.requests[e.floor][btn] = false
		}

	case CV_InDirn:
		e.requests[e.floor][elevio.BT_Cab] = false
		switch e.dirn {
		case elevio.MD_Up:
			if requests_above(e) == 0 && !e.requests[e.floor][elevio.BT_HallUp] {
				e.requests[e.floor][elevio.BT_HallDown] = false
			}
			e.requests[e.floor][elevio.BT_HallUp] = false

		case elevio.MD_Down:
			if requests_below(e) == 0 && !e.requests[e.floor][elevio.BT_HallDown] {
				e.requests[e.floor][elevio.BT_HallUp] = false
			}
			e.requests[e.floor][elevio.BT_HallDown] = false

		case elevio.MD_Stop:
			fallthrough
		default:
			e.requests[e.floor][elevio.BT_HallUp] = false
			e.requests[e.floor][elevio.BT_HallDown] = false
		}

	default:
		// do nothing
	}

	return e
}
