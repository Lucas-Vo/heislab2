package main

import (

	// Adjust these imports to your actual module path:
	//"elevator/elevnetwork"
	"context"
	"elevator/common"
	. "elevator/common"
	"fmt"
	"os"
	"os/signal"
	//quic "github.com/quic-go/quic-go"
)

func main() {
	// start elevator
	common.ElevioInit("localhost:15657")
	input := common.ElevioGetInputDevice()

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
	fsmServicedCh := make(chan NetworkState)
	fsmUpdateCh := make(chan NetworkState)

	// lucas til filip
	networkSnapshot2Ch := make(chan NetworkState)

	// lucas til vetle
	networkSnapshot1Ch := make(chan NetworkState)

	// vetle til filip
	assignerOutputCh := make(chan ElevInput)

	cfg, _, err := common.DefaultConfig()
	if err != nil {
		fmt.Println("Error loading config")

	}

	go networkThread(ctx, cfg, fsmServicedCh, fsmUpdateCh, networkSnapshot1Ch, networkSnapshot2Ch)
	go assignerThread(ctx, cfg, networkSnapshot1Ch, assignerOutputCh)
	go fsmThread(ctx, cfg, input, assignerOutputCh, fsmServicedCh, fsmUpdateCh, networkSnapshot2Ch)
	<-ctx.Done()
	fmt.Println("Shutting down")

}
