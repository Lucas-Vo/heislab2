package elevassigner

import (
	
	. "elevator/common"

)

const HRA_EXECUTABLE = "hall_request_assigner" // Linux only

func RemoveStaleStates(networkSnapshot *NetworkState) {
	for id, alive := range networkSnapshot.Alive {
		if !alive {
			delete(networkSnapshot.States, id)
		}
	}
}