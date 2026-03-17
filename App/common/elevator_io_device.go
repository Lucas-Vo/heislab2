package common

const (
	// N_FLOORS is the number of floors supported by this hand-in controller.
	N_FLOORS = 4
	// N_BUTTONS is the width of Requests: hall-up, hall-down, and cab.
	N_BUTTONS = 3
)

// ElevInputDevice adapts the raw driver functions to the interface used by the
// local FSM.
type ElevInputDevice struct {
	FloorSensor   func() int
	RequestButton func(int, ButtonType) int
	obstruction   func() int
}

// ElevOutputDevice adapts the raw driver functions to the interface used by
// the local FSM.
type ElevOutputDevice struct {
	FloorIndicator     func(int)
	RequestButtonLight func(int, ButtonType, bool)
	DoorLight          func(bool)
	StopButtonLight    func(bool)
	MotorDirection     func(MotorDirection)
}

// ElevioInit initializes the shared driver connection using the project's
// fixed floor count.
func ElevioInit(addr string) {
	Init(addr, N_FLOORS)
}

// ElevioGetInputDevice returns the simulator input hooks consumed by Elevator.
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

// Obstruction returns the current obstruction-switch value as 0 or 1.
func (d ElevInputDevice) Obstruction() int {
	if d.obstruction == nil {
		return 0
	}
	return d.obstruction()
}

// ElevioGetOutputDevice returns the simulator output hooks consumed by
// Elevator.
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
