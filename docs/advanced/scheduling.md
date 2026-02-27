# Scheduling

## Overview

GoAkt includes a built-in scheduler for delayed and recurring message delivery. Use it for timeouts, periodic tasks, and
cron-like behavior.

## Operations

- **ScheduleOnce** — Deliver a message once after a delay
- **Schedule** — Deliver a message at a fixed interval
- **ScheduleWithCron** — Cron-style scheduling

## Use cases

- Heartbeats and health checks
- Timeout notifications
- Periodic aggregation or cleanup
- Scheduled jobs

## API

| Method                                          | Purpose                      |
|-------------------------------------------------|------------------------------|
| `ScheduleOnce(ctx, message, pid, delay)`        | Deliver once after the delay |
| `Schedule(ctx, message, pid, interval)`         | Deliver at a fixed interval  |
| `ScheduleWithCron(ctx, message, pid, cronExpr)` | Cron-style scheduling        |
| `CancelSchedule(reference)`                     | Cancel a scheduled message   |
| `PauseSchedule` / `ResumeSchedule`              | Pause or resume delivery     |

The target actor receives the message; `ctx.Sender()` may be nil for scheduled messages. Use `WithScheduleReference`
when scheduling to obtain a handle for cancel, pause, or resume.

## Location transparency

Scheduling works with both local and remote PIDs. Obtain the PID via `ActorOf` and pass it to the scheduler. The same
APIs apply regardless of location.
