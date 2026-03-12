package elevfsm

import (
	"elevator/common"
	"fmt"
	"time"
)

const (
	DOOR_OPEN_DURATION = 3 * time.Second
	IO_ADDRESS         = "localhost:15657"
)

type Elevator struct {
	inputDevice  common.ElevInputDevice
	outputDevice common.ElevOutputDevice

	floor     int
	dirn      common.MotorDirection
	behaviour ElevatorBehaviour
	requests  Requests

	prevFloor     int
	prevDirection common.MotorDirection
	prevBehaviour ElevatorBehaviour

	doorTimer   time.Time
	announceDir common.MotorDirection
}

func NewElevator() *Elevator {
	common.ElevioInit(IO_ADDRESS)
	e := new(Elevator)
	e.floor = -1
	e.dirn = common.MD_Stop
	e.behaviour = EB_Idle

	e.inputDevice = common.ElevioGetInputDevice()
	e.outputDevice = common.ElevioGetOutputDevice()
	e.outputDevice.DoorLight(false)
	e.SetHallLights([common.N_FLOORS][2]bool{})
	e.SetCabLights([common.N_FLOORS]bool{})
	newFloor := e.inputDevice.FloorSensor()

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

func (e *Elevator) ApplyInjectRequests(requests Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if requests[floor][button] {
				e.onRequestButtonPress(floor, button)
			}
		}
	}
}

func (e *Elevator) onRequestButtonPress(buttonFloor int, buttonType common.ButtonType) {
	e.requests[buttonFloor][buttonType] = true
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
		for button := range common.ButtonType(common.N_BUTTONS) {
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

func (e *Elevator) Tick(now time.Time) (stateChanged bool, servicedFloor int, servicedCalls Requests) {
	servicedFloor = -1
	servicedCalls = Requests{}

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
		e.prevFloor = e.floor
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

	if e.prevBehaviour == EB_DoorOpen && now.Sub(e.doorTimer) >= DOOR_OPEN_DURATION {
		servicedFloor, servicedCalls = e.onDoorTimerExpiry(now)
	}
	return stateChanged, servicedFloor, servicedCalls
}

func (e *Elevator) onDoorTimerExpiry(now time.Time) (servicedFloor int, servicedCalls Requests) {
	servicedCalls = Requests{}
	servicedFloor = e.prevFloor
	e.doorTimer = now

	servicedCalls, nextAnnounceDirection, restartDoorTimer := e.OnDoorClose(e.prevFloor, e.announceDir, true)
	e.announceDir = nextAnnounceDirection
	if restartDoorTimer {
		e.doorTimer = now
	}
	return servicedFloor, servicedCalls
}

func (e *Elevator) GetPrevFloor() int { return e.prevFloor }

func (e *Elevator) GetFloor() int { return e.inputDevice.FloorSensor() }

func (e *Elevator) SetHallLights(hallRequests [common.N_FLOORS][2]bool) {
	for floor := range common.N_FLOORS {
		e.outputDevice.RequestButtonLight(floor, common.BT_HallDown, hallRequests[floor][common.BT_HallDown])
		e.outputDevice.RequestButtonLight(floor, common.BT_HallUp, hallRequests[floor][common.BT_HallUp])
	}
}

func (e *Elevator) SetCabLights(localCab [common.N_FLOORS]bool) {
	for floor := range common.N_FLOORS {
		e.outputDevice.RequestButtonLight(floor, common.BT_Cab, localCab[floor])
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
	upRequestAtFloor, downRequestAtFloor := requests_hallRequestsAtFloor(e.requests, e.floor)
	nextAnnounceDir = announceDir
	//TODO: add comments to describe the different cases
	if announceDir == common.MD_Up && upRequestAtFloor {
		e.requests, cleared = requests_clearAtFloorDir(e.requests, e.floor, common.MD_Up, clearCab)
		if downRequestAtFloor && e.shouldSwitchDirection() {
			return cleared, common.MD_Down, true
		}

	} else if announceDir == common.MD_Down && downRequestAtFloor {
		e.requests, cleared = requests_clearAtFloorDir(e.requests, e.floor, common.MD_Down, clearCab)
		if upRequestAtFloor && e.shouldSwitchDirection() {
			return cleared, common.MD_Up, true
		}

	} else if upRequestAtFloor || downRequestAtFloor {
		arrivalDir := e.dirn
		nextAnnounceDir = e.chooseNewDirAtFloor(floor, arrivalDir)
		e.requests, cleared = requests_clearAtFloorDir(e.requests, e.floor, nextAnnounceDir, clearCab)

		if nextAnnounceDir == common.MD_Up && downRequestAtFloor && e.shouldSwitchDirection() {
			return cleared, common.MD_Down, true
		}
		if nextAnnounceDir == common.MD_Down && upRequestAtFloor && e.shouldSwitchDirection() {
			return cleared, common.MD_Up, true
		}

	} else {
		e.requests, cleared = requests_clearAtFloorDir(e.requests, e.floor, common.MD_Stop, clearCab)
		nextAnnounceDir = common.MD_Stop
	}
	e.onDoorTimeout()
	return cleared, nextAnnounceDir, false
}
