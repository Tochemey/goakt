# Multi-DC Design

This document describes the multi-datacenter (multi-DC) design for GoAkt using an isolation-only model.
Each datacenter runs an independent GoAkt cluster. There is no global cluster state shared between datacenters.
Cross-DC communication is DC-transparent to callers and relies on a DC control plane (registry) for clean,
efficient routing.

## Goals

- Preserve isolation: each datacenter remains a standalone GoAkt cluster.
- Provide DC-transparent messaging (callers do not select a DC explicitly).
- Use the DC control plane registry as the source of truth for routing.
- Support DC-aware placement and controlled relocation without global state.
- Periodically report liveness to keep routing tables accurate.

## Non-goals

- Global cluster membership or consensus across datacenters.
- Transparent global actor identity with shared state.
- Strong consistency across datacenters.

## Current Implementation Scope

- Control plane interface and manager lifecycle (register, heartbeat, cache refresh).
- Etcd control plane provider with watch support.
- DC leader is the sole writer for its DataCenterRecord; followers do not run the manager.
- Lease-based liveness and TTL expiry enforced by the control plane provider.
- Leader-local cache of ACTIVE records with staleness reporting and watch/poll refresh.

## Key Concepts

- DataCenter metadata
  - Fields: Name, Region, Zone, Labels.
  - Endpoints are configured separately for routing (multidatacenter.Config.Endpoints).
  - Labels drive routing and placement policy (for example: tier=prod, latency=low).
- Node association
  - Each discovery.Node references a DataCenter and includes it in its String() output.
- DC control plane (registry interface)
  - Pluggable interface, following the discovery engine pattern.
  - Built-in implementation: Etcd (see `multidatacenter/controplane/etcd`); others are planned.
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
~~~~~~~~~~
- DataCenterRecord
  - ID: stable identifier (immutable); default is DataCenter.ID() (Zone+Region+Name) or Name if empty.
  - Name, Region, Zone, Labels.
  - Endpoints: advertised address list for DC gateways or leaders.
  - State: one of REGISTERED, ACTIVE, DRAINING, INACTIVE.
  - LeaseExpiry: liveness lease expiry time (set by the provider).
  - Version: monotonic revision for conflict-free updates.

Lifecycle State Machine
```

- REGISTERED: metadata accepted, waiting for first heartbeat.
- ACTIVE: heartbeats are current, eligible for routing.
- DRAINING: no new placements; used for planned maintenance or migration.
- INACTIVE: lease expired or explicitly disabled; excluded from routing.

Transitions:

- REGISTERED -> ACTIVE: first valid heartbeat.
- ACTIVE -> DRAINING: operator action or policy.
- ACTIVE -> INACTIVE: TTL expiry or explicit disable.
- DRAINING -> INACTIVE: operator action or TTL expiry.

Control Plane Interface (Minimal and Efficient)

````
The runtime integrates with the control plane via a narrow interface, matching the discovery engine
model so providers can be swapped via configuration without changing runtime code.

- Register(DataCenterRecord) -> RecordID, Version
- Heartbeat(RecordID, Version) -> NewVersion, LeaseExpiry
- SetState(RecordID, State, Version) -> NewVersion
- ListActive() -> list of ACTIVE records (sorted by revision)
- Watch() -> streaming updates (optional for providers that support it)

Built-in Implementations
-------------------------
- Etcd (currently implemented).

Recommended Providers (Current)
--------------------------------
- Etcd.
- Consul (planned).
- NAS (planned).
- PostgreSQL (planned).

Control Plane Interface Shape
-----------------------------
The control plane is exposed as a small interface, consistent with the discovery engine pattern.
This keeps provider swaps configuration-only.

```
type ControlPlane interface {
    Register(ctx, record) (id, version, error)
    Heartbeat(ctx, id, version) (newVersion, leaseExpiry, error)
    SetState(ctx, id, state, version) (newVersion, error)
    ListActive(ctx) ([]record, error)
    Watch(ctx) (stream, error)
}
```

Control Plane Contract Guarantees
---------------------------------
- Register, SetState, and Heartbeat are linearizable per record (CAS by Version).
- Heartbeat extends the lease only when Version is current; stale versions are rejected.
- ListActive returns only ACTIVE records with unexpired leases.
- Watch emits ordered updates by Version and is at-least-once (clients de-dupe by Version).

Configuration (Provider Selection)
-----------------------------------
The provider is wired by passing a ControlPlane implementation into `multidatacenter.Config`.

Example (etcd):
```
etcdConfig := &etcd.Config{
    Endpoints: []string{"127.0.0.1:2379"},
    TTL:       30 * time.Second,
}
cp, _ := etcd.NewControlPlane(etcdConfig)

mdc := multidatacenter.NewConfig()
mdc.ControlPlane = cp
```

Provider-specific settings live in the provider's Config type (for example,
`multidatacenter/controplane/etcd.Config`).

Consistency and Concurrency
-----------------------------
- Versioned writes (compare-and-swap) prevent stale updates.
- All state changes are idempotent; callers can retry safely.
- Reads may be eventually consistent; clients cache with TTL and revalidate.

Efficiency Considerations
-------------------------
- Heartbeats are small, write-optimized (only lease + version update).
- ListActive is optimized for routing (single query, no joins).
- Watch reduces polling when supported; polling remains the fallback.
- TTL expiry is handled by the registry backend to avoid extra GC loops.
- Each DC leader maintains a local cache of active DCs (TTL-based) and
  refreshes periodically to avoid registry calls on every request.

Cache and Refresh Strategy
--------------------------
- The DC leader owns a local registry cache; followers do not run the manager.
- Cache refresh uses jittered intervals to avoid thundering herds.
- Callers can inspect cache staleness to decide fallback behavior; cross-DC routing is not yet wired.
- Watch-based refresh is used when supported; polling still runs and merges when watch is active, otherwise it replaces the cache.

Current Operational Defaults
----------------------------
- Heartbeat interval: 10s with 10% jitter.
- Cache refresh interval: 10s with 10% jitter.
- Leader check interval: 5s.
- Maximum cache staleness: 30s (stale flag only; no routing behavior yet).
- Control plane request timeout: 3s.
- Watch enabled: true.
- Max backoff: 30s.
- Lease TTL: provider-specific; must be set in the control plane config.

Failure Handling
-----------------
- Leader failover in a DC continues heartbeats from the new leader.
- Temporary registry outages are tolerated via client-side caching.
- Registry unavailability does not block local DC operations; it only
  affects control plane updates and any cross-DC routing built on the cache.

Security and Governance
-----------------------
Recommended practices (provider-specific, not enforced by the core runtime):
- mTLS between DC leaders and registry.
- Per-DC credentials scoped to its own record.
- Audit log for state changes (REGISTER, DRAINING, INACTIVE).
- Inter-DC communication is fully secured using HTTP/3 over quic-go.

Routing and DC-Transparent Messaging (Planned)
----------------------------------------------
Status: planned. The current runtime does not yet use the control plane cache for routing.

DC-transparent messaging means callers address logical actor/grain identities without choosing a DC.
The runtime resolves the target DC via a routing policy and the registry’s live DC list.

Routing policy (example behaviors):
- Consistent hashing across live DCs by actor/grain ID.
- Label-based filtering (for example: route only to tier=prod).
- Region preference with fallback order.

Policy evaluation order (MVP):
1) Filter to ACTIVE DCs with valid leases.
2) Apply label constraints (hard filters).
3) Apply region/zone preference (soft ranking).
4) Select via consistent hashing for stability.

Routing flow:
1) Local runtime uses the DC leader's cached view of active DCs (TTL-based),
   which is refreshed periodically from the registry.
2) The routing policy selects a target DC deterministically.
3) The message is sent via remote messaging to the selected DC endpoint.
4) The receiving DC resolves the actor/grain locally and delivers the message.

Placement and Relocation (Planned)
----------------------------------
Status: planned. The current runtime does not yet relocate actors/grains across DCs.

- Placement is DC-aware and derived from the same routing policy used for messaging.
- Relocation is explicit and controlled:
  - Mark source DC (or actor group) as DRAINING in the control plane.
  - Create the actor/grain in the target DC.
  - Drain or stop the source instance.
  - Subsequent routing directs traffic to the new DC via the policy.

Liveness and Health Checks
--------------------------
- Each DC leader periodically updates its liveness in the registry.
- Liveness uses TTL-based heartbeats; absence of heartbeat marks the DC as inactive.
- Routing only considers active DCs. Inactive DCs are removed from routing tables.

Sequence Flows (Planned)
------------------------
Status: planned. These flows describe the intended interactions.
DC registration and heartbeat:
1) DC leader starts and registers DataCenter metadata and endpoints in the registry.
2) DC leader sends heartbeats at a fixed interval (less than TTL).
3) Registry expires the DC entry if heartbeats stop.

DC-transparent message delivery:
1) Actor A sends a message to Actor B (no DC specified).
2) Runtime fetches live DCs from the registry (or uses cached view).
3) Routing policy selects DC X for Actor B.
4) Message is sent to DC X using remote messaging.
5) DC X delivers the message to Actor B locally.

Controlled relocation:
1) Operator or policy triggers relocation of Actor B to DC Y.
2) Source DC (or placement group) is set to DRAINING.
3) DC Y creates Actor B and becomes the new target per routing policy.
4) Source DC stops Actor B after drain.
5) Subsequent messages route to DC Y.
````
