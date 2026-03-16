package main

import (
	"Network-go/network/bcast"
	"Network-go/network/peers"
	"context"
	"elevator/common"
	"fmt"
	"os"
	"os/signal"
)

const (
	NETWORK_CHAN_SIZE = 128
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		cancel()
	}()

	elevUpdateCh := make(chan common.Snapshot, 8)
	netSnapElevCh := make(chan common.Snapshot, 8)
	netSnapAssignerCh := make(chan common.Snapshot, 8)
	assignerOutCh := make(chan common.ElevInput, 4)
	incoming := make(chan common.NetMsg, NETWORK_CHAN_SIZE)
	outgoing := make(chan common.NetMsg, NETWORK_CHAN_SIZE)
	peerUpdateCh := make(chan peers.PeerUpdate, NETWORK_CHAN_SIZE)
	peerTxEnable := make(chan bool, 1)

	config, err := common.DefaultConfig()
	if err != nil {
		fmt.Println("Error loading config")
	}

	go peers.Transmitter(config.PeerPort, config.SelfKey, peerTxEnable)
	go peers.Receiver(config.PeerPort, peerUpdateCh)
	go bcast.Transmitter(config.MsgPort, outgoing)
	go bcast.Receiver(config.MsgPort, incoming)
	go networkThread(config, elevUpdateCh, netSnapAssignerCh, netSnapElevCh, incoming, outgoing, peerUpdateCh)
	go assignerThread(config, netSnapAssignerCh, assignerOutCh)
	go elevatorThread(config, assignerOutCh, elevUpdateCh, netSnapElevCh)
	<-ctx.Done()
	fmt.Println("Shutting down")
}
