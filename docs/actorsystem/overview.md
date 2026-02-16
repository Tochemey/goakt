# Actor System Overview

The Actor System is the foundation of GoAkt - it manages actor lifecycle, provides infrastructure services, and coordinates distributed operations.

## What is an Actor System?

An **Actor System** is a container and runtime environment for actors that provides:

- **Actor lifecycle management**: Creation, supervision, and termination
- **Infrastructure services**: Remoting, clustering, pub/sub
- **Resource management**: Mailboxes, schedulers, event streams
- **Configuration**: Logging, metrics, extensions
- **Coordination**: Distributed operations and shutdown

Think of it as the operating system for your actors.

## Creating an Actor System

### Basic Creation

```go
actorSystem, err := actor.NewActorSystem("MySystem")
if err != nil {
    log.Fatal(err)
}
```

**Requirements:**

- Name must be unique per process
- Name must match pattern: `^[a-zA-Z0-9][a-zA-Z0-9-_]*$`
- Name cannot be empty

### With Options

```go
actorSystem, err := actor.NewActorSystem("MySystem",
    actor.WithLogger(logger),
    actor.WithPassivationDisabled(),
    actor.WithShutdownTimeout(30*time.Second))
```

## Actor System Lifecycle

### Start

Initialize and start the actor system:

```go
ctx := context.Background()
err := actorSystem.Start(ctx)
if err != nil {
    log.Fatalf("Failed to start: %v", err)
}
```

**What happens on Start:**

1. Initialize internal infrastructure
2. Start event stream
3. Start scheduler
4. Enable remoting (if configured)
5. Join cluster (if configured)
6. Start system actors (guardians)
7. Initialize pub/sub (if enabled)
8. Start metric collection (if enabled)

### Stop

Gracefully shutdown the actor system:

```go
err := actorSystem.Stop(ctx)
if err != nil {
    log.Fatalf("Failed to stop: %v", err)
}
```

**What happens on Stop:**

1. Execute shutdown hooks (coordinated shutdown)
2. Stop all actors gracefully
3. Leave cluster (if in cluster mode)
4. Stop remoting
5. Stop scheduler
6. Stop event stream
7. Clean up resources

**Note:** `Stop` does not call `os.Exit()` - you must handle that explicitly.

### Complete Lifecycle Example

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

actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithLogger(logger))
```

**Default:** Uses GoAkt's default logger

### WithPassivationDisabled

Disable passivation globally:

```go
actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithPassivationDisabled())
```

**Use when:**

- All actors should live forever
- Testing/development
- Memory is not constrained

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

Configure actor eviction when memory limits reached:

```go
strategy := actor.NewEvictionStrategy(1000, actor.LRU)

actorSystem, _ := actor.NewActorSystem("MySystem",
    actor.WithEvictionStrategy(strategy, 5*time.Second))
```

**Strategies:**

- `LRU` - Least Recently Used
- `LFU` - Least Frequently Used
- `MRU` - Most Recently Used

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

Create router for load balancing:

```go
routerPID, err := actorSystem.SpawnRouter(ctx, "worker-pool",
    10,
    &WorkerActor{},
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

Lookup actor by name:

```go
pid := actorSystem.ActorOf("my-actor")
if pid != nil {
    actor.Tell(ctx, pid, &Message{})
}
```

**Returns:** PID if exists, nil otherwise

### Actors

Get all local actors:

```go
actors := actorSystem.Actors()
for _, pid := range actors {
    fmt.Printf("Actor: %s\n", pid.Name())
}
```

**Returns:** Slice of local actor PIDs

### ActorRefs

Get all actors (including cluster):

```go
refs, err := actorSystem.ActorRefs(ctx, 5*time.Second)
if err != nil {
    log.Printf("Failed to get refs: %v", err)
}

for _, ref := range refs {
    fmt.Printf("Actor: %s at %s\n", ref.Name(), ref.Address())
}
```

**Returns:** ActorRefs from local and cluster nodes

### Subscribe/Unsubscribe

Topic-based messaging (requires `WithPubSub()`):

```go
// Subscribe
actorSystem.Subscribe(topic, subscriberPID)

// Publish
actorSystem.Publish(ctx, topic, &Event{})

// Unsubscribe
actorSystem.Unsubscribe(topic, subscriberPID)
```

### Metric

Get runtime metrics:

```go
metrics := actorSystem.Metric(ctx)
fmt.Printf("Actors: %d\n", metrics.ActorsCount)
fmt.Printf("Dead letters: %d\n", metrics.DeadlettersCount)
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
if actorSystem.Running() {
    fmt.Println("System is running")
}
```

### InCluster

Check if in cluster mode:

```go
if actorSystem.InCluster() {
    fmt.Println("Running in cluster")
}
```

## Complete Configuration Example

```go
package main

import (
    "context"
    "time"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/discovery"
    "github.com/tochemey/goakt/v3/log"
    "github.com/tochemey/goakt/v3/remote"
    "github.com/tochemey/goakt/v3/supervisor"
)

func main() {
    ctx := context.Background()

    // Setup logger
    logger := log.New(log.InfoLevel, os.Stdout)

    // Setup supervisor
    defaultSupervisor := supervisor.NewSupervisor(
        supervisor.WithStrategy(supervisor.OneForOneStrategy),
        supervisor.WithDirective(&TransientError{}, supervisor.RestartDirective),
        supervisor.WithRetry(3, 30*time.Second))

    // Setup remoting
    remoteConfig := &remote.Config{
        Host: "localhost",
        Port: 3000,
    }

    // Setup clustering
    clusterConfig := &actor.ClusterConfig{
        Discovery:     consulDiscovery,
        ReplicaCount:  3,
    }

    // Create actor system with full configuration
    actorSystem, err := actor.NewActorSystem("ProductionSystem",
        // Logging
        actor.WithLogger(logger),

        // Actor lifecycle
        actor.WithActorInitTimeout(10*time.Second),
        actor.WithActorInitMaxRetries(3),
        actor.WithDefaultSupervisor(defaultSupervisor),

        // Passivation
        actor.WithPassivationDisabled(), // Or custom strategy

        // Distributed features
        actor.WithRemote(remoteConfig),
        actor.WithCluster(clusterConfig),

        // Features
        actor.WithPubSub(),
        actor.WithMetrics(),

        // Shutdown
        actor.WithShutdownTimeout(30*time.Second),
        actor.WithCoordinatedShutdown(
            &FlushMetricsHook{},
            &SaveStateHook{},
        ))

    if err != nil {
        logger.Fatal("Failed to create system", "error", err)
    }

    // Start system
    if err := actorSystem.Start(ctx); err != nil {
        logger.Fatal("Failed to start", "error", err)
    }

    defer func() {
        if err := actorSystem.Stop(ctx); err != nil {
            logger.Error("Failed to stop", "error", err)
        }
    }()

    // System is ready - spawn actors and run application
    logger.Info("Actor system ready")
}
```

## Best Practices

### Do's ✅

1. **Handle signals** for graceful shutdown
2. **Set appropriate timeouts** based on system size
3. **Use coordinated shutdown** for cleanup
4. **Configure default supervisor** for consistency
5. **Enable metrics** for monitoring
6. **Use TLS in production** for security
7. **Test shutdown sequence** thoroughly

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

## Testing

### Test Actor System Setup

```go
func TestActorSystem(t *testing.T) {
    ctx := context.Background()

    // Create test system
    system, err := actor.NewActorSystem("test",
        actor.WithPassivationDisabled())

    assert.NoError(t, err)
    assert.NotNil(t, system)

    // Start
    err = system.Start(ctx)
    assert.NoError(t, err)
    assert.True(t, system.Running())

    // Stop
    err = system.Stop(ctx)
    assert.NoError(t, err)
    assert.False(t, system.Running())
}
```

### Test with Actors

```go
func TestSystemWithActors(t *testing.T) {
    ctx := context.Background()
    system, _ := actor.NewActorSystem("test",
        actor.WithPassivationDisabled())

    system.Start(ctx)
    defer system.Stop(ctx)

    // Spawn actors
    pid, err := system.Spawn(ctx, "test-actor", &TestActor{})
    assert.NoError(t, err)
    assert.NotNil(t, pid)

    // Use actor
    response, err := actor.Ask(ctx, pid, &TestMessage{}, time.Second)
    assert.NoError(t, err)
    assert.NotNil(t, response)
}
```

## Performance Considerations

- **System name**: Short names reduce overhead
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

## Next Steps

- **[Coordinated Shutdown](coordinated_shutdown.md)**: Graceful shutdown with hooks
- **[Spawning Actors](../actors/spawn.md)**: Creating actors
- **[Remoting](../remoting/overview.md)**: Remote communication
- **[Clustering](../cluster/overview.md)**: Distributed systems
