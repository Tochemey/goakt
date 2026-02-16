# Message Scheduling

Message scheduling allows you to send messages to actors at a specific time in the future or on a recurring schedule. This is built into the GoAkt runtime and provides a reliable way to implement time-based behaviors.

## Table of Contents

- 🤔 [What is Message Scheduling?](#what-is-message-scheduling)
- 🛠️ [Scheduling Methods](#scheduling-methods)
- ⏰ [ScheduleOnce Examples](#scheduleonce-examples)
- 📅 [ScheduleWithCron Examples](#schedulewithcron-examples)
- ⚙️ [Scheduling Options](#scheduling-options)
- 🧩 [Common Patterns](#common-patterns)
- ✅ [Best Practices](#best-practices)
- ⚡ [Performance Considerations](#performance-considerations)
- ⚠️ [Limitations](#limitations)
- 📋 [Summary](#summary)
- ➡️ [Next Steps](#next-steps)

---

## What is Message Scheduling?

Message scheduling enables you to:

- **Delay message delivery**: Send a message after a specified duration
- **Schedule recurring messages**: Send messages periodically using cron expressions
- **Implement timers**: Create timeout and reminder patterns
- **Build time-based workflows**: Trigger actions at specific times

## Scheduling Methods

### ScheduleOnce

Send a message once after a delay. Scheduling is done on the **actor system**, not on the receive context. Parameter order: `(ctx, message, pid, delay, opts)`.

```go
// From outside an actor (e.g. main or another component)
err := system.ScheduleOnce(ctx, &ReminderMessage{}, targetPID, 5*time.Second)

// From inside an actor's Receive
err := ctx.ActorSystem().ScheduleOnce(ctx.Context(), &ReminderMessage{}, ctx.Self(), 5*time.Second)
```

**Use for:**

- One-time reminders
- Timeouts
- Delayed actions
- Retry after delay

### ScheduleWithCron

Send messages on a recurring schedule using cron expressions. Parameter order: `(ctx, message, pid, cronExpression, opts)`.

```go
// From outside an actor
err := system.ScheduleWithCron(ctx, &HealthCheck{}, targetPID, "0 */5 * * * *")

// From inside an actor's Receive
err := ctx.ActorSystem().ScheduleWithCron(ctx.Context(), &HealthCheck{}, ctx.Self(), "0 */5 * * * *")
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
        _ = ctx.ActorSystem().ScheduleOnce(ctx.Context(),
            &RequestTimeout{RequestId: msg.GetId()},
            ctx.Self(),
            30*time.Second,
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
            _ = ctx.ActorSystem().ScheduleOnce(ctx.Context(),
                &RetryTask{Task: msg, Attempt: msg.GetAttempt() + 1},
                ctx.Self(),
                5*time.Second,
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
            _ = ctx.ActorSystem().ScheduleOnce(ctx.Context(),
                &RetryTask{Task: msg.GetTask(), Attempt: msg.GetAttempt() + 1},
                ctx.Self(),
                delay,
            )
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
        _ = ctx.ActorSystem().ScheduleOnce(ctx.Context(),
            &Reminder{Id: msg.GetId(), Message: msg.GetMessage()},
            ctx.Self(),
            msg.GetDelay(),
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
        _ = ctx.ActorSystem().ScheduleOnce(ctx.Context(),
            &InactivityTimeout{},
            ctx.Self(),
            30*time.Minute,
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

Cron expressions follow the **Quartz cron format** (as implemented by [go-quartz](https://github.com/reugn/go-quartz?tab=readme-ov-file#cron-expression-format)): six fields (seconds, minutes, hours, day of month, month, day of week), with an optional seventh field for year.

| Field Name   | Mandatory | Allowed Values  | Allowed Special Characters  |
|--------------|-----------|-----------------|-----------------------------|
| Seconds      | YES       | 0-59            | `,` `-` `*` `/`             |
| Minutes      | YES       | 0-59            | `,` `-` `*` `/`             |
| Hours        | YES       | 0-23            | `,` `-` `*` `/`             |
| Day of month | YES       | 1-31            | `,` `-` `*` `?` `/` `L` `W` |
| Month        | YES       | 1-12 or JAN-DEC | `,` `-` `*` `/`             |
| Day of week  | YES       | 1-7 or SUN-SAT  | `,` `-` `*` `?` `/` `L` `#` |
| Year         | NO        | empty, 1970-    | `,` `-` `*` `/`             |

**Special characters:**

- `*` — All values in a field (e.g. every minute).
- `?` — No specific value; use when one of two related fields is specified (e.g. day-of-month **or** day-of-week).
- `-` — Range (e.g. `10-12` in hour = 10, 11, 12).
- `,` — List (e.g. `MON,WED,FRI` in day-of-week).
- `/` — Increments (e.g. `0/15` in seconds = 0, 15, 30, 45; `1/3` in day-of-month = every 3 days from the 1st).
- `L` — Last; meaning depends on field. Day-of-month: last day of month (e.g. `L-3` = third to last). Day-of-week: last weekday (e.g. `6L` = last Friday).
- `W` — Nearest weekday to the given day (e.g. `15W` = nearest weekday to the 15th). Day-of-month only.
- `#` — Nth weekday of month (e.g. `6#3` = third Friday; `2#1` = first Monday). Day-of-week only.

Month and day-of-week names are case-insensitive (e.g. `MON` = `mon`).

**Common expressions:**

- `"0 */5 * * * *"` — Every 5 minutes
- `"0 0 */1 * * *"` — Every hour
- `"0 0 0 * * *"` — Daily at midnight
- `"0 0 9 * * MON-FRI"` — Weekdays at 9 AM
- `"0 30 14 * * *"` — Daily at 2:30 PM

### Health Check

```go
type MonitorActor struct {
    targets []*actor.PID
}

func (a *MonitorActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.PostStart:
        // Schedule health checks every 5 minutes (must be in Receive; PreStart has no Self())
        _ = ctx.ActorSystem().ScheduleWithCron(ctx.Context(),
            &PerformHealthCheck{},
            ctx.Self(),
            "0 */5 * * * *",
        )
    case *PerformHealthCheck:
        ctx.Logger().Debug("Performing health check")

        for _, target := range a.targets {
            response := ctx.Ask(target, &HealthCheck{}, 5*time.Second)
            if response == nil {
                ctx.Logger().Warn("Health check failed", "target", target.Name())
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

func (a *ReportActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.PostStart:
        // Schedule daily reports at 9 AM (must be in Receive; PreStart has no Self())
        _ = ctx.ActorSystem().ScheduleWithCron(ctx.Context(),
            &GenerateReport{},
            ctx.Self(),
            "0 0 9 * * *",
        )
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
    return nil
}

func (a *CacheActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.PostStart:
        // Schedule cleanup every hour (must be in Receive; PreStart has no Self())
        _ = ctx.ActorSystem().ScheduleWithCron(ctx.Context(),
            &CleanupExpired{},
            ctx.Self(),
            "0 0 */1 * * *",
        )
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

func (a *MetricsActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.PostStart:
        // Collect metrics every 30 seconds (must be in Receive; PreStart has no Self())
        _ = ctx.ActorSystem().ScheduleWithCron(ctx.Context(),
            &CollectMetrics{},
            ctx.Self(),
            "*/30 * * * * *",
        )
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

Provide a reference ID so you can later **cancel**, **pause**, or **resume** the scheduled message. Without a reference, the scheduler assigns an internal ID that you cannot use for these operations.

```go
err := system.ScheduleOnce(ctx, &Message{}, targetPID, 10*time.Second,
    actor.WithReference("my-schedule-id"),
)
// Later: system.CancelSchedule("my-schedule-id")
// Or:   system.PauseSchedule("my-schedule-id") / system.ResumeSchedule("my-schedule-id")
```

**Use for:**

- Canceling scheduled messages when they are no longer needed
- Pausing and resuming recurring schedules (e.g. maintenance window)
- Tracking scheduled messages (e.g. resetting a session timeout by reference)
- Avoiding duplicate schedules by reusing the same reference

The same option applies to `ScheduleOnce`, `Schedule`, `ScheduleWithCron`, and their `Remote*` variants.

### Cancel, Pause, and Resume

You can manage a scheduled message by the **reference** you passed with `WithReference`. All three methods are on the **actor system** and take only the reference string.

| Method                                   | Description                                                                                                                                                   |
|------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `CancelSchedule(reference string) error` | Permanently removes the schedule. The message will not be delivered. Use when the schedule is no longer needed (e.g. session ended, task cancelled).          |
| `PauseSchedule(reference string) error`  | Temporarily stops the schedule. No further deliveries until `ResumeSchedule` is called. Use for maintenance windows or temporarily disabling a recurring job. |
| `ResumeSchedule(reference string) error` | Restarts a previously paused schedule. Only valid for a schedule that was paused with `PauseSchedule`.                                                        |

**Requirements:**

- The schedule must have been created with `actor.WithReference("your-id")`.
- The actor system must be started (scheduler is started with the system). Calling these before `Start` or after `Stop` returns an error.

**Errors:**

- `ErrScheduledReferenceNotFound` — No schedule exists for the given reference (never scheduled, already cancelled, or already delivered for one-shot).
- `ErrSchedulerNotStarted` — The actor system (and thus the scheduler) is not running.

**Example: Cancel a one-shot**

```go
ref := "timeout-123"
err := system.ScheduleOnce(ctx, &SessionTimeout{}, pid, 15*time.Minute, actor.WithReference(ref))
if err != nil {
    return err
}
// ... later, user activity: cancel the timeout
if err := system.CancelSchedule(ref); err != nil {
    // handle: not found or scheduler not started
}
```

**Example: Pause and resume a recurring schedule**

```go
ref := "health-check"
err := system.ScheduleWithCron(ctx, &HealthCheck{}, pid, "0 */5 * * * *", actor.WithReference(ref))
if err != nil {
    return err
}
// Pause during maintenance
_ = system.PauseSchedule(ref)
// ... maintenance ...
_ = system.ResumeSchedule(ref)
```

**Example: From inside an actor**

```go
sys := ctx.ActorSystem()
err := sys.CancelSchedule(a.scheduleRef)
if err != nil {
    if errors.Is(err, errors.ErrScheduledReferenceNotFound) {
        // already cancelled or never existed
        return
    }
    ctx.Logger().Error("failed to cancel schedule", "error", err)
}
```

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

        // Reset timeout on activity (cancel previous with ActorSystem.CancelSchedule(a.timeoutRef))
        _ = ctx.ActorSystem().ScheduleOnce(ctx.Context(),
            &SessionTimeout{},
            ctx.Self(),
            15*time.Minute,
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

func (a *HeartbeatActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.PostStart:
        // Send heartbeat every 10 seconds
        _ = ctx.ActorSystem().ScheduleWithCron(ctx.Context(),
            &SendHeartbeat{},
            ctx.Self(),
            "*/10 * * * * *",
        )
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

func (a *BatchActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.PostStart:
        // Process batch every 5 seconds
        _ = ctx.ActorSystem().ScheduleWithCron(ctx.Context(),
            &ProcessBatch{},
            ctx.Self(),
            "*/5 * * * * *",
        )
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

    _ = ctx.ActorSystem().ScheduleOnce(ctx.Context(),
        &RetryTask{Task: task, Attempt: attempt + 1},
        ctx.Self(),
        delay,
    )

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

## Performance Considerations

- **Scheduling overhead**: Minimal overhead for scheduling
- **Cron precision**: Cron schedules are accurate to the second
- **Message ordering**: Scheduled messages maintain order
- **Cluster scheduling**: Works across cluster (sends to correct node)

## Limitations

- **Minimum delay**: Practical minimum is ~10ms
- **Cron granularity**: Second-level granularity
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
