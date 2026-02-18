# Spawning Actors

Spawning is the process of creating and starting new actors in the actor system. GoAkt provides multiple methods for spawning actors with various configuration options to suit different use cases.

## Table of Contents

- 🤔 [What is Spawning?](#what-is-spawning)
- 🛠️ [Spawn Methods](#spawn-methods)
- ⚙️ [Spawn Options](#spawn-options)
- 📍 [Spawn Placement Strategies](#spawn-placement-strategies)
- ✅ [Best Practices](#best-practices)
- ⚠️ [Error Handling](#error-handling)
- 📋 [Summary](#summary)

---

## What is Spawning?

**Spawning** creates a new actor instance, initializes it, and registers it in the actor system. When you spawn an actor:

- A unique **PID** (Process ID) is assigned
- The actor's `PreStart` hook is called
- The actor begins processing messages
- The actor is registered under a unique name
- The actor is added to the supervision hierarchy

## Spawn Methods

| Method               | Use when                                                                                                              |
|----------------------|-----------------------------------------------------------------------------------------------------------------------|
| `Spawn`              | Local actor; you need the PID immediately. Single-node or testing.                                                    |
| `SpawnOn`            | Cluster: actor can be placed on any node. Use `ActorOf` to get PID after spawn. Use with `WithPlacement`, `WithRole`. |
| `SpawnFromFunc`      | Anonymous actor from a receive function; auto-generated name. Simple handlers, prototyping.                           |
| `SpawnNamedFromFunc` | Same as above with a fixed name. Use `WithPreStart` / `WithPostStop` for lifecycle.                                   |
| `SpawnRouter`        | Router that distributes messages across a pool of routees. See [Routers](routers.md).                                 |
| `SpawnSingleton`     | One instance in the cluster. Use `WithSingletonRole`. See [Cluster Singleton](../cluster/cluster_singleton.md).       |

From inside an actor, use `ctx.Spawn` to create a child (supervised by the current actor). Check error and PID; use `ctx.Err` on failure if appropriate. See [Supervisor](supervisor.md) for details.

**Local spawn:**

```go
pid, err := actorSystem.Spawn(ctx, "greeter", &GreeterActor{})
if err != nil {
    return err
}
// use pid for Tell, Ask, etc.
```

**Cluster spawn (SpawnOn):**

```go
err := actorSystem.SpawnOn(ctx, "cart", &CartActor{},
    actor.WithPlacement(actor.RoundRobin),
    actor.WithRole("web"))
if err != nil {
    return err
}
pid := actorSystem.ActorOf("cart")
```

**Child from inside Receive:**

```go
child, err := ctx.Spawn("worker", &WorkerActor{}, actor.WithSupervisor(spec))
if err != nil {
    ctx.Err(err)
    return
}
```

**Function-based actor:**

```go
pid, err := actorSystem.SpawnNamedFromFunc(ctx, "logger",
    func(ctx context.Context, msg proto.Message) error {
        log.Printf("got: %v", msg)
        return nil
    },
    actor.WithPreStart(func(context.Context) error { log.Println("started"); return nil }))
```

## Spawn Options

Options are passed as variadic args when calling a spawn method.

- `WithMailbox` — Unbounded (default), bounded, priority, fair, or segmented. See [Mailbox](mailbox.md).
- `WithSupervisor` — Restart/stop/resume and retry. See [Supervisor](supervisor.md).
- `WithPassivationStrategy` — When to stop idle actors: time-based, long-lived, or message-count. See [Passivation](passivation.md). `WithPassivateAfter` is deprecated; use the strategy instead. `WithLongLived` is equivalent to a long-lived passivation strategy.
- `WithDependencies` — Inject deps (e.g. DB, cache) for testing or sharing. See [Dependencies](dependencies.md).
- `WithStashing` — Enable a stash for messages the actor can't handle yet. See [Stashing](stashing.md).
- `WithReentrancy` — Allow concurrent request handling or stash non-reentrant. See [Reentrancy](reentrancy.md).
- `WithPlacement` — For `SpawnOn` only: `RoundRobin`, `Random`, `Local`, `LeastLoad`.
- `WithRole` — For `SpawnOn`: only nodes advertising this role.
- `WithRelocationDisabled` — Do not relocate actor on node failure (e.g. node-local state).
- `WithDataCenter` — For `SpawnOn` with a datacenter-aware cluster.

## Spawn Placement Strategies

When using **SpawnOn**, pass **WithPlacement**:

- **RoundRobin** (default) — Distributes actors evenly across nodes.
- **Random** — Picks a random node.
- **Local** — Forces the actor onto the current node.
- **LeastLoad** — Places on the node with lowest load (higher overhead).

Combine with **WithRole** to restrict placement to nodes that advertise that role.

**Spawn with options:**

```go
pid, err := actorSystem.Spawn(ctx, "user-service", &UserActor{},
    actor.WithMailbox(actor.NewBoundedMailbox(1000)),
    actor.WithSupervisor(supervisorSpec),
    actor.WithPassivationStrategy(passivation.NewTimeBasedStrategy(5*time.Minute)))
```

## Best Practices

### Do's ✅

Use unique names per system/cluster; choose mailbox and supervision for your workload; set passivation for long-running systems; use SpawnOn for cluster; always check spawn errors and handle `ErrActorSystemNotStarted`, `ErrActorAlreadyExists` (then use `ActorOf`), and `ErrInvalidActorName`.

### Don'ts ❌

Don't reuse actor names; don't ignore spawn errors; don't skip passivation in production; don't spawn in constructors—spawn in `PreStart` or from `Receive` with `ctx.Spawn`; consider mailbox size and backpressure.

## Error Handling

Check the error returned by Spawn/SpawnOn. Use `errors.Is(err, actor.ErrActorSystemNotStarted)` if the system isn't started; `ErrActorAlreadyExists` to detect duplicate name (then get PID with `ActorOf("name")`); `ErrInvalidActorName` for invalid names. Handle or log and exit as appropriate.

## Summary

- `Spawn` creates actors locally and returns PID
- `SpawnOn` distributes actors across cluster
- `SpawnFromFunc` creates function-based actors
- `SpawnRouter` creates message distribution routers
- `SpawnSingleton` ensures single instance across cluster
- Spawn options configure mailbox, supervision, passivation, etc.
- Placement strategies control cluster distribution
- Best practices ensure reliable actor creation
