# 10. Passivation and Eviction

## Contents

- [What you will learn](#what-you-will-learn)
- [10.1 Strategies](#101-strategies)
- [10.2 The manager and its entries](#102-the-manager-and-its-entries)
- [10.3 Activity](#103-activity)
- [10.4 The loop](#104-the-loop)
- [10.5 One attempt](#105-one-attempt)
- [10.6 Pause, resume, reinstate](#106-pause-resume-reinstate)
- [10.7 System eviction](#107-system-eviction)
- [Guarantees](#guarantees)
- [Implementation details (may change)](#implementation-details-may-change)
- [Behaviours to know](#behaviours-to-know)

## What you will learn

- The three passivation strategies, which actor gets which, and what each costs.
- How an actor's activity reaches the passivation manager, and why the manager may not trust its own deadline.
- What the manager's one goroutine does on a deadline or a message count, and what it does with an actor that refuses.
- What a passivation attempt is, step by step, and the four races it closes.
- How pause, resume and reinstate interact with passivation.
- What system eviction is, how it picks actors, and why it is a stop rather than a passivation.

Source files: `passivation/strategy.go`, `actor/passivation_manager.go`, `actor/system_eviction.go`, and the passivation path in `actor/pid.go` and `actor/actor_system.go`.

## 10.1 Strategies

A `Strategy` is a name and a string (`passivation/strategy.go`); the three implementations carry the parameters:

| Strategy | Constructor | Meaning | Source |
|---|---|---|---|
| time-based | `NewTimeBasedStrategy(timeout)` | stop the actor once it has been idle for `timeout` | `NewTimeBasedStrategy` in `passivation/strategy.go` |
| message-count | `NewMessageCountBasedStrategy(n)` | stop the actor once it has handled `n` user messages | `NewMessageCountBasedStrategy` in `passivation/strategy.go` |
| long-lived | `NewLongLivedStrategy()` | never passivate | `NewLongLivedStrategy` in `passivation/strategy.go` |

A `Strategy` is configuration, not behaviour: the interface has only `Name` and `String`, and all the logic is in the passivation manager. Adding a fourth strategy is therefore not a matter of implementing the interface. Spawn validation rejects any type other than the three, so a new one needs changes to that validation, to `withPassivationStrategy` and to the manager.

The constructors do not validate their argument: a timeout of zero passivates the actor right after it starts, and a count of zero passivates it after `PostStart`. Spawn validation only checks that the strategy is one of the three types (`spawnConfig.Validate` in `actor/spawn_option.go`).

**Which strategy an actor gets.** `WithPassivationStrategy` and `WithLongLived` set it per spawn (`actor/spawn_option.go`). Without an option, an actor gets the system default, a time-based strategy of two minutes (`NewActorSystem` in `actor/actor_system.go`; `DefaultPassivationTimeout` in `actor/defaults.go`); a child spawned without an option gets the same default from `newPID` (`actor/pid.go`). There is no option that changes that default system-wide. System actors, singletons, routers and their routees, relocation workers, and reliable-delivery endpoints and their controllers are long-lived (`actorSystem.SpawnRouter` in `actor/spawn.go`; `router.spawnRoutees` in `actor/router.go`; `actorSystem.configPID` in `actor/actor_system.go`). A reliable endpoint accepts no strategy other than long-lived (`spawnConfig.Validate` in `actor/spawn_option.go`). The PID also records whether its strategy is message-count based, in an atomic read on the hot path instead of a type switch (`withPassivationStrategy` in `actor/pid_option.go`).

## 10.2 The manager and its entries

There is one `passivationManager` per actor system, created with the system and started by `Start` (`NewActorSystem` in `actor/actor_system.go`). Every PID receives it at construction. It holds, under one mutex, a map of entries by actor ID and a min-heap of the time-based entries ordered by deadline (`actor/passivation_manager.go`). An entry carries the strategy's parameters, the deadline or the message baseline, four flags (`paused`, `pending`, `enqueued`, which keeps an entry from being queued on the trigger channel twice, and `deferred`) and a `triggers` count that a message-count attempt compares before and after it runs (`passivationEntry` in `actor/passivation_manager.go`).

**Registration.** `newPID` registers the actor after `init` and before `PostStart` is scheduled, unless the strategy is long-lived (`PID.startPassivation` in `actor/pid.go`); a restart registers it again. `Register` (`actor/passivation_manager.go`):

- does nothing when the manager is not started;
- for a time-based entry, sets the deadline to the latest activity plus the timeout and pushes it on the heap;
- for a message-count entry, sets the baseline to the processed count **plus one**, because `PostStart` has not run yet and is counted like any message, so the threshold is crossed after `n` user messages.

`Unregister` removes the entry; `Shutdown` and a successful passivation call it (`actor/passivation_manager.go`).

**Start and Stop.** `Start` creates fresh stop and done channels, so a manager stopped by `ActorSystem.Stop` starts again with the system (`actor/passivation_manager.go`). `Stop` closes the stop channel and waits for the loop to end; the loop reads the stop channel before every entry it fires, so the wait is bounded by the attempt in progress.

## 10.3 Activity

Inside a turn, `handleReceived` marks activity and counts the message **before** the handler runs (`actor/pid.go`). Control messages are dispatched elsewhere and are not activity (`PID.dispatchOne` in `actor/pid.go`). The time is read once per turn ([Chapter 7, §7.3](chap-07.md#73-one-turn)), so every message of a turn records the turn's start.

`markActivity` (`actor/pid.go`) stores the time in the PID and, at most once per 100 ms (`passivationTouchInterval` in `actor/pid.go`), tells the manager with `Touch`, which recomputes the entry's deadline and fixes the heap (`actor/passivation_manager.go`). The heap's deadline can therefore lag the actor's true latest activity by up to 100 ms. The manager knows that: when a deadline fires, it reads the actor's latest activity and, if the actor has not been idle for the whole timeout yet, re-arms the entry for the remaining time instead of attempting (`passivationManager.trigger` in `actor/passivation_manager.go`).

`recordProcessedMessage` increments the processed count and, for a message-count actor, calls `MessageProcessed` (`actor/pid.go`). `MessageProcessed` compares the count with the baseline plus the threshold; at or above it, it marks the entry pending and hands it to the manager through a 1,024-slot channel, with a goroutine as the fallback when the channel is full (`passivationManager.signalMessageEntry` in `actor/passivation_manager.go`). Since the count is raised before the handler runs, the first attempt usually finds the actor busy and is deferred, unless the turn has already ended by the time the manager goroutine reaches it; the turn's end raises the trigger again when the mailbox is empty, which retries a deferred attempt (`PID.finishOrReclaim` in `actor/pid.go`).

## 10.4 The loop

One goroutine runs `run` (`actor/passivation_manager.go`). Each iteration takes the earliest time-based entry, skipping paused ones (`passivationManager.nextEntry` in `actor/passivation_manager.go`). With no entry it waits for a heap change, a message-count trigger or the stop signal. With an entry whose deadline has passed it checks the stop signal and calls `trigger`; otherwise it waits for the deadline, a trigger, a heap change or the stop signal, with one reused timer.

`trigger` (`actor/passivation_manager.go`) runs one pass: it checks that the entry is still at the top and due, re-arms it if the actor's latest activity says it is not idle yet ([§10.3](#103-activity)), pops it, releases the mutex, and calls the actor's `tryPassivation`. Afterwards, under the mutex again: an entry removed or replaced meanwhile is left alone; a passivated one is deleted; a paused one stays out of the heap; a refused one is re-armed for its refreshed deadline, pushed to at least one timeout from now when the attempt was deferred or when nothing moved the deadline, so the loop never spins on an actor it cannot stop.

`processMessageEntry` is the message-count counterpart (`actor/passivation_manager.go`). After a deferred attempt it retries only if a trigger was raised during the attempt; otherwise it waits for the next one.

`Defer` records that an attempt found the actor busy (`actor/passivation_manager.go`). The two consumers read and clear it after the attempt.

## 10.5 One attempt

`tryPassivation` (`actor/pid.go`) runs on the manager's goroutine. In order:

1. Return `false` for a long-lived strategy, while the system is stopping, when the one-shot skip flag is set (consuming it), and when the actor is stopping, suspended or paused.
2. Take the actor's stop lock, which `Shutdown` also takes, and **check again that the actor is running and not stopping**: a `Shutdown` that took the lock first has already stopped it, and `PostStop` must not run twice.
3. Check the skip flag again: a reinstate that landed after step 1 cancels the attempt.
4. **Take the dispatch state from `Idle` to `Processing`** with `TakeIdleForStop` (`actor/dispatch_state.go`). This succeeds only when no turn is running or waiting, and while the attempt owns the state no worker can start one, so `PostStop` cannot overlap `Receive` ([Chapter 3, §3.5](chap-03.md#35-stop)). A busy actor refuses the attempt: `Defer` is recorded and the attempt returns `false`.
5. **Check for work.** A sender that enqueued a message just before step 4 and lost the race to schedule the actor has left it in the mailbox. If the mailbox, the system queue or the `PostStart` slot is not empty, the attempt hands the actor back, schedules that work, defers and returns `false` (`PID.hasRunnableWork` and `PID.scheduleWithheldWork` in `actor/pid.go`).
6. **Raise the passivating bit**, only now that the stop is certain. The bit makes `Tell` refuse the actor and `IsRunning` report false; raising it earlier would make a live, busy actor look dead to its senders, its parent and the actors it watches for the length of a refused attempt.
7. Unregister the entry and run `doStop` (`actor/pid.go`): cancel the requests in flight, release what the actor watches, stop its children, run `PostStop`, release the name, tell the watchers. The children are stopped with `Shutdown`, which does not wait for a child's running handler, so a child's `PostStop` can overlap its `Receive` even though the parent's cannot.
8. Publish `ActorPassivated` and count the `passivated` metric. A passivation publishes no `ActorStopped`.

A `PostStop` that fails still leaves the actor dead: `doStop` releases the name regardless. The attempt then returns `false` and publishes nothing.

## 10.6 Pause, resume, reinstate

`PausePassivation` and `ResumePassivation` are control messages, handled between two user messages (`PID.pausePassivation` and `PID.resumePassivation` in `actor/pid.go`). Pausing marks the entry paused and removes it from the heap (`passivationManager.Pause` in `actor/passivation_manager.go`); resuming puts a time-based entry back with a fresh deadline, or re-signals a pending message-count entry (`passivationManager.Resume` in `actor/passivation_manager.go`). A `ResumePassivation` sent to an actor that was never paused registers it again, which for a message-count actor restarts the count.

A suspension pauses passivation and a reinstate resumes it ([Chapter 9, §9.7](chap-09.md#97-reinstate)). A reinstate also sets the one-shot skip flag and marks activity, so an attempt that was already past its first check is cancelled and the actor is not stopped the moment it comes back.

## 10.7 System eviction

Eviction is a system-wide bound on the number of user actors, independent of per-actor strategies. `NewEvictionStrategy(limit, policy, percentage)` rejects a limit of zero and an unknown policy, and clamps the percentage to 0 to 100 (`actor/system_eviction.go`). `WithEvictionStrategy(strategy, interval)` installs it; a non-positive interval falls back to `DefaultEvictionInterval`, five seconds (`actor/option.go`).

`Start` runs `evictionLoop`, which calls `runEviction` on every tick until `Stop` closes its signal (`actorSystem.startEviction` in `actor/actor_system.go`). Each run:

1. Does nothing unless `NumActors`, the count of non-system actors, **exceeds** the limit.
2. Takes the candidates: every actor in the tree without a reserved name, children included (`actorSystem.localActors` in `actor/actor_system.go`).
3. Orders them by policy: LRU by ascending latest activity, LFU by ascending processed count, MRU by descending latest activity, with the other value as the tie-break (`actorSystem.getLRUActors`, `actorSystem.getLFUActors` and `actorSystem.getMRUActors` in `actor/actor_system.go`). Activity is compared to the second.
4. Evicts `max(excess, candidates × percentage / 100)` of them, at least one (`computeEvictionCount` in `actor/actor_system.go`). The percentage applies to all candidates, not to the excess.
5. Calls `Shutdown` on each. Eviction is a **stop**, not a passivation: it ignores the actor's strategy, so long-lived actors are evicted; it does not wait for an idle moment; it publishes `ActorStopped` and counts `stopped`. Stopping a parent stops its children, so more actors can disappear than the count says.

## Guarantees

| Statement | Enforced by |
|---|---|
| A time-based actor is passivated once idle, one `ActorPassivated` names it, and a later `Tell` returns `ErrDead`; a message-count actor is passivated after `n` messages; a long-lived one stays; passivation frees the name | `TestPassivation` in `actor/pid_test.go` |
| `PausePassivation` keeps an actor alive past its timeout or count; `ResumePassivation` lets a paused time-based actor passivate | `TestPassivation` in `actor/pid_test.go` |
| An actor whose `PostStop` fails during passivation is still dead | `TestPassivation` in `actor/pid_test.go` |
| Passivation leaves a busy actor alone and passivates it once idle, for both strategies; a refused attempt never makes the actor refuse messages | `TestPassivationSkipsBusyActor` in `actor/pid_test.go` |
| An attempt after a `Shutdown` runs `PostStop` no second time; a message accepted just before an attempt is handled | `TestPassivationRaces` in `actor/pid_test.go` |
| A reinstate that lands after the first skip check cancels the attempt | `TestReinstateAvoidsPassivationRace` in `actor/pid_test.go` |
| The manager fires an expired entry, and a stopped manager starts again | `TestPassivationManager_TimeBasedTrigger` and `TestPassivationManager_RestartAfterStop` in `actor/passivation_manager_test.go` |
| A deferred message-count attempt is retried only on a new trigger; a deferred time-based attempt a whole timeout later | `TestPassivationManager_Defer` in `actor/passivation_manager_test.go` |
| A stale deadline re-arms for the remaining idle time; a refused attempt is retried a timeout later; a paused entry stays out of the heap | `TestPassivationManager_TriggerPaths` in `actor/passivation_manager_test.go` |
| `Stop` returns while an expired entry is being refused, and the loop reads the stop signal before an expired entry | `TestPassivationManager_StopReturnsWhileRefusedEntryIsExpired` and `TestPassivationManager_RunObservesStopBeforeExpiredEntry` in `actor/passivation_manager_test.go` |
| `NewEvictionStrategy` rejects a zero limit and an unknown policy, and clamps the percentage; a non-positive interval falls back to the default | `TestEvictionStrategy` and `TestNewEvictionStrategy` in `actor/system_eviction_test.go`; `TestWithEvictionStrategy` in `actor/option_test.go` |
| Over the limit, LRU, LFU and MRU eviction bring the count down, long-lived actors included | `TestActorSystem` in `actor/actor_system_test.go` |
| A router is long-lived | `TestSpawnRouterIsLongLived` in `actor/spawn_test.go` |

## Implementation details (may change)

- The two-minute default, the 100 ms touch interval, the 1,024-slot trigger channel and the five-second eviction interval.
- One manager goroutine, which also runs the `PostStop` of every passivated actor.
- The `processedCount + 1` baseline.
- Second-granularity ordering for LRU and MRU.

## Behaviours to know

| Behaviour | Source |
|---|---|
| Every actor spawned without an option, children included, passivates after two idle minutes, and no system-wide option changes that | `NewActorSystem` in `actor/actor_system.go` |
| Control messages are not activity; all messages of a turn record the turn's start | `PID.handleReceived` in `actor/pid.go` |
| The message-count threshold is crossed as the `n`th message starts; the actor is passivated the first time it goes idle after that, so the messages already queued behind it are handled first | `PID.recordProcessedMessage` and `PID.finishOrReclaim` in `actor/pid.go` |
| A passivated actor's children are stopped at once, not between messages | `PID.freeChildren` in `actor/pid.go` |
| Passivation publishes `ActorPassivated`; eviction publishes `ActorStopped` | `PID.tryPassivation` in `actor/pid.go`; `actorSystem.runEviction` in `actor/actor_system.go` |
| A `PostStop` error during passivation leaves the actor dead with no lifecycle event | `PID.tryPassivation` in `actor/pid.go` |
| A slow `PostStop` delays every other passivation, and `ActorSystem.Stop` behind it | `passivationManager.Stop` in `actor/passivation_manager.go` |
| `ResumePassivation` without a prior pause restarts a message-count actor's count | `PID.resumePassivation` in `actor/pid.go` |
| A zero timeout or count passivates the actor right after it starts; neither is validated | `NewTimeBasedStrategy` in `passivation/strategy.go` |
| Eviction ignores strategies, counts children among its candidates, and applies its percentage to all candidates | `actorSystem.runEviction` and `computeEvictionCount` in `actor/actor_system.go` |
| `Stop` does not wait for an eviction round in progress | `actorSystem.evictionLoop` in `actor/actor_system.go` |
