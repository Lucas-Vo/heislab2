package main

import (
	

	// Adjust these imports to your actual module path:
	//"elevator/elevnetwork"
	. "elevator/common"

	//quic "github.com/quic-go/quic-go"
)

func main() {
	// filip til lucas
	elevalgoServiced := make(chan NetworkState)
	elevalgoStateOfTheCountry := make(chan NetworkState)

	// lucas til vetle
	networkStateOfTheWorld := make(chan NetworkState)
	theWorldIsReady := make(chan bool)

	// vetle til filip
	assignerOutput := make(chan ElevInput)

	go networkthread(elevalgoServiced,elevalgoStateOfTheCountry,networkStateOfTheWorld,theWorldIsReady)
	go assignerThread(networkStateOfTheWorld,theWorldIsReady,assignerOutput)
	go fsmthread(assignerOutput)
}