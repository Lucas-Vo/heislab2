package elevnetwork

import (
	"Network-go/network/bcast"
	"Network-go/network/peers"
	"elevator/common"
	"log"
)

// NETWORK_CHAN_SIZE is the buffer size used for the UDP peer and snapshot
// channels.
const NETWORK_CHAN_SIZE = 128

// Network wraps the peer-discovery and UDP broadcast channels used by the
// controller.
//
// The type also keeps the last transmitted snapshot and a per-origin message
// counter so higher layers can reason about coherence and stale frames.
type Network struct {
	selfKey    string
	msgCounter uint64

	incoming     chan common.NetMsg
	outgoing     chan common.NetMsg
	peerUpdates  chan peers.PeerUpdate
	peerTxEnable chan bool

	lastSnapshot    common.Snapshot
	hasLastSnapshot bool
}

// InitNetwork starts the background peer-discovery and snapshot broadcast
// goroutines and returns their channel wrapper.
func InitNetwork(config common.Config) *Network {
	network := &Network{
		selfKey:      config.SelfKey,
		incoming:     make(chan common.NetMsg, NETWORK_CHAN_SIZE),
		outgoing:     make(chan common.NetMsg, NETWORK_CHAN_SIZE),
		peerUpdates:  make(chan peers.PeerUpdate, NETWORK_CHAN_SIZE),
		peerTxEnable: make(chan bool, 1),
	}

	go peers.Transmitter(config.PeerPort, config.SelfKey, network.peerTxEnable)
	go peers.Receiver(config.PeerPort, network.peerUpdates)
	go bcast.Transmitter(config.MsgPort, network.outgoing)
	go bcast.Receiver(config.MsgPort, network.incoming)

	return network
}

// Incoming returns the receive-only channel for snapshot messages from peers.
func (network *Network) Incoming() <-chan common.NetMsg {
	return network.incoming
}

// PeerUpdates returns the receive-only channel for peer liveness updates.
func (network *Network) PeerUpdates() <-chan peers.PeerUpdate {
	return network.peerUpdates
}

// LastSentSnapshot returns a defensive copy of the most recent transmitted
// snapshot.
func (network *Network) LastSentSnapshot() (common.Snapshot, bool) {
	if !network.hasLastSnapshot {
		return common.Snapshot{}, false
	}
	return common.DeepCopySnapshot(network.lastSnapshot), true
}

// SendSnapshot broadcasts snapshot if this elevator is currently considered
// alive.
//
// The function returns false when transmission is skipped or the outgoing queue
// is full.
func (network *Network) SendSnapshot(snapshot common.Snapshot, selfAlive bool) bool {
	if !selfAlive || network.outgoing == nil {
		return false
	}

	network.msgCounter++
	msg := common.NetMsg{
		Origin:   network.selfKey,
		Counter:  network.msgCounter,
		Snapshot: snapshot,
	}

	select {
	case network.outgoing <- msg:
		network.lastSnapshot = common.DeepCopySnapshot(snapshot)
		network.hasLastSnapshot = true
		return true
	default:
		log.Printf("sendOverNetwork: dropping frame origin=%s counter=%d kind=%v (tx queue full)", msg.Origin, msg.Counter, snapshot.UpdateKind)
		return false
	}
}
