package main

import (
	"log"
	"time"

	"elevator/common"
	"elevator/elevfsm"
)

const (
	inputPollRateMs        = 25
	confirmTimeout         = 200 * time.Millisecond
	assignerOfflineTimeout = 1200 * time.Millisecond
)

func fsmThread(
	config common.Config,
	assignerOutputCh <-chan common.ElevInput,
	elevUpdateCh chan<- common.Snapshot,
	netWorldView2Ch <-chan common.Snapshot, // network -> fsm
) {
	log.Printf("fsmThread started (self=%s)", config.SelfKey)

	sync := elevfsm.NewFsmSync(config)
	elevator := elevfsm.NewElevator("localhost:15657")
	online := false
	now := time.Now()
	lastAssignerTaskAt := time.Time{}

	initialBehaviour, initialDirection := elevator.MotionStrings()
	initialSnapshot := sync.BuildSnapshot(
		-1,
		common.UpdateRequests,
		elevfsm.Requests{},
		online,
		initialBehaviour,
		initialDirection,
	)
	select {
	case elevUpdateCh <- initialSnapshot:
	default:
	}

	ticker := time.NewTicker(time.Duration(inputPollRateMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case snap := <-netWorldView2Ch:
			now = time.Now()
			sync.HandleNetworkSnapshot(snap, now)

		case task := <-assignerOutputCh:
			now = time.Now()
			lastAssignerTaskAt = now
			sync.HandleAssignerTask(task, elevator)

		case <-ticker.C:
			now = time.Now()
			networkFresh := sync.NetworkOnline(now)
			assignerFresh := !lastAssignerTaskAt.IsZero() && now.Sub(lastAssignerTaskAt) < assignerOfflineTimeout
			online = networkFresh && assignerFresh

			edgePresses, newButtonPressed := elevator.PollButtonPresses()
			sync.HandleLocalButtonPresses(edgePresses, now, elevator)

			elevStateChange, servicedFloor, servicedCalls := elevator.Tick(now)
			sync.ClearServicedRequests(servicedFloor, servicedCalls, online)

			sync.InjectReadyRequests(now, confirmTimeout, online, elevator)
			sync.SetLights(online, elevator)

			if !sync.IsInitFromNetwork() {
				continue
			}

			floorWasServiced := servicedFloor >= 0 &&
				servicedFloor < common.N_FLOORS &&
				(servicedCalls[servicedFloor][common.BT_HallUp] ||
					servicedCalls[servicedFloor][common.BT_HallDown] ||
					servicedCalls[servicedFloor][common.BT_Cab])
			if floorWasServiced {
				behaviour, direction := elevator.MotionStrings()
				snapshot := sync.BuildSnapshot(servicedFloor, common.UpdateServiced, servicedCalls, online, behaviour, direction)
				select {
				case elevUpdateCh <- snapshot:
				default:
				}
			}
			if elevStateChange || newButtonPressed {
				behaviour, direction := elevator.MotionStrings()
				snapshot := sync.BuildSnapshot(elevator.CurrentFloor(), common.UpdateRequests, elevfsm.Requests{}, online, behaviour, direction)
				select {
				case elevUpdateCh <- snapshot:
				default:
				}
			}
		}
	}
}
