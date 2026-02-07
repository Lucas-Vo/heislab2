package elevfsm

import (
	"Driver-go/elevio"
	"elevator/common"
)

// Fsm_clearRequest clears a specific request from the internal FSM queue.
// It does not change motor direction immediately; the FSM will re-evaluate on the next event.
func Fsm_clearRequest(floor int, btn elevio.ButtonType) {
	if floor < 0 || floor >= common.N_FLOORS {
		return
	}
	if btn < 0 || btn >= common.N_BUTTONS {
		return
	}
	elevator.requests[floor][btn] = false
}
