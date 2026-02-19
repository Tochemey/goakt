# GoAkt Architecture

> A distributed actor framework for Go, built on Protocol Buffers.

---

## Bird's Eye View

GoAkt is a framework for building **concurrent, distributed, and fault-tolerant systems** in Go using the [Actor Model](https://www.brianstorti.com/the-actor-model/). Every unit of computation is an **actor** — a lightweight, isolated entity that communicates exclusively through immutable Protocol Buffer messages. There is no shared state between actors; the only way to interact with one is to send it a message.

### The Three Deployment Modes

GoAkt operates across three escalating levels of distribution:

| Mode                      | Description                                                                                                                          |
|---------------------------|--------------------------------------------------------------------------------------------------------------------------------------|
| **Standalone**            | Single process. Actors communicate in-process. No network needed.                                                                    |
| **Clustered (Single DC)** | Multiple nodes. Discovery via Consul, etcd, Kubernetes, NATS, mDNS, DNS-SD, or Static. Actors are location-transparent across nodes. |
| **Multi-Datacenter**      | Multiple clusters across DCs. Pluggable control plane (NATS JetStream or etcd). DC-aware actor placement and cross-DC messaging.     |

### Core Concepts

| Concept         | Description                                                                                                                                                   |
|-----------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Actor**       | The fundamental unit. Receives messages, updates private state, spawns children, sends messages.                                                              |
| **ActorSystem** | The runtime host. Manages actor lifecycle, messaging, cluster membership, and remoting.                                                                       |
| **PID**         | A process identifier — a live handle to a running actor. Used for all interactions.                                                                           |
| **Address**     | The canonical location of an actor: `goakt://system@host:port/path/to/actor`.                                                                                 |
| **Grain**       | A virtual actor. Automatically activated on first message, deactivated when idle. Always addressable by identity regardless of where it lives in the cluster. |
| **Mailbox**     | Each actor has one. Messages wait here until the actor is ready to process them.                                                                              |
| **Supervisor**  | Each actor has a parent that defines what happens when the actor fails (stop, resume, restart, escalate).                                                     |
| **Passivation** | Actors that have been idle for too long are automatically stopped to reclaim memory.                                                                          |

### Message Flow

```
Sender                      Transport                     Receiver
──────                      ─────────                     ────────

[Actor/Client]              [local / TCP]             [Target Actor]
      │                                                       │
      │── Tell(message) ─────────────────────────────────────►│
      │                                                       │ enqueue → mailbox
      │                                                       │ dequeue → Receive(ctx)
      │                                                       │
      │── Ask(message, timeout) ─────────────────────────────►│
      │◄─────────────────────────────────── response ─────────│
```

For remote messages, the `remote` package serialises the protobuf payload over a custom TCP frame protocol with optional compression (gzip, brotli, zstd).

### Actor Hierarchy

Every actor lives inside a tree. GoAkt creates three guardian actors at startup that serve as roots:

```
/ (root guardian)
├── /system              ← internal system actors (dead letter, scheduler, topic actors…)
└── /user                ← all user-defined actors live here
    ├── /user/orderService
    │   ├── /user/orderService/inventory
    │   └── /user/orderService/payment
    └── /user/reportService
```

When a parent is stopped, all its children are stopped first (depth-first). A parent supervises its children: on failure the configured `Supervisor` directive decides whether to resume, restart, stop, or escalate the error up the tree.

### Cluster Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│ GoAkt Cluster Node                                               │
│                                                                  │
│ ┌──────────────┐   ┌───────────────┐   ┌────────────────────┐    │
│ │ ActorSystem  │──►│ Cluster       │──►│ Discovery          │    │
│ │              │   │ (Olric DMap)  │   │ (K8s/Consul/...)   │    │
│ └──────┬───────┘   └───────────────┘   └────────────────────┘    │
│        │                                                         │
│ ┌──────▼───────┐   ┌───────────────┐                             │
│ │ Remoting     │──►│ TCP Server    │ ◄── connections from peers  │
│ │ (TCP/TLS)    │   │ (proto frame) │                             │
│ └──────────────┘   └───────────────┘                             │
└──────────────────────────────────────────────────────────────────┘
```

Cluster state (actor registry, grain registry) is stored in a distributed hash map powered by [Olric](https://github.com/tochemey/olric). Node membership is maintained by [Hashicorp Memberlist](https://github.com/hashicorp/memberlist). Cluster events (node joined/left, actor relocated) are broadcast over Olric's embedded pub/sub channel (which exposes a Redis-compatible interface, but runs in-process — no external Redis is needed).

---

## Code Map

This section describes every top-level directory and its most important files. Think of it as a guided tour of the codebase.

```
goakt/
├── actor/              ← THE core package. Start here.
├── address/            ← Actor address type
├── breaker/            ← Circuit breaker utility
├── client/             ← Cluster client (external callers)
├── datacenter/         ← Multi-datacenter support
├── discovery/          ← Service discovery providers
├── errors/             ← Shared error sentinels
├── eventstream/        ← In-process pub/sub event bus
├── extension/          ← Extension (system-wide) and Dependency (per-actor) interfaces
├── goaktpb/            ← Generated public protobuf Go code
├── hash/               ← Consistent hashing utility
├── internal/           ← Private implementation details
├── log/                ← Logging abstraction
├── memory/             ← Memory utility helpers
├── mocks/              ← Test mocks (generated)
├── passivation/        ← Passivation strategy types
├── protos/             ← Protobuf source definitions (.proto files)
├── reentrancy/         ← Reentrancy configuration types
├── remote/             ← Remoting layer (TCP messaging)
├── supervisor/         ← Supervision strategy types
├── testkit/            ← Testing helpers for actor-based tests
└── tls/                ← TLS configuration helpers
```

---

### `actor/` — The Core

Everything starts and ends here. The `ActorSystem` is the entry point; `PID` is the handle you interact with after spawning an actor.

| File                             | Purpose                                                                                                                                                             |
|----------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `actor.go`                       | Defines the `Actor` interface: `PreStart`, `Receive`, `PostStop`.                                                                                                   |
| `actor_system.go`                | `ActorSystem` interface + concrete `actorSystem` implementation. Wires together cluster, remoting, scheduling, passivation, extensions, and the guardian hierarchy. |
| `pid.go`                         | `PID` — a live reference to an actor. Owns the mailbox dispatch loop, metrics, and state transitions.                                                               |
| `actor_ref.go`                   | `ActorRef` — a serializable, lightweight actor reference (address only, no mailbox). Used for cross-node references.                                                |
| `receive_context.go`             | `ReceiveContext` — passed to `Receive`. It represents the message context used during message handling.                                                             |
| `context.go`                     | `Context` — passed to `PreStart` / `PostStop`. Provides some context for PreStart and PostStop.                                                                     |
| `grain.go`                       | `Grain` interface + virtual actor machinery.                                                                                                                        |
| `grain_engine.go`                | Grains engine: activates grains on demand, routes messages to the right node.                                                                                       |
| `grain_pid.go`                   | `GrainPID` — PID variant for virtual actors.                                                                                                                        |
| `grain_context.go`               | `GrainContext` — message handling context for grain `OnReceive`.                                                                                                    |
| `mailbox.go`                     | `Mailbox` interface.                                                                                                                                                |
| `unbounded_mailbox.go`           | Default FIFO mailbox (lock-free queue).                                                                                                                             |
| `bounded_mailbox.go`             | Capacity-capped mailbox; blocks when full until space is available (backpressure).                                                                                  |
| `unbounded_priority_mailbox.go`  | Priority-ordered mailbox for urgent messages.                                                                                                                       |
| `unbounded_fair_mailbox.go`      | Fair-scheduled mailbox preventing starvation.                                                                                                                       |
| `unbounded_segmented_mailbox.go` | Segmented mailbox for throughput partitioning.                                                                                                                      |
| `router.go`                      | `Router` actor — fans out messages to a pool of routees using a strategy.                                                                                           |
| `routing_strategy.go`            | Built-in strategies: round-robin, random, fan-out.                                                                                                                  |
| `scheduler.go`                   | Scheduled / recurring message delivery (backed by go-quartz).                                                                                                       |
| `supervision_signal.go`          | Internal signals for supervisor directives (Resume, Restart, Stop, Escalate).                                                                                       |
| `passivation_manager.go`         | Tracks idle actors and triggers passivation according to the configured strategy.                                                                                   |
| `stash.go`                       | Stash buffer — lets an actor defer messages for later processing.                                                                                                   |
| `behavior_stack.go`              | `Become`/`Unbecome` stack for behaviour switching.                                                                                                                  |
| `spawn.go`                       | Spawn logic for both local and remote actors.                                                                                                                       |
| `spawn_option.go`                | `SpawnOption` functional options (mailbox, supervisor, passivation, reentrancy…).                                                                                   |
| `option.go`                      | `Option` functional options for `NewActorSystem`.                                                                                                                   |
| `cluster_config.go`              | Cluster-mode configuration for the actor system.                                                                                                                    |
| `cluster_singleton.go`           | Cluster singleton: guarantees a single instance of an actor across all nodes.                                                                                       |
| `dead_letter.go`                 | Dead-letter actor: captures messages sent to stopped/non-existent actors.                                                                                           |
| `death_watch.go`                 | System actor that cleans up dead actors: removes them from the actor tree and cluster registry on termination.                                                      |
| `relocator.go`                   | Handles actor relocation when a cluster node departs.                                                                                                               |
| `remote_server.go`               | TCP server side of remoting, hosted inside the actor system.                                                                                                        |
| `root_guardian.go`               | Root of the actor hierarchy tree.                                                                                                                                   |
| `system_guardian.go`             | Parent of all internal system actors.                                                                                                                               |
| `user_guardian.go`               | Parent of all user-spawned actors.                                                                                                                                  |
| `metric.go`                      | OpenTelemetry actor metrics (message count, mailbox depth, processing time…).                                                                                       |
| `pools.go`                       | Object pools for `ReceiveContext` and related structs.                                                                                                              |
| `func_actor.go`                  | `FuncActor` — create an actor from a plain function (no struct needed).                                                                                             |
| `topic_actor.go`                 | Pub/sub topic actor powering the `EventStream`.                                                                                                                     |
| `reentrancy.go`                  | Reentrancy support wired into PID's dispatch loop.                                                                                                                  |
| `reflection.go`                  | Runtime type helpers for grain identity and actor registration.                                                                                                     |

---

### `address/`

Defines the `Address` type used everywhere to locate actors.

```
goakt://system@host:port/user/myActor
```

Handles parsing, formatting, equality, and parent/child path relationships.

---

### `remote/`

The remoting layer enables actors on different nodes to exchange messages transparently.

| File                    | Purpose                                                                                                   |
|-------------------------|-----------------------------------------------------------------------------------------------------------|
| `remoting.go`           | `Remoting` interface + implementation. Provides `RemoteTell`, `RemoteAsk`, `RemoteLookup`, `RemoteSpawn`. |
| `config.go`             | `Config` for host, port, TLS, compression, connection pool settings.                                      |
| `peer.go`               | `Peer` — a remote node endpoint (host + port).                                                            |
| `compression.go`        | Message compression helpers (gzip, brotli, zstd).                                                         |
| `context_propagator.go` | Pluggable `ContextPropagator` interface for request-scoped metadata.                                      |

The wire protocol is a custom length-prefixed Protocol Buffer frame sent over TCP (see `internal/net`).

---

### `internal/cluster/`

Implements distributed cluster membership and actor/grain registry.

| File              | Purpose                                                                                       |
|-------------------|-----------------------------------------------------------------------------------------------|
| `cluster.go`      | `Cluster` interface + `cluster` implementation. Coordinates Olric, Memberlist, and Discovery. |
| `peer.go`         | `Peer` struct representing a cluster member.                                                  |
| `store.go`        | `Store` interface for actor/grain persistence.                                                |
| `memory_store.go` | In-memory store implementation.                                                               |
| `boltdb_store.go` | BoltDB-backed persistent store.                                                               |
| `event.go`        | `Event` type for cluster membership changes (NodeJoined, NodeLeft, NodeModified).             |
| `discovery.go`    | Bridge between the cluster and the `discovery.Provider` interface.                            |

---

### `discovery/`

Pluggable service discovery. Each sub-package implements `discovery.Provider`.

| Provider      | Backend                                         |
|---------------|-------------------------------------------------|
| `consul/`     | HashiCorp Consul                                |
| `etcd/`       | etcd v3                                         |
| `kubernetes/` | Kubernetes API (headless services / pod labels) |
| `nats/`       | NATS messaging                                  |
| `mdns/`       | Multicast DNS (local network, zero-config)      |
| `dnssd/`      | DNS Service Discovery                           |
| `static/`     | Hard-coded peer list (useful for testing)       |

The `Provider` interface exposes: `Initialize`, `Register`, `Deregister`, `DiscoverPeers`, `Close`.

---

### `datacenter/`

Multi-datacenter support. Actors can be spawned or sent messages to across datacenters.

```
datacenter/
├── data_center.go          ← DataCenter metadata type
├── config.go               ← DataCenter configuration
└── controlplane/
    ├── nats/               ← Control plane backed by NATS JetStream
    └── etcd/               ← Control plane backed by etcd
```

The control plane propagates DC-level topology and actor placement decisions across sites.

---

### `supervisor/`

Defines supervision strategies and directives.

- **Strategy**: `OneForOne` (only the failing child is affected) or `OneForAll` (all siblings are affected).
- **Directive**: `Resume` (ignore failure), `Restart` (re-initialize), `Stop` (terminate), `Escalate` (bubble up to grandparent).
- Supports configurable retry windows (max retries within a time period before escalating).

---

### `passivation/`

Strategy types for actor passivation (automatic idle shutdown).

| Strategy                     | Trigger                                             |
|------------------------------|-----------------------------------------------------|
| `TimeBasedStrategy`          | No message received within a configurable duration. |
| `MessagesCountBasedStrategy` | Fewer than N messages processed within a window.    |
| `LongLivedStrategy`          | Never passivated (explicitly opt-out).              |

---

### `reentrancy/`

Configuration for async `Ask` reentrancy inside an actor's `Receive` method.

| Mode                | Behaviour                                                                          |
|---------------------|------------------------------------------------------------------------------------|
| `Off`               | No reentrancy. Sequential processing only.                                         |
| `AllowAll`          | All incoming messages can interrupt the current handler while awaiting a response. |
| `StashNonReentrant` | Non-reentrant messages are stashed; reentrant ones proceed.                        |

---

### `eventstream/`

An in-process event bus. Actors and system components publish typed events; subscribers receive them via Go channels.

Used by the actor system to broadcast: actor started/stopped, cluster node events, dead letters, and custom application events.

---

### `extension/`

Defines two distinct interfaces:

- **`Extension`** — a system-wide plugin registered once at `ActorSystem` creation time via `WithExtensions(...)`. Extensions are shared across the entire system and accessible from any actor's `Context`, `ReceiveContext`, or `GrainContext` via `Extension(id)` or `Extensions()`. The interface requires only an `ID()` method; the concrete type carries domain-specific methods that actors access through type assertion. Typical uses: event sourcing engines, metrics recorders, service registry clients.

- **`Dependency`** — a per-actor resource injected at spawn time via `WithDependencies(...)`. Unlike extensions, dependencies are scoped to a single actor and must implement `BinaryMarshaler`/`BinaryUnmarshaler` so they can be serialised during actor relocation across cluster nodes. Typical uses: database clients, external API clients, configuration providers.

---

### `client/`

A standalone cluster client for callers that live outside the actor system (e.g., HTTP handlers, CLI tools).

| File             | Purpose                                                                 |
|------------------|-------------------------------------------------------------------------|
| `client.go`      | `Client` — connects to the cluster and provides `Tell`, `Ask`, `Spawn`. |
| `balancer.go`    | `Balancer` interface for selecting a target node.                       |
| `round_robin.go` | Round-robin node selection.                                             |
| `random.go`      | Random node selection.                                                  |
| `least_load.go`  | Least-loaded node selection.                                            |

---

### `internal/net/`

The low-level network transport used by remoting.

| File              | Purpose                                                                                           |
|-------------------|---------------------------------------------------------------------------------------------------|
| `proto_server.go` | TCP server that reads length-prefixed protobuf frames and dispatches them to registered handlers. |
| `client.go`       | TCP client with connection pooling.                                                               |
| `compress.go`     | Pluggable compression (gzip, brotli, zstd).                                                       |
| `frame_pool.go`   | Pool of reusable frame buffers to reduce GC pressure.                                             |
| `worker_pool.go`  | Worker pool for concurrent frame processing.                                                      |

---

### `internal/` — Other Sub-packages

| Package               | Purpose                                                                                                                |
|-----------------------|------------------------------------------------------------------------------------------------------------------------|
| `internal/chain`      | Middleware-style chain execution for request pipelines.                                                                |
| `internal/codec`      | Protobuf marshal/unmarshal helpers.                                                                                    |
| `internal/future`     | Future/promise used by `Ask` for async result delivery.                                                                |
| `internal/id`         | unique id generation.                                                                                                  |
| `internal/memberlist` | Thin wrapper over Hashicorp Memberlist (cluster membership) for TCP transport. This is mainly used when TLS is enabled |
| `internal/metric`     | OpenTelemetry metric registration helpers.                                                                             |
| `internal/pointer`    | Generic pointer utilities.                                                                                             |
| `internal/quorum`     | Quorum size calculations.                                                                                              |
| `internal/registry`   | Runtime registry mapping actor type names to factory functions (needed for remote spawn and grain activation).         |
| `internal/strconvx`   | Extended string conversion utilities.                                                                                  |
| `internal/ticker`     | Enhanced Ticker abstraction utility.                                                                                   |
| `internal/timer`      | Enhanced Timer abstraction utility.                                                                                    |
| `internal/types`      | Shared internal type aliases.                                                                                          |
| `internal/validation` | Fluent validation helpers.                                                                                             |
| `internal/xsync`      | Extended synchronisation primitives (atomic maps, counters).                                                           |

---

### `goaktpb/` and `protos/`

```
protos/
├── goakt/      ← Public-facing .proto definitions (messages, actor requests, cluster events)
├── internal/   ← Internal wire types (cluster registry, remoting frames)
└── test/       ← Test fixture proto messages

goaktpb/        ← Generated Go code from protos/goakt/
```

All inter-actor messages **must** be protobuf types. The framework itself uses `goaktpb` for its own wire types (remote tell/ask envelopes, cluster actor records, etc.).

---

### Supporting Packages

| Package    | Purpose                                                                                                |
|------------|--------------------------------------------------------------------------------------------------------|
| `log/`     | Logging interface. Default implementation is `zap`. Swap in any logger at system creation time.        |
| `errors/`  | Sentinel error values shared across packages (e.g., `ErrActorNotFound`, `ErrDeadLetter`).              |
| `hash/`    | Consistent hashing for partition-based actor placement in the cluster.                                 |
| `breaker/` | Circuit breaker for protecting async outbound calls (used by `PipeTo` operations).                     |
| `memory/`  | Memory size helpers.                                                                                   |
| `tls/`     | `TLSInfo` config type for TLS-enabled remoting.                                                        |
| `testkit/` | Test probes, assertion helpers, and fixture factories for writing actor-level tests.                   |
| `mocks/`   | Generated mock implementations of public interfaces (`Actor`, `Mailbox`, `Remoting`, `Cluster`, etc.). |

---

## Key Data Flows

### Spawning a Local Actor

```
user code
  └─► actorSystem.Spawn(ctx, name, actor)
        ├─ validate options
        ├─ create PID (mailbox, supervisor, metrics)
        ├─ register in pid tree
        ├─ call actor.PreStart(ctx)
        └─ start mailbox dispatch goroutine
             └─ loop: dequeue → actor.Receive(ctx)
```

### Sending a Remote Message (Tell)

```
actor.Tell(to *PID, msg)
  └─ PID.isForeign() ?
       yes → remoting.RemoteTell(from.Address, to.Address, msg)
               └─ serialize msg to Any
               └─ build RemoteMessage protobuf frame
               └─ compress (optional)
               └─ write to TCP connection (from pool)
                    └─ target node's proto_server receives frame
                    └─ dispatch to actorSystem.handleRemoteTell
                    └─ look up local PID by address
                    └─ enqueue into local mailbox
```

### Activating a Grain

```
grainEngine.Invoke(identity, message)
  └─ look up grain in cluster registry (Olric DMap)
       found on this node → route to local GrainPID
       found on other node → RemoteTell to that node
       not found →
          acquire activation barrier (per-identity mutex)
          call grain.OnActivate(ctx, props)
          register in cluster registry
          route message to new GrainPID
```

### Cluster Node Failure & Actor Relocation

```
memberlist detects node departure
  └─ cluster emits NodeLeft event
  └─ every node: resync local actors/grains in registry, trigger DC reconciliation
  └─ leader node only (non-leaders just clean up the departed peer's cached state):
       ├─ check relocation is enabled
       ├─ deduplicate via rebalancedNodes set (process each departure at most once)
       ├─ fetch departed node's PeerState from cluster store (actors + grains it owned)
       ├─ enqueue PeerState into rebalancingQueue
       └─ rebalancingLoop picks it up:
            ├─ set relocating flag (only one rebalance at a time)
            ├─ send Rebalance message to the relocator actor
            └─ relocator.Relocate():
                 ├─ allocateActors(): partition actors among leader + peers
                 │    ├─ singleton actors → always go to the leader
                 │    ├─ remaining actors → split evenly across all nodes
                 │    └─ skip: system actors, non-relocatable actors
                 ├─ allocateGrains(): partition grains evenly across all nodes
                 │    └─ skip: system grains, grains with relocation disabled
                 ├─ run leader + peer relocation in parallel (errgroup):
                 │    ├─ leader shares: recreate locally (Spawn or SpawnSingleton)
                 │    │    └─ remove stale entry → instantiate actor → apply
                 │    │       original options (supervisor, passivation, reentrancy,
                 │    │       dependencies, stashing, role) → register new location
                 │    └─ peer shares: spawn remotely via RemoteSpawn / RemoteActivateGrain
                 │         └─ remove stale entry → send spawn request to target node
                 │            → target registers new location
                 └─ send RebalanceComplete back to the actor system
```

---

## Dependency Summary

| Package            | Depends On                                                                                                                                                                 |
|--------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `actor`            | `address`, `remote`, `internal/cluster`, `discovery/*`, `datacenter`, `supervisor`, `passivation`, `reentrancy`, `eventstream`, `extension`, `log`, `errors`, `internal/*` |
| `remote`           | `address`, `internal/net`, `internal/codec`                                                                                                                                |
| `internal/cluster` | `discovery`, `internal/memberlist`, `hash`                                                                                                                                 |
| `client`           | `remote`, `address`                                                                                                                                                        |

`address`, `log`, `errors`, `extension`, `eventstream`, `supervisor`, `passivation`, and `reentrancy` are **leaf packages** — they depend on nothing else in this repository.

---

## Design Decisions & Rationale

This section explains the *why* behind major technical choices. Understanding these helps contributors make decisions that stay consistent with the framework's philosophy.

### Why Protocol Buffers as the sole message format?

Actors in a distributed system must exchange messages across process and machine boundaries. This requires a well-defined serialization format. GoAkt mandates Protocol Buffers for all actor messages because:

- **Schema-driven contracts.** Every message has a `.proto` definition, making the wire format unambiguous. Contributors and users cannot accidentally pass unserializable types.
- **Cross-language potential.** Protobuf schemas are language-neutral. Even though GoAkt is a Go framework, the wire format is readable by any language with a protobuf compiler.
- **Efficient encoding.** Protobuf's binary format is compact and fast to marshal/unmarshal — critical when millions of messages flow through the system per second.
- **Type resolution at runtime.** The framework uses protobuf's global type registry (`protoregistry.GlobalTypes`) to reconstruct messages from their fully-qualified type name. This powers remote deserialization without the sender and receiver sharing Go types at compile time.

The trade-off is that users cannot send plain Go structs. This is intentional — it forces explicit message contracts and keeps the system honest about what crosses boundaries.

### Why a custom TCP frame protocol instead of gRPC?

GoAkt uses a hand-rolled, length-prefixed binary frame protocol over raw TCP instead of gRPC because:

- **Minimal overhead.** gRPC adds HTTP/2 framing, header compression (HPACK), and stream multiplexing. For actor messages — which are small, frequent, and point-to-point — this is unnecessary weight.
- **Control over connection lifecycle.** The framework manages its own connection pools, compression wrappers, and frame buffer pools. This level of control enables optimisations (zero-copy type extraction, size-bucketed buffer pools, lock-free worker dispatch) that would be difficult or impossible behind gRPC's abstractions.
- **Fewer dependencies.** gRPC pulls in a large dependency tree. A raw TCP protocol keeps the framework lean.

The trade-off is more code to maintain (frame parsing, connection management, compression negotiation). This is acceptable because the network layer changes infrequently and is well-tested.

### Why Olric for cluster state?

Cluster state (which actors live where, which grains are activated on which node) needs to be replicated across all nodes. GoAkt uses [Olric](https://github.com/tochemey/olric) because:

- **Embedded.** Olric runs in-process — no external database to deploy or manage.
- **Distributed hash map with quorum.** Olric provides configurable read/write quorums and synchronous replication, giving the framework strong consistency guarantees for actor placement.
- **Built on Memberlist.** Olric uses the same Hashicorp Memberlist library that GoAkt uses for membership, so there is a single membership layer rather than two competing ones.

### Why a tree-based actor hierarchy?

Every actor has exactly one parent, forming a tree rooted at the root guardian. This mirrors Erlang/OTP and Akka. The benefits:

- **Deterministic lifecycle.** Stopping a parent stops all descendants, depth-first. There is no ambiguity about cleanup order.
- **Scoped supervision.** A parent defines the failure policy for its children. Escalation travels up the tree until a supervisor handles the error or the root guardian is reached.
- **Namespacing.** Actor addresses reflect the tree path (`/user/orderService/payment`), preventing name collisions and enabling hierarchical lookups.

### Why separate Actor and Grain abstractions?

Actors and Grains serve different use cases:

- **Actors** are explicitly spawned and stopped. The caller controls the lifecycle. Best for long-lived services, stateful workers, and infrastructure components.
- **Grains** (virtual actors) are identity-addressed and automatically activated on first message. The framework manages the lifecycle — activation, passivation, and placement across the cluster. Best for entity-per-identity patterns (users, sessions, devices) where the population is large and mostly idle.

Combining both in one framework lets users pick the right tool without switching libraries.

---

## Actor Lifecycle State Machine

A PID tracks its state as a bitmask of flags stored in an `atomic.Uint32`. Multiple flags can be active simultaneously.

### State Flags

| Flag                       | Meaning                                                           |
|----------------------------|-------------------------------------------------------------------|
| `runningState`             | Initialisation complete. The actor can process messages.          |
| `stoppingState`            | Shutdown or passivation is in progress.                           |
| `suspendedState`           | Suspended by the supervisor after a failure.                      |
| `passivatingState`         | Executing the passivation path (idle timeout).                    |
| `passivationPausedState`   | Passivation temporarily paused (e.g., during Watch or Reinstate). |
| `passivationSkipNextState` | One-shot guard to skip the next passivation check.                |
| `singletonState`           | This actor is a cluster singleton.                                |
| `relocationState`          | This actor may be relocated to another node.                      |
| `systemState`              | This is a system actor (guardian, dead letter, etc.).             |

### Lifecycle Transitions

```
                                   ┌──────────────┐
         newPID() + PreStart()     │              │
        ─────────────────────────► │   Running    │ ◄──── Reinstate (clear suspended)
                                   │              │
                                   └──┬───┬───┬───┘
                                      │   │   │
                  Shutdown() ─────────┘   │   └──────── Supervisor suspends
                  or passivation          │
                                          │
                 ┌────────────┐    ┌──────▼───────┐
                 │            │    │              │
                 │  Stopping  │    │  Suspended   │
                 │            │    │              │
                 └─────┬──────┘    └──────────────┘
                       │
          PostStop() + cleanup
                       │
                 ┌─────▼──────┐
                 │            │
                 │  Stopped   │
                 │            │
                 └────────────┘
```

**Key rules:**

- `IsRunning()` returns `true` only when `runningState` is set AND none of `stoppingState`, `passivatingState`, or `suspendedState` are set.
- A PID in the `suspendedState` does not process messages. It resumes only when the supervisor reinstates it.
- The `stopLocker` mutex ensures that concurrent `Shutdown()` calls are serialised — only one shutdown proceeds.

---

## Concurrency Model & Thread Safety Guarantees

### Single-threaded execution per actor

GoAkt guarantees that **only one goroutine processes messages for a given actor at any time**. This is the foundational concurrency invariant — actors never need internal locks to protect their own state.

**How it works:**

Each PID has a `processing` field (`atomic.Int32`) with two states: `idle` and `busy`.

1. When a message is enqueued, `process()` is called.
2. `process()` attempts `CompareAndSwap(idle, busy)`. If it fails, another goroutine is already processing — the call returns immediately (the running goroutine will pick up the new message).
3. If the CAS succeeds, a new goroutine is launched. It loops: dequeue a message, handle it via `Receive`, repeat.
4. When the mailbox is empty, the goroutine sets `processing` back to `idle`. It then checks if new messages arrived in the meantime. If so, it CAS-es back to `busy` and continues. Otherwise it exits.

This means the processing goroutine is **created on demand** and exits when the mailbox drains. There is no permanent goroutine per actor — just a supervision goroutine and the on-demand processor.

### Message ordering

Messages between a specific sender-receiver pair are delivered in the order they were sent (FIFO). This follows from the mailbox being a FIFO queue and the single-threaded processing guarantee.

### Goroutines per PID

| Goroutine            | Lifetime                                                      | Purpose                                                                |
|----------------------|---------------------------------------------------------------|------------------------------------------------------------------------|
| Message processor    | On-demand (created when mailbox has work, exits when drained) | Dequeues and dispatches messages sequentially.                         |
| Supervision listener | Entire PID lifetime                                           | Listens on `supervisionChan` for error signals and forwards to parent. |

The passivation manager is a **single shared goroutine** at the actor system level, not per-PID.

### Locks in PID

| Lock           | Type            | Protects                                                                                                                                                            |
|----------------|-----------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `fieldsLocker` | `sync.RWMutex`  | Actor reference, address, logger, behaviour stack, stash, reentrancy config, event stream, supervisor, passivation strategy, role, singleton spec, metric provider. |
| `stopLocker`   | `sync.Mutex`    | Shutdown sequence — ensures only one `Shutdown()` call proceeds.                                                                                                    |
| `state`        | `atomic.Uint32` | State flags (lock-free via CAS).                                                                                                                                    |
| `processing`   | `atomic.Int32`  | Processing state (lock-free via CAS).                                                                                                                               |

### How reentrancy relaxes the guarantee

By default (`reentrancy.Off`), the actor processes messages strictly one at a time. With reentrancy enabled:

- **`AllowAll`**: While the actor is waiting for an async `Request()` response, all incoming messages are processed normally. This means the actor's state may change between sending the request and receiving the response.
- **`StashNonReentrant`**: While a blocking request is in flight, regular user messages are stashed (buffered for later). Only async responses and system messages are processed. When the last outstanding request completes, stashed messages are unstashed and processed in order.

In both modes, the single-goroutine guarantee still holds — only one goroutine processes at a time. What changes is *which* messages are eligible for processing while a request is pending.

---

## Startup & Shutdown Sequence

### ActorSystem.Start()

The startup sequence is fail-fast — if any step errors, the system calls `shutdown()` to clean up anything already started and returns the error.

```
1.  Pre-checks
    └─ Verify system is not already started
    └─ Set "starting" flag

2.  Infrastructure setup
    └─ setupRemoting()    — validate and prepare remote config
    └─ setupCluster()     — validate and prepare cluster config

3.  Spawn guardian actors (sequential, each depends on the previous)
    └─ spawnRootGuardian()       — root of the actor tree
    └─ spawnSystemGuardian()     — parent of all system actors
    └─ spawnNoSender()           — placeholder for messages without a sender
    └─ spawnUserGuardian()       — parent of all user-spawned actors
    └─ spawnDeathWatch()         — tracks actor termination subscriptions
    └─ spawnDeadletter()         — captures messages to dead/missing actors
    └─ spawnSingletonManager()   — manages cluster singletons
    └─ spawnRelocator()          — handles actor relocation (cluster mode)
    └─ spawnTopicActor()         — pub/sub topic actor (if enabled)

4.  Start network services
    └─ startRemoteServer()           — TCP listener for remote messages (if enabled)
    └─ startCluster()                — join cluster membership (if enabled)
    └─ startDataCenterController()   — DC controller (if multi-DC enabled)
    └─ startDataCenterLeaderWatch()  — watch for DC leader changes

5.  Start background services
    └─ startMessagesScheduler()  — cron-like scheduled message delivery
    └─ passivator.Start()        — passivation manager (shared goroutine)
    └─ startEviction()           — periodic eviction of expired state

6.  Finalise
    └─ Set "started" flag, clear "starting" flag
    └─ Record start timestamp
    └─ Register OpenTelemetry metrics
```

### ActorSystem.Stop()

Shutdown is **best-effort** — it continues even if individual steps fail, collecting all errors. A configurable `shutdownTimeout` (default: 3 minutes) bounds the total shutdown time.

```
1.  Pre-checks
    └─ Verify system is started
    └─ Set "shuttingDown" flag

2.  Stop background services
    └─ Close eviction stop signal
    └─ Stop passivation manager
    └─ Stop message scheduler

3.  Run coordinated shutdown hooks
    └─ Execute registered hooks (with optional retry logic)

4.  Stop datacenter services
    └─ Stop DC leader watch
    └─ Stop DC controller

5.  Snapshot cluster state
    └─ Build peer state snapshot for persistence

6.  Shutdown user-facing actors (depth-first, children before parents)
    └─ User guardian    → all user actors
    └─ Singleton manager
    └─ Relocator
    └─ Dead letter
    └─ Death watch

7.  Deactivate all grains
    └─ Call OnDeactivate for each active grain

8.  Shutdown system actors
    └─ Topic actor
    └─ NoSender
    └─ System guardian
    └─ Root guardian

9.  Cleanup
    └─ Remove root guardian from actor tree
    └─ Close event stream

10. Shutdown cluster & remoting
    └─ Leave cluster, persist peer state
    └─ Stop TCP remoting server

11. Finalise
    └─ Reset system state
    └─ Flush logger
    └─ Return combined errors
```

---

## Cluster Consistency Model

### Actor & Grain Registry

The cluster registry (which actors/grains live on which node) is stored in an Olric distributed map (DMap) with **synchronous replication** and **configurable quorum**.

| Parameter            | Role                                                                     |
|----------------------|--------------------------------------------------------------------------|
| `replicaCount`       | Number of copies of each registry entry across nodes.                    |
| `writeQuorum`        | Minimum replicas that must acknowledge a write before it succeeds.       |
| `readQuorum`         | Minimum replicas that must agree on a read before it succeeds.           |
| `minimumPeersQuorum` | Minimum cluster members required before the registry accepts operations. |

When `readQuorum + writeQuorum > replicaCount`, the system prevents stale reads — a property known as **strong quorum consistency**. Read repair is enabled to heal divergent replicas in the background.

### Grain Activation: Exactly-Once Guarantee

Double-activation of a grain (two nodes activating the same identity simultaneously) is prevented at two levels:

1. **Cluster-level atomic claim.** `PutGrainIfAbsent` uses Olric's `NX` (not-exists) flag. Only the first node to write the grain record wins; subsequent attempts see the existing record and route to the owner node instead.

2. **Activation barrier.** During cluster bootstrap, membership may not have converged yet. The `grainActivationBarrier` blocks all grain activations until `minimumPeersQuorum` nodes have joined. Once the barrier opens (a closed channel), the check is effectively free.

### Network Partitions

During a partition, quorum rules apply:

- The **majority partition** (with enough nodes to satisfy quorum) continues operating normally.
- The **minority partition** fails reads and writes because quorum cannot be reached. Actors on minority-side nodes remain alive but cannot register new actors/grains or look up remote ones.
- When the partition heals, Memberlist detects the re-joined nodes, Olric rebalances data, and the cluster emits `NodeJoined` events.

There is no automatic split-brain resolution — the framework relies on quorum to prevent conflicting state.

### Node Failure & Relocation

When Memberlist detects a node departure:

1. The cluster emits a `NodeLeft` event.
2. The **leader node** fetches the departed node's peer state (list of actors and grains it owned) from the cluster store.
3. The leader sends a `Rebalance` message to the relocator actor.
4. The relocator distributes the departed node's actors and grains across remaining nodes:
   - Singleton actors always move to the leader.
   - Non-singleton, relocatable actors are spread across peers.
   - Grains are distributed evenly.
5. For each relocated actor/grain: the stale registry entry is removed, the actor is re-spawned on the target node, and the new location is registered.
6. A `RebalanceComplete` event is emitted.

Actors flagged as non-relocatable and system actors are skipped.

---

## Wire Protocol & Framing

### Frame Layout

Every message on the wire is a self-describing, length-prefixed binary frame. Two formats exist:

**Standard format (no metadata):**

```
┌──────────┬──────────┬────────────┬──────────────┐
│ totalLen │ nameLen  │ type name  │ proto bytes  │
│ 4 bytes  │ 4 bytes  │ N bytes    │ M bytes      │
│ uint32BE │ uint32BE │ UTF-8      │ marshalled   │
└──────────┴──────────┴────────────┴──────────────┘
```

**Extended format (with metadata — headers, deadlines):**

```
┌──────────┬──────────┬────────────┬──────────┬────────────┬──────────────┐
│ totalLen │ nameLen  │ type name  │ metaLen  │ metadata   │ proto bytes  │
│ 4 bytes  │ 4 bytes  │ N bytes    │ 4 bytes  │ K bytes    │ M bytes      │
│ uint32BE │ uint32BE │ UTF-8      │ uint32BE │ binary     │ marshalled   │
└──────────┴──────────┴────────────┴──────────┴────────────┴──────────────┘
```

- All integers are big-endian `uint32`.
- `totalLen` covers the entire frame including itself.
- `type name` is the fully-qualified protobuf message name (e.g., `goakt.RemoteTellRequest`), used for dynamic type resolution via the global protobuf registry.
- `metadata` carries request-scoped headers and deadlines for context propagation.
- Default maximum frame size: **16 MiB**. Frames exceeding this limit cause the connection to close.

### Connection Management

**Client side:** A LIFO pool of idle connections per endpoint. Default: 8 max idle connections, 30-second idle timeout. Stale connections are evicted lazily on acquisition. New connections are dialled on demand if the pool is empty. Connections are wrapped in order: raw TCP, then TLS (if configured), then compression.

**Server side:** Multiple accept loops (default: `GOMAXPROCS`-based) each call `AcceptTCP()` and dispatch accepted connections to a sharded worker pool. Connection structs are recycled via `sync.Pool`.

### Compression

Compression is applied as a connection-level wrapper — all data on the connection is compressed, not individual frames.

| Type      | Default | Library                   |
|-----------|---------|---------------------------|
| None      | No      | —                         |
| Gzip      | No      | `compress/gzip`           |
| Zstandard | **Yes** | `klauspost/compress/zstd` |
| Brotli    | No      | `andybalholm/brotli`      |

Both client and server must be configured with the same compression type. There is no in-band negotiation — compression is a deployment-time configuration choice.

### Worker Pool

The server uses a sharded worker pool for processing inbound connections:

- Shards: `GOMAXPROCS * 2` by default.
- Each shard maintains idle workers with atomic fast-path selection (two atomic pointer slots per shard before falling back to a mutex-guarded list).
- Workers are goroutines with a task channel. They execute the frame handler and return to idle.
- Idle workers are cleaned up after a configurable lifetime (default: 5 seconds for the server).

### Performance Optimisations

- **Frame buffer pooling.** Size-bucketed `sync.Pool` (256 B to 4 MiB) to avoid allocating per frame.
- **Zero-copy type extraction.** Uses `unsafe.String` to read the type name directly from the frame buffer without copying.
- **Single-allocation serialisation.** Frame size is pre-computed so the entire outbound frame is built in one allocation.
- **Stack-allocated header reads.** The 4-byte length prefix is read into a stack-allocated `[4]byte` array.

---

## How to Extend GoAkt

This section describes the steps to add common types of extensions. No code is shown — the goal is to tell you *which* interface to implement, *where* to put it, and *how* to wire it in.

### Adding a New Discovery Provider

1. **Create a new sub-package** under `discovery/` (e.g., `discovery/zookeeper/`).
2. **Implement the `discovery.Provider` interface** — six methods: `ID`, `Initialize`, `Register`, `Deregister`, `DiscoverPeers`, `Close`.
3. `DiscoverPeers` must return a list of peer addresses (`host:port` strings) that the cluster can connect to.
4. **Wire it in** by passing the provider to `ClusterConfig` when creating the actor system. The cluster wraps it into an Olric-compatible discovery adapter automatically. No changes to `internal/cluster` are needed.
5. **Add tests.** If the backend needs infrastructure, use testcontainers (see `discovery/consul/` and `discovery/etcd/` for patterns).

### Adding a New Mailbox Type

1. **Create a new file** in `actor/` (e.g., `actor/my_mailbox.go`).
2. **Implement the `Mailbox` interface** — five methods: `Enqueue`, `Dequeue`, `IsEmpty`, `Len`, `Dispose`.
3. `Enqueue` receives a `*ReceiveContext`. `Dequeue` returns the next one (or `nil` if empty). The mailbox must be safe for concurrent `Enqueue` calls from multiple goroutines, but `Dequeue` is only ever called from the single processing goroutine.
4. **Wire it in** by passing the mailbox via `WithMailbox(myMailbox)` as a `SpawnOption` when spawning an actor. No registration step is needed.
5. Look at `unbounded_mailbox.go` (simplest) and `unbounded_priority_mailbox.go` (priority ordering) as reference implementations.

### Adding a New Passivation Strategy

1. **Create a new file** in `passivation/` (e.g., `passivation/custom_strategy.go`).
2. **Implement the `passivation.Strategy` interface** — two methods: `Name` and `String`.
3. The strategy is a configuration type — it tells the passivation manager *when* to passivate. The actual passivation logic lives in `actor/passivation_manager.go`, which reads the strategy's fields to decide idle thresholds.
4. **Wire it in** by passing the strategy via `WithPassivationStrategy(myStrategy)` as a `SpawnOption`.

### Adding a New Control Plane Backend

1. **Create a new sub-package** under `datacenter/controlplane/` (e.g., `datacenter/controlplane/consul/`).
2. **Implement the `datacenter.ControlPlane` interface** — six methods: `Register`, `Heartbeat`, `SetState`, `ListActive`, `Watch`, `Deregister`.
3. The control plane manages datacenter registration, heartbeating, state transitions, and event watching. See `datacenter/controlplane/nats/` (NATS JetStream) and `datacenter/controlplane/etcd/` as reference implementations.
4. **Wire it in** by passing the control plane to the datacenter configuration when creating the actor system.

### Creating an Extension

1. **Implement the `extension.Extension` interface** — one method: `ID()` (must return a unique, alphanumeric identifier up to 255 characters).
2. Add any domain-specific methods to your concrete type (e.g., `RecordMetric`, `PersistEvent`). Actors retrieve the extension by ID and type-assert to access these methods.
3. **Wire it in** by passing extensions via `WithExtensions(myExtension)` as a system-level `Option` when creating the actor system. Actors access extensions through `ctx.Extension(id)` on their `Context`, `ReceiveContext`, or `GrainContext`.

### Creating a Dependency

1. **Implement the `extension.Dependency` interface** — requires `ID()` plus `MarshalBinary()` and `UnmarshalBinary()` from the standard `encoding` package.
2. Serialisability is mandatory because dependencies travel with actors during cluster relocation. The framework serialises them to the cluster registry and reconstructs them on the target node.
3. **Wire it in** by passing dependencies via `WithDependencies(myDep)` as a `SpawnOption` when spawning an actor. The actor receives them through `ctx.Dependencies()` in `PreStart`, `Receive`, and `PostStop`.

---

## Testing Strategy

### Test Layout

Tests are **co-located** with source code in `_test.go` files alongside the package they test. There is no separate `test/` directory for tests (the `test/` directory contains only generated protobuf fixtures).

### TestKit

The `testkit` package provides purpose-built helpers for actor-level testing:

- **`TestKit`** — creates and manages a throwaway actor system for a single test. Provides `Spawn`, `Kill`, `Subscribe`, and probe creation methods.
- **`Probe`** — a test actor that records received messages. Supports assertions like `ExpectMessage`, `ExpectMessageOfType`, `ExpectAnyMessage`, `ExpectNoMessage`, and `ExpectTerminated`, each with optional timeout variants.
- **`GrainProbe`** — similar to `Probe` but oriented toward grain (virtual actor) testing.
- **`MultiNodes`** — spins up a multi-node cluster in-process (with an embedded NATS server) for integration testing of cluster features. Provides `StartNode`, `SpawnProbe`, and coordinated `Stop`.

### Integration Tests with Infrastructure

Tests that need real infrastructure (Consul, etcd) use **testcontainers-go**. Examples:

- `discovery/consul/` — spins up a Consul container.
- `discovery/etcd/` — spins up an etcd container.
- `datacenter/controlplane/etcd/` — spins up etcd for control plane tests.

These tests run automatically in CI (Docker is required).

### Mocks

Generated by **mockery**. Pre-built mocks live in `mocks/` and cover core interfaces: `Remoting`, `Cluster`, `Hasher`, `Extension`, `Dependency`, `Provider`. Use them for unit tests that need to isolate a component from its dependencies.

### Conventions for Contributors

- Every new public type or behaviour change should have corresponding test coverage.
- Use `testkit.Probe` for message-level assertions rather than sleeping or polling.
- Use `testcontainers` for any test that needs external infrastructure — do not assume services are running on the host.
- Use the mocks in `mocks/` when testing components in isolation.
- Run `go test ./...` locally before submitting. CI runs the same suite.

---

## Glossary

| Term                   | Definition                                                                                                                                                                                                                                                                                                                                                |
|------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Actor**              | The fundamental unit of computation. An isolated entity that processes messages sequentially, maintains private state, and communicates exclusively through message passing. Defined by the `Actor` interface (`PreStart`, `Receive`, `PostStop`).                                                                                                        |
| **Grain**              | A virtual actor. Unlike regular actors, grains are identity-addressed and managed by the framework — automatically activated on first message and deactivated when idle. Defined by the `Grain` interface (`OnActivate`, `OnReceive`, `OnDeactivate`).                                                                                                    |
| **ActorSystem**        | The top-level runtime that hosts actors. Manages lifecycle, messaging, clustering, remoting, scheduling, and passivation. Created via `NewActorSystem`.                                                                                                                                                                                                   |
| **PID**                | Process Identifier. A live, in-memory handle to a running actor. Owns the mailbox, dispatch loop, metrics, and state. Used for all in-process interactions (Tell, Ask). Not serializable across nodes.                                                                                                                                                    |
| **ActorRef**           | A lightweight, serializable reference to an actor. Contains only the address (no mailbox or state). Used for cross-node references where the full PID is not available.                                                                                                                                                                                   |
| **Address**            | The canonical location string for an actor: `goakt://system@host:port/path/to/actor`. Uniquely identifies an actor across the cluster. Serializable and comparable.                                                                                                                                                                                       |
| **Mailbox**            | A per-actor message queue. Messages are enqueued by senders and dequeued by the actor's processing goroutine. Multiple implementations exist (unbounded, bounded, priority, fair, segmented).                                                                                                                                                             |
| **Guardian**           | A built-in actor that serves as a root of the actor tree. Three guardians exist: root (top of tree), system (parent of internal actors), and user (parent of user-spawned actors).                                                                                                                                                                        |
| **Supervisor**         | The parent actor's failure-handling policy. Defines what happens when a child fails: resume, restart, stop, or escalate. Configured via supervision strategies (`OneForOne`, `OneForAll`).                                                                                                                                                                |
| **Directive**          | A specific supervision action: `Resume` (ignore failure), `Restart` (re-initialise), `Stop` (terminate), `Escalate` (pass failure to grandparent).                                                                                                                                                                                                        |
| **Passivation**        | Automatic shutdown of idle actors to reclaim resources. Triggered by a configurable strategy (time-based, message-count-based, or long-lived/never).                                                                                                                                                                                                      |
| **Reentrancy**         | The ability for an actor to process other messages while waiting for an async request response. Modes: `Off` (strict sequential), `AllowAll` (all messages eligible), `StashNonReentrant` (stash regular messages, process only responses).                                                                                                               |
| **Stash**              | A side buffer where an actor can temporarily park messages for later processing. Useful during state transitions or when waiting for a precondition.                                                                                                                                                                                                      |
| **Dead Letter**        | A message sent to an actor that no longer exists or was never created. Captured by the dead-letter actor and published to the event stream.                                                                                                                                                                                                               |
| **Death Watch**        | Two related mechanisms: (1) The `Watch`/`UnWatch` API on PID lets any actor subscribe to another actor's termination and receive a `Terminated` message when the watched actor stops. (2) The `deathWatch` system actor handles cleanup — when it receives a `Terminated` signal, it removes the dead actor from the actor tree and the cluster registry. |
| **Cluster Singleton**  | An actor guaranteed to have exactly one instance running across the entire cluster. If the hosting node departs, the singleton is relocated to the leader.                                                                                                                                                                                                |
| **Relocation**         | The process of re-spawning an actor on a different node after its original host departs the cluster. Controlled by the relocator actor on the leader node.                                                                                                                                                                                                |
| **Discovery Provider** | A pluggable backend that tells the cluster how to find peer nodes. Implementations exist for Consul, etcd, Kubernetes, NATS, mDNS, DNS-SD, and static configuration.                                                                                                                                                                                      |
| **Control Plane**      | The multi-datacenter coordination layer. Manages DC registration, heartbeating, state transitions, and cross-DC event propagation. Implementations exist for NATS JetStream and etcd.                                                                                                                                                                     |
| **Extension**          | A system-wide plugin registered at `ActorSystem` creation time. Provides cross-cutting capabilities (e.g., event sourcing, metrics, tracing) accessible from any actor's context via `Extension(id)`. The interface requires only `ID()`; actors type-assert to access domain-specific methods.                                                           |
| **Dependency**         | A per-actor resource injected at spawn time. Must be serialisable (`BinaryMarshaler`/`BinaryUnmarshaler`) so it can travel with the actor during cluster relocation. Typical examples: database clients, API clients, configuration providers. Accessed via `Dependencies()` on the actor's context.                                                      |
| **Event Stream**       | An in-process pub/sub bus. System events (actor started/stopped, cluster events, dead letters) and custom application events are published here. Subscribers receive events via Go channels.                                                                                                                                                              |
