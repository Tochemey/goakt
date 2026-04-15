# Dispatcher Pool — Design Document

|              |                                                                                         |
|--------------|-----------------------------------------------------------------------------------------|
| **Status**   | Fully implemented behind `useDispatcherPool` flag (default off). See §15.               |
| **Scope**    | Internal refactor of the actor message-processing scheduler                             |
| **Audience** | GoAkt maintainers and contributors                                                      |
| **Risk**     | High — touches core dispatch semantics                                                  |
| **User API** | Unchanged. `Tell`, `Ask`, `Spawn`, `Actor`, mailbox types — none of these are affected. |

---

## 1. Context

GoAkt today processes actor messages on a **spawn-on-demand goroutine model**: each burst of messages arriving at an idle actor causes a goroutine to be spawned, which drains the mailbox until empty, then exits. This is implemented in [`actor/pid.go`](../actor/pid.go) via a `CAS(idle→busy)` check that guarantees at most one drainer per actor.

This model has served well. Benchmarks show local `Tell` reaching ~6.9 million messages/sec with a pair of actors under parallel senders, and the remote `Tell` path — after the `v4.3.0` series of optimisations — reaches ~1.35 million messages/sec across 20 000 actors with zero send errors. For the vast majority of workloads, this is excellent.

However, the model has structural limitations that become visible at extreme scale or in specific workload shapes:

1. **One OS-observable scheduling unit per active actor.** When 50 000 actors are concurrently busy, the Go runtime has 50 000 goroutines contending for the `GOMAXPROCS` available processors. Each goroutine's park/unpark cycle bottoms out in `pthread_cond_wait` / `pthread_cond_signal` on Darwin (or `futex` on Linux). Profiling the remote benchmark shows ~60% of CPU spent in scheduler primitives; not all of this is actor scheduling (netpoll and channel park dominate), but the per-actor goroutine is a meaningful contributor at high actor counts.
2. **No global fairness budget.** An actor that receives a rapid burst of 10 000 messages will process all of them before its drainer goroutine exits. Under Go's preemptive scheduler this does not *starve* other actors — the runtime preempts every 10 ms — but it does produce unpredictable latency tails for other actors co-located on the same processor.
3. **Poor cache locality under fan-out.** Each actor's drainer goroutine may be reassigned to any processor at any time by the Go scheduler. The actor's mailbox, behaviour closure, and per-actor state are cold in the new processor's L1/L2 cache on every reassignment. A static pool of worker goroutines that reuse processor affinity gives much better cache behaviour.

These limitations are quantitative rather than qualitative. The goal of this document is to design — **not to immediately build** — a proper dispatcher-pool architecture, in the style of Akka / Pekko, for use when and if GoAkt's scale justifies the complexity.

---

## 2. Motivation

Industrial actor runtimes — **Akka**, **Pekko**, **Orleans**, **Erlang/OTP** — do not give each actor its own OS-observable scheduling unit. They share a small pool of worker threads across the entire actor population, using cooperative yielding and a fairness budget to rotate between actors. This is the structure that lets Erlang systems host millions of processes on a handful of schedulers, and that lets Akka dispatchers absorb tens of thousands of actors on a JVM thread pool sized to the CPU count.

In Go, goroutines are cheaper than threads but not free. The `m × p × g` runtime model implies genuine costs when `g ≫ p`: cross-processor G stealing, cold cache lines on every schedule, contention on run queues. These costs are sub-linear but observable, and for workloads with very high actor counts they become the dominant constraint.

A dispatcher pool addresses this at the cost of implementation complexity. The trade-off is not "faster vs slower" — it is "simpler but bounded" vs "more complex with a higher ceiling."

**This is purely an internal refactor.** User code does not see it. Users continue to call `Tell`, receive messages on `Actor.Receive`, define lifecycle hooks in `PreStart` / `PostStop`, and rely on the same ordering and isolation guarantees they have today. The swap happens below the `PID.handleReceived` boundary.

---

## 3. Goals and Non-Goals

### 3.1 Goals

- **G1 — Bound the goroutine count** used for actor message processing to a small, fixed pool. Actor count becomes orthogonal to goroutine count.
- **G2 — Preserve every observable semantic** of the current dispatch path: single-threaded execution per actor, FIFO ordering, `PreStart` / `PostStop` contract, reentrancy-stash behaviour, panic recovery, system-message priority, passivation, supervision.
- **G3 — Enforce per-actor fairness** via a throughput budget: an actor processes at most *N* messages per scheduling turn before yielding back to the pool.
- **G4 — Preserve system-message priority**. Shutdown, passivation, and supervision signals cannot be queued behind a user-message backlog.

### 3.2 Non-Goals

- **NG1 — Not a public API change.** No new exports, no new `With…` options, no configuration knobs exposed to users. The dispatcher is an implementation detail of the actor system; its parameters are set once in code, tuned by maintainers, and invisible to callers.
- **NG2 — Not a mailbox change.** The `Mailbox` interface, all existing implementations (unbounded, bounded, fair, segmented), and user-provided mailboxes continue to work unchanged.
- **NG3 — Not preemptive.** The dispatcher cooperatively rotates between actors at the `throughput` boundary. User code that blocks for a long time blocks the worker it runs on, exactly as it blocks its drainer today.
- **NG4 — Not distributed.** The dispatcher schedules within one node. Cross-node load balancing is the cluster layer's concern.

---

## 4. Current State — What We Have Today

Summarising [`actor/pid.go`](../actor/pid.go) for reference:

1. `PID.handleReceived(msg)` calls `mailbox.Enqueue(msg)`, then `PID.process()`.
2. `PID.process()` performs an atomic `CompareAndSwap(processing, idle, busy)`. If it fails, another drainer goroutine is already running; return.
3. If the CAS succeeds, a **new goroutine** is spawned that loops:
   - `received := mailbox.Dequeue()`
   - If `received == nil`: CAS `busy → idle`; return (goroutine exits).
   - Otherwise: handle the message (panic-recover, reentrancy-stash, system messages, user `Receive`).

**Observations:**

- At most one drainer goroutine per actor at any time. Preserves the per-actor single-threaded execution invariant.
- The drainer goroutine's lifetime is **a burst**, not the actor's. Actors that go quiet eventually have zero running goroutines.
- No throughput budget: a drainer stays on its actor until the mailbox is empty.
- No fairness across actors at the scheduler level. Go's preemptive runtime provides some fairness via 10 ms preemption, but nothing is enforced by GoAkt itself.

This works very well. The dispatcher-pool design below preserves every correctness property listed above while replacing the *execution substrate* underneath — from per-actor goroutines to a fixed pool.

---

## 5. Proposed Architecture

```
           ┌───────────────────────────────────────────┐
           │                  Actors                   │
           │   A1   A2   A3   …   A_n   (n → many)     │
           │  (mailbox + state machine each)           │
           └───────────────┬───────────────────────────┘
                           │ Enqueue wakes up scheduler
                           ▼
           ┌───────────────────────────────────────────┐
           │             Ready Queue(s)                │
           │  actors-with-pending-messages             │
           │  (per-worker local + global overflow)     │
           └───────────────┬───────────────────────────┘
                           │ Workers pull ready actors
                           ▼
           ┌──────┬──────┬──────┬─────┬──────┐
           │  W0  │  W1  │  W2  │ …   │  W_k │   ← fixed pool, k = GOMAXPROCS × 2
           └──────┴──────┴──────┴─────┴──────┘
              Each Wᵢ:
                for ready_actor := ReadyQueue.Take():
                    ready_actor.ProcessUpTo(throughput)  // N msgs or empty
                    if ready_actor.Mailbox.Pending() > 0:
                        ReadyQueue.Reschedule(ready_actor)
```

### 5.1 Components

- **Worker goroutines.** A fixed-size pool of long-lived goroutines, created once when the actor system starts and reused for every actor. Each worker pulls ready actors from its local queue, falling back to a global queue and to stealing from sibling workers.
- **Ready queue.** A hybrid per-worker + global MPMC structure of actors that have at least one message pending.
- **Actor state machine.** Each `PID` gains a lock-free status word with three states: `Idle`, `Scheduled`, `Processing`. State transitions are atomic and drive ready-queue membership.
- **Throughput budget.** A single internal constant: the maximum number of messages a worker processes from one actor before yielding and rescheduling it.

### 5.2 Scheduling Loop (Worker)

Pseudo-code for each worker:

```go
func (w *worker) run() {
    for !w.dispatcher.stopping.Load() {
        actor, ok := w.nextReady()  // local → global → steal → park
        if !ok {
            w.parkUntilSignalled()
            continue
        }
        w.runActor(actor)
    }
}

func (w *worker) runActor(actor *PID) {
    // Only one worker may hold an actor at a time.
    // Guaranteed by the Scheduled → Processing CAS performed by this worker
    // when it took the actor from the ready queue.
    budget := w.dispatcher.throughput
    for i := 0; i < budget; i++ {
        received := actor.mailbox.Dequeue()
        if received == nil {
            // Mailbox drained within budget. CAS Processing → Idle.
            // If a concurrent Enqueue raced with us, reschedule.
            if actor.finishIfMailboxEmpty() {
                return
            }
            continue // race: the concurrent Enqueue is now visible; keep processing
        }
        actor.process(received) // existing handler (panic-recover, system msgs, user Receive)
    }
    // Budget exhausted; yield. Push actor back to ready queue.
    actor.yieldAndReschedule()
}
```

### 5.3 Actor State Machine

```
                 ┌────────────┐
                 │    Idle    │
                 └─────┬──────┘
                       │  Enqueue  (CAS Idle → Scheduled)
                       │  + push to ReadyQueue
                       ▼
                 ┌────────────┐
                 │ Scheduled  │
                 └─────┬──────┘
                       │  Worker takes actor (CAS Scheduled → Processing)
                       ▼
                 ┌────────────┐
                 │ Processing │
                 └─────┬──────┘
                       │ Throughput hit, mailbox still has msgs
                       │   → yield: CAS Processing → Scheduled
                       │   → re-push to ReadyQueue
                       │
                       │ Mailbox drained
                       │   → CAS Processing → Idle
                       │   → if racing Enqueue made mailbox non-empty,
                       │     CAS Idle → Scheduled; re-push to ReadyQueue
                       ▼
                     …
```

The critical invariant: **at most one worker holds an actor in `Processing` at any time.** This preserves the single-threaded-execution-per-actor guarantee that user code relies on. Enforced by the atomic CAS on the `Scheduled → Processing` edge.

### 5.4 Enqueue Path

Replaces [`PID.handleReceived`](../actor/pid.go)'s current `process()` CAS:

```go
func (pid *PID) handleReceived(m *ReceiveContext) {
    _ = pid.mailbox.Enqueue(m)
    if pid.state.TrySchedule() {
        // Was Idle; now Scheduled. Push onto dispatcher.
        pid.dispatcher.push(pid)
    }
    // Already Scheduled or Processing: nothing to do.
    // A worker is already on it and will see the new message on
    // its next Dequeue, or on the post-budget mailbox check.
}
```

`TrySchedule` is one atomic CAS. The happy-path cost of enqueue is unchanged (one CAS, one mailbox op).

---

## 6. Fairness and Throughput Policy

### 6.1 The Throughput Budget

Each worker processes at most `N` messages from any one actor per turn. After the budget is hit, the worker **yields the actor** and moves on, even if the actor's mailbox has more messages. The actor is re-pushed to the end of the ready queue; other actors get a turn.

The budget is an internal constant, not user-configurable. Picking the default:

- **Akka default**: 5. Chosen for JVM thread-scheduling cost; JVM park/unpark is relatively cheap.
- **Go-appropriate**: likely in the 16–64 range. Go's park cost is higher than JVM's, so the budget wants to amortise over more messages; but too large a budget degrades fairness.
- **Proposed default**: 32. Revisit during benchmarking (see §9).

### 6.2 Starvation Guarantees

A yielded actor goes to the **end** of the ready queue, not the front. Combined with:

- Round-robin consumption across workers.
- Per-worker local queues + work-stealing from sibling workers.
- Global overflow queue consulted when a worker's local queue is empty.

…this gives a strong *progress* guarantee: every scheduled actor is eventually picked up by some worker. No actor can starve another indefinitely; the throughput budget caps the blocking window.

### 6.3 System-Message Priority

**Problem.** PoisonPill, PausePassivation, Panicking, HealthCheckRequest, etc. are control-plane messages. A user actor with a 10 000-message backlog must shut down on PoisonPill without waiting for the backlog.

**Design.** Two mailboxes per actor:

- `system` — always consulted first at the start of each turn and between user messages.
- `user` — normal FIFO of user messages, used when `system` is empty.

This matches the Akka / Pekko model. The existing `Mailbox` interface stays unchanged; the system mailbox is a separate, internal slot on `PID`. System-message routing is also internal — `handleReceived` inspects the message type and dispatches to the appropriate slot, exactly as it does today.

### 6.4 Ready Queue Bounds

The ready queue holds **actors**, not messages. Its maximum size is bounded by the total number of actors with at least one pending message — which is itself bounded by the actor count. So the queue is naturally bounded in any running system.

Per-worker local queues are bounded (proposed: 256 actors). Overflow spills to the unbounded global queue. Bounds per worker are fixed; total bound scales with worker count.

---

## 7. Internal Knobs

Nothing in this design is user-configurable. The following live as constants or unexported fields in the `actor` package. They are tuned once during implementation and benchmarking, and revisited only when new data suggests a different value:

| Knob                            | Proposed value   | Rationale                                                                           |
|---------------------------------|------------------|-------------------------------------------------------------------------------------|
| Worker pool size                | `GOMAXPROCS × 2` | Enough workers to cover occasional blocking on I/O without oversubscribing.         |
| Throughput                      | 32               | Amortises Go's park/unpark cost over a modest batch; preserves fairness (see §6.1). |
| Per-worker local queue capacity | 256              | Most workloads never overflow this; overflow spills cleanly to the global queue.    |
| System mailbox depth            | unbounded        | System messages are rare; dropping one is worse than holding one.                   |

All four are single-source-of-truth within the `actor` package. Changing any of them is a one-line edit followed by re-running the benchmark suite.

---

## 8. Implementation Details

### 8.1 Ready Queue Structure

**Per-worker local queue.** A fixed-capacity ring buffer (256 slots). Push is lock-free fast-path; full → push to global.

**Global overflow queue.** An MPMC queue. Lock-free implementations exist; a simple mutex-protected linked list is acceptable for the first cut.

**Work stealing.** When a worker's local queue is empty and the global queue is empty, it attempts to steal half of a sibling's local queue. Standard work-stealing as in `tokio`, Go's own runtime, and Akka's `ForkJoinPool`.

**Idle parking.** When the global and all local queues are empty and no steal succeeds, the worker parks on a condition variable / semaphore. `dispatcher.push` signals one parked worker. Prefer LIFO wakeup for cache warmth.

### 8.2 Shutdown

When the actor system stops:

1. Atomically set `stopping = true`. Workers finish their current actor's budget, then exit the outer loop instead of taking another actor.
2. Drain the ready queue: any scheduled actors still there have their mailboxes drained synchronously (respecting throughput budgets) until empty.
3. System messages (PoisonPill, PostStop) are guaranteed to be delivered during drain, preserving the current shutdown contract.
4. If the actor system's `Stop(ctx)` deadline expires before drain completes, remaining messages become dead letters — the same behaviour as today.

### 8.3 Lifecycle and Observable Behaviour

The dispatcher pool preserves:

- **Single-threaded actor execution**: at most one worker holds an actor at a time.
- **FIFO message ordering**: the mailbox is unchanged; Dequeue order is unchanged.
- **`PreStart` / `PostStop` contract**: run exactly once per lifecycle, on the worker that first delivers to the actor and on the worker that drains the final PoisonPill, respectively.
- **Reentrancy stash**: unchanged. The stash is per-actor state, orthogonal to who's running the actor.
- **Panic recovery**: unchanged. Still a `defer recover()` within `process`.

What observably changes:

- **Goroutine count**: drops from "one per active actor" to `worker pool size + O(1)`. Users who call `runtime.NumGoroutine()` will see the difference. This is the one externally-visible change — but it's a counter, not a behavior, so no user code needs to adjust.
- **Latency distribution**: tighter p99 because fairness is enforced explicitly rather than relying on the Go runtime's 10 ms preemption.

---

## 9. Testing Strategy

### 9.1 Unit Tests

- **Actor state machine.** Exhaustive transition tests. Concurrent CAS under `-race`. 100× iteration stress.
- **Ready queue.** FIFO under single-worker; fair rotation across workers; work-stealing correctness; no duplication or loss under N-producer / M-consumer torture.
- **Worker scheduling.** Throughput-budget boundary tested with an actor that would otherwise monopolise; verify rotation.
- **Shutdown.** Drain-on-stop delivers all pending messages including system messages; actor-system `ctx` deadline honoured.

### 9.2 Integration Tests

**The existing `actor/` test suite must pass unchanged.** This is the primary correctness gate. No test modifications should be needed — if any test breaks, the dispatcher has changed an observable semantic and needs to be fixed, not the test. The rare legitimate exception is tests that explicitly assert on `runtime.NumGoroutine()` (if any exist); those are updated to reflect the new, correct count.

The cluster, CRDT, passivation, and reentrancy suites are part of the regression surface and must also pass unchanged.

### 9.3 Property Tests

- **Liveness**: for any sequence of Enqueues, every message is eventually handled (assuming the actor does not block).
- **Safety**: at most one worker executes a given actor at any instant. Verify with `-race` + concurrent sends + trace assertions.
- **Order**: messages `m1, m2, m3` sent by the same producer to the same actor are processed in order.
- **System priority**: a PoisonPill enqueued alongside 1 000 user messages causes shutdown within `throughput + systemMailboxDepth` messages, not 1 000.

### 9.4 Benchmarks

- **Throughput**: 2 actors, parallel senders, 10s — target ≥ current 6.9M msg/s.
- **Fan-out**: 20 000 actors, single sender, measure per-actor latency distribution. Goal: tighter p99 than the current model.
- **Mixed rate**: 1 000 actors where one actor receives 10 000 msg/s and the others receive 100 msg/s — verify fair scheduling keeps the slow actors responsive.
- **Goroutine count**: assert `runtime.NumGoroutine()` stays within `workerPoolSize + O(1)` regardless of actor activity.

### 9.5 Race Detector

All new code paths must run clean under `-race -count=10`. The state-machine transitions and ready-queue operations are the highest-risk concurrency code in the system; treat race-detector runs as non-optional.

---

## 10. Observability

The dispatcher is a natural place for internal telemetry. Propose adding (all exposed via the existing OpenTelemetry metrics surface, cardinality constant):

- **Gauge** `dispatcher.workers_busy` — workers currently running an actor.
- **Gauge** `dispatcher.ready_queue_depth` — scheduled actors waiting.
- **Counter** `dispatcher.actors_scheduled_total` — cumulative schedules since start.
- **Histogram** `dispatcher.messages_per_turn` — budget utilisation distribution.
- **Histogram** `dispatcher.actor_turn_duration` — time each turn took.

These are operator-facing signals, useful for diagnosing overload. They are not user API.

---

## 11. Alternatives Considered

### 11.1 Do Nothing

**Pros:** zero risk, zero code. Current benchmarks are strong.
**Cons:** leaves extreme-scale performance on the table; does not address fairness under burst workloads.

Evaluated in §1. The right choice for most users. The dispatcher pool exists for workloads the current model does not serve well.

### 11.2 Batched Mailbox Dequeue

Keep per-actor goroutines; amortise park/unpark by pulling N messages per Dequeue call.

**Pros:** ~1 day of work; preserves current architecture.
**Cons:** does not solve the "too many goroutines" problem. Profiling earlier established this is not the current bottleneck anyway.

Rejected — targets a different problem.

### 11.3 Preemptive Scheduling

Use `runtime.Gosched()` aggressively, or a time-based preemption.

**Pros:** no new goroutine structure.
**Cons:** violates the cooperative actor model; unpredictable for user code; does not reduce the total goroutine count.

Rejected.

### 11.4 Simple Task Pool Without Throughput Budget

A pool of worker goroutines that each pick up an actor-drainer work unit and run it to mailbox-empty before picking the next.

**Pros:** simpler than a full dispatcher with ready queue + work stealing.
**Cons:** does not enforce the throughput budget — a single drainer-task still monopolises a worker until the mailbox is empty. Doesn't solve fairness.

Rejected. The throughput budget is central to the design's value proposition.

---

## 12. Open Questions

Empirical questions that should be resolved through prototyping and benchmarking before a final implementation merge:

1. **Default worker count.** `GOMAXPROCS × 2` is the opening bid. Workloads with blocking I/O may want higher; CPU-bound workloads may want exactly `GOMAXPROCS`. Pick the value that minimises p99 latency across our benchmark set, and document the trade-off.
2. **Default throughput budget.** 32 is a guess. Sweep values `{1, 4, 16, 32, 64, 128, 256}` across the benchmark scenarios to establish the knee.
3. **Per-worker local queue capacity.** 256 is a guess. Measure overflow rate under load; adjust if overflow becomes common.
4. **LIFO vs FIFO wakeup of parked workers.** LIFO preserves cache warmth; FIFO is fairer. Likely LIFO, but measure.
5. **Interaction with passivation.** Passivation relies on "no messages in X time." Verify the dispatcher does not introduce artificial inactivity windows that trigger unintended passivation.
6. **Interaction with reentrancy.** The reentrancy stash assumes "the actor is running on a specific goroutine at the moment of reentrance." This assumption still holds under the dispatcher — reentrance happens within a single worker's call stack for one actor — but must be verified end-to-end.

---

## 13. Cost Estimate

- **Prototype + benchmarks** (validate design assumptions, default tuning): 1 week.
- **Full implementation** (worker pool, ready queue, state machine, integration with `PID`, tests, docs): 2–3 weeks of focused work.
- **Stabilisation + bug triage** after the merge: rolling, 1–2 months of attention as the new path gets real workload exposure.

Total: on the order of **4–6 weeks** of focused engineering, plus the stabilisation period. This reflects a careful, unhurried implementation. This is core dispatch code — corner-cutting here surfaces as data races and lost messages in production months after the merge. Budget accordingly.

---

## 14. Summary

A dispatcher pool is an internal refactor of GoAkt's message-scheduling substrate. It replaces the current "spawn a goroutine per burst" model with a fixed pool of worker goroutines and a throughput-budgeted cooperative scheduler, following the Akka / Pekko / Erlang / Orleans pattern.

It is a pure implementation change: no public API is added, removed, or modified. Users continue to call `Tell`, receive on `Actor.Receive`, and rely on the same ordering and isolation guarantees they have today. The only externally-visible difference is that `runtime.NumGoroutine()` returns a much smaller, workload-independent number — and latency distributions are tighter at p99.

For GoAkt's current benchmarks (6.9M msg/s local, 1.35M msg/s remote across 20 000 actors), this refactor is not strictly necessary — the existing model is excellent. The design exists for two reasons:

1. Workloads at 100 000+ active actors or very high per-node message rates where goroutine count becomes a bottleneck.
2. Workloads that demand explicit fairness guarantees — latency-sensitive systems where unpredictable preemption from Go's runtime is unacceptable.

Until a user workload demonstrates the need, the right engineering move is to **land the design, not the code**, and revisit when priorities shift.

---

## 15. Current Implementation State

Full dispatcher-pool implementation landed behind an internal constant
`useDispatcherPool` (default `false`). The default path is the legacy
spawn-on-burst drainer and is untouched.

### 15.1 Landed

- **Primitives** (self-contained, 100% covered, race-clean):
  - [`actor/dispatch_state.go`](../actor/dispatch_state.go) — three-state machine (Idle/Scheduled/Processing) per §5.3.
  - [`actor/ready_queue.go`](../actor/ready_queue.go) — per-worker 256-slot local rings, global overflow slice, half-quota work-stealing, `sync.Cond` parking, atomic `globalCount` hint.
  - [`actor/dispatcher.go`](../actor/dispatcher.go) — fixed worker pool (`GOMAXPROCS × 2`), idempotent `start`/`signalStop`/`stop`, throughput budget = 32.
  - [`actor/worker.go`](../actor/worker.go) — worker loop, `reschedule` onto the owning local queue.

- **PID / ActorSystem wiring**:
  - [`actor/actor_system.go`](../actor/actor_system.go) — `dispatcher` field constructed+started in `Start`, torn down via `signalStop` in `shutdown`'s defer (non-blocking — safe when shutdown is triggered from inside a worker turn).
  - [`actor/pid.go`](../actor/pid.go) — `schedState dispatchState`, `systemMailbox Mailbox`, `runTurn(*worker)` per §A.2, `dispatchOne` returns a `retained bool` so stashed contexts aren't recycled, restart barrier waits on `dispatchProcessing`.

- **System mailbox split (§6.3)** — `PoisonPill`, `Panicking`, `Pause/ResumePassivation`, `HealthCheckRequest`, `Terminated`, `PanicSignal`, `SendDeadletter` route to `systemMailbox` and are drained before user messages each turn. PoisonPill no longer queues behind a user-message backlog.

### 15.2 Bugs found and fixed during integration

1. **`dispatcher.stop()` self-deadlock** when `ActorSystem.Stop()` is invoked from inside a worker turn (e.g. `rootGuardian.handlePanicSignal`): the calling worker was waiting on its own `WaitGroup`. Fixed by introducing `signalStop()` (close + wake, no wait) and relocating it to `shutdown()`'s defer.
2. **Stashed-context release-pool corruption**: `runTurn` unconditionally returned the per-message context to the pool, zeroing it while the reentrancy stash still held a pointer. Fixed by returning `retained bool` from `dispatchOne`.

### 15.3 Benchmark profile (Apple M1, 8 cores)

Head-to-head on [`benchmark/processing_test.go`](../benchmark/processing_test.go) (3s × 3 iters, -benchmem):

| Benchmark   | Flag OFF       | Flag ON        | Δ        |
|-------------|----------------|----------------|----------|
| Tell        | 7.4 M msg/s    | **7.9 M**      | **+7%**  |
| Request     | 498 K msg/s    | **658 K**      | **+32%** |
| SendAsync   | 6.1 M msg/s    | **6.5 M**      | **+7%**  |
| Ask         | **1.37 M**     | 1.01 M         | **−26%** |
| SendSync    | **1.32 M**     | 0.96 M         | **−27%** |

**Asymmetry is structural.** The dispatcher pool wins on amortised burst
throughput (Tell, Request, SendAsync) because a worker drains many messages
per park/unpark cycle. It loses on single-shot synchronous round-trips (Ask,
SendSync) because each round-trip pays one `pthread_cond` wake (~200 ns on
Darwin) that the legacy spawn-on-burst model skips by spawning a fresh drainer
in-line with the sender's `Tell` call.

Spin-then-park variants (tight-loop on atomic hint, Gosched-loop, long spin)
were all tested — none recover the sync regression without degrading
throughput. The underlying cost is intrinsic to cooperative goroutine
parking on macOS; it is inherent to the dispatcher-pool architecture, not a
tuning knob.

### 15.4 Default = off, available = yes

The dispatcher pool is kept at `useDispatcherPool = false` by default because
uniform improvement across message patterns is not achieved. It is a net win
for **Tell-heavy, high-actor-count workloads**; a net loss for
**synchronous request/reply-dominated workloads**.

Maintainers who want to benchmark it against their own workload can flip the
constant in [`actor/dispatcher.go`](../actor/dispatcher.go) and rebuild.

### 15.5 Non-Goals still deferred

- Telemetry (§10) — OpenTelemetry metrics surface.
- Benchmark sweep / knob tuning on the internal constants.

---

## Appendix A — Pseudo-code Reference

### A.1 Actor state word

```go
const (
    stateIdle       = uint32(0)
    stateScheduled  = uint32(1)
    stateProcessing = uint32(2)
)

type actorState struct {
    v atomic.Uint32
}

// TrySchedule: Idle -> Scheduled. Returns true if we were the one who transitioned.
func (s *actorState) TrySchedule() bool {
    return s.v.CompareAndSwap(stateIdle, stateScheduled)
}

// TakeForProcessing: Scheduled -> Processing. Called by a worker that has
// pulled the actor from the ready queue and now holds exclusive access.
func (s *actorState) TakeForProcessing() bool {
    return s.v.CompareAndSwap(stateScheduled, stateProcessing)
}

// YieldAndReschedule: Processing -> Scheduled. Called when a worker is
// rotating off an actor that still has messages pending.
func (s *actorState) YieldAndReschedule() {
    s.v.Store(stateScheduled)
}

// FinishIfEmpty: Processing -> Idle, but only if the mailbox is still empty
// after we CAS. Caller is responsible for consulting the mailbox.
func (s *actorState) FinishIfEmpty(mailboxEmpty func() bool) bool {
    s.v.Store(stateIdle)
    if !mailboxEmpty() {
        // Race: an Enqueue slipped in between our last Dequeue and our
        // state store. Try to reclaim.
        if s.v.CompareAndSwap(stateIdle, stateScheduled) {
            return false // caller must re-push to ready queue
        }
    }
    return true
}
```

### A.2 Worker loop (full)

```go
func (w *worker) run() {
    defer w.dispatcher.wg.Done()
    for {
        if w.dispatcher.stopping.Load() {
            return
        }
        actor, ok := w.dispatcher.takeReady(w.id)
        if !ok {
            w.park()
            continue
        }
        if !actor.state.TakeForProcessing() {
            // Race: someone else already picked it up, or it was cancelled.
            continue
        }
        w.processActor(actor)
    }
}

func (w *worker) processActor(actor *PID) {
    budget := dispatcherThroughput // internal constant
    for i := 0; i < budget; i++ {
        // System messages always win.
        if sysMsg := actor.systemMailbox.TryDequeue(); sysMsg != nil {
            actor.handleSystem(sysMsg)
            continue
        }
        received := actor.mailbox.Dequeue()
        if received == nil {
            if actor.state.FinishIfEmpty(actor.mailbox.IsEmpty) {
                return
            }
            // Reclaimed; keep processing.
            continue
        }
        actor.process(received)
    }
    actor.state.YieldAndReschedule()
    w.dispatcher.push(actor)
}
```

### A.3 Enqueue

```go
func (pid *PID) handleReceived(m *ReceiveContext) {
    if err := pid.mailbox.Enqueue(m); err != nil {
        pid.logger.Warn(err)
        pid.handleReceivedError(m, err)
        return
    }
    if pid.state.TrySchedule() {
        pid.dispatcher.push(pid)
    }
}
```

---

## Appendix B — References

- Akka documentation, Dispatchers chapter: <https://doc.akka.io/docs/akka/current/typed/dispatchers.html>
- Pekko Dispatchers: <https://pekko.apache.org/docs/pekko/current/typed/dispatchers.html>
- Erlang/OTP scheduler design: <https://www.erlang.org/blog/a-closer-look-at-the-erlang-vm/>
- Microsoft Orleans schedulers: <https://learn.microsoft.com/en-us/dotnet/orleans/implementation/scheduler>
- Tokio work-stealing scheduler: <https://tokio.rs/blog/2019-10-scheduler>
- Go runtime scheduler design: <https://rakyll.org/scheduler/>
