package elevfsm

import (
	"elevator/common"
	"elevator/elevhw"
	"fmt"
	"time"
)

type Elevator struct {
	inputDevice  elevhw.ElevInputDevice
	outputDevice elevhw.ElevOutputDevice

	floor     int
	dirn      elevhw.MotorDirection
	behaviour ElevatorBehaviour
	requests  common.Requests

	prevFloor     int
	prevDirection elevhw.MotorDirection
	prevBehaviour ElevatorBehaviour

	doorTimer   time.Time
	announceDir elevhw.MotorDirection
}

func InitElevator() *Elevator {
	elevhw.InitElevio(common.IO_ADDRESS)
	e := new(Elevator)
	e.floor = -1
	e.dirn = elevhw.MD_Stop
	e.behaviour = EB_Idle

	e.inputDevice = elevhw.ElevioGetInputDevice()
	e.outputDevice = elevhw.ElevioGetOutputDevice()
	e.outputDevice.DoorLight(false)
	e.SetLights(common.Requests{})
	newFloor := e.inputDevice.FloorSensor()

	if newFloor != -1 {
		e.onFloorArrival(newFloor)

	} else {
		e.outputDevice.MotorDirection(elevhw.MD_Down)
		e.dirn = elevhw.MD_Down
		e.behaviour = EB_Moving
		e.prevFloor = -1
	}

	e.prevDirection = e.dirn
	e.prevBehaviour = e.behaviour
	return e
}

func (e *Elevator) ElevUpdate(now time.Time) (stateChanged bool, servicedRequests common.Requests, isServiced bool) {
	servicedRequests = common.Requests{}
	isServiced = false

	isObstructed := e.inputDevice.Obstruction()
	newFloor := e.inputDevice.FloorSensor()

	if e.floor != newFloor ||
		e.behaviour != e.prevBehaviour ||
		e.dirn != e.prevDirection {
		stateChanged = true
	}

	e.floor = newFloor

	if e.floor != -1 && e.prevFloor != e.floor {
		e.onFloorArrival(e.floor)
	}

	if isObstructed != 0 && e.behaviour == EB_DoorOpen {
		e.doorTimer = now
	}

	if e.prevBehaviour != e.behaviour && e.behaviour == EB_DoorOpen {
		arrivalDirection := e.dirn
		e.announceDir = e.chooseNewDirAtFloor(e.prevFloor, arrivalDirection)
		e.doorTimer = now
	}
	e.prevBehaviour = e.behaviour
	e.prevDirection = e.dirn

	if e.prevBehaviour == EB_DoorOpen && now.Sub(e.doorTimer) >= common.DOOR_OPEN_DURATION {
		servicedRequests = e.onDoorTimerExpiry(now)
		isServiced = true
	}
	return stateChanged, servicedRequests, isServiced
}

func (e *Elevator) ApplyNewRequests(requests common.Requests) {
	for floor := range elevhw.N_FLOORS {
		for button := range elevhw.ButtonType(elevhw.N_BUTTONS) {
			if requests[floor][button] {
				e.onRequest(floor, button)
			}
		}
	}
}

func (e *Elevator) RevokeRequests(requests common.Requests) {
	for floor := range elevhw.N_FLOORS {
		for button := range elevhw.ButtonType(elevhw.N_BUTTONS) {
			if requests[floor][button] {
				e.requests[floor][button] = false
			}
		}
	}
}

func (e *Elevator) PollButtonPresses() (buttonPresses common.Requests, hadPress bool) {
	buttonPresses, hadPress = common.Requests{}, false

	for floor := range elevhw.N_FLOORS {
		for button := range elevhw.ButtonType(elevhw.N_BUTTONS) {
			isPressed := e.inputDevice.RequestButton(floor, button)
			if isPressed != 0 {
				hadPress = true
				buttonType := button
				buttonPresses[floor][buttonType] = true
			}
		}
	}
	return buttonPresses, hadPress
}

func (e *Elevator) GetPrevFloor() int { return e.prevFloor }

func (e *Elevator) GetFloor() int { return e.inputDevice.FloorSensor() }

func (e *Elevator) IsIdle() bool { return e.behaviour == EB_Idle }

func (e *Elevator) SetLights(requests common.Requests) {
	for floor := range elevhw.N_FLOORS {
		for btn := range elevhw.ButtonType(elevhw.N_BUTTONS) {
			e.outputDevice.RequestButtonLight(floor, btn, requests[floor][btn])
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
	case elevhw.MD_Up:
		direction = "up"
	case elevhw.MD_Down:
		direction = "down"
	case elevhw.MD_Stop:
		direction = "stop"
	default:
		direction = "stop"
	}
	return behavior, direction
}

func (e *Elevator) onDoorTimerExpiry(now time.Time) (servicedRequests common.Requests) {
	e.doorTimer = now
	servicedRequests, nextAnnounceDirection := e.onDoorClose(e.prevFloor, e.announceDir)

	pair := requests_chooseDirection(e.requests, e.floor, e.dirn)
	e.dirn = pair.dirn
	e.behaviour = pair.behaviour
	if e.behaviour == EB_Moving || e.behaviour == EB_Idle {
		e.outputDevice.DoorLight(false)
		e.outputDevice.MotorDirection(e.dirn)
	}
	e.announceDir = nextAnnounceDirection

	return servicedRequests
}

// OnDoorClose decides local door-expiry behaviour
// Returns the requests cleared at the given floor, the next announced direction, and whether to restart the door timer.
// TODO: move into onDoorTimerExpiry
func (e *Elevator) onDoorClose(floor int, announceDir elevhw.MotorDirection) (cleared common.Requests, nextAnnounceDir elevhw.MotorDirection) {
	e.floor = floor
	requestsAboveFloor, requestsBelowFloor := requests_hallRequestsAtFloor(e.requests, e.floor)
	nextAnnounceDir = announceDir

	if announceDir == elevhw.MD_Up && requestsAboveFloor {
		e.requests, cleared = requests_clearAtFloorDir(e.requests, e.floor, elevhw.MD_Up)
		if requestsBelowFloor && e.shouldSwitchDirection() {
			return cleared, elevhw.MD_Down
		}

	} else if announceDir == elevhw.MD_Down && requestsBelowFloor {
		e.requests, cleared = requests_clearAtFloorDir(e.requests, e.floor, elevhw.MD_Down)
		if requestsAboveFloor && e.shouldSwitchDirection() {
			return cleared, elevhw.MD_Up
		}

	} else if requestsAboveFloor || requestsBelowFloor {
		arrivalDir := e.dirn
		nextAnnounceDir = e.chooseNewDirAtFloor(floor, arrivalDir)
		e.requests, cleared = requests_clearAtFloorDir(e.requests, e.floor, nextAnnounceDir)

		if nextAnnounceDir == elevhw.MD_Up && requestsBelowFloor && e.shouldSwitchDirection() {
			return cleared, elevhw.MD_Down
		}
		if nextAnnounceDir == elevhw.MD_Down && requestsAboveFloor && e.shouldSwitchDirection() {
			return cleared, elevhw.MD_Up
		}

	} else {
		e.requests, cleared = requests_clearAtFloorDir(e.requests, e.floor, elevhw.MD_Stop)
		nextAnnounceDir = elevhw.MD_Stop
	}
	return cleared, nextAnnounceDir
}

func (e *Elevator) onFloorArrival(newFloor int) {
	fmt.Println("landed on floor: ", newFloor)
	e.floor = newFloor
	e.prevFloor = e.floor
	e.outputDevice.FloorIndicator(e.floor)
	if e.behaviour == EB_Moving && requests_shouldStop(e.requests, e.floor, e.dirn) != 0 {
		e.outputDevice.MotorDirection(elevhw.MD_Stop)
		e.outputDevice.DoorLight(true)
		e.behaviour = EB_DoorOpen
	}
}

func (e *Elevator) onRequest(buttonFloor int, buttonType elevhw.ButtonType) {
	e.requests[buttonFloor][buttonType] = true
	if e.floor == -1 {
		e.floor = e.prevFloor
	}
	if e.behaviour == EB_DoorOpen && buttonFloor == e.floor {
		e.doorTimer = time.Now()
		return
	}
	if e.behaviour == EB_Idle {
		pair := requests_chooseDirection(e.requests, e.floor, e.dirn)
		e.dirn = pair.dirn
		e.behaviour = pair.behaviour
		switch pair.behaviour {
		case EB_DoorOpen:
			e.outputDevice.DoorLight(true)
			e.doorTimer = time.Now()
		case EB_Moving:
			e.outputDevice.MotorDirection(e.dirn)
		}
	}
}

func (e *Elevator) chooseNewDirAtFloor(floor int, fallback elevhw.MotorDirection) elevhw.MotorDirection {
	up, down := requests_hallRequestsAtFloor(e.requests, floor)
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

func (e *Elevator) shouldSwitchDirection() bool {
	switch e.dirn {
	case elevhw.MD_Up:
		return requests_above(e.requests, e.floor) == 0
	case elevhw.MD_Down:
		return requests_below(e.requests, e.floor) == 0
	default:
		return true
	}
}
