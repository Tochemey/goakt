# Coordinated Shutdown

Coordinated shutdown enables graceful cleanup of resources when the actor system stops. It allows you to register shutdown hooks that execute in order, with configurable retry and error handling strategies.

## What is Coordinated Shutdown?

**Coordinated shutdown** provides:

- **Ordered execution**: Hooks run in registration order
- **Error handling**: Configurable retry and failure strategies
- **Resource cleanup**: Flush data, close connections, save state
- **Graceful termination**: Ensures clean shutdown
- **Flexibility**: Skip or retry failed hooks

Without coordinated shutdown, you'd need to manually manage cleanup sequences, handle failures, and ensure proper ordering.

## ShutdownHook Interface

Implement the `ShutdownHook` interface to define custom cleanup logic:

```go
type ShutdownHook interface {
    Execute(ctx context.Context, actorSystem ActorSystem) error
    Recovery() *ShutdownHookRecovery
}
```

### Execute

The `Execute` method contains your cleanup logic:

```go
func (h *MyHook) Execute(ctx context.Context, actorSystem ActorSystem) error {
    // Perform cleanup
    return nil
}
```

**Parameters:**

- `ctx` - Context with shutdown timeout
- `actorSystem` - Reference to the actor system

**Return:** Error if cleanup fails

### Recovery

The `Recovery` method defines how to handle failures:

```go
func (h *MyHook) Recovery() *ShutdownHookRecovery {
    return actor.NewShutdownHookRecovery(
        actor.WithShutdownHookRetry(3, time.Second),
        actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndSkip))
}
```

## Recovery Strategies

### ShouldFail

Stop immediately on failure, don't execute remaining hooks.

```go
actor.NewShutdownHookRecovery(
    actor.WithShutdownHookRecoveryStrategy(actor.ShouldFail))
```

**Use when:**

- Hook is critical
- Subsequent hooks depend on this
- Complete shutdown on any failure

**Behavior:**

```
Hook1: Success → Continue
Hook2: FAIL → STOP (Hook3 not executed)
```

### ShouldRetryAndFail

Retry on failure, then stop if still unsuccessful.

```go
actor.NewShutdownHookRecovery(
    actor.WithShutdownHookRetry(3, time.Second),
    actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndFail))
```

**Use when:**

- Hook is critical but may have transient failures
- Need retry logic
- Stop if retries exhausted

**Behavior:**

```
Hook1: Success → Continue
Hook2: FAIL → Retry (3x) → Still failing → STOP
```

### ShouldSkip

Log error and continue with next hook.

```go
actor.NewShutdownHookRecovery(
    actor.WithShutdownHookRecoveryStrategy(actor.ShouldSkip))
```

**Use when:**

- Hook is optional
- Other hooks are independent
- Best-effort cleanup

**Behavior:**

```
Hook1: Success → Continue
Hook2: FAIL → Log error → Continue
Hook3: Executed
```

### ShouldRetryAndSkip

Retry on failure, then skip and continue if unsuccessful.

```go
actor.NewShutdownHookRecovery(
    actor.WithShutdownHookRetry(2, time.Second),
    actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndSkip))
```

**Use when:**

- Want to try but not block shutdown
- Best-effort with retry
- Independent hooks

**Behavior:**

```
Hook1: Success → Continue
Hook2: FAIL → Retry (2x) → Still failing → Log → Continue
Hook3: Executed
```

## Creating Shutdown Hooks

### Basic Hook

```go
type FlushMetricsHook struct{}

func (h *FlushMetricsHook) Execute(ctx context.Context,
    actorSystem actor.ActorSystem) error {

    log.Println("Flushing metrics...")

    // Flush metrics to storage
    if err := flushMetrics(); err != nil {
        return fmt.Errorf("failed to flush metrics: %w", err)
    }

    log.Println("Metrics flushed")
    return nil
}

func (h *FlushMetricsHook) Recovery() *actor.ShutdownHookRecovery {
    return actor.NewShutdownHookRecovery(
        actor.WithShutdownHookRetry(3, time.Second),
        actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndSkip))
}
```

### Database Connection Hook

```go
type CloseDBHook struct {
    db *sql.DB
}

func (h *CloseDBHook) Execute(ctx context.Context,
    actorSystem actor.ActorSystem) error {

    log.Println("Closing database connections...")

    if h.db == nil {
        return nil
    }

    if err := h.db.Close(); err != nil {
        return fmt.Errorf("failed to close database: %w", err)
    }

    log.Println("Database connections closed")
    return nil
}

func (h *CloseDBHook) Recovery() *actor.ShutdownHookRecovery {
    return actor.NewShutdownHookRecovery(
        actor.WithShutdownHookRetry(2, time.Second),
        actor.WithShutdownHookRecoveryStrategy(actor.ShouldFail))
}
```

### State Persistence Hook

```go
type SaveStateHook struct {
    stateManager *StateManager
}

func (h *SaveStateHook) Execute(ctx context.Context,
    actorSystem actor.ActorSystem) error {

    log.Println("Saving application state...")

    // Get all actors
    actors := actorSystem.Actors()

    // Save state for each actor
    for _, pid := range actors {
        if err := h.stateManager.SaveActorState(pid); err != nil {
            return fmt.Errorf("failed to save state for %s: %w",
                pid.Name(), err)
        }
    }

    log.Printf("Saved state for %d actors\n", len(actors))
    return nil
}

func (h *SaveStateHook) Recovery() *actor.ShutdownHookRecovery {
    return actor.NewShutdownHookRecovery(
        actor.WithShutdownHookRetry(5, 2*time.Second),
        actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndFail))
}
```

### HTTP Server Shutdown Hook

```go
type HTTPServerHook struct {
    server *http.Server
}

func (h *HTTPServerHook) Execute(ctx context.Context,
    actorSystem actor.ActorSystem) error {

    log.Println("Shutting down HTTP server...")

    if h.server == nil {
        return nil
    }

    // Graceful shutdown with timeout
    shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
    defer cancel()

    if err := h.server.Shutdown(shutdownCtx); err != nil {
        return fmt.Errorf("HTTP server shutdown failed: %w", err)
    }

    log.Println("HTTP server stopped")
    return nil
}

func (h *HTTPServerHook) Recovery() *actor.ShutdownHookRecovery {
    return actor.NewShutdownHookRecovery(
        actor.WithShutdownHookRetry(2, time.Second),
        actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndSkip))
}
```

## Registering Shutdown Hooks

### At System Creation

```go
actorSystem, err := actor.NewActorSystem("MySystem",
    actor.WithCoordinatedShutdown(
        &FlushMetricsHook{},
        &SaveStateHook{db: database},
        &CloseDBHook{db: database},
        &HTTPServerHook{server: httpServer}))
```

**Execution order:** Same as registration order

### Hook Execution Flow

```
Stop() called
    ↓
Execute Hook1 → Success
    ↓
Execute Hook2 → Fail → Retry → Success
    ↓
Execute Hook3 → Fail → Retry → Fail → (Strategy: Skip)
    ↓
Execute Hook4 → Success
    ↓
Stop internal actors
    ↓
Shutdown complete
```

## Complete Example

```go
package main

import (
    "context"
    "database/sql"
    "fmt"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/log"
)

// Metrics flush hook
type FlushMetricsHook struct {
    metricsWriter *MetricsWriter
}

func (h *FlushMetricsHook) Execute(ctx context.Context,
    system actor.ActorSystem) error {

    log.Println("Flushing metrics...")

    metrics := system.Metric(ctx)
    if err := h.metricsWriter.Write(metrics); err != nil {
        return err
    }

    log.Println("Metrics flushed successfully")
    return nil
}

func (h *FlushMetricsHook) Recovery() *actor.ShutdownHookRecovery {
    // Retry 3 times, then skip if still failing
    return actor.NewShutdownHookRecovery(
        actor.WithShutdownHookRetry(3, time.Second),
        actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndSkip))
}

// Database cleanup hook
type DatabaseCleanupHook struct {
    db *sql.DB
}

func (h *DatabaseCleanupHook) Execute(ctx context.Context,
    system actor.ActorSystem) error {

    log.Println("Cleaning up database connections...")

    // Close all prepared statements
    // Flush pending transactions
    // Close connection pool

    if err := h.db.Close(); err != nil {
        return fmt.Errorf("database cleanup failed: %w", err)
    }

    log.Println("Database cleanup complete")
    return nil
}

func (h *DatabaseCleanupHook) Recovery() *actor.ShutdownHookRecovery {
    // Critical: must succeed
    return actor.NewShutdownHookRecovery(
        actor.WithShutdownHookRetry(5, 2*time.Second),
        actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndFail))
}

// HTTP server shutdown hook
type HTTPShutdownHook struct {
    server *http.Server
}

func (h *HTTPShutdownHook) Execute(ctx context.Context,
    system actor.ActorSystem) error {

    log.Println("Stopping HTTP server...")

    shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
    defer cancel()

    if err := h.server.Shutdown(shutdownCtx); err != nil {
        return fmt.Errorf("HTTP shutdown failed: %w", err)
    }

    log.Println("HTTP server stopped")
    return nil
}

func (h *HTTPShutdownHook) Recovery() *actor.ShutdownHookRecovery {
    return actor.NewShutdownHookRecovery(
        actor.WithShutdownHookRetry(2, time.Second),
        actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndSkip))
}

func main() {
    ctx := context.Background()
    logger := log.DefaultLogger

    // Setup dependencies
    db, _ := sql.Open("postgres", connectionString)
    metricsWriter := NewMetricsWriter()
    httpServer := &http.Server{Addr: ":8080"}

    // Create system with coordinated shutdown
    actorSystem, err := actor.NewActorSystem("MyApp",
        actor.WithLogger(logger),
        actor.WithShutdownTimeout(30*time.Second),
        actor.WithCoordinatedShutdown(
            // Order matters!
            &FlushMetricsHook{metricsWriter: metricsWriter},
            &HTTPShutdownHook{server: httpServer},
            &DatabaseCleanupHook{db: db},
        ))

    if err != nil {
        logger.Fatal("Failed to create system", "error", err)
    }

    // Start system
    if err := actorSystem.Start(ctx); err != nil {
        logger.Fatal("Failed to start", "error", err)
    }

    logger.Info("System started, spawning actors...")

    // Spawn actors
    workerPID, _ := actorSystem.Spawn(ctx, "worker", &WorkerActor{})

    // Start HTTP server in background
    go func() {
        if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
            logger.Error("HTTP server error", "error", err)
        }
    }()

    logger.Info("Application running")

    // Wait for shutdown signal
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
    <-sigCh

    logger.Info("Shutdown signal received, stopping...")

    // Stop system - triggers coordinated shutdown
    // Hooks execute in order:
    // 1. FlushMetricsHook
    // 2. HTTPShutdownHook
    // 3. DatabaseCleanupHook
    if err := actorSystem.Stop(ctx); err != nil {
        logger.Error("Shutdown error", "error", err)
        os.Exit(1)
    }

    logger.Info("Shutdown complete")
    os.Exit(0)
}
```

## Hook Execution Order

Hooks execute in the **order they are registered**:

```go
actor.WithCoordinatedShutdown(
    &Hook1{},  // Executes first
    &Hook2{},  // Executes second
    &Hook3{},  // Executes third
)
```

**Design your order based on dependencies:**

```
1. Stop accepting new requests (HTTP server)
2. Flush in-flight work (queues, buffers)
3. Save state (databases, files)
4. Close connections (DB, cache, message bus)
5. Final cleanup (temp files, logs)
```

## Recovery Configuration

### WithShutdownHookRetry

Configure retry behavior:

```go
recovery := actor.NewShutdownHookRecovery(
    actor.WithShutdownHookRetry(3, 2*time.Second))
```

**Parameters:**

- `retries` - Number of retry attempts
- `interval` - Delay between retries

**Default:** 3 retries with 1 second interval

### WithShutdownHookRecoveryStrategy

Set failure handling strategy:

```go
recovery := actor.NewShutdownHookRecovery(
    actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndSkip))
```

**Strategies:**

- `ShouldFail` - Stop on failure
- `ShouldRetryAndFail` - Retry then stop
- `ShouldSkip` - Skip and continue
- `ShouldRetryAndSkip` - Retry then skip

**Default:** `ShouldFail`

## Common Patterns

### Pattern 1: Critical Resource Cleanup

```go
type CriticalCleanupHook struct {
    resource *CriticalResource
}

func (h *CriticalCleanupHook) Execute(ctx context.Context,
    system actor.ActorSystem) error {

    // Must succeed
    if err := h.resource.Close(); err != nil {
        return err
    }
    return nil
}

func (h *CriticalCleanupHook) Recovery() *actor.ShutdownHookRecovery {
    // Retry aggressively, fail if can't complete
    return actor.NewShutdownHookRecovery(
        actor.WithShutdownHookRetry(10, time.Second),
        actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndFail))
}
```

### Pattern 2: Best-Effort Cleanup

```go
type CacheFlushHook struct {
    cache *Cache
}

func (h *CacheFlushHook) Execute(ctx context.Context,
    system actor.ActorSystem) error {

    return h.cache.Flush()
}

func (h *CacheFlushHook) Recovery() *actor.ShutdownHookRecovery {
    // Try a few times, but skip if failing
    return actor.NewShutdownHookRecovery(
        actor.WithShutdownHookRetry(2, 500*time.Millisecond),
        actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndSkip))
}
```

### Pattern 3: Ordered Dependencies

```go
// Hook 1: Stop accepting new work
type StopIngressHook struct {
    httpServer *http.Server
}

func (h *StopIngressHook) Execute(ctx context.Context,
    system actor.ActorSystem) error {

    log.Println("Stopping HTTP server...")
    return h.httpServer.Shutdown(ctx)
}

func (h *StopIngressHook) Recovery() *actor.ShutdownHookRecovery {
    return actor.NewShutdownHookRecovery(
        actor.WithShutdownHookRetry(3, time.Second),
        actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndFail))
}

// Hook 2: Complete in-flight work
type DrainWorkQueueHook struct {
    queue *WorkQueue
}

func (h *DrainWorkQueueHook) Execute(ctx context.Context,
    system actor.ActorSystem) error {

    log.Println("Draining work queue...")
    return h.queue.DrainWithTimeout(ctx, 10*time.Second)
}

func (h *DrainWorkQueueHook) Recovery() *actor.ShutdownHookRecovery {
    return actor.NewShutdownHookRecovery(
        actor.WithShutdownHookRetry(2, time.Second),
        actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndSkip))
}

// Hook 3: Persist state
type PersistStateHook struct {
    db *sql.DB
}

func (h *PersistStateHook) Execute(ctx context.Context,
    system actor.ActorSystem) error {

    log.Println("Persisting application state...")

    // Save critical state
    actors := system.Actors()
    for _, pid := range actors {
        // Save actor state
    }

    return nil
}

func (h *PersistStateHook) Recovery() *actor.ShutdownHookRecovery {
    return actor.NewShutdownHookRecovery(
        actor.WithShutdownHookRetry(5, time.Second),
        actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndFail))
}

// Register in order
actorSystem, _ := actor.NewActorSystem("MyApp",
    actor.WithCoordinatedShutdown(
        &StopIngressHook{httpServer: server},      // 1st: Stop new requests
        &DrainWorkQueueHook{queue: workQueue},     // 2nd: Finish pending
        &PersistStateHook{db: database},           // 3rd: Save state
    ))
```

### Pattern 4: Notification Hook

```go
type NotifyShutdownHook struct {
    webhookURL string
}

func (h *NotifyShutdownHook) Execute(ctx context.Context,
    system actor.ActorSystem) error {

    log.Println("Sending shutdown notification...")

    notification := map[string]interface{}{
        "system":    system.Name(),
        "timestamp": time.Now(),
        "reason":    "graceful_shutdown",
    }

    return sendWebhook(h.webhookURL, notification)
}

func (h *NotifyShutdownHook) Recovery() *actor.ShutdownHookRecovery {
    // Best effort - don't block shutdown
    return actor.NewShutdownHookRecovery(
        actor.WithShutdownHookRetry(1, 500*time.Millisecond),
        actor.WithShutdownHookRecoveryStrategy(actor.ShouldSkip))
}
```

### Pattern 5: Timeout-Aware Hook

```go
type TimeoutAwareHook struct {
    operation func(context.Context) error
}

func (h *TimeoutAwareHook) Execute(ctx context.Context,
    system actor.ActorSystem) error {

    // Check remaining time from shutdown timeout
    deadline, ok := ctx.Deadline()
    if ok {
        remaining := time.Until(deadline)
        log.Printf("Executing hook with %v remaining\n", remaining)

        if remaining < time.Second {
            return fmt.Errorf("insufficient time for cleanup")
        }
    }

    return h.operation(ctx)
}

func (h *TimeoutAwareHook) Recovery() *actor.ShutdownHookRecovery {
    return actor.NewShutdownHookRecovery(
        actor.WithShutdownHookRecoveryStrategy(actor.ShouldSkip))
}
```

## Best Practices

### Do's ✅

1. **Order hooks by dependency**: Stop ingress → drain → persist → close
2. **Use appropriate recovery strategies** based on criticality
3. **Set realistic timeouts** for each hook
4. **Log hook execution** for debugging
5. **Handle context cancellation** in hooks
6. **Test shutdown sequence** thoroughly
7. **Make hooks idempotent** when possible

```go
// Good: Well-ordered hooks with appropriate strategies
actor.WithCoordinatedShutdown(
    &StopAPIHook{},        // Stop accepting requests
    &FlushBuffersHook{},   // Flush in-flight data
    &SaveStateHook{},      // Persist state
    &CloseConnectionsHook{}, // Close resources
)
```

### Don'ts ❌

1. **Don't block indefinitely** in hooks
2. **Don't ignore context** cancellation
3. **Don't register too many hooks** (keep it simple)
4. **Don't use ShouldFail** for optional cleanup
5. **Don't forget error logging**

```go
// Bad: Blocking without timeout
func (h *BadHook) Execute(ctx context.Context, system actor.ActorSystem) error {
    for {
        if h.isComplete() {
            break
        }
        time.Sleep(time.Second) // ❌ Could block forever
    }
    return nil
}

// Good: Respect context
func (h *GoodHook) Execute(ctx context.Context, system actor.ActorSystem) error {
    ticker := time.NewTicker(time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return ctx.Err() // ✅ Respect cancellation
        case <-ticker.C:
            if h.isComplete() {
                return nil
            }
        }
    }
}
```

## Testing Shutdown Hooks

```go
func TestShutdownHook(t *testing.T) {
    ctx := context.Background()

    hook := &FlushMetricsHook{metricsWriter: newMockWriter()}

    system, _ := actor.NewActorSystem("test",
        actor.WithPassivationDisabled(),
        actor.WithCoordinatedShutdown(hook))

    system.Start(ctx)

    // Test hook execution
    err := system.Stop(ctx)
    assert.NoError(t, err)

    // Verify hook was executed
    assert.True(t, hook.executed)
}

func TestHookRetry(t *testing.T) {
    ctx := context.Background()

    // Hook that fails first 2 times
    failCount := 0
    hook := &TestHook{
        executeFunc: func() error {
            failCount++
            if failCount < 3 {
                return errors.New("transient error")
            }
            return nil
        },
        recovery: actor.NewShutdownHookRecovery(
            actor.WithShutdownHookRetry(3, 100*time.Millisecond),
            actor.WithShutdownHookRecoveryStrategy(actor.ShouldRetryAndSkip)),
    }

    system, _ := actor.NewActorSystem("test",
        actor.WithCoordinatedShutdown(hook))

    system.Start(ctx)
    err := system.Stop(ctx)

    assert.NoError(t, err)
    assert.Equal(t, 3, failCount) // Verified retry happened
}
```

## Summary

- **Coordinated shutdown** ensures graceful cleanup
- **ShutdownHooks** define cleanup logic
- **Recovery strategies** control error handling
- **Execution order** follows registration order
- **Retry logic** handles transient failures
- **Best practices** ensure reliable shutdown

## Next Steps

- **[Actor System Overview](overview.md)**: System creation and configuration
- **[Spawning Actors](../actors/spawn.md)**: Creating actors
- **[Supervision](../actors/supervision.md)**: Fault tolerance
