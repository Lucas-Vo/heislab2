package elevfsm

import (
	"Driver-go/elevio"
	"elevator/common"
	"log"
)

func FsmInit(e *Elevator, out *common.ElevOutputDevice) (Elevator, common.ElevOutputDevice) {
	*e = elevator_uninitialized()

	ConLoad("elevator.con",
		ConVal("doorOpenDuration_s", &e.Config.DoorOpenDuration_s, "%f"),
		ConEnum("clearRequestVariant", &e.Config.ClearRequestVariant,
			ConMatch("CV_All", CV_All),
			ConMatch("CV_InDirn", CV_InDirn),
		),
	)

	*out = common.ElevOutputDevice{}
	return *e, *out
}

func SetAllLights(snap common.Snapshot, selfKey string, out *common.ElevOutputDevice) {
	for floor := 0; floor < common.N_FLOORS; floor++ {
		for btn := 0; btn < 2; btn++ {
			out.RequestButtonLight(floor, elevio.ButtonType(btn), snap.HallRequests[floor][btn])
		}
		out.RequestButtonLight(floor, elevio.ButtonType(elevio.BT_Cab), snap.States[selfKey].CabRequests[floor])
	}
}

func FsmOnInitBetweenFloors(e *Elevator, out *common.ElevOutputDevice) {
	out.MotorDirection(elevio.MD_Down)
	e.Dirn = elevio.MD_Down
	e.Behaviour = EB_Moving
}

func FsmOnRequestButtonPress(e *Elevator, out *common.ElevOutputDevice, btnFloor int, btnType elevio.ButtonType, online bool) bool {
	request_acknowledged := false
	log.Printf("\n\n%s(%d, %s)\n", "FsmOnRequestButtonPress", btnFloor, common.ElevioButtonToString(btnType))
	elevator_print(*e)

	switch e.Behaviour {

	case EB_DoorOpen:
		if requests_shouldClearImmediately(*e, btnFloor, btnType) != 0 {
			Timer_start(e.Config.DoorOpenDuration_s)
		} else {
			if !online || btnType == elevio.BT_Cab {
				e.Requests[btnFloor][btnType] = true
			}
			request_acknowledged = true
		}

	case EB_Moving:
		if !online || btnType == elevio.BT_Cab {
			e.Requests[btnFloor][btnType] = true
		}
		request_acknowledged = true

	case EB_Idle:
		if !online || btnType == elevio.BT_Cab {
			e.Requests[btnFloor][btnType] = true
		}
		request_acknowledged = true

		pair := requests_chooseDirection(*e)
		e.Dirn = pair.dirn
		e.Behaviour = pair.behaviour

		switch pair.behaviour {

		case EB_DoorOpen:
			out.DoorLight(true)
			Timer_start(e.Config.DoorOpenDuration_s)
			*e, _ = requests_clearAtCurrentFloor(*e, online)

		case EB_Moving:
			out.MotorDirection(e.Dirn)

		case EB_Idle:
		}
	}

	log.Printf("\nNew state:\n")
	elevator_print(*e)

	return request_acknowledged
}

func FsmOnFloorArrival(e *Elevator, out *common.ElevOutputDevice, newFloor int, online bool) bool {
	request_serviced := false
	log.Printf("\n\n%s(%d)\n", "FsmOnFloorArrival", newFloor)
	elevator_print(*e)

	e.Floor = newFloor
	out.FloorIndicator(e.Floor)

	switch e.Behaviour {

	case EB_Moving:
		if requests_shouldStop(*e) != 0 {
			out.MotorDirection(elevio.MD_Stop)
			out.DoorLight(true)
			*e, request_serviced = requests_clearAtCurrentFloor(*e, online)
			Timer_start(e.Config.DoorOpenDuration_s)
			e.Behaviour = EB_DoorOpen
		}
	}

	log.Printf("\nNew state:\n")
	elevator_print(*e)
	return request_serviced
}

func FsmOnDoorTimeout(e *Elevator, out *common.ElevOutputDevice) bool {
	request_serviced := false
	log.Printf("\n\n%s()\n", "FsmOnDoorTimeout")
	elevator_print(*e)

	switch e.Behaviour {

	case EB_DoorOpen:
		pair := requests_chooseDirection(*e) //TODO add this into a thing so that it constantly updates direction and runs elevator even if something else changes elevator struct
		e.Dirn = pair.dirn
		e.Behaviour = pair.behaviour

		switch e.Behaviour {

		case EB_DoorOpen:
			Timer_start(e.Config.DoorOpenDuration_s)
			*e, request_serviced = requests_clearAtCurrentFloor(*e, false)

		case EB_Moving, EB_Idle:
			out.DoorLight(false)
			out.MotorDirection(e.Dirn)
		}
	}

	log.Printf("\nNew state:\n")
	elevator_print(*e)
	return request_serviced
}
