package main

import (
	"elevator/common"
	"encoding/json"
	"log"
	"os/exec"
	"time"
)

const (
	NET_SNAP_TIMEOUT    = 2 * time.Second
	HRA_EXECUTABLE_PATH = "./elevassigner/hall_request_assigner"
)

func assignerThread(config common.Config, netUpdateAssignerCh <-chan common.Snapshot, hallAssignmentCh chan<- common.HallAssignment) {
	selfKey := config.SelfKey
	if selfKey == "" {
		log.Println("assignerThread: config.SelfKey is empty (did you call config.InitSelf()?)")
		return
	}

	hallAssignment := common.HallAssignment{}

	networkTimeout := time.NewTimer(NET_SNAP_TIMEOUT)
	defer networkTimeout.Stop()

	for {
		select {
		case networkSnapshot := <-netUpdateAssignerCh:
			if !networkSnapshot.Coherent {
				continue
			}
			networkTimeout.Reset(NET_SNAP_TIMEOUT)

			err := common.RemoveDeadStates(&networkSnapshot, selfKey)
			if err != nil {
				log.Printf("Remove dead states error: ", err)
				continue
			}

			jsonBytes, err := json.Marshal(networkSnapshot)
			if err != nil {
				log.Println("json.Marshal error:", err)
				continue
			}
			ret, err := exec.Command(HRA_EXECUTABLE_PATH, "-i", string(jsonBytes)).CombinedOutput()
			if err != nil {
				log.Printf("exec.Command error: %v (states=%d, hall=%d)\n", err, len(networkSnapshot.States), len(networkSnapshot.HallRequests))
				log.Println(string(ret))
				continue
			}
			var output map[string]common.HallAssignment
			if err := json.Unmarshal(ret, &output); err != nil {
				log.Println("json.Unmarshal error:", err)
				continue
			}

			hallAssignment = output[selfKey]
			hallAssignmentCh <- hallAssignment

		case <-networkTimeout.C:
			log.Println("Snapshot from network update timeout, withholding updates until next network ack")
		}
	}
}
