# Events Stream Usage Patterns

Practical patterns for using the events stream for monitoring, observability, and system integration.

## Monitoring Patterns

### Simple Event Logger

Log all actor lifecycle events:

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/eventstream"
    "github.com/tochemey/goakt/v3/goaktpb"
)

func logEvents(ctx context.Context, system actor.ActorSystem) {
    subscriber, err := system.Subscribe()
    if err != nil {
        log.Fatal(err)
    }
    defer system.Unsubscribe(subscriber)

    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            for event := range subscriber.Iterator() {
                logEvent(event)
            }
        }
    }
}

func logEvent(event *eventstream.Message) {
    payload := event.Payload()

    switch msg := payload.(type) {
    case *goaktpb.ActorStarted:
        log.Printf("[START] %s", msg.GetAddress())

    case *goaktpb.ActorStopped:
        log.Printf("[STOP] %s", msg.GetAddress())

    case *goaktpb.ActorChildCreated:
        log.Printf("[CHILD] %s -> %s", msg.GetParent(), msg.GetAddress())

    case *goaktpb.ActorPassivated:
        log.Printf("[PASSIVATE] %s", msg.GetAddress())

    case *goaktpb.ActorRestarted:
        log.Printf("[RESTART] %s", msg.GetAddress())

    case *goaktpb.ActorSuspended:
        log.Printf("[SUSPEND] %s: %s", msg.GetAddress(), msg.GetReason())
    }
}
```

### Structured Logging

Export events in structured format (JSON):

```go
type StructuredLogger struct {
    encoder *json.Encoder
}

func NewStructuredLogger(w io.Writer) *StructuredLogger {
    return &StructuredLogger{
        encoder: json.NewEncoder(w),
    }
}

func (l *StructuredLogger) Start(ctx context.Context, system actor.ActorSystem) {
    subscriber, _ := system.Subscribe()
    defer system.Unsubscribe(subscriber)

    ticker := time.NewTicker(50 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            for event := range subscriber.Iterator() {
                l.logStructured(event)
            }
        }
    }
}

func (l *StructuredLogger) logStructured(event *eventstream.Message) {
    entry := map[string]interface{}{
        "timestamp": time.Now().Format(time.RFC3339),
        "topic":     event.Topic(),
    }

    payload := event.Payload()

    switch msg := payload.(type) {
    case *goaktpb.ActorStarted:
        entry["event"] = "actor.started"
        entry["actor"] = msg.GetAddress()
        entry["started_at"] = msg.GetStartedAt().AsTime()

    case *goaktpb.ActorStopped:
        entry["event"] = "actor.stopped"
        entry["actor"] = msg.GetAddress()
        entry["stopped_at"] = msg.GetStoppedAt().AsTime()

    case *goaktpb.ActorSuspended:
        entry["event"] = "actor.suspended"
        entry["actor"] = msg.GetAddress()
        entry["reason"] = msg.GetReason()
        entry["suspended_at"] = msg.GetSuspendedAt().AsTime()
        entry["severity"] = "warning"

    default:
        entry["event"] = fmt.Sprintf("%T", payload)
    }

    l.encoder.Encode(entry)
}
```

## Metrics and Analytics

### Actor Lifecycle Tracker

Track actor lifetimes and patterns:

```go
type LifecycleTracker struct {
    mu           sync.RWMutex
    actors       map[string]*ActorLifecycle
    totalStarts  int64
    totalStops   int64
    totalRestarts int64
}

type ActorLifecycle struct {
    Address      string
    StartedAt    time.Time
    StoppedAt    *time.Time
    RestartCount int
    Children     []string
    Uptime       time.Duration
}

func NewLifecycleTracker() *LifecycleTracker {
    return &LifecycleTracker{
        actors: make(map[string]*ActorLifecycle),
    }
}

func (t *LifecycleTracker) Start(ctx context.Context, system actor.ActorSystem) {
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
                t.trackEvent(event)
            }
        }
    }
}

func (t *LifecycleTracker) trackEvent(event *eventstream.Message) {
    t.mu.Lock()
    defer t.mu.Unlock()

    payload := event.Payload()

    switch msg := payload.(type) {
    case *goaktpb.ActorStarted:
        addr := msg.GetAddress()
        t.actors[addr] = &ActorLifecycle{
            Address:   addr,
            StartedAt: msg.GetStartedAt().AsTime(),
            Children:  make([]string, 0),
        }
        t.totalStarts++

    case *goaktpb.ActorStopped:
        addr := msg.GetAddress()
        if lc, ok := t.actors[addr]; ok {
            stoppedAt := msg.GetStoppedAt().AsTime()
            lc.StoppedAt = &stoppedAt
            lc.Uptime = stoppedAt.Sub(lc.StartedAt)
        }
        t.totalStops++

    case *goaktpb.ActorChildCreated:
        parent := msg.GetParent()
        child := msg.GetAddress()

        if lc, ok := t.actors[parent]; ok {
            lc.Children = append(lc.Children, child)
        }

    case *goaktpb.ActorRestarted:
        addr := msg.GetAddress()
        if lc, ok := t.actors[addr]; ok {
            lc.RestartCount++
        }
        t.totalRestarts++
    }
}

func (t *LifecycleTracker) GetStats() map[string]interface{} {
    t.mu.RLock()
    defer t.mu.RUnlock()

    return map[string]interface{}{
        "total_actors":   len(t.actors),
        "total_starts":   t.totalStarts,
        "total_stops":    t.totalStops,
        "total_restarts": t.totalRestarts,
    }
}

func (t *LifecycleTracker) GetActorLifecycle(address string) *ActorLifecycle {
    t.mu.RLock()
    defer t.mu.RUnlock()

    return t.actors[address]
}
```

### Performance Monitor

Track actor performance metrics:

```go
type PerformanceMonitor struct {
    mu              sync.RWMutex
    actorMetrics    map[string]*ActorMetrics
    restartThreshold int
}

type ActorMetrics struct {
    Address       string
    RestartCount  int
    LastRestart   time.Time
    SuspendCount  int
    LastSuspend   time.Time
    AvgUptime     time.Duration
    UptimeHistory []time.Duration
}

func (pm *PerformanceMonitor) Start(ctx context.Context, system actor.ActorSystem) {
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
                pm.analyzeEvent(event)
            }
        }
    }
}

func (pm *PerformanceMonitor) analyzeEvent(event *eventstream.Message) {
    pm.mu.Lock()
    defer pm.mu.Unlock()

    payload := event.Payload()

    switch msg := payload.(type) {
    case *goaktpb.ActorRestarted:
        addr := msg.GetAddress()
        metrics := pm.getOrCreateMetrics(addr)

        metrics.RestartCount++
        metrics.LastRestart = msg.GetRestartedAt().AsTime()

        // Alert if restart count exceeds threshold
        if metrics.RestartCount >= pm.restartThreshold {
            log.Printf("ALERT: Actor %s has restarted %d times",
                addr, metrics.RestartCount)
        }

    case *goaktpb.ActorSuspended:
        addr := msg.GetAddress()
        metrics := pm.getOrCreateMetrics(addr)

        metrics.SuspendCount++
        metrics.LastSuspend = msg.GetSuspendedAt().AsTime()

        log.Printf("WARNING: Actor %s suspended: %s", addr, msg.GetReason())

    case *goaktpb.ActorStopped:
        addr := msg.GetAddress()
        if metrics, ok := pm.actorMetrics[addr]; ok {
            // Calculate uptime (if we tracked start time)
            // Update average uptime
        }
    }
}

func (pm *PerformanceMonitor) getOrCreateMetrics(addr string) *ActorMetrics {
    if metrics, ok := pm.actorMetrics[addr]; ok {
        return metrics
    }

    metrics := &ActorMetrics{
        Address:       addr,
        UptimeHistory: make([]time.Duration, 0),
    }
    pm.actorMetrics[addr] = metrics
    return metrics
}

func (pm *PerformanceMonitor) GetActorMetrics(addr string) *ActorMetrics {
    pm.mu.RLock()
    defer pm.mu.RUnlock()

    return pm.actorMetrics[addr]
}
```

## Integration Examples

### Exporting to External Systems

#### Datadog Integration

```go
import "github.com/DataDog/datadog-go/statsd"

type DatadogReporter struct {
    client *statsd.Client
}

func NewDatadogReporter(addr string) (*DatadogReporter, error) {
    client, err := statsd.New(addr)
    if err != nil {
        return nil, err
    }

    return &DatadogReporter{client: client}, nil
}

func (r *DatadogReporter) Start(ctx context.Context, system actor.ActorSystem) {
    subscriber, _ := system.Subscribe()
    defer system.Unsubscribe(subscriber)

    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            r.client.Close()
            return
        case <-ticker.C:
            for event := range subscriber.Iterator() {
                r.reportEvent(event)
            }
        }
    }
}

func (r *DatadogReporter) reportEvent(event *eventstream.Message) {
    payload := event.Payload()

    switch payload.(type) {
    case *goaktpb.ActorStarted:
        r.client.Incr("goakt.actor.started", nil, 1)

    case *goaktpb.ActorStopped:
        r.client.Incr("goakt.actor.stopped", nil, 1)

    case *goaktpb.ActorRestarted:
        r.client.Incr("goakt.actor.restarted", nil, 1)

    case *goaktpb.ActorSuspended:
        r.client.Incr("goakt.actor.suspended", nil, 1)
    }
}
```

#### Elasticsearch Integration

```go
import "github.com/elastic/go-elasticsearch/v8"

type ElasticsearchExporter struct {
    client *elasticsearch.Client
    index  string
    buffer []map[string]interface{}
    mu     sync.Mutex
}

func (e *ElasticsearchExporter) Start(ctx context.Context, system actor.ActorSystem) {
    subscriber, _ := system.Subscribe()
    defer system.Unsubscribe(subscriber)

    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()

    flushTicker := time.NewTicker(5 * time.Second)
    defer flushTicker.Stop()

    for {
        select {
        case <-ctx.Done():
            e.flush()
            return

        case <-ticker.C:
            for event := range subscriber.Iterator() {
                e.bufferEvent(event)
            }

        case <-flushTicker.C:
            e.flush()
        }
    }
}

func (e *ElasticsearchExporter) bufferEvent(event *eventstream.Message) {
    doc := map[string]interface{}{
        "timestamp": time.Now(),
        "topic":     event.Topic(),
    }

    payload := event.Payload()

    switch msg := payload.(type) {
    case *goaktpb.ActorStarted:
        doc["event"] = "actor.started"
        doc["actor"] = msg.GetAddress()
        doc["started_at"] = msg.GetStartedAt().AsTime()

    case *goaktpb.ActorStopped:
        doc["event"] = "actor.stopped"
        doc["actor"] = msg.GetAddress()
        doc["stopped_at"] = msg.GetStoppedAt().AsTime()

    case *goaktpb.ActorSuspended:
        doc["event"] = "actor.suspended"
        doc["actor"] = msg.GetAddress()
        doc["reason"] = msg.GetReason()
        doc["severity"] = "error"
    }

    e.mu.Lock()
    e.buffer = append(e.buffer, doc)
    e.mu.Unlock()
}

func (e *ElasticsearchExporter) flush() {
    e.mu.Lock()
    if len(e.buffer) == 0 {
        e.mu.Unlock()
        return
    }

    docs := e.buffer
    e.buffer = make([]map[string]interface{}, 0)
    e.mu.Unlock()

    // Bulk index to Elasticsearch
    for _, doc := range docs {
        // Index document
        // ... Elasticsearch bulk API
    }
}
```

## Alerting Patterns

### Alert on Critical Events

```go
type AlertManager struct {
    suspendedActors map[string]int
    alertThreshold  int
    slackWebhook    string
}

func NewAlertManager(webhook string, threshold int) *AlertManager {
    return &AlertManager{
        suspendedActors: make(map[string]int),
        alertThreshold:  threshold,
        slackWebhook:    webhook,
    }
}

func (am *AlertManager) Start(ctx context.Context, system actor.ActorSystem) {
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
                am.checkAlert(event)
            }
        }
    }
}

func (am *AlertManager) checkAlert(event *eventstream.Message) {
    payload := event.Payload()

    switch msg := payload.(type) {
    case *goaktpb.ActorSuspended:
        addr := msg.GetAddress()
        am.suspendedActors[addr]++

        // Alert on first suspension
        am.sendAlert(fmt.Sprintf(
            "🚨 Actor suspended: %s\nReason: %s\nCount: %d",
            addr,
            msg.GetReason(),
            am.suspendedActors[addr],
        ))

    case *goaktpb.ActorRestarted:
        addr := msg.GetAddress()

        // Alert on excessive restarts
        if count, ok := am.suspendedActors[addr]; ok && count >= am.alertThreshold {
            am.sendAlert(fmt.Sprintf(
                "⚠️ Actor %s has been restarted %d times",
                addr,
                count,
            ))
        }
    }
}

func (am *AlertManager) sendAlert(message string) {
    payload := map[string]string{
        "text": message,
    }

    data, _ := json.Marshal(payload)
    http.Post(am.slackWebhook, "application/json", bytes.NewReader(data))
}
```

### Rate-Based Alerts

Alert on high event rates:

```go
type RateMonitor struct {
    eventCounts   map[string]int64
    lastCheck     time.Time
    rateThreshold int64 // Events per minute
}

func (rm *RateMonitor) Start(ctx context.Context, system actor.ActorSystem) {
    subscriber, _ := system.Subscribe()
    defer system.Unsubscribe(subscriber)

    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()

    checkTicker := time.NewTicker(time.Minute)
    defer checkTicker.Stop()

    for {
        select {
        case <-ctx.Done():
            return

        case <-ticker.C:
            for event := range subscriber.Iterator() {
                rm.countEvent(event)
            }

        case <-checkTicker.C:
            rm.checkRates()
        }
    }
}

func (rm *RateMonitor) countEvent(event *eventstream.Message) {
    payload := event.Payload()

    eventType := fmt.Sprintf("%T", payload)
    rm.eventCounts[eventType]++
}

func (rm *RateMonitor) checkRates() {
    elapsed := time.Since(rm.lastCheck)
    rate := elapsed.Minutes()

    for eventType, count := range rm.eventCounts {
        eventsPerMinute := int64(float64(count) / rate)

        if eventsPerMinute > rm.rateThreshold {
            log.Printf("HIGH RATE ALERT: %s = %d events/min (threshold: %d)",
                eventType,
                eventsPerMinute,
                rm.rateThreshold,
            )
        }
    }

    // Reset counters
    rm.eventCounts = make(map[string]int64)
    rm.lastCheck = time.Now()
}
```

## Testing Support

### Test Event Collector

Collect events during tests for verification:

```go
type TestEventCollector struct {
    events []interface{}
    mu     sync.Mutex
}

func NewTestEventCollector() *TestEventCollector {
    return &TestEventCollector{
        events: make([]interface{}, 0),
    }
}

func (c *TestEventCollector) Collect(ctx context.Context, system actor.ActorSystem, duration time.Duration) {
    subscriber, _ := system.Subscribe()
    defer system.Unsubscribe(subscriber)

    ticker := time.NewTicker(10 * time.Millisecond)
    defer ticker.Stop()

    timeout := time.After(duration)

    for {
        select {
        case <-ctx.Done():
            return
        case <-timeout:
            return
        case <-ticker.C:
            for event := range subscriber.Iterator() {
                c.mu.Lock()
                c.events = append(c.events, event.Payload())
                c.mu.Unlock()
            }
        }
    }
}

func (c *TestEventCollector) GetEvents() []interface{} {
    c.mu.Lock()
    defer c.mu.Unlock()

    return append([]interface{}{}, c.events...)
}

func (c *TestEventCollector) CountEventType(eventType interface{}) int {
    c.mu.Lock()
    defer c.mu.Unlock()

    count := 0
    targetType := fmt.Sprintf("%T", eventType)

    for _, event := range c.events {
        if fmt.Sprintf("%T", event) == targetType {
            count++
        }
    }

    return count
}

// Usage in tests
func TestActorLifecycle(t *testing.T) {
    ctx := context.Background()

    system, _ := actor.NewActorSystem("test")
    system.Start(ctx)
    defer system.Stop(ctx)

    // Collect events
    collector := NewTestEventCollector()
    go collector.Collect(ctx, system, 5*time.Second)

    // Spawn and stop actor
    pid, _ := system.Spawn(ctx, "test-actor", new(MyActor))
    time.Sleep(100 * time.Millisecond)
    pid.Shutdown(ctx)
    time.Sleep(100 * time.Millisecond)

    // Verify events
    events := collector.GetEvents()

    startCount := collector.CountEventType(&goaktpb.ActorStarted{})
    stopCount := collector.CountEventType(&goaktpb.ActorStopped{})

    assert.Equal(t, 1, startCount)
    assert.Equal(t, 1, stopCount)
}
```

## Best Practices

### 1. Regular Polling

Poll the iterator frequently to avoid message buildup:

```go
// Good: Frequent polling
ticker := time.NewTicker(50 * time.Millisecond)

// Acceptable: Moderate polling
ticker := time.NewTicker(100 * time.Millisecond)

// Bad: Infrequent polling (messages may accumulate)
ticker := time.NewTicker(5 * time.Second)
```

### 2. Process Asynchronously

Don't block the polling loop:

```go
for event := range subscriber.Iterator() {
    event := event // Capture

    go func() {
        // Process in background
        processEvent(event)
    }()
}
```

### 3. Handle All Event Types

Use a default case for future event types:

```go
switch msg := payload.(type) {
case *goaktpb.ActorStarted:
    // Handle
case *goaktpb.ActorStopped:
    // Handle
default:
    log.Printf("Unknown event type: %T", msg)
}
```

### 4. Clean Up Resources

Always unsubscribe when done:

```go
subscriber, err := system.Subscribe()
if err != nil {
    return err
}
defer system.Unsubscribe(subscriber) // Essential cleanup
```

### 5. Buffer Management

Handle potential message buildup:

```go
func consumeWithBackpressure(ctx context.Context, subscriber eventstream.Subscriber) {
    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            processed := 0
            for event := range subscriber.Iterator() {
                processEvent(event)
                processed++

                // Limit batch size
                if processed >= 1000 {
                    log.Println("Warning: High event rate, limiting batch")
                    break
                }
            }
        }
    }
}
```

## Next Steps

- [Events Stream Overview](overview.md): Understand event stream fundamentals
- [PubSub Overview](../pubsub/overview.md): Actor-to-actor pub/sub messaging
- [Observability](../observability/metrics.md): Metrics and monitoring
- [Testing](../testing/overview.md): Using events stream in tests
