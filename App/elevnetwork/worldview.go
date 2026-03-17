package elevnetwork

import (
	"Network-go/network/peers"
	"elevator/common"
	"log"
	"time"
)

const (
	// WV_TIMEOUT is the peer-heard timeout before another elevator is marked
	// dead.
	WV_TIMEOUT = 4 * time.Second
	// VALID_SERVICE_WINDOW suppresses hall-call resurrection from delayed packets
	// shortly after a call was served locally.
	VALID_SERVICE_WINDOW = 2 * time.Second
	// VALID_COUNTER_WINDOW bounds how far an older packet counter may lag before
	// it is accepted again after silence.
	VALID_COUNTER_WINDOW = 20
)

// WorldView maintains the controller's merged picture of the whole elevator
// cluster.
//
// WorldView is responsible for hall-request merge semantics, liveness tracking,
// startup recovery of cab requests, and coherence checks before hall assignment.
type WorldView struct {
	peers   []string
	selfKey string

	localSnapshot common.Snapshot
	lastSnapshot  map[string]common.Snapshot

	lastServicedHall [common.N_FLOORS][2]time.Time
	lastHeard        map[string]time.Time

	inStartupPeriod bool
	selfAlive       bool

	latestCount map[string]uint64
}

// InitWorldView returns an initialized world view for the configured peer set.
func InitWorldView(config common.Config) *WorldView {
	wv := &WorldView{
		peers: config.ExpectedKeys(),
		localSnapshot: common.Snapshot{
			HallRequests: [common.N_FLOORS][2]bool{},
			States:       make(map[string]common.ElevState),
			Alive:        make(map[string]bool),
		},
		lastHeard:       make(map[string]time.Time),
		lastSnapshot:    make(map[string]common.Snapshot),
		selfKey:         config.SelfKey,
		selfAlive:       true,
		latestCount:     make(map[string]uint64),
		inStartupPeriod: true,
	}
	wv.CalculateAlive(time.Now())

	return wv
}

// EndStartupPeriod disables the optimistic startup behavior used while waiting
// for the first peer snapshots.
func (wv *WorldView) EndStartupPeriod() {
	wv.inStartupPeriod = false
}

// SetSelfAlive records whether the local elevator should still be considered
// assignable.
func (wv *WorldView) SetSelfAlive(alive bool) {
	wv.selfAlive = alive
}

// GetSelfAlive reports whether the local elevator is currently considered
// assignable.
func (wv *WorldView) GetSelfAlive() bool {
	return wv.selfAlive
}

// HandlePeerUpdate updates last-heard timestamps from the peer-discovery
// subsystem.
func (wv *WorldView) HandlePeerUpdate(update peers.PeerUpdate, now time.Time) {
	if update.New != "" && update.New != wv.selfKey {
		wv.lastHeard[update.New] = now
	}
	for _, id := range update.Peers {
		if id == "" || id == wv.selfKey {
			continue
		}
		wv.lastHeard[id] = now
	}
	for _, id := range update.Lost {
		if id == "" || id == wv.selfKey {
			continue
		}
		delete(wv.lastHeard, id)
	}
}

// PublishLocally sends the current merged snapshot to the assigner and elevator
// threads.
//
// The exported Coherent flag is only set after startup and only when the latest
// peer snapshots agree with the local snapshot.
func (wv *WorldView) PublishLocally(netSnap1Ch, netSnap2Ch chan<- common.Snapshot, snapshotsCoherent bool) {
	snap := common.DeepCopySnapshot(wv.localSnapshot)
	coherent := !wv.inStartupPeriod && snapshotsCoherent
	snap.Coherent = coherent
	snap1 := common.DeepCopySnapshot(snap)
	select {
	case netSnap1Ch <- snap1:
	default:
	}
	snap2 := common.DeepCopySnapshot(snap)
	select {
	case netSnap2Ch <- snap2:
	default:
	}
}

// SnapshotsAreCoherent reports whether all currently alive peers agree on hall
// calls and on this elevator's published local state.
func (wv *WorldView) SnapshotsAreCoherent(selfSnapshot common.Snapshot) bool {
	selfViewOfSelf, ok := selfSnapshot.States[wv.selfKey]
	if !ok {
		return false
	}
	for _, peerID := range wv.peers {
		if peerID == wv.selfKey || !wv.localSnapshot.Alive[peerID] {
			continue
		}
		peerSnapshot, ok := wv.lastSnapshot[peerID]
		if !ok {
			return false
		}
		peerViewOfSelf, ok := peerSnapshot.States[wv.selfKey]
		if !ok {
			return false
		}

		for floor := range common.N_FLOORS {
			if selfSnapshot.HallRequests[floor][common.BT_HallUp] != peerSnapshot.HallRequests[floor][common.BT_HallUp] {
				return false
			}
			if selfSnapshot.HallRequests[floor][common.BT_HallDown] != peerSnapshot.HallRequests[floor][common.BT_HallDown] {
				return false
			}
		}

		if selfViewOfSelf.Behavior != peerViewOfSelf.Behavior ||
			selfViewOfSelf.Direction != peerViewOfSelf.Direction ||
			selfViewOfSelf.Floor != peerViewOfSelf.Floor {
			return false
		}
	}
	return true
}

// HandleLocal merges a locally produced snapshot into the world view.
func (wv *WorldView) HandleLocal(ns common.Snapshot, now time.Time) {
	wv.SetSelfAlive(true)
	if ns.UpdateKind == common.UpdateServiced {
		wv.markRecentlyServicedHalls(ns, now)
	}
	wv.mergeWorldView(wv.selfKey, ns)
}

// HandleRemote merges one remote network message into the world view.
//
// The returned hall matrix is non-zero when recently serviced hall calls were
// filtered out and should be re-broadcast as cleared.
func (wv *WorldView) HandleRemote(msg common.NetMsg, now time.Time) ([common.N_FLOORS][2]bool, bool) {
	msgToMerge, filteredHalls, isFiltered := wv.filterRecentlyServicedHalls(msg, now)
	wv.mergeRemote(msgToMerge)
	if isFiltered {
		wv.CalculateAlive(now)
	}
	wv.mergeWorldView(msgToMerge.Origin, msgToMerge.Snapshot)
	return filteredHalls, isFiltered
}

// SnapshotForBroadcast returns a defensive copy of the merged local snapshot.
func (wv *WorldView) SnapshotForBroadcast() common.Snapshot {
	return common.DeepCopySnapshot(wv.localSnapshot)
}

// SnapshotForResend returns a serviced snapshot that explicitly clears the hall
// calls marked in serviced.
func (wv *WorldView) SnapshotForResend(serviced [common.N_FLOORS][2]bool) common.Snapshot {
	snap := common.DeepCopySnapshot(wv.localSnapshot)
	snap.UpdateKind = common.UpdateServiced
	for floor := range common.N_FLOORS {
		for button := 0; button < 2; button++ {
			if serviced[floor][button] {
				snap.HallRequests[floor][button] = false
			}
		}
	}
	return snap
}

func (wv *WorldView) markRecentlyServicedHalls(ns common.Snapshot, now time.Time) {
	for floor := range common.N_FLOORS {
		for button := 0; button < 2; button++ {
			if wv.localSnapshot.HallRequests[floor][button] && !ns.HallRequests[floor][button] {
				wv.lastServicedHall[floor][button] = now
			}
		}
	}
}

// filterRecentlyServicedHalls masks hall calls that are being resurrected by
// delayed packets shortly after a local service event.
func (wv *WorldView) filterRecentlyServicedHalls(msg common.NetMsg, now time.Time) (common.NetMsg, [common.N_FLOORS][2]bool, bool) {
	var serviced [common.N_FLOORS][2]bool
	msgIsFiltered := false
	if msg.Origin == "" || msg.Origin == wv.selfKey {
		return msg, serviced, msgIsFiltered
	}

	for floor := range common.N_FLOORS {
		for button := 0; button < 2; button++ {
			if msg.Snapshot.HallRequests[floor][button] && wv.isHallRecentlyServiced(floor, button, now) {
				msg.Snapshot.HallRequests[floor][button] = false
				serviced[floor][button] = true
				msgIsFiltered = true
			}
		}
	}
	return msg, serviced, msgIsFiltered
}

func (wv *WorldView) mergeRemote(msg common.NetMsg) {
	if msg.Origin == wv.selfKey || msg.Origin == "" {
		return
	}
	// Keep the origin-specific marker logs so packet-loss traces are easier to
	// scan by eye during FAT/debugging.
	switch msg.Origin {
	case "1":
		log.Printf("((((((((((((((((((((((((((((((((((((((((((((((((((((()))))))))))))))))))))))))))))))))))))))))))))))))))))")
	case "2":
		log.Printf("iiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiii")
	case "3":
		log.Printf("####################################################################################################################################")
	default:
		log.Printf("Unknown id")
	}
	now := time.Now()
	prevCounter := wv.latestCount[msg.Origin]
	prevHeard := wv.lastHeard[msg.Origin]
	wv.lastHeard[msg.Origin] = now
	// A small rollback window tolerates packet reordering without letting very
	// old broadcasts overwrite newer state after a loss burst.
	if now.Sub(prevHeard) < WV_TIMEOUT && msg.Counter < prevCounter && msg.Counter > prevCounter-VALID_COUNTER_WINDOW {
		log.Printf("drop stale/duplicate frame origin=%s counter=%d prevCounter=%d dt=%s", msg.Origin, msg.Counter, prevCounter, now.Sub(prevHeard))
		return
	}
	wv.latestCount[msg.Origin] = msg.Counter
}

// CalculateAlive refreshes the Alive map from peer heartbeats and the local
// selfAlive flag.
//
// During startup, peers that have not yet been heard from are optimistically
// treated as alive until EndStartupPeriod is called.
func (wv *WorldView) CalculateAlive(now time.Time) {
	for _, id := range wv.peers {
		if id == wv.selfKey {
			wv.localSnapshot.Alive[id] = wv.selfAlive
			continue
		}
		if t, ok := wv.lastHeard[id]; ok {
			wv.localSnapshot.Alive[id] = now.Sub(t) <= WV_TIMEOUT
			continue
		}
		wv.localSnapshot.Alive[id] = wv.inStartupPeriod
	}
}

func (wv *WorldView) isHallRecentlyServiced(floor int, button int, now time.Time) bool {
	lastServiced := wv.lastServicedHall[floor][button]
	if lastServiced.IsZero() {
		return false
	}
	return now.Sub(lastServiced) <= VALID_SERVICE_WINDOW
}

func (wv *WorldView) mergeWorldView(fromKey string, snap common.Snapshot) {
	if fromKey != wv.selfKey {
		wv.lastSnapshot[fromKey] = common.DeepCopySnapshot(snap)
		if wv.inStartupPeriod {
			// Cab-call recovery is network-assisted only. On startup, the first peer
			// snapshot that still remembers our last cab lights is merged back into
			// our local state.
			wv.recoverCabRequests(snap)
			wv.inStartupPeriod = false
		}
	}

	wv.mergeHallRequests(snap.HallRequests, snap.UpdateKind)
	for k, st := range snap.States {
		// After startup, remote peers may not overwrite our own local FSM state.
		if k == wv.selfKey && fromKey != wv.selfKey && !wv.inStartupPeriod {
			continue
		}
		wv.localSnapshot.States[k] = st
	}
	wv.localSnapshot.UpdateKind = snap.UpdateKind
}

func (wv *WorldView) recoverCabRequests(ns common.Snapshot) {
	peerSelf, ok := ns.States[wv.selfKey]
	if !ok {
		return
	}

	localSelf := wv.localSnapshot.States[wv.selfKey]
	for i := range common.N_FLOORS {
		localSelf.CabRequests[i] = localSelf.CabRequests[i] || peerSelf.CabRequests[i]
	}
	wv.localSnapshot.States[wv.selfKey] = localSelf
}

func (wv *WorldView) mergeHallRequests(incoming [common.N_FLOORS][2]bool, kind common.UpdateKind) {
	for i := range common.N_FLOORS {
		switch kind {
		case common.UpdateServiced:
			// Service is merged with AND so any false clears the shared hall light.
			wv.localSnapshot.HallRequests[i][0] = wv.localSnapshot.HallRequests[i][0] && incoming[i][0]
			wv.localSnapshot.HallRequests[i][1] = wv.localSnapshot.HallRequests[i][1] && incoming[i][1]
		case common.UpdateRequests:
			// New requests are merged with OR so any node can light a hall call.
			wv.localSnapshot.HallRequests[i][0] = wv.localSnapshot.HallRequests[i][0] || incoming[i][0]
			wv.localSnapshot.HallRequests[i][1] = wv.localSnapshot.HallRequests[i][1] || incoming[i][1]
		}
	}
}
