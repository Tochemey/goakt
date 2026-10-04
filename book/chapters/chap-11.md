# 11. Scheduling, Routers, Event Stream, Pub/Sub

## Contents

- [What you will learn](#what-you-will-learn)
- [11.1 The scheduler](#111-the-scheduler)
  - [Registering](#registering)
  - [Firing](#firing)
  - [Cron in a cluster](#cron-in-a-cluster)
  - [Managing schedules](#managing-schedules)
- [11.2 Routers](#112-routers)
  - [Routing a message](#routing-a-message)
  - [When a routee fails or stops](#when-a-routee-fails-or-stops)
  - [Resizing](#resizing)
- [11.3 The event stream](#113-the-event-stream)
- [11.4 Publish and subscribe](#114-publish-and-subscribe)
- [Guarantees](#guarantees)
- [Implementation details (may change)](#implementation-details-may-change)
- [Behaviours to know](#behaviours-to-know)

## What you will learn

- How the scheduler wraps go-quartz, what it delivers on each tick, and what it keeps track of.
- How a cron schedule fires once per tick across a cluster.
- What a router is, how each routing strategy picks routees, and what happens when a routee fails or stops.
- How the event stream delivers system events, and what a subscriber must do to read them.
- How the topic actor implements publish and subscribe, locally and across nodes.

## 11.1 The scheduler

The scheduling methods of `ActorSystem` forward to a private `scheduler` (`actorSystem.Schedule` in `actor/actor_system.go`). Their `ctx` argument is not used.

**The engine** is a go-quartz `StdScheduler`, created with its logging off, job metadata on and an "outdated" threshold of 24 hours (`newScheduler` in `actor/scheduler.go`). The comment explains the threshold: with go-quartz's default of 100 ms, a one-shot that runs a little late is dropped.

**Lifecycle.** Each `ActorSystem.Start` builds a new scheduler (`actor/actor_system.go`) and starts it once the start chain has run, before the replicator, the passivation manager and eviction start (`actorSystem.startMessagesScheduler` in `actor/actor_system.go`); `shutdown` stops it before any actor. `Start` gives go-quartz a context of the scheduler's own, not the one passed to `ActorSystem.Start` (`actor/scheduler.go`): go-quartz stops itself when its context ends, and the application's start context, often a `WithTimeout`, must not take every schedule with it. `Stop` clears the queue, stops go-quartz and cancels that context. Then, with the scheduler's lock released, it waits for running jobs for at most the shutdown timeout (5 minutes by default), and empties the bookkeeping maps.

### Registering

Every registering method follows one pattern (`scheduler.ScheduleOnce` in `actor/scheduler.go`):

1. Under the scheduler's lock, fail with `ErrSchedulerNotStarted` unless started.
2. For a remote PID without remoting, fail with `ErrRemotingDisabled`. A grain identity is validated.
3. Take the reference from `WithReference`, or generate a UUID (`newScheduleConfig` in `actor/schedule_option.go`).
4. Build the job: a closure that sends the message.
5. Hand the job to go-quartz, and **record the reference and its target only once go-quartz has accepted it**. A reference still in use is rejected by go-quartz with `ErrJobAlreadyExists`, and the recorded target stays the original one.
6. Count the schedule.

| Method | go-quartz trigger | First fire | Source |
|---|---|---|---|
| `ScheduleOnce`, `ScheduleGrainOnce` | `RunOnceTrigger(delay)` | after `delay` | `scheduler.ScheduleOnce` in `actor/scheduler.go` |
| `Schedule`, `ScheduleGrain` | `SimpleTrigger(interval)` | after one `interval`, then every `interval` | `scheduler.Schedule` in `actor/scheduler.go` |
| `ScheduleWithCron`, `ScheduleGrainWithCron` | `CronTriggerWithLoc(expr, location)` | the next cron instant | `scheduler.ScheduleWithCron` in `actor/scheduler.go` |

Neither `delay` nor `interval` is validated (`scheduler.Schedule` in `actor/scheduler.go`).

### Firing

go-quartz runs one loop goroutine and, with no worker limit, which is how GoAkt configures it, **starts a new goroutine for every tick**. A recurring job is rescheduled before it runs, whatever happened on its previous run, so an error never removes it. A job panic is recovered, and a failed run is not retried.

The actor job (`scheduler.makeJobFn` in `actor/scheduler.go`) resolves its sender once, at registration: `WithSender`, or the system's `NoSender` actor, which is not `nil`. On each tick it runs the cluster claim, if any (below), then `sender.Tell(ctx, to, message)`. Three things follow from this closure:

- `Tell` to an actor that is not running fails with `ErrDead` before any dead letter is built ([Chapter 5](chap-05.md)), so a tick to a stopped, suspended or passivated actor is lost without trace. A passivated actor is not reactivated by its schedules; a grain is, because the grain job uses `TellGrain` (`scheduler.makeGrainJobFn` in `actor/scheduler.go`).
- The closure holds the **PID**, not the name. A recurring schedule whose target stopped keeps firing into it, and an actor later spawned under the same name does not receive it. Cancel the schedule yourself.
- The same message value is sent on every tick, and two ticks can be in flight at once, on two goroutines.

### Cron in a cluster

A cron schedule should fire once per tick across the cluster, even though every node registers it:

- Cluster mode is detected from the cluster engine rather than from `InCluster()`, because the engine is wired before the scheduler starts while the started flag is set after (`scheduler.ScheduleWithCron` in `actor/scheduler.go`).
- In a cluster the expression is evaluated in UTC, otherwise in local time (`scheduler.ScheduleWithCron` in `actor/scheduler.go`).
- `WithReference` is required in a cluster, so every node uses the same reference (`scheduler.ScheduleWithCron` in `actor/scheduler.go`).
- At each tick, `claimClusterFire` writes the key `<reference>@<fire time>` with put-if-absent and an expiry; only the node that wins delivers (`actor/scheduler.go`). The expiry is the gap between two fire times, clamped between one minute and 24 hours (`cronClaimTTL` in `actor/scheduler.go`). A tick older than that is skipped without claiming: go-quartz replays missed ticks, and a late node could otherwise win an expired claim and deliver a second time.

The claim is keyed by the reference alone. If each node registers the cron against its own local actor, one of those actors receives each tick, not each of them. Interval and one-shot schedules never claim: they fire on every node that registered them (`scheduler.ScheduleWithCron` in `actor/scheduler.go`).

### Managing schedules

`CancelSchedule`, `PauseSchedule` and `ResumeSchedule` forward to go-quartz by reference (`actor/scheduler.go`). `ListSchedules` walks the recorded references and keeps those go-quartz still knows.

A one-shot releases its reference when it fires, before the delivery (`scheduler.oneShotJobFn` and `scheduler.releaseSchedule` in `actor/scheduler.go`): cancelling, pausing or resuming it afterwards returns `ErrScheduledReferenceNotFound`, and the reference can be used again.

Resuming a paused **one-shot** needs care: go-quartz's `ResumeJob` asks the run-once trigger for its next fire time, which it reports as expired, and drops the job. `ResumeSchedule` therefore reschedules a paused one-shot itself, for its original fire time, or at once when that time has passed (`scheduler.ResumeSchedule` and `scheduler.resumeOneShot` in `actor/scheduler.go`).

## 11.2 Routers

**A router is an actor.** `SpawnRouter` spawns a top-level actor with relocation disabled, flagged as a system actor, and supervised to resume on any error (`actor/spawn.go`). Its defaults are fan-out routing, the stop directive for failing routees, and three restart attempts one second apart (`newRouter` in `actor/router.go`). `PreStart` validates the pool size and the settings of the chosen strategy.

**Routees are children.** On `PostStart` the router spawns its routees and switches to its `broadcast` behaviour (`router.postStart` in `actor/router.go`). Each routee is a **zero value** of the routee type, created with `reflect.New`, so any field set on the value passed to `SpawnRouter` is lost. It is named `<router>Routee<i>`, kept long-lived, and supervised to escalate every error to the router (`router.spawnRoutees` in `actor/router.go`). The router keeps its routees in a map keyed by ID, touched only on its own turn.

**What a router accepts** (`router.Receive` and `router.broadcast` in `actor/router.go`): `Broadcast` wrapping the real message, `PanicSignal` from a routee, `GetRoutees` and `AdjustRouterPoolSize`. Everything else is unhandled and becomes a dead letter.

### Routing a message

`handleBroadcast` collects the routees with `availableRoutees` and hands the message to the strategy (`router.handleBroadcast` and `router.dispatchToRoutees` in `actor/router.go`). With none, the message is unhandled and the router shuts itself down (`router.handleNoRoutees` in `actor/router.go`). `availableRoutees` decides a lot (`router.availableRoutees` in `actor/router.go`):

The router keeps its routees in a slice, in spawn order, next to the map (`actor/router.go`). `availableRoutees` walks the slice, drops every routee that is no longer running, and returns the rest in that stable order. A routee that stopped by itself is therefore never chosen, and a message that finds no live routee becomes a dead letter and shuts the router down.

| Strategy | How it picks | Sender the routee sees | Source |
|---|---|---|---|
| Round robin | a counter indexes the stable slice, so the routees take turns in spawn order | router | `router.routeByStrategy` in `actor/router.go` |
| Random | `rand.IntN` | router | `router.routeByStrategy` in `actor/router.go` |
| Consistent hash | the key from the extractor, looked up on a hash ring of 150 points per routee by default (`WithConsistentHashVirtualNodes`); an empty key, or a routee that is missing or not running, falls back to random | router | `router.routeByConsistentHash` and `consistentHashRing` in `actor/router.go` |
| Fan-out (default) | every routee, told from the router's turn, so each routee receives the broadcasts in order | router | `router.routeByStrategy` in `actor/router.go` |
| Scatter-gather first | asks every routee; replies with the first success, or a `StatusFailure` after `within` | `NoSender` | `gather.scatterGatherFirst` in `actor/router.go` |
| Tail-chopping | asks one routee, then another every `interval`, until a success or `within` | `NoSender` | `gather.tailChopping` in `actor/router.go` |

Two consequences for routee code:

- **The original sender is never visible.** A routee that answers `ctx.Sender()` answers the router, which treats the reply as unhandled. The two asking strategies send the reply to the original sender themselves, as a `Tell` from the router (`gather.reply` in `actor/router.go`); an `Ask` sent to the router gets no reply.
- **The asking strategies do not hold the router.** A `gather` captures the sender, the payload and a context from the turn, and runs the asks and the wait on its own goroutine, bounded by `within`; the router's `Receive` returns at once and keeps routing (`router.newGather` in `actor/router.go`).

### When a routee fails or stops

A routee's error escalates: the routee is suspended and the router receives a `PanicSignal` ([Chapter 9, §9.5](chap-09.md#95-escalation-and-the-guardians)). `handlePanicSignal` applies the router's directive (`actor/router.go`):

- **stop** (the default): stop the routee, remove it from the map, rebuild the hash ring;
- **restart**: `Restart` the routee, trying up to the configured number of attempts with an exponential backoff whose interval starts at, and is capped by, the configured delay, each wait randomised by up to half of it (`Retrier.RunContext` in `internal/retry/retry.go`), all on the router's turn; with zero attempts or no delay it tries once; if that fails, stop it;
- **resume**: reinstate it and put it back into the pool.

The router does not watch its routees. A routee that stops by itself is noticed only when `availableRoutees` next runs.

### Resizing

`AdjustRouterPoolSize` adds or removes routees (`router.handleAdjustRouterPoolSize` in `actor/router.go`). New routees are named from a counter that only grows, so a name never collides with a live routee's after a removal, and the pool grows by the requested number. A negative delta stops the oldest routees. Shrinking to zero is allowed; the next `Broadcast` then shuts the router down.

## 11.3 The event stream

One `EventsStream` per actor system is created by `NewActorSystem` (`actor/actor_system.go`) and reused across a `Stop` and `Start`. The system publishes on a single topic, `topic.events` (`eventsTopic` in `actor/reserved.go`).

**Publishing is synchronous.** `Publish` takes a read lock, copies the topic's subscribers, and signals each active one on the publisher's goroutine (`EventsStream.publishToTopic` in `eventstream/eventstream.go`). Publishers include callers spawning or stopping actors, actor turns, the dead-letter actor, the cluster event loop and the supervision goroutine, which publishes `ActorSuspended`.

**Each subscriber has an unbounded queue** (`subscriber.signal` in `eventstream/subscriber.go`). A publisher never blocks and never drops, so a subscriber that does not read grows without bound. The queue is a lock-free linked list (`Queue.Enqueue` and `Queue.Dequeue` in `internal/queue/queue.go`); a dequeued node is left to the garbage collector rather than reused, because a producer may still hold it as the tail.

**Reading.** `Iterator()` reads the current length, makes a channel of that size, moves that many messages into it, closes it and returns it (`eventstream/subscriber.go`). It is a snapshot: a consumer must call it again to see later events.

**What the system publishes:**

| Event | Published from | Source |
|---|---|---|
| `ActorStarted` | the end of `init` | `PID.init` in `actor/pid.go` |
| `ActorStopped` | `Shutdown`, after `doStop` | `PID.stop` in `actor/pid.go` |
| `ActorPassivated` | passivation | `PID.tryPassivation` in `actor/pid.go` |
| `ActorChildCreated` | the parent, after spawning a child | `PID.spawnChildLocal` in `actor/pid.go` |
| `ActorRestarted` | the end of a restart | `restartSubtree` in `actor/pid.go` |
| `ActorSuspended`, `ActorReinstated` | `suspend`, `doReinstate` | `PID.suspend` and `PID.doReinstate` in `actor/pid.go` |
| `Deadletter` | the dead-letter actor | `deadLetter.handleDeadletter` in `actor/dead_letter.go` |
| `NodeJoined`, `NodeLeft`, `LeaderChanged` | the cluster event handler | `actorSystem.handleClusterEvent` in `actor/actor_system.go` |
| `RelocationStarted`, `RelocationFailed` | relocation of a departed peer's actors and grains | `actorSystem.publishRelocationStarted` in `actor/actor_system.go`, `relocationWorker.relocate` in `actor/relocation_worker.go` and `publishRelocationFailed` in `actor/relocator.go` |
| `ReliableDeliveryFailed` | a reliable-delivery controller that gives up | `producerController.publishFailure` in `actor/reliable_delivery_producer_controller.go`, `consumerController.fail` in `actor/reliable_delivery_consumer_controller.go` and `workPullingProducerController.publishFailure` in `actor/reliable_delivery_work_pulling_controller.go` |

## 11.4 Publish and subscribe

**The topic actor** is a system actor named `GoAktTopicActor`, spawned at `Start` when the system runs in a cluster or has `WithPubSub()` (`actorSystem.spawnTopicActor` in `actor/topic_actor.go`). It keeps, per topic, a map of subscribers, and a time-limited set of the publications it has seen, retained two minutes by default (`topicActor` in `actor/topic_actor.go`).

**Subscribe** adds the sender to the topic, watches it, and acknowledges (`topicActor.handleSubscribe` in `actor/topic_actor.go`). **Unsubscribe** removes it and acknowledges, even for a topic it was not subscribed to (`topicActor.handleUnsubscribe` in `actor/topic_actor.go`). When a subscriber terminates it is removed from every topic (`topicActor.handleTerminated` in `actor/topic_actor.go`). A topic exists only while it has subscribers: the last one leaving removes it (`topicActor.forgetTopicIfEmpty` in `actor/topic_actor.go`).

**Publish** (`topicActor.handlePublish` in `actor/topic_actor.go`):

1. Drop the publication if the same sender already published the same ID on the same topic within the retention window; otherwise record it.
2. Deliver locally: one goroutine per live local subscriber, each a `Tell` **from the topic actor**, so subscribers see the topic actor as sender, not the publisher (`topicActor.sendToLocalSubscribers` in `actor/topic_actor.go`).
3. In a cluster, send the publication to **every** peer's topic actor, each from a goroutine doing a `RemoteLookup` and a `RemoteTell`, whether or not that peer has subscribers (`topicActor.sendToRemoteTopicActors` in `actor/topic_actor.go`).
4. Wait for all of these before the turn ends. Every enqueue of one publication therefore happens before the next publication starts, which keeps per-subscriber order; and one slow peer slows every publication on the node.

A peer's topic actor delivers a `TopicMessage` to its local subscribers the same way, and records it in its seen-set, so a message redelivered by the same peer is dropped (`topicActor.handleTopicMessage` in `actor/topic_actor.go`).

**`TopicStats`** asks the local topic actor, which counts its live subscribers and, in a cluster, asks every peer's topic actor for its count. That fan-out runs on a goroutine that replies to the asker when done, so the topic actor's turn ends at once and it can answer the peers' own requests meanwhile (`topicActor.handleGetTopicStats` in `actor/topic_actor.go`). An error from any peer means no reply, so the caller sees its own timeout.

## Guarantees

| Statement | Enforced by |
|---|---|
| A one-shot is delivered once and leaves go-quartz; twenty in a row are all delivered | `TestScheduler` in `actor/scheduler_test.go` |
| Scheduling to a stopped actor is accepted and delivers nothing; scheduling on a stopped scheduler fails | `TestScheduler` in `actor/scheduler_test.go` |
| An interval schedule first fires one interval after registration and stays registered, even when its target is stopped | `TestScheduler` in `actor/scheduler_test.go` |
| A remote PID without remoting is rejected | `TestScheduler` in `actor/scheduler_test.go` |
| Pause and resume work for an interval schedule; cancel stops deliveries; an unknown reference gives `ErrScheduledReferenceNotFound` | `TestScheduler` in `actor/scheduler_test.go` |
| A cluster cron needs `WithReference`, and is evaluated in UTC; outside a cluster it uses local time | `TestScheduler` and `TestScheduleWithCronTimezone` in `actor/scheduler_test.go` |
| The claim expiry is the cron period clamped to one minute and 24 hours; a stale tick or a claimed tick is skipped | `TestCronClaimTTL` and `TestClaimClusterFire` in `actor/scheduler_test.go` |
| Three nodes running one cron deliver at most once per tick; interval schedules stay on their node | `TestGrainSchedulerMultiNode` in `actor/scheduler_test.go` |
| `ListSchedules` lists a schedule until it is cancelled or, for a one-shot, until it fires | `TestSchedulerListSchedules` in `actor/scheduler_test.go` |
| A live reference cannot be reused | `TestGrainScheduler` in `actor/scheduler_test.go` |
| Fan-out reaches every routee; scatter-gather and tail-chopping relay the first success, or one `StatusFailure` | `TestRouter` in `actor/router_test.go` |
| With no routee left, the next broadcast becomes a dead letter and the router stops | `TestRouter` in `actor/router_test.go` |
| Invalid pool size or strategy settings fail `SpawnRouter` | `TestRouter` in `actor/router_test.go` |
| The restart, resume and stop directives apply to a failing routee | `TestRouter` in `actor/router_test.go` |
| The pool can be resized | `TestRouter` in `actor/router_test.go` |
| Consistent hashing sends one key to one routee, and the ring remaps only a fraction of keys when members change | `TestRouter` and `TestConsistentHashRing` in `actor/router_test.go` |
| Publish reaches every active subscriber; inactive ones receive nothing; `Iterator` drains what is buffered | `TestStream` in `eventstream/eventstream_test.go` |
| Subscribe and unsubscribe are acknowledged; a publish reaches subscribers on all three nodes; a repeated ID is not delivered again within the retention window | `TestTopicActor` in `actor/topic_actor_test.go` |
| A terminated subscriber is removed | `TestTopicActor` in `actor/topic_actor_test.go` |
| `TopicStats` counts live local subscribers and agrees across nodes; the peer query behind it fails when a peer is unreachable | `TestTopicActor` in `actor/topic_actor_test.go` |
| The scheduler survives a cancelled start context; a fired one-shot releases its reference; a rejected duplicate keeps the listed target; a paused one-shot resumes | `TestSchedulerSurvivesCanceledStartContext`, `TestSchedulerFiredOneShotReleasesReference`, `TestSchedulerDuplicateReferenceKeepsTarget` and `TestSchedulerPauseResumeOneShot` in `actor/scheduler_test.go` |
| Round robin rotates in order; fan-out keeps per-routee order; a stopped routee is dropped before routing; a resumed routee rejoins the pool; scale-up after a removal grows the pool; the asking strategies do not block the router | `TestRouterRoundRobinRotatesInOrder`, `TestRouterFanOutPreservesPerRouteeOrder`, `TestRouterDropsStoppedRouteeBeforeRouting`, `TestRouterResumeReinsertsRoutee`, `TestRouterScaleUpAfterRemovalGrowsPool`, `TestRouterScatterGatherDoesNotBlockRouter` and `TestRouterTailChoppingDoesNotBlockRouter` in `actor/router_test.go` |
| Many producers deliver every value once through the subscriber queue | `TestQueueConcurrentProducersDeliverEveryValueOnce` in `internal/queue/queue_test.go` |
| Concurrent `TopicStats` across the cluster answer; an empty topic is forgotten | `TestTopicActor` in `actor/topic_actor_test.go` |

## Implementation details (may change)

- go-quartz v0.15.2, with a 24-hour outdated threshold and one goroutine per tick, on a context of the scheduler's own.
- UUID references by default; the claim key `<reference>@<fire time>`.
- Router defaults (fan-out, stop, three attempts one second apart), 150 virtual nodes per routee, xxh3 hashing.
- The event topic `topic.events`.
- The topic actor's name and its two-minute retention.

## Behaviours to know

| Behaviour | Source |
|---|---|
| The scheduler is not bound to the context passed to `ActorSystem.Start` | `scheduler.Start` in `actor/scheduler.go` |
| Without `WithSender`, the sender is the `NoSender` actor, not `nil` | `scheduler.makeJobFn` in `actor/scheduler.go` |
| A tick to an actor that is not running is lost without a dead letter; a recurring schedule outlives its target | `scheduler.makeJobFn` in `actor/scheduler.go` |
| A fired one-shot releases its reference, which can then be used again | `scheduler.oneShotJobFn` in `actor/scheduler.go` |
| A paused one-shot resumes for its original fire time, at once if that time has passed | `scheduler.resumeOneShot` in `actor/scheduler.go` |
| A cluster cron delivers each tick to one node's target | `scheduler.claimClusterFire` in `actor/scheduler.go` |
| Routees start from the zero value of their type | `router.spawnRoutees` in `actor/router.go` |
| Round robin rotates in spawn order; fan-out keeps each routee's order | `router.routeByStrategy` in `actor/router.go` |
| A routee that stopped by itself is dropped before the next message is routed | `router.availableRoutees` in `actor/router.go` |
| Scatter-gather and tail-chopping run off the router's turn | `router.newGather` in `actor/router.go` |
| An event-stream subscriber that does not read grows without bound; `Iterator` is a snapshot | `subscriber.Iterator` and `subscriber.signal` in `eventstream/subscriber.go` |
| Pub/sub subscribers see the topic actor as sender | `topicActor.sendToLocalSubscribers` in `actor/topic_actor.go` |
| Every publication goes to every peer, and a slow peer slows every publication | `topicActor.sendToRemoteTopicActors` in `actor/topic_actor.go` |
| A topic exists only while it has subscribers | `topicActor.forgetTopicIfEmpty` in `actor/topic_actor.go` |
| `TopicStats` runs its peer fan-out off the topic actor's turn | `topicActor.handleGetTopicStats` in `actor/topic_actor.go` |
