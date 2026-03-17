// Package main wires the distributed elevator controller together.
//
// The executable is split into three long-running goroutines:
//
//   - elevatorThread owns the local finite-state machine, simulator I/O, and
//     button/lamp handling.
//   - networkThread exchanges snapshots with peers, tracks liveness, and
//     publishes a coherent world view to the rest of the process.
//   - assignerThread runs the external hall request assigner whenever the
//     network view is coherent.
//
// The package intentionally keeps these responsibilities separate so that local
// elevator behavior, network replication, and hall-call assignment can fail or
// stall independently without turning into one large shared-state module.
package main
