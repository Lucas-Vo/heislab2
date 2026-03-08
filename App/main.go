package main

import (
	"context"
	"elevator/common"
	. "elevator/common"
	"fmt"
	"os"
	"os/signal"
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

	elevUpdateCh := make(chan Snapshot, 8)
	netSnapElevCh := make(chan Snapshot, 8)
	netSnapAssignerCh := make(chan Snapshot, 8)
	assignerOutCh := make(chan ElevInput, 4)

	config, _, err := common.DefaultConfig()
	if err != nil {
		fmt.Println("Error loading config")

	}

	go networkThread(ctx, config, elevUpdateCh, netSnapAssignerCh, netSnapElevCh)
	go assignerThread(config, netSnapAssignerCh, assignerOutCh)
	go fsmThread(config, assignerOutCh, elevUpdateCh, netSnapElevCh)
	<-ctx.Done()
	fmt.Println("Shutting down")

}
