package elevnetwork

import (
	"elevator/common"
	"encoding/json"
	"log"
	"sync"
	"time"
)

const (
	WV_TIMEOUT_DURATION = 10
)

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
	mu              sync.Mutex
	world           common.NetworkState
	seen            map[string]bool
	ready           bool
	readyCond       *sync.Cond
	lastHeard       map[string]time.Time
	aliveTimeToLive time.Duration

	selfKey string
	nextSeq uint64
	seenSeq map[string]uint64 // origin -> max seq processed

	pm *PeerManager
}

func NewWorldView(pm *PeerManager, cfg common.Config) *WorldView {
	wv := &WorldView{
		world: common.NetworkState{
			HallRequests: make([][2]bool, common.N_FLOORS),
			States:       make(map[string]common.ElevState),
		},
		seen:            make(map[string]bool),
		lastHeard:       make(map[string]time.Time),
		aliveTimeToLive: WV_TIMEOUT_DURATION * time.Second,

		selfKey: cfg.SelfKey,
		nextSeq: 0,
		seenSeq: make(map[string]uint64),

		pm: pm,
	}
	wv.readyCond = sync.NewCond(&wv.mu)
	return wv
}

func (wv *WorldView) ExpectPeer(id string) {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	if _, ok := wv.seen[id]; !ok {
		wv.seen[id] = false
	}
}

func (wv *WorldView) BroadcastLocal(kind UpdateKind, ns common.NetworkState) {
	if wv.pm == nil {
		return
	}

	wv.mu.Lock()
	wv.nextSeq++
	msg := NetMsg{
		Kind:   kind,
		Origin: wv.selfKey,
		Seq:    wv.nextSeq,
		State:  ns,
	}
	wv.mu.Unlock()

	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	wv.pm.sendToAll(b, 150*time.Millisecond)
}

func (wv *WorldView) BroadcastMsg(msg NetMsg) {
	if wv.pm == nil {
		return
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	wv.pm.sendToAll(b, 150*time.Millisecond)
}

func (wv *WorldView) ShouldAcceptMsg(msg NetMsg) bool {
	wv.mu.Lock()
	defer wv.mu.Unlock()

	maxSeq := wv.seenSeq[msg.Origin]
	if msg.Seq <= maxSeq {
		return false
	}
	wv.seenSeq[msg.Origin] = msg.Seq
	return true
}

func (wv *WorldView) ApplyUpdateAndPublish(
	fromKey string,
	ns common.NetworkState,
	kind UpdateKind,
	networkStateOfTheWorld chan<- common.NetworkState,
) {
	wv.applyUpdate(fromKey, ns, kind)
	justBecameReady := wv.markReadyIfCoherent()
	if justBecameReady || wv.IsReady() {
		wv.publishWorld(networkStateOfTheWorld)
	}
}

func (wv *WorldView) PublishWorld(ch chan<- common.NetworkState) {
	wv.publishWorld(ch)
}

func (wv *WorldView) IsReady() bool {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	return wv.ready
}

/* helper functions */

func (wv *WorldView) mergeHall(current, incoming [][2]bool, kind UpdateKind) [][2]bool {
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

func (wv *WorldView) applyUpdate(fromKey string, ns common.NetworkState, kind UpdateKind) {
	wv.mu.Lock()
	defer wv.mu.Unlock()

	// Merge hall requests by msg.Kind (OR for new, AND for serviced)
	// Ensure wv.world.HallRequests is fixed-size
	if wv.world.HallRequests == nil || len(wv.world.HallRequests) != common.N_FLOORS {
		wv.world.HallRequests = make([][2]bool, common.N_FLOORS)
	}
	wv.world.HallRequests = wv.mergeHall(wv.world.HallRequests, ns.HallRequests, kind)

	// Merge elevator states: owner-only for peer updates
	if wv.world.States == nil {
		wv.world.States = make(map[string]common.ElevState)
	}

	now := time.Now()

	if fromKey != wv.selfKey {
		// Peer update: accept only sender's own state
		if st, ok := ns.States[fromKey]; ok {
			wv.world.States[fromKey] = common.CopyElevState(st)
			wv.seen[fromKey] = true
			wv.lastHeard[fromKey] = now
		}
		return
	}

	// Local update: accept provided keys (should normally be only self)
	for k, st := range ns.States {
		wv.world.States[k] = common.CopyElevState(st)
		wv.seen[k] = true
		wv.lastHeard[k] = now
	}
}

func (wv *WorldView) publishWorld(ch chan<- common.NetworkState) {
	wv.mu.Lock()
	cp := common.DeepCopyNetworkState(wv.world)

	now := time.Now()
	alive := make(map[string]bool, len(wv.seen))
	for id := range wv.seen {
		t, ok := wv.lastHeard[id]
		alive[id] = ok && now.Sub(t) <= wv.aliveTimeToLive
	}
	cp.Alive = alive

	wv.mu.Unlock()

	select {
	case ch <- cp:
	default:
	}
}

func (wv *WorldView) markReadyIfCoherent() bool {
	wv.mu.Lock()
	defer wv.mu.Unlock()

	if wv.ready {
		return false
	}
	if wv.isCoherentLocked() {
		wv.ready = true

		// Wake anyone waiting on readiness (internal condition, not a channel)
		wv.readyCond.Broadcast()

		log.Printf("World view is coherent: ready=true")
		return true
	}
	return false
}

func (wv *WorldView) isCoherentLocked() bool {
	for _, ok := range wv.seen {
		if !ok {
			return false
		}
	}
	return true
}
