package main

import (
	//standard
	"time"
	"fmt"
	"os/exec"
	"strconv"
	"encoding/json"
	"runtime"
	//user defined
	. "elevator/common"
)

//constants
const (
	NETWORK_ACK_TIMEOUT = 2
	NETWORK_PACKET_TIMEOUT = 2
)
/**
* @brief 
*
*
*/
func assignerThread(cfg Config,network_snapshot_ch <-chan NetworkState,network_ack_ch <-chan bool, elevator_tasks_ch chan<- ElevInput){

	hraExecutable := ""
    switch runtime.GOOS {
        case "linux":   hraExecutable  = "hall_request_assigner"
        case "windows": hraExecutable  = "hall_request_assigner.exe"
        default:        panic("OS not supported")
    }

	elevatorID, err := cfg.DetectSelfID()
	if err != nil {
		fmt.Println("json.Unmarshal error: ", err)
	}

	var currentElevInput ElevInput
	for {
		
		select {
		case network_snapshot := <-network_snapshot_ch:
			jsonBytes, err := json.Marshal(network_snapshot)
			if err != nil {
				fmt.Println("json.Marshal error: ", err)
			}

			//run assigner script
			ret, err := exec.Command("../hall_request_assigner/"+hraExecutable, "-i", string(jsonBytes)).CombinedOutput()
			if err != nil {
				fmt.Println("exec.Command error: ", err)
				fmt.Println(string(ret))
			}
			
			//parse data
			var output map[string][][2]bool
			err = json.Unmarshal(ret, &output)
			if err != nil {
				fmt.Println("json.Unmarshal error: ", err)
			}

			//select relevant elevator
			currentElevInput = ElevInput{HallTask:output[strconv.Itoa(elevatorID)]}

		case <-time.After(NETWORK_PACKET_TIMEOUT*time.Second):
			fmt.Println("From network update timeout")
		}

		select{
		case <-network_ack_ch:
			elevator_tasks_ch <- currentElevInput

		case <-time.After(NETWORK_ACK_TIMEOUT*time.Second):
			elevator_tasks_ch <- currentElevInput
			fmt.Println("Acknowledgement from network timeout")
		}

	//thread sleep
	time.Sleep(100*time.Millisecond)
	}
}
