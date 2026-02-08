package elevfsm

import (
	"Driver-go/elevio"
	"elevator/common"
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
	Floor     int
	Dirn      elevio.MotorDirection
	Behaviour ElevatorBehaviour
	Requests  [common.N_FLOORS][common.N_BUTTONS]bool
	Config    struct {
		ClearRequestVariant ClearRequestVariant
		DoorOpenDuration_s  float64
	}
}

// functions

func ebToString(eb ElevatorBehaviour) string {
	switch eb {
	case EB_Idle:
		return "EB_Idle"
	case EB_DoorOpen:
		return "EB_DoorOpen"
	case EB_Moving:
		return "EB_Moving"
	default:
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
		es.Floor,
		common.ElevioDirnToString(es.Dirn),
		ebToString(es.Behaviour),
	)
	fmt.Printf("  +--------------------+\n")
	fmt.Printf("  |  | up  | dn  | cab |\n")

	for f := common.N_FLOORS - 1; f >= 0; f-- {
		fmt.Printf("  | %d", f)
		for btn := elevio.ButtonType(0); btn < common.N_BUTTONS; btn++ {
			if (f == common.N_FLOORS-1 && btn == elevio.BT_HallUp) ||
				(f == 0 && btn == elevio.BT_HallDown) {
				fmt.Printf("|     ")
			} else {
				if es.Requests[f][btn] {
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
	elevator.Floor = -1
	elevator.Dirn = elevio.MD_Stop
	elevator.Behaviour = EB_Idle
	elevator.Config.ClearRequestVariant = CV_All
	elevator.Config.DoorOpenDuration_s = 3.0
	return elevator
}
