---
title: Behaviors
description: Switch message handlers with Become and UnBecome.
sidebarTitle: "🎬 Behaviors"
---

Actors can change their message-handling logic at runtime using **behaviors**. A behavior is a function `func(ctx *ReceiveContext)` that processes messages. The default behavior is the actor's `Receive` method.

## Behavior type

```go
type Behavior func(ctx *ReceiveContext)
```

Behaviors are stored on a stack. The top of the stack handles each incoming message.

## API

| Method                        | Purpose                                                                                                                                                     |
|-------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `ctx.Become(behavior)`        | Replace the current behavior. The current message finishes under the old behavior; subsequent messages use the new one. No stack—previous behavior is lost. |
| `ctx.BecomeStacked(behavior)` | Push a new behavior on top. Current message still uses the existing behavior; subsequent messages use the new one until `UnBecomeStacked`.                  |
| `ctx.UnBecomeStacked()`       | Pop the top behavior. Resume the previous one. No effect if the stack is empty.                                                                             |
| `ctx.UnBecome()`              | Reset to the default (initial) behavior. Clears the stack.                                                                                                  |

## Become vs BecomeStacked

- **Become** — Swap behavior. Use when transitioning to a new state and you do not need to return to the previous one.
- **BecomeStacked** — Push behavior. Use when you need to temporarily handle messages differently, then pop back (e.g., a modal dialog, a temporary protocol phase).

## Example

```go
func (a *AccountActor) Receive(ctx *ReceiveContext) {
    switch ctx.Message().(type) {
    case *Login:
        a.authenticate(ctx)
        ctx.Become(a.Authenticated)
    default:
        ctx.Unhandled()
    }
}

func (a *AccountActor) Authenticated(ctx *ReceiveContext) {
    switch ctx.Message().(type) {
    case *Logout:
        ctx.UnBecome()
    case *Transfer:
        ctx.BecomeStacked(a.ConfirmTransfer)
    default:
        ctx.Unhandled()
    }
}

func (a *AccountActor) ConfirmTransfer(ctx *ReceiveContext) {
    switch ctx.Message().(type) {
    case *Confirm:
        a.doTransfer(ctx)
        ctx.UnBecomeStacked()
    case *Cancel:
        ctx.UnBecomeStacked()
    default:
        ctx.Unhandled()
    }
}
```

## Stashing with behaviors

When switching behaviors, you may want to defer processing of the current message. Use `ctx.Stash()` to buffer it, then `ctx.Unstash()` or `ctx.UnstashAll()` after changing behavior. Requires `WithStashing()` at spawn. See [Stashing](stashing).
