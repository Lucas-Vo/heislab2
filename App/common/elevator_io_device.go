package common

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
	FloorIndicator     func(int)
	RequestButtonLight func(int, elevio.ButtonType, bool)
	DoorLight          func(bool)
	stopButtonLight    func(bool)
	MotorDirection     func(elevio.MotorDirection)
}

func ElevioInit(addr string) {
	elevio.Init(addr, N_FLOORS)
}

func ElevioGetInputDevice() ElevInputDevice {
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

func ElevioGetOutputDevice() ElevOutputDevice {
	return ElevOutputDevice{
		FloorIndicator: func(floor int) {
			elevio.SetFloorIndicator(floor)
		},
		RequestButtonLight: func(f int, b elevio.ButtonType, v bool) {
			elevio.SetButtonLamp(b, f, v)
		},
		DoorLight: func(v bool) {
			elevio.SetDoorOpenLamp(v)
		},
		stopButtonLight: func(v bool) {
			elevio.SetStopLamp(v)
		},
		MotorDirection: func(d elevio.MotorDirection) {
			elevio.SetMotorDirection(d)
		},
	}
}

func ElevioDirnToString(d elevio.MotorDirection) string {
	switch d {
	case elevio.MD_Up:
		return "MD_Up"
	case elevio.MD_Down:
		return "MD_Down"
	case elevio.MD_Stop:
		return "MD_Stop"
	default:
		return "MD_UNDEFINED"
	}
}

func ElevioButtonToString(b elevio.ButtonType) string {
	switch b {
	case elevio.BT_HallUp:
		return "BT_HallUp"
	case elevio.BT_HallDown:
		return "BT_HallDown"
	case elevio.BT_Cab:
		return "BT_Cab"
	default:
		return "BT_UNDEFINED"
	}
}
