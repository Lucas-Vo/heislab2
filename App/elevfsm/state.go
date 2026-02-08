package elevfsm

import (
	"Driver-go/elevio"
)

// CurrentMotionStrings returns the current FSM behaviour and motor direction
// in the string format expected by the hall request assigner.
func CurrentMotionStrings(elevator *Elevator) (string, string) {
	behavior := "idle"
	direction := "stop"
	switch elevator.Behaviour {
	case EB_Idle:
		behavior = "idle"
	case EB_DoorOpen:
		behavior = "doorOpen"
	case EB_Moving:
		behavior = "moving"
	default:
		behavior = "idle"
	}

	switch elevator.Dirn {
	case elevio.MD_Up:
		direction = "up"
	case elevio.MD_Down:
		direction = "down"
	case elevio.MD_Stop:
		direction = "stop"
	default:
		direction = "stop"
	}

	return behavior, direction
}
