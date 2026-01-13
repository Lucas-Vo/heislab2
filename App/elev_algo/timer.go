package main

import "time"

func get_wall_time_s() float64 {
	return float64(time.Now().UnixNano()) * 1e-9
}

var timerEndTime float64
var timerActive int

func timer_start(duration float64) {
	timerEndTime = get_wall_time_s() + duration
	timerActive = 1
}

func timer_stop() {
	timerActive = 0
}

// int timer_timedOut(void)
func timer_timedOut() int {
	if timerActive != 0 && get_wall_time_s() > timerEndTime {
		return 1
	}
	return 0
}
