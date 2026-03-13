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

func GetHallCalls(in Requests) [N_FLOORS][2]bool {
	var out [N_FLOORS][2]bool
	for i, row := range in {
		out[i] = [2]bool{row[0], row[1]}
	}
	return out
}

func GetCabCalls(in Requests) [N_FLOORS]bool {
	var out [N_FLOORS]bool
	for i, row := range in {
		out[i] = row[2]
	}
	return out
}
