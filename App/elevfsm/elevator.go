package elevfsm

//TODO: elevator.go fsm.go requests.go and timer.go is AALLLL soup. make that shit dissapear as most is implemented in fsmsync
import (
	"elevator/common"
)

// enums

type ElevatorBehaviour int

const (
	EB_Idle = iota
	EB_DoorOpen
	EB_Moving
)

type ClearRequestVariant int

// structs
type Elevator struct {
	floor     int
	dirn      common.MotorDirection
	behaviour ElevatorBehaviour
	requests  [common.N_FLOORS][common.N_BUTTONS]bool
	config    struct {
		clearRequestVariant ClearRequestVariant
		doorOpenDuration_s  float64
	}
}

// functions

func elevator_uninitialized() Elevator {
	var elevator Elevator
	elevator.floor = -1
	elevator.dirn = common.MD_Stop
	elevator.behaviour = EB_Idle
	elevator.config.doorOpenDuration_s = 3.0
	return elevator
}
