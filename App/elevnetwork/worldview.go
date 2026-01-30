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
	UpdateNewRequests UpdateKind = iota // OR merge
	UpdateServiced                      // AND merge
)

type NetMsg struct {
	Kind   UpdateKind          `json:"kind"`
	Origin string              `json:"origin"`
	Seq    uint64              `json:"seq"`
	State  common.NetworkState `json:"state"`
}

type WorldView struct {
	mu sync.Mutex

	// Configured membership (static, sorted)
	peers []string

	// Our locally merged worldview (what we publish)
	world common.NetworkState

	// Liveness + coherence inputs
	lastHeard    map[string]time.Time
	lastSnapshot map[string]common.NetworkState
	ttl          time.Duration

	// Readiness = initial contact from any peer != self (or forced by timeout)
	ready bool

	selfKey string

	// Dedupe + local seq
	orderCounter uint64
	latestCount  map[string]uint64 // origin -> max seq processed

	pm *PeerManager
}

func NewWorldView(pm *PeerManager, cfg common.Config) *WorldView {
	wv := &WorldView{
		peers: cfg.ExpectedKeys(),

		world: common.NetworkState{
			HallRequests: make([][2]bool, common.N_FLOORS),
			States:       make(map[string]common.ElevState),
		},

		lastHeard:    make(map[string]time.Time),
		lastSnapshot: make(map[string]common.NetworkState),
		ttl:          WV_TIMEOUT_DURATION * time.Second,

		ready:   false,
		selfKey: cfg.SelfKey,

		orderCounter: 0,
		latestCount:  make(map[string]uint64),

		pm: pm,
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

func (wv *WorldView) World() common.NetworkState {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	return common.DeepCopyNetworkState(wv.world)
}

func (wv *WorldView) ShouldAcceptMsg(msg NetMsg) bool {
	wv.mu.Lock()
	defer wv.mu.Unlock()

	maxSeq := wv.latestCount[msg.Origin]
	if msg.Seq <= maxSeq {
		return false
	}
	wv.latestCount[msg.Origin] = msg.Seq
	return true
}

func (wv *WorldView) ApplyUpdate(fromKey string, ns common.NetworkState, kind UpdateKind) {
	wv.mu.Lock()
	defer wv.mu.Unlock()

	now := time.Now()

	// Liveness
	wv.lastHeard[fromKey] = now

	// Readiness = heard someone else.
	// On the first peer contact, force OR-merge semantics.
	if !wv.ready && fromKey != wv.selfKey {
		wv.ready = true
		kind = UpdateNewRequests
	}

	// Save snapshot for coherency checks (deep copy)
	wv.lastSnapshot[fromKey] = common.DeepCopyNetworkState(ns)

	// Merge snapshot into our local worldview
	wv.mergeSnapshot(fromKey, ns, kind)
}

func (wv *WorldView) mergeSnapshot(fromKey string, ns common.NetworkState, kind UpdateKind) {
	// Ensure hall size (defensive)
	if wv.world.HallRequests == nil || len(wv.world.HallRequests) != common.N_FLOORS {
		wv.world.HallRequests = make([][2]bool, common.N_FLOORS)
	}

	// Merge hall requests (OR/AND based on kind)
	wv.world.HallRequests = mergeHall(wv.world.HallRequests, ns.HallRequests, kind)

	// Merge elevator states with ownership rule:
	// - never allow a peer to overwrite our own self state
	if wv.world.States == nil {
		wv.world.States = make(map[string]common.ElevState)
	}

	for k, st := range ns.States {
		if k == wv.selfKey && fromKey != wv.selfKey {
			continue
		}
		wv.world.States[k] = common.CopyElevState(st)
	}
}

func (wv *WorldView) PublishWorld(ch chan<- common.NetworkState) {
	wv.mu.Lock()
	cp := common.DeepCopyNetworkState(wv.world)

	now := time.Now()
	alive := make(map[string]bool, len(wv.peers))
	for _, id := range wv.peers {
		t, ok := wv.lastHeard[id]
		alive[id] = ok && now.Sub(t) <= wv.ttl
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

	refSet := false
	var ref common.NetworkState

	for _, id := range wv.peers {
		t, ok := wv.lastHeard[id]
		alive := ok && now.Sub(t) <= wv.ttl
		if !alive {
			continue
		}

		snap, ok := wv.lastSnapshot[id]
		if !ok {
			return false // alive but no snapshot stored
		}

		if !refSet {
			ref = snap
			refSet = true
			continue
		}

		if !EqualWorldview(ref, snap) {
			return false
		}
	}

	// 0 or 1 alive peers => coherent
	return true
}

func (wv *WorldView) BroadcastWorld(kind UpdateKind) {
	if wv.pm == nil {
		return
	}

	wv.mu.Lock()
	wv.orderCounter++

	snapshot := common.DeepCopyNetworkState(wv.world)

	msg := NetMsg{
		Kind:   kind,
		Origin: wv.selfKey,
		Seq:    wv.orderCounter,
		State:  snapshot,
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
	for i := 0; i < common.N_FLOORS; i++ {
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
func EqualWorldview(a, b common.NetworkState) bool {
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
