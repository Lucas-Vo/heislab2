# Distributed Elevator Controller

This directory is the Go controller for the TTK4145 elevator project

The controller runs one elevator per process and coordinates hall requests across multiple elevators over peer-to-peer UDP. The design separates:

- local elevator control and door/state-machine logic
- distributed world-view replication and peer liveness tracking
- hall-request assignment through the bundled `hall_request_assigner`

## Features

- Shared hall requests and private cab requests.
- Hall button lamps used as a service guarantee.
- Direction-specific hall clearing: up and down hall requests are cleared separately.
- Three-second door-open behavior with obstruction-driven timer extension.
- Distributed hall-request assignment when peers agree on the shared state.
- Local fallback when no peer is alive.
- Network-assisted cab-request recovery after restart.
- Packet-loss tolerance.

## Directory Overview

- `main.go`: starts the controller and wires the three long-running threads together.
- `elevatorthread.go`: manages elevator FSM transitions and requests.
- `networkthread.go`: merges local and remote snapshots and tracks peer liveness.
- `assignerthread.go`: runs the external hall request assigner on coherent snapshots.
- `common/`: shared configuration and utilities, request/snapshot types.
- `elevfsm/`: local elevator state machine, door logic, and request synchronization.
- `elevhw/`: elevator I/O and elevator I/O wrappers
- `elevnetwork/`: network wrapper and merged world-view logic.
- `elevassigner/`: the bundled `hall_request_assigner` executable + readme.
- `Network-go/`: bundled UDP peer discovery and broadcast library.

the Network-go submodule is taken from (link), and the assigner, a build of the hall_request_assigner.d from (link).

## Build

Prerequisites:

- Go 1.24 or newer.
- An elevator server/simulator available on `localhost:15657`.
- The bundled hall request assigner executable at `./elevassigner/hall_request_assigner`.

Build from this directory:

```bash
go build -o elevator .
```


## Run

Start the simulator/elevator server first, then run:

```bash
./elevator
```

## Elevator identity

- Elevator identity is derived from the machine's local IP address and the `HostByID` map in `common/config.go`.

## One Elevator

Single-elevator operation is the default when no peer is alive.

Behavior in this mode:

- Cab requests are handled locally.
- New hall requests are also accepted and served locally.
- No distributed assignment is needed.

## Multiple Elevators

The current configuration is intended for one process per workspace or machine on the same broadcast network.

For each participating machine:

1. Add its IP to `HostByID` in `common/config.go`.
2. Ensure UDP broadcast works on ports `4242` and `4243`.
3. Start elevatorserver and one `./elevator` process on that machine.

- Elevator/Simulator I/O is hard-coded to `localhost:15657`
- network ports are fixed in code

## Architecture Overview

The executable starts three long-running goroutines:

1. `elevatorThread`
   - Owns the local FSM in `elevfsm.Elevator`, `elevfsm.RequestManager`.
   - Polls buttons, floor sensor, and obstruction.
   - Applies assigned hall requests and local cab requests.
   - Publishes local snapshots and serviced updates.

2. `networkThread`
   - Owns `elevnetwork.WorldView` and `elevnetwork.PeerNetwork`.
   - Merges local and remote snapshots.
   - Tracks peer liveness and startup/coherence state.
   - Republishes a coherent world view to the assigner and local FSM.

3. `assignerThread`
   - Receives coherent snapshots only.
   - Removes stale peers from the assigner input.
   - Runs the external `hall_request_assigner`.
   - Returns this elevator's assigned hall tasks to `elevatorThread`.

### Orders and Lights

- Hall requests are represented as `HallRequests = [N_FLOORS][2]bool` and are shared cluster state.
- Cab requests are represented as part of the local elevator state and are never assigned to other elevators.
- Shared hall lights come from the coherent network snapshot.
- Cab lights are local to the elevator that owns the cab request.

### State-Machine Behavior

- Startup at a known floor: the elevator enters a defined state immediately.
- Startup between floors: the controller drives downward until a floor sensor is reached.
- Door-open duration is set to 3 seconds.
- Obstruction keeps resetting the door timer while active.
- When both hall directions exist at one floor, the controller initially only clears the announced direction and can keep the door open for an additional 3-seconds interval if reversing direction.

## Fault Tolerance and Recovery

### Hall Requests

- New hall requests are merged into the shared world view with logical OR.
- Serviced hall requests are merged with logical AND.
- If delayed packets try to reintroduce a hall request that was just served, `WorldView` filters that update for a short validity window and re-broadcasts the now filtered snapshot.

### Cab Requests

- If another peer still has a snapshot containing this elevator's cab requests, those cab requests are merged back into the local state during startup.
- The cab lights won't turn on before the worldview is coherent with the rest of the alive network. This gives a guarantee for all lit cab calls.

### Network Loss and Reconnection

- If no peer is alive, hall requests are served locally without waiting for the assigner.
- If peers are alive but snapshots are incoherent, the assigner withholds new hall assignments until coherence returns.
- Peer liveness is timeout-based.
- The local elevator is also marked dead if `networkThread` stops receiving local state updates for long enough.

## Assumptions and Limitations

- One controller process per machine or workspace.
- Cab-request crash recovery without another peer is not implemented.
- Network partition handling beyond the course assumption is not implemented.
- Stop button functionality is left as an exercise to the reader
- Shared hall lights are only guaranteed to match when the network is coherent. under packet loss or during recovery there can be temporary delay before convergence.