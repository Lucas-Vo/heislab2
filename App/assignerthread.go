package main

import (
	"context"
	"fmt"
	"time"
	"elevator/elevassigner"
	. "elevator/common"
	"os/exec"
	"encoding/json"
)

// constants (seconds)
const (
	NETWORK_ACK_TIMEOUT    = 2
	NETWORK_PACKET_TIMEOUT = 2

	HRA_EXECUTABLE = "hall_request_assigner"
)

func assignerThread(
	context context.Context,
	config Config,
	networkSnapshotCh <-chan NetworkState,
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
	var ackTimeout bool

	for {
		select {
		case networkSnapshot := <-networkSnapshotCh:
			ackTimeout = false
			
			// remove stale elevators from snapshot
			elevassigner.FilterStaleStates(&networkSnapshot)
			
			// serialize snapshot to JSON
			jsonBytes, err := json.Marshal(networkSnapshot)
			if err != nil {
				fmt.Println("json.Marshal error:", err)
				
			}

			// Linux-only: run external assigner with snapshot as input
			ret, err := exec.Command("./elevassigner/"+HRA_EXECUTABLE, "-i", string(jsonBytes)).CombinedOutput()
			if err != nil {
				fmt.Println("exec.Command error:", err)
				fmt.Println(string(ret))
				
			}

			// parse assigner output
			var output map[string][][2]bool
			if err := json.Unmarshal(ret, &output); err != nil {
				fmt.Println("json.Unmarshal error:", err)
			}

			// pick tasks for THIS elevator to send to fsmthread
			currentElevInput = ElevInput{HallTask: output[selfKey]}

		case <-time.After(NETWORK_PACKET_TIMEOUT * time.Second):
			fmt.Println("Snapshot from network update timeout, holding further updates until next network ack")
			if !ackTimeout {
				elevatorTasksCh <- currentElevInput	
				ackTimeout = true
			}
		}

		// Avoid busy looping; also respects context
		select {
		case <-time.After(100 * time.Millisecond):
		case <-context.Done():
			return
		}
	}
}
