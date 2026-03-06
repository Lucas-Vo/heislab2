package elevnetwork

import (
	"context"
	"elevator/common"
	"encoding/json"
	"log"
	"sync"
	"time"
)

const wvTimeout = 4 * time.Second

type netMsg struct {
	Origin   string          `json:"origin"`
	Counter  uint64          `json:"counter"`
	Snapshot common.Snapshot `json:"snapshot"`
}

type WorldView struct {
	mu           sync.Mutex
	peers        []string
	snapshot     common.Snapshot
	lastHeard    map[string]time.Time
	lastSnapshot map[string]common.Snapshot
	peerTimeout  time.Duration
	startTime    time.Time
	ready        bool
	selfKey      string
	selfAlive    bool
	counter      uint64
	latestCount  map[string]uint64
	pm           *Manager
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

func (wv *WorldView) Ready() bool { return wv.ready }

func (wv *WorldView) ForceReady() { wv.ready = true }

func (wv *WorldView) SetSelfAlive(alive bool) { wv.selfAlive = alive }

func (wv *WorldView) SelfAlive() bool { return wv.selfAlive }

func (wv *WorldView) PublishAll(netSnap1Ch, netSnap2Ch chan<- common.Snapshot) {
	snap := wv.GetSnapshot()
	if wv.SnapshotsAreCoherent() {
		snap1 := common.DeepCopySnapshot(snap)
		select {
		case netSnap1Ch <- snap1:
		default:
		}
	}
	snap2 := common.DeepCopySnapshot(snap)
	select {
	case netSnap2Ch <- snap2:
	default:
	}
}

func (wv *WorldView) GetSnapshot() common.Snapshot {
	wv.mu.Lock()
	snap := common.DeepCopySnapshot(wv.snapshot)
	snap.Alive = wv.calculateAlive(time.Now())
	wv.mu.Unlock()
	return snap
}

func (wv *WorldView) SnapshotsAreCoherent() bool {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	alive := wv.calculateAlive(time.Now())
	var ref common.Snapshot
	hasRef := false
	for _, id := range wv.peers {
		if !alive[id] {
			continue
		}
		snap, ok := wv.lastSnapshot[id]
		if !ok {
			return false
		}
		if !hasRef {
			ref = snap
			hasRef = true
			continue
		}
		if !common.SnapshotsEqual(ref, snap, alive, wv.peers) {
			return false
		}
	}
	return true
}

func (wv *WorldView) MergeLocal(ns common.Snapshot) {
	wv.mu.Lock()
	wv.mergeWorldView(wv.selfKey, ns)
	ready, alive, kind := wv.ready, wv.selfAlive, ns.UpdateKind
	wv.mu.Unlock()
	if !alive || (kind == common.UpdateRequests && !ready) {
		return
	}
	snap := common.DeepCopySnapshot(wv.snapshot)
	snap.UpdateKind = kind
	wv.sendOverNetwork(snap)
}

func (wv *WorldView) MergeRemote(frame []byte) (common.UpdateKind, bool) {
	msg := decodeNetMsg(frame)

	wv.mu.Lock()
	if msg.Origin == wv.selfKey || msg.Origin == "" {
		wv.mu.Unlock()
		return msg.Snapshot.UpdateKind, false
	}
	now := time.Now()
	prevCount, seen := wv.latestCount[msg.Origin]
	prevHeard, heard := wv.lastHeard[msg.Origin]
	wv.lastHeard[msg.Origin] = now
	if !seen || msg.Counter > prevCount || !heard || now.Sub(prevHeard) > wv.peerTimeout {
		wv.latestCount[msg.Origin] = msg.Counter
	} else {
		wv.mu.Unlock()
		return msg.Snapshot.UpdateKind, false
	}
	becameReady := wv.mergeWorldView(msg.Origin, msg.Snapshot)
	alive := wv.selfAlive
	pm := wv.pm
	wv.mu.Unlock()
	if alive && pm != nil {
		pm.Broadcast(frame)
	}
	return msg.Snapshot.UpdateKind, becameReady
}

func (wv *WorldView) BroadcastRequests() {
	alive := wv.selfAlive
	if alive {
		snap := common.DeepCopySnapshot(wv.snapshot)
		snap.UpdateKind = common.UpdateRequests
		wv.sendOverNetwork(snap)
	}
}

func (wv *WorldView) calculateAlive(now time.Time) map[string]bool {
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

func (wv *WorldView) mergeWorldView(fromKey string, ns common.Snapshot) (becameReady bool) {
	wv.lastHeard[fromKey] = time.Now()
	if fromKey != wv.selfKey {
		wv.lastSnapshot[fromKey] = common.DeepCopySnapshot(ns)
		if !wv.ready && ns.UpdateKind == common.UpdateRequests {
			wv.recoverCabRequests(ns)
			wv.ready = true
			becameReady = true
		}
	}

	wv.snapshot.HallRequests = common.MergeHallRequests(wv.snapshot.HallRequests, ns.HallRequests, ns.UpdateKind)
	for k, st := range ns.States {
		if k == wv.selfKey && fromKey != wv.selfKey && wv.ready {
			wv.ready = true
			continue
		}
		wv.snapshot.States[k] = st
	}
	return becameReady
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
