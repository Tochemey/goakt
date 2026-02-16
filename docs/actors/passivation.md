# Passivation

Passivation is an automatic resource management feature that stops idle actors to free up memory and resources. This is particularly useful for systems with many actors where only a subset is actively processing messages at any given time.

## Table of Contents

- 🤔 [What is Passivation?](#what-is-passivation)
- 💡 [Why Use Passivation?](#why-use-passivation)
- ⚙️ [Configuration](#configuration)
- 🔄 [How Passivation Works](#how-passivation-works)
- 💡 [Basic Example](#basic-example)
- 📋 [Use Cases](#use-cases)
- 💾 [Passivation with State Persistence](#passivation-with-state-persistence)
- 📊 [Monitoring Passivation](#monitoring-passivation)
- ✅ [Best Practices](#best-practices)
- 🔀 [Passivation vs. Shutdown](#passivation-vs-shutdown)
- 🧪 [Testing Passivation](#testing-passivation)
- ⚡ [Performance Considerations](#performance-considerations)
- 📋 [Summary](#summary)
- ➡️ [Next Steps](#next-steps)

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

## Basic Example

```go
type SessionActor struct {
    userId    string
    sessionId string
    data      map[string]interface{}
}

func (a *SessionActor) PreStart(ctx *actor.Context) error {
    ctx.ActorSystem().Logger().Info("Session started",
        "user_id", a.userId,
        "session_id", a.sessionId)

    // Initialize session data
    a.data = make(map[string]interface{})
    return nil
}

func (a *SessionActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *UpdateSession:
        a.data[msg.GetKey()] = msg.GetValue()
        ctx.Response(&SessionUpdated{})

    case *GetSession:
        ctx.Response(&SessionData{Data: a.data})
    }
}

func (a *SessionActor) PostStop(ctx *actor.Context) error {
    ctx.ActorSystem().Logger().Info("Session ended (passivated)",
        "user_id", a.userId,
        "session_id", a.sessionId)

    // Save session data before passivation
    if err := a.saveSessionData(); err != nil {
        ctx.ActorSystem().Logger().Error("Failed to save session data", "error", err)
        return err
    }

    return nil
}

// Usage
func main() {
    ctx := context.Background()
    actorSystem, _ := actor.NewActorSystem("SessionSystem")
    actorSystem.Start(ctx)
    defer actorSystem.Stop(ctx)

    // Spawn session actor with time-based passivation (5 min inactivity)
    pid, _ := actorSystem.Spawn(ctx, "session-123", &SessionActor{
        userId:    "user-456",
        sessionId: "session-123",
    }, actor.WithPassivationStrategy(passivation.NewTimeBasedStrategy(5*time.Minute)))

    // Actor will be automatically passivated after 5 minutes of inactivity
}
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

## Monitoring Passivation

### Logging

```go
func (a *MyActor) PostStop(ctx *actor.Context) error {
    ctx.ActorSystem().Logger().Info("Actor passivated",
        "actor_name", ctx.ActorName(),
        "uptime", time.Since(a.startTime))
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

## Testing Passivation

```go
func TestPassivation(t *testing.T) {
    ctx := context.Background()
    system, _ := actor.NewActorSystem("test",
        actor.WithPassivationStrategy(passivation.NewTimeBasedStrategy(100*time.Millisecond)))
    system.Start(ctx)
    defer system.Stop(ctx)

    // Spawn actor
    pid, _ := system.Spawn(ctx, "test", &TestActor{})

    // Send message
    actor.Tell(ctx, pid, &TestMessage{})

    // Verify actor is running
    assert.True(t, pid.IsRunning())

    // Wait for passivation
    time.Sleep(200 * time.Millisecond)

    // Verify actor was passivated
    assert.False(t, pid.IsRunning())
}
```

## Performance Considerations

- **Memory savings**: Passivation frees memory for inactive actors
- **CPU overhead**: Minimal passivation overhead
- **Startup cost**: Re-spawning has PreStart overhead
- **Thrashing**: Too short timeout causes frequent passivation/spawn

### Optimal Timeout Selection

```
Short timeout (< 1 minute):
- High memory savings
- Higher CPU overhead
- Suitable for: caches, temporary sessions

Medium timeout (5-15 minutes):
- Balanced approach
- Good for most use cases
- Suitable for: user sessions, connections

Long timeout (> 30 minutes):
- Low CPU overhead
- Lower memory savings
- Suitable for: critical actors, frequently accessed
```

## Summary

- **Passivation** automatically stops idle actors
- **Configure** with `WithPassivationStrategy()` (e.g. `passivation.NewTimeBasedStrategy(d)` or `passivation.NewLongLivedStrategy()`)
- **PostStop** is called for cleanup
- **Persist state** before passivation
- **Use for** sessions, caches, connections, virtual actors
- **Monitor** with logging and metrics

## Next Steps

- **[Supervision](supervision.md)**: Fault tolerance with passivation
- **[Behaviors](behaviours.md)**: State machines with passivation
- **[Dependencies](dependencies.md)**: Inject dependencies for testing
- **[Overview](overview.md)**: Actor lifecycle and fundamentals
