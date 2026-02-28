---
title: Stashing
description: Buffer messages for later processing with Stash and Unstash.
sidebarTitle: "📥 Stashing"
---

Stashing lets an actor temporarily buffer messages instead of processing them. Use it when the actor cannot handle certain messages in its current state (e.g., waiting for a resource, or during a behavior transition).

## Enabling stashing

Pass `WithStashing()` as a `SpawnOption`:

```go
pid, err := system.Spawn(ctx, "my-actor", actor, actor.WithStashing())
```

Without `WithStashing()`, calls to `Stash`, `Unstash`, or `UnstashAll` record `ErrStashBufferNotSet`.

## API

| Method             | Purpose                                                                                                                                                 |
|--------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------|
| `ctx.Stash()`      | Buffer the current message. It will not be processed until unstashed. The message is moved to a side buffer; the actor continues with the next message. |
| `ctx.Unstash()`    | Dequeue the oldest stashed message and prepend it to the mailbox. Processes one message.                                                                |
| `ctx.UnstashAll()` | Move all stashed messages back to the mailbox in arrival order.                                                                                         |

Stashed messages are processed in FIFO order when unstashed.

## When to use

- **Behavior transitions** — Stash messages you cannot handle in the current behavior; unstash after switching.
- **Conditional processing** — Stash messages until a condition is met (e.g., dependency ready, lock acquired).
- **Reentrancy** — With `StashNonReentrant` mode, in-flight Ask requests cause user messages to be stashed automatically until the request completes.

## Example

```go
func (a *GateActor) Receive(ctx *ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Open:
        a.open = true
        ctx.UnstashAll()
    case *Request:
        if a.open {
            a.handle(ctx, msg)
        } else {
            ctx.Stash()
        }
    default:
        ctx.Unhandled()
    }
}
```

## Stash size

`pid.StashSize()` returns the number of messages currently in the stash. Useful for metrics or backpressure.
