# Events Stream Overview

The Events Stream is GoAkt's built-in system for broadcasting internal actor lifecycle events to external consumers. It enables observability, monitoring, and reactive system integration without intrusive instrumentation.

## Table of Contents

- 🤔 [What is Events Stream?](#what-is-events-stream)
- 🔀 [Events Stream vs Pub/Sub](#events-stream-vs-pubsub)
- 📢 [System Events](#system-events)
- 🚀 [Basic Usage](#basic-usage)
- 💡 [Complete Monitoring Example](#complete-monitoring-example)
- 🔌 [Subscriber API](#subscriber-api)
- 💡 [Use Cases](#use-cases)
- ✅ [Best Practices](#best-practices)
- 💡 [Complete Integration Example](#complete-integration-example)
- 🔧 [Troubleshooting](#troubleshooting)
- ⚠️ [Limitations](#limitations)
- ➡️ [Next Steps](#next-steps)

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

### Subscribe to Events

```go
package main

import (
    "context"
    "log"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/goaktpb"
)

func main() {
    ctx := context.Background()

    // Create actor system
    system, err := actor.NewActorSystem("my-system")
    if err != nil {
        log.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    // Subscribe to events stream
    subscriber, err := system.Subscribe()
    if err != nil {
        log.Fatal(err)
    }
    defer system.Unsubscribe(subscriber)

    // Consume events in background
    go func() {
        for event := range subscriber.Iterator() {
            log.Printf("Event on topic %s: %v\n", event.Topic(), event.Payload())
        }
    }()

    // Spawn an actor (generates ActorStarted event)
    _, err = system.Spawn(ctx, "my-actor", new(MyActor))
    if err != nil {
        log.Fatal(err)
    }

    // Keep running
    select {}
}
```

### Processing Specific Events

```go
func consumeEvents(ctx context.Context, subscriber eventstream.Subscriber) {
    for event := range subscriber.Iterator() {
        payload := event.Payload()

        switch msg := payload.(type) {
        // Actor lifecycle events
        case *goaktpb.ActorStarted:
            log.Printf("Actor started: %s at %v\n",
                msg.GetAddress(),
                msg.GetStartedAt().AsTime())

        case *goaktpb.ActorStopped:
            log.Printf("Actor stopped: %s at %v\n",
                msg.GetAddress(),
                msg.GetStoppedAt().AsTime())

        case *goaktpb.ActorChildCreated:
            log.Printf("Child actor created: %s (parent: %s) at %v\n",
                msg.GetAddress(),
                msg.GetParent(),
                msg.GetCreatedAt().AsTime())

        case *goaktpb.ActorPassivated:
            log.Printf("Actor passivated: %s at %v\n",
                msg.GetAddress(),
                msg.GetPassivatedAt().AsTime())

        case *goaktpb.ActorRestarted:
            log.Printf("Actor restarted: %s at %v\n",
                msg.GetAddress(),
                msg.GetRestartedAt().AsTime())

        case *goaktpb.ActorSuspended:
            log.Printf("Actor suspended: %s (reason: %s) at %v\n",
                msg.GetAddress(),
                msg.GetReason(),
                msg.GetSuspendedAt().AsTime())

        case *goaktpb.ActorReinstated:
            log.Printf("Actor reinstated: %s at %v\n",
                msg.GetAddress(),
                msg.GetReinstatedAt().AsTime())

        // Cluster events (only in cluster mode)
        case *goaktpb.NodeJoined:
            log.Printf("Node joined: %s at %v\n",
                msg.GetAddress(),
                msg.GetTimestamp().AsTime())

        case *goaktpb.NodeLeft:
            log.Printf("Node left: %s at %v\n",
                msg.GetAddress(),
                msg.GetTimestamp().AsTime())

        // Dead letter events
        case *goaktpb.Deadletter:
            log.Printf("Deadletter: sender=%s receiver=%s reason=%s at %v\n",
                msg.GetSender(),
                msg.GetReceiver(),
                msg.GetReason(),
                msg.GetSendTime().AsTime())

        default:
            log.Printf("Unknown event type: %T\n", payload)
        }
    }
}
```

## Complete Monitoring Example

```go
package main

import (
    "context"
    "log"
    "sync"
    "time"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/eventstream"
    "github.com/tochemey/goakt/v3/goaktpb"
)

// SystemMonitor tracks actor system health
type SystemMonitor struct {
    mu              sync.RWMutex
    activeActors    map[string]time.Time
    stoppedActors   int64
    restartedActors int64
    suspendedActors int64
}

func NewSystemMonitor() *SystemMonitor {
    return &SystemMonitor{
        activeActors: make(map[string]time.Time),
    }
}

func (m *SystemMonitor) Start(ctx context.Context, system actor.ActorSystem) error {
    // Subscribe to events stream
    subscriber, err := system.Subscribe()
    if err != nil {
        return err
    }

    // Process events
    go m.processEvents(ctx, subscriber)

    // Periodic reporting
    go m.reportMetrics(ctx)

    return nil
}

func (m *SystemMonitor) processEvents(ctx context.Context, subscriber eventstream.Subscriber) {
    for {
        select {
        case <-ctx.Done():
            return
        case event := <-subscriber.Iterator():
            if event == nil {
                continue
            }

            m.handleEvent(event.Payload())
        }
    }
}

func (m *SystemMonitor) handleEvent(payload interface{}) {
    m.mu.Lock()
    defer m.mu.Unlock()

    switch msg := payload.(type) {
    case *goaktpb.ActorStarted:
        m.activeActors[msg.GetAddress()] = msg.GetStartedAt().AsTime()

    case *goaktpb.ActorStopped:
        delete(m.activeActors, msg.GetAddress())
        m.stoppedActors++

    case *goaktpb.ActorRestarted:
        m.restartedActors++

    case *goaktpb.ActorSuspended:
        m.suspendedActors++
        log.Printf("WARNING: Actor suspended: %s (reason: %s)",
            msg.GetAddress(), msg.GetReason())
    }
}

func (m *SystemMonitor) reportMetrics(ctx context.Context) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            m.mu.RLock()
            log.Printf("Metrics: Active=%d, Stopped=%d, Restarted=%d, Suspended=%d\n",
                len(m.activeActors),
                m.stoppedActors,
                m.restartedActors,
                m.suspendedActors,
            )
            m.mu.RUnlock()
        }
    }
}

func (m *SystemMonitor) GetMetrics() map[string]interface{} {
    m.mu.RLock()
    defer m.mu.RUnlock()

    return map[string]interface{}{
        "active_actors":    len(m.activeActors),
        "stopped_actors":   m.stoppedActors,
        "restarted_actors": m.restartedActors,
        "suspended_actors": m.suspendedActors,
    }
}
```

## Subscriber API

### Creating a Subscriber

```go
// Subscribe to events
subscriber, err := system.Subscribe()
if err != nil {
    log.Fatal(err)
}

// Always clean up
defer system.Unsubscribe(subscriber)
```

### Subscriber Interface

```go
type Subscriber interface {
    // ID returns the unique subscriber ID
    ID() string

    // Active returns whether the subscriber is active
    Active() bool

    // Topics returns the list of subscribed topics
    Topics() []string

    // Iterator returns a channel for consuming messages
    Iterator() chan *Message

    // Shutdown deactivates the subscriber
    Shutdown()
}
```

### Message Interface

```go
type Message interface {
    // Topic returns the topic name
    Topic() string

    // Payload returns the actual event message
    Payload() interface{}
}
```

### Consuming Messages

```go
subscriber, _ := system.Subscribe()

// Iterate over messages
for event := range subscriber.Iterator() {
    topic := event.Topic()
    payload := event.Payload()

    // Process event
    log.Printf("Topic: %s, Event: %T\n", topic, payload)
}
```

**Important**:

- `Iterator()` returns a snapshot of buffered messages at call time
- New messages arriving during iteration are not included
- Call `Iterator()` repeatedly to consume ongoing events

### Polling Pattern

```go
func pollEvents(ctx context.Context, subscriber eventstream.Subscriber) {
    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            // Drain events
            for event := range subscriber.Iterator() {
                processEvent(event)
            }
        }
    }
}
```

## Use Cases

### 1. Health Monitoring

Track system health in real-time:

```go
type HealthChecker struct {
    unhealthyActors map[string]int // Actor ID -> failure count
    alertThreshold  int
    alerter         Alerter
}

func (h *HealthChecker) Monitor(ctx context.Context, system actor.ActorSystem) {
    subscriber, _ := system.Subscribe()
    defer system.Unsubscribe(subscriber)

    ticker := time.NewTicker(time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            for event := range subscriber.Iterator() {
                h.processEvent(event)
            }
        }
    }
}

func (h *HealthChecker) processEvent(event *eventstream.Message) {
    payload := event.Payload()

    switch msg := payload.(type) {
    case *goaktpb.ActorRestarted:
        actorID := msg.GetAddress()
        h.unhealthyActors[actorID]++

        if h.unhealthyActors[actorID] >= h.alertThreshold {
            h.alerter.Send(AlertCritical, fmt.Sprintf(
                "Actor %s has restarted %d times",
                actorID,
                h.unhealthyActors[actorID],
            ))
        }

    case *goaktpb.ActorSuspended:
        h.alerter.Send(AlertWarning, fmt.Sprintf(
            "Actor %s suspended: %s",
            msg.GetAddress(),
            msg.GetReason(),
        ))

    case *goaktpb.ActorStarted:
        // Reset failure counter on successful start
        delete(h.unhealthyActors, msg.GetAddress())
    }
}
```

### 2. Metrics Collection

Export metrics to monitoring systems:

```go
import "github.com/prometheus/client_golang/prometheus"

type PrometheusExporter struct {
    actorStarts   prometheus.Counter
    actorStops    prometheus.Counter
    actorRestarts prometheus.Counter
}

func NewPrometheusExporter() *PrometheusExporter {
    return &PrometheusExporter{
        actorStarts: prometheus.NewCounter(prometheus.CounterOpts{
            Name: "goakt_actor_starts_total",
            Help: "Total number of actor starts",
        }),
        actorStops: prometheus.NewCounter(prometheus.CounterOpts{
            Name: "goakt_actor_stops_total",
            Help: "Total number of actor stops",
        }),
        actorRestarts: prometheus.NewCounter(prometheus.CounterOpts{
            Name: "goakt_actor_restarts_total",
            Help: "Total number of actor restarts",
        }),
    }
}

func (e *PrometheusExporter) Start(ctx context.Context, system actor.ActorSystem) {
    // Register metrics
    prometheus.MustRegister(e.actorStarts, e.actorStops, e.actorRestarts)

    subscriber, _ := system.Subscribe()
    defer system.Unsubscribe(subscriber)

    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            for event := range subscriber.Iterator() {
                e.recordMetric(event.Payload())
            }
        }
    }
}

func (e *PrometheusExporter) recordMetric(payload interface{}) {
    switch payload.(type) {
    case *goaktpb.ActorStarted:
        e.actorStarts.Inc()

    case *goaktpb.ActorStopped:
        e.actorStops.Inc()

    case *goaktpb.ActorRestarted:
        e.actorRestarts.Inc()
    }
}
```

### 3. Audit Logging

Log all actor lifecycle changes:

```go
type AuditLogger struct {
    logger *log.Logger
    file   *os.File
}

func NewAuditLogger(filename string) (*AuditLogger, error) {
    file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        return nil, err
    }

    return &AuditLogger{
        logger: log.New(file, "[AUDIT] ", log.LstdFlags),
        file:   file,
    }, nil
}

func (a *AuditLogger) Start(ctx context.Context, system actor.ActorSystem) {
    subscriber, _ := system.Subscribe()
    defer system.Unsubscribe(subscriber)
    defer a.file.Close()

    ticker := time.NewTicker(50 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            for event := range subscriber.Iterator() {
                a.logEvent(event)
            }
        }
    }
}

func (a *AuditLogger) logEvent(event *eventstream.Message) {
    payload := event.Payload()

    switch msg := payload.(type) {
    case *goaktpb.ActorStarted:
        a.logger.Printf("STARTED actor=%s time=%v",
            msg.GetAddress(),
            msg.GetStartedAt().AsTime())

    case *goaktpb.ActorStopped:
        a.logger.Printf("STOPPED actor=%s time=%v",
            msg.GetAddress(),
            msg.GetStoppedAt().AsTime())

    case *goaktpb.ActorChildCreated:
        a.logger.Printf("CHILD_CREATED child=%s parent=%s time=%v",
            msg.GetAddress(),
            msg.GetParent(),
            msg.GetCreatedAt().AsTime())

    case *goaktpb.ActorRestarted:
        a.logger.Printf("RESTARTED actor=%s time=%v",
            msg.GetAddress(),
            msg.GetRestartedAt().AsTime())

    case *goaktpb.ActorSuspended:
        a.logger.Printf("SUSPENDED actor=%s reason=%s time=%v",
            msg.GetAddress(),
            msg.GetReason(),
            msg.GetSuspendedAt().AsTime())
    }
}
```

### 4. Real-Time Dashboard

Stream events to a web dashboard:

```go
import (
    "encoding/json"
    "net/http"

    "github.com/gorilla/websocket"
)

type DashboardServer struct {
    upgrader websocket.Upgrader
    clients  []*websocket.Conn
    mu       sync.Mutex
}

func NewDashboardServer() *DashboardServer {
    return &DashboardServer{
        upgrader: websocket.Upgrader{
            CheckOrigin: func(r *http.Request) bool { return true },
        },
        clients: make([]*websocket.Conn, 0),
    }
}

func (d *DashboardServer) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
    conn, err := d.upgrader.Upgrade(w, r, nil)
    if err != nil {
        return
    }

    d.mu.Lock()
    d.clients = append(d.clients, conn)
    d.mu.Unlock()
}

func (d *DashboardServer) Start(ctx context.Context, system actor.ActorSystem) {
    subscriber, _ := system.Subscribe()
    defer system.Unsubscribe(subscriber)

    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            for event := range subscriber.Iterator() {
                d.broadcastEvent(event)
            }
        }
    }
}

func (d *DashboardServer) broadcastEvent(event *eventstream.Message) {
    data := map[string]interface{}{
        "topic":   event.Topic(),
        "payload": event.Payload(),
        "time":    time.Now(),
    }

    jsonData, _ := json.Marshal(data)

    d.mu.Lock()
    defer d.mu.Unlock()

    // Send to all connected clients
    for i := len(d.clients) - 1; i >= 0; i-- {
        conn := d.clients[i]
        if err := conn.WriteMessage(websocket.TextMessage, jsonData); err != nil {
            // Remove disconnected client
            d.clients = append(d.clients[:i], d.clients[i+1:]...)
        }
    }
}
```

## Best Practices

### 1. Always Unsubscribe

```go
subscriber, err := system.Subscribe()
if err != nil {
    return err
}
defer system.Unsubscribe(subscriber) // Clean up resources
```

### 2. Poll Regularly

The iterator returns a snapshot; poll for new events:

```go
ticker := time.NewTicker(100 * time.Millisecond)
defer ticker.Stop()

for {
    select {
    case <-ticker.C:
        for event := range subscriber.Iterator() {
            processEvent(event)
        }
    }
}
```

### 3. Handle Context Cancellation

```go
func consumeEvents(ctx context.Context, subscriber eventstream.Subscriber) {
    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return // Graceful shutdown
        case <-ticker.C:
            for event := range subscriber.Iterator() {
                processEvent(event)
            }
        }
    }
}
```

### 4. Filter Events

Only process relevant events:

```go
func filterCriticalEvents(event *eventstream.Message) bool {
    switch event.Payload().(type) {
    case *goaktpb.ActorSuspended:
        return true
    case *goaktpb.ActorRestarted:
        return true
    default:
        return false
    }
}

for event := range subscriber.Iterator() {
    if filterCriticalEvents(event) {
        alert(event)
    }
}
```

### 5. Non-Blocking Processing

Don't block the event loop:

```go
for event := range subscriber.Iterator() {
    event := event // Capture for goroutine

    // Process asynchronously
    go func() {
        processEvent(event)
    }()
}
```

## Complete Integration Example

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/eventstream"
)

// Comprehensive event processor
type EventProcessor struct {
    system      actor.ActorSystem
    subscriber  eventstream.Subscriber
    metrics     *SystemMetrics
    alerter     *Alerter
    logger      *AuditLogger
}

type SystemMetrics struct {
    ActorCount      int64
    StartCount      int64
    StopCount       int64
    RestartCount    int64
    SuspensionCount int64
}

func NewEventProcessor(system actor.ActorSystem) (*EventProcessor, error) {
    subscriber, err := system.Subscribe()
    if err != nil {
        return nil, err
    }

    return &EventProcessor{
        system:     system,
        subscriber: subscriber,
        metrics:    &SystemMetrics{},
        alerter:    NewAlerter(),
        logger:     NewLogger(),
    }, nil
}

func (p *EventProcessor) Start(ctx context.Context) {
    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()
    defer p.system.Unsubscribe(p.subscriber)

    for {
        select {
        case <-ctx.Done():
            log.Println("Event processor shutting down")
            return

        case <-ticker.C:
            for event := range p.subscriber.Iterator() {
                p.processEvent(event)
            }
        }
    }
}

func (p *EventProcessor) processEvent(event *eventstream.Message) {
    payload := event.Payload()

    switch msg := payload.(type) {
    case *goaktpb.ActorStarted:
        p.metrics.ActorCount++
        p.metrics.StartCount++
        p.logger.LogStart(msg.GetAddress(), msg.GetStartedAt().AsTime())

    case *goaktpb.ActorStopped:
        p.metrics.ActorCount--
        p.metrics.StopCount++
        p.logger.LogStop(msg.GetAddress(), msg.GetStoppedAt().AsTime())

    case *goaktpb.ActorRestarted:
        p.metrics.RestartCount++
        p.logger.LogRestart(msg.GetAddress(), msg.GetRestartedAt().AsTime())

        // Alert on frequent restarts
        if p.metrics.RestartCount > 10 {
            p.alerter.SendAlert(fmt.Sprintf(
                "High restart rate: %d restarts", p.metrics.RestartCount))
        }

    case *goaktpb.ActorSuspended:
        p.metrics.SuspensionCount++
        p.logger.LogSuspension(msg.GetAddress(), msg.GetReason())

        // Always alert on suspensions
        p.alerter.SendAlert(fmt.Sprintf(
            "Actor suspended: %s (reason: %s)",
            msg.GetAddress(),
            msg.GetReason()))

    case *goaktpb.ActorPassivated:
        p.logger.LogPassivation(msg.GetAddress(), msg.GetPassivatedAt().AsTime())
    }
}

func (p *EventProcessor) GetMetrics() *SystemMetrics {
    return p.metrics
}

func main() {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Create actor system
    system, err := actor.NewActorSystem("monitored-system")
    if err != nil {
        log.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    // Start event processor
    processor, err := NewEventProcessor(system)
    if err != nil {
        log.Fatal(err)
    }

    go processor.Start(ctx)

    // Run application actors
    for i := 0; i < 10; i++ {
        system.Spawn(ctx, fmt.Sprintf("worker-%d", i), new(WorkerActor))
    }

    // Periodic metrics reporting
    ticker := time.NewTicker(10 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            metrics := processor.GetMetrics()
            log.Printf("System Metrics: %+v\n", metrics)
        }
    }
}
```

## Troubleshooting

### Events Not Received

**Symptoms**: No events in iterator

**Solutions**:

- Verify actor system is started
- Check subscriber is active (`subscriber.Active()`)
- Ensure events are being generated (spawn actors)
- Call `Iterator()` repeatedly to poll for new events

### High Memory Usage

**Symptoms**: Event queue grows indefinitely

**Solutions**:

- Poll `Iterator()` more frequently
- Process events faster
- Reduce number of actors generating events
- Consider sampling (process every Nth event)

### Missed Events

**Symptoms**: Events generated but not in iterator

**Solutions**:

- `Iterator()` returns snapshot at call time
- Poll more frequently to avoid missing events
- Events arriving during iteration not included
- Use shorter polling intervals

## Limitations

1. **Local only**: Events not propagated across cluster nodes
2. **No persistence**: Events not stored
3. **Snapshot iteration**: `Iterator()` returns buffered messages at call time
4. **Unbounded queue**: No backpressure mechanism
5. **Single topic**: All events on same topic ("goakt.events")

## Next Steps

- [Usage Patterns](usage.md): More practical examples
- [PubSub Overview](../pubsub/overview.md): Actor-to-actor messaging
- [Actors Overview](../actors/overview.md): Understanding actor lifecycle
- [Testing](../testing/overview.md): Testing with events stream
