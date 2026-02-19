package common

// constants
const (
	N_FLOORS  = 4
	N_BUTTONS = 3
)

// structs
type ElevInputDevice struct {
	FloorSensor   func() int
	RequestButton func(int, ButtonType) int
	stopButton    func() int
	obstruction   func() int
}

type ElevOutputDevice struct {
	FloorIndicator     func(int)
	RequestButtonLight func(int, ButtonType, bool)
	DoorLight          func(bool)
	stopButtonLight    func(bool)
	MotorDirection     func(MotorDirection)
}

func ElevioInit(addr string) {
	Init(addr, N_FLOORS)
}

func ElevioGetInputDevice() ElevInputDevice {
	return ElevInputDevice{
		FloorSensor: func() int {
			return GetFloor()
		},
		RequestButton: func(f int, b ButtonType) int {
			if GetButton(b, f) {
				return 1
			}
			return 0
		},
		stopButton: func() int {
			if GetStop() {
				return 1
			}
			return 0
		},
		obstruction: func() int {
			if GetObstruction() {
				return 1
			}
			return 0
		},
	}
}

func (d ElevInputDevice) StopButton() int {
	if d.stopButton == nil {
		return 0
	}
	return d.stopButton()
}

func (d ElevInputDevice) Obstruction() int {
	if d.obstruction == nil {
		return 0
	}
	return d.obstruction()
}

func ElevioGetOutputDevice() ElevOutputDevice {
	return ElevOutputDevice{
		FloorIndicator: func(floor int) {
			SetFloorIndicator(floor)
		},
		RequestButtonLight: func(f int, b ButtonType, v bool) {
			SetButtonLamp(b, f, v)
		},
		DoorLight: func(v bool) {
			SetDoorOpenLamp(v)
		},
		stopButtonLight: func(v bool) {
			SetStopLamp(v)
		},
		MotorDirection: func(d MotorDirection) {
			SetMotorDirection(d)
		},
	}
}

func ElevioDirnToString(d MotorDirection) string {
	switch d {
	case MD_Up:
		return "MD_Up"
	case MD_Down:
		return "MD_Down"
	case MD_Stop:
		return "MD_Stop"
	default:
		return "MD_UNDEFINED"
	}
}

func ElevioButtonToString(b ButtonType) string {
	switch b {
	case BT_HallUp:
		return "BT_HallUp"
	case BT_HallDown:
		return "BT_HallDown"
	case BT_Cab:
		return "BT_Cab"
	default:
		return "BT_UNDEFINED"
	}
}
