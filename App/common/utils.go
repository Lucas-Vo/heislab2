package common

import (
	"elevator/elevhw"
	"errors"
	"maps"
)

func BuildSnapshot(
	selfKey string,
	kind UpdateKind,
	requestsCleared Requests,
	floor int,
	behavior string,
	dirn string,
	netRequests Requests,
	localRequests Requests,
) Snapshot {
	outRequests := netRequests
	if kind == UK_Requests {
		for floor := range elevhw.N_FLOORS {
			if localRequests[floor][elevhw.BT_HallUp] {
				outRequests[floor][elevhw.BT_HallUp] = true
			}
			if localRequests[floor][elevhw.BT_HallDown] {
				outRequests[floor][elevhw.BT_HallDown] = true
			}
		}
	}
	if kind == UK_Serviced {
		for floor := range elevhw.N_FLOORS {
			if requestsCleared[floor][elevhw.BT_HallUp] {
				outRequests[floor][elevhw.BT_HallUp] = false
			}
			if requestsCleared[floor][elevhw.BT_HallDown] {
				outRequests[floor][elevhw.BT_HallDown] = false
			}
		}
	}

	return Snapshot{
		HallRequests: GetHallRequests(outRequests),
		States: map[string]ElevState{
			selfKey: {
				Behavior:    behavior,
				Floor:       floor,
				Direction:   dirn,
				CabRequests: GetCabRequests(localRequests),
			},
		},
		UpdateKind: kind,
	}
}

// removes the elevator states for the nodes marked dead.
func RemoveDeadStates(networkSnapshot *Snapshot, selfKey string) error {
	err := errors.New("no elevator states marked alive")
	for key, alive := range networkSnapshot.Alive {
		if key == selfKey && !alive {
			return errors.New("local elevator is not alive, stopping assignment")
		}
		if !alive || networkSnapshot.States[key].Floor == -1 {
			delete(networkSnapshot.States, key)
		} else {
			err = nil
		}
	}
	return err
}

func DeepCopySnapshot(snapshot Snapshot) Snapshot {
	snapshotCopy := Snapshot{
		HallRequests: snapshot.HallRequests,
		States:       make(map[string]ElevState, len(snapshot.States)),
		Alive:        make(map[string]bool, len(snapshot.Alive)),
		Coherent:     snapshot.Coherent,
		UpdateKind:   snapshot.UpdateKind,
	}
	maps.Copy(snapshotCopy.States, snapshot.States)
	maps.Copy(snapshotCopy.Alive, snapshot.Alive)
	return snapshotCopy
}

func GetHallRequests(in Requests) HallRequests {
	var out HallRequests
	for i, row := range in {
		out[i] = [2]bool{row[0], row[1]}
	}
	return out
}

func GetCabRequests(in Requests) CabRequests {
	var out CabRequests
	for i, row := range in {
		out[i] = row[2]
	}
	return out
}
