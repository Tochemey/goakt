# Reentrancy

## Concept

By default, an actor processes messages strictly one at a time. **Reentrancy** allows an actor to process other messages while waiting for an async response from `Request` or `RequestName`.

`Request` and `RequestName` are the non-blocking counterparts of `Ask` and `SendSync`. They return immediately with a `RequestCall` handle instead of blocking until a reply arrives. The reply is delivered back through the actor's mailbox, preserving single-threaded processing.

## Modes

| Mode                  | Behavior                                                                                                                                                                                                                 |
|-----------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Off**               | Strict sequential; `Request`/`RequestName` disabled. Use `Ask`/`SendSync` for sync calls.                                                                                                                                |
| **AllowAll**          | All incoming messages can be processed while awaiting a response. Maximizes throughput.                                                                                                                                  |
| **StashNonReentrant** | User messages are stashed while any stash-mode request is in flight; only async responses and system messages (e.g. `PoisonPill`, `HealthCheckRequest`) are processed. Preserves strict ordering at the cost of latency. |

## Enabling reentrancy

Reentrancy is **opt-in**. Pass `WithReentrancy` when spawning the actor:

```go
import "github.com/tochemey/goakt/v4/reentrancy"

cfg := reentrancy.New(
    reentrancy.WithMode(reentrancy.AllowAll),
    reentrancy.WithMaxInFlight(10),  // optional: cap concurrent requests
)
pid, err := system.Spawn(ctx, "my-actor", actor, actor.WithReentrancy(cfg))
```

| Option               | Purpose                                                                                                                                             |
|----------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------|
| `WithMode(mode)`     | Set reentrancy mode: `Off`, `AllowAll`, or `StashNonReentrant`.                                                                                     |
| `WithMaxInFlight(n)` | Cap the number of outstanding async requests. When reached, `Request`/`RequestName` return `ErrReentrancyInFlightLimit`. Use `n <= 0` for no limit. |

Without `WithReentrancy`, calls to `Request` or `RequestName` fail with `ErrReentrancyDisabled`.

## Request and RequestName

| Method                                    | Purpose                                                                                        |
|-------------------------------------------|------------------------------------------------------------------------------------------------|
| `ctx.Request(to, message, opts...)`       | Send an async request to a PID. Returns a `RequestCall` or `nil` on failure.                   |
| `ctx.RequestName(name, message, opts...)` | Send an async request to an actor by name (local or remote). Returns a `RequestCall` or `nil`. |

On failure to initiate the request, `Err` is set on the context and the returned call is `nil`. Check `ctx.Err()` or use `ctx.getError()` in tests.

## RequestCall

The returned `RequestCall` lets you:

| Method           | Purpose                                                                                                                                                                                                                                        |
|------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Then(callback)` | Register a continuation `func(any, error)` invoked when the request completes (success, error, timeout, or cancel). Only the first call is honored. If the request already completed, the callback runs immediately in the caller's goroutine. |
| `Cancel()`       | Request cancellation. Best-effort; the request may still complete. Idempotent. Returns `nil` if already completed or cancel requested.                                                                                                         |

Continuations registered with `Then` run on the actor's mailbox thread when the request completes, preserving single-threaded access to actor state. Call `Then` from within `Receive` to ensure correct execution.

## Per-call options

| Option                        | Purpose                                                                                                |
|-------------------------------|--------------------------------------------------------------------------------------------------------|
| `WithReentrancyMode(mode)`    | Override the actor-level mode for this request only. Cannot enable if actor has reentrancy off.        |
| `WithRequestTimeout(timeout)` | Set a per-request timeout. On expiry, the request completes with `ErrRequestTimeout`. `<= 0` disables. |

```go
call := ctx.Request(target, msg,
    actor.WithReentrancyMode(reentrancy.StashNonReentrant),
    actor.WithRequestTimeout(5*time.Second),
)
if call != nil {
    call.Then(func(resp any, err error) {
        if err != nil {
            // handle timeout, cancel, or remote error
            return
        }
        // use resp
    })
}
```

## Errors

| Error                        | When                                                                    |
|------------------------------|-------------------------------------------------------------------------|
| `ErrReentrancyDisabled`      | Actor was not spawned with `WithReentrancy`, or per-call mode is `Off`. |
| `ErrReentrancyInFlightLimit` | `MaxInFlight` cap reached; no new async requests until some complete.   |
| `ErrRequestTimeout`          | Request timed out (via `WithRequestTimeout`).                           |
| `ErrRequestCanceled`         | Request was canceled via `Cancel()`.                                    |

## StashNonReentrant and stashing

In `StashNonReentrant` mode, user messages are automatically stashed while any stash-mode request is in flight. A stash buffer is created on demand; you do **not** need `WithStashing()` for reentrancy-driven stashing. When the last blocking request completes, stashed messages are unstashed and processed in order.

## When to use

- **Actors that make async requests** and need to stay responsive (e.g. fan-out, long I/O via PipeTo).
- **Avoiding deadlock** in call cycles (A -> B -> A). Use `AllowAll` so A can process B's reply while waiting.
- **Strict ordering** when you must not interleave user messages with async responses—use `StashNonReentrant`.

## Production notes

- Prefer **AllowAll** for throughput and to avoid deadlocks in call cycles.
- Use **StashNonReentrant** only when strict message ordering is required. Pair it with:
  - A finite `MaxInFlight` limit to bound memory.
  - Per-request timeouts (`WithRequestTimeout`) to avoid unbounded stashing if dependencies stall.
- `AllowAll` can introduce state races if your logic assumes strict ordering between request and response.
- Mixed-version clusters may decode unknown modes as `Off`, disabling async requests.

## Example

```go
// Coordinator calls Worker asynchronously. AllowAll keeps the actor responsive.
coordinator, _ := system.Spawn(ctx, "coordinator", &CoordinatorActor{},
    actor.WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

// In CoordinatorActor.Receive:
func (a *CoordinatorActor) Receive(rctx *ReceiveContext) {
    switch msg := rctx.Message().(type) {
    case *StartJob:
        call := rctx.RequestName("worker", &DoWork{JobID: msg.ID}, actor.WithRequestTimeout(5*time.Second))
        if call == nil {
            rctx.Response(gerrors.ErrReentrancyDisabled)
            return
        }
        call.Then(func(resp any, err error) {
            if err != nil {
                rctx.Response(NewStatusFailure(err.Error(), msg))
                return
            }
            rctx.Response(resp)
        })
    case *GetStatus:
        rctx.Response(a.currentStatus)
    default:
        rctx.Unhandled()
    }
}
```

## See also

- [Stashing](stashing.md) — Manual stashing with `Stash`, `Unstash`, `UnstashAll`; `StashNonReentrant` uses stashing internally.
- [Messaging](messaging.md) — `Ask`, `Tell`, `PipeTo`.
- [Behaviors](behaviors.md) — `Become`, `UnBecome` for state transitions.
