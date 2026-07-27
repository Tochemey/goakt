# Issue 1288: Grain Timers

Living sample for [#1288](https://github.com/Tochemey/goakt/issues/1288), activation-scoped timers for grains.

## Scenario

Two `OrderGrain` instances are placed at the same time:

- **order-42** pays within the deadline. The payment timeout is cancelled, a shipment poll timer runs until the third poll reports delivery, and the poll timer cancels itself from its own tick handler.
- **order-13** never pays. Its one-shot payment timeout fires and the order is cancelled.

Both grains also start an audit heartbeat from `OnActivate` that logs their status for the whole activation.

## What it demonstrates

| Feature                                                                     | Where                                                                            |
|-----------------------------------------------------------------------------|----------------------------------------------------------------------------------|
| Interval timer started from `OnActivate` via `GrainProps.Schedule`          | audit heartbeat                                                                  |
| One-shot with an explicit reference (`ScheduleOnce` + `WithTimerReference`) | payment deadline                                                                 |
| Cancelling a timer from a message handler (`CancelSchedule`)                | payment arrives in time                                                          |
| A timer cancelling itself from its own tick                                 | shipment poll stops after delivery                                               |
| Ticks do not reset the passivation clock                                    | audit ticks keep printing while idle, yet both grains passivate on schedule      |
| All timers cancelled at deactivation                                        | no timer output after `deactivated` lines                                        |
| Reactivation starts fresh                                                   | asking order-42 after passivation reactivates it with status `new` and no timers |

## Run

```bash
go run ./playground/issue-1288
```

Expected: interleaved order lifecycle logs, both grains passivating while their audit timers are still registered, a fresh reactivation of order-42, and a final `OK`. The sample exits non-zero on any deviation.
