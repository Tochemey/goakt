# Mailbox

A mailbox is the message queue that stores incoming messages for an actor. Every actor has exactly one mailbox that buffers messages until the actor is ready to process them.

## Table of Contents

- 🤔 [What is a Mailbox?](#what-is-a-mailbox)
- 🔌 [Mailbox Interface](#mailbox-interface)
- 📬 [Mailbox Types](#mailbox-types)
- 🎯 [Choosing a Mailbox](#choosing-a-mailbox)
- ⚙️ [Configuring Mailboxes](#configuring-mailboxes)
- 🔄 [Mailbox Behavior](#mailbox-behavior)
- 📊 [Monitoring Mailbox](#monitoring-mailbox)
- ✅ [Best Practices](#best-practices)
- ☠️ [Dead Letters](#dead-letters)
- 🧩 [Common Patterns](#common-patterns)
- 📋 [Summary](#summary)

---

## What is a Mailbox?

In the GoAkt actor framework, the mailbox is a fundamental component that functions as the queue for messages sent to an actor. Every actor has its own mailbox that collects incoming messages until the actor is ready to process them. 

This design is central to the actor model: rather than sharing state or locking resources, actors communicate solely by exchanging messages via their mailboxes, ensuring isolation and concurrency while maintaining a sequential processing order.
The mailbox in GoAkt plays a crucial role in decoupling message senders and receivers by buffering messages until the recipient actor is ready to process them. It not only ensures that messages are handled in a controlled, sequential manner but also integrates with the actor’s supervision and lifecycle management. 

By isolating each actor’s message flow, the mailbox enables robust fault tolerance and scalability, allowing the system to support both local and distributed messaging patterns seamlessly while optimising resource usage and throughput.

## Mailbox Interface

Mailboxes implement the following interface. Custom mailbox implementations must be thread-safe.

```go
package actor

// Mailbox defines the actor mailbox. Implementations should be thread-safe.
type Mailbox interface {
	// Enqueue pushes a message into the mailbox. Returns an error when full (e.g. bounded).
	Enqueue(msg *ReceiveContext) error
	// Dequeue fetches the next message from the mailbox.
	Dequeue() (msg *ReceiveContext)
	// IsEmpty returns true when the mailbox has no messages.
	IsEmpty() bool
	// Len returns the number of messages in the mailbox.
	Len() int64
	// Dispose releases the mailbox and unblocks any goroutines waiting on Enqueue/Dequeue.
	Dispose()
}
```

> You can implement a custom mailbox against this interface.

## Mailbox Types

| Type                           | Constructor                                 | Use when                                                                                                     |
|--------------------------------|---------------------------------------------|--------------------------------------------------------------------------------------------------------------|
| **UnboundedMailbox** (default) | `NewUnboundedMailbox()`                     | Default; high throughput; no backpressure needed. Lock-free FIFO.                                            |
| **BoundedMailbox**             | `NewBoundedMailbox(capacity)`               | You need backpressure; Tell blocks when full. Prevents unbounded growth.                                     |
| **UnboundedPriorityMailbox**   | `NewUnboundedPriorityMailBox(priorityFunc)` | Messages must be processed by priority. `priorityFunc(msg1, msg2)` returns true if msg1 has higher priority. |
| **UnboundedFairMailbox**       | `NewUnboundedFairMailbox()`                 | Multiple producers; you want fairness so no single producer starves others.                                  |
| **UnboundedSegmentedMailbox**  | `NewUnboundedSegmentedMailbox()`            | High-throughput, segmented queue.                                                                            |

Pass the mailbox at spawn: **actor.WithMailbox(actor.NewBoundedMailbox(500))**. Bounded mailboxes apply backpressure: when full, **Tell** blocks until space is available.

### When to use which

- **Unbounded** — Most actors; memory not a concern.
- **Bounded** — Resource limits, backpressure, or many actors.
- **Priority** — Urgent vs normal messages; custom `priorityFunc` (e.g. system messages first).
- **Fair** — Many producers to one actor; avoid starvation.

## Choosing a Mailbox

### Decision Matrix

| Requirement                       | Recommended Mailbox         |
|-----------------------------------|-----------------------------|
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

- **At spawn**: Pass the mailbox with `WithMailbox` (e.g. `WithMailbox(NewBoundedMailbox(500))`).
- **Default**: If omitted, the actor uses **UnboundedMailbox**.

## Mailbox Behavior

### Message Ordering

- **FIFO**: Messages are processed in enqueue order; mailboxes are thread-safe.
- **Single sender**: Messages from one sender stay in order.
- **Multiple senders**: Interleaving is non-deterministic (e.g. A then B vs B then A).
- **Priority mailbox**: Order is determined by your priority function, not FIFO.

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

For bounded mailboxes, pick a capacity that matches your message rate: too small causes frequent blocking; too large weakens backpressure. Typical range 100–10000.

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

The dead letter queue can be monitored by subscribing to the actor system’s **events stream**. Dead letters are published as events with payload type `*goaktpb.Deadletter`:

```go
// Subscribe to the events stream (includes dead letters)
subscriber, err := actorSystem.Subscribe()
if err != nil {
    return err
}
defer actorSystem.Unsubscribe(subscriber)

// Consume events and handle dead letters
for event := range subscriber.Iterator() {
    if dl, ok := event.Payload().(*goaktpb.Deadletter); ok {
        log.Printf("Deadletter: from=%s to=%s reason=%s",
            dl.GetSender(), dl.GetReceiver(), dl.GetReason())
    }
}
```

For the full list of event types and usage, see [Events Stream](../events_stream/overview.md).

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

## Summary

- **Mailboxes** buffer messages for actors
- **Unbounded** mailboxes offer high throughput but no backpressure
- **Bounded** mailboxes provide backpressure and memory control
- **Priority** mailboxes enable priority-based processing
- **Fair** mailboxes ensure fairness across producers
- Choose mailbox type based on your requirements