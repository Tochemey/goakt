# Changelog

## Unreleased

### 🔧 Fixes

- **Concurrent same-name spawns no longer create duplicate actors** ([#1277](https://github.com/Tochemey/goakt/pull/1277)). Goroutines racing `Spawn`, `SpawnNamedFromFunc`, `SpawnChild`, or the local `SpawnSingleton` path with the same name could each create a live actor instance: the tree-insertion conflict was swallowed, so the losing caller received an unsupervised orphan whose later shutdown detached the canonical actor from the tree (both instances share the same ID). Local spawns of one identity are now serialized through a singleflight group keyed by the actor address, so a name yields exactly one PID under concurrency; racing callers coalesce onto the in-flight spawn and share its result. The wait is context-aware — a caller whose context expires stops waiting with its own context error — and a waiter that inherits a cancellation caused by the winning caller's context while its own is still live retries once. As a safety net, `completeSpawn` now detects a duplicate tree insertion, returns the canonical instance, undoes the actors-counter bump, and logs a warning instead of handing back an orphan.
- **Singleton idempotent success resolves the PID from the confirmed registry address** ([#1277](https://github.com/Tochemey/goakt/pull/1277)). When `SpawnSingleton` found the name already bound to the singleton it intended to create, it re-resolved the PID with a second registry lookup that could momentarily miss the record and surface `ErrActorNotFound` from a creation call. The PID is now resolved from the address read while confirming the conflict (preferring a live local instance when the singleton is hosted on the calling node), and the idempotent path never surfaces `ErrActorNotFound`.
- **Leader-delegated singleton spawns stay retryable during membership churn** ([#1277](https://github.com/Tochemey/goakt/pull/1277)). A spawn delegated to the cluster leader that failed because no leader was elected yet (`ErrLeaderNotFound`), the cluster engine was not running, or no member carried the requested role was serialized back as `CODE_INTERNAL_ERROR`, which the delegating node treats as terminal. These transient membership conditions now map to `CODE_UNAVAILABLE`, which the delegating node's retry loop recognizes as retryable within its configured budget — so a `SpawnSingleton` issued during a rolling restart retries until the cluster settles instead of failing permanently.

## v4.4.1 - 2026-07-20

### ✨ Features

- **Exponential backoff supervision** ([#1268](https://github.com/Tochemey/goakt/issues/1268)). A supervised child that crashed right after every successful restart was restarted immediately, forever: nothing counted consecutive faults, so a crash loop hammered whatever dependency caused the failure in the first place. The supervisor now tracks consecutive faults per child and drives two safeguards with that counter. `WithRetry(maxRetries, timeout)` finally enforces the restart budget its documentation always described: more than `maxRetries` consecutive faults within the window suspends the child instead of restarting it (a suspended actor can be revived with `Reinstate`). The new `supervisor.WithExponentialBackoff(initialDelay, maxDelay, resetAfter)` option delays the nth consecutive restart by `min(initialDelay << (n-1), maxDelay)`, giving the failing dependency time to recover; the counter resets once the child stays fault-free for `resetAfter` (defaults to `maxDelay` when zero). With `OneForAllStrategy` the delay and the budget apply to the whole group. Restart delays never block the parent, which keeps processing messages while the child waits out its backoff. Behavior is unchanged when neither option is configured.

### 🔧 Fixes

- **Cluster singleton uniqueness is now keyed by actor name instead of actor kind** ([#1272](https://github.com/Tochemey/goakt/issues/1272)). Spawning a second singleton of the same kind under a different name failed with `ErrSingletonAlreadyExists`, and because that error is also the normal outcome when several nodes race to spawn the same singleton, callers treated it as success: the second singleton silently never started. Actor names are the cluster-wide identity everywhere else in the cluster model, so the kind-based reservation is gone entirely; singleton uniqueness now rides on the same name registry every actor uses, with placement unchanged (the coordinator, or the oldest member advertising the role when `WithSingletonRole` is set; the role remains a placement filter and is no longer part of the identity). Two singletons of the same kind under distinct names both run. Removing the reservation also removes its failure mode: a crashed node can no longer leave behind a stale kind reservation that blocked the singleton cluster-wide until relocation cleaned it up. `ErrSingletonAlreadyExists` is deprecated; it is only surfaced when a spawn is handled by a node running an older GoAkt version during a rolling upgrade.
- **`SpawnSingleton` now returns the existing singleton's PID on idempotent success**. When the requested name was already bound to the intended singleton (concurrent spawns, propagation races), the call succeeded but returned a nil PID, which also broke delegated spawns through `RemoteSpawn`. The existing singleton is now resolved and returned.
- **Remote "already exists" errors are no longer misclassified for actor names containing "singleton"**. The client mapped any `CODE_ALREADY_EXISTS` message containing the substring "singleton" to `ErrSingletonAlreadyExists`, so a plain name collision on an actor named e.g. `singleton-a` surfaced as a terminal singleton conflict. The mapping now matches the full sentinel messages.
- **Stopping a running actor no longer wipes the user-supplied supervisor** ([#1269](https://github.com/Tochemey/goakt/issues/1269)). The stop path called `Supervisor.Reset()` on the very object passed to `WithSupervisor`, clearing every directive rule and flipping the strategy to one-for-all. Two actors sharing one supervisor instance lost their supervision after the first of them stopped while running (including the sibling shutdown inside a one-for-all restart), and a restarted-while-running actor came back with an empty rule set; the next fault was then suspended silently instead of handled by the configured directive. The runtime no longer mutates the user's supervisor object.
- **One-for-all restarts with `WithRetry` now restart each group member** ([#1268](https://github.com/Tochemey/goakt/issues/1268)). The retry path restarted the faulty child once per group member concurrently and never restarted the siblings, so the group came back with the faulty child racing itself and every sibling still carrying its old state. Each member now restarts itself, matching the non-retry path.

### ⚡ Performance

- **Grain activation no longer writes the grain kind to the cluster store** ([#1273](https://github.com/Tochemey/goakt/issues/1273)). The kinds namespace was write-only: every activation paid a second quorum write for a record no code path reads (the `GetKinds` remoting call serves each node's locally registered types, and the former singleton-reservation readers were removed by [#1272](https://github.com/Tochemey/goakt/issues/1272)). Activation now performs a single registry write (`PutGrain`), and a kind-write failure can no longer fail an otherwise successful activation. The kinds namespace and the internal `PutKind` cluster API are removed; leftover kind records from older nodes are ignored and vanish once the cluster fully restarts.

### ⚠️ Behavior changes

- **Singleton identity changed from per-kind to per-name** ([#1272](https://github.com/Tochemey/goakt/issues/1272)). `SpawnSingleton` documented "only one instance at a given time of its kind"; the contract is now one instance per name. Respawning an existing singleton under the same name returns its PID instead of `ErrSingletonAlreadyExists`, and a name held by a different actor returns `ErrActorAlreadyExists`. Deployment note for rolling upgrades: old nodes still reserve the singleton kind and new nodes never release those reservations, so a singleton stopped on a new-version node can leave a reservation that blocks old-version nodes from respawning that kind. Complete the rollout before relying on multiple same-kind singletons.
- **`WithRetry` now suspends a crash-looping child once the budget is exhausted.** Previously a child that kept crashing after each successful restart was restarted forever regardless of `maxRetries`. Applications that configured `WithRetry` and relied on unlimited restarts must either raise `maxRetries`, add `WithExponentialBackoff`, or watch for `ActorSuspended` events and call `Reinstate`. Configurations without `WithRetry` are unaffected.

## v4.4.0 - 2026-07-16

### 🔧 Fixes

- **Cluster bootstrap survives transient slowness instead of crashing the node** ([#1257](https://github.com/Tochemey/goakt/issues/1257)). `cluster.Start` was a single all-or-nothing attempt: a node joining a data-bearing cluster (partitions redistributing during a rolling update) could exceed the bootstrap timeout, fail, and take the process down, leaving Kubernetes CrashLoopBackOff as the de facto retry loop. Bootstrap is now retried a bounded number of times (3 attempts, linear backoff) with a full engine teardown between attempts; Olric's own join retries only ever covered the memberlist join, not the initial sync and dmap creation that follow it. `DefaultClusterBootstrapTimeout` is unchanged.
- **The initial-sync failure path no longer leaks a running engine**. When `WaitForInitialSync` timed out, the already-started Olric server (port bound, memberlist joined) was left running, asymmetrically with the `server.Start` and `createDMap` failure paths which both shut it down. It is now torn down like the others, which is also what makes the bootstrap retry above safe.
- **Actor and grain creation now waits for cluster registry publication** ([#1263](https://github.com/Tochemey/goakt/issues/1263)). `SpawnOn` (and every other creation path) returned before the actor's registry record reached the cluster store, so an immediate name-based lookup or send from another node could fail with `actor not found` even though the actor was alive. Creation calls (`Spawn`, `SpawnOn`, `SpawnSingleton`, `SpawnChild`, restarts, and grain activation) now write the registry record synchronously and only succeed once it is durably stored: a successful spawn means the actor is immediately resolvable by name and reachable from any node, with no propagation window to retry around. Retiring the background replication queues also closes two races they created: a killed-right-after-spawn actor could be re-registered in the cluster by the late background write, and a retried `SpawnOn` could double-spawn a name on another node during the visibility window.

### ⚡ Performance

- **Relocation on node departure scales better with grain and actor count** ([#1255](https://github.com/Tochemey/goakt/issues/1255)).
  - **Grains now relocate lazily by default.** Previously every grain of a departed node was eagerly reactivated up front (an O(all grains) `OnActivate` storm for entities that may never be called again). Now, by default, only the grain's directory entry is cleaned and the grain re-activates on a surviving node the next time it is addressed (`TellGrain`/`AskGrain`). The directory cleanup itself is distributed across the surviving nodes through the batched relocation requests instead of being issued entirely by the node coordinating the rebalance.
  - **Topology events no longer trigger a full registry re-put on every join.** A joining node no longer causes every node to re-put all of its local actors and grains into the cluster store; Olric migrates partition data to the new owner during rebalancing.
  - **The repair-on-departure re-put now runs only without replicas.** The full re-put of every local actor and grain on `NodeLeft` exists solely to restore registry entries whose sole copy was lost with the departed node, which can only happen at a replica count of 1. With a replica count greater than 1 Olric already holds and promotes backups for those partitions, so the repair is skipped as redundant write amplification instead of running on every node on every departure.
  - **Relocation placement is now load-aware** ([#1255](https://github.com/Tochemey/goakt/issues/1255)). Before distributing a departed node's actors, the leader seeds each target's load with the number of actors it already hosts (read once from the replicated registry), so actors flow to the nodes that are least loaded overall instead of being split evenly among the survivors. This stops an already-busy survivor (typically the leader, which also takes singletons) from being over-assigned. The scan is best-effort and falls back to an even split if it fails.

### ✨ Features

- **`WithGrainEagerRelocation` opts a grain back into upfront relocation** ([#1255](https://github.com/Tochemey/goakt/issues/1255)). Grains that must stay warm without an external trigger (timers, streams, background work) can be reactivated immediately on a surviving node when their host departs, restoring the pre-lazy behavior per grain. It is mutually exclusive with `WithGrainDisableRelocation` (`ErrGrainRelocationConflict`).
- **Relocation now recovers actors and grains after a node crash** ([#1255](https://github.com/Tochemey/goakt/issues/1255)). Previously relocation only ran when a node shut down gracefully (it depended on the `PeerState` snapshot that node replicated before leaving); an abrupt departure (`kill -9`, OOM, partition) lost its actors and grains. When no snapshot exists, the leader now reconstructs the departed node's relocation set from the replicated cluster registry, so its relocatable actors and grains are recreated on surviving nodes. Each node keeps a small peers-address → remoting-port cache (seeded at startup, refreshed on `NodeJoined`) to identify a crashed node's registry records, since the `NodeLeft` event only carries the peers address. Crash recovery relies on a cluster replica count greater than 1.
- **Role-aware relocation placement** ([#1255](https://github.com/Tochemey/goakt/issues/1255)). Actors spawned with `WithRole` are now relocated only onto nodes advertising that role, matching initial placement. Each actor is assigned to the least-loaded eligible node across the leader and surviving peers. When no surviving node advertises a required role, the actor is reported in the `RelocationFailed` event instead of being silently dropped.
- **Relocation metrics** ([#1255](https://github.com/Tochemey/goakt/issues/1255)). When OpenTelemetry metrics are enabled (`WithMetrics`), the leader records the outcome of each departed node's relocation: `actorsystem.relocation.duration` (histogram, ms), `actorsystem.relocation.relocated.count`, and `actorsystem.relocation.failed.count`, each tagged with `actor.system` and the departed node's address. Recording is a no-op when metrics are disabled.
- **Message handoff buffering during relocation** ([#1255](https://github.com/Tochemey/goakt/issues/1255)). Synchronous name-based sends now mask the brief window in which a departed node's actor is being recreated on a survivor. When `SendSync` (or its `ReceiveContext` wrapper) routes to an actor whose host just left the cluster, the send is buffered (re-resolved and retried with bounded backoff, never beyond the caller's own timeout) instead of immediately returning a connection error or a spurious `ErrActorNotFound`, and is delivered once the actor re-registers elsewhere. `SendAsync` stays non-blocking: a target mid-relocation fails fast with the new retryable `ErrRelocationInProgress` instead of being buffered or dialing the departed host. Each node opens a short, self-expiring handoff window on the departure it observes (closed early if the node rejoins at the same address), so steady-state sends resolve and deliver exactly once with no added latency; a send that outlasts the window returns a retryable error (`ErrActorNotFound` or `ErrRelocationInProgress`). A `actorsystem.relocation.buffered.count` metric (tagged with `actor.system`) reports how often synchronous sends hit the window. Masking applies to identity-based routing; a send on a stale, pre-departure remote `*PID` still bypasses re-resolution.
- **`WithAskTimeout` configures the system-wide Ask timeout.** `NewActorSystem` now accepts `WithAskTimeout(d)`, which sets the timeout applied to the actor system's internal request/response round-trips (querying the deadletter actor for its count, cross-peer topic-stats queries) and the default deadline the remote Ask server uses for requests that carry no timeout of their own. These sites previously pinned the hardcoded `DefaultAskTimeout` (5s), which stays the default when the option is not set. A non-positive value is ignored. The per-call `Ask(ctx, to, msg, timeout)` API is unchanged, since its timeout is supplied by the caller.

### ⚠️ Behavior changes

- **Grains relocate lazily by default.** Applications that relied on a departed node's grains being reactivated up front (without being re-addressed) must now opt in with `WithGrainEagerRelocation`. Grains addressed after a node loss reactivate transparently as before.
- **Creation calls surface registry errors** ([#1263](https://github.com/Tochemey/goakt/issues/1263)). `Spawn`, `SpawnChild`, `SpawnSingleton`, restarts, and grain activation can now fail with registry or quorum errors that previously only surfaced as background warning logs. On such a failure the creation is rolled back so nothing is left behind: the actor is stopped, a singleton's reserved kind is released, and a grain is deactivated with its ownership claim released. Spawning while the system is shutting down now fails with the underlying cluster engine error instead of silently skipping publication.
- **`ActorChildCreated` is published only after successful cluster publication** ([#1263](https://github.com/Tochemey/goakt/issues/1263)), so the event now means the child is durably registered, not merely created locally.
- **Actor spawn no longer performs a post-init readiness probe.** `Spawn` and restart previously issued an internal `HealthCheckRequest` round-trip after `PreStart` and failed with `ErrRequestTimeout` if it did not complete. That probe never exercised user `Receive` code, only framework dispatch plumbing that is already guaranteed once `PreStart` returns and the actor is scheduled on the shared dispatcher, so it was removed. Spawn now returns as soon as `PreStart` succeeds; assert actor readiness in `PreStart`, which still fails the spawn on error.

## v4.3.1 - 2026-07-10

### 🔧 Fixes

- **Remoting no longer inherits cancelation from the `Start` context** ([#1252](https://github.com/Tochemey/goakt/issues/1252)). The remoting server stored the startup context as its long-lived base context, so a bounded `Start(ctx)` (a DI OnStart hook, a startup timeout) expiring after startup poisoned every inbound handler context: all remote operations against the node failed with `context deadline exceeded` until the process restarted. The server base context is now detached via `context.WithoutCancel`, preserving context values while leaving the server's lifetime to `Stop()`.

## v4.3.0 - 2026-07-09

### ✨ Features

- **Topic subscription introspection with `TopicStats`** ([#1238](https://github.com/Tochemey/goakt/issues/1238)). `ActorSystem.TopicStats(ctx, topic, timeout) (*TopicStats, error)` returns a read-only, point-in-time snapshot of a topic's subscription state without ever exposing subscriber identities, so presence-style features ("how many clients follow this feed", "does any node have subscribers for this topic") no longer require a shadow registry alongside the built-in pub/sub.
  - `TopicStats` reports `LocalSubscriberCount` (subscribers registered on this node's topic actor) and `TopicInstanceCount` (topic-actor instances, i.e. nodes, cluster-wide with at least one subscriber for the topic).
  - In cluster mode the local topic actor answers with its own count and fans a lightweight query out to every peer's topic actor; peers answer with their local view only and never fan the query out any further, which avoids query storms. Outside cluster mode `TopicInstanceCount` is simply 0 or 1 depending on whether this node has local subscribers.
  - The count reflects only live subscribers, applying the same liveness filter the publish path uses, so it matches what a publish would actually deliver to.
  - `TopicStats` prefers an explicit failure over an inconsistent count: if any peer cannot be reached within `timeout`, the whole call returns an error rather than a silently undercounted result.
  - It requires the topic actor to be running (`WithPubSub()` or clustering enabled), otherwise it returns the new `ErrTopicActorNotStarted`. Publish, subscribe, and delivery semantics are unchanged.
- **Cluster-wide single fire for cron schedules** ([#1236](https://github.com/Tochemey/goakt/issues/1236)). In cluster mode, `ScheduleWithCron` now delivers exactly once per trigger tick across the cluster.
  - Every node keeps running its own cron trigger locally, so ticks stay in sync with no extra network round trips on the hot path.
  - Immediately before delivery each node races to claim an exclusive, TTL-bounded slot for that specific tick in the cluster store, the same atomic put-if-absent primitive that guarantees single grain activation. Only the claim winner delivers; every other node silently skips that tick.
  - There is no fixed leader to fail over: if the winning node leaves the cluster, arbitration for the next tick proceeds among the remaining nodes.
  - The cron expression is evaluated in UTC in cluster mode so every node computes the same tick instants regardless of its local timezone (outside cluster mode it stays local-timezone, as before).
  - The claim TTL scales with the cron period, clamped between one minute and 24 hours, and a node that reaches a tick more than the TTL late skips it rather than re-delivering an already-delivered tick.
  - `Schedule` (interval) and `ScheduleOnce` remain node-local, in cluster mode or not: their fire times are anchored to each node's own registration clock, so there is no shared tick to arbitrate. Outside cluster mode nothing changes.
- **Scheduler introspection with `ListSchedules`** ([#1235](https://github.com/Tochemey/goakt/issues/1235)). `ActorSystem.ListSchedules() []ScheduleInfo` returns a read-only snapshot of every schedule currently known to the scheduler, exposed alongside the existing reference-based `CancelSchedule`/`PauseSchedule`/`ResumeSchedule` management APIs.
  - Each `ScheduleInfo` reports the schedule `Reference` (user-supplied via `WithReference` or auto-generated) and the target actor `Path`.
  - It has no effect on the schedules themselves, and a schedule stops appearing once it has been canceled or, for one-shot schedules created via `ScheduleOnce`, once it has fired and been delivered.
- **React to cluster leadership changes with the `LeaderChanged` event** ([#1233](https://github.com/Tochemey/goakt/issues/1233)). Each node publishes a `LeaderChanged` event on its eventstream, alongside `NodeJoined`/`NodeLeft`, when the coordinator moves to a different node, so code can react to failovers without polling `IsLeader`.
  - The event carries the new coordinator's peers address (`Address()`) and transition time (`Timestamp()`).
  - The cluster engine emits it from the authoritative membership flag once membership settles after a topology change, at most once per transition; the initial coordinator is seeded silently, not announced.
  - It is best-effort and eventually consistent (may be missed under heavy churn), so query `IsLeader(ctx)` or `Leader(ctx)` for authoritative state. Nothing is emitted outside cluster mode.
- **Factory-free grain activation with `GrainOf`** ([#1231](https://github.com/Tochemey/goakt/issues/1231)). The new generic package-level function `actor.GrainOf[*MyGrain](ctx, system, name, opts...)` retrieves or activates a grain without a factory.
  - The grain kind is derived from the type parameter, auto-registered in the kind registry, and the grain is constructed as a zero value only when local activation is actually needed.
  - Initialization belongs in `OnActivate`, with external resources supplied through `WithGrainDependencies`, which is the same construction contract the cluster already applies when recreating or relocating grains.
  - Identity resolution no longer executes a factory or allocates a grain instance on lookups and remote activations.
  - The testkit gains matching `testkit.GrainOf[T]` and `testkit.NodeGrainOf[T]` helpers.
  - `GrainOf` rejects non pointer-to-struct type parameters with the new `ErrInvalidGrainKind` and detects same-named kind collisions across packages with the new `ErrGrainKindConflict` instead of silently instantiating the wrong type.

### ⚠️ Behavior changes

- **`ScheduleWithCron` in cluster mode requires an explicit reference.** The reference is the shared identity every node claims against, so an auto-generated per-node reference would let every node deliver independently. Calls without `WithReference` are rejected with the new `ErrScheduleReferenceRequired`.
- **Cron schedules registered on every node with the same reference now deliver once per tick cluster-wide** instead of once per node. To keep per-node delivery in cluster mode, register each node's schedule under a distinct reference.
- **`ScheduleWithCron` evaluates the cron expression in UTC when the actor system is in cluster mode** (local timezone is unchanged outside cluster mode). Express clustered cron schedules in UTC; for example `"0 0 9 * * *"` fires at 09:00 UTC.

### 🗑️ Deprecations

- The factory-based grain APIs are deprecated but remain functional: `ActorSystem.GrainIdentity`, `GrainContext.GrainIdentity`, the `GrainFactory` type, and the testkit `TestKit.GrainIdentity` / `TestNode.GrainIdentity` wrappers. Migrate to `GrainOf`; factories that capture dependencies in their closure only ever took effect on the local node, since remote recreation always built the grain as a zero value.

### 🔧 Fixes

- **`WithGrainDisableRelocation` now reaches the cluster** ([#1231](https://github.com/Tochemey/goakt/issues/1231)). The option was stored in the grain configuration but never propagated: the wire record always carried `DisableRelocation=false`, so the relocator relocated grains that had explicitly opted out. The flag now flows end to end through the claim record, the remote activation request (new `DisableRelocation` field on `remote.GrainRequest`), and grain recreation, so peer-activated and recreated grains keep the opt-out.
- **Remote grain activation no longer discards the caller's configuration.** The remote activation request hardcoded a 1s activation timeout with 5 retries and dropped dependencies and mailbox capacity. It now carries the configured `WithGrainInitTimeout`, `WithGrainInitMaxRetries`, `WithGrainMailboxCapacity`, and `WithGrainDependencies` values to the activating node.

## v4.2.13 - 2026-07-04

### ⚡ Performance

- **Supervision no longer runs a goroutine per actor** ([#1222](https://github.com/Tochemey/goakt/issues/1222)). Failure signals are drained by a single shared consumer owned by the dispatcher instead of one lifetime goroutine per actor, so the goroutine count stays bounded and independent of the actor population. Supervision semantics are unchanged (per-actor signal ordering, first failure wins). A build-tagged scale test (`benchmark/scale_test.go`, `-tags=scale`) asserts the bound holds at one million actors.
- **Faster remoting hot path** (client and server, profiled end to end):
  - Protobuf types resolve through a lock-free cache (with bounded negative caching) in front of `protoregistry.GlobalTypes`.
  - Synchronous asks serialize into pooled frame buffers with cached proto sizes; each connection reads through a pooled 32 KiB buffered reader.
  - The server caches parsed sender addresses (bounded at 8192 entries) and validates hosts against one precomputed `host:port` string.
  - Frames that resolve in the proto registry route straight to the proto serializer instead of trial-and-error across registered serializers.
  - The default idle connection pool per endpoint was raised from 8 to 32, exported as `remote.DefaultMaxIdleConns`.
  - Measured on Apple M1 (go1.26): serial remote ask 43 to 40 allocs/op, concurrent remote ask about 7% faster, ingress sender-address handling 28% faster with 33% fewer allocations. Bytes per ask rose roughly 18% because each ask now enforces a real context deadline (see the hang fix below).

### 🔧 Fixes

- **Cross-node grain activation no longer fails identity validation** ([#1227](https://github.com/Tochemey/goakt/issues/1227)). `sendRemoteActivateGrain` sent the full `kind/name` identity as the wire request's `Name`, so the receiver rebuilt it as `kind/kind/name` and rejected valid names. The request now carries the bare grain name. Two-node reproduction in `playground/issue-1227`.
- **Concurrent remote asks could hang forever.** The server could recycle a pooled frame buffer before the handler lookup read the message type name from it, silently dropping the request, and the client never enforced the ask timeout locally. Fixed by looking up the handler before recycling the buffer, closing the connection on a frame with no registered handler (mismatched peers fail fast; pooled clients redial), and bounding `RemoteAsk`, `RemoteBatchAsk`, and `RemoteAskGrain` with a client-side deadline derived from the ask timeout. Validated with 3.6 million operations under the race detector.
- **Remote deadline propagation is clock-skew safe.** The wire format carries the remaining time instead of an absolute timestamp (the approach gRPC uses), so a receiver whose clock runs ahead of the sender no longer fails every ask with `DeadlineExceeded`. Every synchronous remote handler that performs cancellable work now honors the propagated deadline.
- **Hardening from the remoting performance review.** `bufferedConn.Close` no longer races a concurrent `Read` over the pooled reader (reads after close return `net.ErrClosed`); actor system shutdown clears the sender-address cache; host validation compares the same `net.JoinHostPort` format on both sides (fixing IPv6 hosts), with a canonical retry for non-canonical receiver addresses.

## v4.2.12 - 2026-06-26

### ⚡ Performance

#### Lower-overhead memberlist TCP transport packet path

The custom memberlist TCP transport carried avoidable overhead on the packet send path ([#1219](https://github.com/Tochemey/goakt/issues/1219)). Because that path is high frequency (gossip, probes, and pings), each packet was sent with three separate `Write` calls (header, payload, digest), took an `RWMutex` on every send to read the advertised address, and allocated an intermediate string when logging the packet digest. The three writes are now coalesced into a single `writev` via `net.Buffers` with no extra copying, the advertised address (written once during `FinalAdvertiseAddr`) is read lock-free from an atomic value, and the digest is formatted with the `%x` verb directly on the byte slice instead of through an intermediate `fmt.Sprintf`.

### 🔧 Fixes

#### Memberlist transport drops packets that fail the digest check

On the receive path, a packet whose trailing md5 digest did not match the recomputed digest was logged as a mismatch but still delivered to memberlist ([#1219](https://github.com/Tochemey/goakt/issues/1219)). A failed digest check now drops the packet instead of forwarding a corrupt payload. Two related logging defects in the same path are fixed as well: the "not enough data received" warning no longer interpolates an always-nil error, and the three distinct read failures (address length, address bytes, and payload) now report the operation that actually failed instead of sharing one misleading message.

## v4.2.11 - 2026-06-22

### 🔧 Fixes

#### Topic actor deduplication state is now bounded under sustained publishing

The cluster pub/sub topic actor keeps a `processed` set to deduplicate delivered messages, but entries were only ever added, never removed individually ([#1214](https://github.com/Tochemey/goakt/issues/1214)). Under a long-lived producer that publishes continuously, the set grew without bound for the lifetime of the actor system: each publish adds an entry keyed by `{senderID, topic, messageID}`, and the only clearing was `Reset()` in `PreStart`/`PostStop`. Because the topic actor is supervised with `ResumeDirective` (resume does not re-run `PreStart`), in steady state the set was never cleared, leaking memory for always-on, high-rate publishers.

The deduplication state only needs to cover a recent window of message identifiers, so `processed` now uses a time-windowed map (`xsync.TTLMap`) whose entries expire after a configurable retention window. Expired entries are evicted lazily on access, with no background goroutine, so the footprint stays bounded by the publish rate times the window instead of growing forever. A duplicate that arrives after the window has elapsed may be redelivered, which is acceptable under at-least-once delivery.

The retention window defaults to 2 minutes (`DefaultMessageRetention`, matching the duplicate-window convention used by systems such as NATS JetStream) and is configurable via the new `WithMessageRetention(time.Duration)` actor system option:

```go
system, _ := actor.NewActorSystem("sys",
    actor.WithPubSub(),
    actor.WithMessageRetention(5*time.Minute),
)
```

The option is additive and non-breaking; existing systems pick up the bounded behavior automatically with the default window. Deduplication of duplicates that arrive within the window is unchanged.

## v4.2.10 - 2026-06-15

### 🔧 Fixes

#### `SpawnSingleton` retries leader-delegated transient errors during membership churn

`SpawnSingleton` failed terminally on transient cluster conditions when the spawn was delegated to the coordinator over the network, even though the same conditions were retried when detected locally ([#1209](https://github.com/Tochemey/goakt/issues/1209)). A node that called `SpawnSingleton` while cluster membership was still reconciling (for example a node joining during a rolling restart) could fail to start with `invalid response` (`ErrInvalidResponse`) or `remote send failed` (`ErrRemoteSendFailure`) instead of retrying until the cluster settled.

When the local node is not the coordinator, `spawnSingletonOnLeader` delegates the spawn to the leader through `remoting.RemoteSpawn`. On the leader, transient failures are mapped to a proto error code by `wrapSpawnErr` and serialized back across the remoting boundary, where the client's `checkProtoError` de-serializes them into a *different* set of error values than their locally-detected equivalents:

| Leader condition                   | Proto code               | Client yields          | Retried before the fix? |
|------------------------------------|--------------------------|------------------------|-------------------------|
| quorum error                       | `CODE_UNAVAILABLE`       | `ErrRemoteSendFailure` | ❌                       |
| deadline exceeded                  | `CODE_DEADLINE_EXCEEDED` | `ErrRequestTimeout`    | ❌                       |
| stale coordinator / not-yet-placed | `CODE_NOT_FOUND`         | `ErrAddressNotFound`   | ❌                       |
| non-`Error` / empty reply          | (none)                   | `ErrInvalidResponse`   | ❌                       |

The retry classifier `shouldRetrySpawnSingleton` only recognized the locally-detected transient forms (quorum errors, `ErrLeaderNotFound`, `ErrEngineNotRunning`, no-role-members, `context.DeadlineExceeded`, `net.Error` timeouts, and `ECONNREFUSED`), so the leader-delegated forms above were treated as terminal and the retrier stopped immediately, never spending its retry budget.

The classifier now also treats `ErrRemoteSendFailure`, `ErrRequestTimeout`, `ErrAddressNotFound`, and `ErrInvalidResponse` as retryable. Because `shouldRetrySpawnSingleton` is used only on the singleton spawn path, no other call path is affected. A node that calls `SpawnSingleton` during membership churn now retries within its existing budget (default: 5 retries over a 30s window) and eventually spawns or confirms the singleton instead of failing terminally.

A runnable sample lives in `playground/issue-1209`. The exact transient error only surfaces inside a sub-second reconciliation window in a live cluster, so the deterministic regression coverage is in the unit tests (`TestSpawnSingletonRetryBehavior` and `TestShouldRetrySpawnSingleton`), which drive the leader-delegated errors directly through the classifier and the retrier.

## v4.2.9 - 2026-06-13

### ⚡ Performance

#### Node-local grain delivery skips the remoting round trip

In cluster mode, `TellGrain` and `AskGrain` always routed through the remoting stack for a grain owned by the **calling node**, turning every node-local grain message into a loopback TCP round trip (serialize, loopback connection, deserialize, synchronous ack) ([#1203](https://github.com/Tochemey/goakt/issues/1203)). Once a grain was activated and registered in the cluster, the send path went straight to `RemoteTellGrain`/`RemoteAskGrain` regardless of where the grain lived, dialing `host:port` even when that was the current node. This capped local grain throughput at roughly 500 msgs/s and stalled each sender for the full round trip.

`remoteTellGrain` and `remoteAskGrain` now short-circuit when the resolved owner is the calling node (`isLocalGrainOwner`), delivering in-process via `localSend` and skipping the remoting hop. Location transparency is a contract about call semantics, not a mandate to serialize node-local calls, so observable behavior is unchanged. This mainly benefits single-node deployments and any co-located caller/grain pattern, where the round trip was pure overhead.

A runnable sample lives in `playground/issue-1203`.

#### Node-local grain sends skip the cluster registry lookup

After the remoting round trip was removed (above), every node-local `TellGrain`/`AskGrain` still resolved the owner through the cluster registry first: `remoteTellGrain`/`remoteAskGrain` called `cluster.GetGrain` before any local check ([#1203](https://github.com/Tochemey/goakt/issues/1203)), paying a cluster-engine read lock, an olric map `Get`, and a protobuf decode on every send. That lookup became the per-send cost that capped node-local throughput once the network hop was gone, and its read lock is shared with grain registration and removal, coupling the send hot path to cluster mutation.

Both paths now check the local grain table first. When the grain is already activated and live on this node it is owned here, so the message is delivered in-process and the registry lookup is skipped entirely. The `isActive()` guard keeps relocation correct: a grain being moved is deactivated during the handoff, so the send falls through to the authoritative cluster lookup. In `BenchmarkTellGrainNodeLocal` / `BenchmarkAskGrainNodeLocal` this cuts per-send allocations from 36 to 2 (Tell) and 35 to 1 (Ask), roughly 3x to 5x faster per call.

### 🔧 Fixes

#### `NewSlogFrom` honors its level parameter

`log.NewSlogFrom(logger *slog.Logger, level Level)` stored the `level` argument but never used it for filtering ([#1201](https://github.com/Tochemey/goakt/issues/1201)). `Enabled` delegated straight to the wrapped `*slog.Logger`'s handler, so the effective level was whatever the injected handler allowed: sharing an application logger running at INFO and passing `WarningLevel` to keep GoAkt's actor-lifecycle chatter out of production logs silently had no effect, while `LogLevel()` still reported the configured level (which also fed the cluster engine's log configuration with a level GoAkt itself was not honoring).

The level now acts as a minimum level in addition to the wrapped handler's own filtering: a record is emitted only when it passes both. The comparison happens in `slog.Level` space because GoAkt's `Level` constants are not ordered by severity (`DebugLevel` sits numerically above `WarningLevel`), so a record's GoAkt level is mapped through the slog level mapping before being compared against the floor. `NewSlog` is unaffected: it already baked the level into its own handler, and for loggers it builds the new check is a no-op.

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

l := log.NewSlogFrom(logger, log.WarningLevel)
l.Info("suppressed by the WARNING floor") // was printed before the fix
l.Warn("passes the floor")
```

A runnable sample lives in `playground/issue-1201`.

#### Data race between actor-system shutdown and in-flight grain activation

Under `-race`, stopping a clustered `ActorSystem` while a grain was being activated tripped the race detector ([#1205](https://github.com/Tochemey/goakt/issues/1205)). `reset()` (reached from `Stop()`) recreated the `shutdownSignal` channel during teardown, while a concurrent `putGrainOnCluster()` (reached from `GrainIdentity`/`TellGrain`/`AskGrain`) read that same field in its publish `select`, with no synchronization between the two paths. The node-local grain fast path widened the window where activation overlaps shutdown, which is why it surfaced after v4.2.8.

`shutdownSignal` is now recreated once at the start of `Start()`, the only point where no cluster producer or replicate drainer is alive, instead of during `reset()`. Teardown leaves the channel closed, so any producer racing shutdown only ever reads a stable closed channel (its `select` returns at once and the publish is skipped, exactly as intended). The fix adds no locks, atomics, or hot-path allocations, and covers both `putGrainOnCluster` and the structurally identical `putActorOnCluster`.

#### Killed actor re-registered in the cluster registry

In cluster mode, spawning an actor and killing it back-to-back could leave a stale entry in the cluster registry forever. Spawn replicates the actor to the cluster asynchronously: `putActorOnCluster` queues it on `actorsQueue` and the `replicateActors` drainer later calls `cluster.PutActor`. Kill, by contrast, removes it synchronously through the death watch (`deleteNode` then `cluster.RemoveActor`). When the two interleave so the queued `PutActor` runs after `RemoveActor`, the dead actor is re-inserted and never cleaned up. `ActorOf` then resolves it through the cluster slow path and returns a remote PID with no error, even though the actor is gone. This surfaced as a flaky `testkit` `TestTestNode/Kill`.

`replicateOneActor` now skips publishing when the actor is no longer present in the local actor tree, mirroring the stale-actor check in `removeStaleClusterActors`. By the time a stale `PutActor` is drained, the death watch has already removed the node locally, so the publish is dropped and the dead actor stays out of the registry. The normal path (actor still alive) is unaffected.

## v4.2.8 - 2026-06-10

### ✨ New Additions

#### `log.NewZapFrom`

`log.NewZapFrom(logger *zap.Logger)` creates a GoAkt `Logger` backed by a `*zap.Logger` you already own, mirroring the existing `log.NewSlogFrom`. This lets you share one zap instance across GoAkt and the rest of your application without coupling them to GoAkt's `log` package, and without GoAkt dictating the encoding, outputs, or level.

```go
zl, _ := zap.NewProduction()
sys, _ := actor.NewActorSystem("my-system", actor.WithLogger(log.NewZapFrom(zl)))
```

GoAkt's leveled methods wrap the logger and add one stack frame, so `NewZapFrom` derives a child logger with `zap.AddCallerSkip(1)`; if the supplied logger has caller annotation enabled (`zap.AddCaller`), the reported `caller` points at your call site rather than into GoAkt's `log` package. The supplied logger is not modified (zap loggers are immutable). Because the caller owns the output destinations, `LogOutput` returns `nil` and `Flush` delegates to the logger's `Sync` (swallowing the benign sync errors console streams return on some platforms).

### 🔧 Fixes

#### `NewZap` no longer hijacks the global zap logger

`log.NewZap` called `zap.ReplaceGlobals`, mutating process-global zap state every time it ran, including at package init for the `DebugLogger` and `DefaultLogger` vars. Any application constructing its own `NewZap(...)` silently overwrote the host program's global zap logger, and the installed global carried GoAkt's `AddCallerSkip(1)` (calibrated for GoAkt's wrapper), so direct `zap.L()` / `zap.S()` usage elsewhere reported caller information off by one frame. The `ReplaceGlobals` call has been removed; nothing in GoAkt reads the zap global, so this changes no observable behavior for GoAkt callers while leaving the host application's global logger untouched.

### ♻️ Refactors

#### Slimmer `log.Logger` interface

The `log.Logger` interface has been trimmed to align with modern Go logging practice (the `log/slog` design philosophy): a logging abstraction should not let a library terminate the host process, and it should not leak backend internals. The following methods were **removed from the interface**:

- `Fatal(...any)` / `Fatalf(string, ...any)`
- `Panic(...any)` / `Panicf(string, ...any)`
- `LogOutput() []io.Writer`

The level methods (`Info`/`Infof`/`InfoContext`/`InfofContext` and the `Warn`/`Error`/`Debug` equivalents), `Enabled`, `With`, `LogLevel`, `Flush`, and `StdLogger` are unchanged. The concrete implementations (`Slog`, `Zap`, and the discard logger) **still expose** `Fatal`/`Panic`/`LogOutput` as methods, so application code that holds a concrete type keeps working.

`Level.String()` now returns `"invalid"` (instead of an empty string) for `InvalidLevel` and any unrecognized level. The numeric values of the level constants are unchanged.

### ⚠️ Migration

- **You hold the interface (`log.Logger`) and call `Fatal`/`Fatalf`/`Panic`/`Panicf`.** Log at error level and then halt explicitly:

  ```go
  // Before
  logger.Fatal(err)

  // After
  logger.Error(err)
  os.Exit(1)
  ```

  ```go
  // Before
  logger.Panic(err)

  // After
  logger.Error(err)
  panic(err)
  ```

- **You hold a concrete logger** (e.g. the value returned by `log.NewZap` / `log.NewSlog`, or `log.DefaultLogger`). No change is required; `Fatal`/`Panic`/`LogOutput` remain available on the concrete type.

- **You call `LogOutput()` through the interface.** Either keep a reference to the concrete type, or track the writers you passed into the constructor yourself.

## v4.2.7 - 2026-06-01

### ✨ New Additions

#### `RelocationFailed` event

When a departed node's actors or grains cannot be re-deployed during cluster rebalancing, the relocator now publishes a `RelocationFailed` event to the system event stream instead of silently giving up. Relocation is intentionally not retried (a retry would block the serialized relocator and re-run already-completed spawns, lengthening the recovery window for every other node), so subscribe to this event to drive your own recovery, such as re-spawning the affected actors or alerting.

```go
subscriber, _ := sys.Subscribe()
defer sys.Unsubscribe(subscriber)

for msg := range subscriber.Iterator() {
    if e, ok := msg.Payload().(*actor.RelocationFailed); ok {
        log.Printf("relocation failed: node=%s actors=%v grains=%v err=%v",
            e.Address(), e.Actors(), e.Grains(), e.Error())
    }
}
```

### ⚡ Performance

- **Parallel actor and grain relocation.** The relocator previously issued every remote spawn and grain activation for a departed node sequentially from a single goroutine, so recovery time grew linearly with the number of relocated actors. Spawns and activations now run concurrently with a bounded worker pool (`defaultRelocationConcurrency`), capping the fan-out of remote RPCs while shortening the rebalance window for nodes that hosted many actors.

### 🔧 Fixes

- **Failed rebalance no longer wedges future relocations.** When a rebalance returned early on error (cluster peer lookup failure, or a spawn failure surfaced through the wait group), the `relocating` flag was cleared only on the success path, so it stayed set forever and permanently blocked every subsequent cluster rebalance. The relocator now always releases the flag on the failure path.
- **Busy-spin removed from the rebalancing loop.** While a relocation was in flight, the rebalancing loop re-queued the pending peer state and immediately re-read it, spinning a CPU core for the entire duration of the in-flight rebalance. The loop now blocks on a condition variable until the active relocation completes, preserving one-at-a-time serialization without the spin, and wakes promptly on shutdown.
- **Departed node's peer state removed on relocation failure.** The leader previously kept the departed node's peer-state snapshot in the cluster store when relocation failed, leaving an orphaned entry that nothing would ever consume. The failure path now deletes it, mirroring the success path.

## v4.2.6 - 2026-05-29

### 🔧 Fixes

- **Panic in `getDeadlettersCount` under concurrent Prometheus scrape.** When `WithMetrics()` was enabled, the per-PID OTel callback asked the deadletter actor for its count on every scrape. The guard `if to.IsRunning()` is not atomic with the subsequent `Ask`: if the deadletter actor transitioned to stopping in that window, `Ask` returned `(nil, ErrDead)`, the error was discarded, and the bare type assertion `message.(*commands.DeadlettersCountResponse)` panicked on the `nil` reply, taking down the process during `Registry.Gather`. `getDeadlettersCount` now checks the `Ask` error, a `nil` reply, and the type assertion before reading `TotalCount`, returning `0` on any failure, matching the safe pattern already used by `actorSystem.getSetDeadlettersCount`.
- **Leaked OTel metrics callback under high actor churn.** `registerMetrics` discarded the `Registration` handle returned by `meter.RegisterCallback`, so the callback outlived the actor and kept firing on every scrape for every PID that had ever existed, accumulating with each spawn/stop cycle and compounding the panic above. The handle is now stored on the `PID` (guarded by `fieldsLocker`) and unregistered in `doStop`, so the callback is released on every termination path (graceful shutdown and passivation). `registerMetrics` also unregisters any prior handle before installing a new one, so restarting an actor no longer leaks a second live registration.

## v4.2.5 - 2026-05-23

### ✨ New Additions

#### Cluster-aware `PID.Watch` / `PID.UnWatch`

`PID.Watch` and `PID.UnWatch` now work across cluster nodes. Watching a remote actor delivers an `*actor.Terminated` message (through the same `case *Terminated:` arm) as watching a local one, whether the watchee shuts down cleanly or its host node disappears from the cluster.

- **Same API, no migration.** Both calls remain `func(cid *PID)`. Existing local-only code is unaffected; the cross-node path activates automatically when the target PID is remote.
- **Synchronous registration.** When the target is remote, `Watch` blocks on a single round-trip RPC to register the watch on the watchee's host; `UnWatch` reverses it. The deadline is bounded by `WithRemoteWatchTimeout` (default 5s). On failure, no local registration is recorded, so retries are safe.
- **Clean remote shutdown.** When a remote watchee terminates normally, its node delivers a `Terminated` to every watcher via fire-and-forget remote tell, with no synchronous probe, so an unreachable peer cannot stall the watchee's shutdown.
- **Node-loss notification.** If a node disappears from the cluster, watchers of actors on that host receive a `Terminated` stamped with the cluster-detected loss time, so `Terminated.TerminatedAt()` reflects the actual death moment rather than the receiver's wall clock.
- **Symmetric teardown.** When a local actor with outstanding remote watches shuts down, the framework notifies each remote peer and clears both sides of the registry, leaving no dangling entries.

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch ctx.Message().(type) {
    case *actor.PostStart:
        worker := ctx.RemoteLookup("node-b.internal", 9000, "worker-1")
        if worker != nil {
            ctx.Watch(worker) // round-trip RPC registers the watch on node-b
        }
    case *actor.Terminated:
        // fires whether the watchee was local or remote, and when node-b leaves the cluster
    }
}
```

#### `WithRemoteWatchTimeout` option

Bounds the per-call deadline for the `Watch` / `UnWatch` round-trip RPCs. Non-positive values are ignored. Defaults to 5s.

```go
actor.NewActorSystem("svc",
    actor.WithRemote(remote.NewConfig("0.0.0.0", 9000)),
    actor.WithRemoteWatchTimeout(2 * time.Second),
)
```

### 🔌 Wire protocol

Two new internal RPCs (`RemoteWatchRequest`, `RemoteUnWatchRequest`) back the cross-node watch pair, and `actor.Terminated` is now serializable over the wire (`Terminated.TerminatedAt()` survives the round-trip). All three are framework-internal; public Go types and method signatures are unchanged.

## v4.2.4 - 2026-05-15

### 🔉 Logging changes

#### Quieter default log stream

The framework's `Info` level is now reserved for **system-wide, low-frequency operator events**: actor system start/stop, signal handler, clustering enable/start/stop, remote server start/listen/TLS/shutdown, scheduler start/stop, eviction loop start/stop, cluster node joined/left, rebalance start/complete, and data-center topology changes. Per-actor / per-grain / per-RPC / per-peer / per-rebalance-item lifecycle (init, shutdown, passivation, suspension, reinstate, supervision, grain activate/deactivate, remote spawn, peer replication, dead-actor cleanup, routee restart/resume, NATS peer chatter) was demoted to `Debug`.

Third-party `[INFO]` lines emitted by olric and hashicorp/memberlist (routing table updates, gossip events, anti-entropy) are also demoted to `Debug`. `[WARN]` and `[ERROR]`/`[ERR]` from those libraries are unchanged.

**Migration.** If you relied on the previous verbosity (per-actor lifecycle traces, olric routing-table chatter visible at `Info`), set your logger to `log.DebugLevel`. Errors and warnings are unaffected — a healthy steady-state cluster now produces close to zero `Info` lines per node per minute.

#### Memberlist transport now honors `TransportConfig.Logger`

`internal/memberlist.NewTransport` previously ignored the `Logger` field on its config and wrote to stdout at `Info`. It now routes through the configured logger (or `log.DiscardLogger` when none is supplied), so transport-level errors and warnings reach the same sink as the rest of the framework. Added recognition of memberlist's short `[ERR]` prefix in the third-party log adapter — these lines were previously dropped silently.

### ✨ New Additions

#### JSON serializer (sonic-backed)

Third built-in serializer alongside `ProtoSerializer` and `CBORSerializer`. `remote.JSONSerializer` encodes arbitrary Go values as JSON via [bytedance/sonic](https://github.com/bytedance/sonic) using the `ConfigFastest` preset (no HTML escaping, no JSON-marshaler validation — output is consumed by another goAkt node, not a browser).

- **Same wire shape as the other built-ins.** Length-prefixed self-describing frame: `totalLen | nameLen | type-name | json-bytes`, all big-endian uint32 headers — identical layout to `ProtoSerializer` and `CBORSerializer`.
- **Same registration path.** `WithSerializers(new(MyMessage), remote.NewJSONSerializer())` auto-registers the type in the global types registry; no separate `RegisterSerializableTypes`-style step. Both sender and receiver must register the same types.
- **Platform support.** sonic ships JIT-accelerated fast paths for amd64 and arm64 and falls back to `encoding/json` on other architectures (386, riscv64, s390x). API surface is identical; only throughput differs.
- **Depth bound.** sonic does not expose a per-decode nesting limit, so peer-controlled payloads are bounded by `WithMaxFrameSize` (default 16MB). Tune downward if accepting frames from untrusted peers.

#### `WithJSONSerializables` convenience option

Bulk JSON registration mirroring `WithSerializables` for CBOR. Shares a lazy `DefaultJSONSerializer()` singleton across all calls — `JSONSerializer` is stateless, so reuse is safe and avoids per-option allocation.

```go
remote.NewConfig("0.0.0.0", 9000,
    remote.WithJSONSerializables(new(MyEventA), new(MyEventB), (*MyInterface)(nil)),
)
```

Typed nil interface pointers bind the serializer at the interface level; concrete types bind per-type.

## v4.2.3 - 2026-05-09

### ✨ New Additions

#### Cross-node stream refs (`SourceRef` / `SinkRef`)

Wire-portable handles that adapt a `Source[T]` or `Sink[T]` into a stage usable on any node in the cluster, mirroring Akka StreamRefs.

- `Source.SourceRef(ctx, sys)` publishes a producer; receivers reconstruct the source via `ref.Source(sys)`. `Sink.SinkRef(ctx, sys)` does the symmetric thing for consumers via `ref.Sink(sys)`. Refs themselves are plain serialisable values and can be shipped inside any registered remote message — so a node can hand off "where to send/read from" the same way it would hand off any other piece of data.
- **Setup prerequisite — both registrations are required on every node:** append `stream.RemoteOptions()` to your `remote.NewConfig` (registers the subscribe / request / complete / error / cancel control wires) **and** register the element type with `remote.WithSerializables(new(MyEvent))` (the same registration any remote `rctx.Tell` to a remote PID needs). Forgetting either is the common failure mode — missing control-plane registration manifests as the bridge hanging on subscribe; missing element-type registration manifests as `"no serializer found"` on the producer's first ship or as silent dead-letter on the consumer side. Asymmetric registration (one node has it, the other doesn't) breaks only the unconfigured direction. User element types travel across the wire as ordinary remote messages — no extra envelope, no double encoding — so the remoting layer's existing CBOR registry handles them with no per-stream serializer overhead.
- Single-subscription semantics. The endpoint stays alive through a 30s grace window after stream completion so a racing late subscriber gets a deterministic `"already consumed"` rejection rather than telling a dead actor; after the window the endpoint reaps itself via `system.ScheduleOnce`, so consumed refs don't leak in long-running processes.
- Wire-level credit: the consumer's downstream demand drives `streamRequestWire` upstream; the producer ships only what was requested. A bounded pending queue (1024 elements) converts a slow-consumer scenario into a visible `"backpressure overflow"` stream error instead of unbounded mailbox growth.
- Bridges `Watch` their remote endpoint as defense in depth — an unexpected endpoint death (panic, system shutdown mid-stream, expired grace on a stale ref) surfaces a stream error on the bridge's `StreamHandle` instead of a hang.
- **Direct-address resolution** — refs carry the producer node's host:port plus actor name, so the consumer's bridge resolves via `pid.RemoteLookup(host, port, name)` straight against the producer's remote server. Cluster-registry / olric replication is **not** on the critical path, so resolution succeeds the moment the producer node is reachable rather than after async broadcast catches up. A short retry loop (jittered exponential backoff via `flowchartsman/retry`, 10s budget) absorbs transient connectivity blips.

#### Stream graph junctions

Eight new fan-in / fan-out operators for the `stream` package, completing the non-linear topology surface.

Fan-in:

- `Concat(sources...)`: consumes sub-sources sequentially; only one sub-pipeline is materialised at a time.
- `Zip(sources...) Source[[]T]` / `ZipWith(combine, sources...) Source[V]`: pair N same-typed inputs into tuples (or a user combine); completes when any input is exhausted.
- `MergePreferred(preferredIdx, sources...)`: drains the priority slot first; falls back to the lowest-indexed non-empty slot.
- `MergePrioritized(weights, sources...)`: picks the next slot by weighted random over slots with data.
- `MergeLatest(sources...)`: emits `[]T` snapshots of the latest value on every input; first snapshot only after every input has emitted once.
- `MergeSequence(extractSeq, sources...)`: emits in strict ascending sequence order across N inputs; fails on missing seq at completion.

Fan-out:

- `Partition(src, n, partitionFn)`: routes each element to exactly one branch by user predicate.
- `Unzip(src, unzipFn)`: splits a single source into two sources of potentially different element types.

Graph DSL adds a matching `ConcatInto(name, from...)` node kind alongside `MergeInto`.

#### SubFlow: substream support

A "stream of streams" abstraction. Three constructors produce a `SubFlow[K, T]`:

- `GroupBy(src, maxSubstreams, keyFn)`: one substream per distinct key, materialised lazily; cardinality capped by `maxSubstreams` (overflow returns `ErrTooManySubstreams`).
- `SplitWhen(src, predicate)`: start a new substream on a predicate-true element (becomes the first of the new substream).
- `SplitAfter(src, predicate)`: end the current substream after a predicate-true element (becomes the last of the closing substream).

Compose with `SubFlowVia(sf, flow)` for per-substream transforms; `MergeSubstreams(sf)` collapses back into a flat `Source[T]`. Each substream is a real GoAkt sub-pipeline materialised lazily on its first element, so per-substream state (e.g. `Scan` accumulators) is genuinely independent.

#### Per-substream backpressure

`SubFlow.WithSubstreamBuffer(perKeyBuffer, strategy)` caps in-flight elements per substream and applies an `OverflowStrategy`:

- Default: 256 elements per key with `DropTail`.
- `FailSource`: raises `ErrSubstreamOverflow` and terminates the stream so skewed-workload incidents are observable rather than silent.
- `DropHead` and `BackpressureSource` currently collapse to drop-newest (alternatives would break per-key ordering or deadlock the splitter).

Drops are reported via the existing `OnDrop` hook and `droppedElements` metric.

#### `FlatMapConcat` / `FlatMapMerge`

Two flow operators for per-element expansions that are themselves streams (paginated APIs, per-row queries, fan-out workers):

- `FlatMapConcat(fn func(In) Source[Out])`: drains each sub-source fully before starting the next; preserves end-to-end ordering.
- `FlatMapMerge(breadth, fn)`: runs up to `breadth` sub-sources concurrently; order within each preserved, order across non-deterministic. Demand is bounded by `breadth` so a slow inner source throttles the outer pipeline.

#### Per-substream error strategy

`SubFlow.WithErrorStrategy(SubstreamErrorStrategy)` controls how the splitter reacts when a per-key sub-pipeline errors:

- `SubstreamFailAll` (default): fails the whole stream via `StreamHandle.Err()`; matches FailFast.
- `SubstreamDrop`: blocklist the failing key; siblings finish normally.
- `SubstreamRestart`: discard the failed pipeline; next element with the same key spawns a fresh substream.

Previously substream errors were swallowed inside the per-substream sink, a correctness gap closed by this change.

### 🔧 Fixes

- **Shared remoting client closed by per-actor shutdown.** `pid.doStop` and `grainPID.deactivate` were calling `pid.remoting.Close()`, but `pid.remoting` is the actor system's shared remoting client (set on every spawned PID via `withRemoting(x.remoting)` in `actorSystem.spawnActor`). Stopping any single actor closed every outbound coalescer system-wide, so a concurrent `rctx.Tell` from another live actor saw `"remote send failed: coalescer is closed"` and silently dropped the message — observable as cross-node stream tests where the producer source completes, shuts down, and races with the bridge's element sends. The remoting client is system-owned; it is now closed only by `shutdownRemoting` during actor-system shutdown. Per-actor lifecycle no longer touches it.

## v4.2.2 - 2026-04-24

### ✨ New Additions

- **`WithThroughputBudget` option — tunable dispatcher per-turn message budget.** Exposes the dispatcher's per-turn message budget (the maximum number of messages a worker drains from a single actor before yielding back to the scheduler) as a user-configurable `ActorSystem` option. The default remains `32`, chosen for balanced fairness across many actors; raising it amortises per-turn scheduling overhead and keeps CPU caches warm on hot actors at the cost of fairness, and lowering it favours tail-latency predictability under many-small-actor workloads. Per-actor FIFO ordering and the single-threaded-per-actor execution invariant are preserved regardless of the value — a yield is a pause, not a reorder. Variant benchmarks — `BenchmarkTellThroughput`, `BenchmarkRequestThroughput`, `BenchmarkSendSyncThroughput`, `BenchmarkGrainTellThroughput`, `BenchmarkGrainTellFanOutThroughput` — sweep the budget across `{8, 32, 64, 128, 256}` so users can measure impact on their own workload. Measured on Apple M1, the default is near-optimal on typical workloads; fan-out workloads see ~2-5% aggregate gains at `64`-`128` and a mild regression at `256`, confirming the fairness cost at very high budgets. See the `WithThroughputBudget` godoc for guidance per workload class.

### ⚡ Performance

- **Pooled output frames on the remote-Tell send path.** The outbound encoder in `internal/net` now draws its output buffer from the existing `FramePool` (previously used only on the read side) and returns it to the pool after `conn.Write`. This closes the last reflective-`make([]byte, 0, totalLen)` hot-path allocation on the client encode and server response paths. The public `ProtoSerializer.MarshalBinary` and `MarshalBinaryWithMetadata` APIs are unchanged; new `*To` variants accept an optional `*FramePool` and form the pooled fast path used by `Client` and `ProtoServer`. Wire format is unchanged. Measured on Apple M1 with the `BenchmarkRemoteTellThroughput` configuration (20 senders / 10 engines / 2000 actors over localhost): **bytes allocated per message 507 → 427 (−15.8%)** with throughput flat within run-to-run variance. `ProtoSerializer.MarshalBinary` drops from 13.8% of allocated bytes to not appearing in the top allocators at all.
- **Intrusive mailboxes — allocation-free local dispatch hot path.** `UnboundedMailbox` and `grainMailbox` now use their payload types (`*ReceiveContext` / `*GrainContext`) directly as the MPSC linked-list node via an unexported `next` field, retiring the separate `node` struct and its dedicated pool on both paths. Under sustained producer-faster-than-consumer load, peak in-flight message count vastly exceeds any fixed pool size, so the old design churned through `new(node)` per message once the pool saturated — dominating GC CPU time. Collapsing the two-pool design into one eliminates that half of the churn, and the remaining `ReceiveContext` / `GrainContext` pool is now the only allocation surface on the hot path. On the grain side, `grainPID`'s activity timestamp also moves from `go.uber.org/atomic.Time` (boxed through `atomic.Value`) to a stdlib `atomic.Int64` holding UnixNano, removing the per-message allocation inside `markActivity`. Net result — machine-independent: the `Tell` / `SendAsync` benchmarks drop from 1 to 0 reported allocs/op under the bench harness; `BenchmarkGrainTell`, `BenchmarkGrainTellFanOut`, and `BenchmarkGrainAsk` drop from 24 B/op, 1 allocs/op to 0 B/op, 0 allocs/op. Absolute throughput gains are workload- and hardware-dependent; the architectural property is that the hot path no longer allocates, so GC cost scales with application logic, not with message rate. A payload type is linked into at most one mailbox at a time; the stash path therefore copies across mailboxes via a new `cloneContext` (slow path, not on dispatch). Public `ReceiveContext`, `GrainContext`, `Mailbox`, `UnboundedMailbox`, and `grainMailbox` constructors and method signatures are unchanged.

### 🔧 Fixes

- **Worker-pool cleanup off-by-one — leaked one idle worker per pass under the small-list path.** A recent modernisation of `internal/net.WorkerPool.cleanup` swapped the small-list reap loop from `for j = 0; j < iws; j++` to `for j = range iws`; the two forms differ in post-loop `j` value (`iws` vs `iws - 1`), so the reap prefix `idleWorkerList[:j]` left the last expired worker behind on every cleanup tick. Once the list reached a single stuck entry the `if j == 0 { continue }` early-out skipped it forever, leaking worker goroutines. Reverted to the explicit C-style form with a comment documenting the hazard so the linter's "modernize using range over int" suggestion does not re-trigger it. The binary-search branch (`iws > 400`) was unaffected. Covered by `TestWorkerPool_Cleanup` and `TestWorkerPool_CleanupIdleWorkerSlots`, which had been silently failing.
- **Metadata zero-copy lifetime bug under pooled frames.** `internal/net.Metadata.UnmarshalBinary` extracted header keys and values via `unsafe.String` pointers that aliased the caller-supplied byte slice. On the server path that slice is a pooled request frame returned to the `FramePool` before the handler runs — with the write path also now drawing from the same pool, the buffer can be recycled and overwritten while the handler is still holding those metadata strings, corrupting their bytes. Replaced the zero-copy extraction with explicit `string(data[...])` copies; metadata payloads are small (a few headers per call) and the allocation cost is negligible compared to the correctness win. No public API change.
- **Data race on the ejected-sentinel `next` field in `UnboundedMailbox.Dequeue`.** `Dequeue` reset the previous sentinel's `next` with a plain assignment while `IsEmpty` / `Len` (also reachable via `finishOrReclaim`) atomically load that same field. Because `finishOrReclaim` resets `schedState` before calling `IsEmpty`, a sibling worker may enter `Dequeue` on the same PID concurrently and the two accesses collide. The reset is now an `atomic.StorePointer`. Caught by `TestMessageOrdering` under `-race`; no throughput impact since the producer side already used an atomic store on the same field.
- **Data race on `segment.deqIdx` and `head` in `UnboundedSegmentedMailbox` under cross-worker mailbox handoff.** The segment's dequeue index (plain `uint64`) and the mailbox's `head` pointer (plain `*segment`) were written by the consumer in `Dequeue` and read by `IsEmpty`. When the dispatcher pool hands mailbox ownership from one worker to another, the outgoing worker may call `IsEmpty` via `finishOrReclaim` while the incoming worker begins `Dequeue`, colliding on both fields. Both are now `atomic.Uint64` and `atomic.Pointer[segment]` respectively. Covered by a new `TestUnboundedSegmentedMailbox_CrossWorkerHandoff` under `-race`; no throughput impact since the producer side already used the matching atomic stores.
- **Dispatcher self-deadlock when `ActorSystem.Stop` is invoked from within a worker turn.** The old `dispatcher.stop()` blocked on a `sync.WaitGroup` until every worker exited; when shutdown was triggered from inside an actor's `Receive` (or a grain's handler), the calling worker waited on its own `WaitGroup` entry and deadlocked the system. `dispatcher.stop()` and its internal `sync.WaitGroup` have been removed in favour of the fire-and-forget `signalStop`, which closes the ready queue and wakes parked workers without blocking the caller — each worker drains its current turn and exits on its own. Lifecycle goroutines that shutdown must still wait for (`replicateActors` / `replicateGrains`) are tracked via a separate `drainers sync.WaitGroup` so reset can safely reassign `shutdownSignal`. Covered by the updated dispatcher tests.
- **Panic: send on closed channel in `putActorOnCluster` / `putGrainOnCluster`.** Both cluster-publication helpers unconditionally sent on `actorsQueue` / `grainsQueue`; the shutdown path closes those channels under a single-shot `shuttingDown` CAS, and any in-flight spawn arriving after the close panicked the process. Each send is now gated by the `shuttingDown` atomic flag, with a deferred `recover` to absorb the narrow TOCTOU window where the flag still reads `false` but the channel closes between the check and the send. Shutdown now also waits on the replicate drainers before letting `reset()` reassign `shutdownSignal`, so late pushes can no longer observe a half-rebuilt actor system.

### 🔧 Improvements

- **Removed dead stash-release bookkeeping.** Moving context release into `UnboundedMailbox.Dequeue` (see the intrusive-mailbox entry) eliminated the sole reader of `ReceiveContext.stashed`. The field, the `stashed.Store(true)` call inside `ctx.Stash()`, and the obsolete `releaseContext` helper are gone; `dispatchOne` on both actor and grain paths drops its unused `retained bool` return. Observable stash semantics are unchanged (covered by the full `TestStash` / `TestReentrancy` suites under `-race`).
- **Benchmark suite measures end-to-end delivery.** `BenchmarkTell`, `BenchmarkSendAsync`, `BenchmarkGrainTell`, `BenchmarkGrainTellFanOut`, and all `*Throughput` variants now use a counting actor/grain with a done-channel signal and wait for the receiver to drain `b.N` messages before stopping the timer. Fan-out benchmarks hold a per-grain private counter instead of a single shared atomic so the measurement itself adds no cross-core contention. Previously the clock stopped at the end of the producer loop, which reported enqueue rate under producer-faster-than-consumer conditions rather than delivery rate.

## v4.2.1 - 2026-04-16

### ✨ New Additions

- Enhanced the `testkit` package with new helper methods for improved test ergonomics.

### ⚡ Performance

- **Dispatcher pool — actor scheduling substrate.** The per-actor spawn-on-burst drainer goroutine is replaced with a fixed pool of worker goroutines (`max(GOMAXPROCS, 2)`) that cooperatively multiplex the entire actor population, following the Akka / Pekko / Erlang / Orleans pattern. Each `ActorSystem` owns one `dispatcher`; actors are scheduled onto a hybrid ready queue (per-worker 256-slot local rings + shared amortised-FIFO global ring + half-quota work stealing + condvar parking). A worker pulls an actor, drains up to `32` messages per turn, then yields so peers get a turn — bounding the blocking window a single actor can impose and decoupling goroutine count from actor count. A three-state atomic machine (`Idle` / `Scheduled` / `Processing`) on each `PID` preserves the single-threaded-per-actor execution invariant via a `Scheduled → Processing` CAS. Every user-visible semantic (FIFO ordering, `PreStart` / `PostStop` contract, reentrancy stash, panic recovery, supervision) is unchanged; the only observable difference is that `runtime.NumGoroutine()` no longer scales with active-actor count. Each actor also gains a dedicated system mailbox that is drained before the user mailbox every turn, so control-plane messages (`PoisonPill`, supervision, passivation, `Terminated`, dead-letter routing) cannot queue behind a user-message backlog. See [`architecture/DISPATCHER_POOL_DESIGN.md`](architecture/DISPATCHER_POOL_DESIGN.md).
- **Grains migrated onto the dispatcher pool.** `grainPID` now implements the same `schedulable` contract as `PID` and shares the actor system's dispatcher, retiring the per-grain spawn-on-burst drainer goroutine. The single-threaded-per-grain execution invariant is preserved by the same three-state CAS (`Idle` / `Scheduled` / `Processing`) used by actors; worker turns drain up to 32 messages per rotation, with race-safe reclaim on drain and cooperative yield-and-reschedule when the budget is exhausted. Goroutine count is now bounded by GOMAXPROCS regardless of active grain count — at million-grain scale this removes the dominant per-grain goroutine stack overhead. Public `Grain`, `GrainContext`, and `GrainIdentity` semantics are unchanged.
- **Grain send hot path — regex compile elimination.** `GrainIdentity.Validate()` previously recompiled a regex on every `TellGrain` / `AskGrain` call; `GrainIdentity` fields are immutable after construction so the result is now memoised via `sync.Once`. Profiling had this accounting for ~98% of per-call allocations. Measured on Apple M1 at steady state: **allocations per call 51 → 1 (−98%)**, **bytes per call 3,216 → 24 (−99%)**. Throughput measured end-to-end: `TellGrain` single-grain +180% (466k → 1.31M msgs/s @ GOMAXPROCS=8), `TellGrain` fan-out across 256 grains +85% (460k → 850k msgs/s), `AskGrain` +260% (275k → 988k msgs/s). Actor `Tell` / `Ask` paths are untouched — the change is confined to `GrainIdentity` and the fast-path `ensureGrainProcess` bypass of `singleflight` when the grain is already active. Grain-only benchmarks (`BenchmarkGrainTell`, `BenchmarkGrainTellFanOut`, `BenchmarkGrainAsk`) added to the `benchmark` package.
- **Send coalescing for `Tell` to remote actors.** When `Tell` (via `actor.Tell`, `*PID.Tell`, or `ReceiveContext.Tell`) targets a remote `*PID`, the outbound transport path now uses a per-destination Nagle-style writer that amortizes per-RPC cost across messages that pile up while a previous send is in flight. A single message against an idle destination is still sent immediately — batching happens only while a send is already in progress, so there is no artificial latency on the happy path. Measured on Apple M1 with 20 senders / 10 engines / 2000 actors and default zstd compression, throughput goes from **~122k msgs/sec to ~880k msgs/sec (~7.2×)**. The benchmark covering this configuration lives in `benchmark/remote_tell_thoughput_test.go`.
- Context propagation is preserved end-to-end for messages sent to remote actors via a new per-message `metadata` field on the transport-level `RemoteMessage`, populated by the configured `ContextPropagator` at enqueue time and applied by the server before dispatching each message.
- Wire format: `internalpb.RemoteMessage` gained `map<string, string> metadata = 4;`. Additive and backward-compatible — older peers ignore the field.

### ⚠️ Behavior changes

- `Tell` is a fire-and-forget one-way send; its returned `error` still reports failures detected before the message leaves the caller's process (no serializer registered for the message type, already-cancelled context at entry, `errors.ErrRemoteSendBackpressure` on a saturated outbound queue, `errors.ErrRemoteSendFailure` for other enqueue-time failures). What changed is that the call now returns once the message has been handed off to the per-destination coalesced writer rather than after the transport ACK, so transport-level failures on sends that have already been batched happen after `Tell` returns and can no longer appear in the return value; they are logged asynchronously through the actor system's logger instead. Callers that need a reply or delivery confirmation should use `Ask` against the remote `*PID`, which is unaffected. Per-call context deadlines set on a remote `Tell` call are not honored once the message has been batched; the writer uses its own transport deadline.
- **Default remote compression changed from `ZstdCompression` to `NoCompression`.** This aligns GoAkt with the convention adopted by gRPC, Akka, Erlang distribution, Kafka, and Orleans: transport-level compression is left off by default because typical actor payloads are small structured protobuf for which compression yields little bandwidth savings and meaningful CPU and allocator overhead — particularly on intra-cluster LAN links. Deployments where bandwidth is the binding constraint (cross-region / WAN, large or highly repetitive payloads) should opt in explicitly with `remote.WithCompression(remote.ZstdCompression)`. Both ends of a remote connection must agree on the algorithm, so when upgrading either configure both peers explicitly or upgrade them together.
- **`Tell` to a remote `*PID` now applies bounded-queue backpressure when send coalescing is engaged.** Previously, when the per-destination writer's input channel was saturated the call silently fell through to a second synchronous connection; under burst load this exposed callers to transport-level errors from connection-pool contention. Behaviour now matches the industry-standard pattern used by Erlang distribution, Akka Artery, Kafka producer, and gRPC streaming: the call blocks on the outbound queue until space becomes available, the caller's `context` is cancelled, or the transport is shut down. On context deadline/cancel the call returns a new typed error `errors.ErrRemoteSendBackpressure` (joined with the underlying `ctx.Err()`, so `errors.Is(err, context.DeadlineExceeded)` remains true); callers decide whether to retry, drop, or circuit-break. The synchronous fallback path has been removed. This eliminates a class of spurious transport errors under load (benchmarks run during development went from ~500 errors / 10M sends to 0) and makes overload visible as a distinct, actionable signal rather than generic RPC failure. Local `Tell` and all `Ask` calls (local or remote) are unaffected.

### 🔧 Improvements

- Adjusted the `benchmark` package name for consistency.
- Conducted general code cleanup across the codebase.
- Cleaned up the documentation site and README.

### 📦 Dependencies

- Updated `dorny/paths-filter` action to v4.
- Updated `github.com/andybalholm/brotli` to v1.2.1.

## v4.2.0 - 2026-03-31

### 🔄 Distributed Data (CRDTs)

GoAkt now ships with built-in **Conflict-free Replicated Data Types** — data structures that can be updated independently on any node and are guaranteed to converge to a consistent state without coordination, locks, or consensus rounds.

#### 📦 CRDT Types

| Type            | Description                                                        |
|-----------------|--------------------------------------------------------------------|
| **GCounter**    | Grow-only counter with per-node increment slots                    |
| **PNCounter**   | Positive-negative counter (increment and decrement)                |
| **LWWRegister** | Last-writer-wins register with timestamp-based conflict resolution |
| **ORSet**       | Observed-remove set with add-wins semantics                        |
| **ORMap**       | Map with OR-Set keys and per-value CRDT merge                      |
| **Flag**        | Boolean that can only transition from false to true                |
| **MVRegister**  | Multi-value register that preserves concurrent writes              |

#### ⚙️ How It Works

- A per-node **Replicator** system actor manages the local CRDT store.
- Delta dissemination uses the existing **TopicActor** pub/sub — no custom gossip layer.
- **Anti-entropy** runs periodically as a safety net, exchanging version digests with random peers.
- All CRDT types are **immutable values** — every mutation returns a new value plus a delta.

#### 🔧 Enabling CRDTs

```go
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(disco).
    WithCRDT(
        crdt.WithAntiEntropyInterval(30 * time.Second),
        crdt.WithPruneInterval(5 * time.Minute),
        crdt.WithTombstoneTTL(24 * time.Hour),
    )
```

#### 💻 Using CRDTs from an Actor

```go
replicator := ctx.ActorSystem().Replicator()

// Write
actor.Tell(ctx.Context(), replicator, &crdt.Update{
    Key:     crdt.PNCounterKey("request-count"),
    Initial: crdt.NewPNCounter(),
    Modify:  func(current crdt.ReplicatedData) crdt.ReplicatedData {
        return current.(*crdt.PNCounter).Increment(nodeID, 1)
    },
})

// Read
resp, err := actor.Ask(ctx.Context(), replicator, &crdt.Get{
    Key: crdt.PNCounterKey("request-count"),
}, 5*time.Second)
if getResp, ok := resp.(*crdt.GetResponse); ok && getResp.Data != nil {
    count := getResp.Data.(*crdt.PNCounter).Value()
    _ = count
}
```

#### 🔑 Key Capabilities

- **Local-first by default** — reads and writes return immediately with no network wait.
- **Optional coordination** — `WriteTo: Majority/All` and `ReadFrom: Majority/All` for stronger consistency when needed.
- **Key deletion** with tombstones and configurable TTL-based pruning.
- **Change subscriptions** — actors can watch keys and receive `Changed[T]` notifications on update.
- **Durable snapshots** — optional periodic persistence to BoltDB for crash recovery.
- **OpenTelemetry metrics** — store size, merge count, delta publish/receive counts, anti-entropy rounds, tombstone count.
- **Comprehensive benchmarks** — all CRDT types benchmarked for merge, clone, delta, and value operations; replicator benchmarked for end-to-end throughput.

---

## [v4.1.1] - 2026-03-27

### ✨ New Additions

#### 🌊 `stream` package — Reactive Streams for GoAkt

A new top-level `stream` package brings demand-driven, actor-native stream processing to GoAkt.
Every pipeline stage runs inside a GoAkt actor, inheriting supervision, lifecycle management, and
location transparency automatically. Pipelines are lazy: nothing executes until `RunnableGraph.Run`
is called against a live `ActorSystem`.

**Core abstractions**

- `Source[T]` — lazy description of a stream origin; assembled with `Via` / `To` into a `RunnableGraph`.
- `Flow[In, Out]` — lazy description of a transformation stage; type-safe, composable.
- `Sink[T]` — lazy description of a terminal consumer stage.
- `RunnableGraph` — fully assembled pipeline; a value type that can be `Run` multiple times for independent instances.
- `StreamHandle` — live handle returned by `Run`; exposes `ID()`, `Done()`, `Err()`, `Stop(ctx)`, `Abort()`, and `Metrics()`.

**Sources**

| Constructor                       | Description                                                                   |
|-----------------------------------|-------------------------------------------------------------------------------|
| `Of[T](values...)`                | Finite source from a fixed set of values                                      |
| `Range(start, end)`               | Integer range source (`[start, end)`)                                         |
| `FromChannel[T](ch)`              | Reads from a Go channel; completes when the channel closes                    |
| `FromActor[T](pid)`               | Pulls from a GoAkt actor using the `PullRequest` / `PullResponse[T]` protocol |
| `Tick(interval)`                  | Emits `time.Time` on a fixed interval; runs until cancelled                   |
| `Merge[T](sources...)`            | Fans N sources into one; completes when all inputs complete                   |
| `Combine[T,U,V](left, right, fn)` | Zips two sources pairwise via `fn`; zip semantics                             |
| `Broadcast[T](src, n)`            | Fans one source out to N independent branches                                 |
| `Balance[T](src, n)`              | Distributes one source across N branches (round-robin with backpressure)      |
| `FromConn(conn, bufSize)`         | Reads `[]byte` frames from a `net.Conn`                                       |
| `Unfold[S,T](seed, step)`         | Generates values from a seed with a stateful step function                    |

**Flows**

| Constructor                         | Description                                                                     |
|-------------------------------------|---------------------------------------------------------------------------------|
| `Map[In,Out](fn)`                   | Type-changing transformation; no error path                                     |
| `TryMap[In,Out](fn)`                | Transformation with error; ErrorStrategy controls failure handling              |
| `Filter[T](predicate)`              | Keeps only elements where `predicate` returns true                              |
| `FlatMap[In,Out](fn)`               | Expands each element into a slice of outputs                                    |
| `Flatten[T]()`                      | Unwraps `[]T` elements into individual elements                                 |
| `Batch[T](n, maxWait)`              | Groups elements into `[]T` slices of at most `n`; flushes early after `maxWait` |
| `Buffer[T](size, strategy)`         | Asynchronous buffer with configurable overflow strategy                         |
| `Throttle[T](n, per)`               | Limits throughput to at most `n` elements per `per` duration                    |
| `Deduplicate[T]()`                  | Suppresses consecutive duplicate elements (`T` must be comparable)              |
| `Scan[In,State](zero, fn)`          | Running accumulation; emits each intermediate state                             |
| `WithContext[T](key, value)`        | Labels a tracing boundary; passes elements through unchanged                    |
| `ParallelMap[In,Out](n, fn)`        | Applies `fn` concurrently with up to `n` goroutines; unordered output           |
| `OrderedParallelMap[In,Out](n, fn)` | Like `ParallelMap` but preserves input order (min-heap resequencing)            |

**Sinks**

| Constructor                     | Description                                                                    |
|---------------------------------|--------------------------------------------------------------------------------|
| `ForEach[T](fn)`                | Calls `fn` for each element                                                    |
| `Collect[T]()`                  | Accumulates all elements; retrieve via `Collector[T].Items()` after completion |
| `Fold[T,U](zero, fn)`           | Reduces to a single value; retrieve via `FoldResult[U].Value()`                |
| `First[T]()`                    | Captures the first element then cancels upstream                               |
| `Ignore[T]()`                   | Discards all elements; useful for side-effecting flows                         |
| `Chan[T](ch)`                   | Writes each element to a Go channel; applies natural backpressure when full    |
| `ToActor[T](pid)`               | Forwards each element to a GoAkt actor via `Tell`                              |
| `ToActorNamed[T](system, name)` | Resolves actor by name on each element and forwards via `Tell`                 |

**Pipeline DSL**

- `From[T](src)` — starts a `LinearGraph[T]` fluent builder.
- `LinearGraph[T].Via(flow)` — chains a type-preserving flow.
- `LinearGraph[T].To(sink)` — attaches a sink and returns a `RunnableGraph`.
- `ViaLinear[In,Out](g, flow)` — type-changing step on a `LinearGraph`.
- `Via[In,Out](src, flow)` — package-level free function for type-changing flows on `Source`.
- `Graph` DSL — named-node builder for non-linear topologies (fan-out, fan-in, merge).

**Backpressure & configuration**

- Credit-based demand propagation: sinks signal demand upstream; sources produce only what is requested.
- `StageConfig` — per-stage knobs: `InitialDemand`, `RefillThreshold`, `ErrorStrategy`, `RetryConfig`, `OverflowStrategy`, `BufferSize`, `Mailbox`, `Name`, `Tags`, `Tracer`, `Fusion`, `MicroBatch`, `PullTimeout`, `OnDrop`.
- `ErrorStrategy` — `FailFast` (default), `Resume` (skip), `Retry` (with `RetryConfig.MaxAttempts`), `Supervise`.
- `OverflowStrategy` — `DropTail`, `DropHead`, `DropBuffer`, `Backpressure`, `Fail`.
- `FusionMode` — `FuseStateless` (default, fuses adjacent `Map`/`Filter` stages into one actor), `FuseNone`, `FuseAggressive`.

**Observability**

- `Tracer` interface — per-element hooks: `OnElement`, `OnDemand`, `OnError`, `OnComplete`; attach via `WithTracer`.
- `MetricsReporter` interface — snapshot forwarding to external systems (Prometheus, OTel).
- `StreamHandle.Metrics()` — live `StreamMetrics` snapshot with element-in, element-out, and error counts.

**Performance internals**

- `time.Now()` is guarded by `tracer != nil` on the element hot path — no clock overhead when tracing is disabled.
- `fusedFlowActor` (stage-fusion fast path) uses credit-based batch demand refill instead of one `streamRequest` allocation per element, reducing demand-message overhead by ~160× under default configuration.
- `chanSourceActor` bridges external channels to the actor mailbox in batches of up to 64 values per `actor.Tell` call (drain-without-blocking strategy), significantly reducing mailbox-enqueue overhead on high-throughput channels while keeping latency low for slow channels.
- `connSourceActor` pools full-sized read buffers via `sync.Pool`; each element sent downstream is an exact-sized copy, eliminating a per-read heap allocation.
- `Filter` and `Deduplicate` return `nil` instead of an empty `[]any{}` for skipped elements, removing a slice allocation on every filtered-out element.
- Stream sub-IDs use an atomic counter instead of UUID generation, avoiding an entropy read and string-formatting cost on every `RunnableGraph.Run` call.
- `flowActor.tryFlushOutput` now fires `Tracer.OnElement` with the definitively-assigned sequence number, fixing incorrect seqNo reporting for multi-output transforms (`FlatMap`).

## [v4.1.0] - 2026-03-16

### ⚠️ Breaking Changes

- **Lifecycle and dead-letter event types now use `Path` instead of `string`.**
  The following message types have been updated to use the `Path` interface for actor identification, replacing raw `string` addresses:
  - `Deadletter`: `Sender()` and `Receiver()` now return `Path` (was `string`). `NewDeadletter` accepts `Path` for sender and receiver.
  - `Terminated`: `Address() string` replaced by `ActorPath() Path`. `NewTerminated` accepts `Path`.
  - `ActorStarted`: `Address() string` replaced by `ActorPath() Path`. `NewActorStarted` accepts `Path`.
  - `ActorStopped`: `Address() string` replaced by `ActorPath() Path`. `NewActorStopped` accepts `Path`.
  - `ActorPassivated`: `Address() string` replaced by `ActorPath() Path`. `NewActorPassivated` accepts `Path`.
  - `ActorChildCreated`: `Address() string` replaced by `ActorPath() Path`; `Parent()` now returns `Path`. `NewActorChildCreated` accepts `Path` for both arguments.
  - `ActorRestarted`: `Address() string` replaced by `ActorPath() Path`. `NewActorRestarted` accepts `Path`.
  - `ActorSuspended`: `Address() string` replaced by `ActorPath() Path`. `NewActorSuspended` accepts `Path` (reason remains `string`).
  - `ActorReinstated`: `Address() string` replaced by `ActorPath() Path`. `NewActorReinstated` accepts `Path`.

  **Migration:** Replace `.Address()` calls with `.ActorPath()` and pass `Path` values (e.g., from `pid.Path()`) instead of `string` addresses when constructing or consuming these events.

## [v4.0.0] - 2026-03-05

v4.0.0 delivers **simplification** and **performance** through:

| Theme                | Key Changes                                                                                              |
|----------------------|----------------------------------------------------------------------------------------------------------|
| **Unified APIs**     | Single actor reference (`*PID`), single lookup (`ActorOf`), unified scheduler, pluggable serializers     |
| **Type Flexibility** | `any` replaces `proto.Message` across all message-passing surfaces; CBOR supports arbitrary Go types     |
| **Remoting**         | Config-only public API; client is internal; ProtoSerializer (default) and CBORSerializer for any Go type |
| **Identity**         | `Path` interface replaces `*address.Address`; `address` package moved to `internal/address`              |
| **Performance**      | Low-GC serialization, lock-free type registry, single-allocation frames, lock-free `PID.Path()`          |

### 🗺️ Migration Quick Reference

| From                                            | To                                                                                                  |
|-------------------------------------------------|-----------------------------------------------------------------------------------------------------|
| `proto.Message` in handlers/call sites          | `any`                                                                                               |
| `goaktpb.*` types                               | `actor.*` (e.g. `actor.PostStart`, `actor.PoisonPill`)                                              |
| `ActorRef`                                      | `*PID`                                                                                              |
| `ActorOf` → `(addr, pid, err)`                  | `ActorOf` → `(*PID, error)`                                                                         |
| `Actors()` / `ActorRefs(ctx, timeout)`          | `Actors(ctx, timeout) ([]*PID, error)`                                                              |
| `LocalActor(name)`                              | `ActorOf(ctx, name)`                                                                                |
| `RemoteActor(ctx, name)`                        | `ActorOf(ctx, name)`                                                                                |
| `RemoteSchedule*` methods                       | `Schedule*` with remote PID from `ActorOf`                                                          |
| `GetPartition(name)`                            | `Partition(name)`                                                                                   |
| `pid.Address()`                                 | `pid.Path()`                                                                                        |
| `ctx.SenderAddress()` / `ctx.ReceiverAddress()` | `ctx.Sender().Path()` / `ctx.Self().Path()`                                                         |
| `remote.Remoting` / `remote.Client`             | Use actor system and `client.Node` APIs; configure via `WithRemote` / `WithRemoteConfig`            |
| `WithRemoting`                                  | `WithRemote(config)` (actor system) / `WithRemoteConfig(config)` (client node)                      |
| `address` package                               | `internal/address` (use `Path` interface instead)                                                   |
| Custom `Logger` implementations                 | Implement new methods: `*Context`, `LogLevel`, `Enabled`, `With`, `LogOutput`, `Flush`, `StdLogger` |

### ⚠️ Breaking Changes

#### 🔧 API & Type System

- `proto.Message` replaced by `any` in all public methods (`PID.Tell`, `PID.Ask`, `Schedule*`, `AskGrain`, `TellGrain`, etc.).
- `goaktpb` package removed; system message types moved to `actor` package (e.g., `actor.PostStart`, `actor.PoisonPill`, `actor.Terminated`, `actor.Deadletter`, etc.).
- Internal protobuf files removed (`deadletter.proto`, `healthcheck.proto`); payloads are now native Go structs.
- `supervisionSignal` decoupled from protobuf; `msg` is now `any`, `timestamp` is `time.Time`.
- Cluster event payload type changed from `*anypb.Any` to `any` with typed event structs (`NodeJoinedEvent`, `NodeLeftEvent`).

#### 🔍 Actor Reference & Lookup

- `ActorRef` removed; `*PID` is the sole actor reference for local and remote actors.
- `ActorOf` return signature unified: `(addr, pid, err)` → `(*PID, error)`; use `pid.Path()` for host/port/name/system.
- `Actors()` and `ActorRefs()` merged into `Actors(ctx, timeout) ([]*PID, error)`.
- `LocalActor` and `RemoteActor` removed; use `ActorOf(ctx, name)` for both.
- `RemoteScheduleOnce`, `RemoteSchedule`, `RemoteScheduleWithCron` removed; use unified `Schedule*` with remote PID from `ActorOf`.
- `GetPartition(name)` renamed to `Partition(name)`.

#### 🌐 Remoting & Configuration

- Remoting client no longer exported; use actor system and `client.Node` APIs; configure via `WithRemote` / `WithRemoteConfig`.
- `client.Node.Remoting()` and `WithRemoting` removed; use `WithRemote(config *remote.Config)` on the actor system or `WithRemoteConfig(config *remote.Config)` on `client.Node`.

#### 📝 Log Package

- `Logger` interface extended with `*Context` methods, `LogLevel`, `Enabled`, `With`, `LogOutput`, `Flush`, `StdLogger`; custom implementations must add all new methods.

#### 🧪 Testkit & ReceiveContext

- `testkit.Probe.SenderAddress()` removed; use `Sender()` and `pid.Path()`.
- `ReceiveContext.SenderAddress()` and `ReceiverAddress()` removed; use `ctx.Sender().Path()` and `ctx.Self().Path()`.

#### 🆔 Address & Path

- `address` package moved to `internal/address`; use `Path` interface from `pid.Path()` instead.
- `PID.Address()` replaced by `PID.Path()`; returns `Path` interface with `String()`, `Name()`, `Host()`, `Port()`, `HostPort()`, `System()`, `Equals()`.

### ✨ New Additions

#### 📨 System Messages & Serialization

- Native Go system messages in `actor/messages.go` (lifecycle, actor events, deadletter, cluster events) with `time.Time` timestamps.
- `actor/messages_serializers.go` for encoding native message types across process boundaries.
- Pluggable `remote.Serializer` interface; `ProtoSerializer` (default) and `CBORSerializer` for any Go type.
- CBOR serializer with auto-registration via `WithSerializers`; lock-free type registry; single allocation on encode.
- Serializer dispatch on server and client via `map[reflect.Type]Serializer`; composite receive path for protobuf and CBOR coexistence.

#### 🆔 Path & Identity

- `Path` interface for location-transparent actor identity; `String()` and `HostPort()` pre-computed and cached.

#### 🌐 Remote Capabilities

- `ActorState` enum and `RemoteState` for querying actor lifecycle on remote nodes (Running, Suspended, Stopping, Relocatable, Singleton).

#### 🔖 PID & Errors

- `PID.Kind()` — actor kind accessor (reflected type name).
- Remote PID — lightweight cross-node handle via `newRemotePID`.
- New sentinel errors: `errors.ErrRemotingDisabled`, `errors.ErrNotLocal`.

#### 📝 Log Package

- Extended `Logger` interface with context-aware, structured logging, and introspection methods.
- `log/slog.go` — stdlib slog implementation with low-GC optimizations (enabled-before-format, typed attrs, caller caching, buffer pooling).
- `log/zap.go` — Zap implementation with buffered file output, typed fields, stack-allocated `With`.
- `log/discard.go` — no-op logger for tests.

#### 🔀 Routing

- Consistent Hash Router — `WithConsistentHashRouter(extractor)` routes messages with the same key to the same routee. Supports custom hashers, configurable virtual nodes, and automatic ring rebuild on scale events.

#### 📦 Internal Extractions

- `internal/commands` package — command abstraction extracted from `pid.go` and `actor_system.go`.

### 🔧 Bug Fixes

- `cleanupCluster` singleton kind removal: now checks `pid.singletonSpec != nil` instead of `pid.IsSingleton()` to fix stale kind entries and `ErrKindAlreadyExists` / `ErrSingletonAlreadyExists` on new leader.

### ⚡ Internal Improvements

- `address.Address.String()` — eager caching in constructors; eliminates write-race window.
- `internal/xsync.List` — deduplication, `comparable` type param, `Contains`, pre-allocated array, cleared slots on `Reset`.
- Scheduler — unified local/remote delivery via `makeJobFn` passing target PID to `PID.Tell`.
- `toReceiveContext` — else-branch elimination; single unconditional `receiveContext.build` call.
- `actor/actor_path.go` — low-GC path implementation; `HostPort()` allocation-free; `Equals` single cached-string comparison; `PID.Path()` lock-free.

### 🌐 Remoting Capabilities

- Length-prefixed TCP frames; pooled connections; optional TLS; compression (NoCompression, Gzip, Zstd default, Brotli).
- Server config: `NewConfig`, `WithWriteTimeout`, `WithReadIdleTimeout`, `WithMaxFrameSize`, `WithCompression`, `WithContextPropagator`, `WithSerializers`.
- Messaging: `RemoteTell`, `RemoteAsk`, `RemoteBatchTell`, `RemoteBatchAsk`.
- Lifecycle: `RemoteLookup`, `RemoteSpawn`, `RemoteReSpawn`, `RemoteStop`, `RemoteReinstate`, `RemoteState`.
- Remote spawn options: `WithHostAndPort`, `WithRelocationDisabled`, `WithDependencies`, `WithStashing`, `WithPassivationStrategy`, `WithReentrancy`, `WithSupervisor`, `WithRole`.
- Grain operations: `RemoteActivateGrain`, `RemoteTellGrain`, `RemoteAskGrain`.
- Serialization: ProtoSerializer default; CBOR for arbitrary Go types; custom via `remote.Serializer`.

## [v3.14.0] - 2026-02-18

### 🔧 Fixes

- 🛡️ `preShutdown` now skips building and persisting peer state when relocation is disabled, avoiding unnecessary cluster operations and ensuring shutdown proceeds correctly when `WithoutRelocation` is configured.
- 🔀 Fix channel-pool poisoning in `GrainContext.NoErr()` where sending on both the response and error channels for synchronous calls created a scheduling race — if preempted between the two sends, the second value landed on a channel already returned to the pool, corrupting subsequent callers.
- ⏱️ Fix `localSend` timeout/cancel paths returning channels to the pool while the grain goroutine could still write to them, causing stale values to leak into later requests.
- 🔒 Fix data race in `PID.recordProcessedMessage()` reading `passivationStrategy` without a lock by replacing the runtime type assertion with an `atomic.Bool` flag set once at init.

### ✨ Features

- 🌐 Multi-datacenter support with DC-transparent messaging, pluggable control plane (NATS JetStream, Etcd), DC-aware placement via `SpawnOn` with `WithDataCenter`, and cross-DC actor/grain communication. See `datacenter` package and `WithDataCenter` option.
- 🛡️ Added `WithGrainDisableRelocation` option to disable actor/grain relocation for scenarios where relocation is not desired (e.g., stateless actors, short-lived grains).

### ⚡ Performance Improvements

- 🌐 Replace ConnectRPC/HTTP-based remoting with a high-performance protobuf-over-TCP server and connection-pooling client, eliminating HTTP/2 framing, header parsing, and middleware overhead on every remote call:
  - Multi-loop TCP accept with `SO_REUSEPORT`, `TCP_FASTOPEN`, and `TCP_DEFER_ACCEPT` socket options for lower connection-setup latency and kernel-level load balancing across accept loops.
  - Sharded `WorkerPool` for connection dispatch, avoiding single-channel contention when dispatching accepted connections under high concurrency.
  - Self-describing length-prefixed wire protocol with dynamic protobuf type dispatch via the global registry, removing the need for per-service generated stubs and HTTP path routing.
  - `FramePool` with power-of-two bucketed `sync.Pool` instances (256 B – 4 MiB) for read buffers and frame byte slices, minimising per-message heap allocations and GC pressure.
  - LIFO connection pool in the TCP client with lazy stale-connection eviction, enabling connection reuse without background goroutines.
  - Pluggable `ConnWrapper` compression layer supporting Zstandard, Brotli, and Gzip applied transparently on both client and server sides.
- ♻️ Replace `sync.Pool` with GC-resistant channel-based bounded pools for `ReceiveContext`, `GrainContext`, response channels, and error channels, eliminating cross-P pool thrashing and madvise overhead on the `Tell`, `Ask`, `SendAsync`, and `SendSync` hot paths.
- 🧮 Switch `PID.latestReceiveTimeNano` from `atomic.Time` to `atomic.Int64` (Unix nanoseconds) to avoid the ~24-byte interface-boxing allocation per message incurred by `atomic.Time.Store`.
- 💾 Cache `Address.String()` and `GrainIdentity.String()` results lazily, removing repeated `fmt.Sprintf` allocations on every message.
- 📬 Optimise mailbox node value fields from `atomic.Pointer` to plain pointers in both `UnboundedMailbox` and `grainMailbox`, reducing atomic overhead on enqueue/dequeue.
- 🗜️ Inline `defer` closures in Ask-path functions (`PID.Ask`, `actor.Ask`, `handleRemoteAsk`, grain `localSend`) to eliminate per-call closure heap allocations.
- 🔓 Restructure `ActorOf` to perform local actor lookup before acquiring the system-wide `RWMutex`, and bypass `PID.ActorSystem()` getter lock in `SendAsync`/`SendSync`, reducing read-lock acquisitions from three to one on the local hot path.
- ⏳ Coalesce `passivationManager.Touch` calls via an atomic timestamp guard (`lastPassivationTouch`), reducing mutex contention from once-per-message to at most once per 100 ms.

## [v3.13.0] - 2026-01-23

### 🔧 Fixes

- 🔧 Fix and simplify the implementation of the relocation engine.
- 🛡️ Harden the cluster singleton implementation with well guided godoc
- 📦 Exposed the eventstream package that was accidentally moved into the internal package
- 🐛 Fix actor relocation race condition when nodes leave the cluster. Peer state is now persisted to selected cluster peers via RPC before leaving membership, ensuring state is available for relocation when NodeLeft events are processed. BoltDB store now ensures immediate read-after-write visibility to prevent timing issues. All shutdown errors (preShutdown, persistence, cluster cleanup) are properly tracked and returned. See the performance optimisation regarding the relocation further down.
- Upgrade Go version to from 1.25.3 to 1.25.5 due to some dependencies upgrades requiring it.

### ✨ Features

- 🔌 Added `Dependencies()` and `Dependency(dependencyID string) ` to `GrainContext` to access the grain's dependency container.
- ⚙️ Added `Extensions()` and `Extension(extensionID string)` to `GrainContext` to access grain extensions.
- 🔌 Added ` Dependencies()` and `Dependency(dependencyID string)` to `ReceiveContext` to access the actor's dependency container.
- 🛡️ Added remoting panic recovery that logs the procedure and returns a Connect internal error to callers.
- ⬆️ Upgrade Go version to from 1.25.3 to 1.25.5 due to some dependencies upgrades requiring it.

### ⚡ Performance Improvements

#### 🚀 Remoting Optimizations

- ⚡ Changed default compression from `NoCompression` to `ZstdCompression` for both remoting client (`NewRemoting`) and server (`NewConfig`/`DefaultConfig`). Zstd provides excellent compression ratios (50-70% bandwidth reduction) with lower CPU overhead compared to gzip, making it ideal for high-frequency remoting traffic.
- 🔄 Added `RemotingServiceClient` caching to reuse clients per `host:port` endpoint, eliminating repeated client creation overhead and reducing allocations for calls to the same remote node.

#### 🔄 Relocation Process

PR: https://github.com/Tochemey/goakt/pull/1079

##### 🚀 Overview

Improved the efficiency of actor/grain state replication when a node gracefully leaves the cluster. The new implementation reduces network overhead from O(N) to O(3) while maintaining reliability through quorum-based acknowledgment.

##### 🧩 What Changed

- **Targeted Replication**: State is now sent only to the 3 oldest peers instead of all cluster members
- **Quorum-Based Acknowledgment**: Shutdown proceeds once 2-of-3 peers acknowledge, reducing latency
- **Early Termination**: Remaining RPCs are cancelled after quorum is reached
- **Compression Enabled**: Use Zstd compression when setting remoting for the actor system will reduce payload size by 4-6x
- **Resource Cleanup**: Proper cleanup of remoting clients after replication

##### 🧭 Why Oldest Peers?

Leadership in the cluster is determined by node age (oldest = coordinator). By replicating to the 3 oldest peers:

- The current leader always receives the state
- If the leader fails, the next-oldest (who also has the state) becomes leader
- State is guaranteed to be available for relocation regardless of topology changes

##### 📈 Performance Improvement

| Metric           | Before             | After            |
|------------------|--------------------|------------------|
| Network calls    | O(N)               | O(3)             |
| Data transferred | N × payload        | 3 × payload      |
| Shutdown latency | Wait for all peers | Wait for 2 peers |

##### 🛠️ Technical Details

**Shutdown Flow**:

1. Build PeerState snapshot (actors + grains)
2. Select 3 oldest peers by CreatedAt timestamp
3. Send compressed state via parallel RPCs
4. Return success when 2-of-3 acknowledge
5. Cancel remaining RPCs and proceed with membership leave

```text
                    ┌──────────────────────────────┐
                    │  1. Build PeerState snapshot │
                    │     (actors + grains)        │
                    └──────────────────────────────┘
                                   │
                                   ▼
                    ┌──────────────────────────────┐
                    │  2. selectOldestPeers(3)     │
                    │     - Query cluster members  │
                    │     - Sort by CreatedAt      │
                    │     - Return top 3 oldest    │
                    └──────────────────────────────┘
                                   │
                                   ▼
                    ┌──────────────────────────────┐
                    │  3. Create cancellable ctx   │
                    │     + compression remoting   │
                    └──────────────────────────────┘
                                   │
            ┌──────────────────────┼──────────────────────┐
            ▼                      ▼                      ▼
     ┌────────────┐         ┌────────────┐         ┌────────────┐
     │  RPC to    │         │  RPC to    │         │  RPC to    │
     │  Peer 1    │         │  Peer 2    │         │  Peer 3    │
     │ (#1 oldest)│         │ (#2 oldest)│         │ (#3 oldest)│
     └─────┬──────┘         └─────┬──────┘         └─────┬──────┘
           │                      │                      │
           ▼                      ▼                      │
     ┌──────────┐           ┌──────────┐                 │
     │  ACK ✓   │           │  ACK ✓   │                 │
     └─────┬────┘           └─────┬────┘                 │
           │                      │                      │
           └──────────┬───────────┘                      │
                      ▼                                  │
              ┌───────────────┐                          │
              │ QUORUM (2/3)  │                          │
              │   REACHED!    │                          │
              └───────┬───────┘                          │
                      │                                  │
                      ▼                                  │
              ┌───────────────┐                          │
              │ cancelRPCs()  │─────────────────────────►X (cancelled)
              └───────┬───────┘
                      │
                      ▼
              ┌───────────────┐
              │ Return nil    │
              │ (success)     │
              └───────┬───────┘
                      │
                      ▼
              ┌───────────────┐
              │ cluster.Stop()│
              │ (leave member)│
              └───────────────┘
                      │
                      ▼
              ┌───────────────┐
              │ NodeLeft event│
              │ fires on peers│
              └───────────────┘
                      │
                      ▼
              ┌───────────────┐
              │ Leader reads  │
              │ from local    │
              │ BoltDB store  │
              └───────────────┘
                      │
                      ▼
              ┌───────────────┐
              │ Relocator     │
              │ spawns actors │
              └───────────────┘
```

##### 🔁 Backward Compatibility

This is an internal optimization with no API changes. Existing applications require no modifications.

## [v3.12.1] - 2026-06-01

### ✨ Features

- 🔁 Added reentrancy-enabled request messaging (`Request`/`RequestName`) with configurable modes (AllowAll/StashNonReentrant), per-call overrides/timeouts, and per-actor in-flight limits; replies are delivered via `Response` and in-flight requests are canceled on restart/passivation.
- 🔌 Added GrainContext async piping helpers (`PipeToGrain`, `PipeToActor`, `PipeToSelf`) for off-mailbox work with optional timeout/circuit breaker controls.

## [v3.12.0] - 2025-31-12

### ✨ Features

- 🧭 `SpawnOn` now uses the system-wide default supervisor strategy configured via `WithDefaultSupervisor`.
- 🧭 Added `WithDefaultSupervisor` to configure the ActorSystem-wide default supervisor strategy.

### 🔧 Fixes

- 🧱 Grain activation flow revamped to prevent panics and duplicate activations.
- ♻️ Added recovery handling for Grain activation/deactivation failures.
- 🕒 `ScheduleOnce` now reliably triggers.
- 🧮 Actor count tracking fixed to avoid mismatch/underflow/overflow.

### ⚙️ Improvements & Refactors

- ⚖️ Cluster engine now emits topology change events resiliently when stable/healthy; added `WithClusterBalancerInterval` in `ClusterConfig`.
- 📦 Moved supervisor code into its own `supervisor` package (replace `actor` with `supervisor` in existing code).
- 🧵 Relocation avoids relocating child actors during the relocation process.
- 🧬 Relocation now preserves each actor's configured supervisor strategy.
- 🔁 Restart behavior revamped to restart the full child family tree.
- 🧰 Address and PID internals revamped; guardrails and ID utilities cleaned up.
- 🧵 Cluster singleton spawning made more resilient with stronger error handling.
- 🗄️ Cluster storage BoltDB file creation reworked.
- 📴 Reduced log noise during actor system shutdown.
- 🧹 General internal maintenance and cleanup.

### 🧪 Tests & Examples

- 🧷 Expanded test coverage across grains, remoting, cluster singletons, and supervision flows.
- 🧭 Added playground scenarios for regression coverage and issue reproduction (e.g., 1017, 1022, 1031, 1036).

### 📚 Documentation

- 📘 README updated with a full feature list.

### 📦 Dependencies

- ⬆️ Cluster engine dependency upgraded.
- 🔄 Routine dependency maintenance updates.
