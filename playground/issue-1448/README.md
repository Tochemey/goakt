# Issue 1448: a name lookup panics on an actor that is leaving the system

https://github.com/Tochemey/goakt/issues/1448

A name lookup finds the actor's node in the actors tree under the tree's lock,
then reads the node's PID after releasing it. When the actor stops,
`tree.deleteNode` clears that PID slot. A lookup that found the node just
before the actor left reads a nil PID and dereferences it. The panic is on the
caller's goroutine, so it ends the process.

The sample keeps stopping and spawning eight actors from two goroutines while
eight goroutines call one lookup API on the same names for three seconds. The
API is the first argument:

| Argument              | Call                         | Nil PID dereferenced in |
|-----------------------|------------------------------|-------------------------|
| `ActorOf` (default)   | `ActorSystem.ActorOf`        | `PID.IsStopping`        |
| `ActorExists`         | `ActorSystem.ActorExists`    | `PID.IsStopping`        |
| `Kill`                | `ActorSystem.Kill`           | `PID.Shutdown`          |
| `Child`               | `PID.Child`                  | `PID.IsRunning`         |

In production this happens whenever actors are stopped and looked up by name
at the same time, for example while an application stops.

## Actual (before the fix)

Every argument panics within milliseconds. For `ActorOf`:

```
calling ActorOf on 8 actors while they stop and start again, for 3s
panic: runtime error: invalid memory address or nil pointer dereference [recovered, repanicked]
[signal SIGSEGV: segmentation violation code=0x2 addr=0x0 pc=0x100c5c6d8]

goroutine 58 [running]:
...
github.com/tochemey/goakt/v4/actor.(*PID).isStateSet(...)
	actor/pid_state.go:74
github.com/tochemey/goakt/v4/actor.(*PID).IsStopping(0x7957b749ca88?)
	actor/pid.go:856 +0xd8
github.com/tochemey/goakt/v4/actor.(*actorSystem).ActorOf(0x7957b749ca88, {0x101cea048, 0x101e32bc8}, {0x7957b78bcb80, 0x7})
	actor/actor_system.go:2354 +0x2a0
...
exit status 2
```

## Expected (after the fix)

```
calling ActorOf on 8 actors while they stop and start again, for 3s
PASS: ActorOf answered for actors leaving the system without crashing
```

## Run

```bash
go run ./playground/issue-1448 ActorOf
go run ./playground/issue-1448 ActorExists
go run ./playground/issue-1448 Kill
go run ./playground/issue-1448 Child
```
