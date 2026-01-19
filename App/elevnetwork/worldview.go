package elevnetwork

import (
	"elevator/common"
	"encoding/json"
	"log"
	"sync"
	"time"
)

const (
	WV_TIMEOUT_DURATION = 2
)

type UpdateKind int

const (
	UpdateNewRequests UpdateKind = iota // OR merge
	UpdateExternal                      // OR merge
	UpdateServiced                      // AND merge
)

type WorldView struct {
	mu              sync.Mutex
	world           common.NetworkState
	seen            map[string]bool
	ready           bool
	lastHeard       map[string]time.Time
	aliveTimeToLive time.Duration

	pm *PeerManager
}

func NewWorldView(pm *PeerManager, cfg common.Config) *WorldView {
	expected := cfg.ExpectedKeys()

	seen := make(map[string]bool, len(expected))
	lastHeard := make(map[string]time.Time, len(expected))
	for _, k := range expected {
		seen[k] = false
	}

	return &WorldView{
		world: common.NetworkState{
			HallRequests: nil,
			States:       make(map[string]common.ElevState),
		},
		seen:            seen,
		lastHeard:       lastHeard,
		aliveTimeToLive: WV_TIMEOUT_DURATION * time.Second,
		pm:              pm,
	}
}

func (wv *WorldView) ExpectPeer(id string) {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	if _, ok := wv.seen[id]; !ok {
		wv.seen[id] = false
	}
}

func (wv *WorldView) MarkUnseen(id string) {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	wv.seen[id] = false
}

func (wv *WorldView) Broadcast(ns common.NetworkState) {
	if wv.pm == nil {
		return
	}
	b, err := json.Marshal(ns)
	if err != nil {
		return
	}
	wv.pm.sendToAll(b, 150*time.Millisecond)
}

func (wv *WorldView) MaybeSendSnapshotToFSM(snapshotToFSM chan<- common.NetworkState) bool {
	if snapshotToFSM == nil {
		return false
	}

	wv.mu.Lock()
	ready := wv.ready
	cp := common.DeepCopyNetworkState(wv.world)
	wv.mu.Unlock()

	if !ready {
		return false
	}

	select {
	case snapshotToFSM <- cp:
		log.Printf("Sent snapshot to FSM (post-ready)")
		return true
	default:
		return false
	}
}

func (wv *WorldView) ApplyUpdateAndPublish(
	fromKey string,
	ns common.NetworkState,
	kind UpdateKind,
	theWorldIsReady chan<- bool,
	networkStateOfTheWorld chan<- common.NetworkState,
) {
	wv.applyUpdate(fromKey, ns, kind)
	wv.markReadyIfCoherent(theWorldIsReady)
	wv.publishWorld(networkStateOfTheWorld)
}

func (wv *WorldView) PublishWorld(ch chan<- common.NetworkState) {
	wv.publishWorld(ch)
}

/* helper functions */

func (wv *WorldView) mergeHall(current, incoming [][2]bool, kind UpdateKind) [][2]bool {
	out := make([][2]bool, common.N_FLOORS)

	for i := range common.N_FLOORS {
		if kind == UpdateServiced {
			out[i][0] = current[i][0] && incoming[i][0]
			out[i][1] = current[i][1] && incoming[i][1]
		} else {
			out[i][0] = current[i][0] || incoming[i][0]
			out[i][1] = current[i][1] || incoming[i][1]
		}
	}
	return out
}

func (wv *WorldView) applyUpdate(fromKey string, ns common.NetworkState, kind UpdateKind) {
	wv.mu.Lock()
	defer wv.mu.Unlock()

	// Merge hall requests
	wv.world.HallRequests = wv.mergeHall(wv.world.HallRequests, ns.HallRequests, kind)

	// Merge states (last-write-wins)
	if wv.world.States == nil {
		wv.world.States = make(map[string]common.ElevState)
	}
	switch kind {
	case UpdateExternal:
		// Only accept the sender's own state
		if st, ok := ns.States[fromKey]; ok {
			wv.world.States[fromKey] = common.CopyElevState(st)
			wv.seen[fromKey] = true
			wv.lastHeard[fromKey] = time.Now()
		}
	default:
		// Self updates: accept whatever is included (normally only selfKey anyway)
		for k, st := range ns.States {
			wv.world.States[k] = common.CopyElevState(st)
			wv.seen[k] = true
			wv.lastHeard[k] = time.Now()
		}

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

func (wv *WorldView) markReadyIfCoherent(theWorldIsReady chan<- bool) {
	wv.mu.Lock()
	defer wv.mu.Unlock()

	if wv.ready {
		return
	}
	if wv.isCoherentLocked() {
		wv.ready = true
		select {
		case theWorldIsReady <- true:
		default:
		}
		log.Printf("World view is coherent: ready=true")
	}
}

func (wv *WorldView) isCoherentLocked() bool {
	for _, ok := range wv.seen {
		if !ok {
			return false
		}
	}
	return true
}

func (wv *WorldView) isAliveLocked(id string, now time.Time) bool {
	t, ok := wv.lastHeard[id]
	if !ok {
		return false
	}
	return now.Sub(t) <= wv.aliveTimeToLive
}
