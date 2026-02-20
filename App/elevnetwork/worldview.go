// elevnetwork/worldview.go
package elevnetwork

import (
	"elevator/common"
	"encoding/json"
	"log"
	"reflect"
	"sync"
	"time"
)

const WV_TIMEOUT_DURATION = 4

type NetMsg struct {
	Origin   string          `json:"origin"`
	Counter  uint64          `json:"counter"`
	Snapshot common.Snapshot `json:"snapshot"`
}

// MakeEmptyNetMsg constructs a minimal NetMsg with an empty snapshot of the given kind.
// This helper centralizes the construction so callers don't inline the struct literal.
func MakeEmptyNetMsg(origin string, kind common.UpdateKind) NetMsg {
	return NetMsg{
		Origin:  origin,
		Counter: 0,
		Snapshot: common.Snapshot{
			UpdateKind: kind,
			States:     make(map[string]common.ElevState),
		},
	}
}

// ---- WorldView ----

type WorldView struct {
	mu sync.Mutex

	// Statically configured membership from config.go
	peers []string

	snapshot common.Snapshot

	lastHeard    map[string]time.Time
	lastSnapshot map[string]common.Snapshot
	peerTimeout  time.Duration
	startTime    time.Time

	// set true when received a snapshot or timeout
	ready bool

	selfKey   string
	selfAlive bool

	counter     uint64
	latestCount map[string]uint64

	pm *PeerManager
}

func NewWorldView(pm *PeerManager, cfg common.Config) *WorldView {
	wv := &WorldView{
		peers: cfg.ExpectedKeys(),

		snapshot: common.Snapshot{
			HallRequests: make([][2]bool, common.N_FLOORS),
			States:       make(map[string]common.ElevState),
		},

		lastHeard:    make(map[string]time.Time),
		lastSnapshot: make(map[string]common.Snapshot),
		peerTimeout:  WV_TIMEOUT_DURATION * time.Second,
		startTime:    time.Now(),

		ready:     false,
		selfKey:   cfg.SelfKey,
		selfAlive: true,

		counter:     0,
		latestCount: make(map[string]uint64),
		pm:          pm,
	}
	return wv
}

func (wv *WorldView) IsReady() bool {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	return wv.ready
}

func (wv *WorldView) ForceReady() {
	wv.mu.Lock()
	wv.ready = true
	wv.mu.Unlock()
}

func (wv *WorldView) Snapshot() common.Snapshot {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	return common.DeepCopySnapshot(wv.snapshot)
}

func (wv *WorldView) SetSelfAlive(alive bool) {
	wv.mu.Lock()
	wv.selfAlive = alive
	wv.mu.Unlock()
}

func (wv *WorldView) IsSelfAlive() bool {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	return wv.selfAlive
}

func (wv *WorldView) ShouldAcceptMsg(msg NetMsg) bool {
	wv.mu.Lock()
	defer wv.mu.Unlock()

	if msg.Origin == wv.selfKey {
		return false
	}
	now := time.Now()
	prevHeard, hadPrev := wv.lastHeard[msg.Origin]
	wv.lastHeard[msg.Origin] = now

	maxcounter := wv.latestCount[msg.Origin]
	if msg.Counter <= maxcounter {
		if !hadPrev || now.Sub(prevHeard) > wv.peerTimeout {
			// Accept counter resets after silence.
			wv.latestCount[msg.Origin] = msg.Counter
			return true
		}
		return false
	}
	wv.latestCount[msg.Origin] = msg.Counter
	return true
}

func (wv *WorldView) ApplyUpdate(fromKey string, ns common.Snapshot) (becameReady bool) {
	wv.mu.Lock()
	defer wv.mu.Unlock()

	wv.lastHeard[fromKey] = time.Now()
	wv.lastSnapshot[fromKey] = common.DeepCopySnapshot(ns)

	// First contact: accept as "requests" snapshot and recover cab requests
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
		// never overwrite our local self state with a remote copy
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

	n := len(peerSelf.CabRequests)
	if len(localSelf.CabRequests) < n {
		cabRequestsCopy := make([]bool, n)
		copy(cabRequestsCopy, localSelf.CabRequests)
		localSelf.CabRequests = cabRequestsCopy
	}
	for i := 0; i < n; i++ {
		localSelf.CabRequests[i] = localSelf.CabRequests[i] || peerSelf.CabRequests[i]
	}

	wv.snapshot.States[wv.selfKey] = localSelf
}

func (wv *WorldView) PublishWorld(channel chan<- common.Snapshot) {
	wv.mu.Lock()

	now := time.Now()
	snapshotCopy := common.DeepCopySnapshot(wv.snapshot)
	snapshotCopy.Alive = wv.aliveMapLocked(now)

	wv.mu.Unlock()
	select {
	case channel <- snapshotCopy:
	default:
	}
}

func (wv *WorldView) aliveMapLocked(now time.Time) map[string]bool {
	alive := make(map[string]bool, len(wv.peers))
	startupGrace := now.Sub(wv.startTime) <= wv.peerTimeout

	for _, id := range wv.peers {
		if id == wv.selfKey {
			alive[id] = wv.selfAlive
			continue
		}
		t, ok := wv.lastHeard[id]
		if ok {
			alive[id] = now.Sub(t) <= wv.peerTimeout
			continue
		}
		alive[id] = startupGrace
	}

	return alive
}

func (wv *WorldView) IsCoherent() bool {
	wv.mu.Lock()
	defer wv.mu.Unlock()

	now := time.Now()
	aliveIDs := wv.aliveIDsLocked(now)
	if len(aliveIDs) <= 1 {
		return true
	}

	refID := wv.selfKey
	if !containsID(aliveIDs, refID) {
		refID = aliveIDs[0]
	}
	refSnap, ok := wv.lastSnapshot[refID]
	if !ok {
		return false
	}

	for _, id := range aliveIDs {
		if id == refID {
			continue
		}
		snap, ok := wv.lastSnapshot[id]
		if !ok {
			return false
		}
		if !equalWorldview(refSnap, snap) {
			return false
		}
	}

	return true
}

// Broadcast constructs a NetMsg from current snapshot and sends it on the correct net for the kind.
func (wv *WorldView) Broadcast(kind common.UpdateKind) {
	wv.mu.Lock()
	if wv.pm == nil || !wv.selfAlive { // TODO: this could be part of send gating later
		wv.mu.Unlock()
		return
	}
	wv.counter++
	now := time.Now()

	snapshot := common.DeepCopySnapshot(wv.snapshot)
	wv.lastHeard[wv.selfKey] = now // TODO: Widdewavvy this line ahah
	wv.lastSnapshot[wv.selfKey] = snapshot
	msg := NetMsg{
		Origin:   wv.selfKey,
		Counter:  wv.counter,
		Snapshot: snapshot,
	}
	wv.mu.Unlock()

	wv.sendMsg(msg)
	log.Printf("BROADCASTING")
}

// Relay re-broadcasts an already-constructed msg on the SAME net it arrived on.
func (wv *WorldView) Relay(msg NetMsg) { //TODO Combine Relay and sendMsg into same function bruhh. Also broadcast checks for alive as well as relay so what the FUCK is the difference and please make this more compact
	if !wv.IsSelfAlive() {
		return
	}
	wv.sendMsg(msg)
	log.Printf("RELAYING")
}

func (wv *WorldView) sendMsg(msg NetMsg) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	wv.pm.sendToAll(b, 150*time.Millisecond) //TODO: This recursion will bust my balls
}

func mergeHall(current, incomingHall [][2]bool, kind common.UpdateKind) [][2]bool { // TODO: Do we need to copy incomingHall? Is this needed to make it thread safe?
	inc := make([][2]bool, common.N_FLOORS)
	copy(inc, incomingHall)

	mergedHall := make([][2]bool, common.N_FLOORS)
	for i := 0; i < common.N_FLOORS; i++ {
		if kind == common.UpdateServiced {
			mergedHall[i][0] = current[i][0] && inc[i][0]
			mergedHall[i][1] = current[i][1] && inc[i][1]
		} else {
			mergedHall[i][0] = current[i][0] || inc[i][0]
			mergedHall[i][1] = current[i][1] || inc[i][1]
		}
	}
	return mergedHall
}
func equalWorldview(a, b common.Snapshot) bool {
	for i := 0; i < len(a.HallRequests); i++ {
		if a.HallRequests[i] != b.HallRequests[i] {
			return false
		}
	}

	for k, aSt := range a.States {
		bSt, ok := b.States[k]
		if !ok {
			return false
		}
		if !reflect.DeepEqual(aSt, bSt) {
			return false
		}
	}
	return true
}

func (wv *WorldView) aliveIDsLocked(now time.Time) []string {
	aliveMap := wv.aliveMapLocked(now)
	alive := make([]string, 0, len(wv.peers))
	for _, id := range wv.peers {
		if aliveMap[id] {
			alive = append(alive, id)
		}
	}
	return alive
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
