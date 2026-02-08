package elevfsm

import (
	elevio "Driver-go/elevio"

	"elevator/common"
)

func BuildUpdateSnapshot(elevator *Elevator, selfKey string, prevButtons *[common.N_FLOORS][common.N_BUTTONS]bool) common.Snapshot {
	newHallRequests := make([][2]bool, common.N_FLOORS)
	for f := 0; f < common.N_FLOORS; f++ {
		for b := 0; b < 2; b++ {
			if elevator.Requests[f][b] != prevButtons[f][b] {
				newHallRequests[f][b] = prevButtons[f][b]
			} else {
				newHallRequests[f][b] = false
			}
		}
	}
	cabRequests := make([]bool, common.N_FLOORS)
	for f := 0; f < common.N_FLOORS; f++ {
		cabRequests[f] = elevator.Requests[f][elevio.BT_Cab]
	}
	behaviour, direction := CurrentMotionStrings(elevator)
	return common.Snapshot{
		HallRequests: newHallRequests,
		States: map[string]common.ElevState{
			selfKey: {
				Behavior:    behaviour,
				Floor:       elevator.Floor,
				Direction:   direction,
				CabRequests: cabRequests,
			},
		},
	}
}

func BuildServicedSnapshot(elevator *Elevator, selfKey string, floor int,servicedDirection [2]elevio.ButtonType ) common.Snapshot {

	servicedHallRequests := make([][2]bool, common.N_FLOORS)
	for f := 0; f < common.N_FLOORS; f++ {
		for b:=0; b<2; b++ {
			servicedHallRequests[f][b] = true
		}
	}
	for directions := range servicedDirection {
		servicedHallRequests[floor][directions] = false	
	}
	
	
	cabRequests := make([]bool, common.N_FLOORS)
	for f := 0; f < common.N_FLOORS; f++ {
		cabRequests[f] = elevator.Requests[f][elevio.BT_Cab]
	}

	behaviour, direction := CurrentMotionStrings(elevator)
	return common.Snapshot{
		HallRequests: servicedHallRequests,
		States: map[string]common.ElevState{
			selfKey: {
				Behavior:    behaviour,
				Floor:       elevator.Floor,
				Direction:   direction,
				CabRequests: cabRequests,
			},
		},
	}
}

func ServiceLights(output common.ElevOutputDevice, snap common.Snapshot, selfKey string, online bool) {
	if online { //TODO: make not online but different for startup
		SetAllLights(snap, selfKey, &output)
	} else { //on startup make all false to clear any stale lights, then wait for network snapshot to set correct lights

		snap = common.Snapshot{
			HallRequests: make([][2]bool, common.N_FLOORS),
			States: map[string]common.ElevState{
				selfKey: {CabRequests: make([]bool, common.N_FLOORS)},
			},
			Alive: map[string]bool{
				selfKey: true,
			},
		}
		SetAllLights(snap, selfKey, &output)
	}
}

func CountHall(hallTasks [][2]bool) int {
	if hallTasks == nil {
		return 0
	}
	n := 0
	for i := 0; i < len(hallTasks) && i < common.N_FLOORS; i++ {
		if hallTasks[i][0] {
			n++
		}
		if hallTasks[i][1] {
			n++
		}
	}
	return n
}

func CountCabFromSnapshot(snap common.Snapshot, selfKey string) int {
	if snap.States == nil {
		return 0
	}
	st, ok := snap.States[selfKey]
	if !ok || st.CabRequests == nil {
		return 0
	}
	n := 0
	for i := 0; i < len(st.CabRequests) && i < common.N_FLOORS; i++ {
		if st.CabRequests[i] {
			n++
		}
	}
	return n
}

func ApplyAssigner(elevator *Elevator, task common.ElevInput) {
	for floor := 0; floor < common.N_FLOORS; floor++ {
		for btn := 0; btn < 2; btn++ {
			elevator.Requests[floor][btn] = task.HallTask[floor][btn]
		}
	}
}
