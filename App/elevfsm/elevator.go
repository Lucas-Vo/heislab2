package elevfsm

import (
	"elevator/common"
	"fmt"
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
	requests     Requests
	buttonLevels [common.N_FLOORS][common.N_BUTTONS]int
	inputDevice  common.ElevInputDevice
	outputDevice common.ElevOutputDevice
}

func elevatorInit(ioAddr string) *Elevator {
	common.ElevioInit(ioAddr)
	e := new(Elevator)
	e.floor = -1
	e.dirn = common.MD_Stop
	e.behaviour = EB_Idle

	e.inputDevice = common.ElevioGetInputDevice()
	e.outputDevice = common.ElevioGetOutputDevice()
	e.outputDevice.DoorLight(false)

	return e
}

func (e *Elevator) onInitBetweenFloors() {
	e.outputDevice.MotorDirection(common.MD_Down)
	e.dirn = common.MD_Down
	e.behaviour = EB_Moving
}

func (e *Elevator) onRequestButtonPress(btnFloor int, btnType common.ButtonType) {
	e.requests[btnFloor][btnType] = true
	if e.behaviour == EB_Idle {
		pair := requests_chooseDirection(e.requests, e.floor, e.dirn)
		e.dirn = pair.dirn
		e.behaviour = pair.behaviour
		switch pair.behaviour {
		case EB_DoorOpen:
			e.outputDevice.DoorLight(true)
		case EB_Moving:
			e.outputDevice.MotorDirection(e.dirn)
		case EB_Idle:
			// no-op
		}
	}
}

func (e *Elevator) onFloorArrival(newFloor int) {
	fmt.Println("landed on floor: ", newFloor)
	e.floor = newFloor
	e.outputDevice.FloorIndicator(e.floor)
	switch e.behaviour {
	case EB_Moving:
		if requests_shouldStop(e.requests, e.floor, e.dirn) != 0 {
			e.outputDevice.MotorDirection(common.MD_Stop)
			e.outputDevice.DoorLight(true)
			e.behaviour = EB_DoorOpen
		}
	default:
		// no-op
	}
}

func (e *Elevator) onDoorTimeout() {
	switch e.behaviour {
	case EB_DoorOpen:
		pair := requests_chooseDirection(e.requests, e.floor, e.dirn)

		e.dirn = pair.dirn
		e.behaviour = pair.behaviour

		switch e.behaviour {
		case EB_Moving, EB_Idle:
			e.outputDevice.DoorLight(false)
			e.outputDevice.MotorDirection(e.dirn)
		}
	default:
		// no-op
	}
}

func (e *Elevator) onStopButtonPress() { /*TODO make function cool*/ }

func (e *Elevator) switchLight(floor int, btn common.ButtonType, on bool) {
	e.outputDevice.RequestButtonLight(floor, btn, on)
}

func (e *Elevator) getFloor() int { return e.floor }

func (e *Elevator) setFloor(floor int) { e.floor = floor }

func (e *Elevator) floorSensor() int {
	return e.inputDevice.FloorSensor()
}

func (e *Elevator) obstruction() bool {
	return e.inputDevice.Obstruction() != 0
}

func (e *Elevator) chooseNewDirAtFloor(floor int, fallback common.MotorDirection) common.MotorDirection {
	up, down := requests_hallRequestsAtFloor(e.requests, e.floor)
	if up && !down {
		return common.MD_Up
	}
	if down && !up {
		return common.MD_Down
	}
	if up && down {
		if fallback == common.MD_Up || fallback == common.MD_Down {
			return fallback
		}
		return common.MD_Up
	}
	return fallback
}

func (e *Elevator) clearRequest(floor int, btn common.ButtonType) {
	e.requests = requests_clear(e.requests, floor, btn)
}

func (e *Elevator) pollButtonPresses() (buttonPresses Requests, hadPress bool) {
	buttonPresses, hadPress = Requests{}, false

	for f := range common.N_FLOORS {
		for btn := range common.N_BUTTONS {
			value := e.inputDevice.RequestButton(f, common.ButtonType(btn))
			isEdge := value != 0 && value != e.buttonLevels[f][btn]
			if isEdge {
				hadPress = true
				buttonType := common.ButtonType(btn)
				buttonPresses[f][buttonType] = true
			}
			e.buttonLevels[f][btn] = value
		}
	}
	return buttonPresses, hadPress
}

func (e *Elevator) PollSensors() (newFloor int, newBehaviour ElevatorBehaviour, newDirection common.MotorDirection, obstructed bool) {
	newFloor = e.inputDevice.FloorSensor()
	newBehaviour = e.getBehaviour()
	newDirection = e.getDirection()
	obstructed = e.obstruction()
	return
}

func (e *Elevator) setRequestLights(calls Requests) {
	for floor := range common.N_FLOORS {
		for btn := range common.ButtonType(common.N_BUTTONS) {
			e.switchLight(floor, btn, calls[floor][btn])
		}
	}
}

func (e *Elevator) getBehaviour() ElevatorBehaviour { return e.behaviour }

func (e *Elevator) getDirection() common.MotorDirection { return e.dirn }

func (e *Elevator) shouldSwitchDirection() bool {
	switch e.getDirection() {
	case common.MD_Up:
		return requests_above(e.requests, e.floor) == 0
	case common.MD_Down:
		return requests_below(e.requests, e.floor) == 0
	case common.MD_Stop:
		return true
	default:
		return true
	}
}

// OnDoorClose decides local door-expiry behaviour and applies local FSM transitions.
// Returns cleared requests at floor, the next announce direction, and whether to restart the door timer.
func (e *Elevator) OnDoorClose(floor int, announceDir common.MotorDirection, clearCab bool) (cleared Requests, nextAnnounceDir common.MotorDirection, restartDoorTimer bool) {
	e.setFloor(floor)
	upReq, downReq := requests_hallRequestsAtFloor(e.requests, e.floor)
	nextAnnounceDir = announceDir

	if announceDir == common.MD_Up && upReq {
		cleared = requests_clearAtCurrentFloorDir(e.requests, e.floor, common.MD_Up, clearCab)
		if downReq && e.shouldSwitchDirection() {
			return cleared, common.MD_Down, true
		}
		e.onDoorTimeout()
		return cleared, announceDir, false
	}

	if announceDir == common.MD_Down && downReq {
		cleared = requests_clearAtCurrentFloorDir(e.requests, e.floor, common.MD_Down, clearCab)
		if upReq && e.shouldSwitchDirection() {
			return cleared, common.MD_Up, true
		}
		e.onDoorTimeout()
		return cleared, announceDir, false
	}

	if upReq || downReq {
		arrivalDir := e.getDirection()
		nextAnnounceDir = e.chooseNewDirAtFloor(floor, arrivalDir)
		cleared = requests_clearAtCurrentFloorDir(e.requests, e.floor, nextAnnounceDir, clearCab)

		if nextAnnounceDir == common.MD_Up && downReq && e.shouldSwitchDirection() {
			return cleared, common.MD_Down, true
		}
		if nextAnnounceDir == common.MD_Down && upReq && e.shouldSwitchDirection() {
			return cleared, common.MD_Up, true
		}
		e.onDoorTimeout()
		return cleared, nextAnnounceDir, false
	}
	cleared = requests_clearAtCurrentFloorDir(e.requests, e.floor, common.MD_Stop, clearCab)

	e.onDoorTimeout()
	return cleared, common.MD_Stop, false
}

func (e *Elevator) getMotionStrings() (behavior string, direction string) {
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
