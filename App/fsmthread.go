package main

import (
	"log"
	"time"

	"elevator/common"
	"elevator/elevfsm"
)

const (
	POLL_RATE_MS = 25 * time.Millisecond
	IDLE_RATE_MS = 1 * time.Second
)

func fsmThread(
	config common.Config,
	assignerOutputCh <-chan common.ElevInput,
	elevUpdateNetCh chan<- common.Snapshot, // elev -> network
	netUpdateElevCh <-chan common.Snapshot, // network -> elev
) {
	sync := elevfsm.NewFsmSync(config)
	elevator := elevfsm.NewElevator()
	now := time.Now()

	initialBehaviour, initialDirection := elevator.MotionStrings()
	initialSnapshot := sync.BuildSnapshot(
		1,
		common.UpdateRequests,
		elevfsm.Requests{},
		initialBehaviour,
		initialDirection,
	)
	select {
	case elevUpdateNetCh <- initialSnapshot:
	default:
	}

	ticker := time.NewTicker(POLL_RATE_MS)
	defer ticker.Stop()

	idleTicker := time.NewTicker(IDLE_RATE_MS)
	defer idleTicker.Stop()

	i := 0
	for {
		now = time.Now()
		select {
		case snap := <-netUpdateElevCh:
			sync.HandleNetworkSnapshot(snap, now)
			if i%10 == 0 {
				log.Printf("fsmThread: network snapshot received, elevator online. Coherent: %v, hasAlivePeer: %v", snap.Coherent, sync.HasAlivePeer())
			}
			i++
			if snap.Coherent {
				elevator.SetCabLights(sync.GetLocalCab())
				elevator.SetHallLights(sync.GetNetHall())
			}

		case task := <-assignerOutputCh:
			toClear := sync.HandleAssignerTask(task)
			elevator.ApplyClearRequests(toClear)

		case <-ticker.C:
			buttonPresses, newButtonPressed := elevator.PollButtonPresses()
			toInject := sync.HandleLocalButtonPresses(buttonPresses, elevator.GetFloor(), now)
			elevator.ApplyInjectRequests(toInject) // why is this called 2 times, this shit is craaaaazyyy

			elevStateChange, servicedFloor, servicedCalls := elevator.Tick(now)

			elevator.ApplyInjectRequests(sync.ReadyInjects(now))
			sync.ClearServicedRequests(servicedFloor, servicedCalls)

			behaviour, direction := elevator.MotionStrings()
			floorWasServiced := servicedFloor >= 0 &&
				servicedFloor < common.N_FLOORS &&
				(servicedCalls[servicedFloor][common.BT_HallUp] ||
					servicedCalls[servicedFloor][common.BT_HallDown] ||
					servicedCalls[servicedFloor][common.BT_Cab])
			if floorWasServiced {
				snapshot := sync.BuildSnapshot(servicedFloor, common.UpdateServiced, servicedCalls, behaviour, direction)
				elevUpdateNetCh <- snapshot

			} else if elevStateChange || newButtonPressed {
				snapshot := sync.BuildSnapshot(elevator.GetPrevFloor(), common.UpdateRequests, elevfsm.Requests{}, behaviour, direction)
				select {
				case elevUpdateNetCh <- snapshot:
				default:
					log.Printf("fsmThread: elevUpdateCh is full, skipping snapshot update")
				}
			}
		case <-idleTicker.C:
			behaviour, direction := elevator.MotionStrings()
			if behaviour != "idle" {
				continue
			}
			snapshot := sync.BuildSnapshot(elevator.GetPrevFloor(), common.UpdateRequests, elevfsm.Requests{}, behaviour, direction)
			select {
			case elevUpdateNetCh <- snapshot:
			default:
				log.Printf("fsmThread: elevUpdateCh is full, skipping snapshot update")
			}
		}
	}
}
