package elevnetwork

import (
	"Network-go/network/peers"
	"elevator/common"
	"log"
	"time"
)

const (
	WV_TIMEOUT           = 4 * time.Second
	VALID_SERVICE_WINDOW = 2 * time.Second
	VALID_COUNTER_WINDOW = 20
)

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

func (wv *WorldView) EndStartupPeriod() {
	wv.inStartupPeriod = false
}

func (wv *WorldView) SetSelfAlive(alive bool) {
	wv.selfAlive = alive
}

func (wv *WorldView) GetSelfAlive() bool {
	return wv.selfAlive
}

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
			} else if selfSnapshot.HallRequests[floor][common.BT_HallDown] != peerSnapshot.HallRequests[floor][common.BT_HallDown] {
				return false
			} else if selfSnapshot.States[wv.selfKey].CabRequests[floor] != peerSnapshot.States[wv.selfKey].CabRequests[floor] {
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

func (wv *WorldView) HandleLocal(ns common.Snapshot, now time.Time) {
	wv.SetSelfAlive(true)
	if ns.UpdateKind == common.UpdateServiced {
		wv.markRecentlyServicedHalls(ns, now)
	}
	wv.mergeWorldView(wv.selfKey, ns)
}

func (wv *WorldView) HandleRemote(msg common.NetMsg, now time.Time) ([common.N_FLOORS][2]bool, bool) {
	msgToMerge, filteredHalls, isFiltered := wv.filterRecentlyServicedHalls(msg, now)
	wv.mergeRemote(msgToMerge)
	if isFiltered {
		wv.CalculateAlive(now)
	}
	wv.mergeWorldView(msgToMerge.Origin, msgToMerge.Snapshot)
	return filteredHalls, isFiltered
}

func (wv *WorldView) SnapshotForBroadcast() common.Snapshot {
	return common.DeepCopySnapshot(wv.localSnapshot)
}

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
	switch msg.Origin { //TODO: DELETE THIS SHIT
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
	if now.Sub(prevHeard) < WV_TIMEOUT && msg.Counter < prevCounter && msg.Counter > prevCounter-VALID_COUNTER_WINDOW {
		log.Printf("drop stale/duplicate frame origin=%s counter=%d prevCounter=%d dt=%s", msg.Origin, msg.Counter, prevCounter, now.Sub(prevHeard))
		return
	}
	wv.latestCount[msg.Origin] = msg.Counter
}

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
			wv.recoverCabRequests(snap)
			wv.inStartupPeriod = false
		}
	}

	wv.mergeHallRequests(snap.HallRequests, snap.UpdateKind)
	for k, st := range snap.States {
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
			wv.localSnapshot.HallRequests[i][0] = wv.localSnapshot.HallRequests[i][0] && incoming[i][0]
			wv.localSnapshot.HallRequests[i][1] = wv.localSnapshot.HallRequests[i][1] && incoming[i][1]
		case common.UpdateRequests:
			wv.localSnapshot.HallRequests[i][0] = wv.localSnapshot.HallRequests[i][0] || incoming[i][0]
			wv.localSnapshot.HallRequests[i][1] = wv.localSnapshot.HallRequests[i][1] || incoming[i][1]
		}
	}
}
