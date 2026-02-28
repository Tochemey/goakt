---
title: Routers
description: Round robin, random, and fan-out routing strategies.
sidebarTitle: "🔀 Routers"
---

## Concept

A **router** is an actor that fans out messages to a pool of routees using a routing strategy. You send messages to the router; it forwards them to one or more routees according to the configured strategy.

Routers are **not** cluster-relocatable. If the host node leaves or crashes, the router and its routees are not automatically re-spawned elsewhere.

## Sending Messages

Messages to a router must be wrapped in a `Broadcast` envelope. Use `Tell` to send:

```go
ctx.Tell(routerPID, actor.NewBroadcast(myMessage))
```

The router unwraps the payload and dispatches it to its routees based on the routing strategy.

## Routing Strategies

### Standard Strategies (Fire-and-Forget)

These strategies never wait for replies. They are fire-and-forget; routees process messages asynchronously.

| Strategy       | Behavior                                   |
|----------------|--------------------------------------------|
| **RoundRobin** | Rotate through routees in order            |
| **Random**     | Pick a random routee                       |
| **FanOut**     | Send to all routees concurrently (default) |

### Reply-Based Strategies

When you need the router to observe replies and relay the first successful response back to the sender, use one of these. Outcomes are delivered **asynchronously** to the sender as ordinary messages (not via `Ask`).

| Strategy                 | Behavior                                                                                                                                                                                                                                                                         |
|--------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Scatter-Gather First** | Sends the request to all routees concurrently. The first successful reply within the time budget is forwarded to the original sender. Late replies are ignored. If no routee replies in time, the sender receives a `StatusFailure`.                                             |
| **Tail-Chopping**        | Probes one routee at a time (in random order). If no response arrives before the interval, sends to the next routee. Repeats until a routee replies, all routees are tried, or the global deadline expires. First success wins; otherwise the sender receives a `StatusFailure`. |

## Spawning a Router

Use `SpawnRouter` on the actor system:

```go
routerPID, err := system.SpawnRouter(ctx, "my-router", 5, &MyWorker{}, actor.WithRoutingStrategy(actor.RoundRobinRouting))
```

Parameters:

- `name` – Router actor name
- `poolSize` – Number of routees to spawn (must be > 0)
- `routeesKind` – Actor type for routees (a pointer to a struct implementing `Actor`)
- `opts` – Router options (see below)

## Router Options

| Option                                            | Description                                                                                                                                                                         |
|---------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `WithRoutingStrategy(strategy)`                   | Set RoundRobin, Random, or FanOut. Default is FanOut.                                                                                                                               |
| `AsScatterGatherFirst(within)`                    | Use scatter-gather-first. `within` is the total time budget for any reply.                                                                                                          |
| `AsTailChopping(within, interval)`                | Use tail-chopping. `within` is the global deadline; `interval` is the delay between successive attempts. Requires `interval` > 0 and `within` > 0; typically `interval` < `within`. |
| `WithRestartRouteeOnFailure(maxRetries, timeout)` | On routee panic: restart the routee (up to `maxRetries` within `timeout`).                                                                                                          |
| `WithStopRouteeOnFailure()`                       | On routee panic: stop and remove the routee from the pool. **Default.**                                                                                                             |
| `WithResumeRouteeOnFailure()`                     | On routee panic: resume the routee (keep state, continue processing).                                                                                                               |

### Examples

```go
// Round-robin across 10 workers
system.SpawnRouter(ctx, "workers", 10, &Worker{}, actor.WithRoutingStrategy(actor.RoundRobinRouting))

// Scatter-gather: first reply within 500ms wins
system.SpawnRouter(ctx, "lookup", 5, &LookupActor{}, actor.AsScatterGatherFirst(500*time.Millisecond))

// Tail-chopping: try every 200ms for up to 2s
system.SpawnRouter(ctx, "probe", 5, &ProbeActor{}, actor.AsTailChopping(2*time.Second, 200*time.Millisecond))

// Restart failing routees up to 3 times within 1s
system.SpawnRouter(ctx, "resilient", 5, &Worker{}, actor.WithRestartRouteeOnFailure(3, time.Second))
```

## Control Messages

Routers accept these control messages via `Tell` or `Ask`:

| Message                | Usage                      | Response               |
|------------------------|----------------------------|------------------------|
| `GetRoutees`           | Query current routee names | `Routees` (use `Ask`)  |
| `AdjustRouterPoolSize` | Scale pool at runtime      | None (fire-and-forget) |

### GetRoutees

```go
resp, err := ctx.Ask(routerPID, &actor.GetRoutees{}, 500*time.Millisecond)
if err == nil {
    r := resp.(*actor.Routees)
    for _, name := range r.Names() {
        // resolve and use
    }
}
```

### AdjustRouterPoolSize

- `poolSize > 0`: Add that many routees
- `poolSize < 0`: Remove that many routees
- `poolSize == 0`: No-op

```go
ctx.Tell(routerPID, actor.NewAdjustRouterPoolSize(5))   // grow by 5
ctx.Tell(routerPID, actor.NewAdjustRouterPoolSize(-3)) // shrink by 3
```

## Handling Replies (Scatter-Gather / Tail-Chopping)

For reply-based strategies, the sender receives either:

- The first successful reply from a routee (as a normal message)
- A `StatusFailure` if the deadline expires or all routees fail

Handle both in your actor’s `Receive`:

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *MySuccessResponse:
        // first successful reply
    case *actor.StatusFailure:
        // timeout or all failures
    }
}
```

## When to Use

- **Load balancing** – RoundRobin or Random across a pool of workers
- **Parallel processing** – FanOut for pub/sub, cache invalidation, multi-sink processing
- **Fastest responder** – Scatter-Gather First when you want the first successful reply
- **Controlled probing** – Tail-Chopping when you prefer sequential probing with bounded fan-out

## Routee Supervision

When a routee panics, the router receives a `PanicSignal` and applies the configured directive:

- **Stop** (default): Terminate the routee and remove it from the pool
- **Restart**: Restart the routee (with retry limits)
- **Resume**: Keep the routee and continue processing

If all routees are stopped and none remain, the router shuts down and unhandled messages go to the deadletter.
