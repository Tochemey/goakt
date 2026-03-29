# GoAkt Distributed Data (CRDTs) — Architecture

**Status:** Implemented (Phase 1 + Phase 2 + Phase 3)
**Target:** GoAkt v4.2.0
**Author:** GoAkt Core Team

---

## Table of Contents

1. [Motivation](#1-motivation)
2. [Design Principles](#2-design-principles)
3. [Package Layout and Dependency Graph](#3-package-layout-and-dependency-graph)
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
17. [Phased Delivery Plan](#17-phased-delivery-plan)

---

## 1. Motivation

GoAkt provides strong primitives for building distributed systems — actors, clustering, remoting, and grains. However, one important capability is missing: **replicated data structures that actors on different nodes can read and write without coordination**.

Today, if a GoAkt user wants a counter visible across all nodes, a cluster-wide set of active sessions, or a distributed configuration register, they must either:

1. Route all reads and writes through a single actor (bottleneck, single point of failure).
2. Use the Olric DMap directly with quorum settings (breaks the actor abstraction, couples user code to infrastructure).
3. Bring in an external store like Redis or etcd (adds operational complexity).

**Conflict-free Replicated Data Types (CRDTs)** solve this by providing data structures that can be updated independently on any node and are guaranteed to converge to a consistent state — with no coordination, no locks, and no consensus rounds.

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

## 3. Package Layout and Dependency Graph

### The Cycle Problem

A naive design places CRDT types and the Replicator actor in the same `crdt` package:

```
crdt/replicator.go  →  imports actor (implements actor.Actor)
actor/option.go     →  imports crdt (WithReplicator option)
                    ══ CYCLE ══
```

### The Solution: Separate Pure Types from Actor Integration

```
goakt/
├── crdt/                          ← PUBLIC: pure CRDT data types
│   ├── crdt.go                    ← ReplicatedData interface
│   ├── key.go                     ← Key typed, serializable CRDT key + factory functions
│   ├── gcounter.go                ← GCounter
│   ├── pncounter.go               ← PNCounter
│   ├── lww_register.go            ← LWWRegister
│   ├── or_set.go                   ← ORSet (with Compact method)
│   ├── or_map.go                  ← ORMap
│   ├── flag.go                    ← Flag
│   ├── mv_register.go             ← MVRegister
│   ├── consistency.go             ← Coordination type (Majority, All)
│   ├── config.go                  ← Option, WithAntiEntropyInterval, WithRole, etc.
│   └── messages.go                ← Update, Get, Subscribe, Changed message types
│
├── actor/
│   └── replicator.go              ← Replicator system actor (same pattern as relocator.go, topic_actor.go)
│
├── internal/
│   └── ddata/
│       ├── snapshot.go            ← BoltDB snapshot storage
│       ├── crdt_codec.go          ← CRDT encode/decode for protobuf wire format
│       └── crdt_serializer.go     ← Proto+CBOR composite serializer for CRDT values
│
├── protos/
│   └── internal/
│       └── crdt.proto             ← Wire format definitions
│
└── actor/
    ├── cluster_config.go          ← WithCRDT(...crdt.Option) added (imports crdt/ for Option type)
    ├── actor_system.go            ← Replicator() *PID added to ActorSystem interface
    │                                 spawnReplicator(ctx) added (internal)
    └── guardrails.go              ← replicatorType name constant added
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

Key rules:
- `crdt/` imports **nothing** from GoAkt. It is a self-contained data structure library with pure CRDT types, message structs, and configuration options.
- `actor/` imports `crdt/` for the `crdt.Option` type and CRDT data types. This is a one-way leaf dependency — `crdt/` does not import `actor/`, so no cycle. The Replicator system actor lives in `actor/` alongside the other system actors (`relocator.go`, `topic_actor.go`, `death_watch.go`).
- `internal/cluster/` does **not** import `actor/`. Placing the Replicator there would create a cycle since `actor/` imports `internal/cluster/`.
- User code imports both `crdt/` (for types and messages) and `actor/` (for Tell/Ask/ActorSystem).

### Why `crdt/` Does Not Import `actor`

Everything in `crdt/` — data types, messages, and config options — are plain Go structs with no actor dependencies:

```go
package crdt

// Update is sent to the Replicator to create or update a CRDT key.
// The update is always applied locally first and the delta is published
// to the shared CRDT topic via TopicActor. If WriteTo is set, the Replicator
// also sends the delta directly to peers and waits for acknowledgments.
type Update struct {
    Key     Key
    Initial ReplicatedData
    Modify  func(current ReplicatedData) ReplicatedData
    WriteTo Coordination // optional: Majority or All (default: none — local + pub/sub only)
}

// Get is sent to the Replicator to read a CRDT key.
// The local value is always returned. If ReadFrom is set, the Replicator
// also queries peers, merges their values with the local value, and returns
// the merged result.
type Get struct {
    Key      Key
    ReadFrom Coordination // optional: Majority or All (default: none — local only)
}

// GetResponse is the response to a Get.
type GetResponse struct {
    Key  Key
    Data ReplicatedData // nil if key not found
}

// Subscribe registers the sender for change notifications on a key.
type Subscribe struct {
    Key Key
}

// Changed is sent to watchers when a CRDT key's value changes.
type Changed struct {
    Key  Key
    Data ReplicatedData
}

// Delete removes a CRDT key.
type Delete struct {
    Key     Key
    WriteTo Coordination // optional
}
```

These are `any`-typed messages — they work with GoAkt's `Tell`/`Ask` without the `crdt` package knowing about the `actor` package.

---

## 4. Core CRDT Types

### Phase 1 — Foundation

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
package crdt

// ReplicatedData is the base interface for all CRDT types.
type ReplicatedData interface {
    // Merge combines this CRDT with a remote replica's state.
    // Returns the merged result. Both inputs are left unchanged.
    Merge(other ReplicatedData) ReplicatedData

    // Delta returns the state changes since the last call to Delta.
    // Returns nil if there are no changes.
    Delta() ReplicatedData

    // ResetDelta clears the accumulated delta state.
    ResetDelta()

    // Clone returns a deep copy.
    Clone() ReplicatedData
}
```

### Typed Keys

CRDT keys are not plain strings — they are **serializable values** that carry the CRDT data type. This provides serialization for wire transport (anti-entropy, coordination) and self-documenting code.

Defined in `crdt/key.go`:

```go
package crdt

// DataType identifies the CRDT type for serialization.
type DataType int

const (
    GCounterType    DataType = iota
    PNCounterType
    LWWRegisterType
    ORSetType
    ORMapType
    FlagType
    MVRegisterType
)

// Key is a serializable CRDT key.
// The key is serializable as (ID, DataType) for wire transport.
type Key struct {
    id       string
    dataType DataType
}

// ID returns the key's string identifier.
func (k Key) ID() string { return k.id }

// Type returns the CRDT type this key holds.
func (k Key) Type() DataType { return k.dataType }
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

Usage:

```go
// Define keys once — typically as package-level vars.
var (
    requestCount   = crdt.PNCounterKey("request-count")
    activeSessions = crdt.ORSetKey("active-sessions")
    featureFlag    = crdt.LWWRegisterKey("dark-mode")
)
```

The key's ID (`"request-count"`) is used as the TopicActor topic name for replication. The `DataType` is serialized in anti-entropy digests and coordination messages so that peers can validate type consistency.

### Example: PNCounter

```go
package crdt

// PNCounter is a counter that supports both increments and decrements.
// It is composed of two GCounters — one for increments, one for decrements.
type PNCounter struct {
    increments GCounter
    decrements GCounter
}

// Increment adds a positive value on behalf of the given node.
func (c PNCounter) Increment(nodeID string, delta uint64) PNCounter

// Decrement adds a negative value on behalf of the given node.
func (c PNCounter) Decrement(nodeID string, delta uint64) PNCounter

// Value returns the current counter value (increments - decrements).
func (c PNCounter) Value() int64

// Merge combines two PNCounters.
func (c PNCounter) Merge(other ReplicatedData) ReplicatedData
```

### Example: ORSet

```go
package crdt

// ORSet is an observed-remove set that supports concurrent add/remove
// without conflicts. Uses causal dots to track element provenance.
type ORSet struct {
    entries map[any][]dot       // element → causal dots
    clock   map[string]uint64   // vector clock per node
}

// Add inserts an element, tagged with the local node's causal dot.
func (s *ORSet) Add(nodeID string, element any) *ORSet

// Remove removes an element by recording all observed causal dots.
func (s *ORSet) Remove(element any) *ORSet

// Contains returns true if the element is in the set.
func (s *ORSet) Contains(element any) bool

// Elements returns all elements currently in the set.
func (s *ORSet) Elements() []any

// Merge combines two ORSets using observed-remove semantics.
func (s *ORSet) Merge(other ReplicatedData) ReplicatedData
```

---

## 5. Architecture Overview

### Per-Node Replicator Model

Every node in the cluster runs its own Replicator actor. There is **no central coordinator**, **no cluster singleton**, and **no custom gossip protocol** — each Replicator owns its node's local CRDT store and communicates with peer Replicators on other nodes via GoAkt's existing TopicActor pub/sub system.

### The Shared Subscription Model

The entire dissemination mechanism rests on one idea: **all Replicators subscribe to a single well-known topic (`goakt.crdt.deltas`) via GoAkt's TopicActor at startup**.

When any Replicator updates a key, it encodes the delta as a protobuf message and publishes it to this shared topic. Because every Replicator on every node is subscribed to the same topic, they all receive the delta automatically. There is no custom gossip layer, no peer selection logic, and no fan-out code inside the Replicator. The Replicator is simply a subscriber that also publishes — identical to every other Replicator in the cluster.

The TopicActor — which already handles peer discovery, remote TCP delivery, message deduplication, and subscriber cleanup — does all the cross-node delivery work. No changes to the TopicActor are required.

```
    Node A                         Node B                        Node C
    ------                         ------                        ------

 [Replicator A]               [Replicator B]               [Replicator C]
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

**Step 1 — Subscription at startup.** When a Replicator starts (in `PostStart`), it subscribes to the well-known topic `goakt.crdt.deltas` via the TopicActor. This is a one-time operation — there is no per-key subscription. Every delta for every key flows through this single topic.

**Step 2 — Local update.** A user actor sends an `Update` message to its local Replicator. The Replicator applies the mutation to its local CRDT store, extracts the delta, encodes it as a protobuf `CRDTDelta` message (which includes the key ID, data type, origin node, and serialized delta state), and publishes it to the shared topic via the TopicActor.

**Step 3 — Dissemination.** The TopicActor delivers the protobuf delta to all subscribers of `goakt.crdt.deltas` — which is the Replicator on every other node. The TopicActor handles serialization (protobuf), remote TCP delivery, and deduplication. This is not a Replicator concern.

**Step 4 — Remote merge.** Each peer Replicator receives the protobuf delta, decodes it back to the CRDT type, ignores it if the origin is itself, and merges it into its local store using the type's merge function. If the merge changes the value, it notifies any local user actors watching that key.

```
User Actor (Node A)             Replicator (Node A)           Replicator (Node B)
       |                               |                             |
       |                        [subscribed to               [subscribed to
       |                         "goakt.crdt.deltas"]         "goakt.crdt.deltas"]
       |                               |                             |
       |-- Update{Key:"counter"} ----->|                             |
       |                               |                             |
       |                        [apply mutation]                     |
       |                        [extract delta]                      |
       |                        [encode as protobuf]                 |
       |                        [store updated value]                |
       |                               |                             |
       |                        [publish CRDTDelta to                |
       |                         "goakt.crdt.deltas"                 |
       |                         via TopicActor]                     |
       |                               |                             |
       |                               |    TopicActor delivers      |
       |                               |    protobuf delta to all    |
       |                               |    subscribers              |
       |                               |- - - - - - - - - - - - - -->|
       |                               |                             |
       |                               |                      [decode protobuf]
       |                               |                      [ignore if own origin]
       |                               |                      [merge delta into
       |                               |                       local store]
       |                               |                      [notify local
       |                               |                       user actor
       |                               |                       watchers]
```

The key point: **both Replicators are equal participants** — they both subscribe to the same topic and both can publish to it. There is no publisher/subscriber distinction. When Node B updates the same key, the flow is identical with roles reversed.

### Role-Based Scoping

Not all nodes need to participate in CRDT replication. The Replicator can be scoped to nodes tagged with a specific role using GoAkt's existing `WithRoles` cluster configuration:

```go
crdt.WithRole("cache-nodes") // only replicate among nodes with this role
```

When a role is set:
- The Replicator is only spawned on nodes that advertise the matching role.
- Nodes without the role do not run a Replicator and do not subscribe to CRDT topics.
- This allows partitioning CRDT data to a subset of the cluster (e.g., only stateful nodes, not gateway nodes).

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

1. A new `nameType` constant `replicatorType` maps to the reserved name `"GoAktReplicator"`.
2. A new `spawnReplicator(ctx)` method on `actorSystem` creates the actor — conditionally, only when cluster mode is enabled **and** CRDT replication is enabled in `ClusterConfig`.
3. The Replicator PID is stored as `x.replicator *PID` on `actorSystem`, just like `x.deathWatch` and `x.relocator`.
4. It is spawned as a **child of the system guardian** with `asSystem()`, `WithLongLived()`, and a supervisor that restarts on panic.

Every participating node spawns its own independent Replicator. There is no leader election, no cluster singleton — just N identical actors at the same reserved name on N nodes, coordinating through the TopicActor pub/sub.

### Enabling CRDT via ClusterConfig

CRDT replication is enabled through a new option on `ClusterConfig`, keeping all cluster-related configuration in one place:

```go
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(disco).
    WithCRDT(
        crdt.WithAntiEntropyInterval(30 * time.Second),
        crdt.WithMaxDeltaSize(64 * 1024),
        crdt.WithRole("cache-nodes"), // optional: restrict to specific node role
        crdt.WithPruneInterval(5 * time.Minute),
        crdt.WithTombstoneTTL(24 * time.Hour),
    )

system, err := actor.NewActorSystem(
    "my-system",
    actor.WithCluster(clusterConfig),
)
```

When `WithCRDT(...)` is called on `ClusterConfig`:
- A `crdtConfig` field is set on the config struct (non-nil signals CRDT is enabled).
- During `startCluster()`, after the cluster joins, `spawnReplicator(ctx)` is called.
- If `WithCRDT` is not called, no Replicator is spawned — zero overhead for users who don't need CRDTs.

The `crdt.WithAntiEntropyInterval`, `crdt.WithRole`, etc. are option functions defined in the `crdt/` package. `ClusterConfig.WithCRDT` accepts `...crdt.Option` — this is safe because `actor` package already imports leaf config packages (like `datacenter`), and `crdt/` has zero GoAkt imports.

Note: there is no `WithGossipInterval` or `WithGossipFanout` — the TopicActor handles dissemination timing and peer selection. The only CRDT-specific network configuration is `WithAntiEntropyInterval` (the safety-net protocol described in [Section 8](#8-anti-entropy-protocol)).

### Spawn Pattern (mirrors `spawnRelocator`)

```go
// In actor/replicator.go

func (x *actorSystem) spawnReplicator(ctx context.Context) error {
    if !x.clusterEnabled.Load() || x.clusterConfig.crdtConfig == nil {
        return nil
    }

    actorName := reservedName(replicatorType)

    x.replicator, _ = x.configPID(ctx,
        actorName,
        newReplicatorActor(x.clusterConfig.crdtConfig),
        asSystem(),
        WithLongLived(),
        WithSupervisor(
            sup.NewSupervisor(
                sup.WithStrategy(sup.OneForOneStrategy),
                sup.WithAnyErrorDirective(sup.RestartDirective),
            ),
        ),
    )

    // the replicator is a child actor of the system guardian
    return x.actors.addNode(x.systemGuardian, x.replicator)
}
```

The `newReplicatorActor(config)` constructor and `spawnReplicator` both live in `actor/replicator.go`, alongside the replicator actor implementation.

### Accessing the Replicator PID

Since system actors are not accessible via `ActorOf()` (blocked by the `isSystemName` guard), the Replicator PID is exposed through the `ActorSystem` interface:

```go
// In actor/actor_system.go — new method on the ActorSystem interface

// Replicator returns the PID of the local CRDT Replicator actor.
// Returns nil if CRDT replication is not enabled.
Replicator() *PID
```

This is a single additive method — no existing methods change, no existing signatures break.

### Using the Replicator from an Actor

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    replicator := ctx.ActorSystem().Replicator()
    if replicator == nil {
        // CRDT not enabled — handle gracefully
        return
    }

    // Standard Tell/Ask — no string lookups.
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

The TopicActor (`actor/topic_actor.go`) is an existing GoAkt system actor. It provides cluster-wide pub/sub with no additional infrastructure:

1. **Topic subscriptions** — any actor can subscribe to a named topic.
2. **Local + remote delivery** — when a message is published to a topic, the TopicActor delivers it to all local subscribers AND sends it to the TopicActor on every peer node, which delivers to that node's local subscribers.
3. **Message deduplication** — tracks `(senderID, topic, messageID)` tuples to prevent double delivery.
4. **Subscriber lifecycle** — uses death watch to automatically remove terminated subscribers.

### How the Replicator Uses It

The Replicator is just another actor that subscribes to and publishes on a TopicActor topic. All Replicators on all nodes subscribe to a single well-known topic: `goakt.crdt.deltas`. When one Replicator publishes a delta, all other Replicators receive it because they are all subscribers of the same topic.

**Single topic, all keys.** Every CRDT delta for every key flows through one topic. The delta message itself carries the key ID, data type, origin node, and serialized state — the receiving Replicator uses the key ID to route the delta to the correct entry in its local store. This avoids the chicken-and-egg problem of per-key subscriptions (where a peer can't receive a delta for a key it doesn't yet know about).

### Subscribing at Startup

The Replicator subscribes to the shared topic once during `PostStart`:

```go
const crdtTopic = "goakt.crdt.deltas"

func (r *replicatorActor) handlePostStart(ctx *ReceiveContext) {
    r.pid = ctx.Self()
    r.topicActor = ctx.ActorSystem().TopicActor()
    r.nodeID = r.pid.ID()
    scheduleCtx := context.WithoutCancel(ctx.Context())

    sys := ctx.ActorSystem().(*actorSystem)
    r.clusterRef = sys.getCluster()
    r.remoting = sys.getRemoting()

    // subscribe to the CRDT delta topic so this Replicator receives
    // deltas from all peer Replicators in the cluster.
    if r.topicActor != nil {
        ctx.Tell(r.topicActor, NewSubscribe(crdtTopic))
    }

    // start anti-entropy schedule
    if r.config.AntiEntropyInterval() > 0 {
        if err := ctx.ActorSystem().Schedule(
            scheduleCtx,
            &antiEntropyTick{},
            r.pid,
            r.config.AntiEntropyInterval(),
            WithReference(antiEntropyScheduleRef),
        ); err != nil {
            r.logger.Errorf("failed to schedule anti-entropy: %v", err)
            ctx.Err(err)
            return
        }
    }

    // start prune schedule for tombstone cleanup
    if r.config.PruneInterval() > 0 {
        if err := ctx.ActorSystem().Schedule(
            scheduleCtx,
            &pruneTick{},
            r.pid,
            r.config.PruneInterval(),
            WithReference(pruneScheduleRef),
        ); err != nil {
            r.logger.Errorf("failed to schedule prune: %v", err)
            ctx.Err(err)
            return
        }
    }
}
```

From this point on, the Replicator receives every delta published by any Replicator on any node.

### Handling a Local Update

When a user actor sends an `Update` to the Replicator, the Replicator handles it internally. The key's `ID()` is used as the store key:

```go
func (r *replicatorActor) handleUpdate(ctx *ReceiveContext, msg updateCommand) {
    keyID := msg.KeyID()

    // 1. Reject updates to tombstoned keys.
    if _, ok := r.tombstones[keyID]; ok {
        if ctx.Sender() != nil {
            ctx.Response(&crdt.UpdateResponse{})
        }
        return
    }

    // 2. Get or create the CRDT value.
    current, exists := r.store[keyID]
    if !exists {
        current = msg.InitialValue()
        r.trackKey(keyID, msg.CRDTDataType())
    }

    // 3. Apply the user's mutation and extract the delta.
    updated := msg.Apply(current)
    delta := updated.Delta()
    updated.ResetDelta()
    r.store[keyID] = updated
    r.versions[keyID]++

    // 4. Encode the delta as protobuf and publish to the shared CRDT topic.
    if delta != nil {
        r.publishDelta(ctx, keyID, msg.CRDTDataType(), delta)
    }

    // 5. Notify local user actors watching this key.
    r.notifyChanged(ctx, keyID, updated)

    // 6. Reply if this was an Ask.
    if ctx.Sender() != nil {
        ctx.Response(&crdt.UpdateResponse{})
    }
}
```

The `updateCommand` interface is satisfied directly by `crdt.Update`. Methods are exported so that `crdt.Update` (in a separate package) can satisfy the interface:

```go
// updateCommand is implemented by crdt.Update.
type updateCommand interface {
    KeyID() string
    CRDTDataType() crdt.DataType
    InitialValue() crdt.ReplicatedData
    Apply(current crdt.ReplicatedData) crdt.ReplicatedData
}
```

### Handling a Peer Delta

When the TopicActor delivers a protobuf `CRDTDelta` message (because this Replicator is subscribed to `goakt.crdt.deltas`), the Replicator decodes it and delegates to `handleDelta`:

```go
func (r *replicatorActor) handleDelta(ctx *ReceiveContext, msg *crdtDelta) {
    // Ignore our own deltas (TopicActor delivers to local subscribers too).
    if msg.Origin == r.nodeID {
        return
    }

    keyID := msg.KeyID

    // Reject deltas for tombstoned keys.
    if _, ok := r.tombstones[keyID]; ok {
        return
    }

    // If the key doesn't exist locally, the delta serves as the initial value.
    current, exists := r.store[keyID]
    if !exists {
        r.store[keyID] = msg.Delta
        r.trackKey(keyID, msg.DataType)
        r.versions[keyID]++
        r.notifyChanged(ctx, keyID, msg.Delta)
        return
    }

    // Merge the remote delta into the local value.
    merged := current.Merge(msg.Delta)
    r.store[keyID] = merged
    r.versions[keyID]++
    r.notifyChanged(ctx, keyID, merged)
}
```

### Notifying Local User Actors

When a user actor subscribes to changes on a key (via `crdt.Subscribe{Key: counterKey}`), the Replicator tracks the subscriber PID internally. On any change — whether from a local update or a peer delta — it sends a `crdt.Changed` message directly to each watcher:

```go
func (r *replicatorActor) notifyChanged(ctx *ReceiveContext, keyID string, data crdt.ReplicatedData) {
    watchers, ok := r.watchers[keyID]
    if !ok {
        return
    }
    for _, watcher := range watchers {
        if watcher.IsRunning() {
            ctx.Tell(watcher, &crdt.Changed{Data: data})
        }
    }
}
```

This is a **local-only** operation — change notifications to user actors do not go through the TopicActor. Cross-node dissemination of the underlying data is handled by the shared `goakt.crdt.deltas` topic subscription.

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

TopicActor pub/sub is the primary dissemination path, but messages can be lost (network partition, node restart, TopicActor crash and recovery). Anti-entropy is a periodic safety net that ensures all nodes eventually converge, even if some deltas were missed.

### How It Works

```
Every antiEntropyInterval (default: 30s):
    1. Replicator selects one random peer from the cluster peer list.
    2. Sends a digest message to the peer's Replicator via RemoteTell:
       - Digest = map[string]uint64 (key → version for each CRDT key).
    3. Peer Replicator compares the digest with its local store.
    4. For each divergent key, the peer sends back:
       - Full CRDT state for keys that the requester is behind on.
       - A request for keys that the peer is behind on.
    5. Both sides merge any received state and update their stores.
```

Anti-entropy uses **direct RemoteTell between Replicators** (not the TopicActor) because it is a point-to-point synchronization protocol, not a broadcast.

### When Anti-Entropy Fires

- **Periodically** — every `antiEntropyInterval` (configurable, default 30s), via GoAkt's `Schedule` API. The Replicator schedules an `antiEntropyTick` message to itself during `PostStart`.
- **On supervisor restart** — if the Replicator crashes and is restarted, it starts with an empty store and relies on anti-entropy to rebuild from peers.

### Anti-Entropy Messages (Internal)

These are protobuf messages defined in `crdt.proto` and handled by the Replicator:

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

The Replicator maintains a per-key version counter (`map[string]uint64`) that increments on every local update or remote merge. The digest carries these versions so the peer can identify which keys are divergent without exchanging full state.

---

## 9. API Design

### 9.1 Configuration

CRDT replication is enabled through `ClusterConfig.WithCRDT(...)`. All CRDT options live in the `crdt/` package:

```go
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(disco).
    WithCRDT(
        crdt.WithAntiEntropyInterval(30 * time.Second),
        crdt.WithMaxDeltaSize(64 * 1024), // 64KB max delta per publish
        crdt.WithPruneInterval(5 * time.Minute),
        crdt.WithTombstoneTTL(24 * time.Hour),
        crdt.WithRole("cache-nodes"), // optional: restrict to specific node role
    )

system, err := actor.NewActorSystem(
    "my-system",
    actor.WithCluster(clusterConfig),
)
```

### 9.2 Defining Keys

Keys are defined once, typically as package-level variables:

```go
var (
    requestCount   = crdt.PNCounterKey("request-count")
    activeSessions = crdt.ORSetKey("active-sessions")
    featureFlag    = crdt.LWWRegisterKey("dark-mode")
)
```

### 9.3 Writing Data

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    replicator := ctx.ActorSystem().Replicator()

    // Increment a distributed counter.
    // The Modify function receives ReplicatedData — use a type assertion to access the concrete type.
    err := actor.Tell(ctx, replicator, &crdt.Update{
        Key:     requestCount,
        Initial: crdt.NewPNCounter(),
        Modify:  func(current crdt.ReplicatedData) crdt.ReplicatedData {
            return current.(*crdt.PNCounter).Increment(nodeID, 1)
        },
    })
}
```

```go
// Add to a distributed set — wait for majority acknowledgment.
replicator := ctx.ActorSystem().Replicator()
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
replicator := ctx.ActorSystem().Replicator()
resp, err := actor.Ask(ctx, replicator, &crdt.Get{
    Key: requestCount,
}, 5*time.Second)
if err != nil {
    // handle error
}
if getResp, ok := resp.(*crdt.GetResponse); ok && getResp.Data != nil {
    count := getResp.Data.(*crdt.PNCounter).Value()
    _ = count
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
// Inside an actor's PreStart — subscribe to changes on a key.
func (a *MyActor) PreStart(ctx *actor.Context) error {
    replicator := ctx.ActorSystem().Replicator()
    return actor.Tell(ctx, replicator, &crdt.Subscribe{
        Key: activeSessions,
    })
}

// Inside Receive — handle change notifications.
// Changed carries a ReplicatedData value — use a type assertion to access the concrete type.
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *crdt.Changed:
        if msg.Data != nil {
            sessions := msg.Data.(*crdt.ORSet).Elements()
            // react to updated session set
            _ = sessions
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

Deletion removes the key from the local store and publishes a `CRDTTombstone` to the shared `goakt.crdt.deltas` topic via TopicActor. Peer Replicators that receive the tombstone remove the key from their local stores. Tombstones are retained for a configurable TTL (`WithTombstoneTTL`, default 24h) and pruned periodically (`WithPruneInterval`, default 5m). While a tombstone is active, updates and deltas for that key are rejected to prevent resurrection.

---

## 10. Consistency Model

### Default Behavior: Local-First

Every operation is **local-first** by default:

- **Writes** apply the mutation to the local CRDT store and publish the delta via TopicActor. The Replicator returns immediately — no network wait.
- **Reads** return the local value from the store immediately.

This is eventually consistent, with convergence bounded by TopicActor's delivery latency (typically sub-second in a healthy cluster). For most use cases — counters, metrics, session tracking, feature flags — this is sufficient.

### Optional Coordination: `WriteTo` and `ReadFrom` (Phase 3)

The `Update` and `Get` message types define `WriteTo` and `ReadFrom` fields for optional coordination. The Replicator does not yet act on these fields — they are reserved for Phase 3 implementation. When stronger guarantees are needed, they will accept a coordination level:

```go
// Coordination levels defined in crdt/consistency.go
type Coordination int

const (
    Majority Coordination = iota + 1 // ⌊N/2⌋ + 1 nodes
    All                               // all nodes
)
```

#### WriteTo

| `WriteTo`   | Behavior                                                                                                                                           | Use When                                          |
|-------------|----------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------|
| *(not set)* | Apply locally + publish delta via TopicActor. Return immediately.                                                                                  | Counters, metrics, best-effort tracking           |
| `Majority`  | Apply locally + send delta directly to ⌊N/2⌋ + 1 peers via RemoteTell + wait for acks. Delta is also published via TopicActor for remaining peers. | Session state, membership sets                    |
| `All`       | Apply locally + send delta directly to all peers via RemoteTell + wait for acks.                                                                   | Configuration changes requiring global visibility |

#### ReadFrom

| `ReadFrom`  | Behavior                                                                 | Use When                          |
|-------------|--------------------------------------------------------------------------|-----------------------------------|
| *(not set)* | Return local value immediately.                                          | High-throughput reads, counters   |
| `Majority`  | Query ⌊N/2⌋ + 1 peers via RemoteAsk, merge all results with local value. | Accurate membership checks        |
| `All`       | Query all peers via RemoteAsk, merge all results with local value.       | Audit, consistency-critical reads |

### How Coordination Interacts with TopicActor

- **Default (no coordination)** — the delta is encoded as protobuf and published to `goakt.crdt.deltas`. Propagation happens asynchronously via TopicActor's delivery to all subscribed Replicators.
- **`WriteTo: Majority` / `All`** — the Replicator sends the delta directly to peer Replicators via RemoteTell (same pattern as anti-entropy) and waits for acknowledgments. The delta is *also* published via TopicActor so that peers not in the direct set receive it.
- **`ReadFrom: Majority` / `All`** — the Replicator sends RemoteAsk to peer Replicators, collects their local values, merges them with the local value, and returns the merged result.

### Consistency Guarantee

When `ReadFrom: Majority` + `WriteTo: Majority` are used together, the system provides **strong eventual consistency with read-your-writes** guarantees — matching GoAkt's existing Olric quorum model.

Without coordination, the system is **eventually consistent** with convergence bounded by TopicActor's delivery latency.

---

## 11. Cluster Integration

### Lifecycle (Per Node)

Each node independently manages its own Replicator. There is no coordination during startup — nodes catch up via anti-entropy after joining.

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
    │       └── Starts anti-entropy ticker
    │
    ├── ... normal actor system operation ...
    │       Replicator serves local Tell/Ask from user actors
    │       Replicator publishes protobuf deltas to "goakt.crdt.deltas"
    │       Replicator receives peer deltas via TopicActor subscription
    │
    └── ActorSystem.Stop()
            │
            ├── Replicator unsubscribes from "goakt.crdt.deltas"
            ├── Replicator stops anti-entropy ticker
            └── Replicator actor stops (standard system actor shutdown)
```

### Node Join

When a new node joins the cluster:

1. The new node's actor system starts and spawns its own Replicator with an empty CRDT store.
2. The Replicator immediately triggers one anti-entropy round with a random existing peer to catch up on all existing CRDT state.
3. Memberlist detects the join and emits a `NodeJoined` event on all existing nodes.
4. Existing TopicActors now include the new node in their peer set — subsequent publishes to `goakt.crdt.deltas` will reach the new Replicator automatically.
5. The new node is now caught up and participates as an equal peer.

### Node Departure

When a node leaves (graceful or crash):

1. Memberlist detects the departure and emits a `NodeLeft` event.
2. The TopicActor on remaining nodes removes the departed node from its peer set — no deltas are sent to it.
3. CRDT state is **not lost** — it exists on all other replicas.
4. Node-specific slots in GCounters and PNCounters remain valid (they represent historical contributions).
5. Pruning (optional) can reclaim storage for permanently departed nodes after a configurable TTL.

### Relationship with Olric

The CRDT store is **independent of Olric**. Olric continues to manage the actor/grain registry with strong consistency. CRDTs provide a complementary eventually-consistent data layer. This separation is intentional:

- Olric is optimized for partition-based sharding with quorum consistency.
- CRDTs are optimized for full replication with guaranteed convergence.
- Different tools for different consistency needs.

---

## 12. Multi-Datacenter Support

CRDTs are inherently well-suited for multi-DC deployment because they tolerate arbitrary network delays and partitions. This section describes the recommended deployment topology, the two-layer communication model, and how cross-DC CRDT replication integrates with GoAkt's existing infrastructure.

### 12.1 Recommended Topology: One Cluster per Datacenter

GoAkt's multi-DC architecture is built on a clear separation between **intra-DC clustering** and **cross-DC coordination**:

```
┌────────────────────────────────────────────────────────────────────┐
│                       Global Control Plane                         │
│                  (NATS JetStream or etcd)                          │
│                                                                    │
│  Responsibilities:                                                 │
│  - DataCenterRecord registry (name, region, zone, labels, endpoints│
│  - Liveness via heartbeat + lease TTL                              │
│  - State machine: REGISTERED -> ACTIVE -> DRAINING -> INACTIVE     │
│  - Watch stream for topology change notifications                  │
└────────────────────────────────────────────────────────────────────┘
        ↑ register / heartbeat            ↑ register / heartbeat
        ↓ watch / list active             ↓ watch / list active
┌─────────────────────────────────┐ ┌─────────────────────────────────┐
│ Datacenter: dc-west             │ │ Datacenter: dc-east             │
│ Region: us-west-2               │ │ Region: us-east-1               │
│                                 │ │                                 │
│ ┌─────────────────────────────┐ │ │ ┌─────────────────────────────┐ │
│ │ Cluster A                   │ │ │ │ Cluster B                   │ │
│ │ (Olric + Memberlist)        │ │ │ │ (Olric + Memberlist)        │ │
│ │                             │ │ │ │                             │ │
│ │ Discovery: Consul/K8s/      │ │ │ │ Discovery: Consul/K8s/      │ │
│ │   etcd/NATS/DNS-SD          │ │ │ │   etcd/NATS/DNS-SD          │ │
│ │ Peers: A1, A2, A3           │ │ │ │ Peers: B1, B2, B3           │ │
│ │                             │ │ │ │                             │ │
│ │ ┌─────────────────────────┐ │ │ │ │ ┌─────────────────────────┐ │ │
│ │ │ Actor Registry          │ │ │ │ │ │ Actor Registry          │ │ │
│ │ │ Grain Registry          │ │ │ │ │ │ Grain Registry          │ │ │
│ │ │ TopicActor (pub/sub)    │ │ │ │ │ │ TopicActor (pub/sub)    │ │ │
│ │ │ Replicator (CRDTs)      │ │ │ │ │ │ Replicator (CRDTs)      │ │ │
│ │ └─────────────────────────┘ │ │ │ │ └─────────────────────────┘ │ │
│ └─────────────────────────────┘ │ │ └─────────────────────────────┘ │
│                                 │ │                                 │
│ DC Controller (leader only)     │ │ DC Controller (leader only)     │
│ - Advertises endpoints:         │ │ - Advertises endpoints:         │
│   [A1:9090, A2:9090, A3:9090]   │ │   [B1:9090, B2:9090, B3:9090]   │
│ - Renews lease periodically     │ │ - Renews lease periodically     │
│ - Caches active DC records      │ │ - Caches active DC records      │
└─────────────────────────────────┘ └─────────────────────────────────┘
```

**Key principles:**

| Principle                     | Description                                                                                                                                                                                                                       |
|-------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **One cluster per DC**        | Each datacenter runs an independent GoAkt cluster with its own discovery provider, Olric ring, Memberlist, actor registry, and grain registry. Clusters are completely isolated from each other at the membership level.          |
| **Discovery is DC-scoped**    | The discovery provider (Consul, Kubernetes, etcd, NATS, DNS-SD, etc.) discovers peers within a single datacenter only. It has no cross-DC awareness.                                                                              |
| **Control plane bridges DCs** | A global control plane (NATS JetStream or etcd) serves as the cross-DC coordination layer. Each DC's cluster leader registers the DC's metadata and remoting endpoints, renews liveness leases, and caches the set of active DCs. |
| **Leader-only DC management** | Only the cluster leader in each DC runs the DataCenter controller. This avoids conflicting writes to the control plane. Leadership changes are handled automatically via the leader watch loop.                                   |
| **Cross-DC via remoting**     | Cross-DC communication (grain routing, actor spawning, CRDT replication) uses GoAkt's remoting layer to send messages directly to endpoints discovered via the control plane — not through cluster membership.                    |

### 12.2 Two-Layer Communication Model

The topology creates two distinct communication layers:

```
┌───────────────────────────────────┐ ┌─────────────────────────────────────────────┐
│ Layer 1: Intra-DC (Cluster)       │ │ Layer 2: Cross-DC (Control Plane + Remoting)│
├───────────────────────────────────┤ ├─────────────────────────────────────────────┤
│ - Memberlist gossip               │ │ - Control plane registry (NATS JetStream/   │
│ - Olric distributed hash map      │ │   etcd)                                     │
│ - TopicActor pub/sub              │ │ - DC endpoint discovery via cached active   │
│ - Replicator delta dissemination  │ │   records                                   │
│ - Anti-entropy digest exchange    │ │ - Direct remoting RPCs to remote DC         │
│ - Coordinated reads/writes        │ │   endpoints                                 │
│                                   │ │ - Cross-DC CRDT bridge (Phase 4)            │
│                                   │ │ - Cross-DC anti-entropy (Phase 4)           │
│                                   │ │ - DC-aware coordination (Phase 4)           │
├───────────────────────────────────┤ ├─────────────────────────────────────────────┤
│ Latency: sub-ms to low ms         │ │ Latency: tens to hundreds of ms             │
│ Scope: nodes within one cluster   │ │ Scope: all active datacenters               │
└───────────────────────────────────┘ └─────────────────────────────────────────────┘
```

**Intra-DC (Layer 1)** handles all latency-sensitive operations: delta dissemination via TopicActor, anti-entropy via direct peer exchange, and coordinated reads/writes. All peers discovered via `cluster.Peers()` are within the same DC.

**Cross-DC (Layer 2)** handles availability and convergence across datacenters. The DC controller maintains a cached view of all active DCs and their endpoints. Cross-DC operations use remoting RPCs to specific endpoints rather than broadcast.

### 12.3 Why One Cluster per DC (Not a Single Global Cluster)

While it is technically possible to configure a single discovery namespace that spans DCs (making all nodes join one global cluster), this is **not recommended** for CRDT deployments:

| Concern                 | Single Global Cluster                                                                                                 | One Cluster per DC (Recommended)                                                             |
|-------------------------|-----------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------|
| **Memberlist protocol** | Gossip protocol traffic increases with O(n log n) across WAN; failure detection becomes noisy due to cross-DC latency | Gossip stays within DC; fast, reliable failure detection                                     |
| **Olric DMap**          | Distributed hash map partitions span DCs; partition rebalancing on node failure crosses WAN                           | Partitions stay local; rebalancing is fast and LAN-only                                      |
| **TopicActor fan-out**  | Every published delta is sent to every node across all DCs; O(total_nodes) cross-WAN messages per update              | Fan-out stays within DC; cross-DC replication is batched and controlled                      |
| **Anti-entropy**        | Random peer may be in a remote DC; digest exchange pays WAN latency on every tick                                     | Peers are always local; cross-DC anti-entropy runs on a separate, lower-frequency schedule   |
| **Coordinated ops**     | `Majority` quorum includes remote DC nodes; latency is bounded by slowest DC                                          | `Majority` is fast (DC-local); `DCMajority` is available when cross-DC guarantees are needed |
| **Partition behavior**  | A network partition between DCs splits the cluster; Olric quorum breaks, actors may become unreachable                | Each DC continues operating independently; CRDTs converge after partition heals              |
| **Operational clarity** | One large cluster to debug; mixed LAN/WAN latency makes performance unpredictable                                     | Each DC is an independent operational unit; cross-DC issues are isolated                     |

### 12.4 Cross-DC CRDT Replication (Phase 4)

With the one-cluster-per-DC topology, the CRDT Replicator's existing mechanisms (TopicActor, `cluster.Peers()`, coordinated ops) are all **DC-scoped by design**. Cross-DC replication requires a dedicated bridge that uses the control plane to discover remote DCs and remoting to exchange deltas.

#### Architecture

```
    DC-West                                         DC-East
┌──────────────────────────┐                 ┌──────────────────────────┐
│                          │                 │                          │
│ Replicator --> TopicActor│                 │ Replicator --> TopicActor│
│     |         (intra-DC  │                 │     |         (intra-DC  │
│     |          pub/sub)  │                 │     |          pub/sub)  │
│     |                    │                 │     |                    │
│     v                    │  remoting       │     v                    │
│ CRDTBridge ──────────────┼─(batched)───────┼─ CRDTBridge              │
│ (leader only)            │  deltas         │ (leader only)            │
│                          │                 │                          │
│                          │  remoting       │                          │
│ Anti-Entropy ────────────┼─(digest +───────┼─ Anti-Entropy            │
│ (periodic, cross-DC)     │  full state)    │ (periodic, cross-DC)     │
│                          │                 │                          │
└──────────────────────────┘                 └──────────────────────────┘
```

#### Intra-DC Replication (Existing — Phase 1-3)

1. User sends `crdt.Update` to the local Replicator actor.
2. Replicator applies the mutation, extracts the delta, and publishes it as a `CRDTDelta` protobuf to the `goakt.crdt.deltas` topic via TopicActor.
3. TopicActor fans out the delta to all peer Replicators in the local cluster.
4. Each peer merges the delta into its local store and notifies watchers.
5. Anti-entropy runs on `AntiEntropyInterval` (default 30s): picks a random local peer, exchanges digests, transfers full state for divergent keys.

#### Cross-DC Replication (Phase 4 — To Be Built)

**CRDT Bridge Actor** — a new system actor (`crdtBridgeActor`) that runs **on the cluster leader only** (same pattern as the DC controller):

1. **Subscribes** to `goakt.crdt.deltas` locally to receive all intra-DC deltas and tombstones.
2. **Batches** deltas over a configurable interval (`CrossDCReplicationInterval`, default 5s) to reduce cross-DC traffic.
3. **Discovers** remote DC endpoints via the DC controller's cached active records (`ActiveRecords()`).
4. **Forwards** the batched `CRDTDeltaBatch` to the bridge actor on each remote DC via remoting.
5. **Receives** batched deltas from remote bridges, deduplicates by `(origin_node, key, version)`, and publishes them to the local TopicActor for intra-DC dissemination.

**Cross-DC Anti-Entropy** — extends the Replicator's existing anti-entropy to also exchange digests with one random endpoint from each remote DC on a separate, lower-frequency interval (`CrossDCAntiEntropyInterval`, default 60s). This serves as a safety net: even if the bridge misses deltas during a leadership failover or network blip, anti-entropy will converge cross-DC state.

#### DC-Aware Consistency Levels

Extend the existing `Coordination` type with cross-DC scopes:

| Level        | Scope              | Quorum                  | Latency       |
|--------------|--------------------|-------------------------|---------------|
| `Majority`   | Local cluster only | ⌊N_local/2⌋ + 1         | Low (LAN)     |
| `All`        | Local cluster only | N_local                 | Low (LAN)     |
| `DCMajority` | All active DCs     | Majority within each DC | High (WAN)    |
| `DCAll`      | All active DCs     | All nodes in all DCs    | Highest (WAN) |

`Majority` and `All` retain their existing DC-local semantics. `DCMajority` and `DCAll` are new levels that coordinate across DCs using the control plane's active DC list.

#### Cross-DC Tombstone Propagation

Tombstones are published to `goakt.crdt.deltas` like regular deltas, so the bridge forwards them cross-DC automatically. The `tombstoneTTL` (default 24h) must be long enough for tombstones to propagate across all DCs before expiry — otherwise a deleted key could be resurrected by a stale full-state transfer from a DC that hasn't received the tombstone yet.

### 12.5 Configuration

Cross-DC CRDT replication is opt-in. When multi-DC is enabled on the actor system and CRDTs are configured, the bridge and cross-DC anti-entropy activate automatically:

```go
dcConfig := datacenter.NewConfig(
    datacenter.WithControlPlane(natsControlPlane),
    datacenter.WithDataCenter(datacenter.DataCenter{
        Name:   "dc-west",
        Region: "us-west-2",
        Zone:   "us-west-2a",
    }),
)

clusterCfg := actor.NewClusterConfig().
    WithDataCenter(dcConfig).
    WithCRDT(
        // Intra-DC settings
        crdt.WithAntiEntropyInterval(30 * time.Second),
        crdt.WithTombstoneTTL(24 * time.Hour),
        // Cross-DC settings (Phase 4)
        crdt.WithCrossDCReplicationInterval(5 * time.Second),
        crdt.WithCrossDCAntiEntropy(true),
        crdt.WithCrossDCAntiEntropyInterval(60 * time.Second),
    )
```

### 12.6 Failure Modes and Convergence Guarantees

| Failure                           | Behavior                                                                                                                                                                                                     |
|-----------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **DC leader failover**            | New leader starts the bridge actor; missed deltas are recovered by cross-DC anti-entropy on the next tick.                                                                                                   |
| **Network partition between DCs** | Each DC continues operating independently with full local availability. CRDTs on both sides of the partition diverge but are guaranteed to converge once the partition heals and deltas/anti-entropy resume. |
| **Control plane unavailable**     | Bridge cannot discover remote DCs; cross-DC replication pauses. Intra-DC replication is unaffected. Once the control plane recovers, the bridge resumes and anti-entropy fills any gaps.                     |
| **Single node failure within DC** | Handled by intra-DC cluster membership and Replicator's existing anti-entropy. No cross-DC impact.                                                                                                           |
| **Stale DC cache**                | Configurable via `FailOnStaleCache`: strict mode rejects cross-DC operations on stale cache; best-effort mode proceeds with potentially outdated DC list.                                                    |

---

## 13. Protobuf Wire Format

CRDT deltas and full states are serialized using Protocol Buffers for wire transmission. CRDT values (e.g., the contents of an `LWWRegister` or `ORSet` elements) are serialized using a composite Proto+CBOR serializer: `proto.Message` values are encoded with Protocol Buffers, while Go primitives and user-registered types are encoded with CBOR. The protobuf wire format uses `bytes` fields for serialized values (not `google.protobuf.Any`).

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
    message ORSetDot {
        string node_id = 1;
        uint64 counter = 2;
    }
    message ORSetEntry {
        bytes element = 1;
        string type_url = 2;
        repeated ORSetDot dots = 3;
    }
    repeated ORSetEntry entries = 1;
    map<string, uint64> clock = 2;
}

message ORMapData {
    message ORMapEntry {
        bytes key = 1;
        string key_type_url = 2;
        CRDTData value = 3;
    }
    repeated ORMapEntry entries = 1;
    ORSetData key_set = 2;
}

message FlagData {
    bool enabled = 1;
}

message MVRegisterData {
    message MVRegisterEntry {
        bytes value = 1;
        string type_url = 2;
        string node_id = 3;
        uint64 counter = 4;
    }
    repeated MVRegisterEntry entries = 1;
    map<string, uint64> clock = 2;
}

message CRDTDelta {
    CRDTKey key = 1;
    string origin_node = 2;
    CRDTData data = 3;
}

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

message CRDTTombstone {
    CRDTKey key = 1;
    int64 deleted_at_nanos = 2;
    string deleted_by_node = 3;
}
```

---

## 14. GC and Memory Efficiency

CRDT data structures are on the hot path — they are created, merged, and discarded at high frequency. Careless allocation patterns will cause GC pressure that degrades tail latency. This section defines the allocation strategy for each CRDT type.

### 14.1 General Rules

| Rule                                             | Rationale                                                                                                                                                |
|--------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Value receivers on small CRDTs**               | GCounter, PNCounter, Flag, and LWWRegister are small enough to live on the stack or be copied. Avoid pointer indirection.                                |
| **Pre-sized maps**                               | All `make(map[K]V)` calls use a capacity hint based on the known size of the merging peer's state.                                                       |
| **Reuse slices across merges**                   | `ORSet.Elements()` and `ORMap.Entries()` return slices backed by a reusable buffer held in the CRDT struct, reset via `buf[:0]` rather than reallocated. |
| **No closures in merge paths**                   | Merge and Delta methods must not allocate closures. The `Update.Modify` callback is user-supplied and outside the hot merge path.                        |
| **Avoid `interface{}` boxing in internal state** | CRDT internal fields use concrete types. The `ReplicatedData` interface is used only at API boundaries.                                                  |
| **Pool protobuf encode buffers**                 | Use `sync.Pool` for the `[]byte` buffers used during protobuf serialization of deltas and full state.                                                    |

### 14.2 Per-Type Allocation Profile

| Type            | Internal State                                         | Merge Allocation                                                                                  | Notes                                                           |
|-----------------|--------------------------------------------------------|---------------------------------------------------------------------------------------------------|-----------------------------------------------------------------|
| **GCounter**    | `map[string]uint64`                                    | In-place update of existing map entries; new allocation only for unseen node IDs.                 | Map is long-lived, grows monotonically with cluster size.       |
| **PNCounter**   | Two `GCounter` values                                  | Same as GCounter × 2.                                                                             |                                                                 |
| **LWWRegister** | `value any`, `timestamp int64`, `nodeID string`        | Zero allocation if incoming timestamp loses. One assignment if it wins.                           |                                                                 |
| **ORSet**       | `map[any][]dot`, `map[string]uint64`                   | Dot slices grow with concurrent adds. Compact periodically to reclaim dots from removed elements. | Compaction runs inside anti-entropy, not on the hot merge path. |
| **ORMap**       | `ORSet` for keys + `map[any]ReplicatedData` for values | Per-key CRDT merge allocation + key-set OR-Set merge.                                             | Nested CRDT merges follow the same rules recursively.           |
| **Flag**        | `bool`                                                 | Zero allocation.                                                                                  |                                                                 |

### 14.3 Protobuf Serialization Pools

```go
var encodePool = sync.Pool{
    New: func() any { return make([]byte, 0, 4096) },
}

func encodeDelta(data crdt.ReplicatedData) ([]byte, error) {
    buf := encodePool.Get().([]byte)[:0]
    defer func() { encodePool.Put(buf[:0]) }()
    // marshal into buf...
}
```

### 14.4 Delta Size Control

`maxDeltaSize` (default: 64KB) caps the serialized size of a single delta published via TopicActor. If a delta exceeds this limit, the Replicator falls back to publishing a full-state transfer for that key instead. This prevents a single large mutation from overwhelming the TopicActor's fan-out.

### 14.5 Benchmarking Expectations

Every CRDT type must include benchmarks that verify:

1. **Zero-alloc merges** when the incoming state is a subset of the local state (common case after convergence).
2. **Bounded alloc merges** (≤ 2 allocations) when the incoming state introduces new entries.
3. **Encode/decode round-trip** allocation is dominated by the sync.Pool buffer, not per-field allocations.

Use `go test -bench=. -benchmem` and assert `0 allocs/op` for converged-state merges in CI.

---

## 15. Observability

The Replicator emits OpenTelemetry metrics consistent with GoAkt's existing `metric.go` patterns:

### Metrics

| Metric                                  | Type      | Description                                              |
|-----------------------------------------|-----------|----------------------------------------------------------|
| `goakt_crdt_keys_total`                 | Gauge     | Number of CRDT keys in the local store                   |
| `goakt_crdt_updates_total`              | Counter   | Total CRDT update operations (by key, consistency level) |
| `goakt_crdt_reads_total`                | Counter   | Total CRDT read operations (by key, consistency level)   |
| `goakt_crdt_merges_total`               | Counter   | Total merge operations from remote deltas                |
| `goakt_crdt_deltas_published_total`     | Counter   | Deltas published via TopicActor                          |
| `goakt_crdt_deltas_received_total`      | Counter   | Deltas received from peer Replicators via TopicActor     |
| `goakt_crdt_anti_entropy_rounds_total`  | Counter   | Total anti-entropy synchronization rounds                |
| `goakt_crdt_anti_entropy_repairs_total` | Counter   | Keys repaired during anti-entropy                        |
| `goakt_crdt_coordination_latency`       | Histogram | Latency of coordinated writes/reads (WriteTo/ReadFrom)   |
| `goakt_crdt_subscriptions_total`        | Gauge     | Active change subscriptions (user actors watching keys)  |
| `goakt_crdt_tombstones_total`           | Gauge     | Active tombstones pending pruning                        |

### Events

CRDT lifecycle events are published to GoAkt's EventStream:

- `CRDTKeyCreated{Key, Type}` — A new CRDT key was created locally.
- `CRDTKeyUpdated{Key}` — A CRDT key was updated (local or remote).
- `CRDTKeyDeleted{Key}` — A CRDT key was deleted.

---

## 16. Performance Considerations

### Memory

- All CRDT state is held in-memory. Memory usage grows linearly with the number of keys and the size of each CRDT value.
- ORSets and ORMaps maintain metadata (causal dots) that grows with the number of unique add operations. Periodic compaction prunes obsolete dots.
- Tombstones for deleted keys are retained for a configurable TTL (default: 24h) before pruning.

### Network

- **Delta-based replication** keeps bandwidth proportional to change rate, not data size.
- **MaxDeltaSize** (default: 64KB) caps the payload per TopicActor publish. If exceeded, a full-state transfer is used for that key.
- **TopicActor fan-out** — the TopicActor sends to all peers on every publish. For large clusters (50+ nodes), this is acceptable because CRDT deltas are small and publish frequency is bounded by update rate, not a periodic timer.
- **Anti-entropy** is point-to-point and periodic (default: every 30s to one random peer), adding minimal network overhead.

### CPU

- CRDT merge operations are computationally cheap (map merges, max comparisons).
- The Replicator processes updates sequentially (single actor), which simplifies correctness but bounds throughput to one core.
- For extreme throughput, keys can be sharded across multiple Replicator actors (Phase 3 optimization).

### Benchmark Targets

| Operation                            | Target Throughput | Notes                                                       |
|--------------------------------------|-------------------|-------------------------------------------------------------|
| Local update (default)               | > 500K ops/sec    | Single key, PNCounter                                       |
| Local read (default)                 | > 1M ops/sec      | Single key, any type                                        |
| Convergence (5 nodes)                | < 1 second        | 95th percentile (TopicActor is faster than periodic gossip) |
| Convergence (50 nodes)               | < 3 seconds       | 95th percentile                                             |
| PNCounter merge (converged)          | 0 allocs/op       | Benchmark gate                                              |
| ORSet merge (converged, 1K elements) | 0 allocs/op       | Benchmark gate                                              |

---

## 17. Phased Delivery Plan

### Phase 1 — Core Types and TopicActor-Based Replication ✅

**Goal:** Implement foundation CRDT types and the Replicator actor with TopicActor-based dissemination.

- [x] `ReplicatedData` interface in `crdt/crdt.go`
- [x] `Key`, `DataType`, and factory functions in `crdt/key.go`
- [x] `GCounter` implementation + `GCounterKey` + tests + benchmarks
- [x] `PNCounter` implementation + `PNCounterKey` + tests + benchmarks
- [x] `LWWRegister` implementation + `LWWRegisterKey` + tests + benchmarks
- [x] `ORSet` implementation + `ORSetKey` + tests + benchmarks
- [x] Message types: `Update`, `Get`, `GetResponse`, `Subscribe`, `Changed`, `Delete`
- [x] CRDT config options in `crdt/config.go` (`WithAntiEntropyInterval`, `WithRole`, etc.)
- [x] `ClusterConfig.WithCRDT(...)` option in `actor/cluster_config.go`
- [x] `Replicator() *PID` method on `ActorSystem` interface
- [x] `replicatorType` reserved name in `actor/guardrails.go`
- [x] Replicator system actor in `actor/` (spawned by `spawnReplicator`)
- [x] Replicator subscribes to `goakt.crdt.deltas` topic at startup
- [x] Delta encoding as protobuf `CRDTDelta` and publishing via TopicActor
- [x] Delta decoding and merge on receiving peer deltas
- [x] Protobuf wire format for all Phase 1 types (CRDTDelta, CRDTData, etc.)
- [x] Local-first default consistency (no coordination)
- [x] Local change notifications to user actors watching keys
- [x] OpenTelemetry metrics
- [x] Integration tests (multi-node, convergence verification)
- [x] Zero-alloc merge benchmarks as CI gates

### Phase 2 — Anti-Entropy, Composition, and Tombstones ✅

**Goal:** Add anti-entropy, composite CRDT types, and tombstone-based deletion.

- [x] Anti-entropy protocol (version-based digest exchange via direct RemoteTell between Replicators)
- [x] `ORMap` implementation + `ORMapKey` + tests + benchmarks
- [x] `Flag` implementation + `FlagKey` + tests + benchmarks
- [x] `MVRegister` implementation + `MVRegisterKey` + tests + benchmarks
- [x] Key deletion with tombstones and pruning (tombstones published via TopicActor, expired by configurable TTL)
- [x] ORSet compaction (causal dot pruning via `Compact()` method)
- [x] Protobuf wire format for Phase 2 types (ORMapData, FlagData, MVRegisterData, CRDTTombstone)
- [x] Codec encode/decode for all CRDT types with Proto+CBOR serialization
- [x] Scheduled anti-entropy and prune ticks via GoAkt `Schedule` API
- [x] Proper error handling with `ctx.Err()` for supervisor-driven restarts

### Phase 3 — Coordination and Optimization

**Goal:** WriteTo/ReadFrom coordination and performance hardening.

- [x] `WriteTo: Majority` / `ReadFrom: Majority` coordination (direct RemoteTell/RemoteAsk)
- [x] `WriteTo: All` / `ReadFrom: All` coordination
- [x] ORMap compaction
- [x] Durable CRDT snapshots (optional persistence to BoltDB)
- [x] OpenTelemetry metrics
- [x] Benchmark suite for all CRDT types (Clone, Delta, Value, Contains, Compact, merge at scale)
- [x] Performance tuning and load testing

### Phase 4 — Multi-DC and Scaling (Future)

**Goal:** Cross-datacenter replication, DC-aware consistency, and scaling optimizations. See [Section 12](#12-multi-datacenter-support) for full topology and design details.

- [ ] `CRDTDeltaBatch` protobuf message + codec support
- [ ] Cross-DC configuration options (`WithCrossDCReplicationInterval`, `WithCrossDCAntiEntropy`, `WithCrossDCAntiEntropyInterval`)
- [ ] `crdtBridgeActor` system actor (leader-only, batched delta forwarding via remoting)
- [ ] Wire bridge into cluster startup alongside DC controller
- [ ] Cross-DC anti-entropy (extend Replicator to exchange digests with remote DC endpoints)
- [ ] `DCMajority` / `DCAll` coordination levels
- [ ] Cross-DC replication lag metrics (OpenTelemetry)
- [ ] Integration tests (multi-DC convergence, partition healing, leader failover)
- [ ] Replicator sharding for high-throughput keys (if real-world usage demonstrates the bottleneck)
