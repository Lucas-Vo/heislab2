package main

import (
	"log"
	"time"

	"elevator/common"
	"elevator/elevfsm"
)

func fsmThread(
	config common.Config,
	assignerOutputCh <-chan common.ElevInput,
	elevUpdateCh chan<- common.Snapshot,
	netWorldView2Ch <-chan common.Snapshot, // network -> fsm
) {
	log.Printf("fsmThread started (self=%s)", config.SelfKey)

	inputPollRateMs := 25
	confirmTimeout := 200 * time.Millisecond

	sync := elevfsm.NewFsmSyncAndInit(config, elevUpdateCh)

	ticker := time.NewTicker(time.Duration(inputPollRateMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case snap := <-netWorldView2Ch:
			now := time.Now()
			sync.HandleNetworkSnapshot(snap, now, confirmTimeout)

		case task := <-assignerOutputCh:
			now := time.Now()
			sync.HandleAssignerTask(task, now, confirmTimeout)

		case <-ticker.C:
			now := time.Now()
			elevStateChange, servicedFloor, servicedCalls := sync.Synchronize(now, confirmTimeout)

			if !sync.IsInitFromNetwork() {
				continue
			}

			floorWasServiced := false
			if servicedFloor >= 0 && servicedFloor < common.N_FLOORS {
				floorWasServiced = servicedCalls[servicedFloor][common.BT_HallUp] || servicedCalls[servicedFloor][common.BT_HallDown] || servicedCalls[servicedFloor][common.BT_Cab]
			}
			if floorWasServiced {
				snapshot := sync.BuildSnapshot(servicedFloor, common.UpdateServiced, servicedCalls, now)
				select {
				case elevUpdateCh <- snapshot:
				default:
				}
			}
			if elevStateChange {
				snapshot := sync.BuildSnapshot(sync.GetCurrentFloor(), common.UpdateRequests, elevfsm.Requests{}, now)
				select {
				case elevUpdateCh <- snapshot:
				default:
				}
			}
		}
	}
}
