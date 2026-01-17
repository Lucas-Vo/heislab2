package elevnetwork

import (
	"elevator/common"
	"encoding/json"
	"log"
	"sync"
	"time"
)

// UpdateKind avoids stringly-typed "AND"/"OR".
type UpdateKind int

const (
	UpdateNewRequests UpdateKind = iota // OR merge hallRequests
	UpdateServiced                      // AND merge hallRequests
	UpdateFromPeer                      // OR merge (default for peer info)
)

// worldView holds shared state + mutex. Helpers are methods (no passing mutex around).
type WorldView struct {
	mu    sync.Mutex
	world common.NetworkState
	seen  map[string]bool
	ready bool

	pm *PeerManager
}

func (wv *WorldView) isCoherentLocked() bool {
	for _, ok := range wv.seen {
		if !ok {
			return false
		}
	}
	return true
}

func (wv *WorldView) MarkReadyIfCoherent(theWorldIsReady chan<- bool) {
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

func (wv *WorldView) MergeHall(dst, src [][2]bool, kind UpdateKind) [][2]bool {
	n := max(len(dst), len(src))
	if n == 0 {
		return nil
	}
	out := make([][2]bool, n)

	switch kind {
	case UpdateServiced:
		// AND elementwise: true && false clears
		for i := 0; i < n; i++ {
			aSet := i < len(dst)
			bSet := i < len(src)

			var a, b [2]bool
			if aSet {
				a = dst[i]
			}
			if bSet {
				b = src[i]
			}

			if aSet && bSet {
				out[i][0] = a[0] && b[0]
				out[i][1] = a[1] && b[1]
			} else if aSet {
				out[i] = a
			} else {
				out[i] = b
			}
		}

	default:
		// OR elementwise: accumulate new info
		for i := 0; i < n; i++ {
			var a, b [2]bool
			if i < len(dst) {
				a = dst[i]
			}
			if i < len(src) {
				b = src[i]
			}
			out[i][0] = a[0] || b[0]
			out[i][1] = a[1] || b[1]
		}
	}

	return out
}

func (wv *WorldView) ApplyUpdate(fromKey string, ns common.NetworkState, kind UpdateKind) {
	wv.mu.Lock()
	defer wv.mu.Unlock()

	// Merge hall requests
	wv.world.HallRequests = wv.MergeHall(wv.world.HallRequests, ns.HallRequests, kind)

	// Merge states (last-write-wins)
	if wv.world.States == nil {
		wv.world.States = make(map[string]common.ElevState)
	}
	for k, st := range ns.States {
		wv.world.States[k] = common.CopyElevState(st)
		wv.seen[k] = true
	}

	// Mark sender as seen if it included its own key (ideal)
	if _, ok := ns.States[fromKey]; ok {
		wv.seen[fromKey] = true
	}
}

func (wv *WorldView) PublishWorld(networkStateOfTheWorld chan<- common.NetworkState) {
	wv.mu.Lock()
	cp := common.DeepCopyNetworkState(wv.world)
	wv.mu.Unlock()

	select {
	case networkStateOfTheWorld <- cp:
	default:
	}
}

func (wv *WorldView) Broadcast(ns common.NetworkState) {
	if wv.pm == nil {
		return
	}
	b, err := json.Marshal(ns)
	if err != nil {
		return
	}
	wv.pm.SendToAll(b, 150*time.Millisecond)
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

func NewWorldView(pm *PeerManager) *WorldView {
	return &WorldView{
		world: common.NetworkState{
			HallRequests: nil,
			States:       make(map[string]common.ElevState),
		},
		seen: make(map[string]bool),
		pm:   pm,
	}
}

func (wv *WorldView) MarkUnseen(id string) {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	wv.seen[id] = false
}
