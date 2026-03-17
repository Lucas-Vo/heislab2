package elevnetwork

import (
	"elevator/common"
	"elevator/elevhw"
	"log"
	"time"
)

const (
	WV_TIMEOUT           = 4 * time.Second
	VALID_SERVICE_WINDOW = 1 * time.Second
	VALID_COUNTER_WINDOW = 20
)

type ServicedRequest [elevhw.N_FLOORS][elevhw.N_BUTTONS]time.Time

type WorldView struct {
	peers   []string
	selfKey string

	localSnapshot common.Snapshot
	lastSnapshot  map[string]common.Snapshot

	lastServicedRequest ServicedRequest
	lastHeard           map[string]time.Time

	inStartupPeriod bool
	selfAlive       bool

	latestCount map[string]uint64
}

// ------------ Exported Methods -------------

func InitWorldView(config common.Config) *WorldView {
	wv := &WorldView{
		peers: config.ExpectedKeys(),
		localSnapshot: common.Snapshot{
			HallRequests: common.HallRequests{},
			States:       make(map[string]common.ElevState),
			Alive:        make(map[string]bool),
		},
		lastHeard:           make(map[string]time.Time),
		lastSnapshot:        make(map[string]common.Snapshot),
		lastServicedRequest: ServicedRequest{},
		selfKey:             config.SelfKey,
		selfAlive:           true,
		latestCount:         make(map[string]uint64),
		inStartupPeriod:     true,
	}
	wv.CalculateAlivePeers(time.Now())

	return wv
}

// sends the worldview's snapshot to elevatorthread and assignerthread
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

func (wv *WorldView) HandleLocal(snap common.Snapshot, now time.Time) {
	wv.SetSelfAlive(true)
	filteredSnap, _, _ := wv.filterRecentlyServicedHalls(snap, now)
	if filteredSnap.UpdateKind == common.UK_Serviced {
		wv.markRecentlyServicedRequests(filteredSnap, now)
	}
	wv.mergeWorldView(wv.selfKey, filteredSnap)
}

func (wv *WorldView) HandleRemote(msg common.NetMsg, now time.Time) (common.HallRequests, bool) {
	if msg.Origin == "" || msg.Origin == wv.selfKey {
		return common.HallRequests{}, false
	}

	msgToMerge := msg
	var filteredHalls common.HallRequests
	isFiltered := false

	msgToMerge.Snapshot, filteredHalls, isFiltered = wv.filterRecentlyServicedHalls(msg.Snapshot, now)

	switch msgToMerge.Origin {
	case "1":
		log.Printf("incoming message from 1")
	case "2":
		log.Printf("incoming message from 2")
	case "3":
		log.Printf("incoming message from 3")
	default:
		log.Printf("incoming message from unknown id")
	}

	prevCounter := wv.latestCount[msgToMerge.Origin]
	prevHeard := wv.lastHeard[msgToMerge.Origin]
	wv.lastHeard[msgToMerge.Origin] = now

	withinCounterWindow := prevCounter <= VALID_COUNTER_WINDOW ||
		msgToMerge.Counter > prevCounter-VALID_COUNTER_WINDOW
	if now.Sub(prevHeard) < WV_TIMEOUT &&
		msgToMerge.Counter <= prevCounter &&
		withinCounterWindow {
		log.Printf("drop dead/duplicate frame origin=%s counter=%d prev=%d", msgToMerge.Origin, msgToMerge.Counter, prevCounter)
		return common.HallRequests{}, false
	}
	wv.latestCount[msgToMerge.Origin] = msgToMerge.Counter

	if isFiltered {
		wv.CalculateAlivePeers(now)
	}
	wv.mergeWorldView(msgToMerge.Origin, msgToMerge.Snapshot)
	return filteredHalls, isFiltered
}

func (wv *WorldView) ReapplyServicedHalls(serviced common.HallRequests) common.Snapshot {
	snap := common.DeepCopySnapshot(wv.localSnapshot)
	if wv.inStartupPeriod {
		return snap
	}
	snap.UpdateKind = common.UK_Serviced
	for floor := range elevhw.N_FLOORS {
		for button := 0; button < 2; button++ {
			if serviced[floor][button] {
				snap.HallRequests[floor][button] = false
			}
		}
	}
	return snap
}

func (wv *WorldView) CalculateAlivePeers(now time.Time) {
	for _, id := range wv.peers {
		wv.localSnapshot.Alive[id] = wv.inStartupPeriod
		if id == wv.selfKey {
			wv.localSnapshot.Alive[id] = wv.selfAlive
			continue
		}
		if t, ok := wv.lastHeard[id]; ok {
			wv.localSnapshot.Alive[id] = now.Sub(t) <= WV_TIMEOUT
			continue
		}
	}
}

func (wv *WorldView) SnapshotForBroadcast() common.Snapshot {
	return common.DeepCopySnapshot(wv.localSnapshot)
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

		for floor := range elevhw.N_FLOORS {
			if selfSnapshot.HallRequests[floor][elevhw.BT_HallUp] != peerSnapshot.HallRequests[floor][elevhw.BT_HallUp] {
				return false
			} else if selfSnapshot.HallRequests[floor][elevhw.BT_HallDown] != peerSnapshot.HallRequests[floor][elevhw.BT_HallDown] {
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

func (wv *WorldView) EndStartupPeriod() {
	wv.inStartupPeriod = false
}

func (wv *WorldView) SetSelfAlive(alive bool) {
	wv.selfAlive = alive
}

func (wv *WorldView) GetSelfAlive() bool {
	return wv.selfAlive
}

// ------------ Unexported Methods -------------

func (wv *WorldView) markRecentlyServicedRequests(ns common.Snapshot, now time.Time) {
	for floor := range elevhw.N_FLOORS {
		for button := 0; button < 2; button++ {
			if wv.localSnapshot.HallRequests[floor][button] && !ns.HallRequests[floor][button] {
				wv.lastServicedRequest[floor][button] = now
			}
		}
	}

	prevSelfState, hasPrevSelf := wv.localSnapshot.States[wv.selfKey]
	nextSelfState, hasNextSelf := ns.States[wv.selfKey]
	if !hasPrevSelf || !hasNextSelf {
		return
	}
	for floor := range elevhw.N_FLOORS {
		if prevSelfState.CabRequests[floor] && !nextSelfState.CabRequests[floor] {
			wv.lastServicedRequest[floor][elevhw.BT_Cab] = now
		}
	}
}

// removes requests from incoming snapshot if requests were recently serviced
func (wv *WorldView) filterRecentlyServicedHalls(snapshot common.Snapshot, now time.Time) (common.Snapshot, common.HallRequests, bool) {
	var serviced common.HallRequests
	snapIsFiltered := false
	if wv.inStartupPeriod {
		snapshot.UpdateKind = common.UK_Requests
		return snapshot, serviced, snapIsFiltered
	}

	for floor := range elevhw.N_FLOORS {
		for button := 0; button < 2; button++ {
			if snapshot.HallRequests[floor][button] && wv.isRequestRecentlyServiced(floor, elevhw.ButtonType(button), now) {
				snapshot.HallRequests[floor][button] = false
				serviced[floor][button] = true
				snapIsFiltered = true
			}
		}
	}

	for key, state := range snapshot.States {
		if key != wv.selfKey {
			continue
		}
		for floor := range elevhw.N_FLOORS {
			if state.CabRequests[floor] && wv.isRequestRecentlyServiced(floor, elevhw.BT_Cab, now) {
				state.CabRequests[floor] = false
				snapIsFiltered = true
			}
		}
		snapshot.States[key] = state
	}
	return snapshot, serviced, snapIsFiltered
}

func (wv *WorldView) isRequestRecentlyServiced(floor int, button elevhw.ButtonType, now time.Time) bool {
	if button < 0 || button >= elevhw.ButtonType(elevhw.N_BUTTONS) {
		return false
	}
	lastServiced := wv.lastServicedRequest[floor][button]
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
	for i := range elevhw.N_FLOORS {
		localSelf.CabRequests[i] = localSelf.CabRequests[i] || peerSelf.CabRequests[i]
	}
	wv.localSnapshot.States[wv.selfKey] = localSelf
}

func (wv *WorldView) mergeHallRequests(incoming common.HallRequests, kind common.UpdateKind) {
	for i := range elevhw.N_FLOORS {
		switch kind {
		case common.UK_Serviced:
			wv.localSnapshot.HallRequests[i][0] = wv.localSnapshot.HallRequests[i][0] && incoming[i][0]
			wv.localSnapshot.HallRequests[i][1] = wv.localSnapshot.HallRequests[i][1] && incoming[i][1]
		case common.UK_Requests:
			wv.localSnapshot.HallRequests[i][0] = wv.localSnapshot.HallRequests[i][0] || incoming[i][0]
			wv.localSnapshot.HallRequests[i][1] = wv.localSnapshot.HallRequests[i][1] || incoming[i][1]
		}
	}
}
