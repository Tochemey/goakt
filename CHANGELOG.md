# Changelog

## [Unreleased]

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

### 🐛 Fixes

- **Shared remoting client closed by per-actor shutdown.** `pid.doStop` and `grainPID.deactivate` were calling `pid.remoting.Close()`, but `pid.remoting` is the actor system's shared remoting client (set on every spawned PID via `withRemoting(x.remoting)` in `actorSystem.spawnActor`). Stopping any single actor closed every outbound coalescer system-wide, so a concurrent `rctx.Tell` from another live actor saw `"remote send failed: coalescer is closed"` and silently dropped the message — observable as cross-node stream tests where the producer source completes, shuts down, and races with the bridge's element sends. The remoting client is system-owned; it is now closed only by `shutdownRemoting` during actor-system shutdown. Per-actor lifecycle no longer touches it.

## v4.2.2 - 2026-04-24

### ✨ New Additions

- **`WithThroughputBudget` option — tunable dispatcher per-turn message budget.** Exposes the dispatcher's per-turn message budget (the maximum number of messages a worker drains from a single actor before yielding back to the scheduler) as a user-configurable `ActorSystem` option. The default remains `32`, chosen for balanced fairness across many actors; raising it amortises per-turn scheduling overhead and keeps CPU caches warm on hot actors at the cost of fairness, and lowering it favours tail-latency predictability under many-small-actor workloads. Per-actor FIFO ordering and the single-threaded-per-actor execution invariant are preserved regardless of the value — a yield is a pause, not a reorder. Variant benchmarks — `BenchmarkTellThroughput`, `BenchmarkRequestThroughput`, `BenchmarkSendSyncThroughput`, `BenchmarkGrainTellThroughput`, `BenchmarkGrainTellFanOutThroughput` — sweep the budget across `{8, 32, 64, 128, 256}` so users can measure impact on their own workload. Measured on Apple M1, the default is near-optimal on typical workloads; fan-out workloads see ~2-5% aggregate gains at `64`-`128` and a mild regression at `256`, confirming the fairness cost at very high budgets. See the `WithThroughputBudget` godoc for guidance per workload class.

### 🚀 Performance

- **Pooled output frames on the remote-Tell send path.** The outbound encoder in `internal/net` now draws its output buffer from the existing `FramePool` (previously used only on the read side) and returns it to the pool after `conn.Write`. This closes the last reflective-`make([]byte, 0, totalLen)` hot-path allocation on the client encode and server response paths. The public `ProtoSerializer.MarshalBinary` and `MarshalBinaryWithMetadata` APIs are unchanged; new `*To` variants accept an optional `*FramePool` and form the pooled fast path used by `Client` and `ProtoServer`. Wire format is unchanged. Measured on Apple M1 with the `BenchmarkRemoteTellThroughput` configuration (20 senders / 10 engines / 2000 actors over localhost): **bytes allocated per message 507 → 427 (−15.8%)** with throughput flat within run-to-run variance. `ProtoSerializer.MarshalBinary` drops from 13.8% of allocated bytes to not appearing in the top allocators at all.
- **Intrusive mailboxes — allocation-free local dispatch hot path.** `UnboundedMailbox` and `grainMailbox` now use their payload types (`*ReceiveContext` / `*GrainContext`) directly as the MPSC linked-list node via an unexported `next` field, retiring the separate `node` struct and its dedicated pool on both paths. Under sustained producer-faster-than-consumer load, peak in-flight message count vastly exceeds any fixed pool size, so the old design churned through `new(node)` per message once the pool saturated — dominating GC CPU time. Collapsing the two-pool design into one eliminates that half of the churn, and the remaining `ReceiveContext` / `GrainContext` pool is now the only allocation surface on the hot path. On the grain side, `grainPID`'s activity timestamp also moves from `go.uber.org/atomic.Time` (boxed through `atomic.Value`) to a stdlib `atomic.Int64` holding UnixNano, removing the per-message allocation inside `markActivity`. Net result — machine-independent: the `Tell` / `SendAsync` benchmarks drop from 1 to 0 reported allocs/op under the bench harness; `BenchmarkGrainTell`, `BenchmarkGrainTellFanOut`, and `BenchmarkGrainAsk` drop from 24 B/op, 1 allocs/op to 0 B/op, 0 allocs/op. Absolute throughput gains are workload- and hardware-dependent; the architectural property is that the hot path no longer allocates, so GC cost scales with application logic, not with message rate. A payload type is linked into at most one mailbox at a time; the stash path therefore copies across mailboxes via a new `cloneContext` (slow path, not on dispatch). Public `ReceiveContext`, `GrainContext`, `Mailbox`, `UnboundedMailbox`, and `grainMailbox` constructors and method signatures are unchanged.

### 🐛 Fixes

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

### 🚀 Performance

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

### 🐛 Bug Fixes

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

### 🐛 Fixes

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

### 🐛 Fixes

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

### 🐛 Fixes

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
