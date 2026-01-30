package elevfsm

import (
	"Driver-go/elevio"
	"elevator/common"
	"fmt"
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

// NEW: Call this whenever you want lamps to reflect a NetworkState.
// - hall lamps: ns.HallRequests
// - cab lamps:  ns.States[selfKey].CabRequests
func SetAllRequestLightsFromNetworkState(ns common.NetworkState, selfKey string) {
	// Update hall lamp buffer if present
	if ns.HallRequests != nil {
		if hallLamp == nil || len(hallLamp) != common.N_FLOORS {
			hallLamp = make([][2]bool, common.N_FLOORS)
		}

		n := len(ns.HallRequests)
		if n > common.N_FLOORS {
			n = common.N_FLOORS
		}
		for f := 0; f < n; f++ {
			hallLamp[f] = ns.HallRequests[f]
		}
		for f := n; f < common.N_FLOORS; f++ {
			hallLamp[f] = [2]bool{false, false}
		}
	}

	// Update cab lamp buffer for self if present
	if ns.States != nil {
		if st, ok := ns.States[selfKey]; ok && st.CabRequests != nil {
			if cabLamp == nil || len(cabLamp) != common.N_FLOORS {
				cabLamp = make([]bool, common.N_FLOORS)
			}

			n := len(st.CabRequests)
			if n > common.N_FLOORS {
				n = common.N_FLOORS
			}
			for f := 0; f < n; f++ {
				cabLamp[f] = st.CabRequests[f]
			}
			for f := n; f < common.N_FLOORS; f++ {
				cabLamp[f] = false
			}
		}
	}

	// Apply to hardware
	SetAllLights(elevator)
}

// UPDATED: SetAllLights no longer uses the FSM's internal request matrix for lamps.
// It uses hallLamp/cabLamp (network/glue driven) so hall lamps reflect building-wide HallRequests.
func SetAllLights(_ Elevator) {
	if hallLamp == nil || len(hallLamp) != common.N_FLOORS {
		hallLamp = make([][2]bool, common.N_FLOORS)
	}
	if cabLamp == nil || len(cabLamp) != common.N_FLOORS {
		cabLamp = make([]bool, common.N_FLOORS)
	}

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
	fmt.Printf("\n\n%s(%d, %s)\n", "Fsm_onRequestButtonPress", btn_floor, common.ElevioButtonToString(btn_type))
	elevator_print(elevator)

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

	fmt.Printf("\nNew state:\n")
	elevator_print(elevator)
}

func Fsm_onFloorArrival(newFloor int) {
	fmt.Printf("\n\n%s(%d)\n", "Fsm_onFloorArrival", newFloor)
	elevator_print(elevator)

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

	fmt.Printf("\nNew state:\n")
	elevator_print(elevator)
}
