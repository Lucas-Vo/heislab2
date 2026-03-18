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
	prevDirn      elevhw.MotorDirection
	prevBehaviour ElevatorBehaviour

	doorTimer    time.Time
	announceDirn elevhw.MotorDirection
}

// ------------ Exported Methods -------------

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

	e.prevDirn = e.dirn
	e.prevBehaviour = e.behaviour
	return e
}

// polls physical sensors and updates behavior of elevator
func (e *Elevator) ElevUpdate(now time.Time) (stateChanged bool, servicedRequests common.Requests, isServiced bool) {
	servicedRequests = common.Requests{}
	isServiced = false

	isObstructed := e.pollObstruction()
	newFloor := e.pollFloor()

	if e.floor != newFloor ||
		e.behaviour != e.prevBehaviour ||
		e.dirn != e.prevDirn {
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
		arrivalDirn := e.dirn
		e.announceDirn = requests_chooseNewDirnAtFloor(e.requests, e.prevFloor, arrivalDirn)
		e.doorTimer = now
	}
	e.prevBehaviour = e.behaviour
	e.prevDirn = e.dirn

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

func (e *Elevator) RemoveAssignments(removeAssignments common.HallAssignment) {
	for floor := range elevhw.N_FLOORS {
		for button := 0; button < 2; button++ {
			if removeAssignments[floor][button] {
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

func (e *Elevator) SetLights(requests common.Requests) {
	for floor := range elevhw.N_FLOORS {
		for button := range elevhw.ButtonType(elevhw.N_BUTTONS) {
			e.outputDevice.RequestButtonLight(floor, button, requests[floor][button])
		}
	}
}

// makes parsing into json easier
func (e *Elevator) MotionToStrings() (behavior string, dirn string) {
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
		dirn = "up"
	case elevhw.MD_Down:
		dirn = "down"
	case elevhw.MD_Stop:
		dirn = "stop"
	default:
		dirn = "stop"
	}
	return behavior, dirn
}

func (e *Elevator) GetPrevFloor() int { return e.prevFloor }

func (e *Elevator) GetFloor() int { return e.inputDevice.FloorSensor() }

func (e *Elevator) IsIdle() bool { return e.behaviour == EB_Idle }

// ------------ Unexported Methods -------------

func (e *Elevator) onDoorTimerExpiry(now time.Time) (servicedRequests common.Requests) {
	e.doorTimer = now
	nextAnnounceDirn, announceDir := requests_chooseNextAnnounceDirn(e.requests, e.prevFloor, e.announceDirn, e.dirn)
	servicedRequests = e.clearRequestAtFloor(e.prevFloor, announceDir)

	pair := requests_chooseDirn(e.requests, e.floor, e.dirn)
	e.dirn = pair.dirn
	e.behaviour = pair.behaviour
	if e.behaviour == EB_Moving || e.behaviour == EB_Idle {
		e.outputDevice.DoorLight(false)
		e.outputDevice.MotorDirection(e.dirn)
	}
	e.announceDirn = nextAnnounceDirn

	return servicedRequests
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
		pair := requests_chooseDirn(e.requests, e.floor, e.dirn)
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

func (e *Elevator) clearRequestAtFloor(floor int, announceDirn elevhw.MotorDirection) (cleared common.Requests) {
	e.floor = floor
	e.requests, cleared = requests_clearAtFloorDir(e.requests, e.floor, announceDirn)
	return cleared
}

func (e *Elevator) pollObstruction() int {
	return e.inputDevice.Obstruction()
}

func (e *Elevator) pollFloor() int {
	return e.inputDevice.FloorSensor()
}
