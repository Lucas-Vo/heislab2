package elevnetwork

import (
	"Network-go/network/bcast"
	"Network-go/network/peers"
	"context"
	"elevator/common"
	"log"
	"sync"
	"time"
)

const (
	wvTimeout              = 6 * time.Second
	recentlyServicedWindow = 2 * time.Second
	networkChanSize        = 128
)

type netMsg struct {
	Origin   string          `json:"origin"`
	Counter  uint64          `json:"counter"`
	Snapshot common.Snapshot `json:"snapshot"`
}

type servicedFloorTimestamp struct {
	Hall [common.N_FLOORS][2]time.Time
}

type WorldView struct {
	mu              sync.Mutex
	peers           []string
	snapshot        common.Snapshot
	servicedHall    servicedFloorTimestamp
	lastHeard       map[string]time.Time
	lastSnapshot    map[string]common.Snapshot
	peerTimeout     time.Duration
	startTime       time.Time
	inStartupPeriod bool
	selfKey         string
	selfAlive       bool
	counter         uint64
	latestCount     map[string]uint64
	msgTx           chan<- netMsg
}

func InitWorldView(
	ctx context.Context,
	cfg common.Config,
	peerPort int,
	msgPort int,
) (*WorldView, <-chan netMsg, <-chan peers.PeerUpdate) {
	incoming := make(chan netMsg, networkChanSize)
	outgoing := make(chan netMsg, networkChanSize)
	peerUpdateCh := make(chan peers.PeerUpdate, networkChanSize)
	peerTxEnable := make(chan bool, 1)

	go peers.Transmitter(peerPort, cfg.SelfKey, peerTxEnable)
	go peers.Receiver(peerPort, peerUpdateCh)
	go bcast.Transmitter(msgPort, outgoing)
	go bcast.Receiver(msgPort, incoming)

	go func() {
		<-ctx.Done()
		select {
		case peerTxEnable <- false:
		default:
		}
	}()

	wv := &WorldView{
		peers: cfg.ExpectedKeys(),
		snapshot: common.Snapshot{
			HallRequests: [common.N_FLOORS][2]bool{},
			States:       make(map[string]common.ElevState),
		},
		lastHeard:       make(map[string]time.Time),
		lastSnapshot:    make(map[string]common.Snapshot),
		peerTimeout:     wvTimeout,
		startTime:       time.Now(),
		selfKey:         cfg.SelfKey,
		selfAlive:       true,
		latestCount:     make(map[string]uint64),
		inStartupPeriod: true,
		msgTx:           outgoing,
	}
	wv.snapshot.Alive = wv.CalculateAlive(time.Now())
	// Broadcast an initial requests snapshot to seed local/remote bookkeeping early.
	initialSnapshot := common.DeepCopySnapshot(wv.snapshot)
	initialSnapshot.UpdateKind = common.UpdateRequests
	wv.sendOverNetwork(initialSnapshot)
	return wv, incoming, peerUpdateCh
}

func (wv *WorldView) JoinedNetwork() bool {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	return !wv.inStartupPeriod
}

func (wv *WorldView) EndStartupPeriod() {
	wv.mu.Lock()
	wv.inStartupPeriod = false
	wv.mu.Unlock()
}

func (wv *WorldView) SetSelfAlive(alive bool) {
	wv.mu.Lock()
	wv.selfAlive = alive
	wv.snapshot.Alive = wv.CalculateAlive(time.Now())
	wv.mu.Unlock()
}

func (wv *WorldView) SelfAlive() bool {
	wv.mu.Lock()
	defer wv.mu.Unlock()
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
	wv.snapshot.Alive = wv.CalculateAlive(now)
}

func (wv *WorldView) PublishLocally(netSnap1Ch, netSnap2Ch chan<- common.Snapshot) {
	snap := wv.GetSnapshot()
	coherent := wv.JoinedNetwork() && wv.SnapshotsAreCoherent()
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

func (wv *WorldView) GetSnapshot() common.Snapshot {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	wv.snapshot.Alive = wv.CalculateAlive(time.Now())
	snap := common.DeepCopySnapshot(wv.snapshot)
	return snap
}

func (wv *WorldView) SnapshotsAreCoherent() bool {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	alivePeers := wv.CalculateAlive(time.Now())
	selfSnapshot, hasSelfSnapshot := wv.lastSnapshot[wv.selfKey]
	if !hasSelfSnapshot {
		return false
	}
	selfViewOfSelf, hasSelfViewOfSelf := selfSnapshot.States[wv.selfKey]
	if !hasSelfViewOfSelf {
		return false
	}
	for _, peerID := range wv.peers {
		if peerID == wv.selfKey || !alivePeers[peerID] {
			continue
		}
		peerSnapshot, hasPeerSnapshot := wv.lastSnapshot[peerID]
		if !hasPeerSnapshot {
			return false
		}
		peerViewOfSelf, hasPeerViewOfSelf := peerSnapshot.States[wv.selfKey]
		if !hasPeerViewOfSelf {
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
func (wv *WorldView) MergeLocal(ns common.Snapshot) {
	wv.mu.Lock()
	wv.mergeWorldView(wv.selfKey, ns)
	inStartupPeriod, alive, kind := wv.inStartupPeriod, wv.selfAlive, ns.UpdateKind
	snap := common.DeepCopySnapshot(wv.snapshot)
	wv.mu.Unlock()
	if !alive || (kind == common.UpdateRequests && inStartupPeriod) {
		return
	}
	snap.UpdateKind = kind
	wv.sendOverNetwork(snap)
}

func (wv *WorldView) MarkRecentlyServicedHalls(ns common.Snapshot, now time.Time) {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	for floor := range common.N_FLOORS {
		for button := 0; button < 2; button++ {
			if wv.snapshot.HallRequests[floor][button] && !ns.HallRequests[floor][button] {
				wv.servicedHall.Hall[floor][button] = now
			}
		}
	}
}

func (wv *WorldView) FilterRecentlyServicedHalls(
	msg netMsg,
	now time.Time,
) (netMsg, [common.N_FLOORS][2]bool, bool) {
	if msg.Origin == "" || msg.Origin == wv.selfKey {
		return msg, [common.N_FLOORS][2]bool{}, false
	}

	var serviced [common.N_FLOORS][2]bool
	mutated := false

	wv.mu.Lock()
	for floor := range common.N_FLOORS {
		for button := 0; button < 2; button++ {
			if !msg.Snapshot.HallRequests[floor][button] {
				continue
			}
			if wv.wasRecentlyServicedLocked(floor, button, now) {
				msg.Snapshot.HallRequests[floor][button] = false
				serviced[floor][button] = true
				mutated = true
			}
		}
	}
	wv.mu.Unlock()

	if !mutated {
		return msg, serviced, false
	}
	return msg, serviced, true
}

func (wv *WorldView) ResendServicedHalls(serviced [common.N_FLOORS][2]bool) {
	wv.mu.Lock()
	snap := common.DeepCopySnapshot(wv.snapshot)
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

func (wv *WorldView) MergeRemote(msg netMsg) {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	if msg.Origin == wv.selfKey || msg.Origin == "" {
		return
	}
	switch msg.Origin {
	case "1":
		log.Printf("((((((((((((((((((((((((((((((((((((((((((((((((((((()))))))))))))))))))))))))))))))))))))))))))))))))))))")
	case "2":
		log.Printf("nignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignignig")
	case "3":
		log.Printf("####################################################################################################################################")
	}
	now := time.Now()
	prevCount, seen := wv.latestCount[msg.Origin]
	prevHeard, heard := wv.lastHeard[msg.Origin]
	wv.lastHeard[msg.Origin] = now

	if !seen || msg.Counter > prevCount || wv.inStartupPeriod || !heard || now.Sub(prevHeard) > wv.peerTimeout {
		wv.latestCount[msg.Origin] = msg.Counter
	} else {
		log.Printf("drop stale/duplicate frame origin=%s counter=%d prevCounter=%d dt=%s", msg.Origin, msg.Counter, prevCount, now.Sub(prevHeard))
		return
	}
	wv.mergeWorldView(msg.Origin, msg.Snapshot)
}

func (wv *WorldView) BroadcastRequests() {
	wv.mu.Lock()
	alive := wv.selfAlive
	snap := common.DeepCopySnapshot(wv.snapshot)
	wv.mu.Unlock()
	if !alive {
		return
	}
	snap.UpdateKind = common.UpdateRequests
	wv.sendOverNetwork(snap)
}

func (wv *WorldView) CalculateAlive(now time.Time) map[string]bool {
	alive := make(map[string]bool, len(wv.peers))
	startupGrace := now.Sub(wv.startTime) <= wv.peerTimeout
	for _, id := range wv.peers {
		if id == wv.selfKey {
			alive[id] = wv.selfAlive
			continue
		}
		if t, ok := wv.lastHeard[id]; ok {
			alive[id] = now.Sub(t) <= wv.peerTimeout
			continue
		}
		alive[id] = startupGrace
	}
	return alive
}

func (wv *WorldView) sendOverNetwork(snap common.Snapshot) {
	wv.mu.Lock()
	if !wv.selfAlive || wv.msgTx == nil {
		wv.mu.Unlock()
		return
	}
	wv.counter++
	msg := netMsg{Origin: wv.selfKey, Counter: wv.counter, Snapshot: snap}
	wv.lastHeard[wv.selfKey] = time.Now()
	wv.lastSnapshot[wv.selfKey] = common.DeepCopySnapshot(snap)
	wv.mu.Unlock()

	select {
	case wv.msgTx <- msg:
	default:
		log.Printf("sendOverNetwork: dropping frame origin=%s counter=%d kind=%v (tx queue full)", msg.Origin, msg.Counter, snap.UpdateKind)
	}
}

func (wv *WorldView) wasRecentlyServicedLocked(floor int, button int, now time.Time) bool {
	lastServiced := wv.servicedHall.Hall[floor][button]
	if lastServiced.IsZero() {
		return false
	}
	return now.Sub(lastServiced) <= recentlyServicedWindow
}

func (wv *WorldView) mergeWorldView(fromKey string, ns common.Snapshot) { //TODO: maybe since we copy ns, use the new copy to keep synchronizing good
	if fromKey != wv.selfKey {
		wv.lastSnapshot[fromKey] = common.DeepCopySnapshot(ns)
		if wv.inStartupPeriod && ns.UpdateKind == common.UpdateRequests {
			wv.recoverCabRequests(ns)
			wv.inStartupPeriod = false
		}
	}

	wv.mergeHallRequests(ns.HallRequests, ns.UpdateKind)
	wv.snapshot.Alive = wv.CalculateAlive(time.Now())
	for k, st := range ns.States {
		if k == wv.selfKey && fromKey != wv.selfKey && !wv.inStartupPeriod {
			continue
		}

		wv.snapshot.States[k] = st
	}
}

func (wv *WorldView) recoverCabRequests(ns common.Snapshot) {
	peerSelf, ok := ns.States[wv.selfKey]
	if !ok {
		return
	}
	localSelf := wv.snapshot.States[wv.selfKey]
	if len(localSelf.CabRequests) != common.N_FLOORS {
		localSelf.CabRequests = [common.N_FLOORS]bool{}
	}
	for i := range common.N_FLOORS {
		localSelf.CabRequests[i] = localSelf.CabRequests[i] || peerSelf.CabRequests[i]
	}
	wv.snapshot.States[wv.selfKey] = localSelf
}

func (wv *WorldView) mergeHallRequests(incoming [common.N_FLOORS][2]bool, kind common.UpdateKind) {
	for i := range common.N_FLOORS {
		switch kind {
		case common.UpdateServiced:
			wv.snapshot.HallRequests[i][0] = wv.snapshot.HallRequests[i][0] && incoming[i][0]
			wv.snapshot.HallRequests[i][1] = wv.snapshot.HallRequests[i][1] && incoming[i][1]
		case common.UpdateRequests:
			wv.snapshot.HallRequests[i][0] = wv.snapshot.HallRequests[i][0] || incoming[i][0]
			wv.snapshot.HallRequests[i][1] = wv.snapshot.HallRequests[i][1] || incoming[i][1]
		}
	}
}
