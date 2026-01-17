package main

import (

	// Adjust these imports to your actual module path:
	//"elevator/elevnetwork"
	"elevator/common"
	. "elevator/common"
	//quic "github.com/quic-go/quic-go"
)



func main() {
	// filip til lucas
	elevalgoServiced := make(chan NetworkState)
	elevalgoLaManana := make(chan NetworkState)

	// lucas til vetle
	networkStateOfTheWorld := make(chan NetworkState)
	theWorldIsReady := make(chan bool)

	// vetle til filip
	assignerOutput := make(chan ElevInput)

	cfg := common.DefaultConfig()

	go networkthread(elevalgoServiced,elevalgoLaManana,networkStateOfTheWorld,theWorldIsReady)
	go assignerThread(cfg,networkStateOfTheWorld,theWorldIsReady,assignerOutput)
	go fsmthread(assignerOutput,elevalgoServiced,elevalgoLaManana)
}