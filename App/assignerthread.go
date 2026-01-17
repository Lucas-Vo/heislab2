package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	. "elevator/common"
)

// constants (seconds)
const (
	NETWORK_ACK_TIMEOUT    = 2
	NETWORK_PACKET_TIMEOUT = 2

	HRA_EXECUTABLE = "hall_request_assigner" // Linux only
)

func assignerThread(
	ctx context.Context,
	cfg Config,
	networkSnapshotCh <-chan NetworkState,
	networkAckCh <-chan bool,
	elevatorTasksCh chan<- ElevInput,
) {
	// Use cfg.SelfKey (string "1","2",...)
	selfKey := cfg.SelfKey
	if selfKey == "" {
		// fallback if caller didn't init self (shouldn't happen if you use MustDefaultConfig / InitSelf)
		fmt.Println("assignerThread: cfg.SelfKey is empty (did you call cfg.InitSelf()?)")
		return
	}

	var currentElevInput ElevInput

	for {
		select {
		case <-ctx.Done():
			return

		case networkSnapshot := <-networkSnapshotCh:
			jsonBytes, err := json.Marshal(networkSnapshot)
			if err != nil {
				fmt.Println("json.Marshal error:", err)
				break
			}

			// Linux-only: run external assigner
			ret, err := exec.Command("../hall_request_assigner/"+HRA_EXECUTABLE, "-i", string(jsonBytes)).CombinedOutput()
			if err != nil {
				fmt.Println("exec.Command error:", err)
				fmt.Println(string(ret))
				break
			}

			// parse assigner output
			var output map[string][][2]bool
			if err := json.Unmarshal(ret, &output); err != nil {
				fmt.Println("json.Unmarshal error:", err)
				break
			}

			// pick tasks for THIS elevator
			currentElevInput = ElevInput{HallTask: output[selfKey]}

		case <-time.After(NETWORK_PACKET_TIMEOUT * time.Second):
			fmt.Println("From network update timeout")

		case <-ctx.Done():
			return
		}

		// Wait for ack (or timeout), then forward the current tasks to FSM
		select {
		case <-ctx.Done():
			return

		case <-networkAckCh:
			select {
			case elevatorTasksCh <- currentElevInput:
			case <-ctx.Done():
				return
			}

		case <-time.After(NETWORK_ACK_TIMEOUT * time.Second):
			select {
			case elevatorTasksCh <- currentElevInput:
			case <-ctx.Done():
				return
			}
			fmt.Println("Acknowledgement from network timeout")
		}

		// Avoid busy looping; also respects ctx
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return
		}
	}
}
