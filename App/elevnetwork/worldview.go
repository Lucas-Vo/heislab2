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
	WV_TIMEOUT           = 6 * time.Second
	VALID_SERVICE_WINDOW = 2 * time.Second
	NETWORK_CHAN_SIZE    = 128
	VALID_COUNTER_WINDOW = 20
)

type netMsg struct {
	Origin   string          `json:"origin"`
	Counter  uint64          `json:"counter"`
	Snapshot common.Snapshot `json:"snapshot"`
}

type WorldView struct {
	mu               sync.Mutex
	peers            []string
	snapshot         common.Snapshot
	lastServicedHall [common.N_FLOORS][2]time.Time
	lastHeard        map[string]time.Time
	lastSnapshot     map[string]common.Snapshot
	peerTimeout      time.Duration
	startTime        time.Time
	inStartupPeriod  bool
	selfKey          string
	selfAlive        bool
	msgCounter       uint64
	latestCount      map[string]uint64
	msgTxCh          chan<- netMsg
}

func InitWorldView(
	ctx context.Context,
	cfg common.Config,
) (*WorldView, <-chan netMsg, <-chan peers.PeerUpdate) {
	incoming := make(chan netMsg, NETWORK_CHAN_SIZE)
	outgoing := make(chan netMsg, NETWORK_CHAN_SIZE)
	peerUpdateCh := make(chan peers.PeerUpdate, NETWORK_CHAN_SIZE)
	peerTxEnable := make(chan bool, 1)

	go peers.Transmitter(cfg.PeerPort, cfg.SelfKey, peerTxEnable)
	go peers.Receiver(cfg.PeerPort, peerUpdateCh)
	go bcast.Transmitter(cfg.MsgPort, outgoing)
	go bcast.Receiver(cfg.MsgPort, incoming)

	wv := &WorldView{
		peers: cfg.ExpectedKeys(),
		snapshot: common.Snapshot{
			HallRequests: [common.N_FLOORS][2]bool{},
			States:       make(map[string]common.ElevState),
			Alive:        make(map[string]bool),
		},
		lastHeard:       make(map[string]time.Time),
		lastSnapshot:    make(map[string]common.Snapshot),
		peerTimeout:     WV_TIMEOUT,
		startTime:       time.Now(),
		selfKey:         cfg.SelfKey,
		selfAlive:       true,
		latestCount:     make(map[string]uint64),
		inStartupPeriod: true,
		msgTxCh:         outgoing,
	}
	wv.CalculateAlive(time.Now())

	initialSnapshot := common.DeepCopySnapshot(wv.snapshot)
	initialSnapshot.UpdateKind = common.UpdateRequests
	wv.sendOverNetwork(initialSnapshot)

	return wv, incoming, peerUpdateCh
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
	snap := common.DeepCopySnapshot(wv.snapshot)
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
	selfSnapshot, hasSelfSnapshot := wv.lastSnapshot[wv.selfKey]
	if !hasSelfSnapshot {
		return false
	}
	selfViewOfSelf, hasSelfViewOfSelf := selfSnapshot.States[wv.selfKey]
	if !hasSelfViewOfSelf {
		return false
	}
	for _, peerID := range wv.peers {
		if peerID == wv.selfKey || !wv.snapshot.Alive[peerID] {
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
	defer wv.mu.Unlock()
	wv.mergeWorldView(wv.selfKey, ns)
	wv.snapshot.UpdateKind = ns.UpdateKind
}

func (wv *WorldView) MarkRecentlyServicedHalls(ns common.Snapshot, now time.Time) {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	for floor := range common.N_FLOORS {
		for button := 0; button < 2; button++ {
			if wv.snapshot.HallRequests[floor][button] && !ns.HallRequests[floor][button] {
				wv.lastServicedHall[floor][button] = now
			}
		}
	}
}

func (wv *WorldView) FilterRecentlyServicedHalls(msg netMsg, now time.Time) (netMsg, [common.N_FLOORS][2]bool, bool) {
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
	prevCounter := wv.latestCount[msg.Origin]
	prevHeard := wv.lastHeard[msg.Origin]
	wv.lastHeard[msg.Origin] = now
	if now.Sub(prevHeard) < wv.peerTimeout && msg.Counter < prevCounter && msg.Counter > prevCounter-VALID_COUNTER_WINDOW {
		log.Printf("drop stale/duplicate frame origin=%s counter=%d prevCounter=%d dt=%s", msg.Origin, msg.Counter, prevCounter, now.Sub(prevHeard))
		return
	}
	wv.latestCount[msg.Origin] = msg.Counter
	wv.mergeWorldView(msg.Origin, msg.Snapshot)
}

func (wv *WorldView) BroadcastRequests() {
	wv.mu.Lock()
	alive := wv.selfAlive
	snap := common.DeepCopySnapshot(wv.snapshot)
	wv.mu.Unlock()
	if !alive || (snap.UpdateKind == common.UpdateRequests && wv.inStartupPeriod) {
		return
	}
	snap.UpdateKind = common.UpdateRequests
	wv.sendOverNetwork(snap)
}

func (wv *WorldView) CalculateAlive(now time.Time) {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	startupGrace := now.Sub(wv.startTime) <= wv.peerTimeout
	for _, id := range wv.peers {
		if id == wv.selfKey {
			wv.snapshot.Alive[id] = wv.selfAlive
			continue
		}
		if t, ok := wv.lastHeard[id]; ok {
			wv.snapshot.Alive[id] = now.Sub(t) <= wv.peerTimeout
			continue
		}
		wv.snapshot.Alive[id] = startupGrace
	}
}

func (wv *WorldView) sendOverNetwork(snap common.Snapshot) {
	wv.mu.Lock()
	if !wv.selfAlive || wv.msgTxCh == nil {
		wv.mu.Unlock()
		return
	}
	wv.msgCounter++
	msg := netMsg{Origin: wv.selfKey, Counter: wv.msgCounter, Snapshot: snap}
	wv.lastHeard[wv.selfKey] = time.Now()
	wv.lastSnapshot[wv.selfKey] = common.DeepCopySnapshot(snap)
	wv.mu.Unlock()

	select {
	case wv.msgTxCh <- msg:
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

func (wv *WorldView) mergeWorldView(fromKey string, ns common.Snapshot) { //TODO: maybe since we copy ns, use the new copy to keep synchronizing good
	if fromKey != wv.selfKey {
		wv.lastSnapshot[fromKey] = common.DeepCopySnapshot(ns)
		if wv.inStartupPeriod && ns.UpdateKind == common.UpdateRequests {
			wv.recoverCabRequests(ns)
			wv.inStartupPeriod = false
		}
	}

	wv.mergeHallRequests(ns.HallRequests, ns.UpdateKind)
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
