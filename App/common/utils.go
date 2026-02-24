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
		UpdateKind:   ns.UpdateKind,
	}
	maps.Copy(snapshotCopy.States, ns.States)
	maps.Copy(snapshotCopy.Alive, ns.Alive)
	return snapshotCopy
}

func GetHallSlice(in [N_FLOORS][N_BUTTONS]bool) [N_FLOORS][2]bool {
	var out [N_FLOORS][2]bool
	for i, row := range in {
		out[i] = [2]bool{row[0], row[1]}
	}
	return out
}

func GetCabSlice(in [N_FLOORS][N_BUTTONS]bool) [N_FLOORS]bool {
	var out [N_FLOORS]bool
	for i, row := range in {
		out[i] = row[2]
	}
	return out
}
