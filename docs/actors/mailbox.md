# Mailbox

A mailbox is the message queue that stores incoming messages for an actor. Every actor has exactly one mailbox that buffers messages until the actor is ready to process them.

## What is a Mailbox?

The mailbox is a critical component of the actor model. It provides:

- **Message buffering**: Stores messages until the actor can process them
- **Asynchronous communication**: Senders don't block waiting for the actor
- **FIFO ordering**: Messages are processed in the order they are enqueued
- **Backpressure control**: Bounded mailboxes can apply backpressure
- **Thread safety**: Safe for concurrent message producers

## Mailbox Interface

All mailboxes implement the `Mailbox` interface:

```go
type Mailbox interface {
    // Enqueue pushes a message into the mailbox
    Enqueue(msg *ReceiveContext) error

    // Dequeue fetches a message from the mailbox
    Dequeue() (msg *ReceiveContext)

    // IsEmpty returns true when the mailbox is empty
    IsEmpty() bool

    // Len returns the size of the mailbox
    Len() int64

    // Dispose closes the mailbox and releases resources
    Dispose()
}
```

## Mailbox Types

GoAkt provides several mailbox implementations, each with different characteristics:

### 1. UnboundedMailbox (Default)

A lock-free, multi-producer single-consumer (MPSC) FIFO queue with no size limit.

```go
mailbox := actor.NewUnboundedMailbox()
```

**Characteristics:**

- **Unbounded**: No size limit (can grow indefinitely)
- **Lock-free**: Uses atomic operations for high performance
- **FIFO**: Messages processed in arrival order
- **Non-blocking**: Enqueue never blocks
- **Fast**: Optimized for throughput

**When to use:**

- Default choice for most actors
- High-throughput scenarios
- When you trust message producers
- Memory is not constrained

**When NOT to use:**

- Need backpressure control
- Memory is constrained
- Unbounded growth is a concern

**Example:**

```go
pid, err := actorSystem.Spawn(ctx, "processor", &MyActor{},
    actor.WithMailbox(actor.NewUnboundedMailbox()))
```

### 2. BoundedMailbox

A bounded, blocking mailbox with fixed capacity backed by a ring buffer.

```go
mailbox := actor.NewBoundedMailbox(1000) // Capacity of 1000 messages
```

**Characteristics:**

- **Bounded**: Fixed maximum capacity
- **Blocking**: Enqueue blocks when full
- **FIFO**: Messages processed in arrival order
- **Backpressure**: Naturally applies backpressure to producers
- **Resource-safe**: Prevents unbounded memory growth

**When to use:**

- Need strict backpressure control
- Protect against memory exhaustion
- Rate limiting message processing
- Resource-constrained environments

**When NOT to use:**

- Need non-blocking sends
- Very high throughput required
- Blocking senders is unacceptable

**Example:**

```go
// Actor with bounded mailbox of 500 messages
pid, err := actorSystem.Spawn(ctx, "worker", &WorkerActor{},
    actor.WithMailbox(actor.NewBoundedMailbox(500)))
```

**Backpressure behavior:**

```go
// If mailbox is full, Tell will block until space is available
err := actor.Tell(ctx, pid, &Message{})
// This blocks when mailbox capacity is reached
```

### 3. UnboundedPriorityMailbox

A priority queue where messages are processed based on priority rather than arrival order.

```go
// Define priority function
priorityFunc := func(msg1, msg2 proto.Message) bool {
    // Return true if msg1 has higher priority than msg2
    p1 := getPriority(msg1)
    p2 := getPriority(msg2)
    return p1 > p2
}

mailbox := actor.NewUnboundedPriorityMailBox(priorityFunc)
```

**Characteristics:**

- **Unbounded**: No size limit
- **Priority-based**: Messages processed by priority, not order
- **Custom ordering**: Define your own priority function
- **Flexible**: Different message types can have different priorities

**When to use:**

- Critical messages need faster processing
- Different message types have different priorities
- Need to handle urgent messages first
- Implementing priority-based workflows

**When NOT to use:**

- FIFO order is important
- All messages have equal priority
- Need bounded capacity

**Example:**

```go
// Priority function: higher numerical priority first
priorityFunc := func(msg1, msg2 proto.Message) bool {
    // System messages have highest priority
    if _, ok := msg1.(*goaktpb.PostStart); ok {
        return true
    }
    if _, ok := msg2.(*goaktpb.PostStart); ok {
        return false
    }

    // Then urgent messages
    if m1, ok := msg1.(*UrgentMessage); ok {
        if m2, ok := msg2.(*UrgentMessage); ok {
            return m1.GetPriority() > m2.GetPriority()
        }
        return true
    }

    // Regular messages last
    return false
}

pid, err := actorSystem.Spawn(ctx, "priority-worker", &MyActor{},
    actor.WithMailbox(actor.NewUnboundedPriorityMailBox(priorityFunc)))
```

### 4. UnboundedFairMailbox

A fair, unbounded mailbox that ensures no single producer dominates.

```go
mailbox := actor.NewUnboundedFairMailbox()
```

**Characteristics:**

- **Unbounded**: No size limit
- **Fair**: Ensures fairness across producers
- **Lock-free**: High performance
- **FIFO**: Within each producer's messages

**When to use:**

- Multiple producers sending to same actor
- Need fairness guarantees
- Prevent producer starvation
- Load balancing across sources

**Example:**

```go
pid, err := actorSystem.Spawn(ctx, "fair-processor", &MyActor{},
    actor.WithMailbox(actor.NewUnboundedFairMailbox()))
```

### 5. UnboundedSegmentedMailbox

A segmented, unbounded mailbox optimized for high-throughput scenarios.

```go
mailbox := actor.NewUnboundedSegmentedMailbox()
```

**Characteristics:**

- **Unbounded**: No size limit
- **Segmented**: Uses linked segments for memory efficiency
- **High throughput**: Optimized for bulk message processing
- **Cache-friendly**: Better cache locality

**When to use:**

- Very high message rates
- Need optimal performance
- Large message volumes
- Throughput is critical

**Example:**

```go
pid, err := actorSystem.Spawn(ctx, "high-throughput", &MyActor{},
    actor.WithMailbox(actor.NewUnboundedSegmentedMailbox()))
```

## Choosing a Mailbox

### Decision Matrix

| Requirement                       | Recommended Mailbox         |
| --------------------------------- | --------------------------- |
| Default/general purpose           | `UnboundedMailbox`          |
| Need backpressure                 | `BoundedMailbox`            |
| Priority-based processing         | `UnboundedPriorityMailbox`  |
| Multiple producers, need fairness | `UnboundedFairMailbox`      |
| Extreme high throughput           | `UnboundedSegmentedMailbox` |
| Memory constrained                | `BoundedMailbox`            |
| Rate limiting                     | `BoundedMailbox`            |

### Performance Considerations

**Unbounded Mailboxes:**

- ✅ Higher throughput
- ✅ No blocking on send
- ❌ Can grow indefinitely
- ❌ No natural backpressure

**Bounded Mailboxes:**

- ✅ Controlled memory usage
- ✅ Natural backpressure
- ❌ Can block senders
- ❌ Slightly lower throughput

**Priority Mailboxes:**

- ✅ Important messages processed first
- ❌ Additional overhead for priority logic
- ❌ Can starve low-priority messages

## Configuring Mailboxes

### At Spawn Time

```go
// Spawn actor with specific mailbox
pid, err := actorSystem.Spawn(ctx, "my-actor", &MyActor{},
    actor.WithMailbox(actor.NewBoundedMailbox(1000)))
```

### Default Mailbox

If no mailbox is specified, actors use `UnboundedMailbox` by default:

```go
// Uses UnboundedMailbox by default
pid, err := actorSystem.Spawn(ctx, "my-actor", &MyActor{})
```

## Mailbox Behavior

### Message Ordering

The mailbox is a **thread-safe FIFO queue**: messages are processed in the order they are enqueued. There is no per-sender ordering guarantee.

When a single goroutine sends messages in sequence, they are enqueued (and thus processed) in that order:

```go
// Enqueued in order: Msg1 → Msg2 → Msg3
actor.Tell(ctx, pid, &Msg1{})
actor.Tell(ctx, pid, &Msg2{})
actor.Tell(ctx, pid, &Msg3{})
```

When **multiple senders** (or goroutines) send to the same actor concurrently, messages are interleaved in the order they reach the mailbox; ordering between different senders is non-deterministic:

```go
// Goroutine 1
actor.Tell(ctx, pid, &MsgA{})

// Goroutine 2 (concurrent)
actor.Tell(ctx, pid, &MsgB{})

// Order is non-deterministic: could be A→B or B→A
```

### Mailbox Lifecycle

1. **Creation**: Mailbox created when actor spawns
2. **Receiving**: Messages enqueued by senders
3. **Processing**: Actor dequeues and processes messages
4. **Disposal**: Mailbox disposed when actor stops

### Mailbox and Actor State

The mailbox lifecycle is tied to the actor:

```go
// Actor spawned → mailbox created
pid, _ := actorSystem.Spawn(ctx, "actor", &MyActor{})

// Messages enqueued in mailbox
actor.Tell(ctx, pid, &Message{})

// Actor stopped → mailbox disposed
pid.Shutdown(ctx)
```

## Monitoring Mailbox

### Mailbox length (interface only)

The `Mailbox` interface defines `Len() int64`, which returns the current number of messages in the queue. This is available only when you hold a reference to the mailbox (for example in tests or custom code that constructs the mailbox). The `PID` does **not** expose mailbox length to actors at runtime — there is no `MailboxSize()` or similar method on `PID`.

To observe actor load from inside an actor, use `pid.Metric(ctx)` (see [Metrics](../observability/metrics.md)): it provides `ProcessedCount()`, `StashSize()`, and other counters. When using a bounded mailbox, handle `errors.ErrMailboxFull` from `Tell` to detect backpressure.

### Actor metrics (OpenTelemetry)

When metrics are enabled with `WithMetrics()`, GoAkt registers per-actor instruments. None of them are mailbox enqueue/dequeue counters. The ones relevant to message flow are:

- `actor.stash.size` — number of messages currently stashed
- `actor.processed.count` — total messages processed
- `actor.deadletters.count` — messages dropped to dead letters

For the full list of system and actor metrics, see [Metrics](../observability/metrics.md).

## Best Practices

### Choosing Mailbox Size

For bounded mailboxes, consider:

```go
// Too small: frequent blocking
actor.WithMailbox(actor.NewBoundedMailbox(10)) // ❌ May block often

// Too large: defeats purpose of backpressure
actor.WithMailbox(actor.NewBoundedMailbox(1000000)) // ❌ Too large

// Reasonable: 100-10000 based on message rate
actor.WithMailbox(actor.NewBoundedMailbox(1000)) // ✅ Good balance
```

### Mailbox Sizing Formula

```
Mailbox Size = Message Rate × Processing Time × Safety Factor

Example:
- 1000 msgs/sec
- 10ms avg processing time
- 2x safety factor
= 1000 × 0.01 × 2 = 20 messages

Use 50-100 for safety margin
```

### Priority Function Guidelines

When using priority mailboxes:

✅ **Do:**

- Keep priority function fast
- Use simple comparisons
- Cache priority values
- Handle all message types

❌ **Don't:**

- Make expensive calculations
- Access external state
- Use I/O operations
- Ignore unknown message types

```go
// ✅ Good: Fast priority function
priorityFunc := func(msg1, msg2 proto.Message) bool {
    return getPriority(msg1) > getPriority(msg2)
}

// ❌ Bad: Slow priority function
priorityFunc := func(msg1, msg2 proto.Message) bool {
    // Don't do this!
    p1 := queryDatabase(msg1) // ❌ I/O operation
    p2 := expensiveCalculation(msg2) // ❌ Expensive
    return p1 > p2
}
```

### Backpressure Strategies

When using bounded mailboxes:

1. **No sender feedback for Tell**: When a bounded mailbox is full, `Enqueue` fails inside the PID; the error is handled internally (e.g. dead-letter). **`Tell` does not return `ErrMailboxFull` to the caller** — the sender gets `nil` even when the message was not enqueued.
2. **Observe load**: Use `pid.Metric(ctx)` or OpenTelemetry (e.g. `actor.processed.count`, `actor.stash.size`) to detect slow consumers and apply backpressure upstream — throttle sends, use a circuit breaker, or route to backup actors.
3. **Alternative routing**: Route to a backup actor or queue when metrics or timeouts indicate high load.
4. **Grain mailboxes**: For grains with a bounded mailbox, `TellGrain` returns `gerrors.ErrMailboxFull` when full, so callers can retry or route elsewhere (see [Grains](../grains/overview.md)).

## Dead Letters

Messages sent to non-existent or stopped actors go to the dead letter queue:

```go
// Actor is stopped
pid.Shutdown(ctx)

// This message goes to dead letters
err := actor.Tell(ctx, pid, &Message{})
// err == actor.ErrDead
```

The dead letter queue can be monitored for debugging:

```go
// Subscribe to dead letters
actorSystem.Subscribe(actor.DeadLetterTopic, handlerPID)
```

## Common Patterns

### Rate-Limited Actor

```go
// Use bounded mailbox for rate limiting
pid, err := actorSystem.Spawn(ctx, "rate-limited", &MyActor{},
    actor.WithMailbox(actor.NewBoundedMailbox(100)))

// Sending will block when rate exceeds processing capacity
```

### Priority Message Processing

```go
type Message struct {
    Priority int
    Data     string
}

priorityFunc := func(msg1, msg2 proto.Message) bool {
    m1 := msg1.(*Message)
    m2 := msg2.(*Message)
    return m1.Priority > m2.Priority
}

pid, _ := actorSystem.Spawn(ctx, "priority", &MyActor{},
    actor.WithMailbox(actor.NewUnboundedPriorityMailBox(priorityFunc)))
```

### Fair Resource Allocation

```go
// Use fair mailbox when multiple producers
pid, err := actorSystem.Spawn(ctx, "fair", &ResourceManager{},
    actor.WithMailbox(actor.NewUnboundedFairMailbox()))

// Each producer gets fair access
for i := 0; i < 10; i++ {
    go func(id int) {
        for j := 0; j < 1000; j++ {
            actor.Tell(ctx, pid, &Request{ProducerID: id})
        }
    }(i)
}
```

## Advanced Topics

### Custom Mailbox Implementation

You can implement custom mailboxes by implementing the `Mailbox` interface:

```go
type CustomMailbox struct {
    // Your implementation
}

func (m *CustomMailbox) Enqueue(msg *ReceiveContext) error {
    // Your enqueue logic
}

func (m *CustomMailbox) Dequeue() *ReceiveContext {
    // Your dequeue logic
}

func (m *CustomMailbox) IsEmpty() bool {
    // Your empty check
}

func (m *CustomMailbox) Len() int64 {
    // Your length calculation
}

func (m *CustomMailbox) Dispose() {
    // Your cleanup logic
}
```

### Mailbox Selection Strategy

```go
func selectMailbox(actorType string, expectedLoad int) actor.Mailbox {
    switch {
    case expectedLoad > 10000:
        // High throughput
        return actor.NewUnboundedSegmentedMailbox()

    case expectedLoad > 1000:
        // Medium throughput with backpressure
        return actor.NewBoundedMailbox(expectedLoad)

    default:
        // Default for normal load
        return actor.NewUnboundedMailbox()
    }
}

mailbox := selectMailbox("worker", 5000)
pid, _ := actorSystem.Spawn(ctx, "worker", &Worker{},
    actor.WithMailbox(mailbox))
```

## Summary

- **Mailboxes** buffer messages for actors
- **Unbounded** mailboxes offer high throughput but no backpressure
- **Bounded** mailboxes provide backpressure and memory control
- **Priority** mailboxes enable priority-based processing
- **Fair** mailboxes ensure fairness across producers
- Choose mailbox type based on your requirements

## Next Steps

- **[Messaging](messaging.md)**: Learn about message sending patterns
- **[Stashing](stashing.md)**: Temporarily defer message processing
- **[Behaviors](behaviours.md)**: Change actor behavior dynamically
- **[Passivation](passivation.md)**: Automatic actor lifecycle management
