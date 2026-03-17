package common

// UpdateKind describes how hall-request information in a snapshot should be
// merged into the world view.
type UpdateKind int

const (
	// UpdateRequests announces new or still-active hall requests and is merged
	// with logical OR.
	UpdateRequests UpdateKind = iota
	// UpdateServiced announces that hall requests have been served and is merged
	// with logical AND so a single false clears the shared hall light.
	UpdateServiced
)

// ElevState is the assigner-facing state of one elevator.
type ElevState struct {
	// Behavior is one of "idle", "moving", or "doorOpen".
	Behavior string `json:"behaviour"`
	// Floor is the last known floor index. The code only publishes defined
	// floors, not "between floors".
	Floor int `json:"floor"`
	// Direction is one of "up", "down", or "stop".
	Direction string `json:"direction"`
	// CabRequests contains the local cab lights that must stay private to this
	// elevator while still being recoverable after restart.
	CabRequests [N_FLOORS]bool `json:"cabRequests"`
}

// Snapshot is the replicated system view exchanged locally and over the
// network.
type Snapshot struct {
	// HallRequests is indexed as [floor][hallDirection], where direction 0 is
	// up and 1 is down.
	HallRequests [N_FLOORS][2]bool `json:"hallRequests"`
	// States holds one ElevState per known elevator key.
	States map[string]ElevState `json:"states"`
	// Alive reports the local liveness decision for each configured elevator.
	Alive map[string]bool `json:"alive"`
	// Coherent is set locally when peers agree on hall requests and on this
	// elevator's published state.
	Coherent bool `json:"coherent,omitempty"`
	// UpdateKind selects OR-merge or AND-merge semantics for hall requests.
	UpdateKind UpdateKind `json:"type"`
}

// NetMsg wraps a Snapshot with sender identity and a monotonically increasing
// per-sender counter.
type NetMsg struct {
	Origin string `json:"origin"`
	// Counter is used to drop stale or duplicate frames from the same origin.
	Counter  uint64   `json:"counter"`
	Snapshot Snapshot `json:"snapshot"`
}

// ElevInput is the hall-task assignment delivered from the external assigner to
// the local FSM thread.
type ElevInput struct {
	HallTask [N_FLOORS][2]bool `json:"HallTask"`
}

// Requests is the controller's internal request matrix indexed as
// [floor][button], where button order is hall-up, hall-down, cab.
type Requests [N_FLOORS][N_BUTTONS]bool
