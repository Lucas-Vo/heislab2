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
	VALID_COUNTER_WINDOW = 2
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

	latestMsgCount map[string]uint64
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
		latestMsgCount:      make(map[string]uint64),
		inStartupPeriod:     true,
	}
	wv.CalculateAlivePeers(time.Now())

	return wv
}

// sends the worldview's snapshot to elevatorthread and assignerthread
func (wv *WorldView) PublishLocally(netSnap1Ch, netSnap2Ch chan<- common.Snapshot, snapshotsCoherent bool) {
	snapshot := common.DeepCopySnapshot(wv.localSnapshot)
	coherent := !wv.inStartupPeriod && snapshotsCoherent
	snapshot.Coherent = coherent
	snap1 := common.DeepCopySnapshot(snapshot)
	select {
	case netSnap1Ch <- snap1:
	default:
	}
	snap2 := common.DeepCopySnapshot(snapshot)
	select {
	case netSnap2Ch <- snap2:
	default:
	}
}

func (wv *WorldView) HandleLocalSnapshot(snapshot common.Snapshot, now time.Time) {
	wv.SetSelfAlive(true)
	filteredSnap, _ := wv.filterServicedHalls(snapshot, now)
	if filteredSnap.UpdateKind == common.UK_Serviced {
		wv.markServicedRequests(filteredSnap, now)
	}
	wv.mergeSnapshot(wv.selfKey, filteredSnap)
}

func (wv *WorldView) HandleRemoteMsg(msg common.NetMsg, now time.Time) bool {
	if msg.Origin == "" || msg.Origin == wv.selfKey {
		return false
	}

	msgToMerge := msg
	isFiltered := false

	msgToMerge.Snapshot, isFiltered = wv.filterServicedHalls(msg.Snapshot, now)

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

	prevCounter := wv.latestMsgCount[msgToMerge.Origin]
	prevHeard := wv.lastHeard[msgToMerge.Origin]
	wv.lastHeard[msgToMerge.Origin] = now

	withinCounterWindow := prevCounter <= VALID_COUNTER_WINDOW ||
		msgToMerge.Counter > prevCounter-VALID_COUNTER_WINDOW
	if now.Sub(prevHeard) < WV_TIMEOUT &&
		msgToMerge.Counter <= prevCounter &&
		withinCounterWindow {
		log.Printf("drop dead/duplicate frame origin=%s counter=%d prev=%d", msgToMerge.Origin, msgToMerge.Counter, prevCounter)
		return false
	}
	wv.latestMsgCount[msgToMerge.Origin] = msgToMerge.Counter

	if isFiltered {
		wv.CalculateAlivePeers(now)
	}
	wv.mergeSnapshot(msgToMerge.Origin, msgToMerge.Snapshot)
	return isFiltered
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

func (wv *WorldView) SnapshotsAreCoherent(snapshot common.Snapshot) bool {
	selfViewOfSelf, ok := snapshot.States[wv.selfKey]
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
			if snapshot.HallRequests[floor][elevhw.BT_HallUp] != peerSnapshot.HallRequests[floor][elevhw.BT_HallUp] {
				return false
			} else if snapshot.HallRequests[floor][elevhw.BT_HallDown] != peerSnapshot.HallRequests[floor][elevhw.BT_HallDown] {
				return false
			} else if snapshot.States[wv.selfKey].CabRequests[floor] != peerSnapshot.States[wv.selfKey].CabRequests[floor] {
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

func (wv *WorldView) GetLocalSnapshot() common.Snapshot {
	return common.DeepCopySnapshot(wv.localSnapshot)
}

// ------------ Unexported Methods -------------

// removes requests from incoming snapshot if requests were recently serviced
func (wv *WorldView) filterServicedHalls(snapshot common.Snapshot, now time.Time) (common.Snapshot, bool) {
	var serviced common.HallRequests
	snapIsFiltered := false
	if wv.inStartupPeriod {
		snapshot.UpdateKind = common.UK_Requests
		return snapshot, snapIsFiltered
	}

	for floor := range elevhw.N_FLOORS {
		for button := 0; button < 2; button++ {
			if snapshot.HallRequests[floor][button] && wv.isRequestServiced(floor, elevhw.ButtonType(button), now) {
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
			if state.CabRequests[floor] && wv.isRequestServiced(floor, elevhw.BT_Cab, now) {
				state.CabRequests[floor] = false
				snapIsFiltered = true
			}
		}
		snapshot.States[key] = state
	}
	return snapshot, snapIsFiltered
}

func (wv *WorldView) markServicedRequests(snapshot common.Snapshot, now time.Time) {
	for floor := range elevhw.N_FLOORS {
		for button := 0; button < 2; button++ {
			if wv.localSnapshot.HallRequests[floor][button] && !snapshot.HallRequests[floor][button] {
				wv.lastServicedRequest[floor][button] = now
			}
		}
	}

	prevSelfState, hasPrevSelf := wv.localSnapshot.States[wv.selfKey]
	nextSelfState, hasNextSelf := snapshot.States[wv.selfKey]
	if !hasPrevSelf || !hasNextSelf {
		return
	}
	for floor := range elevhw.N_FLOORS {
		if prevSelfState.CabRequests[floor] && !nextSelfState.CabRequests[floor] {
			wv.lastServicedRequest[floor][elevhw.BT_Cab] = now
		}
	}
}

func (wv *WorldView) isRequestServiced(floor int, button elevhw.ButtonType, now time.Time) bool {
	if button < 0 || button >= elevhw.ButtonType(elevhw.N_BUTTONS) {
		return false
	}
	lastServiced := wv.lastServicedRequest[floor][button]
	if lastServiced.IsZero() {
		return false
	}
	return now.Sub(lastServiced) <= VALID_SERVICE_WINDOW
}

func (wv *WorldView) mergeSnapshot(fromKey string, snapshot common.Snapshot) {
	if fromKey != wv.selfKey {
		wv.lastSnapshot[fromKey] = common.DeepCopySnapshot(snapshot)
		if wv.inStartupPeriod {
			wv.recoverCabRequests(snapshot)
		}
	}

	wv.mergeHallRequests(snapshot.HallRequests, snapshot.UpdateKind)
	for k, st := range snapshot.States {
		if k == wv.selfKey && fromKey != wv.selfKey && !wv.inStartupPeriod {
			continue
		}
		wv.localSnapshot.States[k] = st
	}
	wv.localSnapshot.UpdateKind = snapshot.UpdateKind
}

func (wv *WorldView) recoverCabRequests(snapshot common.Snapshot) {
	peerSelf, ok := snapshot.States[wv.selfKey]
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
