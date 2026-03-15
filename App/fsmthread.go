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

	//dummy
	initialSnapshot := sync.BuildSnapshot(elevator, common.UpdateRequests, common.Requests{})
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
				elevator.SetLights(sync.GetLocalCalls())
			}

		case task := <-assignerOutputCh:
			toClear := sync.HandleAssignerTask(task)
			elevator.ApplyClearRequests(toClear)

		case <-ticker.C:
			buttonPresses, newButtonPressed := elevator.PollButtonPresses()
			toInject := sync.HandleLocalButtonPresses(buttonPresses, elevator.GetFloor(), now)
			elevator.ApplyInjectRequests(toInject) // why is this called 2 times, this shit is craaaaazyyy

			elevStateChange, servicedCalls, isServiced := elevator.Tick(now)

			elevator.ApplyInjectRequests(sync.ReadyInjects(now))
			sync.ClearServicedRequests(elevator.GetPrevFloor(), servicedCalls)

			if isServiced {
				snapshot := sync.BuildSnapshot(elevator, common.UpdateServiced, servicedCalls)
				elevUpdateNetCh <- snapshot

			} else if elevStateChange || newButtonPressed {
				snapshot := sync.BuildSnapshot(elevator, common.UpdateRequests, common.Requests{})
				select {
				case elevUpdateNetCh <- snapshot:
				default:
					log.Printf("fsmThread: elevUpdateCh is full, skipping snapshot update")
				}
			}
		case <-idleTicker.C:
			if !elevator.IsIdle() {
				continue
			}
			snapshot := sync.BuildSnapshot(elevator, common.UpdateRequests, common.Requests{})
			select {
			case elevUpdateNetCh <- snapshot:
			default:
				log.Printf("fsmThread: elevUpdateCh is full, skipping snapshot update")
			}
		}
	}
}
