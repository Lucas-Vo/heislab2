package elevalgo

import (
	"Driver-go/elevio"
	"fmt"
)

// enums

type ElevatorBehaviour int

const (
	EB_Idle = iota
	EB_DoorOpen
	EB_Moving
)

type ClearRequestVariant int

const (
	CV_All ClearRequestVariant = iota
	CV_InDirn
)

// structs
type Elevator struct {
	floor     int
	dirn      elevio.MotorDirection
	behaviour ElevatorBehaviour
	requests  [N_FLOORS][N_BUTTONS]bool
	config    struct {
		clearRequestVariant ClearRequestVariant
		doorOpenDuration_s  float64
	}
}

// functions

func eb_toString(eb ElevatorBehaviour) string {
	if eb == EB_Idle {
		return "EB_Idle"
	} else if eb == EB_DoorOpen {
		return "EB_DoorOpen"
	} else if eb == EB_Moving {
		return "EB_Moving"
	} else {
		return "EB_UNDEFINED"
	}
}

// TODO: consider removing
func elevator_print(es Elevator) {
	fmt.Println("  +--------------------+")
	fmt.Printf(
		"  |floor = %-2d          |\n"+
			"  |dirn  = %-12.12s|\n"+
			"  |behav = %-12.12s|\n",
		es.floor,
		elevio_dirn_toString(es.dirn),
		eb_toString(es.behaviour),
	)
	fmt.Printf("  +--------------------+\n")
	fmt.Printf("  |  | up  | dn  | cab |\n")

	for f := N_FLOORS - 1; f >= 0; f-- {
		fmt.Printf("  | %d", f)
		for btn := elevio.ButtonType(0); btn < N_BUTTONS; btn++ {
			if (f == N_FLOORS-1 && btn == elevio.BT_HallUp) ||
				(f == 0 && btn == elevio.BT_HallDown) {
				fmt.Printf("|     ")
			} else {
				if es.requests[f][btn] {
					fmt.Printf("|  #  ")
				} else {
					fmt.Printf("|  -  ")
				}
			}
		}
		fmt.Printf("|\n")
	}
	fmt.Printf("  +--------------------+\n")
}

func elevator_uninitialized() Elevator {
	var elevator Elevator
	elevator.floor = -1
	elevator.dirn = elevio.MD_Stop
	elevator.behaviour = EB_Idle
	elevator.config.clearRequestVariant = CV_All
	elevator.config.doorOpenDuration_s = 3.0
	return elevator
}
