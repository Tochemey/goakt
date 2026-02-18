# Events Stream Overview

The Events Stream is GoAkt's built-in system for broadcasting internal actor lifecycle events to external consumers. It enables observability, monitoring, and reactive system integration without intrusive instrumentation.

## Table of Contents

- 🤔 [What is Events Stream?](#what-is-events-stream)
- 🔀 [Events Stream vs Pub/Sub](#events-stream-vs-pubsub)
- 📢 [System Events](#system-events)
- 🚀 [Basic Usage](#basic-usage)
- 🔌 [Subscriber API](#subscriber-api)
- 💡 [Use Cases](#use-cases)
- ✅ [Best Practices](#best-practices)
- 🔧 [Troubleshooting](#troubleshooting)
- ⚠️ [Limitations](#limitations)

---

## What is Events Stream?

The Events Stream:

- **Publishes system events**: Actor lifecycle events (started, stopped, child created, etc.)
- **External consumption**: Consumed from outside the actor system
- **Non-blocking delivery**: Messages queued for subscribers
- **Channel-based**: Uses Go channels for event consumption
- **Local only**: Not propagated across cluster nodes
- **Observability-focused**: Designed for monitoring and diagnostics

## Events Stream vs Pub/Sub

| Feature       | Events Stream                   | Pub/Sub                            |
|---------------|---------------------------------|------------------------------------|
| **Purpose**   | System event observability      | Application messaging              |
| **Events**    | Actor lifecycle events          | Custom application messages        |
| **Consumers** | External (outside actor system) | Actors (inside actor system)       |
| **API**       | `system.Subscribe()`            | `goaktpb.Subscribe` to topic actor |
| **Delivery**  | Channel-based iterator          | Actor mailbox                      |
| **Cluster**   | Local only                      | Cluster-wide propagation           |
| **Use case**  | Monitoring, diagnostics         | Event-driven architecture          |

## System Events

The events stream publishes these events:

### Actor Lifecycle Events

| Event               | Description                                             | Key Fields                         |
|---------------------|---------------------------------------------------------|------------------------------------|
| `ActorStarted`      | Actor successfully started                              | `Address`, `StartedAt`             |
| `ActorStopped`      | Actor stopped or shut down                              | `Address`, `StoppedAt`             |
| `ActorPassivated`   | Actor passivated after idle timeout                     | `Address`, `PassivatedAt`          |
| `ActorChildCreated` | Actor spawned a child actor                             | `Address`, `Parent`, `CreatedAt`   |
| `ActorRestarted`    | Actor restarted by supervisor after failure             | `Address`, `RestartedAt`           |
| `ActorSuspended`    | Actor suspended due to an error                         | `Address`, `SuspendedAt`, `Reason` |
| `ActorReinstated`   | Previously suspended actor brought back to active state | `Address`, `ReinstatedAt`          |

### Cluster Events

These are only published when the actor system is running in cluster mode:

| Event        | Description                   | Key Fields             |
|--------------|-------------------------------|------------------------|
| `NodeJoined` | A new node joined the cluster | `Address`, `Timestamp` |
| `NodeLeft`   | A node left the cluster       | `Address`, `Timestamp` |

### Dead Letter Events

| Event        | Description                                                | Key Fields                                            |
|--------------|------------------------------------------------------------|-------------------------------------------------------|
| `Deadletter` | A message could not be delivered to its intended recipient | `Sender`, `Receiver`, `Message`, `SendTime`, `Reason` |

## Basic Usage

Subscribe with `system.Subscribe()`, then consume from `subscriber.Iterator()`. Always call `system.Unsubscribe(subscriber)` when done. The iterator returns a **snapshot** of buffered events at call time; poll repeatedly (e.g. in a loop with a ticker) to process ongoing events.

```go
subscriber, err := system.Subscribe()
if err != nil {
    log.Fatal(err)
}
defer system.Unsubscribe(subscriber)

go func() {
    for event := range subscriber.Iterator() {
        switch msg := event.Payload().(type) {
        case *goaktpb.ActorStarted:
            log.Printf("Actor started: %s", msg.GetAddress())
        case *goaktpb.ActorStopped:
            log.Printf("Actor stopped: %s", msg.GetAddress())
        case *goaktpb.ActorRestarted, *goaktpb.ActorSuspended:
            log.Printf("Event: %T %s", msg, msg.GetAddress())
        case *goaktpb.Deadletter:
            log.Printf("Deadletter: %s -> %s", msg.GetSender(), msg.GetReceiver())
        }
    }
}()
```

## Subscriber API

Create and cleanup:

```go
subscriber, err := system.Subscribe()
if err != nil {
    return err
}
defer system.Unsubscribe(subscriber)
```

**Subscriber** (public methods):

```go
type Subscriber interface {
    ID() string
    Active() bool
    Topics() []string
    Iterator() chan *Message
    Shutdown()
}
```

**Message** — payload is one of the `*goaktpb.*` event types:

```go
func (m *Message) Topic() string   // topic name
func (m *Message) Payload() any   // e.g. *goaktpb.ActorStarted, *goaktpb.Deadletter
```

`Iterator()` returns a channel that delivers a **snapshot** of buffered events at call time; events arriving during iteration are not included. Poll in a loop (e.g. with a ticker and `select` on `ctx.Done()`) to consume ongoing events.

## Use Cases

- **Health monitoring:** Subscribe, poll `Iterator()` on a ticker, and react to `ActorRestarted` / `ActorSuspended` (e.g. increment counters, alert when over threshold).
- **Metrics:** Feed `ActorStarted`, `ActorStopped`, `ActorRestarted` (etc.) into Prometheus or similar; poll in a loop and call your recording function per event.
- **Audit logging:** Log lifecycle events to a file or logging backend; switch on `event.Payload()` and write one line per event type.
- **Dashboards:** Poll events and push `Topic()` + `Payload()` to WebSocket or SSE clients (e.g. JSON-serialized).

## Best Practices

- **Always unsubscribe:** Use `defer system.Unsubscribe(subscriber)` after `Subscribe()`.
- **Poll regularly:** `Iterator()` is a snapshot; use a ticker loop (and `select` on `ctx.Done()`) to drain events periodically.
- **Filter in code:** Switch on `event.Payload()` type and only handle events you need.
- **Avoid blocking:** Process events quickly or hand off to a goroutine so the poll loop doesn’t stall.

## Troubleshooting

- **No events:** Ensure the system is started, the subscriber is active (`subscriber.Active()`), and you poll `Iterator()` regularly (it returns a snapshot at call time).
- **High memory:** Poll and process events more often; avoid slow processing so the queue doesn’t grow.
- **Missed events:** Poll more frequently; events that arrive while you’re iterating aren’t included in the current snapshot.

## Limitations

1. **Local only**: Events not propagated across cluster nodes
2. **No persistence**: Events not stored
3. **Snapshot iteration**: `Iterator()` returns buffered messages at call time
4. **Unbounded queue**: No backpressure mechanism
5. **Single topic**: All events on same topic ("goakt.events")
