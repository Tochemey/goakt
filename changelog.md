# Changelog

## [Unreleased]

### 🐛 Fixes
- 🔧 Fix and simplify the implementation of the relocation engine.
- 🛡️ Harden the cluster singleton implementation with well guided godoc
- 📦 Exposed the eventstream package that was accidentally moved into the internal package
- 🐛 Fix actor relocation race condition when nodes leave the cluster. Peer state is now persisted to all cluster peers via RPC before leaving membership, ensuring state is available for relocation when NodeLeft events are processed. BoltDB store now ensures immediate read-after-write visibility to prevent timing issues. All shutdown errors (preShutdown, persistence, cluster cleanup) are properly tracked and returned.
- ⬆️ Upgrade Go version to from 1.25.3 to 1.25.5 due to some dependencies upgrades requiring it.

### ✨ Features
- 🔌 Added `Dependencies()` and `Dependency(dependencyID string) ` to `GrainContext` to access the grain's dependency container.
- ⚙️ Added `Extensions()` and `Extension(extensionID string)` to `GrainContext` to access grain extensions.
- 🔌 Added ` Dependencies()` and `Dependency(dependencyID string)` to `ReceiveContext` to access the actor's dependency container.

### ⚡ Performance Improvements

#### 🔄 Actor/Grains Relocation Revamped
- ⚡ **Dramatic shutdown latency reduction**: Shutdown time reduced from seconds/minutes to < 100ms for up to 1,000 actors (100x+ faster). Achieved by eliminating expensive shutdown-time RPC state transfers.
- 🗑️ **Zero network transfer during shutdown**: Eliminated shutdown-time RPC transfers to peers (0 bytes vs. MBs previously). State is now proactively maintained in Olric's distributed map throughout the node lifecycle.
- 💾 **Reduced GC allocations**: GC allocations during shutdown reduced from 10s of MBs to < 1MB for typical scenarios (90%+ reduction), significantly reducing garbage collection pressure.
- 🔍 **Query-based rebalancing**: Implemented efficient `GetActorsByOwner()` and `GetGrainsByOwner()` methods using Olric's distributed map queries, enabling fast rebalancing when nodes leave the cluster without requiring shutdown-time state transfers.

##### 📊 Ownership Tracking
- 📊 **Automatic ownership metadata**: Added `owner_node`, `created_at`, and `updated_at` fields to Actor and Grain protobuf messages. Ownership is automatically tracked and updated during actor/grain creation and migration, providing complete lifecycle visibility.
- 🔄 **Ownership updates during migration**: Ownership metadata is automatically updated when actors/grains are relocated to new nodes, ensuring accurate tracking throughout the cluster lifecycle.

##### 🧹 Code Cleanup & Architecture
- 🗑️ **Removed shutdown-time RPC persistence**: Eliminated RPC calls from shutdown flow, simplifying the shutdown process and removing network bottlenecks.
- 💾 **Leveraged Olric's built-in rebalancing**: Rebalancing now uses Olric's distributed map queries instead of BoltDB peer state storage, eliminating redundant storage and improving scalability.
- ⚙️ **Proactive state replication**: Actors and grains are now proactively replicated to Olric's distributed map during creation, eliminating the need for expensive shutdown-time state transfers.


##### ⚙️ Replication Configuration Best Practices
When enabling actor/grain relocation, proper replication configuration is critical to ensure **no data loss** and provide the **best relocation experience**. The following guidelines should be followed:

**Minimum Configuration for Production:**
- **`ReplicaCount`**: Should be set to at least **3** for proper fault tolerance. This ensures data survives the failure of at least one node.
- **`WriteQuorum`**: Should be set to **majority of replicas** = `(ReplicaCount / 2) + 1`. For 3 replicas, this is **2**. This ensures writes are acknowledged by a majority, preventing data loss.
- **`ReadQuorum`**: Should be set to **majority of replicas** = `(ReplicaCount / 2) + 1`. For 3 replicas, this is **2**. This ensures reads always see the latest written data.

**Example Configuration:**
```go
cfg := actor.NewClusterConfig().
    WithReplicaCount(3).        // 3 replicas for fault tolerance
    WithWriteQuorum(2).         // Majority: (3/2) + 1 = 2
    WithReadQuorum(2).          // Majority: (3/2) + 1 = 2
    // ... other config
```

**Why These Settings Matter:**
- **No Data Loss**: With majority quorum (2 out of 3), data is safely replicated to at least 2 nodes. Even if one node fails, the data remains available on another node.
- **Consistent Reads**: Majority read quorum ensures you always read from a node that has the latest data, preventing stale reads during rebalancing.
- **Smooth Relocation**: When a node leaves, the remaining nodes (2 out of 3) still form a quorum, allowing relocation to complete without blocking.
- **Network Partition Resilience**: Majority quorum prevents split-brain scenarios where isolated nodes could serve conflicting data.

**Scaling Guidelines:**
- **3-node cluster**: `ReplicaCount=3, WriteQuorum=2, ReadQuorum=2` (recommended minimum)
- **5-node cluster**: `ReplicaCount=3, WriteQuorum=2, ReadQuorum=2` (can tolerate 1 node failure)
- **7-node cluster**: `ReplicaCount=5, WriteQuorum=3, ReadQuorum=3` (can tolerate 2 node failures)

**Warning**: Using `WriteQuorum=1` or `ReadQuorum=1` with relocation enabled risks **data loss** if a node fails during or immediately after a write operation, as the data may not yet be replicated to other nodes.

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
