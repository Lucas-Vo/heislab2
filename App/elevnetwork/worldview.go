// elevnetwork/worldview.go
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
	Origin   string          `json:"origin"`
	Counter  uint64          `json:"counter"`
	Snapshot common.Snapshot `json:"snapshot"`
}

// ---- Transport abstraction (so WorldView only owns ONE handle) ----

type Transport interface {
	SendToAll(net UpdateKind, b []byte, deadline time.Duration)
}

type MuxTransport struct {
	reqPM *PeerManager
	svcPM *PeerManager
}

func NewMuxTransport(reqPM *PeerManager, svcPM *PeerManager) *MuxTransport {
	return &MuxTransport{reqPM: reqPM, svcPM: svcPM}
}

func (mt *MuxTransport) SendToAll(net UpdateKind, b []byte, deadline time.Duration) {
	switch net {
	case UpdateRequests:
		if mt.reqPM != nil {
			mt.reqPM.sendToAll(b, deadline)
		}
	case UpdateServiced:
		if mt.svcPM != nil {
			mt.svcPM.sendToAll(b, deadline)
		}
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

	// set true when received a snapshot or timeout
	ready bool

	selfKey string

	counter     uint64
	latestCount map[string]uint64

	tx Transport
}

func NewWorldView(tx Transport, cfg common.Config) *WorldView {
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

		tx: tx,
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

func (wv *WorldView) extractSnapshot() common.Snapshot {
	wv.mu.Lock()
	defer wv.mu.Unlock()
	return common.DeepCopySnapshot(wv.snapshot)
}

func (wv *WorldView) SnapshotCopy() common.Snapshot {
	return wv.extractSnapshot()
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

	// First contact: accept as "requests" snapshot and recover cab requests
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
		tmp := make([]bool, n)
		copy(tmp, localSelf.CabRequests)
		localSelf.CabRequests = tmp
	}
	for i := 0; i < n; i++ {
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

	refID := wv.selfKey
	refSnap, ok := wv.lastSnapshot[refID]
	if !ok {
		return false
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

// Broadcast constructs a NetMsg from current snapshot and sends it on the correct net for the kind.
func (wv *WorldView) Broadcast(kind UpdateKind) {
	if wv.tx == nil {
		return
	}

	wv.mu.Lock()
	wv.counter++

	snapshot := common.DeepCopySnapshot(wv.snapshot)
	msg := NetMsg{
		Origin:   wv.selfKey,
		Counter:  wv.counter,
		Snapshot: snapshot,
	}
	wv.mu.Unlock()

	wv.sendMsg(kind, msg)
}

// Relay re-broadcasts an already-constructed msg on the SAME net it arrived on.
func (wv *WorldView) Relay(kind UpdateKind, msg NetMsg) {
	if wv.tx == nil {
		return
	}
	wv.sendMsg(kind, msg)
}

func (wv *WorldView) sendMsg(kind UpdateKind, msg NetMsg) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	wv.tx.SendToAll(kind, b, 150*time.Millisecond)
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
