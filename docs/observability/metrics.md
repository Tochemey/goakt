# Metrics

GoAkt integrates with [OpenTelemetry](https://opentelemetry.io/) to expose runtime metrics for both the actor system and individual actors. Metrics are collected via observable counters and reported through the standard OpenTelemetry pipeline.

## Table of Contents

- ⚙️ [Enabling Metrics](#enabling-metrics)
- 📡 [Setting Up OpenTelemetry](#setting-up-opentelemetry)
- 📊 [Instrumentation](#instrumentation)
- 🏗️ [Actor System Metrics](#actor-system-metrics)
- 🎭 [Actor Metrics](#actor-metrics)
- 🔌 [Programmatic Metrics Access](#programmatic-metrics-access)
- 💡 [Complete Example](#complete-example)
- ✅ [Best Practices](#best-practices)

---

## Enabling Metrics

Enable metrics collection with the `WithMetrics()` option when creating the actor system:

```go
import "github.com/tochemey/goakt/v3/actor"

system, _ := actor.NewActorSystem("my-system",
    actor.WithMetrics(),
)
```

**Important:**
- **WithMetrics()** registers GoAkt instruments with the OpenTelemetry global `MeterProvider`.
- Metrics are only exported if you initialize the OpenTelemetry SDK with a configured exporter **before** starting the actor system.

## Setting Up OpenTelemetry

Initialize the OpenTelemetry SDK before starting GoAkt. Here is an example using the Prometheus exporter:

```go
import (
    "context"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/prometheus"
    sdkmetric "go.opentelemetry.io/otel/sdk/metric"

    "github.com/tochemey/goakt/v3/actor"
)

func main() {
    ctx := context.Background()

    // Create the Prometheus exporter
    exporter, err := prometheus.New()
    if err != nil {
        panic(err)
    }

    // Create a MeterProvider with the exporter
    provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
    defer provider.Shutdown(ctx)

    // Register the MeterProvider globally
    otel.SetMeterProvider(provider)

    // Now create the actor system with metrics enabled
    system, _ := actor.NewActorSystem("my-system",
        actor.WithMetrics(),
    )
    _ = system.Start(ctx)
    defer system.Stop(ctx)
}
```

### OTLP Exporter Example

For exporting to an OpenTelemetry Collector:

```go
import (
    "context"
    "time"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
    sdkmetric "go.opentelemetry.io/otel/sdk/metric"

    "github.com/tochemey/goakt/v3/actor"
)

func main() {
    ctx := context.Background()

    // Create an OTLP gRPC exporter
    exporter, err := otlpmetricgrpc.New(ctx,
        otlpmetricgrpc.WithEndpoint("localhost:4317"),
        otlpmetricgrpc.WithInsecure(),
    )
    if err != nil {
        panic(err)
    }

    // Create a MeterProvider with periodic reader
    provider := sdkmetric.NewMeterProvider(
        sdkmetric.WithReader(
            sdkmetric.NewPeriodicReader(exporter,
                sdkmetric.WithInterval(10*time.Second),
            ),
        ),
    )
    defer provider.Shutdown(ctx)

    otel.SetMeterProvider(provider)

    system, _ := actor.NewActorSystem("my-system",
        actor.WithMetrics(),
    )
    _ = system.Start(ctx)
    defer system.Stop(ctx)
}
```

## Instrumentation

GoAkt uses the instrumentation name `github.com/Tochemey/goakt/v3/telemetry` for all metric instruments. All metrics are registered as **Int64ObservableCounters** using OpenTelemetry's callback-based observation.

## Actor System Metrics

System-level metrics provide a high-level view of the actor system's health.

| Metric Name                     | Unit    | Description                                      |
|---------------------------------|---------|--------------------------------------------------|
| `actorsystem.actors.count`      | -       | Total number of live actors (PIDs) in the system |
| `actorsystem.deadletters.count` | -       | Total number of messages dropped to dead letters |
| `actorsystem.uptime`            | seconds | How long the actor system has been running       |
| `actorsystem.peers.count`       | -       | Number of connected peers (cluster mode only)    |

### Attributes

All actor system metrics include the following attribute:

| Attribute      | Description              |
|----------------|--------------------------|
| `actor.system` | Name of the actor system |

## Actor Metrics

Per-actor metrics provide fine-grained visibility into individual actor behavior. These are registered for every spawned actor when metrics are enabled.

| Metric Name                    | Unit    | Description                                               |
|--------------------------------|---------|-----------------------------------------------------------|
| `actor.children.count`         | -       | Number of direct child actors                             |
| `actor.stash.size`             | -       | Number of currently stashed messages                      |
| `actor.deadletters.count`      | -       | Messages dropped to dead letters by this actor            |
| `actor.restart.count`          | -       | Number of times the actor has been restarted              |
| `actor.last.received.duration` | ms      | Duration since the last message was processed             |
| `actor.processed.count`        | -       | Total number of messages processed                        |
| `actor.uptime`                 | seconds | How long the actor has been alive (resets on restart)     |
| `actor.failure.count`          | -       | Number of failures (panics/errors triggering supervision) |
| `actor.reinstate.count`        | -       | Number of suspended-to-resumed transitions                |

### Attributes

All actor metrics include the following attributes:

| Attribute       | Description                           |
|-----------------|---------------------------------------|
| `actor.system`  | Name of the actor system              |
| `actor.name`    | Name of the actor                     |
| `actor.kind`    | Type/kind of the actor (Go type name) |
| `actor.address` | Full address/ID of the actor          |

## Programmatic Metrics Access

In addition to OpenTelemetry export, GoAkt exposes point-in-time metric snapshots through the API. These are useful for health checks, dashboards, or lightweight telemetry without requiring OpenTelemetry setup.

### Actor System Snapshot

```go
ctx := context.Background()
m := system.Metric(ctx)
if m != nil {
    fmt.Printf("Actors:       %d\n", m.ActorsCount())
    fmt.Printf("Deadletters:  %d\n", m.DeadlettersCount())
    fmt.Printf("Uptime:       %d seconds\n", m.Uptime())
    fmt.Printf("Memory Used:  %d bytes\n", m.MemoryUsed())
    fmt.Printf("Memory Size:  %d bytes\n", m.MemorySize())
    fmt.Printf("Memory Avail: %d bytes\n", m.MemoryAvailable())
}
```

The `actor.Metric` struct provides:

| Method               | Return Type | Description                     |
|----------------------|-------------|---------------------------------|
| `ActorsCount()`      | `int64`     | Total live actors on this node  |
| `DeadlettersCount()` | `int64`     | Total dead letters on this node |
| `Uptime()`           | `int64`     | System uptime in seconds        |
| `MemoryUsed()`       | `uint64`    | Used memory in bytes            |
| `MemorySize()`       | `uint64`    | Total system memory in bytes    |
| `MemoryAvailable()`  | `uint64`    | Available memory in bytes       |

Returns `nil` if the system is not started.

### Actor Snapshot

```go
ctx := context.Background()
m := pid.Metric(ctx)
if m != nil {
    fmt.Printf("Processed:     %d\n", m.ProcessedCount())
    fmt.Printf("Children:      %d\n", m.ChidrenCount())
    fmt.Printf("Deadletters:   %d\n", m.DeadlettersCount())
    fmt.Printf("Restarts:      %d\n", m.RestartCount())
    fmt.Printf("Failures:      %d\n", m.FailureCount())
    fmt.Printf("Reinstates:    %d\n", m.ReinstateCount())
    fmt.Printf("Stash Size:    %d\n", m.StashSize())
    fmt.Printf("Uptime:        %d seconds\n", m.Uptime())
    fmt.Printf("Last Duration: %v\n", m.LatestProcessedDuration())
}
```

The `actor.ActorMetric` struct provides:

| Method                      | Return Type     | Description                                     |
|-----------------------------|-----------------|-------------------------------------------------|
| `ProcessedCount()`          | `uint64`        | Total messages processed                        |
| `ChidrenCount()`            | `uint64`        | Number of direct child actors                   |
| `DeadlettersCount()`        | `uint64`        | Messages dropped to dead letters                |
| `RestartCount()`            | `uint64`        | Number of restarts                              |
| `FailureCount()`            | `uint64`        | Number of failures observed                     |
| `ReinstateCount()`          | `uint64`        | Number of reinstatements (suspended -> resumed) |
| `StashSize()`               | `uint64`        | Currently stashed messages                      |
| `Uptime()`                  | `int64`         | Actor uptime in seconds (resets on restart)     |
| `LatestProcessedDuration()` | `time.Duration` | Duration of the most recent message processing  |

Returns `nil` if the actor is not running.

## Complete Example

A health endpoint that combines both OpenTelemetry export and programmatic access:

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/prometheus"
    sdkmetric "go.opentelemetry.io/otel/sdk/metric"
    promclient "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/log"
)

func main() {
    ctx := context.Background()

    // Set up Prometheus exporter
    exporter, _ := prometheus.New()
    provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
    defer provider.Shutdown(ctx)
    otel.SetMeterProvider(provider)

    // Create actor system with metrics
    system, _ := actor.NewActorSystem("orders",
        actor.WithLogger(log.DefaultLogger),
        actor.WithMetrics(),
    )
    _ = system.Start(ctx)
    defer system.Stop(ctx)

    // Prometheus metrics endpoint
    http.Handle("/metrics", promhttp.HandlerFor(
        promclient.DefaultGatherer,
        promhttp.HandlerOpts{},
    ))

    // Custom health endpoint using programmatic API
    http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        m := system.Metric(r.Context())
        if m == nil {
            http.Error(w, "system not started", http.StatusServiceUnavailable)
            return
        }
        json.NewEncoder(w).Encode(map[string]any{
            "status":       "healthy",
            "actors":       m.ActorsCount(),
            "deadletters":  m.DeadlettersCount(),
            "uptime_sec":   m.Uptime(),
            "memory_used":  m.MemoryUsed(),
            "memory_avail": m.MemoryAvailable(),
        })
    })

    fmt.Println("Serving on :8080")
    http.ListenAndServe(":8080", nil)
}
```

## Best Practices

1. **Initialize OTel SDK before starting the actor system** -- `WithMetrics()` uses `otel.GetMeterProvider()` which reads the global provider at construction time

2. **Use Prometheus for pull-based monitoring** -- Ideal for Kubernetes environments where a `/metrics` endpoint is scraped

3. **Use OTLP for push-based monitoring** -- Best for centralized collectors (Grafana Agent, OTel Collector, Datadog)

4. **Combine programmatic and OTel metrics** -- Use `system.Metric()` for health checks and readiness probes; use OTel for dashboards and alerting

5. **Monitor dead letters** -- A rising `actorsystem.deadletters.count` usually indicates actors being stopped prematurely or unhandled message types

6. **Watch failure and restart counts** -- High `actor.failure.count` or `actor.restart.count` values signal supervision issues worth investigating

7. **Use actor attributes for filtering** -- The `actor.kind` attribute allows grouping metrics by actor type across all instances