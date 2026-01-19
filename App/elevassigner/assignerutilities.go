package elevassigner

import (
	. "elevator/common"
)

const HRA_EXECUTABLE = "hall_request_assigner" // Linux only

// RemoveStaleStates removes the elevator states for the nodes that are marked as stale.
// It mutates networkSnapshot by deleting entries from networkSnapshot.States.
func RemoveStaleStates(networkSnapshot *NetworkState) {
	for id, alive := range networkSnapshot.Alive {
		if !alive {
			delete(networkSnapshot.States, id)
		}
	}
}
