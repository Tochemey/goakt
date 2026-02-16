# Actor Relocation

Actor relocation is GoAkt's automatic mechanism for moving actors to healthy nodes when their host node fails or leaves the cluster. This ensures high availability and continuous operation of distributed actors.

## Overview

When a node leaves the cluster (gracefully or due to failure), actors hosted on that node need to be recreated elsewhere. GoAkt's relocation system:

- **Automatically detects** node departures via cluster membership events
- **Redistributes actors** to remaining healthy nodes
- **Maintains placement rules** (roles, data center constraints)
- **Preserves actor identity** (name, type, configuration)
- **Updates routing tables** so existing references continue to work

## How Relocation Works

### The Relocation Process

```
1. Node Failure Detected
   ↓
2. Cluster Leader Notified (NodeLeft Event)
   ↓
3. Leader Queries Cluster State for Departed Node's Actors
   ↓
4. Actors Are Allocated Across Remaining Nodes
   ↓
5. Relocator System Actor Spawns Actors on Target Nodes
   ↓
6. Cluster Routing Table Is Updated
   ↓
7. Actors Are Operational on New Nodes
```

### Step-by-Step Example

**Initial State:**

```
Node1 (leader): actor-A, actor-B
Node2: actor-C, actor-D, actor-E
Node3: actor-F
```

**Node2 Crashes:**

1. **Detection**: Cluster detects Node2 heartbeat timeout
2. **Event**: `NodeLeft` event is published
3. **Query**: Leader queries cluster state and finds `[actor-C, actor-D, actor-E]` were on Node2
4. **Allocation**: Leader allocates actors across remaining nodes:
   - Node1 gets: `actor-C`, `actor-D`
   - Node3 gets: `actor-E`
5. **Spawning**:
   - Node1's relocator spawns `actor-C` and `actor-D` locally
   - Node3's relocator receives remote spawn request for `actor-E`
6. **Registration**: New actors are registered in cluster routing table
7. **Complete**: All actors are operational with new addresses

**Final State:**

```
Node1 (leader): actor-A, actor-B, actor-C, actor-D
Node3: actor-F, actor-E
```

## Relocation Configuration

### System-Level: Enable/Disable Relocation

By default, relocation is enabled. Disable it system-wide with `WithoutRelocation()`:

```go
system, err := actor.NewActorSystem(
    "my-system",
    actor.WithCluster(clusterConfig),
    actor.WithRemote("localhost", 3321),
    actor.WithoutRelocation(), // Disable all relocation
)
```

**When to disable:**

- Testing scenarios where you want predictable placement
- Stateful actors that cannot survive relocation
- Custom relocation logic is implemented

### Actor-Level: Opt-Out Individual Actors

Prevent specific actors from being relocated using `WithRelocationDisabled()`:

```go
pid, err := system.Spawn(
    ctx,
    "stateful-actor",
    new(StatefulActor),
    actor.WithRelocationDisabled(), // This actor won't be relocated
)
```

**When to use:**

- Actors with local state that cannot be transferred
- Actors bound to specific hardware/resources
- Actors that must terminate on node failure

## Relocation Strategies

### Round-Robin Allocation

Actors from the failed node are distributed evenly across remaining nodes:

```go
// If Node2 had 10 actors and 2 nodes remain:
// Node1 gets: 5 actors (including those the leader keeps locally)
// Node3 gets: 5 actors (sent via remote spawn)
```

This is the default and only built-in strategy.

### Considerations for Custom Strategies

While GoAkt uses round-robin by default, you can implement custom allocation logic by:

1. Creating a custom relocator actor
2. Subscribing to `NodeLeft` events
3. Implementing your own allocation algorithm
4. Spawning actors according to your policy

## Relocation and Actor State

### Stateless Actors

Stateless actors relocate seamlessly:

```go
type StatelessWorker struct{}

func (w *StatelessWorker) Receive(ctx *actor.ReceiveContext) {
    // No state - relocation is transparent
    switch msg := ctx.Message().(type) {
    case *WorkRequest:
        result := processWork(msg)
        ctx.Response(result)
    }
}
```

### Stateful Actors with Persistence

Stateful actors should persist state externally for successful relocation:

```go
type StatefulWorker struct {
    state *WorkerState
    db    StateStore
}

func (w *StatefulWorker) PreStart(ctx *actor.Context) error {
    // Restore state on start (including after relocation)
    w.db = ctx.Dependencies()[0].(StateStore)
    state, err := w.db.Load(ctx.ActorName())
    if err != nil {
        return err
    }
    w.state = state
    ctx.Logger().Info("State restored after relocation")
    return nil
}

func (w *StatefulWorker) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *UpdateState:
        w.state.Update(msg)
        // Persist immediately
        if err := w.db.Save(ctx.Self().Name(), w.state); err != nil {
            ctx.Err(err)
            return
        }
        ctx.Response(&UpdateStateResponse{Success: true})
    }
}

func (w *StatefulWorker) PostStop(ctx *actor.Context) error {
    // Final persist before shutdown
    return w.db.Save(ctx.ActorName(), w.state)
}
```

### Stateful Actors Without Persistence

Actors with ephemeral state lose their state on relocation:

```go
type EphemeralCache struct {
    cache map[string]interface{}
}

func (c *EphemeralCache) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *CacheGet:
        // After relocation, cache is empty
        value, exists := c.cache[msg.Key]
        ctx.Response(&CacheGetResponse{Value: value, Found: exists})
    case *CacheSet:
        c.cache[msg.Key] = msg.Value
        // This state is lost if the actor relocates
        ctx.Response(&CacheSetResponse{Success: true})
    }
}
```

**Solutions:**

1. Use `WithRelocationDisabled()` if state loss is unacceptable
2. Implement external persistence
3. Accept state loss and rebuild from source
4. Use grains (virtual actors) with automatic rehydration

## Relocation Behavior by Actor Type

### Regular Actors

Regular actors spawned with `Spawn()` or `SpawnOn()` are relocatable by default:

```go
// This actor will be relocated if its node fails
pid, err := system.Spawn(ctx, "worker-1", new(WorkerActor))

// This actor will NOT be relocated
pid, err := system.Spawn(
    ctx,
    "worker-2",
    new(WorkerActor),
    actor.WithRelocationDisabled(),
)
```

### Singleton Actors

Singleton actors are always relocated to maintain the single-instance guarantee:

```go
// Singletons are always relocated to the new leader
err := system.SpawnSingleton(ctx, "coordinator", new(CoordinatorActor))
```

When the leader hosting a singleton fails:

1. New leader is elected
2. Singleton is automatically relocated to the new leader
3. Singleton resumes operation with the same name

### Grains (Virtual Actors)

Grains are designed for automatic relocation:

```go
// Grains are activated on-demand and relocated automatically
grain, err := system.ActivateGrain(ctx, "user-123", new(UserGrain))
```

Grains handle relocation via:

- Identity-based placement
- Automatic deactivation/reactivation
- Built-in state persistence hooks

### Routers

Router actors are not directly relocated. Instead:

1. The router itself can be relocated if it's a named actor
2. Routees (worker actors) are relocated independently
3. The router discovers new routee locations via the routing table

```go
// Router and its routees are relocated independently
err := system.SpawnRouter(ctx, "worker-pool", routerConfig)
```

## Relocation and Dependencies

Actors with dependencies are relocated with their dependencies intact:

```go
// Spawn actor with dependencies
pid, err := system.Spawn(
    ctx,
    "data-processor",
    new(DataProcessorActor),
    actor.WithDependencies(dbClient, httpClient),
)
```

**Relocation behavior:**

1. The cluster routing table stores dependency metadata
2. When relocating, the relocator reconstructs the actor with the same dependencies
3. Dependencies must be serializable and registered on all nodes

**Best practices:**

- Use shared infrastructure (databases, clients) as dependencies
- Ensure dependencies can be recreated on any node
- Register dependency types in all nodes' actor systems

## Relocation and Supervision

Actors with custom supervisors are relocated with their supervision strategy:

```go
import "github.com/tochemey/goakt/v3/supervisor"

pid, err := system.Spawn(
    ctx,
    "critical-worker",
    new(WorkerActor),
    actor.WithSupervisor(
        supervisor.NewSupervisor(
            supervisor.WithStrategy(supervisor.OneForOneStrategy),
            supervisor.WithAnyErrorDirective(supervisor.RestartDirective),
            supervisor.WithRetry(3, time.Second),
        ),
    ),
)
```

**Relocation behavior:**

1. Supervision strategy is preserved in cluster metadata
2. Actor is recreated with the same supervision policy
3. Parent-child relationships are reconstructed if possible

## Monitoring Relocation

### Cluster Events

Subscribe to cluster events to track relocations:

```go
subscriber, err := system.Subscribe()
if err != nil {
    log.Fatal(err)
}
defer system.Unsubscribe(subscriber)

for event := range subscriber.Iterator() {
    switch e := event.(type) {
    case *goaktpb.NodeLeft:
        log.Printf("Node left: %s - relocation will occur\n", e.GetAddress())
    case *goaktpb.ActorStarted:
        // May indicate actor was relocated
        log.Printf("Actor started: %s\n", e.GetAddress())
    }
}
```

### Metrics

Monitor relocation metrics (if using `WithMetrics()`):

- Actor relocation count
- Relocation latency
- Failed relocation attempts
- Actors per relocation event

## Edge Cases and Failure Scenarios

### Relocation During Network Partition

**Scenario**: Network partition causes nodes to see each other as failed

**Behavior**:

- Each partition attempts to relocate actors from "failed" nodes
- Temporary duplicate actors may exist in different partitions

**Resolution**:

- When partition heals, consistent hashing resolves duplicates
- Latest registration wins in the routing table

**Mitigation**:

- Use majority quorums to reduce false node-left detections
- Monitor network health and partition events

### Relocation Target Node Fails

**Scenario**: Node chosen for relocation fails before actor is spawned

**Behavior**:

- Relocation fails for that actor
- Cluster will retry relocation in the next rebalancing cycle

**Mitigation**:

- Ensure sufficient healthy nodes for relocation
- Monitor relocation success rates

### Cascading Failures

**Scenario**: Multiple nodes fail rapidly

**Behavior**:

- Multiple relocation cycles occur
- Actors may be relocated multiple times
- Increased cluster load during stabilization

**Mitigation**:

- Increase cluster size for redundancy
- Monitor node health to detect cascading issues early
- Use circuit breakers for actor operations during instability

### Actor Kind Not Registered on Target

**Scenario**: Relocator tries to spawn an actor on a node without the actor kind registered

**Behavior**:

- Spawn fails with `ErrTypeNotRegistered`
- Actor remains unavailable until a suitable node joins

**Mitigation**:

- Register all actor kinds on all nodes with `WithKinds()`
- Use role-based placement if only specific nodes should host certain actors

## Best Practices

### Design for Relocation

1. **Externalize state**: Use databases or caches for actor state
2. **Idempotent operations**: Design message handlers to be idempotent
3. **No local resources**: Avoid binding to node-specific resources (files, hardware)
4. **Implement PreStart/PostStop**: Handle state restoration and cleanup

### Configuration

1. **Enable relocation by default**: Only disable when absolutely necessary
2. **Register all kinds**: Use `WithKinds()` to register actor types cluster-wide
3. **Set appropriate quorums**: Prevent false node-left events due to transient issues
4. **Use roles for dedicated actors**: Pin critical actors to specific node roles

### Testing

1. **Simulate node failures**: Test relocation in development
2. **Verify state recovery**: Ensure actors restore state correctly
3. **Measure relocation time**: Track how long relocation takes
4. **Test cascading failures**: Verify behavior when multiple nodes fail

### Operations

1. **Monitor cluster events**: Track `NodeLeft` and relocation activity
2. **Alert on relocation failures**: Set up alerts for failed relocations
3. **Plan for capacity**: Ensure remaining nodes can handle relocated actors
4. **Graceful shutdowns**: Use coordinated shutdown to minimize relocation

## Disabling Relocation (Advanced)

### System-Wide Disable

```go
system, err := actor.NewActorSystem(
    "my-system",
    actor.WithCluster(clusterConfig),
    actor.WithRemote("localhost", 3321),
    actor.WithoutRelocation(),
)
```

**Implications:**

- Actors on failed nodes are permanently lost
- Manual intervention required to restore actors
- Useful for testing or very specific use cases

### Per-Actor Disable

```go
pid, err := system.Spawn(
    ctx,
    "local-only-actor",
    new(LocalActor),
    actor.WithRelocationDisabled(),
)
```

**Implications:**

- This actor will not be relocated on node failure
- Actor is lost if its node fails
- Useful for actors with non-transferable local state or resources

## Troubleshooting

### Actors Not Relocating

**Symptoms:**

- Actors remain unavailable after node failure
- No relocation logs

**Possible Causes:**

- Relocation disabled system-wide or per-actor
- No healthy nodes available to host relocated actors
- Actor kind not registered on remaining nodes
- Insufficient cluster quorum

**Solutions:**

- Verify relocation is enabled
- Check that actor kinds are registered with `WithKinds()`
- Ensure sufficient healthy nodes in cluster
- Review minimum quorum settings

### Actors Relocated Multiple Times

**Symptoms:**

- Actors repeatedly moving between nodes
- High relocation frequency in logs

**Possible Causes:**

- Unstable cluster membership (nodes repeatedly joining/leaving)
- Network issues causing false node-left events
- Very low heartbeat timeouts

**Solutions:**

- Investigate node health and stability
- Increase heartbeat and timeout intervals
- Review network quality between nodes
- Check node resource utilization (may be crashing due to OOM)

### State Loss After Relocation

**Symptoms:**

- Actors lose in-memory state after relocation
- Actors behave as if freshly started

**Expected Behavior:**

- In-memory state is not preserved during relocation
- Actors must restore state in `PreStart()`

**Solutions:**

- Implement external state persistence
- Use databases or distributed caches for state
- Load state in `PreStart()` lifecycle hook
- Consider using grains with built-in state management

### Relocation Performance Issues

**Symptoms:**

- Cluster takes long time to stabilize after node failure
- High latency during relocation

**Possible Causes:**

- Large number of actors to relocate
- High network latency
- Insufficient resources on target nodes

**Solutions:**

- Reduce number of actors per node
- Optimize actor spawn time
- Increase cluster size for better distribution
- Monitor and scale cluster resources

## Next Steps

- [Cluster Overview](overview.md): Learn about clustering fundamentals
- [Cluster Singleton](cluster_singleton.md): Understand singleton relocation behavior
- [Discovery Providers](discovery.md): Configure cluster discovery
- [Actor Lifecycle](../actors/overview.md): Learn about PreStart and PostStop hooks
