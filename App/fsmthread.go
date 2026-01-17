package main

import (
	"Driver-go/elevio"
	. "elevator/common"
	"elevator/elev_algo"
	"fmt"
	"time"
)

func fsmthread(assignerOutput chan<- ElevInput, in <-chan NetworkState, out <-chan NetworkState) {
	fmt.Printf("Started!\n")

	elev_algo.Elevio_init("localhost:15657")
	elev_algo.Fsm_init()

	inputPollRate_ms := 25
	elev_algo.ConLoad("elevator.con",
		elev_algo.ConVal("inputPollRate_ms", &inputPollRate_ms, "%d"),
	)
	input := elev_algo.Elevio_getInputDevice()

	if input.FloorSensor() == -1 {
		elev_algo.Fsm_onInitBetweenFloors()
	}

	// C: static int prev[N_FLOORS][N_BUTTONS];
	var prevReq [elev_algo.N_FLOORS][elev_algo.N_BUTTONS]int

	// C: static int prev = -1;
	prevFloor := -1

	for {
		{ // Request button
			for f := 0; f < elev_algo.N_FLOORS; f++ {
				for b := 0; b < elev_algo.N_BUTTONS; b++ {
					v := input.RequestButton(f, elevio.ButtonType(b))
					if v != 0 && v != prevReq[f][b] {
						elev_algo.Fsm_onRequestButtonPress(f, elevio.ButtonType(b))
					}
					prevReq[f][b] = v
				}
			}
		}

		{ // Floor sensor
			f := input.FloorSensor()
			if f != -1 && f != prevFloor {
				elev_algo.Fsm_onFloorArrival(f)
			}
			prevFloor = f
		}

		{ // Timer
			if elev_algo.Timer_timedOut() != 0 {
				elev_algo.Timer_stop()
				elev_algo.Fsm_onDoorTimeout()
			}
		}

		time.Sleep(time.Duration(inputPollRate_ms) * time.Millisecond)
	}

}
