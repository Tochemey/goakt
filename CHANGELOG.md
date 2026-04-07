# Changelog

## [Unreleased]

### ✨ New Additions

- Enhanced the `testkit` package with new helper methods for improved test ergonomics.

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
