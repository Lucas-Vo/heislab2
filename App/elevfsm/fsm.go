package elevfsm

import (
	"elevator/common"
	"log"
)

var outputDevice common.ElevOutputDevice

func Fsm_init() (elevator *Elevator) {
	e := new(Elevator)
	*e = elevator_uninitialized()

	ConLoad("elevator.con",
		ConVal("doorOpenDuration_s", &e.config.doorOpenDuration_s, "%f"),
		ConEnum("clearRequestVariant", &e.config.clearRequestVariant,
			ConMatch("CV_All", CV_All),
			ConMatch("CV_InDirn", CV_InDirn),
		),
	)

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
	log.Printf("FSM: request press floor=%d btn=%s (before floor=%d dir=%s behav=%s)",
		btn_floor,
		common.ElevioButtonToString(btn_type),
		e.floor,
		common.ElevioDirnToString(e.dirn),
		ebToString(e.behaviour),
	)
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
			*e = requests_clearAtCurrentFloor(*e)

		case EB_Moving:
			outputDevice.MotorDirection(e.dirn)

		case EB_Idle:
			// do nothing
		}
	}
}

func Fsm_onFloorArrival(e *Elevator, newFloor int) {
	log.Printf("FSM: floor arrival %d (before floor=%d dir=%s behav=%s)",
		newFloor,
		e.floor,
		common.ElevioDirnToString(e.dirn),
		ebToString(e.behaviour),
	)

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
	log.Printf("FSM: floor arrival handled (after floor=%d dir=%s behav=%s)",
		e.floor,
		common.ElevioDirnToString(e.dirn),
		ebToString(e.behaviour),
	)
}

func Fsm_onDoorTimeout(e *Elevator) {
	log.Printf("FSM: door timeout (before floor=%d dir=%s behav=%s)",
		e.floor,
		common.ElevioDirnToString(e.dirn),
		ebToString(e.behaviour),
	)

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
	log.Printf("FSM: door timeout handled (after floor=%d dir=%s behav=%s)",
		e.floor,
		common.ElevioDirnToString(e.dirn),
		ebToString(e.behaviour),
	)
}

func CurrentBehaviour(e *Elevator) ElevatorBehaviour {
	return e.behaviour
}

func CurrentDirection(e *Elevator) common.MotorDirection {
	return e.dirn
}

func DoorOpenDuration(e *Elevator) float64 {
	return e.config.doorOpenDuration_s
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
