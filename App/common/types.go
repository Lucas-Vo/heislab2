package common

type ElevState struct {
	Behavior    string `json:"behaviour"`
	Floor       int    `json:"floor"`
	Direction   string `json:"direction"`
	CabRequests []bool `json:"cabRequests"`
}

type Snapshot struct {
	HallRequests [][2]bool            `json:"hallRequests"`
	States       map[string]ElevState `json:"states"`
	Alive        map[string]bool      `json:"alive"`
}

type ElevInput struct {
	HallTask [][2]bool `json:"HallTask"`
}

type HRAOutput struct {
	HallTasks []ElevInput `json:"HallTasks"`
}
