# Scheduling

## Overview

GoAkt includes a built-in scheduler for delayed and recurring message delivery. It is powered by [go-quartz](https://github.com/reugn/go-quartz), a minimalist scheduling library that supports the Quartz cron expression format.

The scheduler starts automatically when the actor system starts and stops when the system shuts down. Use it for timeouts, periodic tasks, heartbeats, and cron-style jobs.

## Location Transparency

**Message scheduling is location-transparent:** the same APIs work for local and remote actors. Obtain the target PID via `ActorOf` and pass it to `ScheduleOnce`, `Schedule`, or `ScheduleWithCron`; the scheduler delivers to the actor regardless of where it runs.

## Operations

| Operation            | Trigger Type | Behavior                                          |
|----------------------|--------------|---------------------------------------------------|
| **ScheduleOnce**     | RunOnce      | Deliver a message once after a delay              |
| **Schedule**         | Simple       | Deliver a message at a fixed interval (repeating) |
| **ScheduleWithCron** | Cron         | Deliver a message according to a cron expression  |

## API

| Method                                                   | Purpose                                 |
|----------------------------------------------------------|-----------------------------------------|
| `ScheduleOnce(ctx, message, pid, delay, opts...)`        | Deliver once after the delay            |
| `Schedule(ctx, message, pid, interval, opts...)`         | Deliver at a fixed interval (repeating) |
| `ScheduleWithCron(ctx, message, pid, cronExpr, opts...)` | Cron-style scheduling                   |
| `CancelSchedule(reference)`                              | Cancel a scheduled message              |
| `PauseSchedule(reference)`                               | Pause delivery                          |
| `ResumeSchedule(reference)`                              | Resume a paused schedule                |

## Schedule Options

| Option                       | Purpose                                                                                                                                                                                 |
|------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `WithReference(referenceID)` | Set a unique ID for cancel, pause, or resume. **Strongly recommended** if you plan to manage the schedule later. Without it, an auto-generated UUID is used and may not be retrievable. |
| `WithSender(pid)`            | Set the sender PID for the scheduled message. Defaults to `NoSender` (nil) so the target actor sees `ctx.Sender()` as nil.                                                              |

## Examples

```go
// One-time delivery after 5 seconds
err := system.ScheduleOnce(ctx, &MyMessage{}, pid, 5*time.Second)

// Repeating every 10 seconds (with reference for later cancellation)
err := system.Schedule(ctx, &Heartbeat{}, pid, 10*time.Second, actor.WithReference("heartbeat-1"))

// Cron: every minute at second 0
err := system.ScheduleWithCron(ctx, &Cleanup{}, pid, "0 * * * * *", actor.WithReference("cleanup"))

// Cancel, pause, or resume
system.CancelSchedule("heartbeat-1")
system.PauseSchedule("cleanup")
system.ResumeSchedule("cleanup")
```

## Cron Expression Format

`ScheduleWithCron` uses the **Quartz cron expression format** as implemented by [go-quartz](https://github.com/reugn/go-quartz). This format supports 6 or 7 fields (seconds, minutes, hours, day-of-month, month, day-of-week, and optionally year).

### Field Summary

| Field Name   | Mandatory | Allowed Values  | Allowed Special Characters  |
|--------------|-----------|-----------------|-----------------------------|
| Seconds      | YES       | 0-59            | `,` `-` `*` `/`             |
| Minutes      | YES       | 0-59            | `,` `-` `*` `/`             |
| Hours        | YES       | 0-23            | `,` `-` `*` `/`             |
| Day of month | YES       | 1-31            | `,` `-` `*` `?` `/` `L` `W` |
| Month        | YES       | 1-12 or JAN-DEC | `,` `-` `*` `/`             |
| Day of week  | YES       | 1-7 or SUN-SAT  | `,` `-` `*` `?` `/` `L` `#` |
| Year         | NO        | empty, 1970-    | `,` `-` `*` `/`             |

### Special Characters

- **`*`** — All values in a field (e.g., `*` in minutes = "every minute").
- **`?`** — No specific value; use when specifying one of two related fields (e.g., "10" in day-of-month, `?` in day-of-week).
- **`-`** — Range of values (e.g., `10-12` in hour = "hours 10, 11, and 12").
- **`,`** — List of values (e.g., `MON,WED,FRI` in day-of-week = "Monday, Wednesday, Friday").
- **`/`** — Increments (e.g., `0/15` in seconds = "0, 15, 30, 45"; `1/3` in day-of-month = "every 3 days from the 1st").
- **`L`** — Last; meaning varies by field. Ranges or lists are not allowed with `L`.
  - Day-of-month: Last day of the month (e.g., `L-3` = third to last day of the month).
  - Day-of-week: Last day of the week (7 or SAT) when alone; "last xxx day" when used after another value (e.g., `6L` = "last Friday").
- **`W`** — Nearest weekday to the given day (e.g., `15W` = "nearest weekday to the 15th"). If `1W` falls on Saturday, it fires Monday the 3rd. `W` only applies to a single day, not ranges or lists.
- **`#`** — Nth weekday of the month (e.g., `6#3` = "third Friday"; `2#1` = "first Monday"). Does not fire if that nth weekday does not exist in the month.

**Notes:**

- `L` and `W` can be combined in day-of-month as `LW` = "last weekday of the month".
- Month and day-of-week names are case-insensitive (e.g., `MON` = `mon`).

### Cron Examples

| Expression       | Meaning                             |
|------------------|-------------------------------------|
| `0 * * * * *`    | Every minute at second 0            |
| `0 0 * * * *`    | Every hour at minute 0              |
| `0 0 12 * * *`   | Every day at noon                   |
| `0 */5 * * * *`  | Every 5 minutes                     |
| `0 0 0 * * MON`  | Every Monday at midnight            |
| `0 0 12 1 * *`   | 1st of every month at noon          |
| `0 0 12 L * *`   | Last day of every month at noon     |
| `0 0 12 ? * WED` | Every Wednesday at noon             |
| `0 0 12 15W * *` | Nearest weekday to the 15th at noon |

### Timezone

Cron expressions are evaluated in the **local timezone** of the process (`time.Now().Location()`).

## Use Cases

- **Heartbeats and health checks** — Use `Schedule` with a fixed interval.
- **Timeout notifications** — Use `ScheduleOnce` to send a timeout message after a delay.
- **Periodic aggregation or cleanup** — Use `ScheduleWithCron` for daily, hourly, or custom schedules.
- **Scheduled jobs** — Use cron expressions for complex recurring patterns (e.g., "every weekday at 9am").

## Sender and Target Actor

For scheduled messages, the target actor receives the message via `Tell`. By default, `ctx.Sender()` is `nil` (NoSender). Use `WithSender(pid)` if you want the target to know which actor scheduled the message.

## Errors

| Error                           | Condition                                                      |
|---------------------------------|----------------------------------------------------------------|
| `ErrSchedulerNotStarted`        | Actor system not started; scheduler is started with the system |
| `ErrScheduledReferenceNotFound` | Cancel, pause, or resume called with an unknown reference      |
| `ErrRemotingDisabled`           | Scheduling to a remote PID when remoting is not enabled        |

## Delivery Semantics

- **Fire-and-forget** — Scheduling does not provide built-in delivery guarantees (at-least-once or exactly-once). Ensure idempotency where needed.
- **No retries** — If delivery fails (e.g., actor not found), the message is not retried.
- **ScheduleOnce** — Delivered exactly once after the delay; no further deliveries.
