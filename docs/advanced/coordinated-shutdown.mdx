---
title: Coordinated Shutdown
description: Configurable shutdown hooks and graceful termination.
sidebarTitle: "🛑 Coordinated Shutdown"
---

The actor system supports coordinated shutdown with configurable hooks and timeout.

## Shutdown sequence

When `Stop(ctx)` is called, the system:

1. Stops background services (eviction, passivation, scheduler)
2. Runs coordinated shutdown hooks (if registered)
3. Stops datacenter services (if enabled)
4. Snapshots cluster state (if clustered)
5. Shuts down user actors depth-first (children before parents)
6. Deactivates all grains
7. Shuts down system actors
8. Leaves cluster and stops remoting

Shutdown is best-effort: it continues even if individual steps fail, collecting errors. A configurable `shutdownTimeout` (default: 3 minutes) bounds the total time.

## Shutdown hooks

Register hooks via `WithCoordinatedShutdown(hooks...)` when creating the actor system. Hooks run during shutdown, before actors are stopped.

### The ShutdownHook interface

```go
type ShutdownHook interface {
    Execute(ctx context.Context, actorSystem ActorSystem) error
    Recovery() *ShutdownHookRecovery
}
```

| Method       | Purpose                                                       |
|--------------|---------------------------------------------------------------|
| **Execute**  | Run cleanup logic. Use the actor system for final operations. |
| **Recovery** | Return retry and recovery configuration for failures.         |

### Recovery strategies

| Strategy             | Behavior                                       |
|----------------------|------------------------------------------------|
| `ShouldFail`         | Stop shutdown on hook failure                  |
| `ShouldRetryAndFail` | Retry, then stop if still failing              |
| `ShouldSkip`         | Skip failed hook, continue                     |
| `ShouldRetryAndSkip` | Retry, then skip and continue if still failing |

### Configuring recovery

```go
recovery := actor.NewShutdownHookRecovery(
    actor.WithShutdownHookRetry(2, time.Second),
    actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndSkip),
)
```

Pass this from your hook's `Recovery()` method.

## Shutdown timeout

Use `WithShutdownTimeout(duration)` when creating the actor system to bound the total shutdown time. The default is 3 minutes.
