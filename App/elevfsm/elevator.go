package elevfsm

//TODO: elevator.go fsm.go requests.go and timer.go is AALLLL soup. make that shit dissapear as most is implemented in fsmsync
import (
	"elevator/common"
)

// enums
type ElevatorBehaviour int

const (
	EB_Idle ElevatorBehaviour = iota
	EB_DoorOpen
	EB_Moving
)

// structs
type Elevator struct {
	floor        int
	dirn         common.MotorDirection
	behaviour    ElevatorBehaviour
	requests     [common.N_FLOORS][common.N_BUTTONS]bool
	outputDevice common.ElevOutputDevice
}

func ElevatorInit() *Elevator {
	e := new(Elevator)
	e.floor = -1
	e.dirn = common.MD_Stop
	e.behaviour = EB_Idle

	e.outputDevice = common.ElevioGetOutputDevice()
	e.outputDevice.DoorLight(false)

	return e
}

func (e *Elevator) OnInitBetweenFloors() {
	e.outputDevice.MotorDirection(common.MD_Down)
	e.dirn = common.MD_Down
	e.behaviour = EB_Moving
}

func (e *Elevator) OnRequestButtonPress(btn_floor int, btn_type common.ButtonType) {

	switch e.behaviour {

	case EB_DoorOpen:
		if requests_shouldClearImmediately(*e, btn_floor, btn_type) != 0 {
			// timer handled by FSM thread
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
			e.outputDevice.DoorLight(true)
			*e, _ = requests_clearAtCurrentFloor(*e)

		case EB_Moving:
			e.outputDevice.MotorDirection(e.dirn)

		case EB_Idle:
			// no-op
		}
	}
}

func (e *Elevator) OnFloorArrival(newFloor int) {
	e.floor = newFloor
	e.outputDevice.FloorIndicator(e.floor)
	switch e.behaviour {
	case EB_Moving:
		if requests_shouldStop(*e) != 0 {
			e.outputDevice.MotorDirection(common.MD_Stop)
			e.outputDevice.DoorLight(true)

			*e, _ = requests_clearAtCurrentFloor(*e)
			e.behaviour = EB_DoorOpen
		}
	default: // no-op
	}
}

func (e *Elevator) OnDoorTimeout() {
	switch e.behaviour {
	case EB_DoorOpen:
		pair := requests_chooseDirection(*e)

		e.dirn = pair.dirn
		e.behaviour = pair.behaviour

		switch e.behaviour {
		case EB_DoorOpen:
			*e, _ = requests_clearAtCurrentFloor(*e)

		case EB_Moving, EB_Idle:
			e.outputDevice.DoorLight(false)
			e.outputDevice.MotorDirection(e.dirn)
		}
	default:
		// no-op
	}
}

func (e *Elevator) OnStopButtonPress() { //TODO make function cool
	switch e.behaviour {
	case EB_Moving:
		e.outputDevice.MotorDirection(common.MD_Stop)
		e.outputDevice.DoorLight(true)
		e.behaviour = EB_DoorOpen
	}
}

func (e *Elevator) SwitchLight(floor int, btn common.ButtonType, on bool) {
	e.outputDevice.RequestButtonLight(floor, btn, on)
}

func (e *Elevator) GetBehaviour() ElevatorBehaviour { return e.behaviour }

func (e *Elevator) GetDirection() common.MotorDirection { return e.dirn }

func (e *Elevator) GetMotionStrings() (behavior string, direction string) {

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
