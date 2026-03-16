# Elevator

Distributed elevator controller in Go using peer-to-peer UDP communication.

## Build and run

Build and run the controller from the project root:

```bash
go run .
```

Runtime dependencies:

- An elevator server must be available on `localhost:15657`
- The hall request assigner binary must exist at `./elevassigner/hall_request_assigner`
- For multi-node runs, each machine must be reachable on the configured UDP ports

## Module overview

- `main.go`: starts the application and wires the threads together with channels
- `elevatorthread.go`: runs the local elevator FSM and talks to the elevator I/O layer
- `networkthread.go`: maintains cluster state, liveness, coherence, and snapshot distribution
- `assignerthread.go`: runs the external hall request assigner and forwards assigned hall calls
- `common/`: shared config, snapshot types, request types, and elevator I/O wrappers
- `elevfsm/`: local elevator behavior and request handling
- `elevnetwork/`: network wrapper and world-view merge logic
- `elevassigner/`: assigner helpers plus the `hall_request_assigner` executable
- `Network-go/`: vendored UDP peer discovery and broadcast library

## Assumptions and config

- The app has no CLI flags; configuration is hardcoded in `common/config.go`
- Elevator identity is derived from the machine's local IP and matched against `HostByID`
- Peer discovery uses UDP broadcast on port `4242`
- Snapshot/message exchange uses UDP broadcast on port `4243`
- Elevator I/O is hardcoded to `localhost:15657` in `elevfsm/elevator.go`
- The system assumes 4 floors and 3 button types
- Important timing is hardcoded in the thread packages, including:
  - local elevator polling: `25ms`
  - local snapshot publish: `50ms`
  - network broadcast: `1s`
  - world-view timeout: `4s`
  - elevator fault timeout: `6s`
- `go.mod` includes `replace Driver-go => ./driver-go`; if that path is missing in your checkout, the module file may need cleanup before building

## Architecture summary

The program is split into three long-running goroutines:

1. `elevatorThread` owns the local FSM and elevator I/O.
2. `networkThread` shares state with peers and builds a coherent world view.
3. `assignerThread` computes hall-call assignments from the shared snapshot.

Networking is peer-to-peer. Nodes discover each other with UDP broadcast heartbeats and exchange elevator snapshots over UDP broadcast as JSON messages. Hall calls are treated as shared cluster state, while cab calls remain local to each elevator. When the network view is coherent, hall calls are assigned through the external hall request assigner; when peers disappear, the local node can continue serving requests on its own.
