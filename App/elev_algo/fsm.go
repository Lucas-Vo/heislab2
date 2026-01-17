package elev_algo

import (
	"Driver-go/elevio"
	"fmt"
)

var elevator Elevator
var outputDevice ElevOutputDevice

func Fsm_init() {
	elevator = elevator_uninitialized()

	ConLoad("elevator.con",
		ConVal("doorOpenDuration_s", &elevator.config.doorOpenDuration_s, "%f"),
		ConEnum("clearRequestVariant", &elevator.config.clearRequestVariant,
			ConMatch("CV_All", CV_All),
			ConMatch("CV_InDirn", CV_InDirn),
		),
	)

	outputDevice = Elevio_getOutputDevice()
}

func SetAllLights(es Elevator) {
	for floor := range N_FLOORS {
		for btn := range N_BUTTONS {
			outputDevice.RequestButtonLight(floor, elevio.ButtonType(btn), es.requests[floor][btn])
		}
	}
}

func Fsm_onInitBetweenFloors() {
	outputDevice.motorDirection(elevio.MD_Down)
	elevator.dirn = elevio.MD_Down
	elevator.behaviour = EB_Moving
}

func Fsm_onRequestButtonPress(btn_floor int, btn_type elevio.ButtonType) {
	fmt.Printf("\n\n%s(%d, %s)\n", "Fsm_onRequestButtonPress", btn_floor, elevio_button_toString(btn_type))
	elevator_print(elevator)

	switch elevator.behaviour {
	case EB_DoorOpen:
		if requests_shouldClearImmediately(elevator, btn_floor, btn_type) != 0 {
			timer_start(elevator.config.doorOpenDuration_s)
		} else {
			elevator.requests[btn_floor][btn_type] = true
		}

	case EB_Moving:
		elevator.requests[btn_floor][btn_type] = true

	case EB_Idle:
		elevator.requests[btn_floor][btn_type] = true
		pair := requests_chooseDirection(elevator)
		elevator.dirn = pair.dirn
		elevator.behaviour = pair.behaviour

		switch pair.behaviour {
		case EB_DoorOpen:
			outputDevice.doorLight(true)
			timer_start(elevator.config.doorOpenDuration_s)
			elevator = requests_clearAtCurrentFloor(elevator)

		case EB_Moving:
			outputDevice.motorDirection(elevator.dirn)

		case EB_Idle:
			// do nothing
		}
	}

	SetAllLights(elevator)

	fmt.Printf("\nNew state:\n")
	elevator_print(elevator)
}

func Fsm_onFloorArrival(newFloor int) {
	fmt.Printf("\n\n%s(%d)\n", "Fsm_onFloorArrival", newFloor)
	elevator_print(elevator)

	elevator.floor = newFloor
	outputDevice.floorIndicator(elevator.floor)

	switch elevator.behaviour {
	case EB_Moving:
		if requests_shouldStop(elevator) != 0 {
			outputDevice.motorDirection(elevio.MD_Stop)
			outputDevice.doorLight(true)
			elevator = requests_clearAtCurrentFloor(elevator)
			timer_start(elevator.config.doorOpenDuration_s)
			SetAllLights(elevator)
			elevator.behaviour = EB_DoorOpen
		}
	default:
		// do nothing
	}

	fmt.Printf("\nNew state:\n")
	elevator_print(elevator)
}

func Fsm_onDoorTimeout() {
	fmt.Printf("\n\n%s()\n", "Fsm_onDoorTimeout")
	elevator_print(elevator)

	switch elevator.behaviour {
	case EB_DoorOpen:
		pair := requests_chooseDirection(elevator)
		elevator.dirn = pair.dirn
		elevator.behaviour = pair.behaviour

		switch elevator.behaviour {
		case EB_DoorOpen:
			timer_start(elevator.config.doorOpenDuration_s)
			elevator = requests_clearAtCurrentFloor(elevator)
			SetAllLights(elevator)

		case EB_Moving, EB_Idle:
			outputDevice.doorLight(false)
			outputDevice.motorDirection(elevator.dirn)
		}
	default:
		// do nothing
	}

	fmt.Printf("\nNew state:\n")
	elevator_print(elevator)
}
