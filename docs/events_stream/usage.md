# Events Stream Usage Patterns

Practical patterns for using the events stream for monitoring, observability, and integration. See [Overview](overview.md) for API and system events.

## Table of Contents

- 📊 [Monitoring Patterns](#monitoring-patterns)
- 📈 [Metrics and Analytics](#metrics-and-analytics)
- 🔗 [Integration Examples](#integration-examples)
- 🚨 [Alerting Patterns](#alerting-patterns)
- ✅ [Best Practices](#best-practices)

---

## Monitoring Patterns

Subscribe, poll `Iterator()` on a ticker, and switch on `event.Payload()` to log or forward events. Use `ctx.Done()` in `select` for shutdown.

```go
func logEvents(ctx context.Context, system actor.ActorSystem) {
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
                switch msg := event.Payload().(type) {
                case *goaktpb.ActorStarted:
                    log.Printf("[START] %s", msg.GetAddress())
                case *goaktpb.ActorStopped:
                    log.Printf("[STOP] %s", msg.GetAddress())
                case *goaktpb.ActorSuspended:
                    log.Printf("[SUSPEND] %s: %s", msg.GetAddress(), msg.GetReason())
                }
            }
        }
    }
}
```

For structured output (e.g. JSON), build a map from `event.Topic()` and the payload type (e.g. `msg.GetAddress()`, `msg.GetStartedAt().AsTime()`), then encode and write.

## Metrics and Analytics

Maintain in-memory state keyed by actor address: on `ActorStarted` create an entry, on `ActorStopped`/`ActorRestarted`/`ActorSuspended` update counters and timestamps. Expose totals (starts, stops, restarts) and per-actor metrics (restart count, last restart time) for dashboards or Prometheus. Same poll loop as above; in the switch, update your structs and optionally call your metrics backend (e.g. `counter.Inc()`).

## Integration Examples

Use the same subscribe–poll–switch pattern; in each case branch, push to your backend:

- **Datadog / statsd:** Map event types to metric names (e.g. `goakt.actor.started`, `goakt.actor.restarted`) and call `client.Incr()` or equivalent.
- **Elasticsearch / logging:** Build a document from `event.Topic()` and payload fields (address, timestamps, reason); buffer and bulk-index on an interval or size threshold.
- **Prometheus:** Increment counters or update gauges per event type and optionally per actor address.

## Alerting Patterns

In your event loop, track per-actor restart/suspend counts and send an alert (e.g. webhook, Slack) when a threshold is exceeded:

```go
case *goaktpb.ActorSuspended:
    addr := msg.GetAddress()
    count := incrementCount(addr)
    if count >= alertThreshold {
        sendAlert("Actor suspended: %s (reason: %s)", addr, msg.GetReason())
    }
case *goaktpb.ActorRestarted:
    addr := msg.GetAddress()
    if restartCount(addr) >= restartThreshold {
        sendAlert("Actor %s restarted %d times", addr, restartCount(addr))
    }
```

For **rate-based alerts**, count events by type in a map, run a periodic check (e.g. every minute), compute events per minute, and alert when above a threshold; then reset counters.

## Best Practices

- **Poll regularly:** Use a ticker in the 50–100 ms range to avoid queue buildup; avoid multi-second intervals.
- **Don’t block the loop:** Process events quickly or hand off to a goroutine so the next poll isn’t delayed.
- **Handle unknown types:** Use a `default` case in the payload switch to log or ignore new event types.
- **Always unsubscribe:** Call `defer system.Unsubscribe(subscriber)` after `Subscribe()`.
- **Backpressure:** If processing is slow, limit batch size per poll (e.g. break after N events) and log when you hit the limit.