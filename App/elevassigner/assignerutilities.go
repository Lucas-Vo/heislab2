package elevassigner

import (
	"elevator/common"
	"errors"
)

// RemoveStaleStates removes the elevator states for the nodes marked stale.
func RemoveStaleStates(networkSnapshot *common.Snapshot, selfKey string) error {
	err := errors.New("no elevator states marked alive")
	for id, alive := range networkSnapshot.Alive {
		if id == selfKey && !alive {
			return errors.New("local elevator is not alive, stopping assignment")
		}
		if !alive {
			delete(networkSnapshot.States, id)
		} else {
			err = nil
		}
	}
	return err
}
