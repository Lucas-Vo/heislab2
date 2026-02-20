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
	SelfAlive bool

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

		ready:   false,
		selfKey: cfg.SelfKey,

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

func (wv *WorldView) ApplyUpdate(fromKey string, ns common.Snapshot, kind common.UpdateKind) (becameReady bool) {
	wv.mu.Lock()
	defer wv.mu.Unlock()

	wv.lastHeard[fromKey] = time.Now()
	wv.lastSnapshot[fromKey] = common.DeepCopySnapshot(ns)

	// First contact: accept as "requests" snapshot and recover cab requests
	DELETE := false
	if !wv.ready && fromKey != wv.selfKey && ns.UpdateKind == common.UpdateRequests {
		log.Printf("Suggested recovered cab BEFORE is: %v", wv.snapshot.States[wv.selfKey].CabRequests)
		log.Printf("INBOUND cabs from peer is: %v", ns.States[wv.selfKey].CabRequests)
		wv.recoverCabRequests(ns)
		wv.ready = true
		becameReady = true
		DELETE = true
	}
	wv.mergeSnapshot(fromKey, ns)
	if DELETE {
		log.Printf("Suggested recovered cab AFTER is: %v", wv.snapshot.States[wv.selfKey].CabRequests)
		DELETE = false
	}
	if fromKey != wv.selfKey && ns.UpdateKind == common.UpdateServiced {
		log.Printf("APPLIED SERVICED ############")
	}
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
	log.Printf("recoverCabRequests entered")
	log.Printf("")
	peerSelf, ok := ns.States[wv.selfKey]
	if !ok {
		log.Printf("skipped recoverCabRequests due to lack of external")
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
	log.Printf("sucessfully recoverCabRequests")
}

func (wv *WorldView) PublishWorld(channel chan<- common.Snapshot) {
	wv.mu.Lock()

	now := time.Now()
	snapshotCopy := common.DeepCopySnapshot(wv.snapshot)
	snapshotCopy.Alive = wv.computeAlive(now)

	wv.mu.Unlock()
	select {
	case channel <- snapshotCopy:
	default:
	}
}

func (wv *WorldView) computeAlive(now time.Time) map[string]bool {
	alive := make(map[string]bool, len(wv.peers))
	startupGrace := now.Sub(wv.startTime) <= wv.peerTimeout

	for _, id := range wv.peers {
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
func (wv *WorldView) Broadcast(kind common.UpdateKind) {
	if wv.pm == nil || !wv.SelfAlive { //TODO: This boo thang has been changed, but it could have problem with sending
		return
	}

	wv.mu.Lock()
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
	if !wv.SelfAlive {
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
