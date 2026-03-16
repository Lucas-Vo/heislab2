package common

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

type Requests [N_FLOORS][N_BUTTONS]bool
type CabRequests [N_FLOORS]bool
type HallRequests [N_FLOORS][N_BUTTONS - 1]bool

type HallAssignment [N_FLOORS][N_BUTTONS - 1]bool
