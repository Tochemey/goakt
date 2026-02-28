---
title: Death Watch
description: Watch actors and react when they terminate.
sidebarTitle: "👁️ Death Watch"
---

Death watch lets an actor receive a `Terminated` message when another actor stops. Use it to clean up references, fail over, or react to actor lifecycle.

## API

| Method             | Purpose                                            |
|--------------------|----------------------------------------------------|
| `pid.Watch(cid)`   | Register to receive `Terminated` when `cid` stops. |
| `pid.UnWatch(cid)` | Cancel the watch for `cid`.                        |
| `ctx.Watch(cid)`   | Same as `ctx.Self().Watch(cid)`.                   |
| `ctx.UnWatch(cid)` | Same as `ctx.Self().UnWatch(cid)`.                 |

`Watch` and `UnWatch` are on `*PID` and `ReceiveContext`. Use `ctx.Watch(pid)` from inside `Receive` to watch another actor.

## Terminated message

When a watched actor stops, the watcher receives:

```go
type Terminated struct {
    // Address returns the address (ID) of the terminated actor.
    Address() string
}
```

Handle it in `Receive`:

```go
case *actor.Terminated:
    addr := msg.Address()
    // remove from local state, restart a replacement, etc.
```

## When to use

- **Dependencies** — Watch a child or peer; when it stops, recreate or fail over.
- **Resource cleanup** — Release references to the stopped actor.
- **Supervision** — Custom logic when a watched actor dies.

## Automatic unwatch

When an actor stops, the system automatically calls `UnWatch` on all actors it was watching. Watchers do not need to call `UnWatch` when they receive `Terminated`—the watch is already cancelled. Call `UnWatch` only when you want to stop watching before the target terminates.

## Example

```go
func (a *ParentActor) Receive(ctx *ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *SpawnWorker:
        pid, _ := ctx.Spawn("worker", NewWorkerActor())
        ctx.Watch(pid)
        a.workers[pid.ID()] = pid
    case *actor.Terminated:
        delete(a.workers, msg.Address())
        // optionally respawn
    default:
        ctx.Unhandled()
    }
}
```
