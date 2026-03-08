package common

import "maps"

func TrimZeros(b []byte) []byte {
	i := len(b)
	for i > 0 && b[i-1] == 0 {
		i--
	}
	return b[:i]
}

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

func GetHallCalls(in [N_FLOORS][N_BUTTONS]bool) [N_FLOORS][2]bool {
	var out [N_FLOORS][2]bool
	for i, row := range in {
		out[i] = [2]bool{row[0], row[1]}
	}
	return out
}

func GetCabCalls(in [N_FLOORS][N_BUTTONS]bool) [N_FLOORS]bool {
	var out [N_FLOORS]bool
	for i, row := range in {
		out[i] = row[2]
	}
	return out
}

func MergeHallRequests(current, incoming [N_FLOORS][2]bool, kind UpdateKind) [N_FLOORS][2]bool {
	merged := [N_FLOORS][2]bool{}
	for i := range N_FLOORS {
		switch kind {
		case UpdateServiced:
			merged[i][0] = current[i][0] && incoming[i][0]
			merged[i][1] = current[i][1] && incoming[i][1]
		case UpdateRequests:
			merged[i][0] = current[i][0] || incoming[i][0]
			merged[i][1] = current[i][1] || incoming[i][1]
		}
	}
	return merged
}
