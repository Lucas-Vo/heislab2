# Distributed Elevator Controller

This directory is the Go controller for the TTK4145 elevator project

The controller runs one elevator per process and coordinates hall calls across multiple elevators over peer-to-peer UDP. The design separates:

- local elevator control and door/state-machine logic
- distributed world-view replication and peer liveness tracking
- hall-call assignment through the bundled `hall_request_assigner`

## Features

- Shared hall calls and private cab calls.
- Hall button lamps used as a service guarantee.
- Direction-specific hall clearing: up and down hall calls are cleared separately.
- Three-second door-open behavior with obstruction-driven timer extension.
- Distributed hall-call assignment when peers agree on the shared state.
- Local fallback when no peer is alive.
- Network-assisted cab-call recovery after restart when another peer still remembers the last published state.
- Packet-loss mitigation by filtering delayed hall updates that would otherwise resurrect already served calls.

## Directory Overview

- `main.go`: starts the controller and wires the three long-running threads together.
- `elevatorthread.go`: owns the local FSM and simulator I/O.
- `networkthread.go`: merges local and remote snapshots and tracks peer liveness.
- `assignerthread.go`: runs the external hall request assigner on coherent snapshots.
- `common/`: shared configuration, request/snapshot types, and elevator I/O wrappers.
- `elevfsm/`: local elevator state machine, door logic, and request synchronization.
- `elevnetwork/`: network wrapper and merged world-view logic.
- `elevassigner/`: helper code plus the bundled `hall_request_assigner` executable.
- `Network-go/`: bundled UDP peer discovery and broadcast library.

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

```bash
./elevator
```

Behavior in this mode:

- Cab calls are handled locally.
- New hall calls are also accepted and served locally.
- No distributed assignment is needed.

## Multiple Elevators

The current configuration is intended for one process per workspace or machine on the same broadcast network.

For each participating machine:

1. Add its IP to `HostByID` in `common/config.go`.
2. Ensure UDP broadcast works on ports `4242` and `4243`.
3. Start one simulator and one `./elevator` process on that machine.

- simulator I/O is hard-coded to `localhost:15657`
- network ports are fixed in code

## Architecture Overview

The executable starts three long-running goroutines:

1. `elevatorThread`
   - Owns the local FSM in `elevfsm.Elevator`.
   - Polls buttons, floor sensor, and obstruction.
   - Applies assigned hall calls and local cab calls.
   - Publishes local snapshots and serviced updates.

2. `networkThread`
   - Owns `elevnetwork.WorldView`.
   - Merges local and remote snapshots.
   - Tracks peer liveness and startup/coherence state.
   - Republishes a coherent world view to the assigner and local FSM.

3. `assignerThread`
   - Receives coherent snapshots only.
   - Removes stale peers from the assigner input.
   - Runs the external `hall_request_assigner`.
   - Returns this elevator's assigned hall tasks to `elevatorThread`.

### Orders and Lights

- Hall calls are represented as `[floor][2]bool` and are shared cluster state.
- Cab calls are represented as part of the local elevator state and are never assigned to other elevators.
- Shared hall lights come from the coherent network snapshot.
- Cab lights are local to the elevator that owns the cab call.

### State-Machine Behavior

- Startup at a known floor: the elevator enters a defined state immediately.
- Startup between floors: the controller drives downward until a floor sensor is reached.
- Door-open duration is fixed to 3 seconds.
- Obstruction keeps resetting the door timer while active.
- When both hall directions exist at one floor, the controller clears only the announced direction and can keep the door open for a second 3-second interval when reversing direction.

## Fault Tolerance and Recovery

### Hall Calls

- New hall requests are merged into the shared world view with logical OR.
- Serviced hall requests are merged with logical AND, so one elevator clearing a call turns the shared hall light off.
- If delayed packets try to reintroduce a hall call that was just served, `WorldView` filters that update for a short validity window and re-broadcasts the clear.

### Cab Calls

- Cab calls are not stored on disk.
- Cab-call recovery is network-assisted: if another peer still has a snapshot containing this elevator's cab lights, those cab requests are merged back into the local state during startup.
- This means restart recovery of cab calls depends on another alive peer having seen the previous state.

### Network Loss and Reconnection

- If no peer is alive, hall calls are served locally without waiting for the assigner.
- If peers are alive but snapshots are incoherent, the assigner withholds new hall assignments until coherence returns.
- Peer liveness is timeout-based.
- The local elevator is also marked dead if `networkThread` stops receiving local state updates for long enough.

## Assumptions and Limitations

- Fixed configuration: 4 floors and 3 button types.
- One controller process per machine or workspace.
- No `--id` flag; identity is IP-based.
- No persistent storage on disk.
- Cab-call crash recovery without another peer is not implemented.
- Network partition handling beyond the course assumption is not implemented.
- Stop button semantics are effectively unspecified in this codebase; the controller does not use the stop button in its main control flow.
- Shared hall lights are only guaranteed to match immediately when the network is coherent; under packet loss or during recovery there can be temporary delay before convergence.

## Package Documentation

Package-level Go docs live in:

- `doc.go`
- `common/doc.go`
- `elevfsm/doc.go`
- `elevnetwork/doc.go`
- `elevassigner/doc.go`

Useful local inspection commands:

```bash
go doc .
go doc ./common
go doc ./elevfsm
go doc ./elevnetwork
go doc ./elevassigner
