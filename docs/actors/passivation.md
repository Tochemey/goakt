# Passivation

Passivation is an automatic resource management feature that stops idle actors to free up memory and resources. This is particularly useful for systems with many actors where only a subset is actively processing messages at any given time.

## Table of Contents

- 🤔 [What is Passivation?](#what-is-passivation)
- 💡 [Why Use Passivation?](#why-use-passivation)
- ⚙️ [Configuration](#configuration)
- 🔄 [How Passivation Works](#how-passivation-works)
- 📋 [Use Cases](#use-cases)
- 💾 [Passivation with State Persistence](#passivation-with-state-persistence)
- ✅ [Best Practices](#best-practices)
- 🔀 [Passivation vs. Shutdown](#passivation-vs-shutdown)
- 🎯 [System-Level Eviction](#system-level-eviction)
- 📋 [Summary](#summary)

---

## What is Passivation?

**Passivation** automatically stops actors that have been idle for a configured period. When an actor is passivated:

- Its `PostStop` hook is called for cleanup
- All resources are released
- Memory is freed
- The actor is removed from the system

If a message arrives for a passivated actor, it will be dead-lettered (the actor won't be automatically reactivated).

## Why Use Passivation?

Passivation is beneficial for:

- **Memory management**: Free resources from idle actors
- **Scalability**: Support large numbers of actors
- **Resource efficiency**: Only active actors consume resources
- **Automatic cleanup**: No manual lifecycle management
- **Virtual actor patterns**: Similar to Orleans grains

## Configuration

### Per-Actor Passivation

Configure passivation when spawning an actor. Use a **strategy** (time-based, message-count, or long-lived):

```go
import "github.com/tochemey/goakt/v3/passivation"

// Time-based: passivate after 5 minutes of inactivity
pid, err := actorSystem.Spawn(ctx, "my-actor", &MyActor{},
    actor.WithPassivationStrategy(passivation.NewTimeBasedStrategy(5*time.Minute)))

// Message-count: passivate after processing N messages
pid, err := actorSystem.Spawn(ctx, "my-actor", &MyActor{},
    actor.WithPassivationStrategy(passivation.NewMessageCountBasedStrategy(100)))

// Long-lived: never passivate (effective "disable")
pid, err := actorSystem.Spawn(ctx, "my-actor", &MyActor{},
    actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()))
```

## How Passivation Works

1. **Idle detection**: Actor has no messages for configured period
2. **Passivation triggered**: System decides to stop actor
3. **PostStop called**: Actor cleanup hook is invoked
4. **Resources released**: Memory and resources freed
5. **Actor removed**: Actor is removed from system

### Passivation Timer

The passivation timer:

- **Resets** on every message received
- **Starts** after actor finishes processing a message
- **Triggers** only after idle period

```
Message → Process → Idle Start → Timer → Passivate
         Reset Timer
```

## Use Cases

### 1. Session Management

```go
type UserSessionActor struct {
    userId       string
    lastActivity time.Time
    cart         *ShoppingCart
}

func (a *UserSessionActor) PreStart(ctx *actor.Context) error {
    // Load session from database
    session, err := loadUserSession(a.userId)
    if err != nil {
        return err
    }

    a.cart = session.Cart
    a.lastActivity = time.Now()
    return nil
}

func (a *UserSessionActor) Receive(ctx *actor.ReceiveContext) {
    a.lastActivity = time.Now()

    switch msg := ctx.Message().(type) {
    case *AddToCart:
        a.cart.AddItem(msg.GetItem())
        ctx.Response(&CartUpdated{})

    case *GetCart:
        ctx.Response(&Cart{Items: a.cart.Items})
    }
}

func (a *UserSessionActor) PostStop(ctx *actor.Context) error {
    // Persist cart before passivation
    return saveUserSession(a.userId, &Session{
        Cart:         a.cart,
        LastActivity: a.lastActivity,
    })
}

// Spawn with 30-minute session timeout
pid, _ := actorSystem.Spawn(ctx, fmt.Sprintf("session-%s", userId),
    &UserSessionActor{userId: userId},
    actor.WithPassivationStrategy(passivation.NewTimeBasedStrategy(30*time.Minute)))
```

### 2. Cache Entries

```go
type CacheEntryActor struct {
    key   string
    value []byte
    hits  int64
}

func (a *CacheEntryActor) PreStart(ctx *actor.Context) error {
    ctx.ActorSystem().Logger().Debug("Cache entry loaded", "key", a.key)
    a.hits = 0
    return nil
}

func (a *CacheEntryActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *GetValue:
        a.hits++
        ctx.Response(&Value{Data: a.value})

    case *UpdateValue:
        a.value = msg.GetData()
        ctx.Response(&ValueUpdated{})
    }
}

func (a *CacheEntryActor) PostStop(ctx *actor.Context) error {
    ctx.ActorSystem().Logger().Debug("Cache entry evicted",
        "key", a.key,
        "hits", a.hits)
    return nil
}

// Spawn with LRU-style passivation
pid, _ := actorSystem.Spawn(ctx, fmt.Sprintf("cache-%s", key),
    &CacheEntryActor{key: key, value: value},
    actor.WithPassivationStrategy(passivation.NewTimeBasedStrategy(5*time.Minute)))
```

### 3. Connection Actors

```go
type ConnectionActor struct {
    clientId   string
    connection net.Conn
}

func (a *ConnectionActor) PreStart(ctx *actor.Context) error {
    // Establish connection
    conn, err := dialClient(a.clientId)
    if err != nil {
        return err
    }

    a.connection = conn
    ctx.ActorSystem().Logger().Info("Connection established", "client_id", a.clientId)
    return nil
}

func (a *ConnectionActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *SendData:
        if _, err := a.connection.Write(msg.GetData()); err != nil {
            ctx.Err(err)
            return
        }
        ctx.Response(&DataSent{})
    }
}

func (a *ConnectionActor) PostStop(ctx *actor.Context) error {
    ctx.ActorSystem().Logger().Info("Connection closing", "client_id", a.clientId)

    if a.connection != nil {
        return a.connection.Close()
    }
    return nil
}

// Passivate idle connections after 2 minutes
pid, _ := actorSystem.Spawn(ctx, fmt.Sprintf("conn-%s", clientId),
    &ConnectionActor{clientId: clientId},
    actor.WithPassivationStrategy(passivation.NewTimeBasedStrategy(2*time.Minute)))
```

### 4. Virtual Actors (Grain-like Pattern)

```go
type OrderActor struct {
    orderId string
    state   string
    items   []string
}

func (a *OrderActor) PreStart(ctx *actor.Context) error {
    // Load order from database
    order, err := loadOrder(a.orderId)
    if err != nil {
        // Order doesn't exist, create new
        a.state = "new"
        a.items = []string{}
        return nil
    }

    a.state = order.State
    a.items = order.Items
    return nil
}

func (a *OrderActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *AddItem:
        a.items = append(a.items, msg.GetItem())
        ctx.Response(&ItemAdded{})

    case *GetOrder:
        ctx.Response(&Order{
            Id:    a.orderId,
            State: a.state,
            Items: a.items,
        })
    }
}

func (a *OrderActor) PostStop(ctx *actor.Context) error {
    // Persist order state
    return saveOrder(&Order{
        Id:    a.orderId,
        State: a.state,
        Items: a.items,
    })
}

// Get or create order actor (virtual actor pattern)
func getOrCreateOrderActor(ctx context.Context, system actor.ActorSystem,
    orderId string) (*actor.PID, error) {

    actorName := fmt.Sprintf("order-%s", orderId)

    // Try to get existing actor (returns addr, pid, err)
    _, pid, err := system.ActorOf(ctx, actorName)
    if err == nil && pid != nil {
        return pid, nil
    }

    // Spawn new actor with passivation
    return system.Spawn(ctx, actorName, &OrderActor{orderId: orderId},
        actor.WithPassivationStrategy(passivation.NewTimeBasedStrategy(10*time.Minute)))
}
```

## Passivation with State Persistence

Best practice is to persist state in `PostStop`:

```go
type StatefulActor struct {
    id    string
    state *ActorState
    db    *Database
}

func (a *StatefulActor) PreStart(ctx *actor.Context) error {
    // Initialize database connection
    db, err := connectDB()
    if err != nil {
        return err
    }
    a.db = db

    // Load persisted state
    state, err := a.db.LoadState(a.id)
    if err != nil {
        // No persisted state, use defaults
        a.state = &ActorState{
            Counter: 0,
            Items:   []string{},
        }
    } else {
        a.state = state
    }

    return nil
}

func (a *StatefulActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *IncrementCounter:
        a.state.Counter++
        ctx.Response(&CounterValue{Value: a.state.Counter})

    case *AddItem:
        a.state.Items = append(a.state.Items, msg.GetItem())
        ctx.Response(&ItemAdded{})
    }
}

func (a *StatefulActor) PostStop(ctx *actor.Context) error {
    ctx.ActorSystem().Logger().Info("Persisting state before passivation", "id", a.id)

    // Persist state to database
    if err := a.db.SaveState(a.id, a.state); err != nil {
        ctx.ActorSystem().Logger().Error("Failed to persist state", "error", err)
        return err
    }

    // Close database connection
    if err := a.db.Close(); err != nil {
        return err
    }

    return nil
}
```

### Metrics

For system and per-actor metrics (including lifecycle), see [Observability — Metrics](../observability/metrics.md). Use the events stream to observe actor stopped/passivated events; see [Events Stream](../events_stream/overview.md).

## Best Practices

### Do's ✅

1. **Persist state in PostStop**: Save important data before passivation
2. **Clean up resources**: Close connections, release locks
3. **Use appropriate timeouts**: Balance memory vs. latency
4. **Log passivation**: Track actor lifecycle
5. **Handle PostStop errors**: Log and handle gracefully

```go
func (a *MyActor) PostStop(ctx *actor.Context) error {
    // Clean up all resources
    if err := a.connection.Close(); err != nil {
        ctx.ActorSystem().Logger().Error("Failed to close connection", "error", err)
    }

    // Persist state
    if err := a.saveState(); err != nil {
        ctx.ActorSystem().Logger().Error("Failed to save state", "error", err)
        return err
    }

    return nil
}
```

### Don'ts ❌

1. **Don't lose data**: Always persist important state
2. **Don't leave resources open**: Clean up in PostStop
3. **Don't set too short timeouts**: Causes thrashing
4. **Don't forget error handling**: PostStop can fail
5. **Don't block PostStop**: Keep it fast

## Passivation vs. Shutdown

### Passivation

- **Automatic**: Triggered by inactivity
- **Resource management**: Frees memory
- **Transparent**: Happens in background
- **PostStop called**: Cleanup hook invoked

### Shutdown

- **Manual**: Explicitly called
- **Actor termination**: Permanent stop
- **Intentional**: Part of logic
- **PostStop called**: Cleanup hook invoked

```go
// Passivation (automatic)
// Actor idle for configured period → passivated

// Shutdown (manual)
ctx.Shutdown() // In Receive
pid.Shutdown(ctx) // External
```

## System-Level Eviction

While per-actor passivation strategies manage individual actor lifecycles based on idle time or message count, GoAkt also provides a **system-level eviction mechanism** that controls the total number of active actors across the entire actor system. This is particularly useful for managing memory pressure when dealing with large numbers of actors.

### What is System Eviction?

System eviction is a global memory management strategy that monitors the total number of active actors and automatically passivates actors when the count exceeds a configured limit. Unlike per-actor passivation which is based on individual actor inactivity, system eviction makes decisions based on the overall system state and actor usage patterns.

### How System Eviction Works

The eviction mechanism operates on a periodic interval and follows these steps:

1. **Monitoring**: The system periodically checks the total number of active actors
2. **Threshold Detection**: When the count exceeds the configured limit, eviction is triggered
3. **Actor Selection**: Actors are selected for passivation based on the chosen eviction policy
4. **Passivation**: Selected actors are gracefully shut down (PostStop is called)
5. **Resource Release**: Memory and resources are freed as actors are removed

### Eviction Policies

The system supports three eviction policies that determine which actors to passivate when the limit is exceeded:

**LRU (Least Recently Used)**
- Passivates actors that have not been accessed for the longest time
- Suitable for scenarios where recently active actors are more likely to be needed again
- Prioritizes keeping "hot" actors in memory
- Effective for caching patterns and session management

**LFU (Least Frequently Used)**
- Passivates actors that have been used the least number of times
- Retains actors that are consistently accessed, even if not recently
- Good for scenarios where usage frequency indicates importance
- Useful when popular actors should remain active

**MRU (Most Recently Used)**
- Passivates actors that were accessed most recently
- Less common but useful in specific scenarios
- Effective when fresh data should be cycled out quickly
- Can be useful for certain cache invalidation patterns

### Key Concepts

**Limit**: The maximum number of active actors allowed before eviction begins. This acts as a memory ceiling for the system.

**Interval**: How frequently the eviction engine evaluates actor counts and triggers passivation. Shorter intervals provide more aggressive memory management but consume more system resources for evaluation.

**Percentage**: When the limit is exceeded, this determines what percentage of actors to passivate. For example, with a 50% setting and 1000 actors exceeding a limit of 700, approximately 150 actors would be passivated (50% of the 300 actors over the limit).

### System Eviction vs. Per-Actor Passivation

These two mechanisms can work together complementarily:

**System Eviction**:
- **Global scope**: Manages total actor count across the system
- **Memory-driven**: Triggered by overall system memory concerns
- **Policy-based**: Uses usage patterns (LRU, LFU, MRU) to select victims
- **System-level**: Configured at actor system creation
- **Periodic**: Runs at configured intervals

**Per-Actor Passivation**:
- **Individual scope**: Each actor manages its own lifecycle
- **Time or message-driven**: Based on inactivity period or message count
- **Idle detection**: Triggers on individual actor inactivity
- **Actor-level**: Configured per actor at spawn time
- **Event-driven**: Triggers when individual conditions are met

**When to Use What**:
- Use **system eviction** when you need to control overall memory usage and have a large number of actors
- Use **per-actor passivation** when actors have natural lifecycle boundaries (sessions, connections, cache entries)
- Use **both together** for comprehensive resource management: per-actor passivation handles idle actors while system eviction provides a safety net for overall memory pressure

### Best Practices for System Eviction

**Choose the Right Policy**:
- Use LRU for session-like actors where recent activity indicates future use
- Use LFU for actors that serve popular resources or frequently accessed data
- Use MRU sparingly and only when your access patterns justify it

**Set Appropriate Limits**:
- Consider available memory and average actor memory footprint
- Leave headroom for actor creation bursts
- Monitor actual actor counts in production to tune the limit

**Balance Interval Timing**:
- Shorter intervals (1-5 seconds) provide tighter memory control but increase overhead
- Longer intervals (10-60 seconds) reduce overhead but allow temporary memory spikes
- Adjust based on actor creation rate and memory constraints

**Configure Appropriate Percentages**:
- Higher percentages (50-70%) provide more aggressive cleanup but may evict actors that will be needed soon
- Lower percentages (10-30%) provide gentler cleanup but may require multiple eviction cycles
- Consider the cost of actor recreation when setting this value

**Combine with Per-Actor Strategies**:
- Use system eviction as a global safety net
- Use per-actor passivation for predictable lifecycle management
- Ensure actors properly persist state in PostStop for both mechanisms

### Monitoring System Eviction

When system eviction is active, you should monitor:
- **Actor count trends**: Watch for patterns that indicate the limit needs adjustment
- **Eviction frequency**: How often the eviction mechanism triggers
- **Actor recreation patterns**: Actors that are repeatedly evicted and recreated may need different handling
- **Memory usage**: Ensure eviction is effectively managing memory pressure
- **System performance**: Balance between memory management and actor recreation overhead

For system and per-actor metrics (including lifecycle), see [Observability — Metrics](../observability/metrics.md). Use the events stream to observe actor stopped/passivated events; see [Events Stream](../events_stream/overview.md).

## Summary

- **Passivation** automatically stops idle actors based on inactivity or message count
- **Configure per-actor** with `WithPassivationStrategy()` (e.g. `passivation.NewTimeBasedStrategy(d)` or `passivation.NewLongLivedStrategy()`)
- **System-level eviction** manages total actor count with policies (LRU, LFU, MRU) when limit is exceeded
- **PostStop** is called for cleanup in both passivation and eviction
- **Persist state** before passivation to avoid data loss
- **Use per-actor passivation for** sessions, caches, connections, virtual actors
- **Use system eviction for** global memory management and actor count control
- **Combine both mechanisms** for comprehensive resource management
- **Monitor** with logging and metrics to tune behavior
