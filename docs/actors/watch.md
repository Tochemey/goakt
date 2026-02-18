# Watch and Unwatch

`Watch` and `Unwatch` provide a mechanism for actors to monitor the lifecycle of other actors and receive notifications when they terminate. This is essential for building fault-tolerant, reactive systems that can respond to actor failures or shutdowns.

## Table of Contents

- 🤔 [What is Watch?](#what-is-watch)
- 💡 [Why Use Watch?](#why-use-watch)
- 👁️ [Watching Actors](#watching-actors)
- 🔕 [Unwatching Actors](#unwatching-actors)
- 📩 [The Terminated Message](#the-terminated-message)
- 🎯 [Common Use Cases](#common-use-cases)
- 🔄 [Watch vs Supervision](#watch-vs-supervision)
- ✅ [Best Practices](#best-practices)
- 📋 [Summary](#summary)

---

## What is Watch?

**Watch** is a mechanism that allows one actor (the **watcher**) to subscribe to termination notifications for another actor (the **watched**). When the watched actor terminates—either gracefully or due to a failure—the watcher receives a `Terminated` message.

**Key characteristics:**

- **Non-intrusive**: The watched actor is unaware it's being watched
- **Asynchronous**: Notification arrives as a regular message
- **Idempotent**: Watching the same actor multiple times has no adverse effect
- **Automatic cleanup**: Watch relationships are removed when either actor terminates
- **Local only**: Only local actors can be watched (not remote actors)

## Why Use Watch?

Use watch when you need to:

- **Monitor dependencies**: Detect when a service or dependency actor fails
- **Coordinate lifecycle**: Perform cleanup when related actors terminate
- **Build resilient patterns**: Implement circuit breakers, health checks
- **Resource management**: Release resources when actors shutdown
- **Dynamic supervision**: Implement custom supervision logic beyond the built-in supervisor
- **State synchronization**: React to actor termination in distributed systems
- **Connection management**: Detect disconnections and reconnect

## Watching Actors

- **From inside Receive:** Call **ctx.Watch(pid)** to watch another actor. When that actor terminates, you receive a **Terminated** message (with the watched actor’s address). Call **ctx.UnWatch(pid)** to stop watching.
- **From outside Receive:** Use **watcherPID.Watch(workerPID)** so the watcher gets **Terminated** when the worker stops.
- **Multiple actors:** You can watch multiple actors; each termination sends one **Terminated**. Watch is **local only** (no remote actors).

**Essential pattern:** Spawn workers, call **ctx.Watch(workerPID)** for each, and handle `*goaktpb.Terminated` to remove the PID from your list and optionally restart.

```go
func (c *Coordinator) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *StartWorkers:
        for i := 0; i < 5; i++ {
            workerPID, err := ctx.Spawn(fmt.Sprintf("worker-%d", i), &Worker{})
            if err != nil {
                continue
            }
            ctx.Watch(workerPID)
            c.workers = append(c.workers, workerPID)
        }

    case *goaktpb.Terminated:
        addr := msg.GetAddress()
        for i, w := range c.workers {
            if w.ID() == addr {
                c.workers = append(c.workers[:i], c.workers[i+1:]...)
                break
            }
        }
    }
}
```

## Unwatching Actors

Stop watching with `ctx.UnWatch(pid)` inside `Receive`, or `watcherPID.UnWatch(workerPID)` from outside. Both are idempotent (no-op if not watching).

```go
case *UnregisterWorker:
    ctx.UnWatch(msg.GetWorkerPID())
    a.removeWorker(msg.GetWorkerPID())
```

### Automatic Cleanup

Watch relationships are automatically cleaned up when:

- The watched actor terminates (you receive `Terminated` message)
- The watcher terminates
- The actor system shuts down

**Idempotent operations:**

- Watching an already-watched actor is safe (no duplicate notifications)
- Unwatching an actor that isn't watched is safe (no-op)
- Unwatching after receiving `Terminated` is safe (already cleaned up)

## The Terminated Message

When a watched actor terminates, the watcher receives a `Terminated` message defined in the `goaktpb` package:

```protobuf
message Terminated {
    // The address of the terminated actor
    string address = 1;
    // When the actor was terminated
    google.protobuf.Timestamp terminated_at = 2;
}
```

### Handling Terminated Messages

Handle `*goaktpb.Terminated` in `Receive`; use `msg.GetAddress()` to identify the actor (compare with `pid.ID()`). Clean up your state and optionally restart or notify others.

```go
case *goaktpb.Terminated:
    addr := msg.GetAddress()
    // Remove from your map/slice and optionally restart or notify
    a.handleTermination(ctx, addr)
```

### Terminated Message Properties

**`address` field:**
- Unique identifier of the terminated actor
- Format: `name@system:host:port`
- Can be compared with `PID.ID()` to identify which actor terminated

**`terminated_at` field:**
- Timestamp when the actor was terminated
- Type: `google.protobuf.Timestamp`
- Access via `msg.GetTerminatedAt().AsTime()` for Go `time.Time`

## Common Use Cases

- **Worker pool:** Spawn workers, `ctx.Watch(workerPID)` for each, maintain a map/slice by address. On `*goaktpb.Terminated`, remove that PID and optionally spawn a replacement to keep a minimum size (see [Watching Actors](#watching-actors)).
- **Connection manager:** On client connect, spawn a connection actor and `ctx.Watch(connPID)`; store by client ID. On `Terminated`, find client by address, remove from map, notify others.
- **Health monitor:** Register services with `ctx.Watch(servicePID)`; on `Terminated`, increment failure count, optionally restart or alert after a threshold.
- **Task coordination:** Spawn a worker per task, `ctx.Watch(workerPID)`, track by task ID. On `Terminated`, match address to task, cleanup, notify completion.
- **Circuit breaker:** Watch the service PID; on `Terminated`, increment failures and open circuit (reject requests); schedule a reset message to try again later.

## Watch vs Supervision

Watch and supervision serve different purposes and work together:

- **Supervision:** Parent supervises children; automatic restart/stop/resume via strategies. Use for built-in fault tolerance (see [Supervisor](supervisor.md)).
- **Watch:** Any actor can watch any other; you receive `Terminated` and implement custom logic. Use for lifecycle awareness, resource cleanup, and monitoring non-children.

| Scenario                        | Use                                         |
|---------------------------------|---------------------------------------------|
| Standard error recovery         | Supervision                                 |
| Child actor restart on failure  | Supervision                                 |
| Custom termination logic        | Watch                                       |
| Monitor non-child actors        | Watch                                       |
| Resource cleanup on termination | Watch                                       |
| Both together                   | Supervision + Watch for full fault handling |

## Best Practices

### Do's ✅

1. **Always handle `Terminated`:** Add a `case *goaktpb.Terminated:` in `Receive`; unhandled notifications go to dead letters.
2. **Match by address:** Use `msg.GetAddress()` and compare with `pid.ID()` to identify which actor terminated.
3. **Unwatch when done:** Call `ctx.UnWatch(pid)` when you no longer need the notification (e.g. on unregister).
4. **Combine with supervision:** Use supervision for automatic recovery and watch for custom cleanup or coordination.

### Don'ts ❌

1. **Don't watch yourself:** `ctx.Watch(ctx.Self())` is unnecessary overhead.
2. **Don't assume immediate delivery:** After `pid.Shutdown(ctx)`, the `Terminated` message is asynchronous; don't rely on it having arrived yet.
3. **Don't leak watches:** Unwatch or handle termination so relationships don't accumulate indefinitely.

### Idempotency

Watching the same actor multiple times does not create duplicate notifications. Unwatching an actor you are not watching is a no-op.

### Automatic Cleanup

Watch relationships are removed when the watched actor terminates (you get `Terminated`), when the watcher terminates, or when the system shuts down.

### Message Delivery

`Terminated` is delivered like a normal message: queued in the watcher's mailbox and processed in order. Delivery is best-effort if the watcher is alive; it may be delayed if the mailbox is full.

### Local Actors Only

Watch works only with **local** actors in the same actor system. Remote actors cannot be watched.

## Summary

- `Watch` subscribes an actor to termination notifications for another actor; `UnWatch` cancels it.
- When a watched actor terminates, the watcher receives a `Terminated` message (`msg.GetAddress()`).
- Idempotent and automatic cleanup when either actor or the system shuts down.
- Use for worker pools, connection management, health monitoring, task coordination; combine with supervision for full fault handling.
- Always handle `Terminated` in `Receive`; watch is local-only.
