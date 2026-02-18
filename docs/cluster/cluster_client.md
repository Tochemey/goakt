# Cluster Client

The GoAkt client package provides a lightweight library for external applications to interact with GoAkt actor system clusters without running a full actor system themselves. It enables remote actor communication, spawning, and management through a simple, load-balanced interface.

## Table of Contents

- 📖 [Overview](#overview)
- 📥 [Installation](#installation)
- ⚡ [Quick Start](#quick-start)
- 🏗️ [Creating a Client](#creating-a-client)
- 📍 [Node Configuration](#node-configuration)
- ⚙️ [Configuration Options](#configuration-options)
- 📤 [Client Operations](#client-operations)
- 🌾 [Grain Operations](#grain-operations)
- ⚖️ [Load Balancing](#load-balancing)
- ✅ [Best Practices](#best-practices)
- 🔧 [Troubleshooting](#troubleshooting)

---

## Overview

The cluster client is designed for scenarios where you need to communicate with actors in a GoAkt cluster from:

- **External services**: Microservices, web servers, or other applications not running GoAkt
- **Client applications**: Command-line tools, monitoring systems, or administrative interfaces
- **Gateways and proxies**: API gateways that route requests to actor systems
- **Testing tools**: Integration test frameworks that interact with actor clusters

### Key Features

- **No actor system required**: Lightweight client without the overhead of running an actor system
- **Load balancing**: Automatic distribution of requests across cluster nodes
- **Connection pooling**: Efficient connection management to cluster nodes
- **Multiple balancing strategies**: Round-robin, random, or least-load balancing
- **Actor operations**: Spawn, tell, ask, stop, and lookup actors remotely
- **Grain support**: Interact with grain actors across the cluster
- **Thread-safe**: Safe for concurrent use from multiple goroutines

## Installation

```bash
go get github.com/tochemey/goakt/v3/client
```

## Quick Start

Create a client with cluster node addresses (remoting `host:port`), then use `Tell` for fire-and-forget or `Ask` for request-response. Always call `Close()` when done.

```go
nodes := []*client.Node{
    client.NewNode("node1.example.com:3321"),
    client.NewNode("node2.example.com:3321"),
}
cl, err := client.New(ctx, nodes)
if err != nil {
    log.Fatal(err)
}
defer cl.Close()

err = cl.Tell(ctx, "user-session-123", &MyMessage{})
response, err := cl.Ask(ctx, "user-session-123", &QueryRequest{}, 5*time.Second)
```

## Creating a Client

Use `client.New(ctx, nodes, opts...)`. Default is round-robin. Options: `WithBalancerStrategy` and `WithRefresh` (periodic node weight updates; useful with `LeastLoadStrategy`).

```go
cl, err := client.New(ctx, nodes,
    client.WithBalancerStrategy(client.LeastLoadStrategy),
    client.WithRefresh(10*time.Second),
)
```

## Node Configuration

A `Node` is a cluster member identified by its remoting address. Create with `client.NewNode(address)` or `client.NewNode(address, client.WithWeight(weight), client.WithRemoting(remoting))`. The address must be `host:port` using the **remoting port** (not the discovery or peers port).

## Configuration Options

| Option                           | Effect                                                                                                                            |
|----------------------------------|-----------------------------------------------------------------------------------------------------------------------------------|
| `WithBalancerStrategy(strategy)` | `RoundRobinStrategy` (default), `RandomStrategy`, or `LeastLoadStrategy`.                                                         |
| `WithRefresh(interval)`          | Periodically refresh node weights from cluster metrics. Disabled by default. Use with `LeastLoadStrategy` for load-aware routing. |

## Client Operations

All operations use the client's balancer to pick a node. Messages must be protobuf. Use `errors.Is(err, gerrors.ErrActorNotFound)` to detect actor-not-found.

| Operation                                    | Description                                                                                       |
|----------------------------------------------|---------------------------------------------------------------------------------------------------|
| `Kinds(ctx)`                                 | Returns `([]string, error)` — actor kinds registered with `WithKinds()` on the cluster.           |
| `Spawn(ctx, spawnRequest)`                   | Creates and starts an actor on a node (default strategy). Kind must be registered on the cluster. |
| `SpawnBalanced(ctx, spawnRequest, strategy)` | Same as `Spawn` but uses the given `BalancerStrategy` for this call.                              |
| `Tell(ctx, actorName, message)`              | Fire-and-forget; returns error if actor not found.                                                |
| `Ask(ctx, actorName, message, timeout)`      | Request-response; returns `(proto.Message, error)`.                                               |
| `Stop(ctx, actorName)`                       | Gracefully stops the actor (runs `PostStop`). No error if actor doesn't exist.                    |
| `ReSpawn(ctx, actorName)`                    | Stops then spawns a new instance with the same name; state not preserved.                         |
| `Whereis(ctx, actorName)`                    | Returns `(*address.Address, error)` — host and port of the node hosting the actor.                |
| `Reinstate(ctx, actorName)`                  | Resumes a suspended (passivated) actor. No error if actor doesn't exist.                          |

**SpawnRequest** (`remote.SpawnRequest`): Required fields are `Name` and `Kind`. Optional: `Singleton` (*SingletonSpec), `Relocatable` (bool), `PassivationStrategy`, `Supervisor`, `Dependencies` ([]extension.Dependency), `EnableStashing`, `Role` (*string), `Reentrancy`. Actor kind must be registered on the cluster with `WithKinds()`.

```go
req := &remote.SpawnRequest{Name: "session-1", Kind: "SessionActor"}
err := cl.Spawn(ctx, req)
addr, err := cl.Whereis(ctx, "session-1")  // after spawn
```

## Grain Operations

Grains are virtual actors identified by `Name` and `Kind`. Grain kinds must be registered on the cluster with `WithGrains()` in the cluster configuration. Use `remote.GrainRequest{Name: "...", Kind: "..."}` to target a grain.

| Operation                                       | Description                                         |
|-------------------------------------------------|-----------------------------------------------------|
| `TellGrain(ctx, grainRequest, message)`         | Fire-and-forget to a grain.                         |
| `AskGrain(ctx, grainRequest, message, timeout)` | Request-response; returns `(proto.Message, error)`. |

```go
gr := &remote.GrainRequest{Name: "user-123", Kind: "UserGrain"}
err := cl.TellGrain(ctx, gr, &UpdateUserRequest{})
reply, err := cl.AskGrain(ctx, gr, &GetUserRequest{}, 5*time.Second)
```

## Load Balancing

| Strategy                       | Description                                                                                                 |
|--------------------------------|-------------------------------------------------------------------------------------------------------------|
| `RoundRobinStrategy` (default) | Cycles through nodes in order; even distribution.                                                           |
| `RandomStrategy`               | Picks a random node per request.                                                                            |
| `LeastLoadStrategy`            | Picks the node with lowest weight. Use `WithRefresh(interval)` so weights are updated from cluster metrics. |

## Best Practices

- **Reuse and close:** Create the client once per process; call `Close()` on shutdown.
- **Errors:** Use `errors.Is(err, gerrors.ErrActorNotFound)` for actor-not-found; handle `context.DeadlineExceeded` for timeouts.
- **Load balancing:** Use `WithRefresh` with `LeastLoadStrategy` so node weights stay current.
- **Concurrency:** The client is thread-safe; use it from multiple goroutines. Set appropriate `Ask` timeouts.

## Troubleshooting

- **Connection failures:** Use remoting `host:port` (not discovery port); ensure remoting is enabled on nodes and firewall allows it.
- **Actor not found:** Use `errors.Is(err, gerrors.ErrActorNotFound)`. Confirm the actor was spawned and the name is correct.
- **Uneven load:** Set `WithBalancerStrategy` and use `WithRefresh` for `LeastLoadStrategy`; ensure nodes are reachable.
- **Timeouts:** Increase `Ask` timeout; consider `Tell` for fire-and-forget.
