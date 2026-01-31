# Multi-DC Design

This document describes the multi-datacenter (multi-DC) design for GoAkt using an isolation-only model.
Each datacenter runs an independent GoAkt cluster. There is no global cluster state shared between datacenters.
Cross-DC communication is DC-transparent to callers and relies on a DC control plane (registry) for clean,
efficient routing.

## Goals

- Preserve isolation: each datacenter remains a standalone GoAkt cluster.
- Provide DC-transparent messaging (callers do not select a DC explicitly).
- Use the DC control plane registry as the source of truth for routing.
- Support DC-aware placement (e.g. SpawnOn with WithDataCenter) without global state.
- Periodically report liveness to keep routing tables accurate.

## Non-goals

- Global cluster membership or consensus across datacenters.
- Transparent global actor identity with shared state.
- Strong consistency across datacenters.

## Current Implementation Scope

- Control plane interface and manager lifecycle (register, heartbeat, cache refresh).
- NATS JetStream and Etcd control plane providers with watch support.
- DC leader is the sole writer for its DataCenterRecord; followers do not run the manager.
- Lease-based liveness and TTL expiry enforced by the control plane provider.
- Leader-local cache of ACTIVE records with staleness reporting and watch/poll refresh.
- DC-transparent message routing via discovery-based approach (SendAsync/SendSync methods).

## Key Concepts

- DataCenter metadata
  - Fields: Name, Region, Zone, Labels.
  - Endpoints are configured in datacenter.Config.Endpoints and drive cross-DC messaging and spawning.
  - Labels can drive routing and placement policy (for example: tier=prod, latency=low).
- DC control plane (registry interface)
  - Pluggable interface, following the discovery engine pattern.
  - Built-in implementations: NATS JetStream (see `datacenter/controlplane/nats`), Etcd (see `datacenter/controlplane/etcd`).
  - Stores DataCenter records, endpoints, and liveness TTLs/leases.

## Architecture

The DC control plane registry is the single routing authority across datacenters. Each DC registers itself and
maintains a heartbeat. Routing decisions rely on the registry’s view of live DCs.

Diagram: multi-DC routing

```
+------------------+        register/heartbeat        +----------------------+
| DC A cluster     | <------------------------------> | DC control plane     |
| nodes + leader   |                                  | (pluggable backend)  |
+------------------+                                  +----------------------+
        |                                                      |
        | DC-transparent message                               |
        | resolve via registry                                 |
        v                                                      v
+------------------+                                  +------------------+
| DC B cluster     | <------------------------------> | DC C cluster     |
| nodes + leader   |          registry lookup         | nodes + leader   |
+------------------+                                  +------------------+
```

## DC Control Plane Responsibilities

- Accept DataCenter registrations from DC leaders.
- Maintain a liveness TTL for each DC and expire stale entries.
- Serve queries and watches for active DCs and endpoints.
- Provide minimal metadata required for routing and placement.

## DC Control Plane Design (Lifecycle-Oriented)

The DC control plane is a lightweight registry component that tracks the lifecycle of datacenters
and provides a stable routing source of truth.

Design Principles

```
- Control plane is a control-only dependency; local DC operation continues if it is unavailable.
- Each DC leader is the sole writer for its DataCenterRecord to avoid write contention.
- Routing is deterministic for the same inputs (policy + registry snapshot).
- Liveness is lease-based and time-bounded to prevent stale routing.

Data Model
----------
- DataCenterRecord
  - ID: stable identifier (immutable); default is DataCenter.ID() (Zone+Region+Name) or Name if empty.
  - Name, Region, Zone, Labels.
  - Endpoints: advertised address list for DC gateways or leaders.
  - State: one of REGISTERED, ACTIVE, DRAINING, INACTIVE.
  - LeaseExpiry: liveness lease expiry time (set by the provider).
  - Version: monotonic revision for conflict-free updates.

Lifecycle State Machine
-----------------------
```

- REGISTERED: metadata accepted, waiting for first heartbeat.
- ACTIVE: heartbeats are current, eligible for routing.
- DRAINING: no new placements; used during graceful shutdown (actor system stop).
- INACTIVE: lease expired or explicitly disabled; excluded from routing.

Transitions (all driven by actor system lifecycle or control plane TTL):

- REGISTERED -> ACTIVE: first valid heartbeat (on actor system start).
- ACTIVE -> DRAINING: on actor system shutdown (DC leader/controller stops).
- ACTIVE -> INACTIVE: TTL expiry (heartbeats stopped) or explicit disable.
- DRAINING -> INACTIVE: on actor system shutdown (controller transitions to INACTIVE after DRAINING) or TTL expiry.

DRAINING and INACTIVE are not exposed as separate operator APIs; they are controlled solely by actor system start and shutdown. This keeps lifecycle simple and avoids split control between runtime and external operators.

````

Control Plane Interface (Minimal and Efficient)
-----------------------------------------------
The runtime integrates with the control plane via a narrow interface, matching the discovery engine
model so providers can be swapped via configuration without changing runtime code.

- Register(DataCenterRecord) -> RecordID, Version
- Heartbeat(RecordID, Version) -> NewVersion, LeaseExpiry
- SetState(RecordID, State, Version) -> NewVersion
- ListActive() -> list of ACTIVE records (sorted by revision)
- Watch() -> streaming updates (optional for providers that support it)

Built-in Implementations
-------------------------
- NATS JetStream (datacenter/controlplane/nats).
- Etcd (datacenter/controlplane/etcd).

Recommended Providers (Current)
--------------------------------
- NATS JetStream.
- Etcd.
- Consul (planned).
- PostgreSQL (planned).

Control Plane Interface Shape
-----------------------------
The control plane is exposed as a small interface, consistent with the discovery engine pattern.
This keeps provider swaps configuration-only.

```go
type ControlPlane interface {
Register(ctx, record) (id, version, error)
Heartbeat(ctx, id, version) (newVersion, leaseExpiry, error)
SetState(ctx, id, state, version) (newVersion, error)
ListActive(ctx) ([]record, error)
Watch(ctx) (stream, error)
}
```
````

## Control Plane Contract Guarantees

- Register, SetState, and Heartbeat are linearizable per record (CAS by Version).
- Heartbeat extends the lease only when Version is current; stale versions are rejected.
- ListActive returns only ACTIVE records with unexpired leases.
- Watch emits ordered updates by Version and is at-least-once (clients de-dupe by Version).

## Configuration (Provider Selection)

The provider is wired by passing a ControlPlane implementation into `datacenter.Config`.

Example (etcd):

```

etcdConfig := &etcd.Config{
Endpoints: []string{"127.0.0.1:2379"},
TTL: 30 \* time.Second,
}
cp, \_ := etcd.NewControlPlane(etcdConfig)

cfg := datacenter.NewConfig()
cfg.ControlPlane = cp

```

Provider-specific settings live in the provider's Config type (for example,
`datacenter/controlplane/etcd.Config` or `datacenter/controlplane/nats.Config`).

## Consistency and Concurrency

- Versioned writes (compare-and-swap) prevent stale updates.
- All state changes are idempotent; callers can retry safely.
- Reads may be eventually consistent; clients cache with TTL and revalidate.

## Efficiency Considerations

- Heartbeats are small, write-optimized (only lease + version update).
- ListActive is optimized for routing (single query, no joins).
- Watch reduces polling when supported; polling remains the fallback.
- TTL expiry is handled by the registry backend to avoid extra GC loops.
- Each DC leader maintains a local cache of active DCs (TTL-based) and
  refreshes periodically to avoid registry calls on every request.

## Cache and Refresh Strategy

- The DC leader owns a local registry cache; followers do not run the manager.
- Cache refresh uses jittered intervals to avoid thundering herds.
- Callers can inspect cache staleness to decide fallback behavior; cross-DC routing and placement use this cache.
- Watch-based refresh is used when supported; polling still runs and merges when watch is active, otherwise it replaces the cache.

## Current Operational Defaults

- Heartbeat interval: 10s with 10% jitter.
- Cache refresh interval: 10s with 10% jitter.
- Leader check interval: 5s.
- Maximum cache staleness: 30s (stale flag may affect cross-DC routing and placement).
- Control plane request timeout: 3s.
- Watch enabled: true.
- Max backoff: 30s.
- Lease TTL: provider-specific; must be set in the control plane config.

## Failure Handling

- Leader failover in a DC continues heartbeats from the new leader.
- Temporary registry outages are tolerated via client-side caching.
- Registry unavailability does not block local DC operations; it only
  affects control plane updates and any cross-DC routing built on the cache.

## Security and Governance

Recommended practices (provider-specific, not enforced by the core runtime):

- mTLS between DC leaders and registry.
- Per-DC credentials scoped to its own record.
- Inter-DC communication is fully secured using HTTP/3 over quic-go.

## Routing and DC-Transparent Messaging

Status: implemented. The runtime uses the control plane cache for discovery-based routing.

DC-transparent messaging means callers address logical actor/grain identities without choosing a DC.
The runtime discovers which DC contains the target actor by querying active DCs in parallel.

## Current Implementation (Discovery-Based Routing):

The current implementation uses a discovery-based approach that queries all active DCs to locate
the target actor. This approach is well-suited for the current state where actors may exist in
any DC and deterministic placement policies are not yet implemented.

Routing flow:

1. Local runtime first attempts to find the actor in the local datacenter using ActorOf.
2. If not found locally, the runtime retrieves the list of active DCs from the control plane cache.
3. The runtime queries all active DC endpoints in parallel using RemoteLookup to discover
   which DC contains the target actor.
4. The first DC that responds with a valid actor address is selected.
5. Remaining pending lookups are cancelled to avoid unnecessary network traffic.
6. The message is sent via remote messaging to the discovered DC endpoint.
7. The receiving DC resolves the actor locally and delivers the message.

Implementation details:

- Parallel queries: All active DCs are queried concurrently for low latency.
- Early cancellation: Once a match is found, remaining lookups are cancelled.
- Timeout protection: Lookups are bounded by a timeout (default 5 seconds) to avoid hanging.
- Stale cache handling: The implementation uses cached DC records but may proceed with
  stale data if the cache hasn't been refreshed recently (configurable behavior).

## DC-aware placement (implemented)

- SpawnOn accepts WithDataCenter(target) to spawn an actor on a node in a different data center.
- The runtime looks up the target DC in the control plane cache, then sends RemoteSpawn to one of
  that DC's advertised Endpoints chosen at random. Which nodes are eligible is determined by
  the target DC's datacenter.Config.Endpoints (leader-only vs all nodes).
- There is no built-in relocation of actors/grains across DCs.

## Liveness and Health Checks

- Each DC leader periodically updates its liveness in the registry.
- Liveness uses TTL-based heartbeats; absence of heartbeat marks the DC as inactive.
- Routing only considers active DCs. Inactive DCs are removed from routing tables.

## Sequence Flows

```

DC registration and heartbeat (implemented):

1. DC leader starts and registers DataCenter metadata and endpoints in the registry.
2. DC leader sends heartbeats at a fixed interval (less than TTL).
3. Registry expires the DC entry if heartbeats stop.

DC-transparent message delivery (implemented):

1. Actor A calls SendAsync/SendSync with Actor B's name (no DC specified).
2. Runtime first attempts to find Actor B in the local datacenter using ActorOf.
3. If not found locally, runtime retrieves active DCs from the control plane cache.
4. Runtime queries all active DC endpoints in parallel using RemoteLookup.
5. First DC that responds with Actor B's address is selected.
6. Remaining lookups are cancelled.
7. Message is sent to the discovered DC using remote messaging.
8. Receiving DC resolves Actor B locally and delivers the message.
```
