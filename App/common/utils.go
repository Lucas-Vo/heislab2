package common

import "maps"

// DeepCopySnapshot returns a copy of snap with duplicated maps so callers can
// mutate the result without aliasing the original.
func DeepCopySnapshot(snap Snapshot) Snapshot {
	snapshotCopy := Snapshot{
		HallRequests: snap.HallRequests,
		States:       make(map[string]ElevState, len(snap.States)),
		Alive:        make(map[string]bool, len(snap.Alive)),
		Coherent:     snap.Coherent,
		UpdateKind:   snap.UpdateKind,
	}
	maps.Copy(snapshotCopy.States, snap.States)
	maps.Copy(snapshotCopy.Alive, snap.Alive)
	return snapshotCopy
}

// GetHallRequests extracts the shared hall-call portion of a full request
// matrix.
func GetHallRequests(in Requests) [N_FLOORS][2]bool {
	var out [N_FLOORS][2]bool
	for i, row := range in {
		out[i] = [2]bool{row[0], row[1]}
	}
	return out
}

// GetCabRequests extracts the cab-call portion of a full request matrix.
func GetCabRequests(in Requests) [N_FLOORS]bool {
	var out [N_FLOORS]bool
	for i, row := range in {
		out[i] = row[2]
	}
	return out
}

// BuildSnapshot constructs the local elevator's outbound snapshot.
//
// Only hall requests participate in distributed merge logic. Cab requests stay
// local to the elevator state entry so they can be recovered by peers after a
// restart without becoming shared hall calls.
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
		// New local hall calls are merged into the replicated hall view.
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
		// A serviced update clears only the hall calls that were just completed.
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
