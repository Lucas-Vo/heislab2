package common

type ElevState struct {
    Behavior    string      `json:"behaviour"`
    Floor       int         `json:"floor"` 
    Direction   string      `json:"direction"`
    CabRequests []bool      `json:"cabRequests"`
}

type NetworkState struct {
    HallRequests    [][2]bool		`json:"hallRequests"`
    States          map[string]ElevState     `json:"states"`
}

type ElevInput struct {
    HallTask    	[][2]bool			`json:"HallTask"`
}

type HRAOutput struct {
	HallTasks		[]ElevInput			`json:"HallTasks"`
}