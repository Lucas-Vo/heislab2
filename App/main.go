// main.go
// Purpose: Application entry point. Initializes elevator I/O, configuration and
// starts the network, assigner and FSM threads goroutines. Handles shutdown
// on interrupt (Ctrl+C).
// Includes: wiring channels between threads and bootstrap of subsystems.
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
	elevUpdateCh := make(chan Snapshot)

	// lucas til filip
	netSnap2Ch := make(chan Snapshot)

	// lucas til vetle
	netSnap1Ch := make(chan Snapshot)

	// vetle til filip
	assignerOutCh := make(chan ElevInput)

	cfg, _, err := common.DefaultConfig()
	if err != nil {
		fmt.Println("Error loading config")

	}

	go networkThread(ctx, cfg, elevUpdateCh, netSnap1Ch, netSnap2Ch)
	go assignerThread(ctx, cfg, netSnap1Ch, assignerOutCh)
	go fsmThread(ctx, cfg, input, assignerOutCh, elevUpdateCh, netSnap2Ch)
	<-ctx.Done()
	fmt.Println("Shutting down")

}
