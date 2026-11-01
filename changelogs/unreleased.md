# Unreleased

## Performance

- **Lock-free send path.** `Tell` and `Ask` from outside an actor no longer take a read lock on the target or on the actor system. The fields of a `PID` are grouped so that what senders read and what the receiving actor writes do not share a cache line.
- **Fewer worker wake-ups.** A `Tell` from outside the actor system to an idle actor no longer wakes a parked worker for every message. The actor goes on the ready queue, and a worker is woken only when none is already looking for work. An `Ask` or a grain call, whose caller waits for the answer, still hands the actor to a parked worker directly. A child spawned from its parent's handler is queued on the parent's worker.
- **Cheaper spawn and stop.** Spawns of different names no longer contend on one mutex. An actor's incarnation identity is generated without a system call, and is still a version 4 UUID. Actor names are validated without a regular expression. An actor's first mailbox segment holds 8 messages instead of 32 and grows when a backlog needs it. The `ActorChildCreated` event is built only when the topic has a subscriber. Outside a cluster, a stopping actor no longer sends a `Terminated` to the death watch, which had nothing to do with it. Registering an actor for passivation wakes the passivation loop only when its deadline is the earliest.

Message ordering, settings, errors and event delivery are unchanged.

## Documentation

- **One benchmark suite.** The `benchmark` package now holds eighteen scenarios covering sends, asks, actor-to-actor exchanges, spawn and stop, and grains, each reporting throughput, time, bytes and allocations per operation, plus the live heap per idle actor and grain. `benchmark/cmd/table` renders result files as tables. The earlier benchmarks were removed; the `scale` soak tests remain.
