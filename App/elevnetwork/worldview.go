package elevnetwork

import (
	"elevator/common"
	"encoding/json"
	"reflect"
	"sync"
	"time"
)

const WV_TIMEOUT_DURATION = 10

type UpdateKind int

const (
	UpdateRequests UpdateKind = iota // OR merge
	UpdateServiced                   // AND merge
)

type NetMsg struct {
	Kind     UpdateKind      `json:"kind"`
	Origin   string          `json:"origin"`
	Counter  uint64          `json:"counter"`
	Snapshot common.Snapshot `json:"snapshot"`
}

type WorldView struct {
	mu sync.Mutex

	// Statically configured membership from config.go
	peers []string

	snapshot common.Snapshot

	lastHeard    map[string]time.Time
	lastSnapshot map[string]common.Snapshot
	peerTimeout  time.Duration

	// set true when received a snapshot or timeout
	ready bool

	selfKey string

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

		ready:   false,
		selfKey: cfg.SelfKey,

		counter:     0,
		latestCount: make(map[string]uint64),

		pm: pm,
	}
	wv.snapshot.States = make(map[string]common.ElevState)
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

func (wv *WorldView) extractSnapshot() common.Snapshot {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	return common.DeepCopySnapshot(wv.snapshot)
}

func (wv *WorldView) ShouldAcceptMsg(msg NetMsg) bool {
	wv.mu.Lock()
	defer wv.mu.Unlock()

	maxcounter := wv.latestCount[msg.Origin]
	if msg.Counter <= maxcounter {
		return false
	}
	wv.latestCount[msg.Origin] = msg.Counter
	return true
}

func (wv *WorldView) ApplyUpdate(fromKey string, ns common.Snapshot, kind UpdateKind) {
	wv.mu.Lock()
	defer wv.mu.Unlock()

	wv.lastHeard[fromKey] = time.Now()
	wv.lastSnapshot[fromKey] = common.DeepCopySnapshot(ns)

	if !wv.ready && fromKey != wv.selfKey {
		kind = UpdateRequests
		wv.recoverCabRequests(ns)
		wv.ready = true
	}

	wv.mergeSnapshot(fromKey, ns, kind)
}

func (wv *WorldView) mergeSnapshot(fromKey string, ns common.Snapshot, kind UpdateKind) {

	wv.snapshot.HallRequests = mergeHall(wv.snapshot.HallRequests, ns.HallRequests, kind)

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

	n := len(peerSelf.CabRequests)
	if len(localSelf.CabRequests) < n {
		tmp := make([]bool, n)
		copy(tmp, localSelf.CabRequests)
		localSelf.CabRequests = tmp
	}
	for i := range n {
		localSelf.CabRequests[i] = localSelf.CabRequests[i] || peerSelf.CabRequests[i]
	}

	wv.snapshot.States[wv.selfKey] = localSelf
}

func (wv *WorldView) PublishWorld(ch chan<- common.Snapshot) {
	wv.mu.Lock()
	cp := common.DeepCopySnapshot(wv.snapshot)

	now := time.Now()
	alive := make(map[string]bool, len(wv.peers))
	for _, id := range wv.peers {
		t, ok := wv.lastHeard[id]
		alive[id] = ok && now.Sub(t) <= wv.peerTimeout
	}
	cp.Alive = alive
	wv.mu.Unlock()

	select {
	case ch <- cp:
	default:
	}
}

func (wv *WorldView) IsCoherent() bool {
	wv.mu.Lock()
	defer wv.mu.Unlock()

	now := time.Now()

	alive := make([]string, 0, len(wv.peers))
	for _, id := range wv.peers {
		t, ok := wv.lastHeard[id]
		if ok && now.Sub(t) <= wv.peerTimeout {
			alive = append(alive, id)
		}
	}

	if len(alive) <= 1 {
		return true
	}

	refID := alive[0]
	refSnap, ok := wv.lastSnapshot[refID]
	if !ok {
		return false // alive but no snapshot stored
	}

	for _, id := range alive[1:] {
		snap, ok := wv.lastSnapshot[id]
		if !ok {
			return false
		}
		if !EqualWorldview(refSnap, snap) {
			return false
		}
	}

	return true
}

func (wv *WorldView) BroadcastWorld(kind UpdateKind) {
	if wv.pm == nil {
		return
	}

	wv.mu.Lock()
	wv.counter++

	snapshot := common.DeepCopySnapshot(wv.snapshot)

	msg := NetMsg{
		Kind:     kind,
		Origin:   wv.selfKey,
		Counter:  wv.counter,
		Snapshot: snapshot,
	}
	wv.mu.Unlock()

	wv.sendMsg(msg)
}

func (wv *WorldView) RelayMsg(msg NetMsg) {
	if wv.pm == nil {
		return
	}
	wv.sendMsg(msg)
}

func (wv *WorldView) sendMsg(msg NetMsg) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	wv.pm.sendToAll(b, 150*time.Millisecond)
}

func mergeHall(current, incoming [][2]bool, kind UpdateKind) [][2]bool {
	inc := make([][2]bool, common.N_FLOORS)
	copy(inc, incoming)

	out := make([][2]bool, common.N_FLOORS)
	for i := range common.N_FLOORS {
		if kind == UpdateServiced {
			out[i][0] = current[i][0] && inc[i][0]
			out[i][1] = current[i][1] && inc[i][1]
		} else {
			out[i][0] = current[i][0] || inc[i][0]
			out[i][1] = current[i][1] || inc[i][1]
		}
	}
	return out
}

// Ignore Alive; compare HallRequests + States.
func EqualWorldview(a, b common.Snapshot) bool {
	if len(a.HallRequests) != len(b.HallRequests) {
		return false
	}
	for i := 0; i < len(a.HallRequests); i++ {
		if a.HallRequests[i] != b.HallRequests[i] {
			return false
		}
	}

	if len(a.States) != len(b.States) {
		return false
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
