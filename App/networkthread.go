// networkthread.go
package main

import (
	"context"
	"log"
	"time"

	"elevator/common"
	"elevator/elevnetwork"
)

const INITIAL_CONTACT_TIMEOUT = 8 * time.Second

func networkThread(
	ctx context.Context,
	cfg common.Config,
	elevUpdateCh <-chan common.Snapshot,
	netSnap1Ch chan<- common.Snapshot,
	netSnap2Ch chan<- common.Snapshot,
) {
	selfKey := cfg.SelfKey

	wv, incoming := elevnetwork.Start(ctx, cfg, 4242)
	wv.Poke()

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	contactTimer := time.NewTimer(INITIAL_CONTACT_TIMEOUT)
	defer contactTimer.Stop()

	elevatorErrorTimer := time.NewTimer(4 * time.Second)
	defer elevatorErrorTimer.Stop()

	publish := func(ch chan<- common.Snapshot, snap common.Snapshot) {
		select {
		case ch <- snap:
		default:
		}
	}

	publishAll := func() {
		snap := wv.Snapshot()
		if wv.Ready() && wv.Coherent() {
			publish(netSnap1Ch, snap)
		}
		publish(netSnap2Ch, snap)
	}

	for {
		select {
		case <-ctx.Done():
			return

		case ns := <-elevUpdateCh:
			wv.SetSelfAlive(true)
			elevatorErrorTimer.Reset(4 * time.Second)
			wv.HandleLocal(ns)

		case frame := <-incoming:
			kind, becameReady, ok := wv.HandleRemoteFrame(frame)
			if !ok {
				continue
			}
			if kind == common.UpdateRequests && becameReady {
				publishAll()
			}

		case <-contactTimer.C:
			log.Printf("networkThread: initial contact timeout; forcing ready")
			wv.ForceReady()

		case <-ticker.C:
			wv.Tick()
			if wv.Ready() {
				publishAll()
			}

		case <-elevatorErrorTimer.C:
			snap := wv.Snapshot()
			if snap.States[selfKey].Behavior != "idle" {
				if wv.SelfAlive() {
					wv.SetSelfAlive(false)
					log.Printf("No behavior change detected for 4 seconds, marking Elevator as stale")
					publishAll()
				}
			} else {
				if !wv.SelfAlive() {
					wv.SetSelfAlive(true)
					publishAll()
				}
				elevatorErrorTimer.Reset(4 * time.Second)
			}
		}
	}
}
