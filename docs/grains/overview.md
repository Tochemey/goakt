# Grains (Virtual Actors)

## Concept

Grains are **virtual actors**: identity-addressed and managed by the framework. They are activated on first message and
deactivated when idle. You don't spawn them explicitly; you send to an identity and the framework routes to the right
instance.

## The Grain interface

Every grain must implement the `Grain` interface:

```go
type Grain interface {
    OnActivate(ctx context.Context, props *GrainProps) error
    OnReceive(ctx *GrainContext)
    OnDeactivate(ctx context.Context, props *GrainProps) error
}
```

| Method           | When called                                          | Purpose                                                                            |
|------------------|------------------------------------------------------|------------------------------------------------------------------------------------|
| **OnActivate**   | When the grain is loaded into memory (first message) | Initialize state or resources. Return error to prevent activation.                 |
| **OnReceive**    | For each message                                     | Handle messages. Use `ctx.Response(resp)` for Ask, `ctx.NoErr()` for Tell success. |
| **OnDeactivate** | Before the grain is removed (passivation)            | Persist state, release resources.                                                  |

## GrainContext

Inside `OnReceive`, `GrainContext` provides:

| Method                         | Purpose                               |
|--------------------------------|---------------------------------------|
| `Message()`                    | The message being processed           |
| `Self()`                       | Grain identity                        |
| `Response(resp)`               | Reply to an Ask                       |
| `NoErr()`                      | Signal success for Tell (no response) |
| `Err(err)`                     | Signal failure                        |
| `Unhandled()`                  | Mark message as unhandled             |
| `TellActor(name, msg)`         | Send to an actor by name              |
| `AskActor(name, msg, timeout)` | Ask an actor by name                  |
| `AskGrain(to, msg, timeout)`   | Ask another grain                     |

## Identity and messaging

Use `GrainIdentity(ctx, instanceName, factory)` to obtain an identity. The factory creates the grain when the framework
activates it. Then use `TellGrain` or `AskGrain` with that identity. The instance name (e.g., `"user-123"`) uniquely
identifies the grain within its type.

## When to use grains

- Entity-per-identity patterns (users, sessions, devices)
- Large populations that are mostly idle
- When you want the framework to manage lifecycle and placement

## When to use actors

- Long-lived services
- Explicit lifecycle control
- Infrastructure components

## Grain lifecycle

1. First message arrives → framework activates the grain (calls `OnActivate`)
2. Messages are routed to the grain's `OnReceive`
3. After idle timeout (passivation) → grain is deactivated (`OnDeactivate`)
4. Next message → grain is reactivated

## Activation guarantee

In cluster mode, the system claims grain ownership in the cluster registry using an atomic put-if-absent operation before
activating locally. If another node already owns the grain, requests are forwarded to that owner. If local activation
fails after a successful claim, the claim is removed.

When configured, the grain activation barrier delays grain activations until the cluster reaches the minimum peers
quorum (or until the barrier timeout elapses).

## See also

- [Passivation](../actor/passivation.md) — Grains use passivation for idle-based deactivation
- [Actor Model](../actor/actor-model.md) — When to use actors vs grains
