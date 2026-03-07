package common

type UpdateKind int

const (
	UpdateRequests UpdateKind = iota // OR merge
	UpdateServiced                   // AND merge
)

type ElevState struct {
	Behavior    string         `json:"behaviour"`
	Floor       int            `json:"floor"`
	Direction   string         `json:"direction"`
	CabRequests [N_FLOORS]bool `json:"cabRequests"`
}

type Snapshot struct {
	HallRequests [N_FLOORS][2]bool    `json:"hallRequests"`
	States       map[string]ElevState `json:"states"`
	Alive        map[string]bool      `json:"alive"`
	Coherent     bool                 `json:"coherent,omitempty"`
	UpdateKind   UpdateKind           `json:"type"`
}

type ElevInput struct {
	HallTask     [N_FLOORS][2]bool `json:"HallTask"`
	HallRequests [N_FLOORS][2]bool `json:"hallRequests"`
}

type HRAOutput struct {
	HallTasks []ElevInput `json:"HallTasks"`
}
