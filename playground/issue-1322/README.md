# Issue 1322: metric instrument types and deadletter scrape cost

Living sample for [issue #1322](https://github.com/Tochemey/goakt/issues/1322).

The runtime metrics enabled by `WithMetrics()` had two defects.

Several instruments reported an absolute current value that falls during normal operation (live actors, connected peers, child actors, stashed messages, uptime, time since last message), yet they were registered as monotonic `Int64ObservableCounter`. Exporters that convert a counter to a rate or a delta treat every decrease as a counter reset, so a stopping actor or a draining stash produced wrong series. Those instruments are now `Int64ObservableGauge`.

The per-actor metrics callback also asked the deadletter actor for its count once per running actor per scrape. Each ask was a message to a single mailbox, so scrape cost grew linearly with the actor population and the deadletter actor was flooded at scale. The callback now asks the deadletter actor once per scrape for a snapshot of all per-address counts and looks each actor up in that snapshot. The deadletter actor still owns its state; it is queried through one message, not bypassed.

## What the sample does

The sample guards both fixes without an OpenTelemetry SDK, matching how the library's own tests exercise metrics. Each assertion fails the run with a non-zero exit code if it does not hold.

1. Spawns actors and stops some, then reads the live actor count through the public `Metric` API and asserts it rises to the spawned total and falls after the stops. A monotonic counter cannot represent that fall; a gauge can.
2. Spawns children under a parent and stops some, asserting the child count rises then falls the same way, and sends messages to a black-hole actor to confirm per-actor deadletter counts are recorded.
3. Runs one full metrics scrape over 10k resident actors through an in-process meter and asserts a metrics-enabled system registers a constant two meter callbacks and the scrape completes without error. Before the fix the same scrape blocked on one deadletter ask per actor.

This is the follow-up called out in the `issue-1315` sample, which noted that batching the per-actor deadletter counts into one request was possible future work for large metrics-enabled populations.

## Run

```bash
go run ./playground/issue-1322
```

## Expected output

```text
system-level gauge (live actor count rises then falls):
  actors after spawn                 ok     10 spawned
  actors after stop falls            ok     6 remaining

per-actor gauge (child count rises then falls):
  children after spawn               ok     5 spawned
  children after stop falls          ok     2 remaining
  deadletters recorded per actor     ok     6 deadletters

deadletter scrape cost (constant callbacks, no per-actor ask):
  constant meter registrations       ok     2 registrations
  scrape wall time                   info   4ms per full scrape over 10000 actors

PASS: gauge semantics hold and the scrape reads deadletters without per-actor asks
```
