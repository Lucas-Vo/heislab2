// timer.go
// Purpose: Simple wall-clock timer utilities used by the FSM for door timeouts
// and other timing-based actions. Provides start/stop and timed-out query.
package elevfsm

import "time"

func get_wall_time_s() float64 {
	return float64(time.Now().UnixNano()) * 1e-9
}

var timerEndTime float64
var timerActive int

func Timer_start(duration float64) {
	timerEndTime = get_wall_time_s() + duration
	timerActive = 1
}

func Timer_stop() {
	timerActive = 0
}

// int timer_timedOut(void)
func Timer_timedOut() int {
	if timerActive != 0 && get_wall_time_s() > timerEndTime {
		return 1
	}
	return 0
}
