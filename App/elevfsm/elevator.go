package elevfsm

import (
	"elevator/common"
	"fmt"
	"time"
)

const (
	// DOOR_OPEN_DURATION is the course-specified door-open time.
	DOOR_OPEN_DURATION = 3 * time.Second
	// IO_ADDRESS is the hard-coded simulator TCP endpoint used by this build.
	IO_ADDRESS = "localhost:15657"
)

// Elevator owns the local elevator FSM and simulator I/O.
//
// It is designed to be driven by exactly one goroutine. The type keeps the
// currently latched requests, the last known floor, the movement state, and the
// temporary direction announcement used to clear up/down hall calls separately.
type Elevator struct {
	inputDevice  common.ElevInputDevice
	outputDevice common.ElevOutputDevice

	floor     int
	dirn      common.MotorDirection
	behaviour ElevatorBehaviour
	requests  common.Requests

	prevFloor     int
	prevDirection common.MotorDirection
	prevBehaviour ElevatorBehaviour

	doorTimer   time.Time
	announceDir common.MotorDirection
}

// NewElevator initializes the local controller and returns it in a defined
// startup state.
//
// If the elevator starts between floors, the controller drives downward until a
// floor sensor becomes active. The function panics if the simulator connection
// at IO_ADDRESS cannot be opened.
func NewElevator() *Elevator {
	common.ElevioInit(IO_ADDRESS)
	e := new(Elevator)
	e.floor = -1
	e.dirn = common.MD_Stop
	e.behaviour = EB_Idle

	e.inputDevice = common.ElevioGetInputDevice()
	e.outputDevice = common.ElevioGetOutputDevice()
	e.outputDevice.DoorLight(false)
	e.SetLights(common.Requests{})
	newFloor := e.inputDevice.FloorSensor()

	if newFloor != -1 {
		e.onFloorArrival(newFloor)
	} else {
		// The project assumes the controller may need to recover its position at
		// startup. The chosen policy is to move downward until the first floor
		// sensor edge is observed.
		e.outputDevice.MotorDirection(common.MD_Down)
		e.dirn = common.MD_Down
		e.behaviour = EB_Moving
		e.prevFloor = 1
	}

	e.prevDirection = e.dirn
	e.prevBehaviour = e.behaviour
	return e
}

// ApplyNewRequests latches the supplied requests into the local FSM.
func (e *Elevator) ApplyNewRequests(requests common.Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if requests[floor][button] {
				e.onRequest(floor, button)
			}
		}
	}
}

func (e *Elevator) onRequest(buttonFloor int, buttonType common.ButtonType) {
	e.requests[buttonFloor][buttonType] = true
	if e.behaviour == EB_DoorOpen && buttonFloor == e.floor {
		// Re-pressing a button at the current floor extends the open-door window
		// instead of scheduling an extra stop later.
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

// RevokeRequest removes requests that were de-assigned away from this elevator.
func (e *Elevator) RevokeRequest(requests common.Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if requests[floor][button] {
				e.requests[floor][button] = false
			}
		}
	}
}

// PollButtonPresses snapshots the current button matrix and reports whether any
// button is pressed right now.
//
// The method polls level-triggered simulator inputs, so repeated calls while a
// physical button remains pressed will continue to report that request.
func (e *Elevator) PollButtonPresses() (buttonPresses common.Requests, hadPress bool) {
	buttonPresses, hadPress = common.Requests{}, false

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

// UpdateFSM advances the local FSM by one polling tick.
//
// It samples the floor sensor and obstruction switch, updates the movement and
// door state, and returns any requests that were just serviced. The caller is
// responsible for running this method periodically from a single goroutine.
func (e *Elevator) UpdateFSM(now time.Time) (stateChanged bool, servicedRequests common.Requests, isServiced bool) {
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

	// Obstruction keeps the door open by restarting the timer every tick while
	// the switch is active.
	if isObstructed != 0 && e.behaviour == EB_DoorOpen {
		e.doorTimer = now
	}

	if e.prevBehaviour != e.behaviour && e.behaviour == EB_DoorOpen {
		// The first door-open period announces the direction that is being served
		// at this floor. If the opposite hall call must also be announced, the
		// remaining request stays latched and causes a second 3 s door-open cycle.
		arrivalDirection := e.dirn
		e.announceDir = e.chooseNewDirAtFloor(e.prevFloor, arrivalDirection)
		e.doorTimer = now
	}
	e.prevBehaviour = e.behaviour
	e.prevDirection = e.dirn

	if e.prevBehaviour == EB_DoorOpen && now.Sub(e.doorTimer) >= DOOR_OPEN_DURATION {
		servicedRequests = e.onDoorTimerExpiry(now)
		isServiced = true
	}
	return stateChanged, servicedRequests, isServiced
}

func (e *Elevator) onDoorTimerExpiry(now time.Time) (servicedRequests common.Requests) {
	e.doorTimer = now
	servicedRequests, nextAnnounceDirection := e.OnDoorClose(e.prevFloor, e.announceDir)

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

// GetPrevFloor returns the last defined floor seen by the controller.
func (e *Elevator) GetPrevFloor() int { return e.prevFloor }

// GetFloor returns the current floor sensor value, or -1 between floors.
func (e *Elevator) GetFloor() int { return e.inputDevice.FloorSensor() }

// IsIdle reports whether the local FSM is currently idle.
func (e *Elevator) IsIdle() bool { return e.behaviour == EB_Idle }

// SetLights writes the complete local request matrix to the simulator lamps.
//
// In distributed mode this should normally be called with the coherent network
// request set so hall lights remain shared across workspaces.
func (e *Elevator) SetLights(requests common.Requests) {
	for floor := range common.N_FLOORS {
		for btn := range common.ButtonType(common.N_BUTTONS) {
			e.outputDevice.RequestButtonLight(floor, btn, requests[floor][btn])
		}
	}
}

// MotionStrings returns the assigner-compatible string encoding of the current
// behaviour and direction.
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

// SetStopLight uses the simulator stop lamp as a coarse online/offline
// indicator for this elevator.
func (e *Elevator) SetStopLight(online bool) {
	e.outputDevice.StopButtonLight(!online)
}

func (e *Elevator) onFloorArrival(newFloor int) {
	fmt.Println("landed on floor: ", newFloor)
	e.floor = newFloor
	e.prevFloor = e.floor
	e.outputDevice.FloorIndicator(e.floor)
	if e.behaviour == EB_Moving && requests_shouldStop(e.requests, e.floor, e.dirn) != 0 {
		// Stopping decisions happen on floor-sensor edges so the elevator never
		// opens the door while between floors.
		e.outputDevice.MotorDirection(common.MD_Stop)
		e.outputDevice.DoorLight(true)
		e.behaviour = EB_DoorOpen
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

// OnDoorClose decides which requests are cleared when the current 3 s door-open
// interval expires.
//
// Cab calls are always cleared at the stop floor. Hall calls are only cleared
// for the currently announced direction so up and down guarantees remain
// separate. If the opposite hall direction is still waiting and the elevator is
// about to reverse, the method leaves that opposite request latched and returns
// the next announced direction so a second 3 s open interval can serve it.
func (e *Elevator) OnDoorClose(floor int, announceDir common.MotorDirection) (cleared common.Requests, nextAnnounceDir common.MotorDirection) {
	e.floor = floor
	upRequestAtFloor, downRequestAtFloor := requests_hallRequestsAtFloor(e.requests, e.floor)
	nextAnnounceDir = announceDir
	if announceDir == common.MD_Up && upRequestAtFloor {
		e.requests, cleared = requests_clearAtFloorDir(e.requests, e.floor, common.MD_Up)
		if downRequestAtFloor && e.shouldSwitchDirection() {
			return cleared, common.MD_Down
		}

	} else if announceDir == common.MD_Down && downRequestAtFloor {
		e.requests, cleared = requests_clearAtFloorDir(e.requests, e.floor, common.MD_Down)
		if upRequestAtFloor && e.shouldSwitchDirection() {
			return cleared, common.MD_Up
		}

	} else if upRequestAtFloor || downRequestAtFloor {
		arrivalDir := e.dirn
		nextAnnounceDir = e.chooseNewDirAtFloor(floor, arrivalDir)
		e.requests, cleared = requests_clearAtFloorDir(e.requests, e.floor, nextAnnounceDir)

		if nextAnnounceDir == common.MD_Up && downRequestAtFloor && e.shouldSwitchDirection() {
			return cleared, common.MD_Down
		}
		if nextAnnounceDir == common.MD_Down && upRequestAtFloor && e.shouldSwitchDirection() {
			return cleared, common.MD_Up
		}

	} else {
		e.requests, cleared = requests_clearAtFloorDir(e.requests, e.floor, common.MD_Stop)
		nextAnnounceDir = common.MD_Stop
	}
	return cleared, nextAnnounceDir
}
