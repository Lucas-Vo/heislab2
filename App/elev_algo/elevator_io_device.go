package elev_algo

import "Driver-go/elevio"

// constants
const (
	N_FLOORS  = 4
	N_BUTTONS = 3
)

// structs
type ElevInputDevice struct {
	FloorSensor   func() int
	RequestButton func(int, elevio.ButtonType) int
	stopButton    func() int
	obstruction   func() int
}

type ElevOutputDevice struct {
	floorIndicator     func(int)
	RequestButtonLight func(int, elevio.ButtonType, bool)
	doorLight          func(bool)
	stopButtonLight    func(bool)
	motorDirection     func(elevio.MotorDirection)
}

func Elevio_init(addr string) {
	elevio.Init(addr, N_FLOORS)
}

func Elevio_getInputDevice() ElevInputDevice {
	return ElevInputDevice{
		FloorSensor: func() int {
			return elevio.GetFloor()
		},
		RequestButton: func(f int, b elevio.ButtonType) int {
			if elevio.GetButton(b, f) {
				return 1
			}
			return 0
		},
		stopButton: func() int {
			if elevio.GetStop() {
				return 1
			}
			return 0
		},
		obstruction: func() int {
			if elevio.GetObstruction() {
				return 1
			}
			return 0
		},
	}
}

func Elevio_getOutputDevice() ElevOutputDevice {
	return ElevOutputDevice{
		floorIndicator: func(floor int) {
			elevio.SetFloorIndicator(floor)
		},
		RequestButtonLight: func(f int, b elevio.ButtonType, v bool) {
			elevio.SetButtonLamp(b, f, v)
		},
		doorLight: func(v bool) {
			elevio.SetDoorOpenLamp(v)
		},
		stopButtonLight: func(v bool) {
			elevio.SetStopLamp(v)
		},
		motorDirection: func(d elevio.MotorDirection) {
			elevio.SetMotorDirection(d)
		},
	}
}

func elevio_dirn_toString(d elevio.MotorDirection) string {
	if d == elevio.MD_Up {
		return "elevio.MD_Up"
	} else if d == elevio.MD_Down {
		return "MD_Down"
	} else if d == elevio.MD_Stop {
		return "MD_Stop"
	} else {
		return "MD_UNDEFINED"
	}
}

func elevio_button_toString(b elevio.ButtonType) string {
	if b == elevio.BT_HallUp {
		return "BT_HallUp"
	} else if b == elevio.BT_HallDown {
		return "BT_HallDown"
	} else if b == elevio.BT_Cab {
		return "BT_Cab"
	} else {
		return "BT_UNDEFINED"
	}
}
