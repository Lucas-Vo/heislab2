package elevfsm

import (
	"Driver-go/elevio"
	"elevator/common"
	"log"
)

var elevator Elevator
var outputDevice common.ElevOutputDevice

// NEW: these are the lamp states we want to show.
// They are driven by:
// - HallRequests from network snapshots
// - CabRequests (for self) from network snapshots (and/or local updates via glue snapshots)
var hallLamp [][2]bool
var cabLamp []bool

func Fsm_init() {
	elevator = elevator_uninitialized()

	ConLoad("elevator.con",
		ConVal("doorOpenDuration_s", &elevator.config.doorOpenDuration_s, "%f"),
		ConEnum("clearRequestVariant", &elevator.config.clearRequestVariant,
			ConMatch("CV_All", CV_All),
			ConMatch("CV_InDirn", CV_InDirn),
		),
	)

	outputDevice = common.ElevioGetOutputDevice()

	// Init lamp buffers
	hallLamp = make([][2]bool, common.N_FLOORS)
	cabLamp = make([]bool, common.N_FLOORS)

	// Clear lamps at init
	SetAllLights(elevator)
}

// UPDATED: SetAllLights no longer uses the FSM's internal request matrix for lamps.
// It uses hallLamp/cabLamp (network/glue driven) so hall lamps reflect building-wide HallRequests.
func SetAllLights(snap Snapshot,selfKey string) {
	cabLamp = snap.States[selfKey].CabRequests
	hallLamp = snap.HallRequests


	for floor := range common.N_FLOORS {
		outputDevice.RequestButtonLight(floor, elevio.BT_HallUp, hallLamp[floor][0])
		outputDevice.RequestButtonLight(floor, elevio.BT_HallDown, hallLamp[floor][1])
		outputDevice.RequestButtonLight(floor, elevio.BT_Cab, cabLamp[floor])
	}
}

func Fsm_onInitBetweenFloors() {
	outputDevice.MotorDirection(elevio.MD_Down)
	elevator.dirn = elevio.MD_Down
	elevator.behaviour = EB_Moving
}

func Fsm_onRequestButtonPress(btn_floor int, btn_type elevio.ButtonType) {
	log.Printf("FSM: request press floor=%d btn=%s (before floor=%d dir=%s behav=%s reqs=%d)",
		btn_floor,
		common.ElevioButtonToString(btn_type),
		elevator.floor,
		common.ElevioDirnToString(elevator.dirn),
		ebToString(elevator.behaviour),
		countRequests(elevator),
	)

	switch elevator.behaviour {
	case EB_DoorOpen:
		if requests_shouldClearImmediately(elevator, btn_floor, btn_type) != 0 {
			Timer_start(elevator.config.doorOpenDuration_s)
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
			outputDevice.DoorLight(true)
			Timer_start(elevator.config.doorOpenDuration_s)
			elevator = requests_clearAtCurrentFloor(elevator)

		case EB_Moving:
			outputDevice.MotorDirection(elevator.dirn)

		case EB_Idle:
			// do nothing
		}
	}

	// Lamps are driven by network/glue state; keep applying current lamp buffers.
	SetAllLights(elevator)
	log.Printf("FSM: request handled (after floor=%d dir=%s behav=%s reqs=%d)",
		elevator.floor,
		common.ElevioDirnToString(elevator.dirn),
		ebToString(elevator.behaviour),
		countRequests(elevator),
	)
}

func Fsm_onFloorArrival(newFloor int) {
	log.Printf("FSM: floor arrival %d (before floor=%d dir=%s behav=%s reqs=%d)",
		newFloor,
		elevator.floor,
		common.ElevioDirnToString(elevator.dirn),
		ebToString(elevator.behaviour),
		countRequests(elevator),
	)

	elevator.floor = newFloor
	outputDevice.FloorIndicator(elevator.floor)

	switch elevator.behaviour {
	case EB_Moving:
		if requests_shouldStop(elevator) != 0 {
			outputDevice.MotorDirection(elevio.MD_Stop)
			outputDevice.DoorLight(true)
			elevator = requests_clearAtCurrentFloor(elevator)
			Timer_start(elevator.config.doorOpenDuration_s)
			SetAllLights(elevator)
			elevator.behaviour = EB_DoorOpen
		}
	default:
		// do nothing
	}
	log.Printf("FSM: floor arrival handled (after floor=%d dir=%s behav=%s reqs=%d)",
		elevator.floor,
		common.ElevioDirnToString(elevator.dirn),
		ebToString(elevator.behaviour),
		countRequests(elevator),
	)
}

func Fsm_onDoorTimeout() {
	log.Printf("FSM: door timeout (before floor=%d dir=%s behav=%s reqs=%d)",
		elevator.floor,
		common.ElevioDirnToString(elevator.dirn),
		ebToString(elevator.behaviour),
		countRequests(elevator),
	)

	switch elevator.behaviour {
	case EB_DoorOpen:
		pair := requests_chooseDirection(elevator)
		elevator.dirn = pair.dirn
		elevator.behaviour = pair.behaviour

		switch elevator.behaviour {
		case EB_DoorOpen:
			Timer_start(elevator.config.doorOpenDuration_s)
			elevator = requests_clearAtCurrentFloor(elevator)
			SetAllLights(elevator)

		case EB_Moving, EB_Idle:
			outputDevice.DoorLight(false)
			outputDevice.MotorDirection(elevator.dirn)
		}
	default:
		// do nothing
	}
	log.Printf("FSM: door timeout handled (after floor=%d dir=%s behav=%s reqs=%d)",
		elevator.floor,
		common.ElevioDirnToString(elevator.dirn),
		ebToString(elevator.behaviour),
		countRequests(elevator),
	)
}

func countRequests(e Elevator) int {
	n := 0
	for f := 0; f < common.N_FLOORS; f++ {
		for b := 0; b < common.N_BUTTONS; b++ {
			if e.requests[f][b] {
				n++
			}
		}
	}
	return n
}
