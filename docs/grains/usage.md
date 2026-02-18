# Grain Usage Patterns

Practical patterns for using grains. See [Grains Overview](overview.md) for concepts and [Stateful Grains](stateful_grains.md) for persistence.

## Table of Contents

- 🚀 [Basic Patterns](#basic-patterns)
- 🔧 [Advanced Patterns](#advanced-patterns)
- 🔗 [Integration Patterns](#integration-patterns)
- ⚡ [Performance](#performance)
- ✅ [Best Practices](#best-practices-summary)
- ⚠️ [Common Pitfalls](#common-pitfalls)

---

## Basic Patterns

Implement `OnActivate` (load state or init), `OnReceive` (switch on message type; use `ctx.Response()` or `ctx.Err()`), and `OnDeactivate` (persist if needed). Register the grain kind, then resolve identity by name and use `TellGrain` / `AskGrain`.

**Minimal grain:**

```go
type CounterGrain struct {
    count int64
}

func (g *CounterGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    g.count = 0
    return nil
}

func (g *CounterGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *Increment:
        g.count += msg.Delta
        ctx.Response(&CountResponse{Count: g.count})
    case *GetCount:
        ctx.Response(&CountResponse{Count: g.count})
    default:
        ctx.Unhandled()
    }
}

func (g *CounterGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    return nil
}
```

**Usage:** Register with the system (or cluster `WithGrains`), then `GrainIdentity(ctx, name, factory)` to resolve (activates on first use), and `TellGrain(ctx, identity, msg)` or `AskGrain(ctx, identity, msg, timeout)` to communicate.

## Advanced Patterns

- **Entity / CRUD grain:** Load entity in `OnActivate` (e.g. from DB via injected dependency), update in `OnReceive`, persist in `OnDeactivate`. Use `props.Dependencies()` to resolve repositories.
- **Event-sourced aggregate:** In `OnActivate`, load events from store and replay into in-memory state. In `OnReceive`, append new events and apply them; in `OnDeactivate`, persist new events.
- **Saga coordinator:** Track saga ID, state, and steps. On `StartSaga` run first step; on `StepCompleted` advance and run next or complete; on `StepFailed` run compensation. Persist state in `OnDeactivate`.
- **Session / key-value grain:** Identity = session ID. Store `map[string]interface{}` or similar; check expiry in `OnReceive`; persist in `OnDeactivate` (e.g. to Redis).


## Integration Patterns

- **HTTP API:** In the handler, get entity ID from request, call `system.GrainIdentity(ctx, id, factory)` to resolve (activates on first use), then `AskGrain` for reads or `TellGrain` for writes; encode response to JSON.
- **Message queue (e.g. Kafka):** In the consumer loop, parse the message, extract the grain identity key (e.g. order ID), call `GrainIdentity(ctx, key, factory)` and `TellGrain(ctx, identity, msg)` to push work to the grain.
- **Scheduled tasks:** Use `ActorSystem` schedule (e.g. `ScheduleOnce`) to send a message to the grain at a future time, or start a goroutine with a timer that calls `TellGrain` with a "trigger" message when the timer fires.

## Performance

- **Batching:** Accumulate updates in a slice in `OnReceive`; flush to storage when the batch reaches a size or on an explicit `FlushBatch` message.
- **Caching:** In `OnActivate`, try cache first; on miss load from DB and populate cache. Reduces external calls for hot grains.


## Best Practices Summary

1. **Keep grains small and focused**: One entity or aggregate per grain.
2. **Persist state**: Use `OnDeactivate` to save state.
3. **Handle failures**: Return errors from `OnActivate` for retry.
4. **Use dependencies**: Inject external services via `WithGrainDependencies`.
5. **Set appropriate timeouts**: Tune passivation based on access patterns.
6. **Test thoroughly**: Unit test grain logic; integration test persistence.
7. **Monitor metrics**: Track activation rate, latency, memory usage.
8. **Use bounded mailboxes**: Apply backpressure under load.
9. **Batch when possible**: Group related updates.
10. **Cache hot data**: Reduce external service calls.

## Common Pitfalls

- **Don't block in OnReceive:** Use async patterns (e.g. goroutine that sends a result message back) instead of blocking I/O in the handler.
- **Don't retain GrainContext:** Use `ctx` only inside the method; do not store it in the grain struct.
- **Don't ignore OnActivate errors:** Return errors from `OnActivate` so the system can retry activation.
