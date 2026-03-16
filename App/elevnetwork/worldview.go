package elevnetwork

import (
	"Network-go/network/peers"
	"elevator/common"
	"log"
	"sync"
	"time"
)

const (
	WV_TIMEOUT           = 6 * time.Second
	VALID_SERVICE_WINDOW = 2 * time.Second
	VALID_COUNTER_WINDOW = 20
)

type WorldView struct {
	mu      sync.Mutex
	peers   []string
	selfKey string

	localSnapshot common.Snapshot
	lastSnapshot  map[string]common.Snapshot

	lastServicedHall [common.N_FLOORS][2]time.Time
	lastHeard        map[string]time.Time

	inStartupPeriod bool
	selfAlive       bool

	msgCounter  uint64
	latestCount map[string]uint64
	outgoingCh  chan<- common.NetMsg
}

func InitWorldView(config common.Config, outgoing chan<- common.NetMsg) *WorldView {
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
		outgoingCh:      outgoing,
	}
	wv.CalculateAlive(time.Now())

	initialSnapshot := common.DeepCopySnapshot(wv.localSnapshot)
	initialSnapshot.UpdateKind = common.UpdateRequests
	wv.sendOverNetwork(initialSnapshot)

	return wv
}

func (wv *WorldView) EndStartupPeriod() {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	wv.inStartupPeriod = false
}

func (wv *WorldView) SetSelfAlive(alive bool) {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	wv.selfAlive = alive
}

func (wv *WorldView) GetSelfAlive() bool {
	return wv.selfAlive
}

func (wv *WorldView) HandlePeerUpdate(update peers.PeerUpdate, now time.Time) {
	wv.mu.Lock()
	defer wv.mu.Unlock()

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

func (wv *WorldView) PublishLocally(netSnap1Ch, netSnap2Ch chan<- common.Snapshot) {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	snap := common.DeepCopySnapshot(wv.localSnapshot)
	coherent := !wv.inStartupPeriod && wv.SnapshotsAreCoherent()
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

func (wv *WorldView) SnapshotsAreCoherent() bool {
	selfSnapshot, ok := wv.lastSnapshot[wv.selfKey]
	if !ok {
		return false
	}
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

func (wv *WorldView) MarkRecentlyServicedHalls(ns common.Snapshot, now time.Time) {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	for floor := range common.N_FLOORS {
		for button := 0; button < 2; button++ {
			if wv.localSnapshot.HallRequests[floor][button] && !ns.HallRequests[floor][button] {
				wv.lastServicedHall[floor][button] = now
			}
		}
	}
}

func (wv *WorldView) FilterRecentlyServicedHalls(msg common.NetMsg, now time.Time) (common.NetMsg, [common.N_FLOORS][2]bool, bool) {
	var serviced [common.N_FLOORS][2]bool
	msgIsFiltered := false
	if msg.Origin == "" || msg.Origin == wv.selfKey {
		return msg, serviced, msgIsFiltered
	}

	wv.mu.Lock()
	defer wv.mu.Unlock()
	for floor := range common.N_FLOORS {
		for button := 0; button < 2; button++ {
			if msg.Snapshot.HallRequests[floor][button] && wv.wasRecentlyServicedLocked(floor, button, now) {
				msg.Snapshot.HallRequests[floor][button] = false
				serviced[floor][button] = true
				msgIsFiltered = true
			}
		}
	}
	return msg, serviced, msgIsFiltered
}

func (wv *WorldView) ResendServicedHalls(serviced [common.N_FLOORS][2]bool) {
	wv.mu.Lock()
	snap := common.DeepCopySnapshot(wv.localSnapshot)
	wv.mu.Unlock()

	snap.UpdateKind = common.UpdateServiced
	for floor := range common.N_FLOORS {
		for button := 0; button < 2; button++ {
			if serviced[floor][button] {
				snap.HallRequests[floor][button] = false
			}
		}
	}
	wv.sendOverNetwork(snap)
}

func (wv *WorldView) MergeRemote(msg common.NetMsg) {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	if msg.Origin == wv.selfKey || msg.Origin == "" {
		return
	}
	switch msg.Origin { //TODO: DELETE THIS SHIT
	case "1":
		log.Printf("((((((((((((((((((((((((((((((((((((((((((((((((((((()))))))))))))))))))))))))))))))))))))))))))))))))))))")
	case "2":
		log.Printf("nignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignig")
	case "3":
		log.Printf("####################################################################################################################################")
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

func (wv *WorldView) Broadcast() {
	wv.mu.Lock()
	alive := wv.selfAlive
	snap := common.DeepCopySnapshot(wv.localSnapshot)
	wv.mu.Unlock()
	if !alive || (snap.UpdateKind == common.UpdateRequests && wv.inStartupPeriod) {
		return
	}
	wv.sendOverNetwork(snap)
}

func (wv *WorldView) CalculateAlive(now time.Time) {
	wv.mu.Lock()
	defer wv.mu.Unlock()
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

func (wv *WorldView) sendOverNetwork(snap common.Snapshot) {
	wv.mu.Lock()
	if !wv.selfAlive || wv.outgoingCh == nil {
		wv.mu.Unlock()
		return
	}
	wv.msgCounter++
	msg := common.NetMsg{Origin: wv.selfKey, Counter: wv.msgCounter, Snapshot: snap}
	wv.lastHeard[wv.selfKey] = time.Now()
	wv.lastSnapshot[wv.selfKey] = common.DeepCopySnapshot(snap)
	wv.mu.Unlock()

	select {
	case wv.outgoingCh <- msg:
	default:
		log.Printf("sendOverNetwork: dropping frame origin=%s counter=%d kind=%v (tx queue full)", msg.Origin, msg.Counter, snap.UpdateKind)
	}
}

func (wv *WorldView) wasRecentlyServicedLocked(floor int, button int, now time.Time) bool {
	lastServiced := wv.lastServicedHall[floor][button]
	if lastServiced.IsZero() {
		return false
	}
	return now.Sub(lastServiced) <= VALID_SERVICE_WINDOW
}

func (wv *WorldView) MergeWorldView(fromKey string, ns common.Snapshot) { //TODO: maybe since we copy ns, use the new copy to keep synchronizing good
	if fromKey != wv.selfKey {
		wv.lastSnapshot[fromKey] = common.DeepCopySnapshot(ns)
		if wv.inStartupPeriod {
			wv.recoverCabRequests(ns)
			wv.inStartupPeriod = false
		}
	}

	wv.mergeHallRequests(ns.HallRequests, ns.UpdateKind)
	for k, st := range ns.States {
		if k == wv.selfKey && fromKey != wv.selfKey && !wv.inStartupPeriod {
			continue
		}
		wv.localSnapshot.States[k] = st
	}
	wv.localSnapshot.UpdateKind = ns.UpdateKind
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
