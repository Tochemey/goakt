# GoAkt Distributed Data (CRDTs) — Architecture

---

## Table of Contents

1. [Motivation](#1-motivation)
2. [Design Principles](#2-design-principles)
3. [Package Layout](#3-package-layout)
4. [Core CRDT Types](#4-core-crdt-types)
5. [Architecture Overview](#5-architecture-overview)
6. [Replicator as a Cluster System Actor](#6-replicator-as-a-cluster-system-actor)
7. [Replication via TopicActor](#7-replication-via-topicactor)
8. [Anti-Entropy Protocol](#8-anti-entropy-protocol)
9. [API Design](#9-api-design)
10. [Consistency Model](#10-consistency-model)
11. [Cluster Integration](#11-cluster-integration)
12. [Multi-Datacenter Support](#12-multi-datacenter-support)
13. [Protobuf Wire Format](#13-protobuf-wire-format)
14. [GC and Memory Efficiency](#14-gc-and-memory-efficiency)
15. [Observability](#15-observability)
16. [Performance Considerations](#16-performance-considerations)

---

## 1. Motivation

GoAkt provides strong primitives for building distributed systems — actors, clustering, remoting, and grains. **Replicated data structures that actors on different nodes can read and write without coordination** fill a gap that otherwise requires routing through a single actor, using Olric DMaps directly, or bringing in external stores.

**Conflict-free Replicated Data Types (CRDTs)** are data structures that can be updated independently on any node and are guaranteed to converge to a consistent state — with no coordination, no locks, and no consensus rounds.

### What This Enables

| Use Case                                   | Without CRDTs                             | With CRDTs                                                |
|--------------------------------------------|-------------------------------------------|-----------------------------------------------------------|
| Distributed counter (rate limits, metrics) | Single actor bottleneck or external store | PN-Counter updated locally, converges automatically       |
| Cluster-wide actor/session registry        | Quorum writes to Olric DMap               | OR-Set replicated via pub/sub, always available for reads |
| Feature flags / configuration              | External config service                   | LWW-Register updated from any node                        |
| Pub/sub topic membership                   | Centralized topic actor                   | OR-Set of subscribers, partition-tolerant                 |
| Shopping cart / collaborative editing      | Conflict resolution in user code          | OR-Map with automatic merge semantics                     |

### Why CRDTs Fit the Actor Model

- **Actors encapsulate state** — CRDTs define how that state merges across replicas.
- **Actors communicate via messages** — CRDT delta propagation maps directly to message passing.
- **Actors are location-transparent** — CRDT replication works identically whether the peer is local or remote.
- **Actors tolerate partitions** — CRDTs are designed for exactly this: progress during partitions, convergence after healing.

GoAkt's architecture — with Hashicorp Memberlist for gossip, a per-node actor system, and cluster-wide pub/sub via the TopicActor — is ideally suited to host a CRDT replication layer.

---

## 2. Design Principles

| Principle                              | What It Means                                                                                                                                                                                                                                                                                                                               |
|----------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Zero import cycles**                 | The `crdt` package contains only pure data types and config — no dependency on `actor`, `internal`, or any GoAkt package. The `actor` package imports `crdt/` as a leaf dependency (for `crdt.Option` and CRDT types). The Replicator actor lives in `actor/` alongside the other system actors. `crdt/` never imports `actor/`. No cycles. |
| **No breaking changes**                | One additive method (`Replicator() *PID`) on `ActorSystem`. No existing methods or signatures change. CRDT is opt-in via `ClusterConfig.WithCRDT(...)` — zero overhead when not enabled.                                                                                                                                                    |
| **Reuse existing infrastructure**      | Delta dissemination uses the existing TopicActor pub/sub system — no custom gossip layer. Peer discovery, remote fan-out, message deduplication, and subscriber lifecycle are already handled.                                                                                                                                              |
| **Actor-native**                       | CRDTs are accessed through a dedicated Replicator actor. Interaction is via `Tell` / `Ask` — no new communication paradigm.                                                                                                                                                                                                                 |
| **Delta-based**                        | Only state deltas are replicated, not full values. This keeps bandwidth proportional to change rate, not data size.                                                                                                                                                                                                                         |
| **CRDT-as-value**                      | CRDT types are immutable values. Every mutation returns a new value plus a delta. No hidden shared state.                                                                                                                                                                                                                                   |
| **GC-friendly**                        | CRDT types minimize heap allocations: reuse slices and maps across merge operations, avoid closures in hot paths, prefer value types over pointers where practical. See [Section 14](#14-gc-and-memory-efficiency).                                                                                                                         |
| **Local-first, coordination-optional** | All operations are local-first by default. Optional `WriteTo` / `ReadFrom` coordination (`Majority`, `All`) is available when stronger guarantees are needed — matching GoAkt's existing quorum model.                                                                                                                                      |
| **Composable**                         | Complex CRDTs (maps, sets of registers) are built from simpler ones. The merge function composes.                                                                                                                                                                                                                                           |

---

## 3. Package Layout

```
goakt/
├── crdt/                          ← PUBLIC: pure CRDT data types
│   ├── crdt.go                    ← ReplicatedData interface
│   ├── key.go                     ← Key, DataType, and factory functions
│   ├── gcounter.go                ← GCounter
│   ├── pncounter.go               ← PNCounter
│   ├── lww_register.go            ← LWWRegister
│   ├── or_set.go                  ← ORSet (with Compact method)
│   ├── or_map.go                  ← ORMap
│   ├── flag.go                    ← Flag
│   ├── mv_register.go             ← MVRegister
│   ├── consistency.go             ← Coordination type (Majority, All, DCMajority, DCAll)
│   ├── config.go                  ← Option, WithAntiEntropyInterval, WithRole, etc.
│   └── messages.go                ← Update, Get, Subscribe, Changed message types
│
├── actor/
│   └── replicator.go              ← Replicator system actor
│
├── internal/
│   ├── ddata/
│   │   ├── snapshot.go            ← BoltDB snapshot storage
│   │   ├── crdt_codec.go          ← CRDT encode/decode for protobuf wire format
│   │   └── crdt_serializer.go     ← Proto+CBOR composite serializer for CRDT values
│   ├── codec/
│   │   └── codec.go               ← CRDTKey encode/decode helpers
│   └── metric/
│       └── replicator_metric.go   ← OpenTelemetry instruments
│
├── protos/internal/
│   └── crdt.proto                 ← Wire format definitions
│
└── actor/
    ├── cluster_config.go          ← WithCRDT(...crdt.Option)
    ├── actor_system.go            ← Replicator() *PID on ActorSystem interface
    └── guardrails.go              ← replicatorType name constant
```

### Dependency Flow (No Cycles)

```
                    ┌─────────────┐
                    │   crdt/     │  ← pure types + config, ZERO GoAkt imports
                    │  (public)   │
                    └──────▲──────┘
                           │ imports
               ┌───────────┴───────────┐
               │                       │
    ┌──────────┴──────┐          ┌─────┴───────────┐
    │  actor/         │          │  user code      │
    │  (imports crdt/ │          │  (imports crdt/ │
    │   for Option +  │          │   + actor/)     │
    │   Replicator    │          │                 │
    │   lives here)   │          │                 │
    └─────────────────┘          └─────────────────┘
```

- `crdt/` imports **nothing** from GoAkt. It is a self-contained library with pure CRDT types, message structs, and configuration options.
- `actor/` imports `crdt/` for the `crdt.Option` type and CRDT data types. The Replicator system actor lives in `actor/` alongside the other system actors.
- User code imports both `crdt/` (for types and messages) and `actor/` (for Tell/Ask/ActorSystem).

---

## 4. Core CRDT Types

### Foundation Types

| Type            | Description                                                                              | Merge Rule                                 | Primary Use Cases                                       |
|-----------------|------------------------------------------------------------------------------------------|--------------------------------------------|---------------------------------------------------------|
| **GCounter**    | Grow-only counter. Each node maintains its own increment slot. Value = sum of all slots. | Per-node max                               | Monotonic metrics, event counts                         |
| **PNCounter**   | Positive-negative counter. Two GCounters: one for increments, one for decrements.        | Component-wise GCounter merge              | Rate limiters, gauges, inventory levels                 |
| **LWWRegister** | Last-writer-wins register. Stores a single `any` value with a timestamp.                 | Highest timestamp wins                     | Configuration, feature flags, actor metadata            |
| **ORSet**       | Observed-remove set. Stores `any` elements. Supports add and remove without conflicts.   | Union of observed elements, causal removal | Session tracking, topic subscriptions, actor registries |

### Composition Types

| Type           | Description                                                                                  | Merge Rule                                   | Primary Use Cases                                      |
|----------------|----------------------------------------------------------------------------------------------|----------------------------------------------|--------------------------------------------------------|
| **ORMap**      | Map where keys are `any` and values are `ReplicatedData` CRDTs.                              | Key-wise OR-Set merge + per-value CRDT merge | Shopping carts, user profiles, distributed config maps |
| **Flag**       | A boolean that can only transition from `false` to `true`.                                   | Logical OR                                   | One-time coordination signals, feature activation      |
| **MVRegister** | Multi-value register. Stores `any` values. Concurrent writes are preserved, not overwritten. | Union of concurrent values                   | Conflict-visible state where user resolution is needed |

### CRDT Interface

All types implement a common interface defined in `crdt/crdt.go`:

```go
type ReplicatedData interface {
    Merge(other ReplicatedData) ReplicatedData
    Delta() ReplicatedData
    ResetDelta()
    Clone() ReplicatedData
}
```

### Typed Keys

CRDT keys carry the data type for serialization. Defined in `crdt/key.go`:

```go
type Key struct {
    id       string
    dataType DataType
}
```

Factory functions create keys with the correct data type:

```go
func GCounterKey(id string) Key    { ... }
func PNCounterKey(id string) Key   { ... }
func LWWRegisterKey(id string) Key { ... }
func ORSetKey(id string) Key       { ... }
func ORMapKey(id string) Key       { ... }
func FlagKey(id string) Key        { ... }
func MVRegisterKey(id string) Key  { ... }
```

---

## 5. Architecture Overview

### Per-Node Replicator Model

Every node in the cluster runs its own Replicator actor. There is **no central coordinator**, **no cluster singleton**, and **no custom gossip protocol** — each Replicator owns its node's local CRDT store and communicates with peer Replicators on other nodes via GoAkt's existing TopicActor pub/sub system.

### The Shared Subscription Model

All Replicators subscribe to a single well-known topic (`goakt.crdt.deltas`) via GoAkt's TopicActor at startup.

When any Replicator updates a key, it encodes the delta as a protobuf message and publishes it to this shared topic. Because every Replicator on every node is subscribed to the same topic, they all receive the delta automatically. The TopicActor handles peer discovery, remote TCP delivery, message deduplication, and subscriber cleanup.

```
    Node A                         Node B                        Node C
    ------                         ------                        ------

| [Replicator A]               [Replicator B]               [Replicator C]
  CRDT Store                   CRDT Store                   CRDT Store
      |                            |                            |
      |--- subscribe ------------->|                            |
      |    "goakt.crdt.deltas"     |--- subscribe ------------->|
      |                            |    "goakt.crdt.deltas"     |--- subscribe
      |                            |                            |    "goakt.crdt.deltas"
      |                            |                            |
      |  All three Replicators subscribe to the same topic      |
      |  at startup. When any one publishes a delta,            |
      |  the others receive it via TopicActor fan-out.          |
```

### Data Flow

**Step 1 — Subscription at startup.** When a Replicator starts (in `PostStart`), it subscribes to `goakt.crdt.deltas` via the TopicActor.

**Step 2 — Local update.** A user actor sends an `Update` message to its local Replicator. The Replicator applies the mutation, extracts the delta, encodes it as a protobuf `CRDTDelta`, and publishes it to the shared topic.

**Step 3 — Dissemination.** The TopicActor delivers the protobuf delta to all subscribers — the Replicator on every other node.

**Step 4 — Remote merge.** Each peer Replicator decodes the delta, ignores it if the origin is itself, and merges it into its local store. If the merge changes the value, it notifies any local user actors watching that key.

### Role-Based Scoping

Not all nodes need to participate in CRDT replication. The Replicator can be scoped to nodes tagged with a specific role:

```go
crdt.WithRole("cache-nodes")
```

When a role is set:

- The Replicator is only spawned on nodes whose `ClusterConfig.WithRoles(...)` includes the matching role.
- Nodes without the role do not run a Replicator and do not subscribe to CRDT topics.

When no role is set (default), all cluster nodes participate.

### Key Components

| Component            | Location            | Responsibility                                                                                                                                                                                                       |
|----------------------|---------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **CRDT Types**       | `crdt/` (public)    | Pure data structures with merge semantics. No actor dependency.                                                                                                                                                      |
| **Replicator Actor** | `actor/`            | Per-node system actor. Subscribes to `goakt.crdt.deltas` at startup. On local update, encodes and publishes delta. On receiving peer delta, decodes, merges into local store. Notifies local user actors of changes. |
| **CRDT Store**       | `actor/`            | In-memory `map[string]crdt.ReplicatedData` held inside the Replicator. Owned exclusively by the Replicator — no concurrent access.                                                                                   |
| **TopicActor**       | `actor/` (existing) | Existing cluster pub/sub. All Replicators subscribe to `goakt.crdt.deltas` through it. Handles cross-node delivery, serialization, deduplication, and subscriber lifecycle. No changes required.                     |
| **CRDT Messages**    | `crdt/` (public)    | Plain Go structs (`Update`, `Get`, `Subscribe`, `Changed`) that user actors send to their local Replicator via Tell/Ask.                                                                                             |

---

## 6. Replicator as a Cluster System Actor

### How It Fits Into the Existing System

The Replicator follows the same pattern as GoAkt's existing system actors (`GoAktDeathWatch`, `GoAktRebalancer`, `GoAktDeadletter`):

1. A `nameType` constant `replicatorType` maps to the reserved name `"GoAktReplicator"`.
2. `spawnReplicator(ctx)` on `actorSystem` creates the actor — conditionally, only when cluster mode is enabled, CRDT is configured, and role filtering passes.
3. The Replicator PID is stored as `x.replicator *PID` on `actorSystem`.
4. It is spawned as a **child of the system guardian** with `asSystem()`, `WithLongLived()`, and a supervisor that restarts on panic.

Every participating node spawns its own independent Replicator. There is no leader election for CRDT — just N identical actors at the same reserved name on N nodes, coordinating through the TopicActor pub/sub.

### Enabling CRDT via ClusterConfig

```go
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(disco).
    WithCRDT(
        crdt.WithAntiEntropyInterval(30 * time.Second),
        crdt.WithPruneInterval(5 * time.Minute),
        crdt.WithTombstoneTTL(24 * time.Hour),
        crdt.WithRole("cache-nodes"),
    )

system, err := actor.NewActorSystem(
    "my-system",
    actor.WithCluster(clusterConfig),
)
```

When `WithCRDT(...)` is called on `ClusterConfig`:

- A `crdtConfig` field is set on the config struct (non-nil signals CRDT is enabled).
- During `startCluster()`, after the cluster joins, `spawnReplicator(ctx)` is called.
- If `WithCRDT` is not called, no Replicator is spawned — zero overhead.

### Accessing the Replicator PID

System actors are not accessible via `ActorOf()`. The Replicator PID is exposed through the `ActorSystem` interface:

```go
Replicator() *PID  // returns nil if CRDT replication is not enabled
```

### Using the Replicator from an Actor

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    replicator := ctx.ActorSystem().Replicator()
    if replicator == nil {
        return
    }

    counter := crdt.PNCounterKey("request-count")
    err := actor.Tell(ctx, replicator, &crdt.Update{
        Key:     counter,
        Initial: crdt.NewPNCounter(),
        Modify:  func(current crdt.ReplicatedData) crdt.ReplicatedData {
            return current.(*crdt.PNCounter).Increment(nodeID, 1)
        },
    })
}
```

### Why a Per-Node Actor?

1. **Serialized access** — Each node's CRDT store is modified only inside that node's Replicator's `Receive`, eliminating concurrent access without locks.
2. **Backpressure** — The Replicator's mailbox naturally throttles writes from local actors.
3. **Supervision** — If a Replicator crashes, the system guardian restarts it and state is rebuilt from peer Replicators via anti-entropy.
4. **Observability** — Standard actor metrics (mailbox depth, processing time) apply directly to each node's Replicator.
5. **Consistency with GoAkt idioms** — Users interact with CRDTs the same way they interact with any actor: Tell/Ask to a PID.

---

## 7. Replication via TopicActor

### What the TopicActor Provides

The TopicActor (`actor/topic_actor.go`) is an existing GoAkt system actor that provides cluster-wide pub/sub:

1. **Topic subscriptions** — any actor can subscribe to a named topic.
2. **Local + remote delivery** — when a message is published, the TopicActor delivers it to all local subscribers AND sends it to the TopicActor on every peer node.
3. **Message deduplication** — tracks `(senderID, topic, messageID)` tuples to prevent double delivery.
4. **Subscriber lifecycle** — uses death watch to automatically remove terminated subscribers.

### How the Replicator Uses It

All Replicators subscribe to a single well-known topic: `goakt.crdt.deltas`. **Single topic, all keys.** Every CRDT delta for every key flows through one topic. The delta message itself carries the key ID, data type, origin node, and serialized state — the receiving Replicator uses the key ID to route the delta to the correct entry in its local store.

### Handling a Local Update

When a user actor sends an `Update`, the Replicator:

1. Rejects updates to tombstoned keys.
2. Gets or creates the CRDT value.
3. Applies the user's mutation and extracts the delta.
4. If coordination is set (`WriteTo`), sends the delta directly to peers via RemoteTell.
5. Encodes the delta as protobuf and publishes to the shared CRDT topic.
6. Notifies local user actors watching this key.
7. If cross-DC replication is enabled, appends the protobuf delta to the pending buffer.

### Handling a Peer Delta

When the TopicActor delivers a `CRDTDelta`:

1. Ignores it if the origin is the local node (TopicActor delivers to local subscribers too).
2. Rejects deltas for tombstoned keys.
3. If the key doesn't exist locally, the delta serves as the initial value.
4. Otherwise, merges the remote delta into the local value using the type's `Merge` function.
5. Notifies local user actors watching this key.

### Notifying Local User Actors

When a user actor subscribes to changes on a key (via `crdt.Subscribe`), the Replicator tracks the subscriber PID and sets a death watch. On any change — local or remote — it sends a `crdt.Changed` message directly to each watcher. Dead watchers are pruned from the list.

### Summary

| What the Replicator does                | What the TopicActor does                            |
|-----------------------------------------|-----------------------------------------------------|
| Owns the local CRDT store               | Maintains topic subscriptions                       |
| Applies mutations and merges            | Delivers messages to local subscribers              |
| Extracts and publishes deltas           | Sends messages to peer TopicActors via TCP remoting |
| Tracks local user actor watchers        | Deduplicates messages across the cluster            |
| Runs anti-entropy (Section 8)           | Cleans up terminated subscribers via death watch    |
| Handles consistency levels (Section 10) | Handles serialization via pluggable serializers     |

---

## 8. Anti-Entropy Protocol

TopicActor pub/sub is the primary dissemination path, but messages can be lost (network partition, node restart, TopicActor crash and recovery). Anti-entropy is a periodic safety net that ensures all nodes eventually converge.

### How It Works

```
Every antiEntropyInterval (default: 30s):
    1. Replicator selects one random peer from the cluster peer list.
    2. Sends a CRDTDigest (map of key → version) to the peer's Replicator via RemoteTell.
    3. Peer compares the digest with its local store.
    4. For each key where the peer is ahead (higher version or key the sender lacks),
       the peer sends back a CRDTFullState with the full CRDT value.
    5. The sender merges any received full state into its local store.
```

Anti-entropy is **one-directional per round**: A sends its digest to B, and B responds with keys where B is ahead. A does not receive keys where A is ahead of B in that round. The reverse repair happens when B randomly selects A on a future tick.

Anti-entropy uses **direct RemoteTell between Replicators** (not the TopicActor) because it is a point-to-point synchronization protocol, not a broadcast.

### When Anti-Entropy Fires

- **Periodically** — every `antiEntropyInterval` (configurable, default 30s), via GoAkt's `Schedule` API.
- **On supervisor restart** — if the Replicator crashes and is restarted, it starts with an empty store (or restored from snapshot) and relies on anti-entropy to rebuild from peers on subsequent ticks.

### Anti-Entropy Messages (Internal)

```protobuf
message CRDTDigestEntry {
    CRDTKey key = 1;
    uint64 version = 2;
}

message CRDTDigest {
    repeated CRDTDigestEntry entries = 1;
}

message CRDTFullStateEntry {
    CRDTKey key = 1;
    CRDTData data = 2;
}

message CRDTFullState {
    repeated CRDTFullStateEntry entries = 1;
}
```

The Replicator maintains a per-key version counter (`map[string]uint64`) that increments on every local update or remote merge. The digest carries these versions so the peer can identify divergent keys without exchanging full state.

---

## 9. API Design

### 9.1 Configuration

```go
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(disco).
    WithCRDT(
        crdt.WithAntiEntropyInterval(30 * time.Second),
        crdt.WithPruneInterval(5 * time.Minute),
        crdt.WithTombstoneTTL(24 * time.Hour),
        crdt.WithRole("cache-nodes"),
        crdt.WithCoordinationTimeout(5 * time.Second),
        crdt.WithSnapshotInterval(5 * time.Minute),
        crdt.WithSnapshotDir("/var/data/crdt"),
    )
```

### 9.2 Defining Keys

```go
var (
    requestCount   = crdt.PNCounterKey("request-count")
    activeSessions = crdt.ORSetKey("active-sessions")
    featureFlag    = crdt.LWWRegisterKey("dark-mode")
)
```

### 9.3 Writing Data

```go
// Fire-and-forget — local + pub/sub only.
err := actor.Tell(ctx, replicator, &crdt.Update{
    Key:     requestCount,
    Initial: crdt.NewPNCounter(),
    Modify:  func(current crdt.ReplicatedData) crdt.ReplicatedData {
        return current.(*crdt.PNCounter).Increment(nodeID, 1)
    },
})
```

```go
// Wait for majority acknowledgment.
resp, err := actor.Ask(ctx, replicator, &crdt.Update{
    Key:     activeSessions,
    Initial: crdt.NewORSet(),
    Modify:  func(current crdt.ReplicatedData) crdt.ReplicatedData {
        return current.(*crdt.ORSet).Add(nodeID, sessionID)
    },
    WriteTo: crdt.Majority,
}, 5*time.Second)
```

### 9.4 Reading Data

```go
// Read from local store (default — fast, eventually consistent).
resp, err := actor.Ask(ctx, replicator, &crdt.Get{
    Key: requestCount,
}, 5*time.Second)
if getResp, ok := resp.(*crdt.GetResponse); ok && getResp.Data != nil {
    count := getResp.Data.(*crdt.PNCounter).Value()
}
```

```go
// Read with majority coordination — queries peers and merges.
resp, err := actor.Ask(ctx, replicator, &crdt.Get{
    Key:      activeSessions,
    ReadFrom: crdt.Majority,
}, 5*time.Second)
```

### 9.5 Subscribing to Changes

```go
func (a *MyActor) PreStart(ctx *actor.Context) error {
    replicator := ctx.ActorSystem().Replicator()
    return actor.Tell(ctx, replicator, &crdt.Subscribe{
        Key: activeSessions,
    })
}

func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *crdt.Changed:
        if msg.Data != nil {
            sessions := msg.Data.(*crdt.ORSet).Elements()
        }
    }
}
```

### 9.6 Deleting Data

```go
err := actor.Tell(ctx, replicator, &crdt.Delete{
    Key: activeSessions,
})
```

Deletion removes the key from the local store and publishes a `CRDTTombstone` to the shared topic via TopicActor. Peer Replicators that receive the tombstone remove the key from their stores. Tombstones are retained for a configurable TTL (`WithTombstoneTTL`, default 24h) and pruned periodically (`WithPruneInterval`, default 5m). While a tombstone is active, updates and deltas for that key are rejected to prevent resurrection.

---

## 10. Consistency Model

### Default Behavior: Local-First

Every operation is **local-first** by default:

- **Writes** apply the mutation to the local CRDT store and publish the delta via TopicActor. The Replicator returns immediately — no network wait.
- **Reads** return the local value from the store immediately.

This is eventually consistent, with convergence bounded by TopicActor's delivery latency (typically sub-second in a healthy cluster).

### Optional Coordination: `WriteTo` and `ReadFrom`

The `Update` and `Get` message types accept `WriteTo` and `ReadFrom` for optional coordination. The Replicator implements `Majority` and `All` against **local cluster peers** (`cluster.Peers()`).

```go
type Coordination int

const (
    Majority   Coordination = iota + 1  // local cluster ⌊N/2⌋ + 1 nodes
    All                                  // all local cluster nodes
    DCMajority                           // reserved (not implemented)
    DCAll                                // reserved (not implemented)
)
```

`DCMajority` and `DCAll` are defined as constants but are **not wired** in the Replicator. They do not trigger any cross-DC peer fan-out; use `Majority` / `All` for intra-cluster guarantees.

#### WriteTo

| `WriteTo`   | Behavior                                                                                                                                           | Use When                                          |
|-------------|----------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------|
| *(not set)* | Apply locally + publish delta via TopicActor. Return immediately.                                                                                  | Counters, metrics, best-effort tracking           |
| `Majority`  | Apply locally + send delta directly to ⌊N/2⌋ + 1 peers via RemoteTell. Delta is also published via TopicActor for remaining peers.                 | Session state, membership sets                    |
| `All`       | Apply locally + send delta directly to all peers via RemoteTell.                                                                                   | Configuration changes requiring global visibility |

#### ReadFrom

| `ReadFrom`  | Behavior                                                                 | Use When                          |
|-------------|--------------------------------------------------------------------------|-----------------------------------|
| *(not set)* | Return local value immediately.                                          | High-throughput reads, counters   |
| `Majority`  | Query ⌊N/2⌋ + 1 peers via RemoteAsk, merge all results with local value. | Accurate membership checks        |
| `All`       | Query all peers via RemoteAsk, merge all results with local value.       | Audit, consistency-critical reads |

### How Coordination Interacts with TopicActor

- **Default (no coordination)** — the delta is published to `goakt.crdt.deltas`. Propagation happens asynchronously.
- **`WriteTo: Majority` / `All`** — the Replicator sends the delta directly to peer Replicators via RemoteTell and waits for acknowledgments. The delta is *also* published via TopicActor so that peers not in the direct set receive it.
- **`ReadFrom: Majority` / `All`** — the Replicator sends RemoteAsk to peer Replicators, collects their local values, merges them with the local value, and returns the merged result.

### Consistency Guarantee

When `ReadFrom: Majority` + `WriteTo: Majority` are used together, the system provides **strong eventual consistency with read-your-writes** guarantees. Without coordination, the system is **eventually consistent** with convergence bounded by TopicActor's delivery latency.

---

## 11. Cluster Integration

### Lifecycle (Per Node)

```
ActorSystem.Start() [on each node]
    │
    ├── Cluster joins (Memberlist)
    │
    ├── TopicActor starts (existing system actor)
    │
    ├── startCluster() checks clusterConfig.crdtConfig != nil
    │       │
    │       ├── spawnReplicator(ctx) checks role filter (skip if node's roles don't match)
    │       ├── Spawns GoAktReplicator as system guardian child
    │       ├── Replicator subscribes to "goakt.crdt.deltas" via TopicActor
    │       ├── Starts anti-entropy schedule
    │       ├── Starts prune schedule
    │       ├── Starts snapshot schedule (if configured)
    │       └── Starts cross-DC flush and anti-entropy schedules (if configured)
    │
    ├── ... normal actor system operation ...
    │
    └── ActorSystem.Stop()
            │
            ├── Replicator cancels all schedules
            ├── Replicator persists final snapshot (if configured)
            └── Replicator actor stops
```

### Node Join

When a new node joins the cluster:

1. The new node's actor system starts and spawns its own Replicator with an empty CRDT store (or restored from a BoltDB snapshot if persistence is configured).
2. Memberlist detects the join and emits a `NodeJoined` event on all existing nodes.
3. Existing TopicActors now include the new node in their peer set — subsequent publishes to `goakt.crdt.deltas` reach the new Replicator automatically.
4. The new node catches up on existing state via anti-entropy on the first scheduled tick.

### Node Departure

When a node leaves (graceful or crash):

1. Memberlist detects the departure and emits a `NodeLeft` event.
2. The TopicActor on remaining nodes removes the departed node from its peer set.
3. CRDT state is **not lost** — it exists on all other replicas.
4. Node-specific slots in GCounters and PNCounters remain valid (they represent historical contributions).

### Relationship with Olric

The CRDT store is **independent of Olric**. Olric continues to manage the actor/grain registry with strong consistency. CRDTs provide a complementary eventually-consistent data layer.

---

## 12. Multi-Datacenter Support

CRDTs are inherently well-suited for multi-DC deployment because they tolerate arbitrary network delays and partitions.

### 12.1 Topology: One Cluster per Datacenter

```
┌────────────────────────────────────────────────────────────────────┐
│                       Global Control Plane                         │
│                  (NATS JetStream or etcd)                          │
│                                                                    │
│  - DataCenterRecord registry (name, region, zone, labels,          │
│    endpoints)                                                      │
│  - Liveness via heartbeat + lease TTL                              │
│  - State machine: REGISTERED -> ACTIVE -> DRAINING -> INACTIVE     │
└────────────────────────────────────────────────────────────────────┘
        ↑ register / heartbeat            ↑ register / heartbeat
        ↓ watch / list active             ↓ watch / list active
┌─────────────────────────────────┐ ┌─────────────────────────────────┐
│ Datacenter: dc-west             │ │ Datacenter: dc-east             │
│                                 │ │                                 │
│ ┌─────────────────────────────┐ │ │ ┌─────────────────────────────┐ │
│ │ Cluster (Olric + Memberlist)│ │ │ │ Cluster (Olric + Memberlist)│ │
│ │ Discovery: Consul/K8s/     │ │ │ │ Discovery: Consul/K8s/       │ │
│ │   etcd/NATS/DNS-SD          │ │ │ │   etcd/NATS/DNS-SD          │ │
│ │ Replicator (CRDTs)*         │ │ │ │ Replicator (CRDTs)*         │ │
│ │ TopicActor (pub/sub)        │ │ │ │ TopicActor (pub/sub)        │ │
│ └─────────────────────────────┘ │ │ └─────────────────────────────┘ │
│                                 │ │                                 │
│ DC Controller (leader only)     │ │ DC Controller (leader only)     │
└─────────────────────────────────┘ └─────────────────────────────────┘
```

\* If `crdt.WithRole(...)` is set, the Replicator is spawned only on nodes whose cluster roles include that name.

Each datacenter runs an **independent** GoAkt cluster. Discovery is DC-scoped. The control plane bridges DCs: each DC's cluster leader registers the DC's metadata and remoting endpoints, renews liveness leases, and caches the set of active DCs.

### 12.2 Two-Layer Communication Model

| Layer                | Mechanisms                                                                                         | Latency                  | Scope                     |
|----------------------|----------------------------------------------------------------------------------------------------|--------------------------|---------------------------|
| **Intra-DC (Cluster)** | Memberlist gossip, Olric DMap, TopicActor pub/sub, Replicator delta dissemination, anti-entropy, coordinated reads/writes | Sub-ms to low ms        | Nodes within one cluster  |
| **Cross-DC (Remoting)** | Control plane registry, DC endpoint discovery, direct remoting RPCs, cross-DC batch flush, cross-DC anti-entropy | Tens to hundreds of ms  | All active datacenters    |

### 12.3 Cross-DC CRDT Replication

Cross-DC replication is handled inside the **same Replicator system actor**. The cluster leader flushes its pending delta/tombstone buffer in a `CRDTDeltaBatch` over remoting to remote DC endpoints. There is no separate bridge actor.

#### Architecture

```
    DC-West                                         DC-East
┌──────────────────────────┐                 ┌──────────────────────────┐
│                          │                 │                          │
│ Replicator --> TopicActor│                 │ Replicator --> TopicActor│
│ (per-node; optional role)│                 │ (per-node; optional role)│
│                          │                 │                          │
│ Leader Replicator        │  remoting       │ One endpoint per DC      │
│ - pending buffer +       │  CRDTDeltaBatch │ - receive batch via      │
│   periodic flush to      ├────────────────►│   RemoteTell             │
│   each remote DC         │                 │ - merge locally; DC-wide │
│                          │                 │  fan-out via anti-entropy│
│                          │                 │                          │
│ Cross-DC anti-entropy    │  digest         │ Same path in reverse     │
│ (optional schedule)      ├────────────────►│ (random remote DC)       │
└──────────────────────────┘                 └──────────────────────────┘
```

#### Intra-DC replication

1. User sends `crdt.Update` to the local Replicator.
2. Replicator applies the mutation, publishes `CRDTDelta` on `goakt.crdt.deltas` via TopicActor.
3. Peers in the same cluster merge and notify watchers.
4. Intra-DC anti-entropy runs on `AntiEntropyInterval` (default 30s) with random local peers.

#### Cross-DC replication (leader Replicator)

1. When `crdt.WithDataCenterReplication()` is enabled and `ClusterConfig.WithDataCenter(...)` supplies local DC metadata and a running DC controller, each Replicator that handles a local `Update` or `Delete` **appends** the protobuf delta or tombstone to its in-memory pending buffer.
2. On a scheduled tick (`DataCenterReplicationInterval`, default 5s), only the **cluster leader** builds a `CRDTDeltaBatch` from its own pending buffer, clears that buffer, and sends the batch via remoting to one reachable endpoint per remote DC (from `ActiveRecords()`), shuffling endpoints within a DC and using a per-attempt timeout (`DataCenterSendTimeout`, default 10s).
3. A Replicator that receives a batch **drops** batches whose `origin_dc` matches the local DC (echo avoidance), records cross-DC lag metrics, then merges each delta/tombstone into its local store only. There is no automatic republish to the CRDT TopicActor, so other nodes in the receiving DC converge through intra-DC anti-entropy.

Non-leader nodes do not flush their pending buffers. Their pending entries are retained until (if ever) that node becomes leader.

#### Cross-DC anti-entropy (optional)

If `crdt.WithDataCenterAntiEntropy()` is set and `DataCenterAntiEntropyInterval` > 0 (default 2m), the leader periodically picks a random remote DC and sends a `CRDTDigest` to a remote Replicator over remoting (with `DataCenterSendTimeout`). The remote responds with `CRDTFullState` via `Tell`, matching the intra-cluster anti-entropy response path.

Cross-DC anti-entropy ensures that all data eventually reaches every DC, including deltas that were written on non-leader nodes within the origin DC (since the leader's store contains all intra-DC data via TopicActor).

#### Cross-DC tombstone propagation

Tombstones are included in the same pending buffer as deltas and shipped in `CRDTDeltaBatch`. Keep `WithTombstoneTTL` long enough for batches and anti-entropy to propagate across WAN latency before expiry, to avoid resurrection from stale state.

### 12.4 Configuration

Cross-DC replication is **opt-in**. You must also configure multi-DC on the cluster (`WithDataCenter`) so the local `DataCenter` identity and DC controller are available.

```go
dcConfig := datacenter.NewConfig()
dcConfig.ControlPlane = natsControlPlane
dcConfig.DataCenter = datacenter.DataCenter{
    Name:   "dc-west",
    Region: "us-west-2",
    Zone:   "us-west-2a",
}

clusterCfg := actor.NewClusterConfig().
    WithDataCenter(dcConfig).
    WithRoles("crdt-node").
    WithCRDT(
        crdt.WithAntiEntropyInterval(30 * time.Second),
        crdt.WithTombstoneTTL(24 * time.Hour),
        crdt.WithRole("crdt-node"),
        crdt.WithDataCenterReplication(),
        crdt.WithDataCenterReplicationInterval(5 * time.Second),
        crdt.WithDataCenterAntiEntropy(),
        crdt.WithDataCenterAntiEntropyInterval(2 * time.Minute),
        crdt.WithDataCenterSendTimeout(10 * time.Second),
    )
```

If `WithDataCenter` is omitted or `WithDataCenterReplication()` is not set, behavior is intra-cluster only.

`crdt.WithRole` must align with `ClusterConfig.WithRoles` on each node: the Replicator is not spawned unless the node advertises that role. Ensure the cluster leader advertises the CRDT role if you rely on scheduled cross-DC flush or anti-entropy.

### 12.5 Failure Modes

| Failure                              | Behavior                                                                                                                                                                     |
|--------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **DC leader failover**               | The new cluster leader performs the next scheduled cross-DC flush. Cross-DC anti-entropy (if enabled) repairs missed batches.                                                |
| **Network partition between DCs**    | Each DC continues operating independently. CRDTs on both sides diverge but converge once the partition heals and batches/anti-entropy resume.                               |
| **Control plane unavailable**        | The leader cannot list remote DC endpoints; cross-DC flush skips until `ActiveRecords()` is healthy. Intra-DC replication is unaffected.                                    |
| **Single node failure within DC**    | Handled by intra-DC cluster membership and Replicator anti-entropy. No cross-DC impact.                                                                                     |
| **Stale DC cache**                   | When `FailOnStaleCache` is true, flush skips and increments `crdt.replicator.crossdc.stale.skip.count`; when false, best-effort routing uses the cached DC list.            |
| **Leader has no Replicator**         | If `crdt.WithRole` excludes the current leader, scheduled cross-DC flush and anti-entropy do not run. Intra-DC CRDT still works on nodes that have a Replicator.            |

---

## 13. Protobuf Wire Format

CRDT deltas and full states are serialized using Protocol Buffers. CRDT values (e.g., the contents of an `LWWRegister` or `ORSet` elements) are serialized using a composite Proto+CBOR serializer: `proto.Message` values are encoded with Protocol Buffers, while Go primitives and user-registered types are encoded with CBOR.

```protobuf
syntax = "proto3";
package internalpb;

enum CRDTDataType {
    CRDT_DATA_TYPE_UNSPECIFIED = 0;
    CRDT_DATA_TYPE_G_COUNTER = 1;
    CRDT_DATA_TYPE_PN_COUNTER = 2;
    CRDT_DATA_TYPE_LWW_REGISTER = 3;
    CRDT_DATA_TYPE_OR_SET = 4;
    CRDT_DATA_TYPE_OR_MAP = 5;
    CRDT_DATA_TYPE_FLAG = 6;
    CRDT_DATA_TYPE_MV_REGISTER = 7;
}

message CRDTKey {
    string id = 1;
    CRDTDataType data_type = 2;
}

message CRDTData {
    oneof type {
        GCounterData g_counter = 1;
        PNCounterData pn_counter = 2;
        LWWRegisterData lww_register = 3;
        ORSetData or_set = 4;
        ORMapData or_map = 5;
        FlagData flag = 6;
        MVRegisterData mv_register = 7;
    }
}

message GCounterData {
    map<string, uint64> state = 1;
}

message PNCounterData {
    GCounterData increments = 1;
    GCounterData decrements = 2;
}

message LWWRegisterData {
    bytes value = 1;
    string type_url = 2;
    int64 timestamp_nanos = 3;
    string node_id = 4;
}

message ORSetData {
    message ORSetDot { string node_id = 1; uint64 counter = 2; }
    message ORSetEntry { bytes element = 1; string type_url = 2; repeated ORSetDot dots = 3; }
    repeated ORSetEntry entries = 1;
    map<string, uint64> clock = 2;
}

message ORMapData {
    message ORMapEntry { bytes key = 1; string key_type_url = 2; CRDTData value = 3; }
    repeated ORMapEntry entries = 1;
    ORSetData key_set = 2;
}

message FlagData { bool enabled = 1; }

message MVRegisterData {
    message MVRegisterEntry { bytes value = 1; string type_url = 2; string node_id = 3; uint64 counter = 4; }
    repeated MVRegisterEntry entries = 1;
    map<string, uint64> clock = 2;
}

message CRDTDelta {
    CRDTKey key = 1;
    string origin_node = 2;
    CRDTData data = 3;
}

message CRDTDigestEntry { CRDTKey key = 1; uint64 version = 2; }
message CRDTDigest { repeated CRDTDigestEntry entries = 1; }

message CRDTFullStateEntry { CRDTKey key = 1; CRDTData data = 2; }
message CRDTFullState { repeated CRDTFullStateEntry entries = 1; }

message CRDTTombstone {
    CRDTKey key = 1;
    int64 deleted_at_nanos = 2;
    string deleted_by_node = 3;
}

message CRDTDeltaBatch {
    repeated CRDTDelta deltas = 1;
    repeated CRDTTombstone tombstones = 2;
    DataCenter origin_dc = 3;
    int64 sent_at_nanos = 4;
}
```

---

## 14. GC and Memory Efficiency

### General Rules

| Rule                                             | Rationale                                                                                                                                                |
|--------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Value receivers on small CRDTs**               | GCounter, PNCounter, Flag, and LWWRegister are small enough to live on the stack or be copied. Avoid pointer indirection.                                |
| **Pre-sized maps**                               | All `make(map[K]V)` calls use a capacity hint based on the known size of the merging peer's state.                                                       |
| **Reuse slices across merges**                   | `ORSet.Elements()` and `ORMap.Entries()` return slices backed by a reusable buffer held in the CRDT struct, reset via `buf[:0]` rather than reallocated. |
| **No closures in merge paths**                   | Merge and Delta methods do not allocate closures. The `Update.Modify` callback is user-supplied and outside the hot merge path.                          |
| **Avoid `interface{}` boxing in internal state** | CRDT internal fields use concrete types. The `ReplicatedData` interface is used only at API boundaries.                                                  |

### Per-Type Allocation Profile

| Type            | Internal State                                         | Merge Allocation                                                                                  |
|-----------------|--------------------------------------------------------|---------------------------------------------------------------------------------------------------|
| **GCounter**    | `map[string]uint64`                                    | In-place update of existing map entries; new allocation only for unseen node IDs.                 |
| **PNCounter**   | Two `GCounter` values                                  | Same as GCounter x 2.                                                                             |
| **LWWRegister** | `value any`, `timestamp int64`, `nodeID string`        | Zero allocation if incoming timestamp loses. One assignment if it wins.                           |
| **ORSet**       | `map[any][]dot`, `map[string]uint64`                   | Dot slices grow with concurrent adds. Compact periodically to reclaim dots from removed elements. |
| **ORMap**       | `ORSet` for keys + `map[any]ReplicatedData` for values | Per-key CRDT merge allocation + key-set OR-Set merge.                                             |
| **Flag**        | `bool`                                                 | Zero allocation.                                                                                  |

### Replicator-Level Efficiency

- **Pending delta/tombstone slices** are cleared with `[:0]` to retain backing arrays across flush cycles.
- **Origin DC proto** is allocated once in `PreStart` and reused on every cross-DC flush.
- **Atomic counters** (`atomic.Uint64`, `atomic.Int64`) are used for all OTel metric values to avoid data races with the OTel callback, which runs on a separate goroutine.
- **Watcher removal** uses swap-remove to avoid slice leaks.
- **Digest building** pre-allocates contiguous `CRDTDigestEntry` and `CRDTKey` buffers to reduce per-entry heap allocations.

---

## 15. Observability

The Replicator emits OpenTelemetry metrics registered in `internal/metric/replicator_metric.go`:

| Name (instrument)                         | Kind                         | Description                                                        |
|-------------------------------------------|------------------------------|--------------------------------------------------------------------|
| `crdt.replicator.store.size`              | Int64 observable gauge       | Number of CRDT keys in the local store                             |
| `crdt.replicator.merge.count`             | Int64 observable counter     | Merges applied from remote deltas                                  |
| `crdt.replicator.delta.publish.count`     | Int64 observable counter     | Deltas published to the CRDT TopicActor                            |
| `crdt.replicator.delta.receive.count`     | Int64 observable counter     | Deltas received via TopicActor                                     |
| `crdt.replicator.coordinated.write.count` | Int64 observable counter     | Coordinated write operations (`WriteTo` set)                       |
| `crdt.replicator.coordinated.read.count`  | Int64 observable counter     | Coordinated read operations (`ReadFrom` set)                       |
| `crdt.replicator.antientropy.count`       | Int64 observable counter     | Intra-DC anti-entropy rounds completed                             |
| `crdt.replicator.tombstone.count`         | Int64 observable gauge       | Active tombstones in the replicator                                |
| `crdt.replicator.crossdc.send.count`      | Int64 observable counter     | Cross-DC delta batches successfully sent (per remote DC attempt)   |
| `crdt.replicator.crossdc.receive.count`   | Int64 observable counter     | Cross-DC batches received and merged                               |
| `crdt.replicator.crossdc.replication.lag` | Int64 observable gauge (ns)  | Lag from last received batch (`sent_at` vs receive time)           |
| `crdt.replicator.crossdc.stale.skip.count`| Int64 observable counter     | Cross-DC flushes skipped due to stale DC cache                     |

---

## 16. Performance Considerations

### Memory

- All CRDT state is held in-memory. Memory usage grows linearly with the number of keys and the size of each CRDT value.
- ORSets and ORMaps maintain metadata (causal dots) that grows with the number of unique add operations. Periodic compaction prunes obsolete dots during the prune tick.
- Tombstones for deleted keys are retained for a configurable TTL (default: 24h) before pruning.

### Network

- **Delta-based replication** keeps bandwidth proportional to change rate, not data size.
- **TopicActor fan-out** sends to all peers on every publish. For large clusters (50+ nodes), this is acceptable because CRDT deltas are small and publish frequency is bounded by update rate.
- **Anti-entropy** is point-to-point and periodic (default: every 30s to one random peer), adding minimal network overhead.
- **Cross-DC batching** aggregates deltas over `DataCenterReplicationInterval` (default 5s) into a single `CRDTDeltaBatch`, reducing cross-WAN message count.

### CPU

- CRDT merge operations are computationally cheap (map merges, max comparisons).
- The Replicator processes updates sequentially (single actor), which simplifies correctness but bounds throughput to one core.

### Benchmark Targets

| Operation                            | Target Throughput | Notes                                                       |
|--------------------------------------|-------------------|-------------------------------------------------------------|
| Local update (default)               | > 500K ops/sec    | Single key, PNCounter                                       |
| Local read (default)                 | > 1M ops/sec      | Single key, any type                                        |
| Convergence (5 nodes)                | < 1 second        | 95th percentile (TopicActor is faster than periodic gossip) |
| Convergence (50 nodes)               | < 3 seconds       | 95th percentile                                             |
| PNCounter merge (converged)          | 0 allocs/op       | Benchmark gate                                              |
| ORSet merge (converged, 1K elements) | 0 allocs/op       | Benchmark gate                                              |
