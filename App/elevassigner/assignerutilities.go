package elevassigner

import (
	"elevator/common"
	"errors"
)

// RemoveStaleStates drops state entries for peers that are not currently alive.
//
// The external hall request assigner should only see elevators that can still
// be assigned hall calls. If the local elevator is already marked dead,
// assignment is aborted instead of asking the assigner to allocate work to an
// unusable node.
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
