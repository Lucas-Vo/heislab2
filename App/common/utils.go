package common

import "maps"

func DeepCopySnapshot(ns Snapshot) Snapshot {
	snapshotCopy := Snapshot{
		HallRequests: ns.HallRequests,
		States:       make(map[string]ElevState, len(ns.States)),
		Alive:        make(map[string]bool, len(ns.Alive)),
		Coherent:     ns.Coherent,
		UpdateKind:   ns.UpdateKind,
	}
	maps.Copy(snapshotCopy.States, ns.States)
	maps.Copy(snapshotCopy.Alive, ns.Alive)
	return snapshotCopy
}

func GetHallRequests(in Requests) [N_FLOORS][2]bool {
	var out [N_FLOORS][2]bool
	for i, row := range in {
		out[i] = [2]bool{row[0], row[1]}
	}
	return out
}

func GetCabRequests(in Requests) [N_FLOORS]bool {
	var out [N_FLOORS]bool
	for i, row := range in {
		out[i] = row[2]
	}
	return out
}

func BuildSnapshot(
	selfKey string,
	kind UpdateKind,
	requestsCleared Requests,
	floor int,
	behavior string,
	direction string,
	netRequests Requests,
	localRequests Requests,
) Snapshot {
	outRequests := netRequests
	if kind == UpdateRequests {
		for floor := range N_FLOORS {
			if localRequests[floor][BT_HallUp] {
				outRequests[floor][BT_HallUp] = true
			}
			if localRequests[floor][BT_HallDown] {
				outRequests[floor][BT_HallDown] = true
			}
		}
	}
	if kind == UpdateServiced {
		for floor := range N_FLOORS {
			if requestsCleared[floor][BT_HallUp] {
				outRequests[floor][BT_HallUp] = false
			}
			if requestsCleared[floor][BT_HallDown] {
				outRequests[floor][BT_HallDown] = false
			}
		}
	}

	return Snapshot{
		HallRequests: GetHallRequests(outRequests),
		States: map[string]ElevState{
			selfKey: {
				Behavior:    behavior,
				Floor:       floor,
				Direction:   direction,
				CabRequests: GetCabRequests(localRequests),
			},
		},
		UpdateKind: kind,
	}
}
