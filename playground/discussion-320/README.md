# Discussion-320: Reentrancy Example

For more context check the github [discussion-320](https://github.com/Tochemey/goakt/discussions/320)

This playground demonstrates how reentrancy enables safe request/response cycles
between actors without blocking the caller.

## Scenario overview
The example models a common pattern where an actor needs data from another actor,
but that other actor also needs to ask back:

Client -> ActorA -> ActorB -> ActorA -> ActorB -> ActorA -> Client

Without reentrancy, ActorA would block and the cycle could deadlock. With
reentrancy enabled, ActorA keeps processing messages while it waits for its
request to complete.

## Actor roles
- Client: sends the initial message and waits for the final result.
- ActorA: starts the request to ActorB using RequestName (non-blocking).
- ActorB: asks ActorA for data using Ask (blocking) and responds to ActorA.

## Message flow
1. Client sends TestPing to ActorA.
2. ActorA calls RequestName to ActorB and returns immediately.
3. ActorB receives TestPing, then calls Ask on ActorA for TestGetCount.
4. ActorA replies with TestCount{Value: 42}.
5. ActorB responds to ActorA with that TestCount.
6. ActorA forwards the TestCount to the Client.

## Why RequestName is needed
Ask is blocking and would stall ActorA while it waits for ActorB. RequestName is
non-blocking and delivers the reply through ActorA's mailbox, so ActorA can still
handle incoming messages (including ActorB's Ask).

## How to run
```bash
go run ./playground/discussion-320
```

Expected output includes:
- "Client received count=42"
- "Reentrancy cycle completed."
- Prompt to press Ctrl+C to stop the system.

## Notes
- ActorA is spawned with `WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll)))` to allow the cycle.
- WithMaxInFlight sets a guardrail for outstanding Request calls.
- WithRequestTimeout bounds each request so failures do not stall the actor.
- RequestName records errors on the ReceiveContext and returns nil on failure.
  The Then callback runs without a ReceiveContext, so errors are logged or can
  be reported by sending a message back to the actor.
