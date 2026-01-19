package elevassigner

import (
	"encoding/json"
	"fmt"
	"os/exec"
	. "elevator/common"

)

const HRA_EXECUTABLE = "hall_request_assigner" // Linux only

func AssignRequests(networkSnapshot NetworkState,selfKey string) (ElevInput, error) {
	jsonBytes, err := json.Marshal(networkSnapshot)
	if err != nil {
		fmt.Println("json.Marshal error:", err)
		return ElevInput{}, err
	}

	// Linux-only: run external assigner
	ret, err := exec.Command("./elevassigner/"+HRA_EXECUTABLE, "-i", string(jsonBytes)).CombinedOutput()
	if err != nil {
		fmt.Println("exec.Command error:", err)
		fmt.Println(string(ret))
		return ElevInput{}, err
	}

	// parse assigner output
	var output map[string][][2]bool
	if err := json.Unmarshal(ret, &output); err != nil {
		fmt.Println("json.Unmarshal error:", err)
		return ElevInput{}, err
	}

	// pick tasks for THIS elevator
	return ElevInput{HallTask: output[selfKey]}, nil
		
}