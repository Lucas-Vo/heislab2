package main

import (
	"context"
	"elevator/common"
	"fmt"
	"os"
	"os/signal"
)

// main starts the three controller threads and waits for an interrupt.
func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		cancel()
	}()

	elevUpdateNetCh := make(chan common.Snapshot, 8)
	netUpdateElevCh := make(chan common.Snapshot, 8)
	netUpdateAssignerCh := make(chan common.Snapshot, 8)
	assignerUpdateElevCh := make(chan common.ElevInput, 4)

	config, err := common.DefaultConfig()
	if err != nil {
		fmt.Println("Error loading config")
	}

	go networkThread(config, elevUpdateNetCh, netUpdateAssignerCh, netUpdateElevCh)
	go assignerThread(config, netUpdateAssignerCh, assignerUpdateElevCh)
	go elevatorThread(config, assignerUpdateElevCh, elevUpdateNetCh, netUpdateElevCh)
	<-ctx.Done()
	fmt.Println("Shutting down")
}
