package main

import (
	"log"
	"time"

	"elevator/common"
	"elevator/elevfsm"
)

const (
	inputPollRateMs = 25
	confirmTimeout  = 200 * time.Millisecond
)

func fsmThread(
	config common.Config,
	assignerOutputCh <-chan common.ElevInput,
	elevUpdateNetCh chan<- common.Snapshot, // elev -> network
	netUpdateElevCh <-chan common.Snapshot, // network -> elev
) {
	log.Printf("fsmThread started (self=%s)", config.SelfKey)

	sync := elevfsm.NewFsmSync(config)
	elevator := elevfsm.NewElevator("localhost:15657")
	online := false
	now := time.Now()

	initialBehaviour, initialDirection := elevator.MotionStrings()
	initialSnapshot := sync.BuildSnapshot(
		elevator.CurrentFloor(),
		common.UpdateRequests,
		elevfsm.Requests{},
		online,
		initialBehaviour,
		initialDirection,
	)
	select {
	case elevUpdateNetCh <- initialSnapshot:
	default:
	}

	ticker := time.NewTicker(time.Duration(inputPollRateMs) * time.Millisecond)
	defer ticker.Stop()

	idleTicker := time.NewTicker(1 * time.Second)
	defer idleTicker.Stop()

	for {
		select {
		case snap := <-netUpdateElevCh:
			now = time.Now()
			sync.HandleNetworkSnapshot(snap, now)
			online = sync.NetworkOnline(now)

			if online {
				log.Printf("fsmThread: network snapshot received, elevator online. Coherent: %v, hasAlivePeer: %v", snap.Coherent, sync.HasAlivePeer())
				if snap.Coherent {
					elevator.SetCabLights(sync.GetLocalCab())
					elevator.SetHallLights(sync.GetNetHall())
				}
			}

		case task := <-assignerOutputCh:
			now = time.Now()
			toClear := sync.HandleAssignerTask(task)
			elevator.ApplyClearRequests(toClear)

		case <-ticker.C:
			now = time.Now()
			online = sync.NetworkOnline(now)

			edgePresses, newButtonPressed := elevator.PollButtonPresses()
			toInject := sync.HandleLocalButtonPresses(edgePresses, elevator.FloorSensor(), now, online)
			elevator.ApplyInjectRequests(toInject)

			elevStateChange, servicedFloor, servicedCalls := elevator.Tick(now)
			sync.ClearServicedRequests(servicedFloor, servicedCalls, online)
			elevator.ApplyInjectRequests(sync.ReadyInjects(now, confirmTimeout, online))

			if !online {
				elevator.SetHallLights(sync.GetLocalHall())
				elevator.SetCabLights(sync.GetLocalCab())
			}

			behaviour, direction := elevator.MotionStrings()
			floorWasServiced := servicedFloor >= 0 &&
				servicedFloor < common.N_FLOORS &&
				(servicedCalls[servicedFloor][common.BT_HallUp] ||
					servicedCalls[servicedFloor][common.BT_HallDown] ||
					servicedCalls[servicedFloor][common.BT_Cab])
			if floorWasServiced {
				snapshot := sync.BuildSnapshot(servicedFloor, common.UpdateServiced, servicedCalls, online, behaviour, direction)
				elevUpdateNetCh <- snapshot
			}
			if elevStateChange || newButtonPressed {
				snapshot := sync.BuildSnapshot(elevator.CurrentFloor(), common.UpdateRequests, elevfsm.Requests{}, online, behaviour, direction)
				select {
				case elevUpdateNetCh <- snapshot:
				default:
					log.Printf("fsmThread: elevUpdateCh is full, skipping snapshot update")
				}
			}
			
		case <-idleTicker.C:
			behaviour, direction := elevator.MotionStrings()
			snapshot := sync.BuildSnapshot(elevator.CurrentFloor(), common.UpdateRequests, elevfsm.Requests{}, online, behaviour, direction)
			select {
			case elevUpdateNetCh <- snapshot:
			default:
				log.Printf("fsmThread: elevUpdateCh is full, skipping snapshot update")
			}
		}
	}
}
