# Cluster Singleton

A cluster singleton is an actor that has exactly one instance across the entire cluster. This is useful for coordinating tasks, managing global state, or ensuring sequential processing of certain operations.

## Table of Contents

- 📖 [Overview](#overview)
- 💡 [Use Cases](#use-cases)
- 🏗️ [Creating a Singleton](#creating-a-singleton)
- 💡 [Basic Example](#basic-example)
- ⚙️ [Configuration Options](#configuration-options)
- 📍 [Singleton Placement](#singleton-placement)
- 🔄 [Relocation](#relocation)
- 📤 [Communicating with Singletons](#communicating-with-singletons)
- 🔁 [Idempotent Spawning](#idempotent-spawning)
- 💾 [State Management](#state-management)
- ⚠️ [Error Handling](#error-handling)
- 🔧 [Advanced Patterns](#advanced-patterns)
- ✅ [Best Practices](#best-practices)
- 🔧 [Troubleshooting](#troubleshooting)

---

## Overview

In a clustered environment, you may need to ensure that only one instance of a particular actor exists across all nodes. GoAkt provides cluster singletons to address this need:

- **Single instance guarantee**: Only one instance exists cluster-wide
- **Automatic placement**: The singleton is placed on the cluster leader (coordinator node)
- **Automatic relocation**: When the leader changes or fails, the singleton is automatically recreated on the new leader
- **Role-based placement**: Optionally pin singletons to nodes with specific roles
- **Lifecycle management**: Full actor lifecycle support (PreStart, Receive, PostStop)

## Use Cases

### Coordination and Orchestration

- **Cluster-wide scheduler**: A single scheduler managing periodic jobs
- **Work dispatcher**: Central coordinator distributing work to other actors
- **State machine coordinator**: Managing cluster-wide state transitions

### Resource Management

- **Connection pool manager**: Managing a single shared connection pool
- **Rate limiter**: Cluster-wide rate limiting for external APIs
- **License manager**: Tracking and enforcing license limits

### Sequential Processing

- **Event sequencer**: Ensuring events are processed in order
- **Saga coordinator**: Managing distributed transactions
- **Migration controller**: Coordinating data migrations

### Global State

- **Configuration manager**: Holding cluster-wide configuration
- **Feature flag controller**: Managing feature toggles
- **Circuit breaker**: Cluster-wide circuit breaker state

## Creating a Singleton

Use the `SpawnSingleton()` method to create a singleton actor:

```go
err := system.SpawnSingleton(ctx, "my-singleton", new(MySingletonActor))
if err != nil {
    log.Fatal(err)
}
```

### Requirements

1. **Clustering must be enabled**: The actor system must be configured with clustering
2. **Actor kind must be registered**: Use `WithKinds()` in cluster configuration
3. **Unique name**: The singleton name must be unique across the cluster

## Basic Example

```go
package main

import (
    "context"
    "log"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/discovery/nats"
    "google.golang.org/protobuf/proto"
)

// CoordinatorActor is a singleton that coordinates cluster-wide operations
type CoordinatorActor struct{}

func (c *CoordinatorActor) PreStart(ctx *actor.Context) error {
    ctx.ActorSystem().Logger().Info("Coordinator singleton started")
    return nil
}

func (c *CoordinatorActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *CoordinateRequest:
        // Handle coordination request
        ctx.Logger().Infof("Coordinating: %v", msg)
        ctx.Response(&CoordinateResponse{Success: true})
    }
}

func (c *CoordinatorActor) PostStop(ctx *actor.Context) error {
    ctx.ActorSystem().Logger().Info("Coordinator singleton stopped")
    return nil
}

func main() {
    ctx := context.Background()

    // Setup discovery
    disco := nats.NewDiscovery(&nats.Config{
        NatsServer:    "nats://localhost:4222",
        NatsSubject:   "my-cluster",
        Host:          "localhost",
        DiscoveryPort: 3320,
    })

    // Configure cluster
    clusterConfig := actor.NewClusterConfig().
        WithDiscovery(disco).
        WithDiscoveryPort(3320).
        WithPeersPort(3322).
        WithMinimumPeersQuorum(2).
        WithKinds(new(CoordinatorActor)) // Register the singleton actor kind

    // Create actor system
    system, err := actor.NewActorSystem(
        "my-cluster",
        actor.WithCluster(clusterConfig),
        actor.WithRemote("localhost", 3321),
    )
    if err != nil {
        log.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    // Spawn the singleton
    err = system.SpawnSingleton(ctx, "coordinator", new(CoordinatorActor))
    if err != nil {
        log.Fatal(err)
    }

    log.Println("Cluster singleton created successfully")
    select {} // Keep running
}
```

## Configuration Options

GoAkt provides several options to configure singleton behavior:

### WithSingletonRole

Pin the singleton to nodes advertising a specific role:

```go
err := system.SpawnSingleton(
    ctx,
    "scheduler",
    new(SchedulerActor),
    actor.WithSingletonRole("control-plane"),
)
```

**When to use:**

- Dedicate specific nodes for singleton actors
- Separate coordination from compute workloads
- Ensure singletons run on nodes with specific resources

**Behavior:**

- The singleton is placed on the oldest node with the specified role
- If no nodes have the role, spawn fails
- The empty string is a no-op (places on overall oldest node)

### WithSingletonSpawnTimeout

Set the maximum time to wait for singleton spawn:

```go
err := system.SpawnSingleton(
    ctx,
    "scheduler",
    new(SchedulerActor),
    actor.WithSingletonSpawnTimeout(30 * time.Second),
)
```

**Default:** 30 seconds

**When to use:**

- Increase for slow networks or overloaded clusters
- Decrease for faster failure detection in testing

### WithSingletonSpawnWaitInterval

Set the delay between spawn retry attempts:

```go
err := system.SpawnSingleton(
    ctx,
    "scheduler",
    new(SchedulerActor),
    actor.WithSingletonSpawnWaitInterval(500 * time.Millisecond),
)
```

**Default:** 500 milliseconds

**When to use:**

- Adjust based on expected cluster stabilization time
- Balance responsiveness with retry overhead

### WithSingletonSpawnRetries

Set the maximum number of spawn attempts:

```go
err := system.SpawnSingleton(
    ctx,
    "scheduler",
    new(SchedulerActor),
    actor.WithSingletonSpawnRetries(5),
)
```

**Default:** 5 attempts

**When to use:**

- Increase for unreliable networks
- Decrease for faster failure in stable environments

**Approximate retry window:** `retries * interval` (subject to scheduling jitter)

## Combined Configuration Example

```go
err := system.SpawnSingleton(
    ctx,
    "critical-coordinator",
    new(CoordinatorActor),
    actor.WithSingletonRole("coordinator"),
    actor.WithSingletonSpawnTimeout(60 * time.Second),
    actor.WithSingletonSpawnWaitInterval(time.Second),
    actor.WithSingletonSpawnRetries(10),
)
```

## Singleton Placement

### Default Placement (Leader Node)

Without specifying a role, the singleton is placed on the cluster leader:

```go
err := system.SpawnSingleton(ctx, "coordinator", new(CoordinatorActor))
```

1. `SpawnSingleton` identifies the cluster leader
2. If the leader is the local node, spawns locally
3. If the leader is a remote node, delegates via `RemoteSpawn`
4. The actor is registered in the cluster routing table

### Role-Based Placement

With a role specified, the singleton is placed on the oldest node with that role:

```go
err := system.SpawnSingleton(
    ctx,
    "scheduler",
    new(SchedulerActor),
    actor.WithSingletonRole("scheduler"),
)
```

1. `SpawnSingleton` finds all nodes advertising the "scheduler" role
2. Selects the oldest node among them
3. Spawns the singleton on that node
4. Returns an error if no nodes have the role

## Relocation

When the node hosting a singleton fails or leaves the cluster:

1. **Detection**: The cluster detects the node departure via heartbeat timeout
2. **Leader coordination**: The cluster leader initiates relocation
3. **New placement**: The singleton is respawned on the new leader (or oldest role-bearing node)
4. **Registration**: The new instance is registered in the cluster routing table
5. **Routing update**: All nodes update their routing tables to point to the new instance

### Relocation Example Scenario

```
Initial state:
- Node1 (leader): hosts "coordinator" singleton
- Node2: hosts regular actors
- Node3: hosts regular actors

Node1 crashes:

1. Cluster detects Node1 departure
2. Node2 becomes the new leader (oldest remaining node)
3. Node2 recreates "coordinator" singleton locally
4. Cluster routing table is updated
5. All nodes can now reach the singleton on Node2
```

### Preventing Relocation

Singletons are always relocatable by default. If you need a singleton that doesn't relocate, consider:

1. **Using role-based placement** with a dedicated node that never restarts
2. **Handling state persistence** in `PostStop` and restoration in `PreStart`
3. **Using grains** instead (virtual actors with identity-based placement)

## Communicating with Singletons

### Lookup the Singleton

```go
addr, pid, err := system.ActorOf(ctx, "coordinator")
if err != nil {
    log.Fatal(err)
}

if pid != nil {
    // Singleton is local
    pid.Tell(message)
} else {
    // Singleton is remote: from inside an actor use ctx.RemoteTell(addr, message);
    // from outside use the cluster client: cl.Tell(ctx, "coordinator", message)
}
```

### Direct Communication

Since singletons have cluster-wide unique names, you can message them as follows:

**From outside (e.g. main):** use the [cluster client](../cluster_client.md): `cl.Ask(ctx, "coordinator", message, timeout)` or `cl.Tell(ctx, "coordinator", message)`.

**From within an actor:** use the context's remoting helpers (response is `*anypb.Any`; check for nil on failure):

```go
// Tell (fire-and-forget)
ctx.RemoteTell(addr, message)

// Ask (request-response)
response := ctx.RemoteAsk(addr, message, timeout)
if response == nil {
    // check ctx.Err() for error
}
```

### From Within an Actor

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    // Look up the singleton
    addr, pid, err := ctx.ActorSystem().ActorOf(ctx.Context(), "coordinator")
    if err != nil {
        ctx.Err(err)
        return
    }

    // Send message: local PID or remote address
    if pid != nil {
        pid.Tell(message)
    } else {
        ctx.RemoteTell(addr, message)
    }
}
```

To perform request-response from inside an actor, use `response := ctx.RemoteAsk(addr, message, timeout)` and check `response == nil` / `ctx.Err()`.

## Idempotent Spawning

`SpawnSingleton` is idempotent across the cluster:

```go
// On Node1
err := system.SpawnSingleton(ctx, "coordinator", new(CoordinatorActor))
// err == nil: singleton created

// On Node2 (shortly after)
err := system.SpawnSingleton(ctx, "coordinator", new(CoordinatorActor))
// err == nil: singleton already exists, no duplicate created
```

**How it works:**

1. The singleton kind is registered in the cluster using a unique key
2. Before spawning, the system checks if the kind is already registered
3. If registered, the spawn is skipped (considered successful)
4. This prevents duplicate singletons during concurrent spawn attempts

## State Management

### Stateless Singletons

For stateless singletons, no special handling is needed:

```go
type StatelessCoordinator struct{}

func (s *StatelessCoordinator) Receive(ctx *actor.ReceiveContext) {
    // Process messages without maintaining state
}
```

### Stateful Singletons

For stateful singletons, persist state in `PostStop` and restore in `PreStart`:

```go
type StatefulCoordinator struct {
    state *CoordinatorState
}

func (s *StatefulCoordinator) PreStart(ctx *actor.Context) error {
    // Restore state from persistent storage
    state, err := loadStateFromDB(ctx)
    if err != nil {
        return err
    }
    s.state = state
    ctx.ActorSystem().Logger().Info("State restored")
    return nil
}

func (s *StatefulCoordinator) Receive(ctx *actor.ReceiveContext) {
    // Update state based on messages
    switch msg := ctx.Message().(type) {
    case *UpdateState:
        s.state.Update(msg)
        ctx.Response(&UpdateStateResponse{Success: true})
    }
}

func (s *StatefulCoordinator) PostStop(ctx *actor.Context) error {
    // Persist state to durable storage
    if err := saveStateToDB(ctx, s.state); err != nil {
        ctx.ActorSystem().Logger().Errorf("Failed to persist state: %v", err)
        return err
    }
    ctx.ActorSystem().Logger().Info("State persisted")
    return nil
}
```

### Using Dependencies for State

Inject storage dependencies for cleaner state management:

```go
type DBClient interface {
    LoadState() (*State, error)
    SaveState(*State) error
}

type CoordinatorActor struct {
    db    DBClient
    state *State
}

func (c *CoordinatorActor) PreStart(ctx *actor.Context) error {
    c.db = ctx.Dependencies()[0].(DBClient)
    state, err := c.db.LoadState()
    if err != nil {
        return err
    }
    c.state = state
    return nil
}

func (c *CoordinatorActor) PostStop(ctx *actor.Context) error {
    return c.db.SaveState(c.state)
}

// Spawn with dependencies
err := system.SpawnSingleton(
    ctx,
    "coordinator",
    new(CoordinatorActor),
    actor.WithDependencies(dbClient),
)
```

## Error Handling

### Spawn Errors

```go
err := system.SpawnSingleton(ctx, "coordinator", new(CoordinatorActor))
if err != nil {
    switch {
    case errors.Is(err, gerrors.ErrClusterDisabled):
        // Clustering not enabled
    case errors.Is(err, gerrors.ErrActorSystemNotStarted):
        // System not started
    case errors.Is(err, gerrors.ErrSingletonAlreadyExists):
        // Singleton with this name already exists (different type)
    case errors.Is(err, gerrors.ErrLeaderNotFound):
        // Cluster leader not available
    case errors.Is(err, gerrors.ErrTypeNotRegistered):
        // Actor kind not registered with WithKinds()
    default:
        // Other errors (network, timeout, etc.)
    }
}
```

### Retry Logic

`SpawnSingleton` automatically retries on transient errors:

- **Quorum errors**: Cluster state replication failures
- **Leader not found**: Waiting for leadership election
- **Network timeouts**: Temporary connectivity issues
- **Connection refused**: Target node restarting

Retries stop on terminal errors:

- **Actor already exists**: Duplicate spawn attempt
- **Type not registered**: Configuration error
- **Context cancellation**: Caller cancelled
- **Spawn timeout exceeded**: Max duration reached

## Advanced Patterns

### Coordinating Multiple Singletons

Use role-based placement to distribute singleton responsibilities:

```go
// Scheduler singleton on scheduler nodes
system.SpawnSingleton(
    ctx,
    "scheduler",
    new(SchedulerActor),
    actor.WithSingletonRole("scheduler"),
)

// Work dispatcher singleton on dispatcher nodes
system.SpawnSingleton(
    ctx,
    "dispatcher",
    new(DispatcherActor),
    actor.WithSingletonRole("dispatcher"),
)

// Rate limiter singleton on gateway nodes
system.SpawnSingleton(
    ctx,
    "rate-limiter",
    new(RateLimiterActor),
    actor.WithSingletonRole("gateway"),
)
```

### Singleton with Supervision

Singletons support custom supervision strategies:

```go
import "github.com/tochemey/goakt/v3/supervisor"

system.SpawnSingleton(
    ctx,
    "coordinator",
    new(CoordinatorActor),
    actor.WithSupervisor(
        supervisor.NewSupervisor(
            supervisor.WithStrategy(supervisor.OneForOneStrategy),
            supervisor.WithAnyErrorDirective(supervisor.RestartDirective),
            supervisor.WithRetry(3, time.Second),
        ),
    ),
)
```

### Singleton as a Service Locator

```go
type ServiceRegistry struct {
    services map[string]*address.Address
}

func (s *ServiceRegistry) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *RegisterService:
        s.services[msg.Name] = msg.Address
        ctx.Response(&RegisterServiceResponse{Success: true})
    case *LookupService:
        addr, exists := s.services[msg.Name]
        ctx.Response(&LookupServiceResponse{Address: addr, Found: exists})
    }
}

// Other actors look up services via the singleton
addr, pid, _ := system.ActorOf(ctx, "service-registry")
if pid != nil {
    response, _ := actor.Ask(ctx, pid, &LookupService{Name: "payment-service"}, timeout)
    _ = response
}
```

### Scheduled Work Coordinator

```go
type SchedulerActor struct{}

func (s *SchedulerActor) PreStart(ctx *actor.Context) error {
    return nil
}

func (s *SchedulerActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.PostStart:
        // Schedule periodic work from Receive (scheduler is on the system)
        sys := ctx.ActorSystem()
        _ = sys.ScheduleWithCron(ctx.Context(), &HourlyJob{}, ctx.Self(), "0 * * * *")
        _ = sys.ScheduleOnce(ctx.Context(), &HealthCheck{}, ctx.Self(), 5*time.Minute)
    case *HourlyJob:
        // Dispatch work to other actors
        s.dispatchWork(ctx)
    case *HealthCheck:
        // Check cluster health
        s.checkHealth(ctx)
        // Reschedule
        _ = ctx.ActorSystem().ScheduleOnce(ctx.Context(), &HealthCheck{}, ctx.Self(), 5*time.Minute)
    }
}
```

## Best Practices

### Design

1. **Keep singletons lightweight**: Avoid heavy processing in singletons
2. **Delegate work**: Use singletons for coordination, not execution
3. **Design for relocation**: Assume the singleton will move to different nodes
4. **Persist state externally**: Don't rely on in-memory state surviving relocation

### Configuration

1. **Use roles for dedicated nodes**: Isolate critical singletons on specific nodes
2. **Set appropriate timeouts**: Balance responsiveness with reliability
3. **Register actor kinds**: Always use `WithKinds()` in cluster configuration
4. **Handle spawn errors**: Implement retry logic in your application if needed

### State Management

1. **Minimize state**: Keep singleton state as small as possible
2. **Use durable storage**: Persist important state to databases or distributed caches
3. **Implement PreStart/PostStop**: Handle state restoration and persistence
4. **Use dependencies**: Inject storage clients for cleaner design

### Operations

1. **Monitor singleton health**: Track singleton uptime and relocation events
2. **Test relocation**: Simulate node failures and verify singleton recovery
3. **Plan for downtime**: Accept brief unavailability during relocation
4. **Use multiple singletons**: Distribute responsibilities rather than creating mega-singletons

### Performance

1. **Avoid hotspots**: Don't route all traffic through a single singleton
2. **Cache lookups**: Cache singleton addresses rather than looking up repeatedly
3. **Use local actors when possible**: Reserve singletons for truly global concerns
4. **Batch operations**: Minimize singleton invocations by batching requests

## Troubleshooting

### Singleton Won't Spawn

- **Check clustering is enabled**: Verify `WithCluster()` was used
- **Verify actor kind is registered**: Use `WithKinds()` in cluster config
- **Check minimum quorum**: Ensure enough nodes have joined
- **Review logs**: Look for leader election or quorum errors

### Multiple Singleton Instances

- **Different actor types**: Ensure all nodes use the same actor implementation
- **Network partition**: Split brain can temporarily create duplicates (resolves automatically)
- **Race condition during spawn**: Use the idempotent retry behavior

### Singleton Not Relocating

- **Check relocation is enabled**: Verify `WithoutRelocation()` wasn't used
- **Review cluster leader**: Only the leader triggers relocation
- **Check actor kind**: Ensure the kind is registered on potential target nodes
- **Inspect logs**: Look for relocation errors or quorum issues

### Performance Issues

- **Too much traffic**: Singleton is a bottleneck; consider sharding or distribution
- **Remote calls**: Singleton on remote node adds latency; consider caching
- **Heavy processing**: Move computation out of singleton to worker actors
- **Slow network**: Increase timeouts or improve network infrastructure