package elevfsm

import "Driver-go/elevio"

// CurrentMotionStrings returns the current FSM behaviour and motor direction
// in the string format expected by the hall request assigner.
func CurrentMotionStrings() (behavior string, direction string) {
	switch elevator.behaviour {
	case EB_Idle:
		behavior = "idle"
	case EB_DoorOpen:
		behavior = "doorOpen"
	case EB_Moving:
		behavior = "moving"
	default:
		behavior = "idle"
	}

	switch elevator.dirn {
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

func CurrentBehaviour() ElevatorBehaviour {
	return elevator.behaviour
}

func DoorOpenDuration() float64 {
	return elevator.config.doorOpenDuration_s
}
