package elevfsm

import (
	"elevator/common"
	"fmt"
	"time"
)

// enums
type ElevatorBehaviour int

const (
	EB_Idle ElevatorBehaviour = iota
	EB_DoorOpen
	EB_Moving
)

const doorOpenDuration = 3 * time.Second

// structs
type Elevator struct {
	floor        int
	dirn         common.MotorDirection
	behaviour    ElevatorBehaviour
	requests     Requests
	buttonLevels [common.N_FLOORS][common.N_BUTTONS]int
	inputDevice  common.ElevInputDevice
	outputDevice common.ElevOutputDevice

	prevFloor     int
	prevBehaviour ElevatorBehaviour
	prevDirection common.MotorDirection
	doorTimerEnd  time.Time
	announceDir   common.MotorDirection
}

func NewElevator(ioAddress string) *Elevator {
	common.ElevioInit(ioAddress)
	e := new(Elevator)
	e.floor = -1
	e.dirn = common.MD_Stop
	e.behaviour = EB_Idle

	e.inputDevice = common.ElevioGetInputDevice()
	e.outputDevice = common.ElevioGetOutputDevice()
	e.outputDevice.DoorLight(false)
	newFloor := e.FloorSensor()

	if newFloor != -1 {
		e.onFloorArrival(newFloor)
	} else {
		e.outputDevice.MotorDirection(common.MD_Down)
		e.dirn = common.MD_Down
		e.behaviour = EB_Moving
	}
	e.prevFloor = e.floor
	e.prevDirection = e.dirn
	e.prevBehaviour = e.behaviour
	return e
}

func (e *Elevator) InjectRequest(buttonFloor int, buttonType common.ButtonType) {
	e.onRequestButtonPress(buttonFloor, buttonType)
}

func (e *Elevator) ApplyInjectRequests(requests Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if requests[floor][button] {
				e.InjectRequest(floor, button)
			}
		}
	}
}

func (e *Elevator) onRequestButtonPress(buttonFloor int, buttonType common.ButtonType) {
	e.requests[buttonFloor][buttonType] = true
	if e.behaviour == EB_DoorOpen && buttonFloor == e.floor {
		e.doorTimerEnd = time.Now().Add(doorOpenDuration)
		return
	}
	if e.behaviour == EB_Idle {
		pair := requests_chooseDirection(e.requests, e.floor, e.dirn)
		e.dirn = pair.dirn
		e.behaviour = pair.behaviour
		switch pair.behaviour {
		case EB_DoorOpen:
			e.outputDevice.DoorLight(true)
		case EB_Moving:
			e.outputDevice.MotorDirection(e.dirn)
		}
	}
}

func (e *Elevator) ClearRequest(floor int, button common.ButtonType) {
	e.requests = requests_clear(e.requests, floor, button)
}

func (e *Elevator) ApplyClearRequests(requests Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if requests[floor][button] {
				e.ClearRequest(floor, button)
			}
		}
	}
}

func (e *Elevator) PollButtonPresses() (buttonPresses Requests, hadPress bool) {
	buttonPresses, hadPress = Requests{}, false

	for floor := range common.N_FLOORS {
		for button := range common.N_BUTTONS {
			value := e.inputDevice.RequestButton(floor, common.ButtonType(button))
			isEdge := value != 0 && value != e.buttonLevels[floor][button]
			if isEdge {
				hadPress = true
				buttonType := common.ButtonType(button)
				buttonPresses[floor][buttonType] = true
			}
			e.buttonLevels[floor][button] = value
		}
	}
	return buttonPresses, hadPress
}

func (e *Elevator) Tick(now time.Time) (stateChanged bool, servicedFloor int, servicedCalls Requests) {
	servicedFloor = -1
	servicedCalls = Requests{}

	newFloor := e.FloorSensor()
	newBehaviour := e.behaviour
	newDirection := e.dirn
	obstructed := e.obstruction()

	if newFloor != e.prevFloor ||
		newBehaviour != e.prevBehaviour ||
		newDirection != e.prevDirection {
		stateChanged = true
	}

	if newFloor != -1 && e.prevFloor != newFloor {
		e.onFloorArrival(newFloor)
		e.prevFloor = newFloor
	}

	if obstructed && newBehaviour == EB_DoorOpen {
		e.doorTimerEnd = now.Add(doorOpenDuration)
	}

	if e.prevBehaviour != newBehaviour && newBehaviour == EB_DoorOpen {
		arrivalDirection := e.dirn
		e.announceDir = e.chooseNewDirAtFloor(e.prevFloor, arrivalDirection)
		e.doorTimerEnd = now.Add(doorOpenDuration)
	}
	e.prevBehaviour = newBehaviour
	e.prevDirection = newDirection

	if now.After(e.doorTimerEnd) && e.prevBehaviour == EB_DoorOpen {
		servicedFloor, servicedCalls = e.onDoorTimerExpiry(now)
	}
	return stateChanged, servicedFloor, servicedCalls
}

func (e *Elevator) onDoorTimerExpiry(now time.Time) (servicedFloor int, servicedCalls Requests) {
	servicedFloor = -1
	servicedCalls = Requests{}

	e.doorTimerEnd = now
	if e.prevFloor < 0 || e.prevFloor >= common.N_FLOORS {
		return servicedFloor, servicedCalls
	}

	servicedFloor = e.prevFloor
	servicedCalls, nextAnnounceDirection, restartDoorTimer := e.OnDoorClose(e.prevFloor, e.announceDir, true)
	e.announceDir = nextAnnounceDirection
	if restartDoorTimer {
		e.doorTimerEnd = now.Add(doorOpenDuration)
	}
	return servicedFloor, servicedCalls
}

func (e *Elevator) CurrentFloor() int {
	return e.prevFloor
}

func (e *Elevator) FloorSensor() int {
	return e.inputDevice.FloorSensor()
}

func (e *Elevator) SetRequestLights(calls Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			e.outputDevice.RequestButtonLight(floor, button, calls[floor][button])
		}
	}
}

func (e *Elevator) MotionStrings() (behavior string, direction string) {
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

func (e *Elevator) onFloorArrival(newFloor int) {
	fmt.Println("landed on floor: ", newFloor)
	e.floor = newFloor
	e.outputDevice.FloorIndicator(e.floor)
	if e.behaviour == EB_Moving && requests_shouldStop(e.requests, e.floor, e.dirn) != 0 {
		e.outputDevice.MotorDirection(common.MD_Stop)
		e.outputDevice.DoorLight(true)
		e.behaviour = EB_DoorOpen
	}
}

func (e *Elevator) onDoorTimeout() {
	if e.behaviour != EB_DoorOpen {
		return
	}
	pair := requests_chooseDirection(e.requests, e.floor, e.dirn)
	e.dirn = pair.dirn
	e.behaviour = pair.behaviour
	if e.behaviour == EB_Moving || e.behaviour == EB_Idle {
		e.outputDevice.DoorLight(false)
		e.outputDevice.MotorDirection(e.dirn)
	}
}

func (e *Elevator) obstruction() bool {
	return e.inputDevice.Obstruction() != 0
}

func (e *Elevator) chooseNewDirAtFloor(floor int, fallback common.MotorDirection) common.MotorDirection {
	up, down := requests_hallRequestsAtFloor(e.requests, floor)
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

func (e *Elevator) shouldSwitchDirection() bool {
	switch e.dirn {
	case common.MD_Up:
		return requests_above(e.requests, e.floor) == 0
	case common.MD_Down:
		return requests_below(e.requests, e.floor) == 0
	default:
		return true
	}
}

// OnDoorClose decides local door-expiry behaviour and applies local FSM transitions.
// Returns cleared requests at floor, the next announce direction, and whether to restart the door timer.
func (e *Elevator) OnDoorClose(floor int, announceDir common.MotorDirection, clearCab bool) (cleared Requests, nextAnnounceDir common.MotorDirection, restartDoorTimer bool) {
	e.floor = floor
	upReq, downReq := requests_hallRequestsAtFloor(e.requests, e.floor)
	nextAnnounceDir = announceDir

	if announceDir == common.MD_Up && upReq {
		e.requests, cleared = requests_clearAtCurrentFloorDir(e.requests, e.floor, common.MD_Up, clearCab)
		if downReq && e.shouldSwitchDirection() {
			return cleared, common.MD_Down, true
		}
		e.onDoorTimeout()
		return cleared, announceDir, false
	}

	if announceDir == common.MD_Down && downReq {
		e.requests, cleared = requests_clearAtCurrentFloorDir(e.requests, e.floor, common.MD_Down, clearCab)
		if upReq && e.shouldSwitchDirection() {
			return cleared, common.MD_Up, true
		}
		e.onDoorTimeout()
		return cleared, announceDir, false
	}

	if upReq || downReq {
		arrivalDir := e.dirn
		nextAnnounceDir = e.chooseNewDirAtFloor(floor, arrivalDir)
		e.requests, cleared = requests_clearAtCurrentFloorDir(e.requests, e.floor, nextAnnounceDir, clearCab)

		if nextAnnounceDir == common.MD_Up && downReq && e.shouldSwitchDirection() {
			return cleared, common.MD_Down, true
		}
		if nextAnnounceDir == common.MD_Down && upReq && e.shouldSwitchDirection() {
			return cleared, common.MD_Up, true
		}
		e.onDoorTimeout()
		return cleared, nextAnnounceDir, false
	}

	e.requests, cleared = requests_clearAtCurrentFloorDir(e.requests, e.floor, common.MD_Stop, clearCab)
	e.onDoorTimeout()
	return cleared, common.MD_Stop, false
}
