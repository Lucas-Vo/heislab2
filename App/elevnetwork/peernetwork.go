package elevnetwork

import (
	"Network-go/network/bcast"
	"Network-go/network/peers"
	"elevator/common"
	"log"
)

const NETWORK_CHAN_SIZE = 128

type PeerNetwork struct {
	selfKey    string
	msgCounter uint64

	incoming     chan common.NetMsg
	outgoing     chan common.NetMsg
	peerUpdates  chan peers.PeerUpdate
	peerTxEnable chan bool

	lastSnapshot    common.Snapshot
	hasLastSnapshot bool
}

func InitPeerNetwork(config common.Config) *PeerNetwork {
	pn := &PeerNetwork{
		selfKey:      config.SelfKey,
		incoming:     make(chan common.NetMsg, NETWORK_CHAN_SIZE),
		outgoing:     make(chan common.NetMsg, NETWORK_CHAN_SIZE),
		peerUpdates:  make(chan peers.PeerUpdate, NETWORK_CHAN_SIZE),
		peerTxEnable: make(chan bool, 1),
	}

	go peers.Transmitter(config.PeerPort, config.SelfKey, pn.peerTxEnable)
	go peers.Receiver(config.PeerPort, pn.peerUpdates)
	go bcast.Transmitter(config.MsgPort, pn.outgoing)
	go bcast.Receiver(config.MsgPort, pn.incoming)

	return pn
}

func (pn *PeerNetwork) SendSnapshot(snapshot common.Snapshot, selfAlive bool) bool {
	if !selfAlive || pn.outgoing == nil {
		return false
	}

	pn.msgCounter++
	msg := common.NetMsg{
		Origin:   pn.selfKey,
		Counter:  pn.msgCounter,
		Snapshot: snapshot,
	}

	select {
	case pn.outgoing <- msg:
		pn.lastSnapshot = common.DeepCopySnapshot(snapshot)
		pn.hasLastSnapshot = true
		return true
	default:
		log.Printf("sendOverNetwork: dropping frame origin=%s counter=%d kind=%v (tx queue full)", msg.Origin, msg.Counter, snapshot.UpdateKind)
		return false
	}
}

func (wv *WorldView) HandlePeerUpdate(update peers.PeerUpdate) {
	for _, id := range update.Lost {
		if id == "" || id == wv.selfKey {
			continue
		}
		delete(wv.latestMsgCount, id)
	}
}

func (pn *PeerNetwork) PeerUpdateCh() <-chan peers.PeerUpdate {
	return pn.peerUpdates
}

func (pn *PeerNetwork) PeerMessageCh() <-chan common.NetMsg {
	return pn.incoming
}

func (pn *PeerNetwork) LastSentSnapshot() (snapshot common.Snapshot, ok bool) {
	if !pn.hasLastSnapshot {
		return common.Snapshot{}, false
	}
	return common.DeepCopySnapshot(pn.lastSnapshot), true
}
