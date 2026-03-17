// This file wraps the course driver-go TCP protocol. The public functions panic
// if the connection to the elevator server is lost, because higher layers treat
// simulator loss as a local controller failure rather than recoverable I/O.
package common

import (
	"fmt"
	"net"
	"sync"
	"time"
)

const _pollRate = 20 * time.Millisecond

var _initialized bool = false
var _numFloors int = 4
var _mtx sync.Mutex
var _conn net.Conn

type MotorDirection int

const (
	// MD_Up commands upward motor motion.
	MD_Up MotorDirection = 1
	// MD_Down commands downward motor motion.
	MD_Down MotorDirection = -1
	// MD_Stop commands the motor to stop.
	MD_Stop MotorDirection = 0
)

// ButtonType identifies one physical elevator button column.
type ButtonType int

const (
	// BT_HallUp is the hall call for upward travel.
	BT_HallUp ButtonType = 0
	// BT_HallDown is the hall call for downward travel.
	BT_HallDown ButtonType = 1
	// BT_Cab is the local in-cab destination request.
	BT_Cab ButtonType = 2
)

// ButtonEvent reports one button press edge.
type ButtonEvent struct {
	Floor  int
	Button ButtonType
}

// Init connects to the simulator or hardware server at addr.
//
// Init is effectively single-use for the lifetime of the process. Repeated
// calls print a warning and leave the first connection in place.
func Init(addr string, numFloors int) {
	if _initialized {
		fmt.Println("Driver already initialized!")
		return
	}
	_numFloors = numFloors
	_mtx = sync.Mutex{}
	var err error
	_conn, err = net.Dial("tcp", addr)
	if err != nil {
		panic(err.Error())
	}
	_initialized = true
}

// SetMotorDirection writes the commanded motor direction to the elevator
// server.
func SetMotorDirection(dir MotorDirection) {
	write([4]byte{1, byte(dir), 0, 0})
}

// SetButtonLamp sets one hall or cab button lamp.
func SetButtonLamp(button ButtonType, floor int, value bool) {
	write([4]byte{2, byte(button), byte(floor), toByte(value)})
}

// SetFloorIndicator sets the floor indicator lamp.
func SetFloorIndicator(floor int) {
	write([4]byte{3, byte(floor), 0, 0})
}

// SetDoorOpenLamp controls the door-open lamp used as the door actuator in the
// simulator.
func SetDoorOpenLamp(value bool) {
	write([4]byte{4, toByte(value), 0, 0})
}

// SetStopLamp controls the stop lamp.
func SetStopLamp(value bool) {
	write([4]byte{5, toByte(value), 0, 0})
}

// PollButtons continuously sends button press edges on receiver.
//
// The function never returns and will block if receiver is not serviced.
func PollButtons(receiver chan<- ButtonEvent) {
	prev := make([][3]bool, _numFloors)
	for {
		time.Sleep(_pollRate)
		for f := 0; f < _numFloors; f++ {
			for b := ButtonType(0); b < 3; b++ {
				v := GetButton(b, f)
				if v != prev[f][b] && v != false {
					receiver <- ButtonEvent{f, ButtonType(b)}
				}
				prev[f][b] = v
			}
		}
	}
}

// PollFloorSensor continuously sends defined floor-sensor edges on receiver.
func PollFloorSensor(receiver chan<- int) {
	prev := -1
	for {
		time.Sleep(_pollRate)
		v := GetFloor()
		if v != prev && v != -1 {
			receiver <- v
		}
		prev = v
	}
}

// PollStopButton continuously sends stop-button state changes on receiver.
func PollStopButton(receiver chan<- bool) {
	prev := false
	for {
		time.Sleep(_pollRate)
		v := GetStop()
		if v != prev {
			receiver <- v
		}
		prev = v
	}
}

// GetButton reads the current state of one hall or cab button.
func GetButton(button ButtonType, floor int) bool {
	a := read([4]byte{6, byte(button), byte(floor), 0})
	return toBool(a[1])
}

// GetFloor returns the current floor sensor value, or -1 when the car is
// between floors.
func GetFloor() int {
	a := read([4]byte{7, 0, 0, 0})
	if a[1] != 0 {
		return int(a[2])
	} else {
		return -1
	}
}

// GetStop reports whether the stop button is currently pressed.
func GetStop() bool {
	a := read([4]byte{8, 0, 0, 0})
	return toBool(a[1])
}

// GetObstruction reports whether the obstruction switch is currently active.
func GetObstruction() bool {
	a := read([4]byte{9, 0, 0, 0})
	return toBool(a[1])
}

func read(in [4]byte) [4]byte {
	_mtx.Lock()
	defer _mtx.Unlock()

	_, err := _conn.Write(in[:])
	if err != nil {
		panic("Lost connection to Elevator Server")
	}

	var outFrame [4]byte
	_, err = _conn.Read(outFrame[:])
	if err != nil {
		panic("Lost connection to Elevator Server")
	}

	return outFrame
}

func write(in [4]byte) {
	_mtx.Lock()
	defer _mtx.Unlock()

	_, err := _conn.Write(in[:])
	if err != nil {
		panic("Lost connection to Elevator Server")
	}
}

func toByte(a bool) byte {
	var byteVal byte = 0
	if a {
		byteVal = 1
	}
	return byteVal
}

func toBool(a byte) bool {
	var boolVal bool = false
	if a != 0 {
		boolVal = true
	}
	return boolVal
}
