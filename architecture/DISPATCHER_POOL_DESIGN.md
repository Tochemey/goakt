# Dispatcher Pool — Architecture

> The execution substrate underneath every actor in GoAkt.

---

## Table of Contents

1. [Overview](#1-overview)
2. [Components](#2-components)
3. [Actor State Machine](#3-actor-state-machine)
4. [Enqueue Path](#4-enqueue-path)
5. [Worker Turn](#5-worker-turn)
6. [Ready Queue](#6-ready-queue)
7. [System-Message Priority](#7-system-message-priority)
8. [Lifecycle](#8-lifecycle)
9. [Observable Guarantees](#9-observable-guarantees)
10. [Tuning Constants](#10-tuning-constants)

---

## 1. Overview

GoAkt processes actor messages on a **fixed pool of worker goroutines** that
cooperatively multiplex the entire actor population. Each `ActorSystem` owns
one `dispatcher`. When an actor receives a message, it is scheduled onto a
shared ready queue; a worker pulls it off, drains up to `throughput` messages
from its mailboxes, and yields back so other actors get a turn.

This is the Akka / Pekko / Erlang / Orleans pattern adapted for Go: the
worker count is bounded by `GOMAXPROCS`, independent of the actor count.
Actors become units of work on a scheduler rather than units that each own a
goroutine.

```
           ┌───────────────────────────────────────────┐
           │                  Actors                   │
           │   A1   A2   A3   …   A_n   (n → many)     │
           │  (user mailbox + system mailbox + state)  │
           └───────────────┬───────────────────────────┘
                           │ schedState.TrySchedule() wakes a worker
                           ▼
           ┌───────────────────────────────────────────┐
           │             Ready Queue                   │
           │  per-worker local rings (256 slots) +     │
           │  shared global ring (amortised FIFO)      │
           └───────────────┬───────────────────────────┘
                           │ Workers pull ready actors
                           ▼
           ┌──────┬──────┬──────┬─────┬──────┐
           │  W0  │  W1  │  W2  │ …   │  W_k │   ← k = max(GOMAXPROCS, 2)
           └──────┴──────┴──────┴─────┴──────┘
              Each Wᵢ loop:
                 s := readyQueue.take(i)          // local → global → steal → park
                 s.runTurn(w)                     // drain ≤ 32 msgs, then yield
```

The public API is untouched: `Tell`, `Ask`, `Spawn`, `Actor.Receive`,
`PreStart`, `PostStop`, mailboxes — all unchanged. The dispatcher lives
below `PID.doReceive` and is an implementation detail of the actor package.

---

## 2. Components

| File                                                    | Responsibility                                                                    |
|---------------------------------------------------------|-----------------------------------------------------------------------------------|
| [`actor/dispatcher.go`](../actor/dispatcher.go)         | Worker pool, lifecycle (`start` / `stop` / `signalStop`), `schedule` entry point. |
| [`actor/worker.go`](../actor/worker.go)                 | Worker goroutine loop, local-queue reschedule helper.                             |
| [`actor/ready_queue.go`](../actor/ready_queue.go)       | Per-worker ring buffers, global overflow ring, work stealing, condvar parking.    |
| [`actor/dispatch_state.go`](../actor/dispatch_state.go) | Three-state atomic machine (`Idle` / `Scheduled` / `Processing`) per actor.       |
| [`actor/pid.go`](../actor/pid.go)                       | `schedulable.runTurn` implementation, mailbox split, `doReceive` enqueue path.    |

The dispatcher is constructed in
[`actorSystem.Start`](../actor/actor_system.go) via
`newDispatcher(dispatcherWorkerCount(), dispatcherThroughput)` and started
before any actor is spawned.

### 2.1 `schedulable` interface

The worker is agnostic to the actor state machine. `ready_queue.go` defines:

```go
type schedulable interface {
    runTurn(w *worker)
}
```

`PID` implements `runTurn`. This keeps the dispatcher a reusable scheduling
primitive; any future schedulable (e.g. timers, stream stages) can plug in
without touching the worker loop.

---

## 3. Actor State Machine

Each `PID` carries a lock-free `schedState` with three values:

```
            ┌────────────┐
            │    Idle    │   no pending work, not on ready queue
            └─────┬──────┘
                  │  Enqueue observes Idle and CAS → Scheduled
                  │  producer pushes PID onto dispatcher
                  ▼
            ┌────────────┐
            │ Scheduled  │   on ready queue, waiting for a worker
            └─────┬──────┘
                  │  Worker takes actor and CAS Scheduled → Processing
                  ▼
            ┌────────────┐
            │ Processing │   exactly one worker owns the actor
            └─────┬──────┘
                  │ Throughput hit, mailbox non-empty
                  │   → Store Scheduled; re-push to owning worker's local ring
                  │
                  │ Budget exhausted with empty mailboxes
                  │   → Store Idle; re-check mailboxes; if a racing enqueue
                  │     slipped in, CAS Idle → Scheduled and reclaim
                  ▼
                …
```

**Invariant:** at most one worker holds an actor in `Processing`. Enforced by
the `Scheduled → Processing` CAS at the top of `runTurn`; this preserves the
actor model's single-threaded-per-actor execution guarantee that user code
relies on.

`TrySchedule` reads the state before attempting the CAS. Under N-way parallel
`Tell` to the same actor, the hot cache line is read in shared mode by N-1
losers; an unconditional CAS would force request-for-ownership traffic on
every call.

---

## 4. Enqueue Path

`PID.doReceive` is the unified enqueue entry point:

```go
func (pid *PID) doReceive(receiveCtx *ReceiveContext) {
    msg := receiveCtx.Message()
    // …stopping guard…
    if isControlMessage(msg) {
        _ = pid.systemMailbox.Enqueue(receiveCtx)
    } else if err := pid.mailbox.Enqueue(receiveCtx); err != nil {
        pid.handleReceivedError(receiveCtx, err)
        return
    }
    if pid.schedState.TrySchedule() {
        pid.dispatcher.schedule(pid)
    }
}
```

`isControlMessage` routes `PoisonPill`, `Panicking`, `Pause/ResumePassivation`,
`PanicSignal`, `Terminated`, and `SendDeadletter` to the system mailbox; all
other messages go to the user mailbox. `AsyncRequest` / `AsyncResponse` are
*not* control messages — they participate in the reentrancy stash and must
keep FIFO order with user traffic.

The hot path is one atomic read, one CAS, one mailbox op, and (for the first
producer that wins the `Idle → Scheduled` transition) one global-queue push
with condvar signal.

---

## 5. Worker Turn

`PID.runTurn` is the `schedulable` implementation. The worker calls it after
`readyQueue.take` returns a `PID`:

```go
func (pid *PID) runTurn(w *worker) {
    if !pid.schedState.TakeForProcessing() {
        return
    }
    now := time.Now()
    budget := w.dispatcher.throughput
    for range budget {
        if sysMsg := pid.systemMailbox.Dequeue(); sysMsg != nil {
            if !pid.dispatchOne(sysMsg, now) {
                releaseContext(sysMsg)
            }
            continue
        }
        received := pid.mailbox.Dequeue()
        if received == nil {
            if pid.finishOrReclaim() {
                return
            }
            continue
        }
        if !pid.dispatchOne(received, now) {
            releaseContext(received)
        }
    }
    pid.schedState.YieldToScheduled()
    w.reschedule(pid)
}
```

Two correctness details worth calling out:

- **`dispatchOne` returns `retained bool`.** The reentrancy-stash path holds
  on to the `ReceiveContext` beyond the turn, so the caller must *not* return
  it to the pool in that case. All other paths dispatch in place and the
  context is released.
- **`finishOrReclaim` closes the enqueue/finish race.** After a drained
  dequeue, it stores `Idle`, re-reads both mailboxes, and — if a concurrent
  `doReceive` slipped a message in between the last dequeue and the state
  store — attempts `TrySchedule` followed by `TakeForProcessing` to reclaim
  ownership and keep draining within the current turn.

When the budget is exhausted and work remains, the actor yields to
`Scheduled` and is re-pushed onto the owning worker's local ring via
`worker.reschedule`. Yielded actors land at the **tail** of the local ring,
behind any other scheduled actor, guaranteeing forward progress for peers.

---

## 6. Ready Queue

`readyQueue` combines per-worker local rings with a shared global ring and
condvar parking. The take priority is: own local → global → steal from
siblings → park.

### 6.1 Local rings

Each worker has a `localQueue`: a mutex-guarded 256-slot ring (`localQueueCap`).
Push and pop are FIFO from the owner's side; stealers take from the head.
A `sizeAtomic` counter mirrors the live count and is read without the mutex
so empty victims cost one atomic load during a steal probe.

Local-ring overflow (owner push finds the ring full) spills into the global
ring. The owner always drains its local ring first, so overflow is rare in
practice.

### 6.2 Global ring

`globalQueue` is an amortised-FIFO ring buffer starting at 64 slots
(`globalQueueInitialCap`) and doubling on overflow. It replaces a naive
`slice = slice[1:]` implementation whose backing array would grow unboundedly
under steady churn. The global ring is guarded by `readyQueue.parkMu`, the
same mutex the condition variable uses.

A lock-free `globalCount` atomic mirrors `global.size`. Workers consult it on
the take fast-path to skip the mutex when the global ring is known empty.

### 6.3 Work stealing

When both the local and global rings are empty, the worker rotates through
sibling workers and attempts to steal half of the first non-empty victim.
The probe uses the victim's `sizeAtomic` to skip idle siblings without a
mutex acquire — this matters at scale because every park/wake cycle walks
the sibling array, and the N-way mutex fan-out dominated the take loop
before the atomic-probe optimisation.

`stealHalf` moves half the victim's contents into the thief's ring under
pointer-ordered locking (`lockOrder`) to avoid deadlock when two workers
attempt to steal from each other simultaneously.

### 6.4 Parking

When no work is found anywhere, the worker parks on `readyQueue.cond`.
Producers signal the condvar only when `parked > 0`, skipping the syscall on
the hot path. `parkAndTake` fuses the park and the subsequent global-ring pop
into one critical section so a woken worker does not unlock only to
re-acquire immediately.

`close` sets a flag and broadcasts. Parked workers observe the flag on wake
and exit their run loop.

---

## 7. System-Message Priority

Control-plane messages (`PoisonPill`, supervision, passivation, dead-letter
routing) cannot queue behind a user-message backlog. Every actor has two
mailboxes:

- `systemMailbox Mailbox` — an unbounded mailbox that holds control
  messages. Always consulted first inside the budget loop.
- `mailbox Mailbox` — the normal user mailbox, unchanged from the public
  `Mailbox` contract.

This is the same split used by Akka and Pekko. Because `systemMailbox` is
drained before the user mailbox on every iteration, a `PoisonPill` delivered
to an actor with 10 000 user messages queued shuts the actor down after at
most one additional user message, not 10 000.

User-provided custom mailboxes continue to work: they sit in the `mailbox`
slot and are not aware of the system mailbox.

---

## 8. Lifecycle

### 8.1 Start

`actorSystem.Start` constructs the dispatcher with
`workerCount = max(GOMAXPROCS, 2)` and `throughput = 32`, then calls
`dispatcher.start()` which spawns the worker goroutines. Both `start` and the
stop variants are idempotent.

### 8.2 Shutdown

`dispatcher.stop()` closes the ready queue and blocks on the worker
`WaitGroup`. It must not be called from within a worker turn — the caller
would wait on its own completion and deadlock.

`actorSystem.shutdown` can be triggered from inside an actor receive handler
(for example, `rootGuardian.handlePanicSignal`), so it uses
`dispatcher.signalStop()` from the shutdown defer instead. `signalStop`
closes the ready queue and wakes all parked workers without blocking; each
worker finishes its current turn and exits autonomously on its next take.

PoisonPill delivery is preserved during shutdown: the system-message path is
drained at turn time, so actors still process their final
lifecycle messages even as the system unwinds.

### 8.3 Restart

`PID` restart waits for any in-flight turn to finish before reinitialising:

```go
for pid.schedState.Load() == dispatchProcessing {
    runtime.Gosched()
}
```

The MPSC mailbox is single-consumer, so restarting while a worker is mid
`Dequeue` would be a data race. After reinitialisation the state is reset
to `Idle` and subsequent enqueues re-schedule the actor normally.

---

## 9. Observable Guarantees

The dispatcher pool preserves every semantic that user code relies on:

- **Single-threaded actor execution.** The `Scheduled → Processing` CAS
  guarantees at most one worker holds an actor at a time.
- **FIFO within a producer.** Messages `m1, m2, m3` sent by the same
  producer to the same actor are dequeued in order (mailbox contract
  unchanged).
- **`PreStart` / `PostStop` contract.** Run exactly once per lifecycle,
  inside a turn on whichever worker happens to own the actor.
- **Reentrancy stash.** Unchanged; reentrance happens within a single
  worker's call stack for one actor.
- **Panic recovery.** `defer recover()` inside `dispatchOne`, same as before.
- **Supervision.** Supervisor directives run on the parent's turn after the
  child's `Panicking` message reaches the parent's system mailbox.

The only externally-visible change is `runtime.NumGoroutine()`: it now
returns `workerCount + O(1)` instead of scaling with active-actor count.

---

## 10. Tuning Constants

All four are unexported package constants, tuned once and revisited only
when benchmarks demand it:

| Constant                | Value                  | Location                                          |
|-------------------------|------------------------|---------------------------------------------------|
| Worker pool size        | `max(GOMAXPROCS, 2)`   | `dispatcherWorkerCount` in `actor/dispatcher.go`  |
| Throughput budget       | `32`                   | `dispatcherThroughput` in `actor/dispatcher.go`   |
| Local-ring capacity     | `256`                  | `localQueueCap` in `actor/ready_queue.go`         |
| Global-ring initial cap | `64` (doubles on grow) | `globalQueueInitialCap` in `actor/ready_queue.go` |

The worker count is floored at 2 so work-stealing always has at least one
sibling to probe. The throughput budget is chosen to amortise Go's
park/unpark cost over a meaningful batch while still bounding the blocking
window a single actor can impose on its peers.

---

## References

- Akka Dispatchers: <https://doc.akka.io/docs/akka/current/typed/dispatchers.html>
- Pekko Dispatchers: <https://pekko.apache.org/docs/pekko/current/typed/dispatchers.html>
- Erlang/OTP scheduler: <https://www.erlang.org/blog/a-closer-look-at-the-erlang-vm/>
- Microsoft Orleans schedulers: <https://learn.microsoft.com/en-us/dotnet/orleans/implementation/scheduler>
- Tokio work-stealing scheduler: <https://tokio.rs/blog/2019-10-scheduler>
- Go runtime scheduler: <https://rakyll.org/scheduler/>
