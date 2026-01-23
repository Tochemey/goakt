# Changelog

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
| ---------------- | ------------------ | ---------------- |
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
