package main

import (
	"context"
	"log"
	"time"

	"elevator/common"
	"elevator/elevfsm"
)

func fsmThread(
	ctx context.Context,
	cfg common.Config,
	elevInputDevice common.ElevInputDevice,
	assignerOutputCh <-chan common.ElevInput,
	elevUpdateCh chan<- common.Snapshot,
	netWorldView2Ch <-chan common.Snapshot, // network -> fsm
) {
	log.Printf("fsmThread started (self=%s)", cfg.SelfKey)

	sync := elevfsm.NewFsmSync(cfg)
	initialSnap := sync.Initialize(elevInputDevice, time.Now())
	select {
	case elevUpdateCh <- initialSnap:
	default:
	}

	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case snap := <-netWorldView2Ch:
			sync.HandleNetwork(snap, time.Now())

		case task := <-assignerOutputCh:
			sync.HandleAssigner(task, time.Now())

		case <-ticker.C:
			updates := sync.Tick(elevInputDevice, time.Now())
			if updates.HasServiced {
				select {
				case elevUpdateCh <- updates.Serviced:
				default:
				}
			}
			if updates.HasRequests {
				select {
				case elevUpdateCh <- updates.Requests:
				default:
				}
			}
		}
	}
}
