# Spawning Actors

Spawning is the process of creating and starting new actors in the actor system. GoAkt provides multiple methods for spawning actors with various configuration options to suit different use cases.

## What is Spawning?

**Spawning** creates a new actor instance, initializes it, and registers it in the actor system. When you spawn an actor:

- A unique **PID** (Process ID) is assigned
- The actor's `PreStart` hook is called
- The actor begins processing messages
- The actor is registered under a unique name
- The actor is added to the supervision hierarchy

## Spawn Methods

### 1. Spawn

Create a named actor on the local node.

```go
pid, err := actorSystem.Spawn(ctx, "user-service", &UserActor{},
    actor.WithMailbox(actor.NewBoundedMailbox(1000)))
```

**Returns:** `*PID` - Process ID for the spawned actor

**Use when:**

- Spawning actors locally
- Need immediate PID reference
- Single-node deployments
- Testing

### 2. SpawnOn

Create an actor that can be placed on any node in the cluster.

```go
err := actorSystem.SpawnOn(ctx, "cart-manager", &CartActor{},
    actor.WithPlacement(actor.RoundRobin))
```

**Returns:** `error` - No PID returned (use `ActorOf` to lookup)

**Use when:**

- Running in cluster mode
- Want automatic load distribution
- Don't need immediate PID
- Implementing distributed systems

### 3. SpawnFromFunc

Create an anonymous actor using a function.

```go
pid, err := actorSystem.SpawnFromFunc(ctx,
    func(ctx context.Context, msg proto.Message) error {
        // Handle message
        return nil
    })
```

**Returns:** `*PID` - Process ID with auto-generated UUID name

**Use when:**

- Simple message handlers
- Don't need actor struct
- Quick prototyping
- Temporary actors

### 4. SpawnNamedFromFunc

Create a named actor using a function.

```go
pid, err := actorSystem.SpawnNamedFromFunc(ctx, "logger",
    func(ctx context.Context, msg proto.Message) error {
        log.Printf("Message: %v", msg)
        return nil
    },
    actor.WithPreStart(func(ctx context.Context) error {
        log.Println("Logger started")
        return nil
    }))
```

**Returns:** `*PID` - Process ID for the spawned actor

**Use when:**

- Need named function-based actor
- Want lifecycle hooks
- Simple actors without full struct

### 5. SpawnRouter

Create a router that distributes messages across multiple routee actors.

```go
pid, err := actorSystem.SpawnRouter(ctx, "worker-pool",
    10,              // Pool size
    &WorkerActor{},  // Routee kind
    actor.WithRoutingStrategy(actor.RoundRobinRouting))
```

**Returns:** `*PID` - Process ID for the router

**Use when:**

- Need work distribution
- Want parallel processing
- Load balancing messages
- Pool of identical workers

### 6. SpawnSingleton

Create a cluster-wide singleton actor.

```go
err := actorSystem.SpawnSingleton(ctx, "payment-processor",
    &PaymentActor{},
    actor.WithSingletonRole("payments"))
```

**Returns:** `error` - No PID returned

**Use when:**

- Need single instance across cluster
- Coordinating global state
- Critical single-point actors
- Ensuring uniqueness

## Spawn Options

### WithMailbox

Set a custom mailbox for the actor.

```go
pid, _ := actorSystem.Spawn(ctx, "actor", &MyActor{},
    actor.WithMailbox(actor.NewBoundedMailbox(500)))
```

**Available mailboxes:**

- `NewUnboundedMailbox()` - Default, no size limit
- `NewBoundedMailbox(capacity)` - Fixed capacity with backpressure
- `NewUnboundedPriorityMailBox(priorityFunc)` - Priority-based
- `NewUnboundedFairMailbox()` - Fair across producers
- `NewUnboundedSegmentedMailbox()` - High-throughput

See [Mailbox documentation](mailbox.md) for details.

### WithSupervisor

Configure supervision strategy for fault tolerance.

```go
supervisor := supervisor.NewSupervisor(
    supervisor.WithStrategy(supervisor.OneForOneStrategy),
    supervisor.WithDirective(&DatabaseError{}, supervisor.RestartDirective),
    supervisor.WithRetry(3, 30*time.Second))

pid, _ := actorSystem.Spawn(ctx, "db-actor", &DatabaseActor{},
    actor.WithSupervisor(supervisor))
```

See [Supervision documentation](supervision.md) for details.

### WithPassivationStrategy

Control when idle actors are stopped.

```go
// Time-based passivation
pid, _ := actorSystem.Spawn(ctx, "session", &SessionActor{},
    actor.WithPassivationStrategy(
        passivation.NewTimeBasedStrategy(5*time.Minute)))

// Long-lived (no passivation)
pid, _ := actorSystem.Spawn(ctx, "core-service", &CoreActor{},
    actor.WithPassivationStrategy(
        passivation.NewLongLivedStrategy()))

// Message count-based
pid, _ := actorSystem.Spawn(ctx, "temp-worker", &WorkerActor{},
    actor.WithPassivationStrategy(
        passivation.NewMessagesCountBasedStrategy(1000)))
```

See [Passivation documentation](passivation.md) for details.

### WithPassivateAfter (Deprecated)

```go
// Deprecated: Use WithPassivationStrategy instead
pid, _ := actorSystem.Spawn(ctx, "actor", &MyActor{},
    actor.WithPassivateAfter(5*time.Minute))
```

### WithLongLived

Ensure actor lives for entire system lifetime.

```go
pid, _ := actorSystem.Spawn(ctx, "critical-service", &CriticalActor{},
    actor.WithLongLived())
```

Equivalent to:

```go
actor.WithPassivationStrategy(passivation.NewLongLivedStrategy())
```

### WithDependencies

Inject dependencies for testing and resource sharing.

```go
db, _ := sql.Open("postgres", connectionString)
cache := NewCache()

pid, _ := actorSystem.Spawn(ctx, "user-service", &UserActor{},
    actor.WithDependencies(db, cache))
```

See [Dependencies documentation](dependencies.md) for details.

### WithStashing

Enable message stashing for state transitions.

```go
pid, _ := actorSystem.Spawn(ctx, "connection", &ConnectionActor{},
    actor.WithStashing())
```

See [Stashing documentation](stashing.md) for details.

### WithReentrancy

Enable async request handling.

```go
pid, _ := actorSystem.Spawn(ctx, "order-processor", &OrderActor{},
    actor.WithReentrancy(reentrancy.New(reentrancy.AllowReentrant)))
```

**Modes:**

- `AllowReentrant` - Multiple concurrent requests
- `StashNonReentrant` - Sequential request processing

See [Reentrancy documentation](reentrancy.md) for details.

### WithPlacement

Set cluster placement strategy (SpawnOn only).

```go
err := actorSystem.SpawnOn(ctx, "worker", &WorkerActor{},
    actor.WithPlacement(actor.RoundRobin))
```

**Strategies:**

- `RoundRobin` - Distribute evenly across nodes
- `Random` - Random node selection
- `Local` - Force local node
- `LeastLoad` - Node with least load

### WithRole

Restrict placement to nodes with specific role.

```go
err := actorSystem.SpawnOn(ctx, "payment", &PaymentActor{},
    actor.WithRole("payments"))
```

Only spawns on nodes advertising the "payments" role.

### WithRelocationDisabled

Prevent actor relocation on node failure.

```go
pid, _ := actorSystem.Spawn(ctx, "local-only", &LocalActor{},
    actor.WithRelocationDisabled())
```

**Use when:**

- Actor has node-specific state
- Local resources required
- Don't want automatic failover

### WithDataCenter

Spawn actor in a different datacenter.

```go
dc := &datacenter.DataCenter{
    Name:   "dc-west",
    Region: "us-west-2",
}

err := actorSystem.SpawnOn(ctx, "analytics", &AnalyticsActor{},
    actor.WithDataCenter(dc))
```

## Spawn Placement Strategies

When using `SpawnOn` in cluster mode, placement strategies determine where actors are created.

### RoundRobin (Default)

Distributes actors evenly across nodes.

```go
err := actorSystem.SpawnOn(ctx, "worker-1", &WorkerActor{},
    actor.WithPlacement(actor.RoundRobin))
```

**Distribution:**

```
Node1: actor1, actor4, actor7
Node2: actor2, actor5, actor8
Node3: actor3, actor6, actor9
```

**Best for:**

- Balanced load distribution
- Stable cluster topology
- Predictable placement

### Random

Selects a random node for each spawn.

```go
err := actorSystem.SpawnOn(ctx, "task-executor", &TaskActor{},
    actor.WithPlacement(actor.Random))
```

**Distribution:**

```
Node1: actor1, actor3, actor7, actor8
Node2: actor4, actor5
Node3: actor2, actor6, actor9
```

**Best for:**

- Quick distribution
- Dynamic clusters
- Don't need even distribution

### Local

Forces spawn on the local node.

```go
err := actorSystem.SpawnOn(ctx, "local-cache", &CacheActor{},
    actor.WithPlacement(actor.Local))
```

**Best for:**

- Local resource access
- Co-location requirements
- Testing cluster features locally

### LeastLoad

Spawns on node with lowest current load.

```go
err := actorSystem.SpawnOn(ctx, "compute-worker", &ComputeActor{},
    actor.WithPlacement(actor.LeastLoad))
```

**Best for:**

- Dynamic load balancing
- Resource optimization
- Heterogeneous nodes

**Note:** Higher overhead due to load metric collection.

## Complete Examples

### Basic Local Spawn

```go
package main

import (
    "context"
    "time"

    "github.com/tochemey/goakt/v3/actor"
)

type GreeterActor struct {
    greetings int
}

func (a *GreeterActor) PreStart(ctx *actor.Context) error {
    a.greetings = 0
    ctx.ActorSystem().Logger().Info("Greeter started")
    return nil
}

func (a *GreeterActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Greet:
        a.greetings++
        ctx.Response(&Greeting{
            Message: "Hello, " + msg.GetName(),
            Count:   int32(a.greetings),
        })
    }
}

func (a *GreeterActor) PostStop(ctx *actor.Context) error {
    ctx.ActorSystem().Logger().Info("Greeter stopped", "greetings", a.greetings)
    return nil
}

func main() {
    ctx := context.Background()

    // Create actor system
    actorSystem, _ := actor.NewActorSystem("GreeterSystem",
        actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()))

    actorSystem.Start(ctx)
    defer actorSystem.Stop(ctx)

    // Spawn actor
    pid, err := actorSystem.Spawn(ctx, "greeter", &GreeterActor{})
    if err != nil {
        panic(err)
    }

    // Use actor
    response, _ := actor.Ask(ctx, pid, &Greet{Name: "World"}, time.Second)
    greeting := response.(*Greeting)
    println(greeting.Message)
}
```

### Spawn with Options

```go
supervisor := supervisor.NewSupervisor(
    supervisor.WithStrategy(supervisor.OneForOneStrategy),
    supervisor.WithDirective(&DatabaseError{}, supervisor.RestartDirective),
    supervisor.WithRetry(3, 30*time.Second))

pid, err := actorSystem.Spawn(ctx, "user-service", &UserActor{},
    // Mailbox configuration
    actor.WithMailbox(actor.NewBoundedMailbox(1000)),

    // Supervision
    actor.WithSupervisor(supervisor),

    // Passivation
    actor.WithPassivationStrategy(
        passivation.NewTimeBasedStrategy(5*time.Minute)),

    // Dependencies
    actor.WithDependencies(db, cache),

    // Stashing
    actor.WithStashing(),

    // Reentrancy
    actor.WithReentrancy(reentrancy.New(reentrancy.AllowReentrant)))
```

### Cluster Spawn

```go
// Spawn on cluster with specific placement
err := actorSystem.SpawnOn(ctx, "cart-manager", &CartActor{},
    actor.WithPlacement(actor.RoundRobin),
    actor.WithRole("web"),
    actor.WithPassivationStrategy(
        passivation.NewTimeBasedStrategy(10*time.Minute)))

if err != nil {
    log.Fatalf("Failed to spawn: %v", err)
}

// Lookup actor after spawn
time.Sleep(100 * time.Millisecond) // Give time to spawn
pid := actorSystem.ActorOf("cart-manager")
if pid != nil {
    actor.Tell(ctx, pid, &AddItem{ItemId: "item123"})
}
```

### Function-Based Actor

```go
// Anonymous function actor
pid, err := actorSystem.SpawnFromFunc(ctx,
    func(ctx context.Context, msg proto.Message) error {
        switch m := msg.(type) {
        case *LogMessage:
            log.Printf("[%s] %s", m.Level, m.Message)
        }
        return nil
    })

// Named function actor with hooks
pid, err := actorSystem.SpawnNamedFromFunc(ctx, "metrics-collector",
    func(ctx context.Context, msg proto.Message) error {
        switch m := msg.(type) {
        case *Metric:
            recordMetric(m)
        }
        return nil
    },
    actor.WithPreStart(func(ctx context.Context) error {
        log.Println("Metrics collector started")
        return initializeMetrics()
    }),
    actor.WithPostStop(func(ctx context.Context) error {
        log.Println("Metrics collector stopped")
        return flushMetrics()
    }))
```

### Router

```go
// Create worker pool
routerPID, err := actorSystem.SpawnRouter(ctx, "worker-pool",
    10,              // 10 workers
    &WorkerActor{},  // Worker type
    actor.WithRoutingStrategy(actor.RoundRobinRouting))

if err != nil {
    log.Fatal(err)
}

// Send messages - distributed across workers
for i := 0; i < 100; i++ {
    actor.Tell(ctx, routerPID, &WorkItem{Id: i})
}
```

### Singleton

```go
// Spawn singleton across cluster
err := actorSystem.SpawnSingleton(ctx, "payment-coordinator",
    &PaymentCoordinatorActor{},
    actor.WithSingletonRole("payments"),
    actor.WithSingletonSpawnTimeout(30*time.Second))

if err != nil {
    log.Fatalf("Failed to spawn singleton: %v", err)
}

// Use singleton
time.Sleep(time.Second) // Wait for spawn
pid := actorSystem.ActorOf("payment-coordinator")
if pid != nil {
    response, _ := actor.Ask(ctx, pid, &ProcessPayment{}, 5*time.Second)
}
```

## Spawn Patterns

### Pattern 1: Factory Pattern

```go
type ActorFactory struct {
    db    *sql.DB
    cache *Cache
}

func (f *ActorFactory) SpawnUserActor(ctx context.Context,
    system *actor.ActorSystem, userId string) (*actor.PID, error) {

    return system.Spawn(ctx, fmt.Sprintf("user-%s", userId),
        &UserActor{userId: userId},
        actor.WithDependencies(f.db, f.cache),
        actor.WithPassivationStrategy(
            passivation.NewTimeBasedStrategy(10*time.Minute)))
}

// Usage
factory := &ActorFactory{db: db, cache: cache}
pid, _ := factory.SpawnUserActor(ctx, actorSystem, "user123")
```

### Pattern 2: Actor Hierarchy

```go
type ManagerActor struct {
    workers []*actor.PID
}

func (a *ManagerActor) PreStart(ctx *actor.Context) error {
    // Spawn child workers
    for i := 0; i < 5; i++ {
        worker, err := ctx.Spawn(fmt.Sprintf("worker-%d", i),
            &WorkerActor{})
        if err != nil {
            return err
        }
        a.workers = append(a.workers, worker)
    }
    return nil
}

func (a *ManagerActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *DistributeWork:
        // Distribute to workers
        for i, work := range msg.WorkItems {
            workerIdx := i % len(a.workers)
            ctx.Tell(a.workers[workerIdx], work)
        }
    }
}
```

### Pattern 3: Dynamic Spawning

```go
type PoolManager struct {
    activeActors map[string]*actor.PID
}

func (a *PoolManager) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *GetOrCreateActor:
        // Spawn if doesn't exist
        if _, exists := a.activeActors[msg.GetKey()]; !exists {
            pid, err := ctx.Spawn(msg.GetKey(), &DynamicActor{})
            if err != nil {
                ctx.Err(err)
                return
            }
            a.activeActors[msg.GetKey()] = pid
        }

        ctx.Response(&ActorReady{
            PID: a.activeActors[msg.GetKey()],
        })
    }
}
```

### Pattern 4: Conditional Spawn

```go
func spawnWithConfig(ctx context.Context, system *actor.ActorSystem,
    config *ActorConfig) (*actor.PID, error) {

    var opts []actor.SpawnOption

    // Conditional mailbox
    if config.HighThroughput {
        opts = append(opts,
            actor.WithMailbox(actor.NewUnboundedSegmentedMailbox()))
    } else {
        opts = append(opts,
            actor.WithMailbox(actor.NewBoundedMailbox(config.MailboxSize)))
    }

    // Conditional passivation
    if config.Persistent {
        opts = append(opts, actor.WithLongLived())
    } else {
        opts = append(opts,
            actor.WithPassivationStrategy(
                passivation.NewTimeBasedStrategy(config.IdleTimeout)))
    }

    // Conditional supervision
    if config.CriticalActor {
        supervisor := supervisor.NewSupervisor(
            supervisor.WithAnyErrorDirective(supervisor.RestartDirective),
            supervisor.WithRetry(10, time.Minute))
        opts = append(opts, actor.WithSupervisor(supervisor))
    }

    return system.Spawn(ctx, config.Name, config.ActorInstance, opts...)
}
```

## Best Practices

### Do's ✅

1. **Use unique names** for actors within the system
2. **Choose appropriate mailbox** based on requirements
3. **Configure supervision** for fault tolerance
4. **Set passivation** for resource management
5. **Use SpawnOn** for distributed deployments
6. **Handle spawn errors** appropriately

```go
// Good: Comprehensive configuration
pid, err := actorSystem.Spawn(ctx, "user-service", &UserActor{},
    actor.WithMailbox(actor.NewBoundedMailbox(1000)),
    actor.WithSupervisor(supervisor),
    actor.WithPassivationStrategy(
        passivation.NewTimeBasedStrategy(5*time.Minute)),
    actor.WithDependencies(db))

if err != nil {
    log.Fatalf("Failed to spawn: %v", err)
}
```

### Don'ts ❌

1. **Don't reuse actor names** in the same system
2. **Don't spawn without error handling**
3. **Don't ignore passivation** for long-running systems
4. **Don't spawn actors in actor constructors** (use PreStart)
5. **Don't spawn without considering mailbox size**

```go
// Bad: No error handling, no configuration
pid, _ := actorSystem.Spawn(ctx, "actor", &MyActor{})

// Bad: Spawning in constructor
type BadActor struct {
    child *actor.PID
}

func NewBadActor(ctx context.Context, system *actor.ActorSystem) *BadActor {
    child, _ := system.Spawn(ctx, "child", &ChildActor{}) // ❌
    return &BadActor{child: child}
}

// Good: Spawn in PreStart
func (a *GoodActor) PreStart(ctx *actor.Context) error {
    child, err := ctx.Spawn("child", &ChildActor{}) // ✅
    if err != nil {
        return err
    }
    a.child = child
    return nil
}
```

## Error Handling

Common spawn errors:

```go
pid, err := actorSystem.Spawn(ctx, "my-actor", &MyActor{})
if err != nil {
    switch {
    case errors.Is(err, actor.ErrActorSystemNotStarted):
        log.Fatal("Actor system not started")

    case errors.Is(err, actor.ErrActorAlreadyExists):
        log.Warn("Actor already exists, getting existing")
        pid = actorSystem.ActorOf("my-actor")

    case errors.Is(err, actor.ErrInvalidActorName):
        log.Fatal("Invalid actor name")

    default:
        log.Fatalf("Failed to spawn: %v", err)
    }
}
```

## Performance Considerations

- **Spawn overhead**: Minimal, but avoid spawning thousands in tight loop
- **Mailbox choice**: Impacts memory and throughput
- **Cluster placement**: LeastLoad has higher overhead than RoundRobin
- **Passivation**: Balance memory vs. startup cost

## Testing

```go
func TestActorSpawn(t *testing.T) {
    ctx := context.Background()
    system, _ := actor.NewActorSystem("test",
        actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()))
    system.Start(ctx)
    defer system.Stop(ctx)

    // Test spawn
    pid, err := system.Spawn(ctx, "test-actor", &TestActor{})
    assert.NoError(t, err)
    assert.NotNil(t, pid)
    assert.True(t, pid.IsRunning())

    // Test duplicate spawn
    _, err = system.Spawn(ctx, "test-actor", &TestActor{})
    assert.Error(t, err)

    // Test with options
    pid2, err := system.Spawn(ctx, "test-actor-2", &TestActor{},
        actor.WithMailbox(actor.NewBoundedMailbox(10)),
        actor.WithPassivationStrategy(
            passivation.NewTimeBasedStrategy(time.Second)))
    assert.NoError(t, err)
    assert.NotNil(t, pid2)
}
```

## Summary

- **Spawn** creates actors locally and returns PID
- **SpawnOn** distributes actors across cluster
- **SpawnFromFunc** creates function-based actors
- **SpawnRouter** creates message distribution routers
- **SpawnSingleton** ensures single instance across cluster
- **Spawn options** configure mailbox, supervision, passivation, etc.
- **Placement strategies** control cluster distribution
- **Best practices** ensure reliable actor creation

## Next Steps

- **[Overview](overview.md)**: Actor fundamentals and lifecycle
- **[Mailbox](mailbox.md)**: Mailbox types and configuration
- **[Supervision](supervision.md)**: Fault tolerance strategies
- **[Passivation](passivation.md)**: Resource management
- **[Dependencies](dependencies.md)**: Dependency injection
