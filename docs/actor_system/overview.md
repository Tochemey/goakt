# Actor System Overview

The Actor System is the foundation of GoAkt - it manages actor lifecycle, provides infrastructure services, and coordinates distributed operations.

## Table of Contents

- 🤔 [What is an Actor System?](#what-is-an-actor-system)
- 🏗️ [Creating an Actor System](#creating-an-actor-system)
- 🔄 [Actor System Lifecycle](#actor-system-lifecycle)
- ⚙️ [Configuration Options](#configuration-options)
- 📋 [Actor System Methods](#actor-system-methods)
- ✅ [Best Practices](#best-practices)
- ⚡ [Performance Considerations](#performance-considerations)
- 📋 [Summary](#summary)

---

## What is an Actor System?

An **Actor System** is the container and runtime for actors. It provides:

- **Lifecycle** — Creation, supervision, termination, relocation (when clustering is enabled)
- **Infrastructure** — Remoting, clustering, pub/sub
- **Resources** — Mailboxes, schedulers, event streams
- **Configuration** — Logging, metrics, [extensions](extensions.md)
- **Coordination** — Distributed operations, [coordinated shutdown](coordinated_shutdown.md)
- **Location transparency** — With remoting/clustering, you address actors by identity; local vs remote is abstracted
- **Fault tolerance** — Supervision and restart strategies
- **Addressing** — Unique addresses for routing and discovery
- **Event stream** — System events for health and status
- **Scheduling** — Timers and delayed messages
- **Multi-datacenter** — Control plane for cross-DC actor management (if enabled)
- **Actor tree** — Hierarchical organization under guardians for supervision and lifecycle

Think of it as the operating system for your actors.

## Creating an Actor System

### Basic Creation

```go
actorSystem, err := actor.NewActorSystem("MySystem")
```

**Requirements:**

- Name must be unique per process
- Name must match pattern: `^[a-zA-Z0-9][a-zA-Z0-9-_]*$`
- Name cannot be empty

### With Options

```go
actorSystem, err := actor.NewActorSystem("MySystem",
    actor.WithLogger(logger),
    actor.WithShutdownTimeout(30*time.Second))
// To disable passivation per-actor, use actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()) when spawning
```

## Actor System Lifecycle

### Start

Initialize and start the actor system:

```go
ctx := context.Background()
err := actorSystem.Start(ctx)
```

**What happens on Start:**

1. Initialize internal infrastructure
2. Start event stream
3. Start scheduler
4. Enable remoting (if configured)
5. Join cluster (if configured)
6. **Start data center control plane** (if multi-DC is configured): leader watch runs on all nodes; the **data center controller** is started only on the **cluster leader** and registers this data center with the control plane. Required for `SpawnOn` with `WithDataCenter` and for cross–data center grain messaging.
7. Start system actors (guardians)
8. Initialize pub/sub (if enabled)
9. Start metric collection (if enabled)

### Stop

Gracefully shutdown the actor system:

```go
err := actorSystem.Stop(ctx)
```

**What happens on Stop:**

1. Execute shutdown hooks (coordinated shutdown)
2. **Stop data center control plane** (if multi-DC configured): stop leader watch and data center controller
3. Stop all actors gracefully
4. Leave cluster (if in cluster mode)
5. Stop remoting
6. Stop scheduler
7. Stop event stream
8. Clean up resources

> `Stop` does not call `os.Exit()` - you must handle that explicitly.

### Example

```go
package main

import (
    "context"
    "os"
    "os/signal"
    "syscall"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/log"
)

func main() {
    ctx := context.Background()
    logger := log.DefaultLogger

    // Create actor system
    actorSystem, err := actor.NewActorSystem("MyApp",
        actor.WithLogger(logger),
        actor.WithShutdownTimeout(30*time.Second))

    if err != nil {
        logger.Fatal("Failed to create system", "error", err)
    }

    // Start system
    if err := actorSystem.Start(ctx); err != nil {
        logger.Fatal("Failed to start", "error", err)
    }

    logger.Info("Actor system started successfully")

    // Spawn actors and do work...
    pid, _ := actorSystem.Spawn(ctx, "worker", &WorkerActor{})

    // Handle graceful shutdown
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
    <-sigCh

    logger.Info("Shutting down...")

    // Stop system
    if err := actorSystem.Stop(ctx); err != nil {
        logger.Error("Failed to stop cleanly", "error", err)
        os.Exit(1)
    }

    logger.Info("Shutdown complete")
    os.Exit(0)
}
```

## Configuration Options

### WithLogger

Set custom logger for the actor system:

```go
logger := log.New(log.DebugLevel, os.Stdout)
actorSystem, _ := actor.NewActorSystem("MySystem", actor.WithLogger(logger))
```

**Default:** Uses GoAkt's default logger

### Passivation

Passivation is configured **per actor** at spawn time, not globally. To keep an actor from being passivated, use the long-lived strategy when spawning:

```go
import "github.com/tochemey/goakt/v3/passivation"

pid, _ := actorSystem.Spawn(ctx, "my-actor", &MyActor{},
    actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()))
```

**Use when:**

- Actor should never be passivated
- Testing/development
- Memory is not constrained

See [Passivation](../actors/passivation.md) for time-based and message-count strategies.

### WithShutdownTimeout

Set timeout for graceful shutdown:

```go
actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithShutdownTimeout(30*time.Second))
```

**Default:** 10 seconds

**Recommendations:**

- Small systems: 10-30 seconds
- Large systems: 30-60 seconds
- Very large systems: 60-120 seconds

### WithActorInitTimeout

Timeout for actor initialization (PreStart):

```go
actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithActorInitTimeout(10*time.Second))
```

**Default:** 5 seconds

### WithActorInitMaxRetries

Maximum retries for actor initialization:

```go
actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithActorInitMaxRetries(3))
```

**Default:** 5 retries

### WithDefaultSupervisor

Set default supervision strategy for all actors:

```go
supervisor := supervisor.NewSupervisor(
    supervisor.WithStrategy(supervisor.OneForOneStrategy),
    supervisor.WithDirective(&DatabaseError{}, supervisor.RestartDirective),
    supervisor.WithRetry(3, 30*time.Second))

actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithDefaultSupervisor(supervisor))
```

**Applies to:** Actors spawned without explicit supervisor

### WithRemote

Enable remoting for actor-to-actor communication across nodes:

```go
remoteConfig := &remote.Config{
    Host: "localhost",
    Port: 3000,
}

actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithRemote(remoteConfig))
```

See [Remoting documentation](../remoting/overview.md) for details.

### WithCluster

Enable clustering for distributed actor systems:

```go
clusterConfig := &actor.ClusterConfig{
    Discovery: consulDiscovery,
    ReplicaCount: 3,
}

actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithCluster(clusterConfig))
```

See [Cluster documentation](../cluster/overview.md) for details.

### WithPubSub

Enable publish-subscribe messaging:

```go
actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithPubSub())
```

**Enables:**

- Topic-based messaging
- Subscribe/Unsubscribe operations
- Multi-subscriber support

See [PubSub documentation](../pubsub/overview.md) for details.

### WithMetrics

Enable OpenTelemetry metrics collection:

```go
actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithMetrics())
```

**Requires:** OpenTelemetry SDK initialization

**Metrics collected:**

- Actor count
- Message throughput
- Mailbox sizes
- Dead letters
- Passivation events

### WithExtensions

Register custom extensions:

```go
actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithExtensions(
        NewEventSourcingExtension(),
        NewTracingExtension()))
```

**Extensions** provide pluggable functionality accessible from actors.

See [Extensions documentation](extensions.md) for details.

### WithTLS

Enable TLS for secure communication:

```go
tlsConfig := &tls.Info{
    CertFile:      "server.crt",
    KeyFile:       "server.key",
    CAFile:        "ca.crt",
    ClientAuth:    true,
}

actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithTLS(tlsConfig),
    actor.WithRemote(remoteConfig))
```

**Note:** All cluster nodes must use same CA certificate.

### WithCoordinatedShutdown

Register shutdown hooks for coordinated cleanup:

```go
actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithCoordinatedShutdown(
        &FlushMetricsHook{},
        &CloseConnectionsHook{}))
```

See [Coordinated Shutdown documentation](coordinated_shutdown.md) for details.

### WithoutRelocation

Disable actor relocation in cluster:

```go
actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithCluster(clusterConfig),
    actor.WithoutRelocation())
```

**Effect:** Actors on failing nodes are stopped, not relocated.

### WithPartitionHasher

Set custom hash function for partitioning:

```go
actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithPartitionHasher(customHasher))
```

**Used for:** Cluster partitioning and consistent hashing

### WithEvictionStrategy

Configure actor eviction when memory limits are reached:

```go
strategy, err := actor.NewEvictionStrategy(1000, actor.LRU, 10) // limit, policy, percentage
if err != nil {
    log.Fatal(err)
}
actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithEvictionStrategy(strategy, 5*time.Second))
```

**Policies:** `actor.LRU`, `actor.LFU`, `actor.MRU`. **Percentage** (0–100) controls how many actors to evict per run.

## Actor System Methods

### Spawn

Create actors locally:

```go
pid, err := actorSystem.Spawn(ctx, "my-actor", &MyActor{},
    actor.WithMailbox(actor.NewBoundedMailbox(1000)))
```

See [Spawning Actors documentation](../actors/spawn.md) for details.

### SpawnOn

Create actors with cluster placement:

```go
err := actorSystem.SpawnOn(ctx, "distributed-actor", &MyActor{},
    actor.WithPlacement(actor.RoundRobin))
```

### SpawnRouter

Create router for load balancing (pool size, routee kind, then router options):

```go
routerPID, err := actorSystem.SpawnRouter(ctx, "worker-pool",
    10,                    // pool size
    &WorkerActor{},        // routee kind
    actor.WithRoutingStrategy(actor.RoundRobinRouting))
```

See [Routers documentation](../actors/routers.md) for details.

### SpawnSingleton

Create cluster-wide singleton:

```go
err := actorSystem.SpawnSingleton(ctx, "coordinator", &CoordinatorActor{})
```

See [Cluster Singleton documentation](../cluster/cluster_singleton.md) for details.

### ActorOf

Lookup actor by name (local or cluster). Returns address, PID, and error:

```go
addr, pid, err := actorSystem.ActorOf(ctx, "my-actor")
// If actor is on a remote node, addr is set and pid may be nil; use remoting to send to addr
```

**Returns:** `(addr *address.Address, pid *PID, err error)` — PID if found locally, address if on another node

### Actors

Get all local actors:

```go
actors := actorSystem.Actors()
```

**Returns:** Slice of local actor PIDs

### ActorRefs

Get all actors (including cluster):

```go
refs, err := actorSystem.ActorRefs(ctx, 5*time.Second)
```

**Returns:** ActorRefs from local and cluster nodes

### Pub/Sub (Topic messaging)

Topic-based publish-subscribe is **not** done via system methods. With `WithPubSub()` enabled, you send `Subscribe`, `Publish`, and `Unsubscribe` messages to the **Topic Actor**:

```go
topicActor := actorSystem.TopicActor()
// Subscribe: actor.Tell(ctx, topicActor, &goaktpb.Subscribe{Topic: "orders", Subscriber: subscriberPID})
// Publish:   actor.Tell(ctx, topicActor, &goaktpb.Publish{Topic: "orders", Message: ...})
// Unsubscribe: actor.Tell(ctx, topicActor, &goaktpb.Unsubscribe{Topic: "orders", Subscriber: subscriberPID})
```

See [PubSub](../pubsub/overview.md) for full usage.  
**Note:** `actorSystem.Subscribe()` / `Unsubscribe()` are for the **events stream** (internal system events), not for topic pub/sub.

### Metric

Get runtime metrics (methods, not fields):

```go
metrics := actorSystem.Metric(ctx)
```

### Name

Get actor system name:

```go
name := actorSystem.Name()
fmt.Printf("System: %s\n", name)
```

### Running

Check if system is running:

```go
isRunning := actorSystem.Running()
```

### InCluster

Check if in cluster mode:

```go
inCluster := actorSystem.InCluster()
```

## Best Practices

### Do's ✅

1. **Single Instance** per application per node
2. **Handle signals** for graceful shutdown
3. **Set appropriate timeouts** based on system size
4. **Use coordinated shutdown** for cleanup
5. **Configure default supervisor** for consistency
6. **Enable metrics** for monitoring
7. **Use TLS in production** for security
8. **Test shutdown sequence** thoroughly

```go
// Good: Comprehensive configuration
actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithLogger(logger),
    actor.WithShutdownTimeout(30*time.Second),
    actor.WithDefaultSupervisor(supervisor),
    actor.WithCoordinatedShutdown(shutdownHooks...))
```

### Don'ts ❌

1. **Don't skip shutdown handling**
2. **Don't use same name** for multiple systems
3. **Don't start without error checking**
4. **Don't forget to call Stop()**
5. **Don't use very short timeouts**

```go
// Bad: No error handling, no shutdown
system, _ := actor.NewActorSystem("Bad")
system.Start(ctx)

// Bad: Invalid name
system, _ := actor.NewActorSystem("My System!") // ❌ Spaces not allowed

// Good: Proper handling
system, err := actor.NewActorSystem("MySystem")
if err != nil {
    log.Fatal(err)
}

if err := system.Start(ctx); err != nil {
    log.Fatal(err)
}
defer system.Stop(ctx)
```

## Performance Considerations

- **Passivation**: Balance memory vs. startup cost
- **Shutdown timeout**: Longer = more graceful, shorter = faster
- **Metrics**: Small overhead when enabled
- **Clustering**: Adds coordination overhead
- **TLS**: Adds encryption overhead

## Summary

- **Actor System** is the runtime environment for actors
- **Create** with `NewActorSystem` and configuration options
- **Start/Stop** manage lifecycle
- **Configure** logging, clustering, remoting, metrics, etc.
- **Methods** provide actor management and system operations
- **Best practices** ensure reliable operation
