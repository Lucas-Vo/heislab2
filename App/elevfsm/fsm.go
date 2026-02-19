package elevfsm

import (
	"elevator/common"
	"log"
)

var outputDevice common.ElevOutputDevice
var hallLamp [][2]bool
var cabLamp []bool

func Fsm_init() (elevator *Elevator) {
	e := new(Elevator)
	*e = elevator_uninitialized()

	ConLoad("elevator.con",
		ConVal("doorOpenDuration_s", &e.config.doorOpenDuration_s, "%f"),
		ConEnum("clearRequestVariant", &e.config.clearRequestVariant,
			ConMatch("CV_All", CV_All),
			ConMatch("CV_InDirn", CV_InDirn),
		),
	)

	outputDevice = common.ElevioGetOutputDevice()

	// Init lamp buffers
	hallLamp = make([][2]bool, common.N_FLOORS)
	cabLamp = make([]bool, common.N_FLOORS)

	// Clear lamps at init
	SetAllLights(*e)
	return e
}

func SetAllRequestLightsFromSnapshot(e *Elevator, ns common.Snapshot, selfKey string) {
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
	SetAllLights(*e)
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
		outputDevice.RequestButtonLight(floor, common.BT_HallUp, hallLamp[floor][0])
		outputDevice.RequestButtonLight(floor, common.BT_HallDown, hallLamp[floor][1])
		outputDevice.RequestButtonLight(floor, common.BT_Cab, cabLamp[floor])
	}
}

func Fsm_onInitBetweenFloors(e *Elevator) {
	outputDevice.MotorDirection(common.MD_Down)
	e.dirn = common.MD_Down
	e.behaviour = EB_Moving
}

func Fsm_onRequestButtonPress(e *Elevator, btn_floor int, btn_type common.ButtonType) {
	log.Printf("FSM: request press floor=%d btn=%s (before floor=%d dir=%s behav=%s)",
		btn_floor,
		common.ElevioButtonToString(btn_type),
		e.floor,
		common.ElevioDirnToString(e.dirn),
		ebToString(e.behaviour),
	)
	switch e.behaviour {
	case EB_DoorOpen:
		if requests_shouldClearImmediately(*e, btn_floor, btn_type) != 0 {
			Timer_start(e.config.doorOpenDuration_s)
		} else {
			e.requests[btn_floor][btn_type] = true
		}

	case EB_Moving:
		
		e.requests[btn_floor][btn_type] = true

	case EB_Idle:
		e.requests[btn_floor][btn_type] = true
		pair := requests_chooseDirection(*e)
		e.dirn = pair.dirn
		e.behaviour = pair.behaviour

		switch pair.behaviour {
		case EB_DoorOpen:
			outputDevice.DoorLight(true)
			Timer_start(e.config.doorOpenDuration_s)
			*e = requests_clearAtCurrentFloor(*e)

		case EB_Moving:
			outputDevice.MotorDirection(e.dirn)

		case EB_Idle:
			// do nothing
		}
	}

	// Lamps are driven by network/glue state; keep applying current lamp buffers.
	SetAllLights(*e)
}

func Fsm_onFloorArrival(e *Elevator, newFloor int) {
	log.Printf("FSM: floor arrival %d (before floor=%d dir=%s behav=%s)",
		newFloor,
		e.floor,
		common.ElevioDirnToString(e.dirn),
		ebToString(e.behaviour),
	)

	e.floor = newFloor
	outputDevice.FloorIndicator(e.floor)

	switch e.behaviour {
	case EB_Moving:
		if requests_shouldStop(*e) != 0 {
			outputDevice.MotorDirection(common.MD_Stop)
			outputDevice.DoorLight(true)
			*e = requests_clearAtCurrentFloor(*e)
			Timer_start(e.config.doorOpenDuration_s)
			SetAllLights(*e)
			e.behaviour = EB_DoorOpen
		}
	default:
		// do nothing
	}
	log.Printf("FSM: floor arrival handled (after floor=%d dir=%s behav=%s)",
		e.floor,
		common.ElevioDirnToString(e.dirn),
		ebToString(e.behaviour),
	)
}

func Fsm_onDoorTimeout(e *Elevator) {
	log.Printf("FSM: door timeout (before floor=%d dir=%s behav=%s)",
		e.floor,
		common.ElevioDirnToString(e.dirn),
		ebToString(e.behaviour),
	)

	switch e.behaviour {
	case EB_DoorOpen:
		pair := requests_chooseDirection(*e)
		e.dirn = pair.dirn
		e.behaviour = pair.behaviour

		switch e.behaviour {
		case EB_DoorOpen:
			Timer_start(e.config.doorOpenDuration_s)
			*e = requests_clearAtCurrentFloor(*e)
			SetAllLights(*e)

		case EB_Moving, EB_Idle:
			outputDevice.DoorLight(false)
			outputDevice.MotorDirection(e.dirn)
		}
	default:
		// do nothing
	}
	log.Printf("FSM: door timeout handled (after floor=%d dir=%s behav=%s)",
		e.floor,
		common.ElevioDirnToString(e.dirn),
		ebToString(e.behaviour),
	)
}

func CurrentBehaviour(e *Elevator) ElevatorBehaviour {
	return e.behaviour
}

func CurrentDirection(e *Elevator) common.MotorDirection {
	return e.dirn
}

func DoorOpenDuration(e *Elevator) float64 {
	return e.config.doorOpenDuration_s
}

func CurrentMotionStrings(e *Elevator) (behavior string, direction string) {
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
