# Message Scheduling

Message scheduling allows you to send messages to actors at a specific time in the future or on a recurring schedule. This is built into the GoAkt runtime and provides a reliable way to implement time-based behaviors.

## What is Message Scheduling?

Message scheduling enables you to:

- **Delay message delivery**: Send a message after a specified duration
- **Schedule recurring messages**: Send messages periodically using cron expressions
- **Implement timers**: Create timeout and reminder patterns
- **Build time-based workflows**: Trigger actions at specific times

## Scheduling Methods

### ScheduleOnce

Send a message once after a delay:

```go
err := ctx.ScheduleOnce(
    5*time.Second,        // Delay
    targetPID,            // Recipient
    &ReminderMessage{},   // Message
)
```

**Use for:**

- One-time reminders
- Timeouts
- Delayed actions
- Retry after delay

### ScheduleWithCron

Send messages on a recurring schedule using cron expressions:

```go
err := actorSystem.ScheduleWithCron(ctx,
    "0 */5 * * * *",      // Every 5 minutes
    targetPID,            // Recipient
    &HealthCheck{},       // Message
)
```

**Use for:**

- Periodic health checks
- Scheduled reports
- Regular cleanups
- Monitoring tasks

## ScheduleOnce Examples

### Basic Timeout

```go
func (a *RequestActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *StartRequest:
        // Send timeout message after 30 seconds
        ctx.ScheduleOnce(
            30*time.Second,
            ctx.Self(),
            &RequestTimeout{RequestId: msg.GetId()},
        )

        // Make external request
        a.makeExternalRequest(msg)

    case *RequestComplete:
        // Cancel timeout logic here
        a.handleComplete(msg)

    case *RequestTimeout:
        ctx.Logger().Warn("Request timed out",
            "request_id", msg.GetRequestId())
        ctx.Response(&TimeoutError{})
    }
}
```

### Delayed Retry

```go
func (a *RetryActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ProcessTask:
        err := a.processTask(msg)
        if err != nil {
            // Retry after 5 seconds
            ctx.ScheduleOnce(
                5*time.Second,
                ctx.Self(),
                &RetryTask{
                    Task:     msg,
                    Attempt:  msg.GetAttempt() + 1,
                },
            )
        }

    case *RetryTask:
        if msg.GetAttempt() >= 3 {
            ctx.Response(&TaskFailed{Reason: "Max retries"})
            return
        }

        // Try again
        err := a.processTask(msg.GetTask())
        if err != nil {
            // Exponential backoff
            delay := time.Duration(msg.GetAttempt()) * 5 * time.Second
            ctx.ScheduleOnce(delay, ctx.Self(), &RetryTask{
                Task:    msg.GetTask(),
                Attempt: msg.GetAttempt() + 1,
            })
        }
    }
}
```

### Reminder Pattern

```go
func (a *ReminderActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *SetReminder:
        // Schedule reminder
        ctx.ScheduleOnce(
            msg.GetDelay(),
            ctx.Self(),
            &Reminder{
                Id:      msg.GetId(),
                Message: msg.GetMessage(),
            },
        )
        ctx.Response(&ReminderSet{Id: msg.GetId()})

    case *Reminder:
        // Reminder triggered
        ctx.Logger().Info("Reminder triggered",
            "id", msg.GetId(),
            "message", msg.GetMessage())

        // Notify user
        ctx.Tell(a.userPID, &ReminderNotification{
            Message: msg.GetMessage(),
        })
    }
}
```

### Delayed Shutdown

```go
func (a *SessionActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *UserActivity:
        // Cancel previous shutdown
        if a.shutdownScheduled {
            // Would need to track and cancel
        }

        // Schedule shutdown after inactivity
        ctx.ScheduleOnce(
            30*time.Minute,
            ctx.Self(),
            &InactivityTimeout{},
        )
        a.shutdownScheduled = true
        a.handleActivity(msg)

    case *InactivityTimeout:
        ctx.Logger().Info("Session timeout due to inactivity")
        ctx.Shutdown()
    }
}
```

## ScheduleWithCron Examples

### Cron Expression Format

```
┌─────────── second (0 - 59)
│ ┌───────── minute (0 - 59)
│ │ ┌─────── hour (0 - 23)
│ │ │ ┌───── day of month (1 - 31)
│ │ │ │ ┌─── month (1 - 12)
│ │ │ │ │ ┌─ day of week (0 - 6) (Sunday to Saturday)
│ │ │ │ │ │
* * * * * *
```

**Common expressions:**

- `"0 */5 * * * *"` - Every 5 minutes
- `"0 0 */1 * * *"` - Every hour
- `"0 0 0 * * *"` - Daily at midnight
- `"0 0 9 * * MON-FRI"` - Weekdays at 9 AM
- `"0 30 14 * * *"` - Daily at 2:30 PM

### Health Check

```go
type MonitorActor struct {
    targets []*actor.PID
}

func (a *MonitorActor) PreStart(ctx *actor.Context) error {
    // Schedule health checks every 5 minutes
    return ctx.ActorSystem().ScheduleWithCron(ctx.Context(),
        "0 */5 * * * *",
        ctx.Self(),
        &PerformHealthCheck{},
    )
}

func (a *MonitorActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *PerformHealthCheck:
        ctx.Logger().Debug("Performing health check")

        for _, target := range a.targets {
            response, err := ctx.Ask(target, &HealthCheck{}, 5*time.Second)
            if err != nil {
                ctx.Logger().Warn("Health check failed",
                    "target", target.Name(),
                    "error", err)
                continue
            }

            status := response.(*HealthStatus)
            if !status.GetHealthy() {
                ctx.Logger().Error("Target unhealthy",
                    "target", target.Name())
            }
        }

    case *RegisterTarget:
        a.targets = append(a.targets, msg.GetPid())
    }
}
```

### Scheduled Reports

```go
type ReportActor struct {
    reportRecipients []*actor.PID
}

func (a *ReportActor) PreStart(ctx *actor.Context) error {
    // Schedule daily reports at 9 AM
    return ctx.ActorSystem().ScheduleWithCron(ctx.Context(),
        "0 0 9 * * *",
        ctx.Self(),
        &GenerateReport{},
    )
}

func (a *ReportActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *GenerateReport:
        ctx.Logger().Info("Generating daily report")

        report := a.generateDailyReport()

        // Send to all recipients
        for _, recipient := range a.reportRecipients {
            ctx.Tell(recipient, &Report{
                Date: time.Now(),
                Data: report,
            })
        }
    }
}
```

### Cleanup Task

```go
type CacheActor struct {
    cache map[string]*CacheEntry
}

func (a *CacheActor) PreStart(ctx *actor.Context) error {
    a.cache = make(map[string]*CacheEntry)

    // Schedule cleanup every hour
    return ctx.ActorSystem().ScheduleWithCron(ctx.Context(),
        "0 0 */1 * * *",
        ctx.Self(),
        &CleanupExpired{},
    )
}

func (a *CacheActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *CleanupExpired:
        ctx.Logger().Info("Running cache cleanup")

        now := time.Now()
        removed := 0

        for key, entry := range a.cache {
            if entry.ExpiresAt.Before(now) {
                delete(a.cache, key)
                removed++
            }
        }

        ctx.Logger().Info("Cache cleanup complete",
            "removed", removed,
            "remaining", len(a.cache))
    }
}
```

### Metrics Collection

```go
type MetricsActor struct {
    metricsDB *MetricsDatabase
}

func (a *MetricsActor) PreStart(ctx *actor.Context) error {
    // Collect metrics every 30 seconds
    return ctx.ActorSystem().ScheduleWithCron(ctx.Context(),
        "*/30 * * * * *",
        ctx.Self(),
        &CollectMetrics{},
    )
}

func (a *MetricsActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *CollectMetrics:
        metrics := a.gatherSystemMetrics()

        if err := a.metricsDB.Store(metrics); err != nil {
            ctx.Logger().Error("Failed to store metrics", "error", err)
        }
    }
}
```

## Scheduling Options

### WithReference

Provide a reference ID to manage scheduled messages:

```go
err := ctx.ScheduleOnce(
    10*time.Second,
    targetPID,
    &Message{},
    actor.WithReference("my-schedule-id"),
)
```

**Use for:**

- Canceling scheduled messages
- Tracking scheduled messages
- Preventing duplicates

## Common Patterns

### Pattern 1: Session Timeout

```go
type SessionActor struct {
    lastActivity time.Time
    timeoutRef   string
}

func (a *SessionActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *UserAction:
        a.lastActivity = time.Now()
        a.timeoutRef = fmt.Sprintf("timeout-%d", time.Now().Unix())

        // Reset timeout on activity
        ctx.ScheduleOnce(
            15*time.Minute,
            ctx.Self(),
            &SessionTimeout{},
            actor.WithReference(a.timeoutRef),
        )

    case *SessionTimeout:
        ctx.Logger().Info("Session timed out")
        ctx.Shutdown()
    }
}
```

### Pattern 2: Heartbeat

```go
type HeartbeatActor struct {
    serverPID *actor.PID
}

func (a *HeartbeatActor) PreStart(ctx *actor.Context) error {
    // Send heartbeat every 10 seconds
    return ctx.ActorSystem().ScheduleWithCron(ctx.Context(),
        "*/10 * * * * *",
        ctx.Self(),
        &SendHeartbeat{},
    )
}

func (a *HeartbeatActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *SendHeartbeat:
        ctx.Tell(a.serverPID, &Heartbeat{
            Timestamp: time.Now(),
        })
    }
}
```

### Pattern 3: Rate Limited Batch Processing

```go
type BatchActor struct {
    pending []*WorkItem
}

func (a *BatchActor) PreStart(ctx *actor.Context) error {
    // Process batch every 5 seconds
    return ctx.ActorSystem().ScheduleWithCron(ctx.Context(),
        "*/5 * * * * *",
        ctx.Self(),
        &ProcessBatch{},
    )
}

func (a *BatchActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *WorkItem:
        a.pending = append(a.pending, msg)

    case *ProcessBatch:
        if len(a.pending) == 0 {
            return
        }

        ctx.Logger().Info("Processing batch",
            "count", len(a.pending))

        a.processBatch(a.pending)
        a.pending = make([]*WorkItem, 0)
    }
}
```

### Pattern 4: Exponential Backoff

```go
func (a *RetryActor) scheduleRetry(ctx *actor.ReceiveContext, task *Task, attempt int) {
    // Exponential backoff: 1s, 2s, 4s, 8s, 16s...
    delay := time.Duration(1<<attempt) * time.Second

    // Cap at 1 minute
    if delay > time.Minute {
        delay = time.Minute
    }

    ctx.ScheduleOnce(delay, ctx.Self(), &RetryTask{
        Task:    task,
        Attempt: attempt + 1,
    })

    ctx.Logger().Info("Retry scheduled",
        "attempt", attempt+1,
        "delay", delay)
}
```

## Best Practices

### Do's ✅

1. **Use appropriate scheduling method**: ScheduleOnce for delays, ScheduleWithCron for periodic
2. **Handle scheduled messages**: Always implement handlers for scheduled messages
3. **Use references**: Track scheduled messages with references
4. **Log scheduling**: Log when messages are scheduled
5. **Test cron expressions**: Verify cron expressions are correct

### Don'ts ❌

1. **Don't schedule too frequently**: Avoid sub-second scheduling for heavy operations
2. **Don't forget error handling**: Handle scheduling errors
3. **Don't leak schedules**: Cancel schedules when no longer needed
4. **Don't use for precise timing**: Scheduling has some delay tolerance
5. **Don't schedule during shutdown**: Check actor state before scheduling

## Testing Scheduled Messages

```go
func TestScheduling(t *testing.T) {
    ctx := context.Background()
    system, _ := actor.NewActorSystem("test",
        actor.WithPassivationDisabled())
    system.Start(ctx)
    defer system.Stop(ctx)

    pid, _ := system.Spawn(ctx, "test", &TestActor{})

    // Schedule message
    err := system.ScheduleOnce(ctx, 100*time.Millisecond, pid, &TestMessage{})
    assert.NoError(t, err)

    // Wait for message to be delivered
    time.Sleep(200 * time.Millisecond)

    // Verify message was received
    response, _ := actor.Ask(ctx, pid, &GetReceivedCount{}, time.Second)
    count := response.(*ReceivedCount)
    assert.Equal(t, 1, count.Value)
}
```

## Performance Considerations

- **Scheduling overhead**: Minimal overhead for scheduling
- **Cron precision**: Cron schedules are accurate to the second
- **Message ordering**: Scheduled messages maintain order
- **Cluster scheduling**: Works across cluster (sends to correct node)

## Limitations

- **Minimum delay**: Practical minimum is ~10ms
- **Cron granularity**: Second-level granularity
- **No cancellation API**: Can't cancel scheduled messages (use references + logic)
- **No persistence**: Schedules lost on restart

## Summary

- **ScheduleOnce** sends messages after a delay
- **ScheduleWithCron** sends recurring messages
- Use for **timeouts**, **reminders**, **health checks**, and **periodic tasks**
- **Cron expressions** provide flexible scheduling
- **References** help track scheduled messages
- Built into the **runtime** for reliability

## Next Steps

- **[Messaging](messaging.md)**: Learn about message patterns
- **[PipeTo Pattern](pipeto.md)**: Execute async work
- **[Passivation](passivation.md)**: Automatic actor lifecycle
- **[Behaviors](behaviours.md)**: Combine scheduling with state machines
