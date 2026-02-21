package elevnetwork

import (
	"context"
	"elevator/common"
	"encoding/json"
	"sync"
	"time"
)

const wvTimeout = 4 * time.Second

type sender interface{ Broadcast([]byte) }

type netMsg struct {
	Origin   string          `json:"origin"`
	Counter  uint64          `json:"counter"`
	Snapshot common.Snapshot `json:"snapshot"`
}

type WorldView struct {
	mu          sync.Mutex
	peers       []string
	snapshot    common.Snapshot
	lastHeard   map[string]time.Time
	lastDigest  map[string]uint64
	peerTimeout time.Duration
	startTime   time.Time
	ready       bool
	selfKey     string
	selfAlive   bool
	counter     uint64
	latestCount map[string]uint64
	sender      sender
}

func Start(ctx context.Context, cfg common.Config, port int) (*WorldView, <-chan []byte) {
	pm := NewPeerManager()
	incoming := pm.Start(ctx, cfg, port)
	wv := newWorldView(pm, cfg)
	return wv, incoming
}

func newWorldView(s sender, cfg common.Config) *WorldView {
	return &WorldView{
		peers: cfg.ExpectedKeys(),
		snapshot: common.Snapshot{
			HallRequests: make([][2]bool, common.N_FLOORS),
			States:       make(map[string]common.ElevState),
		},
		lastHeard:   make(map[string]time.Time),
		lastDigest:  make(map[string]uint64),
		peerTimeout: wvTimeout,
		startTime:   time.Now(),
		selfKey:     cfg.SelfKey,
		selfAlive:   true,
		latestCount: make(map[string]uint64),
		sender:      s,
	}
}

func (wv *WorldView) Ready() bool { wv.mu.Lock(); defer wv.mu.Unlock(); return wv.ready }

func (wv *WorldView) ForceReady() { wv.mu.Lock(); wv.ready = true; wv.mu.Unlock() }

func (wv *WorldView) Snapshot() common.Snapshot {
	wv.mu.Lock()
	snap := common.DeepCopySnapshot(wv.snapshot)
	snap.Alive = wv.aliveMapLocked(time.Now())
	wv.mu.Unlock()
	return snap
}

func (wv *WorldView) Coherent() bool {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	return wv.snapshotsAgreeLocked()
}

func (wv *WorldView) SetSelfAlive(alive bool) { wv.mu.Lock(); wv.selfAlive = alive; wv.mu.Unlock() }

func (wv *WorldView) SelfAlive() bool { wv.mu.Lock(); defer wv.mu.Unlock(); return wv.selfAlive }

func (wv *WorldView) HandleLocal(ns common.Snapshot) {
	wv.mu.Lock()
	wv.applyLocked(wv.selfKey, ns)
	ready, alive, kind := wv.ready, wv.selfAlive, ns.UpdateKind
	wv.mu.Unlock()
	if !alive || (kind == common.UpdateRequests && !ready) {
		return
	}
	wv.broadcast(kind)
}

func (wv *WorldView) HandleRemoteFrame(frame []byte) (common.UpdateKind, bool, bool) {
	msg, ok := decodeNetMsg(frame)
	if !ok {
		return 0, false, false
	}
	wv.mu.Lock()
	if !wv.acceptLocked(msg) {
		wv.mu.Unlock()
		return msg.Snapshot.UpdateKind, false, false
	}
	becameReady := wv.applyLocked(msg.Origin, msg.Snapshot)
	alive := wv.selfAlive
	wv.mu.Unlock()
	if alive {
		wv.send(msg)
	}
	return msg.Snapshot.UpdateKind, becameReady, true
}

func (wv *WorldView) Tick() {
	wv.mu.Lock()
	ready, alive := wv.ready, wv.selfAlive
	wv.mu.Unlock()
	if ready && alive {
		wv.broadcast(common.UpdateRequests)
	}
}

func (wv *WorldView) Poke() {
	wv.sendSnapshot(common.Snapshot{
		UpdateKind:   common.UpdateRequests,
		HallRequests: make([][2]bool, common.N_FLOORS),
		States:       map[string]common.ElevState{},
	})
}

func (wv *WorldView) broadcast(kind common.UpdateKind) {
	snap := common.DeepCopySnapshot(wv.snapshot)
	snap.UpdateKind = kind
	wv.sendSnapshot(snap)
}

func (wv *WorldView) sendSnapshot(snap common.Snapshot) {
	wv.mu.Lock()
	if wv.sender == nil || !wv.selfAlive {
		wv.mu.Unlock()
		return
	}
	wv.counter++
	msg := netMsg{Origin: wv.selfKey, Counter: wv.counter, Snapshot: snap}
	wv.lastHeard[wv.selfKey] = time.Now()
	wv.lastDigest[wv.selfKey] = wv.snapshotDigest(snap)
	wv.mu.Unlock()
	wv.send(msg)
}

func (wv *WorldView) send(msg netMsg) {
	if b, err := json.Marshal(msg); err == nil {
		wv.sender.Broadcast(b)
	}
}

func decodeNetMsg(frame []byte) (netMsg, bool) {
	var msg netMsg
	if err := json.Unmarshal(common.TrimZeros(frame), &msg); err != nil {
		return netMsg{}, false
	}
	return msg, true
}

func (wv *WorldView) acceptLocked(msg netMsg) bool {
	if msg.Origin == wv.selfKey || msg.Origin == "" {
		return false
	}
	now := time.Now()
	prevCount, seen := wv.latestCount[msg.Origin]
	prevHeard, heard := wv.lastHeard[msg.Origin]
	wv.lastHeard[msg.Origin] = now
	if !seen || msg.Counter > prevCount || !heard || now.Sub(prevHeard) > wv.peerTimeout {
		wv.latestCount[msg.Origin] = msg.Counter
		return true
	}
	return false
}

func (wv *WorldView) applyLocked(fromKey string, ns common.Snapshot) (becameReady bool) {
	wv.lastHeard[fromKey] = time.Now()
	if fromKey != wv.selfKey {
		wv.lastDigest[fromKey] = wv.snapshotDigest(ns)
	}
	if !wv.ready && fromKey != wv.selfKey && ns.UpdateKind == common.UpdateRequests {
		wv.recoverCabRequests(ns)
		wv.ready = true
		becameReady = true
	}
	wv.mergeSnapshot(fromKey, ns)
	return becameReady
}

func (wv *WorldView) mergeSnapshot(fromKey string, ns common.Snapshot) {
	wv.snapshot.HallRequests = mergeHall(wv.snapshot.HallRequests, ns.HallRequests, ns.UpdateKind)
	for k, st := range ns.States {
		if k == wv.selfKey && fromKey != wv.selfKey {
			continue
		}
		wv.snapshot.States[k] = common.CopyElevState(st)
	}
}

func (wv *WorldView) recoverCabRequests(ns common.Snapshot) {
	peerSelf, ok := ns.States[wv.selfKey]
	if !ok {
		return
	}
	localSelf := wv.snapshot.States[wv.selfKey]
	if len(localSelf.CabRequests) != common.N_FLOORS {
		localSelf.CabRequests = make([]bool, common.N_FLOORS)
	}
	for i := 0; i < common.N_FLOORS; i++ {
		localSelf.CabRequests[i] = localSelf.CabRequests[i] || peerSelf.CabRequests[i]
	}
	wv.snapshot.States[wv.selfKey] = localSelf
}

func (wv *WorldView) aliveMapLocked(now time.Time) map[string]bool {
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

func (wv *WorldView) snapshotsAgreeLocked() bool {
	alive := wv.aliveMapLocked(time.Now())
	for _, id := range wv.peers {
		if !alive[id] {
			continue
		}
		ref, ok := wv.lastDigest[id]
		if !ok {
			return false
		}
		for _, other := range wv.peers {
			if !alive[other] || other == id {
				continue
			}
			d, ok := wv.lastDigest[other]
			if !ok || d != ref {
				return false
			}
		}
		return true
	}
	return true
}

const (
	digestOffset = 1469598103934665603
	digestPrime  = 1099511628211
)

func (wv *WorldView) snapshotDigest(s common.Snapshot) uint64 {
	h := uint64(digestOffset)
	for i := 0; i < common.N_FLOORS; i++ {
		if s.HallRequests[i][0] {
			h ^= 1
		}
		h *= digestPrime
		if s.HallRequests[i][1] {
			h ^= 1
		}
		h *= digestPrime
	}
	for _, id := range wv.peers {
		st, ok := s.States[id]
		if !ok {
			h ^= 0xff
			h *= digestPrime
			continue
		}
		for i := 0; i < len(st.Behavior); i++ {
			h ^= uint64(st.Behavior[i])
			h *= digestPrime
		}
		for i := 0; i < len(st.Direction); i++ {
			h ^= uint64(st.Direction[i])
			h *= digestPrime
		}
		h ^= uint64(st.Floor + 1000)
		h *= digestPrime
	}
	return h
}

func mergeHall(current, incoming [][2]bool, kind common.UpdateKind) [][2]bool {
	merged := make([][2]bool, common.N_FLOORS)
	for i := 0; i < common.N_FLOORS; i++ {
		if kind == common.UpdateServiced {
			merged[i][0] = current[i][0] && incoming[i][0]
			merged[i][1] = current[i][1] && incoming[i][1]
		} else {
			merged[i][0] = current[i][0] || incoming[i][0]
			merged[i][1] = current[i][1] || incoming[i][1]
		}
	}
	return merged
}
