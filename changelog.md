# Changelog

## [v4.0.0] - TBD

> 📖 **Read more:** For the complete migration guide and detailed change tracking, see [CHANGELOG_V400.md](./CHANGELOG_V400.md).

### 🎯 Executive Summary

- **Unified APIs**: Single actor reference (`*PID`), single lookup (`ActorOf`), unified scheduler, pluggable serializers.
- **Type Flexibility**: `any` replaces `proto.Message` across all message-passing surfaces; CBOR supports arbitrary Go types.
- **Remoting**: Config-only public API; client is internal; ProtoSerializer (default) and CBORSerializer for any Go type.
- **Identity**: `Path` interface replaces `*address.Address`; `address` package moved to `internal/address`.
- **Performance**: Low-GC serialization, lock-free type registry, single-allocation frames, lock-free `PID.Path()`.

### ⚠️ Breaking Changes

#### API & Type System

- `proto.Message` replaced by `any` in all public methods (`PID.Tell`, `PID.Ask`, `Schedule*`, `AskGrain`, `TellGrain`, etc.).
- `goaktpb` package removed; system message types moved to `actor` package (e.g., `actor.PostStart`, `actor.PoisonPill`, `actor.Terminated`, `actor.Deadletter`, etc.).
- Internal protobuf files removed (`deadletter.proto`, `healthcheck.proto`); payloads are now native Go structs.
- `supervisionSignal` decoupled from protobuf; `msg` is now `any`, `timestamp` is `time.Time`.
- Cluster event payload type changed from `*anypb.Any` to `any` with typed event structs (`NodeJoinedEvent`, `NodeLeftEvent`).

#### Actor Reference & Lookup

- `ActorRef` removed; `*PID` is the sole actor reference for local and remote actors.
- `ActorOf` return signature unified: `(addr, pid, err)` → `(*PID, error)`; use `pid.Path()` for host/port/name/system.
- `Actors()` and `ActorRefs()` merged into `Actors(ctx, timeout) ([]*PID, error)`.
- `LocalActor` and `RemoteActor` removed; use `ActorOf(ctx, name)` for both.
- `RemoteScheduleOnce`, `RemoteSchedule`, `RemoteScheduleWithCron` removed; use unified `Schedule*` with remote PID from `ActorOf`.
- `GetPartition(name)` renamed to `Partition(name)`.

#### Remoting & Configuration

- Remoting client no longer exported; use actor system and `client.Node` APIs; configure via `WithRemoteConfig`.
- `client.Node.Remoting()` and `WithRemoting` removed; use `WithRemoteConfig(config *remote.Config)` exclusively.

#### Log Package

- `Logger` interface extended with `*Context` methods, `LogLevel`, `Enabled`, `With`, `LogOutput`, `Flush`, `StdLogger`; custom implementations must add all new methods.

#### Testkit & ReceiveContext

- `testkit.Probe.SenderAddress()` removed; use `Sender()` and `pid.Path()`.
- `ReceiveContext.SenderAddress()` and `ReceiverAddress()` removed; use `ctx.Sender().Path()` and `ctx.Self().Path()`.

#### Address & Path

- `address` package moved to `internal/address`; use `Path` interface from `pid.Path()` instead.
- `PID.Address()` replaced by `PID.Path()`; returns `Path` interface with `String()`, `Name()`, `Host()`, `Port()`, `HostPort()`, `System()`, `Equals()`.

### ✨ New Additions

#### System Messages & Serialization

- Native Go system messages in `actor/messages.go` (lifecycle, actor events, deadletter, cluster events) with `time.Time` timestamps.
- `actor/messages_serializers.go` for encoding native message types across process boundaries.
- Pluggable `remote.Serializer` interface; `ProtoSerializer` (default) and `CBORSerializer` for any Go type.
- CBOR serializer with auto-registration via `WithSerializers`; lock-free type registry; single allocation on encode.
- Serializer dispatch on server and client via `map[reflect.Type]Serializer`; composite receive path for protobuf and CBOR coexistence.

#### Path & Identity

- `Path` interface for location-transparent actor identity; `String()` and `HostPort()` pre-computed and cached.

#### Remote Capabilities

- `ActorState` enum and `RemoteState` for querying actor lifecycle on remote nodes (Running, Suspended, Stopping, Relocatable, Singleton).
- `remote/actor_state.go` with `RemoteState(ctx, host, port, name, state)`.

#### PID & Errors

- `PID.Kind()` — actor kind accessor (reflected type name).
- Remote PID — lightweight cross-node handle via `newRemotePID`.
- New sentinel errors: `errors.ErrRemotingDisabled`, `errors.ErrNotLocal`.

#### Log Package

- Extended `Logger` interface with context-aware, structured logging, and introspection methods.
- `log/slog.go` — stdlib slog implementation with low-GC optimizations (enabled-before-format, typed attrs, caller caching, buffer pooling).
- `log/zap.go` — Zap implementation with buffered file output, typed fields, stack-allocated `With`.
- `log/discard.go` — no-op logger for tests.

#### Internal Extractions

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
