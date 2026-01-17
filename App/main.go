package main

import (

	// Adjust these imports to your actual module path:
	//"elevator/elevnetwork"
	"context"
	. "elevator/common"
	"os"
	"os/signal"
	//quic "github.com/quic-go/quic-go"
)

func main() {
	// ctrl + c handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		cancel()
	}()

	// filip til lucas
	elevalgoServiced := make(chan NetworkState)
	elevalgoLaManana := make(chan NetworkState)

	// lucas til filip
	snapshotToFSM := make(chan NetworkState)

	// lucas til vetle
	networkStateOfTheWorld := make(chan NetworkState)
	theWorldIsReady := make(chan bool)

	// vetle til filip
	assignerOutput := make(chan ElevInput)

	cfg := DefaultConfig()

	go networkthread(ctx, cfg, elevalgoServiced, elevalgoLaManana, networkStateOfTheWorld, theWorldIsReady, snapshotToFSM)
	go assignerThread(networkStateOfTheWorld, theWorldIsReady, assignerOutput)
	go fsmthread(assignerOutput, elevalgoServiced, elevalgoLaManana, snapshotToFSM)

	<-ctx.Done()
}
