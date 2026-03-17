package common

import "elevator/elevhw"

type UpdateKind int

const (
	UK_Requests UpdateKind = iota // OR merge
	UK_Serviced                   // AND merge
)

type ElevState struct {
	Behavior    string      `json:"behaviour"`
	Floor       int         `json:"floor"`
	Direction   string      `json:"direction"`
	CabRequests CabRequests `json:"cabRequests"`
}

type Snapshot struct {
	HallRequests HallRequests         `json:"hallRequests"`
	States       map[string]ElevState `json:"states"`
	Alive        map[string]bool      `json:"alive"`
	Coherent     bool                 `json:"coherent,omitempty"`
	UpdateKind   UpdateKind           `json:"type"`
}

type NetMsg struct {
	Origin   string   `json:"origin"`
	Counter  uint64   `json:"counter"`
	Snapshot Snapshot `json:"snapshot"`
}

type Requests [elevhw.N_FLOORS][elevhw.N_BUTTONS]bool
type CabRequests [elevhw.N_FLOORS]bool
type HallRequests [elevhw.N_FLOORS][elevhw.N_BUTTONS - 1]bool

// produced by assigner, consumed by elevator
type HallAssignment [elevhw.N_FLOORS][elevhw.N_BUTTONS - 1]bool
