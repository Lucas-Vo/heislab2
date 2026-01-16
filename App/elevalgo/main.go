package elevalgo

import (
	"Driver-go/elevio"
	"fmt"
	"time"
)

func main() {
	fmt.Printf("Started!\n")

	elevio_init("localhost:15657")
	fsm_init()

	inputPollRate_ms := 25
	ConLoad("elevator.con",
		ConVal("inputPollRate_ms", &inputPollRate_ms, "%d"),
	)
	fmt.Printf("hello")
	input := elevio_getInputDevice()

	if input.floorSensor() == -1 {
		fsm_onInitBetweenFloors()
	}

	// C: static int prev[N_FLOORS][N_BUTTONS];
	var prevReq [N_FLOORS][N_BUTTONS]int

	// C: static int prev = -1;
	prevFloor := -1

	for {
		{ // Request button
			for f := 0; f < N_FLOORS; f++ {
				for b := 0; b < N_BUTTONS; b++ {
					v := input.requestButton(f, elevio.ButtonType(b))
					if v != 0 && v != prevReq[f][b] {
						fsm_onRequestButtonPress(f, elevio.ButtonType(b))
					}
					prevReq[f][b] = v
				}
			}
		}

		{ // Floor sensor
			f := input.floorSensor()
			if f != -1 && f != prevFloor {
				fsm_onFloorArrival(f)
			}
			prevFloor = f
		}

		{ // Timer
			if timer_timedOut() != 0 {
				timer_stop()
				fsm_onDoorTimeout()
			}
		}

		time.Sleep(time.Duration(inputPollRate_ms) * time.Millisecond)
	}
}
