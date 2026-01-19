package main

import (
	"context"
	"fmt"
	"time"
	"elevator/elevassigner"
	. "elevator/common"

)

// constants (seconds)
const (
	NETWORK_ACK_TIMEOUT    = 2
	NETWORK_PACKET_TIMEOUT = 2
)

func assignerThread(
	context context.Context,
	config Config,
	networkSnapshotCh <-chan NetworkState,
	networkAckCh <-chan bool,
	elevatorTasksCh chan<- ElevInput,
) {
	// Use config.SelfKey (string "1","2",...)
	selfKey := config.SelfKey
	if selfKey == "" {
		// fallback if caller didn't init self (shouldn't happen if you use MustDefaultConfig / InitSelf)
		fmt.Println("assignerThread: config.SelfKey is empty (did you call config.InitSelf()?)")
		return
	}

	// state variables
	var currentElevInput ElevInput
	var err error
	var ackTimeout bool

	for {
		select {
		case networkSnapshot := <-networkSnapshotCh:
			currentElevInput, err = elevassigner.AssignRequests(networkSnapshot, selfKey)
			if err != nil {
				fmt.Println("assignerThread: elevassigner.AssignRequests error:", err)
			}

		case <-time.After(NETWORK_PACKET_TIMEOUT * time.Second):
			fmt.Println("Snapshot from network update timeout")
		
		}
		select {
		case <-networkAckCh:
			elevatorTasksCh <- currentElevInput
			ackTimeout = false

		case <-time.After(NETWORK_ACK_TIMEOUT * time.Second):
			if !ackTimeout {
				elevatorTasksCh <- currentElevInput	
				ackTimeout = true
			}
			fmt.Println("Acknowledgement from network timeout, holding further updates until next network ack")
		}
		// Avoid busy looping; also respects context
		select {
		case <-time.After(100 * time.Millisecond):
		case <-context.Done():
			return
		}
	}
}
