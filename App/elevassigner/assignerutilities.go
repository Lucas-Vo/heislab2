package elevassigner

import (
	. "elevator/common"
	"errors"
)

const HRA_EXECUTABLE = "hall_request_assigner" // Linux only

// RemoveStaleStates removes the elevator states for the nodes that are marked as stale.
// It mutates networkSnapshot by deleting entries from networkSnapshot.States.
func RemoveStaleStates(networkSnapshot *NetworkState, selfKey string) error {
	var removeStaleStatesErr error = errors.New("no elevator states are marked alive")
	for id, alive := range networkSnapshot.Alive {
		if id == selfKey && !alive {
			return errors.New("local elevator is not alive, stopping assignment")
		}
		if !alive {
			delete(networkSnapshot.States, id)
		} else {
			removeStaleStatesErr = nil
		}
	}
	return removeStaleStatesErr
}
