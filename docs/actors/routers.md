# Routers

Routers are specialized actors that distribute messages across a pool of worker actors (routees). They enable parallel processing, load balancing, and various message distribution patterns.

## What is a Router?

A **router** is an actor that:
- Manages a pool of worker actors (routees)
- Routes incoming messages to routees based on a strategy
- Supervises routee lifecycle (restart, stop, resume on failure)
- Supports dynamic pool sizing
- Provides various routing algorithms for different use cases

Think of a router as a smart dispatcher that distributes work across multiple workers while handling failures gracefully.

## Why Use Routers?

**Benefits:**
- **Parallel processing**: Multiple workers process messages concurrently
- **Load balancing**: Even distribution of work across workers
- **Fault isolation**: Worker failures don't affect others
- **Scalability**: Easy to scale by adding more routees
- **Simplified architecture**: Single entry point for work distribution

**Use cases:**
- Worker pools for parallel task processing
- Load balancing across identical services
- Fan-out patterns for broadcasting
- High-throughput message processing
- Resilient service pools

## Creating a Router

### SpawnRouter

```go
routerPID, err := actorSystem.SpawnRouter(ctx, "worker-pool",
    10,              // Pool size (number of routees)
    &WorkerActor{},  // Routee type
    actor.WithRoutingStrategy(actor.RoundRobinRouting))
```

**Parameters:**
- `name` - Unique router name
- `poolSize` - Number of routees to spawn
- `routeesKind` - Actor type for routees (all routees are same type)
- `opts` - Optional router configuration

## Routing Strategies

### 1. RoundRobin (Default)

Distributes messages evenly to routees in sequence.

```go
routerPID, err := actorSystem.SpawnRouter(ctx, "rr-pool",
    5,
    &WorkerActor{},
    actor.WithRoutingStrategy(actor.RoundRobinRouting))
```

**Distribution pattern:**
```
Message 1 → Routee 0
Message 2 → Routee 1
Message 3 → Routee 2
Message 4 → Routee 3
Message 5 → Routee 4
Message 6 → Routee 0 (cycles back)
```

**Best for:**
- Uniform workload distribution
- Stateless workers
- Predictable load balancing
- When order doesn't matter

**Characteristics:**
- ✅ Even distribution
- ✅ Predictable
- ✅ Low overhead
- ⚠️ No consideration for routee load

### 2. Random

Randomly selects a routee for each message.

```go
routerPID, err := actorSystem.SpawnRouter(ctx, "random-pool",
    10,
    &WorkerActor{},
    actor.WithRoutingStrategy(actor.RandomRouting))
```

**Distribution pattern:**
```
Message 1 → Routee 3 (random)
Message 2 → Routee 7 (random)
Message 3 → Routee 1 (random)
Message 4 → Routee 3 (random)
```

**Best for:**
- Simple load distribution
- When exact balance doesn't matter
- Stateless workers
- Quick setup

**Characteristics:**
- ✅ Simple implementation
- ✅ Low overhead
- ⚠️ Uneven distribution possible
- ⚠️ No guarantees

### 3. FanOut (Broadcast)

Sends each message to **all** routees concurrently.

```go
routerPID, err := actorSystem.SpawnRouter(ctx, "fanout-pool",
    5,
    &EventHandlerActor{},
    actor.WithRoutingStrategy(actor.FanOutRouting))
```

**Distribution pattern:**
```
Message 1 → All routees (0, 1, 2, 3, 4) in parallel
```

**Best for:**
- Event broadcasting
- Cache invalidation
- Multi-sink processing
- Pub/sub patterns
- When all routees need the message

**Characteristics:**
- ✅ All routees receive message
- ✅ Parallel execution
- ⚠️ Higher resource usage
- ⚠️ Duplicate processing

### 4. ScatterGatherFirst

Sends message to all routees and returns the **first successful response**.

```go
routerPID, err := actorSystem.SpawnRouter(ctx, "scatter-gather",
    10,
    &SearchActor{},
    actor.AsScatterGatherFirst(500*time.Millisecond))
```

**Flow:**
1. Broadcast message to all routees
2. Wait for first response
3. Return first response to sender
4. Ignore late responses
5. Timeout after specified duration

**Best for:**
- Getting fastest result
- Redundant computing
- Racing multiple services
- Latency optimization
- Failover patterns

**Characteristics:**
- ✅ Returns fastest response
- ✅ Built-in timeout
- ⚠️ Duplicate work
- ⚠️ Higher resource usage

**Example:**

```go
// Create scatter-gather router
routerPID, _ := actorSystem.SpawnRouter(ctx, "search-pool",
    5,
    &SearchActor{},
    actor.AsScatterGatherFirst(1*time.Second))

// Send query - gets fastest response
query, _ := anypb.New(&SearchQuery{Term: "golang"})
actor.Tell(ctx, routerPID, &goaktpb.Broadcast{Message: query})

// First response arrives as message
```

### 5. TailChopping

Probes routees **sequentially** with delays, returning first response.

```go
routerPID, err := actorSystem.SpawnRouter(ctx, "tail-chopping",
    5,
    &QueryActor{},
    actor.AsTailChopping(
        2*time.Second,        // Total time budget (within)
        200*time.Millisecond)) // Interval between attempts
```

**Flow:**
1. Send to first routee
2. Wait `interval` duration
3. If no response, send to next routee
4. Repeat until response or timeout
5. Return first successful response

**Best for:**
- Controlled sequential probing
- Avoiding thundering herd
- Predictable latency
- Resource-constrained scenarios
- When full scatter is too expensive

**Characteristics:**
- ✅ Controlled fan-out
- ✅ Avoids overwhelming routees
- ✅ Bounded latency
- ⚠️ Sequential overhead
- ⚠️ Slower than scatter-gather

**Example:**

```go
// Try routees sequentially
// Total budget: 2s, probe every 200ms
routerPID, _ := actorSystem.SpawnRouter(ctx, "cautious-pool",
    10,
    &DatabaseActor{},
    actor.AsTailChopping(2*time.Second, 200*time.Millisecond))

query, _ := anypb.New(&DatabaseQuery{SQL: "SELECT ..."})
actor.Tell(ctx, routerPID, &goaktpb.Broadcast{Message: query})
```

## Router Options

### WithRoutingStrategy

Set the routing strategy for standard routers.

```go
routerPID, _ := actorSystem.SpawnRouter(ctx, "pool", 10, &Worker{},
    actor.WithRoutingStrategy(actor.RoundRobinRouting))
```

**Note:** Mutually exclusive with `AsScatterGatherFirst` and `AsTailChopping`.

### AsScatterGatherFirst

Configure scatter-gather-first pattern.

```go
routerPID, _ := actorSystem.SpawnRouter(ctx, "pool", 10, &Worker{},
    actor.AsScatterGatherFirst(500*time.Millisecond))
```

**Parameter:**
- `within` - Maximum time to wait for any response

### AsTailChopping

Configure tail-chopping pattern.

```go
routerPID, _ := actorSystem.SpawnRouter(ctx, "pool", 10, &Worker{},
    actor.AsTailChopping(2*time.Second, 200*time.Millisecond))
```

**Parameters:**
- `within` - Total time budget for any response
- `interval` - Delay between successive attempts

### Routee Supervision

Control how the router handles routee failures.

#### WithStopRouteeOnFailure (Default)

Stop and remove failing routees.

```go
routerPID, _ := actorSystem.SpawnRouter(ctx, "pool", 10, &Worker{},
    actor.WithStopRouteeOnFailure())
```

**Behavior:**
- Failing routee is terminated
- Removed from routing pool
- Not automatically replaced
- Use for unrecoverable failures

#### WithRestartRouteeOnFailure

Restart failing routees with retry logic.

```go
routerPID, _ := actorSystem.SpawnRouter(ctx, "pool", 10, &Worker{},
    actor.WithRestartRouteeOnFailure(3, 30*time.Second))
```

**Parameters:**
- `maxRetries` - Maximum restart attempts
- `timeout` - Time window for retries

**Behavior:**
- Routee is restarted (state reset)
- Retries within time window
- If max retries exceeded, routee is stopped
- Use for corrupted state

#### WithResumeRouteeOnFailure

Continue routee without restart.

```go
routerPID, _ := actorSystem.SpawnRouter(ctx, "pool", 10, &Worker{},
    actor.WithResumeRouteeOnFailure())
```

**Behavior:**
- Routee keeps current state
- Continues processing next message
- Failing message not retried
- Use for transient failures

## Sending Messages to Routers

### Using Broadcast

Messages must be wrapped in `Broadcast` for routing:

```go
// Wrap your message in Broadcast
payload, _ := anypb.New(&WorkItem{Id: 1, Data: "work"})
broadcastMsg := &goaktpb.Broadcast{Message: payload}

// Send to router
actor.Tell(ctx, routerPID, broadcastMsg)
```

### Complete Example

```go
package main

import (
    "context"
    "fmt"
    
    "google.golang.org/protobuf/types/known/anypb"
    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/goaktpb"
)

type WorkerActor struct {
    processed int
}

func (a *WorkerActor) PreStart(ctx *actor.Context) error {
    a.processed = 0
    ctx.ActorSystem().Logger().Info("Worker started", "name", ctx.ActorName())
    return nil
}

func (a *WorkerActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *WorkItem:
        // Process work
        a.processed++
        result := a.processWork(msg)
        
        ctx.Logger().Info("Work processed",
            "worker", ctx.Self().Name(),
            "work_id", msg.GetId(),
            "total_processed", a.processed)
        
        // Send result back to sender
        ctx.Response(&WorkResult{
            WorkId: msg.GetId(),
            Result: result,
        })
    }
}

func (a *WorkerActor) processWork(item *WorkItem) string {
    // Simulate work
    return fmt.Sprintf("Processed: %s", item.GetData())
}

func (a *WorkerActor) PostStop(ctx *actor.Context) error {
    ctx.ActorSystem().Logger().Info("Worker stopped",
        "name", ctx.ActorName(),
        "processed", a.processed)
    return nil
}

func main() {
    ctx := context.Background()
    
    // Create actor system (use WithPassivationStrategy(passivation.NewLongLivedStrategy()) to disable passivation per-actor)
    actorSystem, _ := actor.NewActorSystem("RouterSystem")
    actorSystem.Start(ctx)
    defer actorSystem.Stop(ctx)
    
    // Create router with 5 workers
    routerPID, err := actorSystem.SpawnRouter(ctx, "worker-pool",
        5,
        &WorkerActor{},
        actor.WithRoutingStrategy(actor.RoundRobinRouting),
        actor.WithRestartRouteeOnFailure(3, 30*time.Second))
    
    if err != nil {
        panic(err)
    }
    
    // Send work items to router
    for i := 0; i < 20; i++ {
        workItem := &WorkItem{
            Id:   int32(i),
            Data: fmt.Sprintf("task-%d", i),
        }
        
        payload, _ := anypb.New(workItem)
        broadcastMsg := &goaktpb.Broadcast{Message: payload}
        
        actor.Tell(ctx, routerPID, broadcastMsg)
    }
    
    // Wait for processing
    time.Sleep(2 * time.Second)
}
```

## Dynamic Pool Sizing

Routers support dynamic scaling of the routee pool.

### Scaling Up

Add more routees to the pool:

```go
// Increase pool by 5 routees
adjustMsg := &goaktpb.AdjustRouterPoolSize{PoolSize: 5}
actor.Tell(ctx, routerPID, adjustMsg)
```

### Scaling Down

Remove routees from the pool:

```go
// Decrease pool by 3 routees
adjustMsg := &goaktpb.AdjustRouterPoolSize{PoolSize: -3}
actor.Tell(ctx, routerPID, adjustMsg)
```

### Query Routees

Get list of active routees:

```go
response, _ := actor.Ask(ctx, routerPID, &goaktpb.GetRoutees{}, time.Second)
routees := response.(*goaktpb.Routees)
fmt.Printf("Active routees: %v\n", routees.GetNames())
```

### Complete Scaling Example

```go
type LoadMonitor struct {
    routerPID *actor.PID
    threshold int
}

func (a *LoadMonitor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *CheckLoad:
        // Get current routees
        resp, _ := ctx.Ask(a.routerPID, &goaktpb.GetRoutees{}, time.Second)
        routees := resp.(*goaktpb.Routees)
        currentSize := len(routees.GetNames())
        
        // Calculate required size
        load := a.getCurrentLoad()
        requiredSize := load / a.threshold
        
        if requiredSize > currentSize {
            // Scale up
            delta := requiredSize - currentSize
            ctx.Tell(a.routerPID, &goaktpb.AdjustRouterPoolSize{
                PoolSize: int32(delta),
            })
            ctx.Logger().Info("Scaling up", "delta", delta)
        } else if requiredSize < currentSize {
            // Scale down
            delta := currentSize - requiredSize
            ctx.Tell(a.routerPID, &goaktpb.AdjustRouterPoolSize{
                PoolSize: -int32(delta),
            })
            ctx.Logger().Info("Scaling down", "delta", delta)
        }
    }
}
```

## Routing Patterns

### Pattern 1: Worker Pool

```go
// Create worker pool for parallel processing
routerPID, _ := actorSystem.SpawnRouter(ctx, "task-workers",
    10,
    &TaskWorker{},
    actor.WithRoutingStrategy(actor.RoundRobinRouting),
    actor.WithRestartRouteeOnFailure(3, time.Minute))

// Submit tasks
for _, task := range tasks {
    payload, _ := anypb.New(task)
    actor.Tell(ctx, routerPID, &goaktpb.Broadcast{Message: payload})
}
```

### Pattern 2: Event Broadcasting

```go
// Broadcast events to all listeners
routerPID, _ := actorSystem.SpawnRouter(ctx, "event-listeners",
    5,
    &EventListener{},
    actor.WithRoutingStrategy(actor.FanOutRouting))

// Broadcast event
event, _ := anypb.New(&SystemEvent{Type: "shutdown"})
actor.Tell(ctx, routerPID, &goaktpb.Broadcast{Message: event})
```

### Pattern 3: Redundant Computing

```go
// Get fastest result from multiple workers
routerPID, _ := actorSystem.SpawnRouter(ctx, "compute-cluster",
    10,
    &ComputeWorker{},
    actor.AsScatterGatherFirst(500*time.Millisecond))

// Send computation - get fastest result
query, _ := anypb.New(&ComputeTask{Data: complexData})
actor.Tell(ctx, routerPID, &goaktpb.Broadcast{Message: query})
```

### Pattern 4: Adaptive Load Balancing

```go
type AdaptiveRouter struct {
    routerPID     *actor.PID
    minWorkers    int
    maxWorkers    int
    targetLatency time.Duration
}

func (a *AdaptiveRouter) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *MonitorPerformance:
        metrics := a.getMetrics()
        
        resp, _ := ctx.Ask(a.routerPID, &goaktpb.GetRoutees{}, time.Second)
        routees := resp.(*goaktpb.Routees)
        currentSize := len(routees.GetNames())
        
        if metrics.AvgLatency > a.targetLatency && currentSize < a.maxWorkers {
            // Scale up
            ctx.Tell(a.routerPID, &goaktpb.AdjustRouterPoolSize{
                PoolSize: 2,
            })
        } else if metrics.AvgLatency < a.targetLatency/2 && currentSize > a.minWorkers {
            // Scale down
            ctx.Tell(a.routerPID, &goaktpb.AdjustRouterPoolSize{
                PoolSize: -1,
            })
        }
    }
}
```

### Pattern 5: Circuit Breaker with Router

```go
type CircuitBreakerRouter struct {
    routerPID       *actor.PID
    failureCount    int
    failureThreshold int
    circuitOpen     bool
}

func (a *CircuitBreakerRouter) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ProcessRequest:
        if a.circuitOpen {
            ctx.Response(&RequestRejected{Reason: "Circuit open"})
            return
        }
        
        // Forward to router
        payload, _ := anypb.New(msg)
        ctx.Tell(a.routerPID, &goaktpb.Broadcast{Message: payload})
        
    case *WorkerFailure:
        a.failureCount++
        if a.failureCount >= a.failureThreshold {
            a.circuitOpen = true
            ctx.Logger().Warn("Circuit breaker opened")
            
            // Schedule reset
            _ = ctx.ActorSystem().ScheduleOnce(ctx.Context(), &ResetCircuit{}, ctx.Self(), 30*time.Second)
        }
        
    case *ResetCircuit:
        a.circuitOpen = false
        a.failureCount = 0
        ctx.Logger().Info("Circuit breaker reset")
    }
}
```

## Best Practices

### Do's ✅

1. **Choose appropriate strategy** for your use case
2. **Configure supervision** for fault tolerance
3. **Use RoundRobin** for uniform workloads
4. **Use FanOut** for broadcasting
5. **Use ScatterGather** for redundancy
6. **Monitor routee health** and scale accordingly
7. **Set appropriate pool sizes** based on load

```go
// Good: Comprehensive router configuration
routerPID, _ := actorSystem.SpawnRouter(ctx, "api-workers",
    10,
    &APIWorker{},
    actor.WithRoutingStrategy(actor.RoundRobinRouting),
    actor.WithRestartRouteeOnFailure(3, time.Minute))
```

### Don'ts ❌

1. **Don't use routers for stateful actors** (state won't be shared)
2. **Don't forget to wrap messages in Broadcast**
3. **Don't use FanOut for high-frequency messages** (resource intensive)
4. **Don't ignore routee failures**
5. **Don't over-provision** routees unnecessarily

```go
// Bad: Sending unwrapped message
actor.Tell(ctx, routerPID, &WorkItem{}) // ❌ Won't be routed

// Good: Wrapped in Broadcast
payload, _ := anypb.New(&WorkItem{})
actor.Tell(ctx, routerPID, &goaktpb.Broadcast{Message: payload}) // ✅
```

## Strategy Comparison

| Strategy          | Distribution | Response   | Use Case            | Overhead |
|-------------------|--------------|------------|---------------------|----------|
| **RoundRobin**    | Sequential   | No wait    | Load balancing      | Low      |
| **Random**        | Random       | No wait    | Simple distribution | Low      |
| **FanOut**        | All routees  | No wait    | Broadcasting        | High     |
| **ScatterGather** | All routees  | First wins | Fastest result      | High     |
| **TailChopping**  | Sequential   | First wins | Controlled latency  | Medium   |

## Performance Considerations

- **Pool size**: Balance between parallelism and resource usage
- **Routing overhead**: RoundRobin/Random < TailChopping < ScatterGather/FanOut
- **Message frequency**: High frequency → avoid FanOut
- **Routee work duration**: Long tasks → larger pool
- **Memory**: Each routee consumes memory
- **Supervision**: Restart has higher overhead than Resume

### Recommended Pool Sizes

```
Light workload:    5-10 routees
Medium workload:   10-50 routees
Heavy workload:    50-100 routees
Very heavy:        100+ routees (monitor carefully)
```

## Router Limitations

1. **No PID returned**: Unlike `Spawn`, routers don't return PID
2. **Not relocatable**: Routers aren't moved across cluster nodes
3. **Homogeneous routees**: All routees must be same type
4. **No direct routee access**: Must go through router
5. **Fire-and-forget**: Standard strategies don't wait for responses

## Testing

```go
func TestRouter(t *testing.T) {
    ctx := context.Background()
    system, _ := actor.NewActorSystem("test",
        actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()))
    system.Start(ctx)
    defer system.Stop(ctx)
    
    // Create router
    routerPID, err := system.SpawnRouter(ctx, "test-router",
        3,
        &TestWorker{},
        actor.WithRoutingStrategy(actor.RoundRobinRouting))
    
    assert.NoError(t, err)
    assert.NotNil(t, routerPID)
    
    // Send messages
    for i := 0; i < 10; i++ {
        msg := &TestMessage{Id: i}
        payload, _ := anypb.New(msg)
        actor.Tell(ctx, routerPID, &goaktpb.Broadcast{Message: payload})
    }
    
    // Wait and verify
    time.Sleep(500 * time.Millisecond)
    
    // Check routees
    resp, _ := actor.Ask(ctx, routerPID, &goaktpb.GetRoutees{}, time.Second)
    routees := resp.(*goaktpb.Routees)
    assert.Equal(t, 3, len(routees.GetNames()))
}
```

## Summary

- **Routers** distribute messages across worker pools
- **Five strategies**: RoundRobin, Random, FanOut, ScatterGather, TailChopping
- **Supervision** handles routee failures (Stop, Restart, Resume)
- **Dynamic sizing** allows scaling pool up/down
- **Messages** must be wrapped in `Broadcast`
- **Choose strategy** based on your use case
- **Monitor and scale** based on load

## Next Steps

- **[Spawning Actors](spawn.md)**: Learn about SpawnRouter
- **[Supervision](supervision.md)**: Fault tolerance strategies
- **[Messaging](messaging.md)**: Message patterns
- **[Overview](overview.md)**: Actor fundamentals
