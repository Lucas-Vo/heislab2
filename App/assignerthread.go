package main

import (
	"elevator/common"
	"encoding/json"
	"log"
	"os/exec"
)

const (
	HRA_EXECUTABLE_PATH = "./elevassigner/hall_request_assigner"
)

func assignerThread(config common.Config,
	netUpdateAssignerCh <-chan common.Snapshot, // net -> assigner
	hallAssignmentCh chan<- common.HallAssignment, // assigner -> elev
) {
	selfKey := config.SelfKey
	if selfKey == "" {
		log.Println("assignerThread: config.SelfKey is empty (did you call config.InitSelf()?)")
		return
	}

	hallAssignment := common.HallAssignment{}

	for {
		networkSnapshot := <-netUpdateAssignerCh
		if !networkSnapshot.Coherent {
			continue
		}

		err := common.RemoveDeadStates(&networkSnapshot, selfKey)
		if err != nil {
			log.Printf("Remove dead states error: %v", err)
			continue
		}
		// parses snapshot into json format
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
		// parses json into hall assignments
		var assignerOutput map[string]common.HallAssignment
		if err := json.Unmarshal(ret, &assignerOutput); err != nil {
			log.Println("json.Unmarshal error:", err)
			continue
		}

		hallAssignment = assignerOutput[selfKey]
		hallAssignmentCh <- hallAssignment
	}
}
