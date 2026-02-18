# Message Scheduling

Message scheduling allows you to send messages to actors at a specific time in the future or on a recurring schedule. This is built into the GoAkt runtime and provides a reliable way to implement time-based behaviors.

## Table of Contents

- 🤔 [What is Message Scheduling?](#what-is-message-scheduling)
- 🛠️ [Scheduling Methods](#scheduling-methods)
- 📅 [Cron Expression Format](#cron-expression-format)
- ⚙️ [Scheduling Options](#scheduling-options)
- ✅ [Best Practices](#best-practices)
- ⚡ [Performance Considerations](#performance-considerations)
- ⚠️ [Limitations](#limitations)
- 📋 [Summary](#summary)

---

## What is Message Scheduling?

Message scheduling enables you to:

- **Delay message delivery**: Send a message after a specified duration
- **Schedule recurring messages**: Send messages periodically using cron expressions
- **Implement timers**: Create timeout and reminder patterns
- **Build time-based workflows**: Trigger actions at specific times

## Scheduling Methods

### ScheduleOnce

This method is used to scheduled a message that will be sent to a given actor once after a delay.

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

> For remote message scheduling use the **RemoteScheduleOnce**

### ScheduleWithCron

This method is used to schedule messages on a recurring schedule using cron expressions that will be sent to a given actor.

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

> For remote message scheduling use the **RemoteScheduleWithCron**

## Cron Expression Format

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
