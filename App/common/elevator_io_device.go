// Based off of https://github.com/TTK4145/Project-resources/tree/master/elev_algo

package common

const (
	N_FLOORS  = 4
	N_BUTTONS = 3
)

type ElevInputDevice struct {
	FloorSensor   func() int
	RequestButton func(int, ButtonType) int
	obstruction   func() int
}

type ElevOutputDevice struct {
	FloorIndicator     func(int)
	RequestButtonLight func(int, ButtonType, bool)
	DoorLight          func(bool)
	StopButtonLight    func(bool)
	MotorDirection     func(MotorDirection)
}

func InitElevio(addr string) {
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
		obstruction: func() int {
			if GetObstruction() {
				return 1
			}
			return 0
		},
	}
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
		StopButtonLight: func(v bool) {
			SetStopLamp(v)
		},
		DoorLight: func(v bool) {
			SetDoorOpenLamp(v)
		},
		MotorDirection: func(d MotorDirection) {
			SetMotorDirection(d)
		},
	}
}
