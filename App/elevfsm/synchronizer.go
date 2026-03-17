package elevfsm

import (
	"elevator/common"
	"time"
)

// Synchronizer mediates between the local FSM and the replicated network view.
//
// It keeps track of which requests are known locally, which requests are known
// from the network, and which hall calls are currently assigned to this
// elevator. Synchronizer is not concurrency-safe; elevatorThread is expected to
// own it.
type Synchronizer struct {
	selfKey string

	initFromNetwork bool
	hasAlivePeer    bool
	coherent        bool

	// assignedHall is the latest hall-task matrix received from the external
	// assigner.
	assignedHall [common.N_FLOORS][2]bool
	// netRequests mirrors the currently published world view.
	netRequests common.Requests
	// localRequests is the local controller's desired request set.
	localRequests common.Requests
	// deliveredRequests prevents re-sending already injected requests to the
	// Elevator on every polling tick.
	deliveredRequests common.Requests
}

// NewFsmSync returns a Synchronizer for the local elevator identified by
// config.SelfKey.
func NewFsmSync(config common.Config) *Synchronizer {
	return &Synchronizer{selfKey: config.SelfKey}
}

// HandleNetworkSnapshot imports the latest published world view.
//
// Hall requests are always taken from the shared snapshot. Cab requests are only
// recovered from the local elevator's own state entry, which lets another
// elevator hand back our cab lights after a restart without making them global.
func (sync *Synchronizer) HandleNetworkSnapshot(snapshot common.Snapshot, now time.Time) {
	sync.hasAlivePeer = false
	sync.coherent = snapshot.Coherent
	for key, alive := range snapshot.Alive {
		if key != sync.selfKey && alive {
			if _, hasState := snapshot.States[key]; hasState {
				sync.hasAlivePeer = true
				break
			}
		}
	}
	// Hall calls are shared state; cab calls remain local and are recovered only
	// from this elevator's own state entry.
	for floor := range common.N_FLOORS {
		sync.netRequests[floor][0] = snapshot.HallRequests[floor][0]
		sync.netRequests[floor][1] = snapshot.HallRequests[floor][1]
		sync.netRequests[floor][common.BT_Cab] = false
	}
	if state, ok := snapshot.States[sync.selfKey]; ok {
		sync.initFromNetwork = true
		for floor := 0; floor < common.N_FLOORS && floor < len(state.CabRequests); floor++ {
			sync.netRequests[floor][common.BT_Cab] = state.CabRequests[floor]
		}
	}

	// Any request missing from the network view is considered cleared. Cab calls
	// that were recovered from the network are kept latched locally.
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if sync.netRequests[floor][button] {
				if button == common.BT_Cab {
					sync.localRequests[floor][button] = true
				}
				continue
			}
			sync.localRequests[floor][button] = false
			sync.deliveredRequests[floor][button] = false
		}
	}
}

// HandleAssignerTask records a new hall-task assignment and returns hall calls
// that must be revoked from the local Elevator because another node took over
// responsibility for them.
func (sync *Synchronizer) HandleAssignerTask(task common.ElevInput) (toRevoke common.Requests) {
	previousAssignment := sync.assignedHall
	sync.assignedHall = task.HallTask

	for floor := range previousAssignment {
		if previousAssignment[floor][0] && !sync.assignedHall[floor][0] {
			sync.localRequests[floor][common.BT_HallUp] = false
			sync.deliveredRequests[floor][common.BT_HallUp] = false
			toRevoke[floor][common.BT_HallUp] = true
		}
		if previousAssignment[floor][1] && !sync.assignedHall[floor][1] {
			sync.localRequests[floor][common.BT_HallDown] = false
			sync.deliveredRequests[floor][common.BT_HallDown] = false
			toRevoke[floor][common.BT_HallDown] = true
		}
	}
	return toRevoke
}

// HandleButtonPresses latches newly observed button presses.
//
// Cab calls are immediately eligible for local execution. Hall calls are merely
// recorded here and are injected later by TransferReadyRequests once assignment
// and coherence rules allow it.
func (sync *Synchronizer) HandleButtonPresses(edgePresses common.Requests, currentFloor int, now time.Time) (newCabRequests common.Requests, newHallRequests common.Requests) {
	for floor := range common.N_FLOORS {
		for button := range common.ButtonType(common.N_BUTTONS) {
			if !edgePresses[floor][button] {
				continue
			}
			sync.localRequests[floor][button] = true
			switch button {
			case common.BT_Cab:
				newCabRequests[floor][button] = true
				sync.deliveredRequests[floor][button] = true
			case common.BT_HallUp, common.BT_HallDown:
				newHallRequests[floor][button] = true
			}
		}
	}
	return newCabRequests, newHallRequests
}

// TransferReadyRequests returns the requests that should be handed to Elevator
// on this tick.
//
// Cab calls are always local. Hall calls are only released when they are
// assigned to this elevator and either the node is alone or the cluster has
// reached a coherent shared view.
func (sync *Synchronizer) TransferReadyRequests() (toTransfer common.Requests) {
	for floor := range common.N_FLOORS {
		// Cab calls are private to the local elevator and should be injected once.
		if sync.localRequests[floor][common.BT_Cab] && !sync.deliveredRequests[floor][common.BT_Cab] {
			toTransfer[floor][common.BT_Cab] = true
			sync.deliveredRequests[floor][common.BT_Cab] = true
		}

		for button := common.ButtonType(common.BT_HallUp); button <= common.BT_HallDown; button++ {
			if sync.deliveredRequests[floor][button] {
				continue
			}
			// While peers are alive but disagree on the shared snapshot, wait for
			// convergence before committing to a hall assignment.
			if sync.hasAlivePeer && !sync.coherent {
				continue
			}

			hallActive := sync.netRequests[floor][button] || sync.localRequests[floor][button]
			// In distributed mode, only the replicated hall state counts. In
			// single-elevator mode, locally latched hall calls are served directly.
			if sync.hasAlivePeer {
				hallActive = sync.netRequests[floor][button]
			}
			if !hallActive || !sync.assignedHall[floor][button] {
				continue
			}

			sync.localRequests[floor][button] = true
			sync.deliveredRequests[floor][button] = true
			toTransfer[floor][button] = true
		}
	}
	return toTransfer
}

// ClearServicedRequests clears any requests that the Elevator just reported
// serviced at floor.
func (sync *Synchronizer) ClearServicedRequests(floor int, serviced common.Requests) {
	if floor < 0 || floor >= common.N_FLOORS {
		return
	}
	for button := range common.ButtonType(common.N_BUTTONS) {
		if serviced[floor][button] {
			sync.localRequests[floor][button] = false
			sync.netRequests[floor][button] = false
			sync.deliveredRequests[floor][button] = false
		}
	}
}

// HasAlivePeer reports whether at least one other elevator is currently alive
// and represented by a state entry in the world view.
func (sync *Synchronizer) HasAlivePeer() bool { return sync.hasAlivePeer }

// IsInitFromNetwork reports whether the local cab state has been seen in a
// network snapshot since startup.
func (sync *Synchronizer) IsInitFromNetwork() bool { return sync.initFromNetwork }

// GetLocalRequests returns the synchronizer's local request matrix.
func (sync *Synchronizer) GetLocalRequests() common.Requests {
	return sync.localRequests
}

// GetNetRequests returns the latest request matrix derived from the shared
// network snapshot.
func (sync *Synchronizer) GetNetRequests() common.Requests {
	return sync.netRequests
}
