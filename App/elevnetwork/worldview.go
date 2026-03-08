package elevnetwork

import (
	"context"
	"elevator/common"
	"encoding/json"
	"log"
	"sync"
	"time"
)

const (
	wvTimeout              = 6 * time.Second
	recentlyServicedWindow = 4 * time.Second
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
	pm              *Manager
}

func InitWorldView(ctx context.Context, cfg common.Config, port int) (*WorldView, <-chan []byte) {
	pm := NewPeerManager()
	incoming := pm.Start(ctx, cfg, port)
	wv := &WorldView{
		peers: cfg.ExpectedKeys(),
		snapshot: common.Snapshot{
			HallRequests: [common.N_FLOORS][2]bool{},
			States:       make(map[string]common.ElevState),
		},
		lastHeard:    make(map[string]time.Time),
		lastSnapshot: make(map[string]common.Snapshot),
		peerTimeout:  wvTimeout,
		startTime:    time.Now(),
		selfKey:      cfg.SelfKey,
		selfAlive:    true,
		latestCount:  make(map[string]uint64),
		pm:           pm,
	}
	// Broadcast an initial requests snapshot to seed local/remote bookkeeping early.
	initialSnapshot := common.DeepCopySnapshot(wv.snapshot)
	initialSnapshot.UpdateKind = common.UpdateRequests
	wv.sendOverNetwork(initialSnapshot)
	return wv, incoming
}

func (wv *WorldView) JoinedNetwork() bool { return wv.inStartupPeriod }

func (wv *WorldView) EndStartupPeriod() { wv.inStartupPeriod = true }

func (wv *WorldView) SetSelfAlive(alive bool) { wv.selfAlive = alive }

func (wv *WorldView) SelfAlive() bool { return wv.selfAlive }

func (wv *WorldView) PublishLocally(netSnap1Ch, netSnap2Ch chan<- common.Snapshot) {
	snap := wv.GetSnapshot()
	coherent := wv.JoinedNetwork() && (wv.SnapshotsAreCoherent() || snap.UpdateKind == common.UpdateServiced)
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
	snap := common.DeepCopySnapshot(wv.snapshot)
	snap.Alive = wv.CalculateAlive(time.Now())
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
	wv.mu.Unlock()
	if !alive || (kind == common.UpdateRequests && !inStartupPeriod) {
		return
	}
	snap := common.DeepCopySnapshot(wv.snapshot)
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
	frame []byte,
	now time.Time,
) ([]byte, [common.N_FLOORS][2]bool, bool) {
	msg := decodeNetMsg(frame)
	if msg.Origin == "" || msg.Origin == wv.selfKey {
		return frame, [common.N_FLOORS][2]bool{}, false
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
		return frame, serviced, false
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		return frame, [common.N_FLOORS][2]bool{}, false
	}
	return encoded, serviced, true
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

func (wv *WorldView) MergeRemote(frame []byte) {
	msg := decodeNetMsg(frame)

	wv.mu.Lock()
	if msg.Origin == wv.selfKey || msg.Origin == "" {
		wv.mu.Unlock()
		return
	}
	now := time.Now()
	prevCount, seen := wv.latestCount[msg.Origin]
	prevHeard, heard := wv.lastHeard[msg.Origin]
	wv.lastHeard[msg.Origin] = now

	if !seen || msg.Counter > prevCount || !heard || now.Sub(prevHeard) > wv.peerTimeout {
		wv.latestCount[msg.Origin] = msg.Counter
	} else {
		wv.mu.Unlock()
		return
	}
	log.Printf("merge 1  %v", msg.Snapshot.States["1"])
	log.Printf("merge 2  %v", msg.Snapshot.States["2"])
	log.Printf("merge 3  %v", msg.Snapshot.States["3"])
	wv.mergeWorldView(msg.Origin, msg.Snapshot)
	msg.Snapshot = common.DeepCopySnapshot(wv.snapshot)
	b, err := json.Marshal(msg)
	alive := wv.selfAlive
	wv.mu.Unlock()
	if alive && wv.pm != nil {
		wv.pm.Broadcast(b)
	}
}

func (wv *WorldView) BroadcastRequests() {
	alive := wv.selfAlive
	if alive {
		snap := common.DeepCopySnapshot(wv.snapshot)
		log.Printf("broadcast 1  %v", snap.States["1"])
		log.Printf("broadcast 2  %v", snap.States["2"])
		log.Printf("broadcast 3  %v", snap.States["3"])
		snap.UpdateKind = common.UpdateRequests

		wv.sendOverNetwork(snap)
	}
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
	if !wv.selfAlive || wv.pm == nil {
		wv.mu.Unlock()
		return
	}
	wv.counter++
	msg := netMsg{Origin: wv.selfKey, Counter: wv.counter, Snapshot: snap}
	wv.lastHeard[wv.selfKey] = time.Now()
	wv.lastSnapshot[wv.selfKey] = common.DeepCopySnapshot(snap)
	wv.mu.Unlock()
	if b, err := json.Marshal(msg); err == nil {
		wv.pm.Broadcast(b)
	}
}

func decodeNetMsg(frame []byte) netMsg {
	var msg netMsg
	if err := json.Unmarshal(common.TrimZeros(frame), &msg); err != nil {
		log.Printf("Failed to decode NetMsg")
		return netMsg{}
	}
	return msg
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
		if !wv.inStartupPeriod && ns.UpdateKind == common.UpdateRequests {
			wv.recoverCabRequests(ns)
			wv.inStartupPeriod = true
		}
	}

	wv.snapshot.HallRequests = common.MergeHallRequests(wv.snapshot.HallRequests, ns.HallRequests, ns.UpdateKind)
	for k, st := range ns.States {
		if k == wv.selfKey && fromKey != wv.selfKey && wv.inStartupPeriod {
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
