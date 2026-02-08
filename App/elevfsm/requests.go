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
	for f := e.Floor + 1; f < common.N_FLOORS; f++ {
		for btn := range common.N_BUTTONS {
			if e.Requests[f][btn] {
				return 1
			}
		}
	}
	return 0
}

func requests_below(e Elevator) int {
	for f := range e.Floor {
		for btn := range common.N_BUTTONS {
			if e.Requests[f][btn] {
				return 1
			}
		}
	}
	return 0
}

func requests_here(e Elevator) int {
	for btn := range common.N_BUTTONS {
		if e.Requests[e.Floor][btn] {
			return 1
		}
	}
	return 0
}

func requests_chooseDirection(e Elevator) DirnBehaviourPair {
	switch e.Dirn {
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
	switch e.Dirn {
	case elevio.MD_Down:
		if e.Requests[e.Floor][elevio.BT_HallDown] ||
			e.Requests[e.Floor][elevio.BT_Cab] ||
			requests_below(e) == 0 {
			return 1
		}
		return 0

	case elevio.MD_Up:
		if e.Requests[e.Floor][elevio.BT_HallUp] ||
			e.Requests[e.Floor][elevio.BT_Cab] ||
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
	switch e.Config.ClearRequestVariant {
	case CV_All:
		if e.Floor == btn_floor {
			return 1
		}
		return 0

	case CV_InDirn:
		if e.Floor == btn_floor &&
			((e.Dirn == elevio.MD_Up && btn_type == elevio.BT_HallUp) ||
				(e.Dirn == elevio.MD_Down && btn_type == elevio.BT_HallDown) ||
				e.Dirn == elevio.MD_Stop ||
				btn_type == elevio.BT_Cab) {
			return 1
		}
		return 0

	default:
		return 0
	}
}

func requests_clearAtCurrentFloor(e Elevator, online bool) (Elevator, bool, [2]elevio.ButtonType) {
	request_serviced := false
	var servicedDirections [2]elevio.ButtonType

	switch e.Config.ClearRequestVariant {
	case CV_All:
		for btn := elevio.ButtonType(0); btn < common.N_BUTTONS; btn++ {
			if !online {
				e.Requests[e.Floor][btn] = false
			}
			request_serviced = true
		}

	case CV_InDirn:
		if e.Requests[e.Floor][elevio.BT_Cab] == true {
			request_serviced = true
		}
		e.Requests[e.Floor][elevio.BT_Cab] = false

		switch e.Dirn {
		case elevio.MD_Up:
			if requests_above(e) == 0 && !e.Requests[e.Floor][elevio.BT_HallUp] {
				if !online {
					e.Requests[e.Floor][elevio.BT_HallDown] = false
				}
				servicedDirections[0] = elevio.BT_HallDown
			}
			if !online {
				e.Requests[e.Floor][elevio.BT_HallUp] = false
			}
			servicedDirections[1] = elevio.BT_HallUp
			request_serviced = true

		case elevio.MD_Down:
			if requests_below(e) == 0 && !e.Requests[e.Floor][elevio.BT_HallDown] {
				if !online {
					e.Requests[e.Floor][elevio.BT_HallUp] = false
					
				}
				servicedDirections[0] = elevio.BT_HallUp
			}
			if !online {
				e.Requests[e.Floor][elevio.BT_HallDown] = false
			}
			servicedDirections[1] = elevio.BT_HallDown
			request_serviced = true

		case elevio.MD_Stop:
			fallthrough
		default:
			if !online {
				e.Requests[e.Floor][elevio.BT_HallUp] = false
				e.Requests[e.Floor][elevio.BT_HallDown] = false
			}
		}

	default:
		// do nothing
	}

	return e, request_serviced,servicedDirections
}
