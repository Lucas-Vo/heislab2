package main

import (
	"elevator/common"
	"elevator/elevassigner"
	"encoding/json"
	"log"
	"os/exec"
	"time"
)

// constants (seconds)
const (
	NET_SNAP_TIMEOUT    = 2 * time.Second
	HRA_EXECUTABLE_PATH = "./elevassigner/hall_request_assigner"
)

func assignerThread(config common.Config, networkSnapshotCh <-chan common.Snapshot, elevatorTasksCh chan<- common.ElevInput) {
	selfKey := config.SelfKey
	if selfKey == "" {
		log.Println("assignerThread: config.SelfKey is empty (did you call config.InitSelf()?)")
		return
	}

	hallAssignment := common.ElevInput{HallTask: [common.N_FLOORS][2]bool{}, HallRequests: [common.N_FLOORS][2]bool{}}

	networkTimeout := time.NewTimer(NET_SNAP_TIMEOUT)
	defer networkTimeout.Stop()

	for {
		select {
		case networkSnapshot := <-networkSnapshotCh:
			if !networkSnapshot.Coherent {
				continue
			}
			networkTimeout.Reset(NET_SNAP_TIMEOUT)

			err := elevassigner.RemoveStaleStates(&networkSnapshot, selfKey)
			if err != nil {
				log.Println("removing stale states error: ", err)
				continue
			}

			// serialize snapshot to JSON
			jsonBytes, err := json.Marshal(networkSnapshot)
			if err != nil {
				log.Println("json.Marshal error:", err)
				continue
			}
			// Run external hall request assigner executable
			ret, err := exec.Command(HRA_EXECUTABLE_PATH, "-i", string(jsonBytes)).CombinedOutput()
			if err != nil {
				log.Printf("exec.Command error: %v (states=%d, hall=%d)\n", err, len(networkSnapshot.States), len(networkSnapshot.HallRequests))
				log.Println(string(ret))
				continue
			}
			// parse assigner output
			var output map[string][common.N_FLOORS][2]bool
			if err := json.Unmarshal(ret, &output); err != nil {
				log.Println("json.Unmarshal error:", err)
				continue
			}

			hallAssignment = common.ElevInput{HallTask: output[selfKey], HallRequests: networkSnapshot.HallRequests}
			elevatorTasksCh <- hallAssignment

		case <-networkTimeout.C:
			log.Println("Snapshot from network update timeout, withholding updates until next network ack")
		}
	}
}
