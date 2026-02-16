# Stashing

Stashing is a message deferral mechanism that allows actors to temporarily set aside messages for later processing. This is particularly useful when an actor is not ready to handle certain messages in its current state.

## Table of Contents

- 🤔 [What is Stashing?](#what-is-stashing)
- 💡 [When to Use Stashing](#when-to-use-stashing)
- 🚀 [Basic Usage](#basic-usage)
- 🛠️ [Stashing Operations](#stashing-operations)
- 💡 [Example: Initialization](#example-initialization)
- 💡 [Example: State Transitions with Behaviors](#example-state-transitions-with-behaviors)
- 💡 [Example: Resource Acquisition](#example-resource-acquisition)
- 💡 [Example: Batch Processing](#example-batch-processing)
- 💡 [Example: Conditional Processing](#example-conditional-processing)
- ✅ [Stashing Best Practices](#stashing-best-practices)
- 🧩 [Common Patterns](#common-patterns)
- ⚡ [Performance Considerations](#performance-considerations)
- ⚠️ [Limitations](#limitations)
- ⚠️ [Error Handling](#error-handling)
- 🧪 [Testing Stashing](#testing-stashing)
- 📋 [Summary](#summary)
- ➡️ [Next Steps](#next-steps)

---

## What is Stashing?

**Stashing** allows an actor to:

- Temporarily **buffer messages** for later processing
- **Defer** messages that can't be handled in the current state
- **Replay** stashed messages when ready
- Maintain **message order** when transitioning between states

Think of it as a temporary parking lot for messages that will be processed later.

## When to Use Stashing

Stashing is useful when:

- **Initialization**: Actor is waiting for setup to complete
- **State transitions**: Actor is changing states and can't process certain messages
- **Resource acquisition**: Waiting for a resource before processing
- **Ordering requirements**: Ensuring messages are processed in a specific order
- **Conditional processing**: Deferring messages until a condition is met

## Basic Usage

### Enable Stashing

Stashing must be enabled when spawning an actor:

```go
pid, err := actorSystem.Spawn(ctx, "my-actor", &MyActor{},
    actor.WithStashing())
```

### Stash a Message

Use `ctx.Stash()` to defer the current message:

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ProcessData:
        if !a.ready {
            // Not ready, stash for later
            ctx.Stash()
            return
        }
        // Process normally
        a.processData(msg)
    }
}
```

### Unstash Messages

Use `ctx.Unstash()` or `ctx.UnstashAll()` to process stashed messages:

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Initialize:
        a.initialize(msg)
        a.ready = true

        // Process all stashed messages
        ctx.UnstashAll()

    case *ProcessData:
        if !a.ready {
            ctx.Stash()
            return
        }
        a.processData(msg)
    }
}
```

## Stashing Operations

### Stash

Defer the current message for later processing:

```go
ctx.Stash()
```

- **Adds** current message to stash buffer
- Message is **not processed** now
- Will be processed when unstashed
- **Maintains order**: FIFO

### Unstash

Process the oldest stashed message:

```go
ctx.Unstash()
```

- **Removes** oldest message from stash
- **Prepends** to mailbox (processed next)
- Maintains FIFO order
- Does nothing if stash is empty

### UnstashAll

Process all stashed messages at once:

```go
ctx.UnstashAll()
```

- **Removes all** messages from stash
- **Prepends** to mailbox in order
- Maintains original order
- Efficient for bulk unstashing

## Example: Initialization

Wait for initialization before processing work:

```go
type WorkerActor struct {
    initialized bool
    config      *Config
}

func (a *WorkerActor) PreStart(ctx *actor.Context) error {
    a.initialized = false
    return nil
}

func (a *WorkerActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.PostStart:
        a.config = msg.GetConfig()
        a.initialized = true
        ctx.Logger().Info("Initialized, processing stashed messages")

        // Process all messages that arrived during initialization
        ctx.UnstashAll()
        ctx.Response(&InitializeComplete{})

    case *WorkItem:
        if !a.initialized {
            // Not ready yet, stash for later
            ctx.Stash()
            return
        }

        // Process work item normally
        result := a.processWork(msg, a.config)
        ctx.Response(&WorkResult{Data: result})
    }
}

func (a *WorkerActor) PostStop(ctx *actor.Context) error {
    return nil
}
```

**Flow:**

```
1. WorkItem arrives → stashed (not initialized)
2. WorkItem arrives → stashed (not initialized)
3. Initialize arrives → process, set initialized=true
4. UnstashAll() → process stashed WorkItems
5. WorkItem arrives → process immediately
```

## Example: State Transitions with Behaviors

Combine stashing with behaviors for state machines:

```go
type ConnectionActor struct {
    state      string
    connection *Connection
}

func (a *ConnectionActor) PreStart(ctx *actor.Context) error {
    a.state = "Disconnected"
    return nil
}

// Initial state: Disconnected
func (a *ConnectionActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Connect:
        ctx.Logger().Info("Connecting...")

        // Start async connection
        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            conn, err := dialServer(msg.GetAddress())
            if err != nil {
                return &ConnectFailed{Reason: err.Error()}, nil
            }
            return &ConnectionEstablished{Conn: conn}, nil
        })

        // Switch to connecting state
        ctx.Become(a.connectingBehavior)

    case *SendData:
        ctx.Response(&Error{Message: "Not connected"})
    }
}

// Connecting state
func (a *ConnectionActor) connectingBehavior(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ConnectionEstablished:
        a.connection = msg.Conn
        a.state = "Connected"
        ctx.Logger().Info("Connected, unstashing messages")

        // Switch to connected state
        ctx.Become(a.connectedBehavior)

        // Process messages that arrived during connection
        ctx.UnstashAll()

    case *ConnectFailed:
        ctx.Logger().Error("Connection failed", "reason", msg.Reason)
        ctx.UnBecome() // Back to disconnected

    case *SendData:
        // Can't send yet, stash until connected
        ctx.Stash()

    case *Disconnect:
        // Cancel connection and go back
        ctx.UnBecome()
    }
}

// Connected state
func (a *ConnectionActor) connectedBehavior(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *SendData:
        if err := a.connection.Send(msg.GetData()); err != nil {
            ctx.Response(&SendFailed{Reason: err.Error()})
            return
        }
        ctx.Response(&SendSuccess{})

    case *Disconnect:
        a.connection.Close()
        ctx.UnBecome() // Back to disconnected
    }
}

func (a *ConnectionActor) PostStop(ctx *actor.Context) error {
    if a.connection != nil {
        return a.connection.Close()
    }
    return nil
}
```

## Example: Resource Acquisition

Wait for a resource before processing:

```go
type DatabaseActor struct {
    db     *sql.DB
    hasDB  bool
}

func (a *DatabaseActor) PreStart(ctx *actor.Context) error {
    // Start async DB connection
    ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
        db, err := sql.Open("postgres", connectionString)
        if err != nil {
            return nil, err
        }
        return &DatabaseReady{DB: db}, nil
    })

    a.hasDB = false
    return nil
}

func (a *DatabaseActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *DatabaseReady:
        a.db = msg.DB
        a.hasDB = true
        ctx.Logger().Info("Database ready, unstashing queries")

        // Process queued queries
        ctx.UnstashAll()

    case *Query:
        if !a.hasDB {
            // Database not ready, stash query
            ctx.Stash()
            return
        }

        // Execute query
        result, err := a.executeQuery(msg)
        if err != nil {
            ctx.Response(&QueryError{Reason: err.Error()})
            return
        }
        ctx.Response(&QueryResult{Data: result})
    }
}

func (a *DatabaseActor) PostStop(ctx *actor.Context) error {
    if a.db != nil {
        return a.db.Close()
    }
    return nil
}
```

## Example: Batch Processing

Collect messages and process in batches:

```go
type BatchProcessor struct {
    batch     []*WorkItem
    batchSize int
}

func (a *BatchProcessor) PreStart(ctx *actor.Context) error {
    a.batch = make([]*WorkItem, 0, 100)
    a.batchSize = 10
    return nil
}

func (a *BatchProcessor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *WorkItem:
        // Add to batch
        a.batch = append(a.batch, msg)

        if len(a.batch) < a.batchSize {
            // Not enough for a batch yet, stash
            ctx.Stash()
            return
        }

        // Batch is full, process it
        a.processBatch(a.batch)
        a.batch = make([]*WorkItem, 0, 100)

        // Unstash one message to start next batch
        ctx.Unstash()

    case *Flush:
        // Process whatever we have
        if len(a.batch) > 0 {
            a.processBatch(a.batch)
            a.batch = make([]*WorkItem, 0, 100)
        }
        ctx.Response(&FlushComplete{})
    }
}

func (a *BatchProcessor) processBatch(batch []*WorkItem) {
    // Process batch
    for _, item := range batch {
        // Process item
    }
}

func (a *BatchProcessor) PostStop(ctx *actor.Context) error {
    // Flush remaining items
    if len(a.batch) > 0 {
        a.processBatch(a.batch)
    }
    return nil
}
```

## Example: Conditional Processing

Process messages only when a condition is met:

```go
type ThrottledActor struct {
    requestCount int
    limit        int
    resetTime    time.Time
}

func (a *ThrottledActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Request:
        now := time.Now()

        // Reset counter if window expired
        if now.After(a.resetTime) {
            a.requestCount = 0
            a.resetTime = now.Add(time.Minute)

            // Unstash pending requests
            ctx.UnstashAll()
        }

        // Check if we're over the limit
        if a.requestCount >= a.limit {
            // Over limit, stash for next window
            ctx.Stash()
            return
        }

        // Process request
        a.requestCount++
        a.handleRequest(msg)
        ctx.Response(&RequestHandled{})

    case *ResetThrottle:
        // Manual reset
        a.requestCount = 0
        a.resetTime = time.Now().Add(time.Minute)
        ctx.UnstashAll()
    }
}
```

## Stashing Best Practices

### Do's ✅

1. **Enable stashing**: Use `WithStashing()` when spawning
2. **Unstash after state change**: Process stashed messages when ready
3. **Use with behaviors**: Combine stashing with `Become`/`UnBecome`
4. **Limit stash size**: Be mindful of memory
5. **Document stashing logic**: Make it clear why messages are stashed

```go
// Good: Clear stashing logic
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Initialize:
        a.ready = true
        ctx.UnstashAll() // Process pending messages

    case *Work:
        if !a.ready {
            ctx.Stash() // Wait for initialization
            return
        }
        a.process(msg)
    }
}
```

### Don'ts ❌

1. **Don't forget to unstash**: Stashed messages won't process automatically
2. **Don't stash indefinitely**: Unstash when condition is met
3. **Don't use without enabling**: Will result in error
4. **Don't stash everything**: Only stash what's necessary
5. **Don't ignore stash overflow**: Monitor stash size

```go
// Bad: Stashing without unstashing
func (a *BadActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Initialize:
        a.ready = true
        // ❌ Forgot to unstash!

    case *Work:
        if !a.ready {
            ctx.Stash() // Messages pile up forever
            return
        }
        a.process(msg)
    }
}
```

## Common Patterns

### Pattern 1: Initialization Gate

```go
func (a *Actor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Init:
        a.initialize()
        ctx.UnstashAll()
    default:
        if !a.initialized {
            ctx.Stash()
            return
        }
        a.handleMessage(msg)
    }
}
```

### Pattern 2: State Machine with Stashing

```go
func (a *Actor) waitingState(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Ready:
        ctx.Become(a.activeState)
        ctx.UnstashAll()
    default:
        ctx.Stash()
    }
}

func (a *Actor) activeState(ctx *actor.ReceiveContext) {
    // Process normally
    a.handleMessage(ctx.Message())
}
```

### Pattern 3: Resource Pooling

```go
func (a *PoolActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Work:
        if a.hasAvailableWorker() {
            a.assignWork(msg)
        } else {
            ctx.Stash() // Wait for worker
        }

    case *WorkerAvailable:
        ctx.Unstash() // Process next stashed work
    }
}
```

## Performance Considerations

- **Memory**: Stash buffer is unbounded by default
- **Order**: Stashed messages maintain FIFO order
- **Low overhead when unused**: Negligible cost when the stash is empty
- **Unstashing**: O(1) for single unstash, O(n) for unstash all

## Limitations

- **Must enable**: Stashing requires `WithStashing()`
- **Unbounded**: Stash buffer has no size limit
- **Memory**: Large stashes consume memory
- **No persistence**: Stashed messages lost on actor restart

## Error Handling

If stashing is not enabled:

```go
// This will return an error
ctx.Stash() // Error: stash buffer not set
```

Always enable stashing when spawning:

```go
pid, err := actorSystem.Spawn(ctx, "actor", &MyActor{},
    actor.WithStashing()) // Required!
```

## Testing Stashing

```go
func TestStashing(t *testing.T) {
    ctx := context.Background()
    system, _ := actor.NewActorSystem("test",
        actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()))
    system.Start(ctx)
    defer system.Stop(ctx)

    // Spawn with stashing enabled
    pid, _ := system.Spawn(ctx, "test", &TestActor{},
        actor.WithStashing())

    // Send work before initialization
    actor.Tell(ctx, pid, &Work{Id: 1})
    actor.Tell(ctx, pid, &Work{Id: 2})

    // Initialize
    actor.Tell(ctx, pid, &Initialize{})

    // Wait for processing
    time.Sleep(100 * time.Millisecond)

    // Verify all work was processed
    response, _ := actor.Ask(ctx, pid, &GetProcessedCount{}, time.Second)
    count := response.(*ProcessedCount)
    assert.Equal(t, 2, count.Value)
}
```

## Summary

- **Stashing** defers messages for later processing
- **Enable** with `WithStashing()` when spawning
- **Stash** with `ctx.Stash()` to defer current message
- **Unstash** with `ctx.Unstash()` or `ctx.UnstashAll()`
- **Use** during initialization, state transitions, or resource acquisition
- **Combine** with behaviors for state machines
- **Remember** to unstash when ready!

## Next Steps

- **[Behaviors](behaviours.md)**: Combine stashing with dynamic behaviors
- **[Message Scheduling](message_scheduling.md)**: Schedule messages for future delivery
- **[Passivation](passivation.md)**: Automatic actor lifecycle management
- **[Reentrancy](reentrancy.md)**: Handle concurrent requests
