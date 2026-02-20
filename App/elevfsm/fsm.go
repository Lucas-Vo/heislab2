package elevfsm

import (
	"elevator/common"
)

var outputDevice common.ElevOutputDevice

func Fsm_init() (elevator *Elevator) {
	e := new(Elevator)
	*e = elevator_uninitialized()

	outputDevice = common.ElevioGetOutputDevice()
	outputDevice.DoorLight(false)

	return e
}

func Fsm_onInitBetweenFloors(e *Elevator) {
	outputDevice.MotorDirection(common.MD_Down)
	e.dirn = common.MD_Down
	e.behaviour = EB_Moving
}

func Fsm_onRequestButtonPress(e *Elevator, btn_floor int, btn_type common.ButtonType) {

	switch e.behaviour {
	case EB_DoorOpen:
		if requests_shouldClearImmediately(*e, btn_floor, btn_type) != 0 {
			// timer is handled by the fsm thread; no-op here
		} else {
			e.requests[btn_floor][btn_type] = true
		}

	case EB_Moving:

		e.requests[btn_floor][btn_type] = true

	case EB_Idle:
		e.requests[btn_floor][btn_type] = true
		pair := requests_chooseDirection(*e)
		e.dirn = pair.dirn
		e.behaviour = pair.behaviour

		switch pair.behaviour {
		case EB_DoorOpen:
			outputDevice.DoorLight(true)
			*e = requests_clearAtCurrentFloor(*e) //TODO: Bro we have same function in fsmsync. Make it one, make it snappy.

		case EB_Moving:
			outputDevice.MotorDirection(e.dirn)

		case EB_Idle:
			// do nothing
		}
	}
}

func Fsm_onFloorArrival(e *Elevator, newFloor int) {

	e.floor = newFloor
	outputDevice.FloorIndicator(e.floor)

	switch e.behaviour {
	case EB_Moving:
		if requests_shouldStop(*e) != 0 {
			outputDevice.MotorDirection(common.MD_Stop)
			outputDevice.DoorLight(true)
			*e = requests_clearAtCurrentFloor(*e)
			// timer is handled by the fsm thread; no-op here
			//SetAllLights(*e)
			e.behaviour = EB_DoorOpen
		}
	default:
		// do nothing
	}

}

func Fsm_onDoorTimeout(e *Elevator) {

	switch e.behaviour {
	case EB_DoorOpen:
		pair := requests_chooseDirection(*e)
		e.dirn = pair.dirn
		e.behaviour = pair.behaviour

		switch e.behaviour {
		case EB_DoorOpen:
			// timer is handled by the fsm thread; no-op here
			*e = requests_clearAtCurrentFloor(*e)
			//SetAllLights(*e)

		case EB_Moving, EB_Idle:
			outputDevice.DoorLight(false)
			outputDevice.MotorDirection(e.dirn)
		}
	default:
		// do nothing
	}
}

func CurrentBehaviour(e *Elevator) ElevatorBehaviour {
	return e.behaviour
}

func CurrentDirection(e *Elevator) common.MotorDirection {
	return e.dirn
}

func CurrentMotionStrings(e *Elevator) (behavior string, direction string) {
	switch e.behaviour {
	case EB_Idle:
		behavior = "idle"
	case EB_DoorOpen:
		behavior = "doorOpen"
	case EB_Moving:
		behavior = "moving"
	default:
		behavior = "idle"
	}

	switch e.dirn {
	case common.MD_Up:
		direction = "up"
	case common.MD_Down:
		direction = "down"
	case common.MD_Stop:
		direction = "stop"
	default:
		direction = "stop"
	}

	return behavior, direction
}
