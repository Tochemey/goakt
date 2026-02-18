# Stashing

Stashing is a message deferral mechanism that allows actors to temporarily set aside messages for later processing. This is particularly useful when an actor is not ready to handle certain messages in its current state.

## Table of Contents

- 🤔 [What is Stashing?](#what-is-stashing)
- 💡 [When to Use Stashing](#when-to-use-stashing)
- 🚀 [Basic Usage](#basic-usage)
- 🛠️ [Stashing Operations](#stashing-operations)
- 💡 [Example & Other Use Cases](#example--other-use-cases)
- ✅ [Stashing Best Practices](#stashing-best-practices)
- 🧩 [Common Patterns](#common-patterns)
- ⚡ [Performance Considerations](#performance-considerations)
- ⚠️ [Limitations](#limitations)
- ⚠️ [Error Handling](#error-handling)
- 📋 [Summary](#summary)

---

## What is Stashing?

Stashing is a mechanism you can enable in your actors, so they can temporarily stash away messages they cannot or should not handle at the moment. Another way to see it is that stashing allows you to keep processing messages you can handle while saving for later messages you can't. Stashing is handled by GoAkt out of the actor instance just like the mailbox, so if the actor dies while processing a message, all messages in the stash are deleted. This feature is usually used together with `Become/UnBecome`, as they fit together very well, but this is not a requirement.

## When to Use Stashing

Stashing is useful when:

- **Initialization**: Actor is waiting for setup to complete
- **State transitions**: Actor is changing states and can't process certain messages
- **Resource acquisition**: Waiting for a resource before processing
- **Ordering requirements**: Ensuring messages are processed in a specific order
- **Conditional processing**: Deferring messages until a condition is met

## Basic Usage

Enable stashing at spawn with `actor.WithStashing()`. In `Receive`, call `ctx.Stash()` to defer the current message (it goes to a stash queue and will not be processed until you unstash). When ready, call `ctx.Unstash()` to replay one stashed message, or `ctx.UnstashAll()` to replay all. Stashed messages are re-enqueued to the mailbox in stash order. Use when the actor is not ready (e.g. initializing, waiting for a resource, or in a state that can't handle the message yet). Works well with `Become`/`UnBecome` (see [Behaviours](behaviours.md)). Stash is per-actor and cleared when the actor stops.

When you become ready (e.g. after handling an init message), set your ready flag and call `ctx.UnstashAll()` so previously stashed messages are replayed.

## Stashing Operations

| Operation          | Effect                                                                               |
|--------------------|--------------------------------------------------------------------------------------|
| `ctx.Stash()`      | Defer current message for later; adds to stash (FIFO). Message is not processed now. |
| `ctx.Unstash()`    | Replay oldest stashed message (prepends to mailbox). No-op if stash is empty.        |
| `ctx.UnstashAll()` | Replay all stashed messages in order; efficient for bulk replay when ready.          |

## Example & Other Use Cases

**Initialization:** Wait for setup before processing work. Stash work messages until an init message sets a ready flag, then call `ctx.UnstashAll()`.

```go
func (a *WorkerActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Initialize:
        a.initialized = true
        ctx.UnstashAll()

    case *WorkItem:
        if !a.initialized {
            ctx.Stash()
            return
        }
        a.processWork(msg)
    }
}
```

**State transitions:** In the waiting behavior, stash messages you can’t handle yet; when transitioning to the active behavior, call `ctx.UnstashAll()`. (Use `ctx.Become(a.waitingBehavior)` from `Receive` to enter this behavior.)

```go
func (a *Actor) waitingBehavior(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Ready:
        ctx.Become(a.activeBehavior)
        ctx.UnstashAll()
    default:
        ctx.Stash()
    }
}
```

**Resource acquisition:** Stash requests until a “ready” message (e.g. DB connected) arrives, then set the flag and `ctx.UnstashAll()`.

```go
func (a *Actor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ResourceReady:
        a.hasResource = true
        ctx.UnstashAll()
    case *Request:
        if !a.hasResource {
            ctx.Stash()
            return
        }
        a.handle(msg)
    }
}
```

**Batch processing:** Stash until the batch is full; process batch then `ctx.Unstash()` to pull the next item into the batch.

```go
func (a *BatchActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *WorkItem:
        a.batch = append(a.batch, msg)
        if len(a.batch) < a.batchSize {
            ctx.Stash()
            return
        }
        a.processBatch(a.batch)
        a.batch = nil
        ctx.Unstash()
    }
}
```

**Conditional processing:** Stash when over limit; when the condition resets (e.g. new window), call `ctx.UnstashAll()`.

```go
func (a *ThrottledActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Request:
        if a.count >= a.limit {
            ctx.Stash()
            return
        }
        a.count++
        a.handle(msg)
    case *WindowReset:
        a.count = 0
        ctx.UnstashAll()
    }
}
```

## Stashing Best Practices

### Do's ✅

1. **Enable stashing**: Use `WithStashing()` when spawning.
2. **Unstash when ready**: Call `ctx.UnstashAll()` (or `ctx.Unstash()`) after the condition is met; stashed messages are not processed automatically.
3. **Use with behaviors**: Combine with `Become`/`UnBecome` for state machines (stash in waiting state, unstash when transitioning).
4. **Limit stash size**: Be mindful of memory; the stash is unbounded.
5. **Document stashing logic**: Make it clear why messages are stashed.

### Don'ts ❌

1. **Don't forget to unstash**: Stashed messages stay in the stash until you call `Unstash`/`UnstashAll`.
2. **Don't use without enabling**: Stashing requires `WithStashing()` at spawn or you get an error.
3. **Don't stash everything**: Only stash messages you truly can't handle yet.
4. **Don't ignore stash growth**: Monitor and reason about stash size in production.

## Common Patterns

- **Initialization gate:** On init message → set ready, `ctx.UnstashAll()`. On work message → if not ready, `ctx.Stash()`; else process.
- **State machine:** In waiting behavior → stash unknown messages; on “ready” message → `ctx.Become(activeBehavior)` and `ctx.UnstashAll()`. In active behavior → handle normally.
- **Resource / pool:** Stash work when resource (or worker) unavailable; when “available” message arrives → `ctx.Unstash()` or `ctx.UnstashAll()`.

## Performance Considerations

- **Memory**: Stash buffer is unbounded by default
- **Order**: Stashed messages maintain FIFO order
- **Low overhead when unused**: Negligible cost when the stash is empty
- **Unstashing**: O(1) for single unstash, O(n) for unstash all

## Limitations

- **Must enable**: Stashing requires `WithStashing()`
- **Unbounded**: Stash buffer has no size limit
- **Memory**: Large stashes consume memory
- **No persistence**: Stashed messages lost on actor restart

## Error Handling

If stashing is not enabled, `ctx.Stash()` (and unstash) will error. Always pass `WithStashing()` when spawning:

```go
pid, err := actorSystem.Spawn(ctx, "actor", &MyActor{}, actor.WithStashing())
```

## Summary

- Stashing defers messages for later processing.
- Enable with `WithStashing()` when spawning; use `ctx.Stash()`, `ctx.Unstash()`, and `ctx.UnstashAll()` in `Receive`.
- Use during initialization, state transitions, or resource acquisition; combine with `Become`/`UnBecome` for state machines.
- Remember to unstash when the actor is ready.
