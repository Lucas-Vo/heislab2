// Package elevfsm implements the local elevator controller.
//
// Elevator owns the local state machine: movement, stopping policy, door timing,
// obstruction handling, and direction-specific request clearing. Synchronizer
// mediates between that local controller and the distributed world view by
// deciding which hall and cab requests should be injected into the FSM.
//
// The package assumes a single goroutine owns each Elevator and Synchronizer
// instance. Callers are expected to serialize all access through that owner.
package elevfsm
