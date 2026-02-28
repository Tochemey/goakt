---
title: PipeTo
description: Run async tasks and deliver results to actors.
sidebarTitle: "➡️ PipeTo"
---

PipeTo runs a task asynchronously and delivers its result (or failure) to a target actor's mailbox. Use it to offload I/O or CPU-bound work without blocking the actor, while keeping the result in the actor model.

**Note:** PipeTo can only be invoked by **local actors**. Calling `pid.PipeTo` or `ctx.PipeTo` when the sender (`pid` or `ctx.Self()`) is a remote PID returns `ErrNotLocal`. The task runs in a goroutine on the caller's node; remote PIDs do not have a local execution context to run it.


## API

### From ReceiveContext

| Method                                     | Purpose                                                                                                  |
|--------------------------------------------|----------------------------------------------------------------------------------------------------------|
| `ctx.PipeTo(to, task, opts...)`            | Run `task` in a goroutine; on success, send the result to `to`. On failure, behavior depends on options. |
| `ctx.PipeToName(actorName, task, opts...)` | Same, but target is resolved by name (location-transparent).                                             |

### From PID

| Method                                          | Purpose                                                                     |
|-------------------------------------------------|-----------------------------------------------------------------------------|
| `pid.PipeTo(ctx, to, task, opts...)`            | Run `task`; deliver result to `to`. The sender (`pid`) is used for context. |
| `pid.PipeToName(ctx, actorName, task, opts...)` | Same, target by name.                                                       |

### From GrainContext

| Method                                       | Purpose                          |
|----------------------------------------------|----------------------------------|
| `gctx.PipeToGrain(to, task, opts...)`        | Deliver result to a grain.       |
| `gctx.PipeToActor(actorName, task, opts...)` | Deliver result to a named actor. |
| `gctx.PipeToSelf(task, opts...)`             | Deliver result to this grain.    |

## Task signature

```go
task func() (any, error)
```

- On success: the `any` result is sent to the target as a normal message.
- On failure: the error is forwarded to the dead-letter queue, unless `WithCircuitBreaker` is used and the breaker is open (in which case the outcome is dropped).

## PipeOption

| Option                   | Purpose                                                           |
|--------------------------|-------------------------------------------------------------------|
| `WithTimeout(d)`         | Abort delivery if the task does not complete within `d`.          |
| `WithCircuitBreaker(cb)` | If the circuit breaker is open, do not deliver; drop the outcome. |

Only one of `WithTimeout` or `WithCircuitBreaker` may be used per call. Using both returns `ErrOnlyOneOptionAllowed`.

## Example

```go
func (a *FetcherActor) Receive(ctx *ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *FetchURL:
        ctx.PipeToName("aggregator", func() (any, error) {
            return http.Get(msg.URL)
        }, actor.WithTimeout(5*time.Second))
    default:
        ctx.Unhandled()
    }
}
```

The aggregator actor receives the `*http.Response` (or the task fails and the error is handled per options).
