package main

import (
	"time"
	. "elevator/common"

	//"encoding/json"
)



func assignerThread(network_snapshot chan<- NetworkState,network_ack <-chan bool, elevator_tasks chan<- ElevInput){

	for {
		time.Sleep(1*time.Second)
	}
}
