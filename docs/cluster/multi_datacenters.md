# Multi-Datacenter

GoAkt supports deploying actor systems across multiple datacenters (DCs). With multi-DC enabled you can:

- **Send messages to actors in other datacenters** — Use `SendAsync` / `SendSync` (by actor name) or `RemoteTell` / `RemoteAsk` (by address); discovery and remoting handle cross-DC routing.
- **Create actors in other datacenters** — Use `SpawnOn` with `WithDataCenter` to place an actor in a specific DC.

**Prerequisites:** Cluster mode with discovery and remoting enabled. Endpoints for cross-DC routing are **not** configured manually; the cluster leader in each DC registers its members’ remoting addresses with the control plane.

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Configuration](#configuration)
- [Creating Actors on Another Datacenter](#creating-actors-on-another-datacenter)
- [Sending Messages to Actors in Other Datacenters](#sending-messages-to-actors-in-other-datacenters)
- [Best Practices](#best-practices)
- [Error Handling](#error-handling)

---

## Overview

Multi-datacenter support relies on:

1. **Control plane** — A shared coordination service (NATS JetStream or etcd) where each datacenter registers and where others discover active DCs.
2. **Local cluster** — Within each datacenter, nodes form a cluster using normal discovery and remoting.
3. **Datacenter registration** — The **cluster leader** in each DC registers a datacenter record with the control plane. The record’s endpoints are the **remoting addresses of current cluster members**; no endpoint list is configured by the user.
4. **Cross-DC operations** — Spawn uses the target DC’s registered endpoints to send a remote spawn; Tell/Ask use the same cache to discover and message actors in other DCs.

## Architecture

```
Datacenter 1 (us-east-1)              Datacenter 2 (eu-west-1)
┌─────────────────────────┐          ┌─────────────────────────┐
│  Node1   Node2   Node3  │          │  Node4   Node5   Node6  │
│    ↕       ↕       ↕    │          │    ↕       ↕       ↕    │
│  Local Cluster          │          │  Local Cluster          │
└───────────┬─────────────┘          └───────────┬─────────────┘
            │                                    │
            └────────────────┬───────────────────┘
                             │
                    ┌────────▼───────┐
                    │ Control Plane  │
                    │  (NATS/etcd)   │
                    └────────────────┘
```

- Each datacenter runs a local cluster with its own discovery.
- The **leader** of that cluster registers the DC with the control plane and periodically heartbeats. The endpoints in the record are the remoting addresses of `cluster.Members()` at registration (and are updated when membership changes).
- All DCs use the control plane to discover other active datacenters and their endpoints.

## Configuration

### Datacenter metadata

Define the **local** datacenter’s identity with `datacenter.DataCenter`. Target DCs use the same metadata when spawning or routing. `Name` is required; `Region`, `Zone`, and `Labels` are optional.

```go
import "github.com/tochemey/goakt/v3/datacenter"

dc := datacenter.DataCenter{
    Name:   "dc-us-east-1",
    Region: "us-east-1",
    Zone:   "us-east-1a",
    Labels: map[string]string{"env": "production"},
}
```

Routing and placement match on `DataCenter.ID()` (computed from Zone + Region + Name). Use the same identity when targeting that DC with `WithDataCenter` or when reasoning about routing.

### Control plane

You must choose and configure a control plane implementation.

#### NATS JetStream

```go
import "github.com/tochemey/goakt/v3/datacenter/controlplane/nats"

controlPlane, err := nats.NewControlPlane(&nats.Config{
    URL:            "nats://nats-server:4222",
    Bucket:         "goakt_datacenters",
    TTL:            30 * time.Second,
    Timeout:        5 * time.Second,
    ConnectTimeout: 5 * time.Second,
})
```

NATS must have JetStream enabled and be reachable from all DCs.

#### etcd

```go
import "github.com/tochemey/goakt/v3/datacenter/controlplane/etcd"

controlPlane, err := etcd.NewControlPlane(&etcd.Config{
    Endpoints:   []string{"http://etcd1:2379", "http://etcd2:2379"},
    Namespace:   "/goakt/datacenters",
    TTL:         30 * time.Second,
    DialTimeout: 5 * time.Second,
    Timeout:     5 * time.Second,
})
```

### Datacenter config (no endpoints)

Multi-DC config does **not** include an endpoint list; the leader supplies endpoints from the cluster.

```go
dcConfig := datacenter.NewConfig()
dcConfig.ControlPlane = controlPlane
dcConfig.DataCenter = datacenter.DataCenter{
    Name:   "dc-us-east-1",
    Region: "us-east-1",
    Zone:   "us-east-1a",
}
// Optional: override defaults
dcConfig.HeartbeatInterval = 10 * time.Second
dcConfig.CacheRefreshInterval = 10 * time.Second
dcConfig.MaxCacheStaleness = 30 * time.Second
dcConfig.LeaderCheckInterval = 5 * time.Second
dcConfig.RequestTimeout = 5 * time.Second
dcConfig.WatchEnabled = true
dcConfig.FailOnStaleCache = true
```

| Field                  | Description                                                    |
|------------------------|----------------------------------------------------------------|
| `ControlPlane`         | Control plane implementation (required).                       |
| `DataCenter`           | Local DC metadata; `Name` required.                            |
| `HeartbeatInterval`    | Liveness heartbeat frequency (default 10s).                    |
| `CacheRefreshInterval` | How often the DC cache is refreshed (default 10s).             |
| `MaxCacheStaleness`    | Max acceptable cache age for cross-DC ops (default 30s).       |
| `LeaderCheckInterval`  | How often leader status is rechecked (default 5s).             |
| `RequestTimeout`       | Control plane operation timeout (default 5s).                  |
| `WatchEnabled`         | Use watch-based cache updates when supported (default true).   |
| `FailOnStaleCache`     | If true, cross-DC ops fail when cache is stale (default true). |

### Attaching to the cluster

Attach the datacenter config to the cluster config. Use a discovery provider for the **local** cluster (see [Discovery](discovery.md)); remoting must be enabled so each node’s remoting address can be registered as part of the DC record when this node is leader.

```go
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(discoveryProvider).
    WithDiscoveryPort(3320).
    WithPeersPort(3322).
    WithMinimumPeersQuorum(2).
    WithKinds(new(MyActor)).
    WithDataCenter(dcConfig)

system, err := actor.NewActorSystem(
    "my-system",
    actor.WithCluster(clusterConfig),
    actor.WithRemote("node1.us-east-1.example.com", 3321),
)
```

The cluster leader registers the local DC with the control plane; endpoints are taken from current cluster members’ remoting addresses, not from config.

---

## Creating Actors on Another Datacenter

Use `SpawnOn` with `actor.WithDataCenter` to create an actor in a specific datacenter. The target is identified by a `*datacenter.DataCenter`; its `ID()` (Zone + Region + Name) must match the record that DC’s leader registered with the control plane.

```go
targetDC := &datacenter.DataCenter{
    Name:   "dc-eu-west-1",
    Region: "eu-west-1",
}

err := system.SpawnOn(
    ctx,
    "user-data-processor",
    new(DataProcessorActor),
    actor.WithDataCenter(targetDC),
)
if err != nil {
    log.Fatal(err)
}
```

**Behavior:** The system checks the datacenter controller is ready (`ErrDataCenterNotReady` if not), loads active DC records from the control plane (`ErrDataCenterStaleRecords` if cache is stale and `FailOnStaleCache` is true), finds a record whose ID matches the target (`ErrDataCenterRecordNotFound` if none), then sends a remote spawn to one of that record’s endpoints. The actor kind must be registered in the target DC’s cluster (e.g. `WithKinds()`); endpoints are the remoting addresses registered by that DC’s leader.

---

## Sending Messages to Actors in Other Datacenters

You can send messages to actors in other datacenters by actor name or by address.

### By actor name (PID)

When you have a `*actor.PID` (e.g. from `ctx.Self()` or `system.ActorOf`), use `SendAsync` for fire-and-forget or `SendSync` for request-response. Both resolve the actor locally first, then discover across active DCs if not found, and send via remoting.

```go
// Fire-and-forget
err := pid.SendAsync(ctx, "user-data-processor", message)

// Request-response (returns proto.Message; use type assertion or UnmarshalNew on anypb)
reply, err := pid.SendSync(ctx, "user-data-processor", request, 10*time.Second)
```

If the DC cache is stale and `FailOnStaleCache` is true, cross-DC discovery can return `ErrDataCenterStaleRecords`; otherwise the implementation may proceed with best-effort (stale) cache.

### By address (when you already have it)

From inside `Receive`, if you already have the remote `*address.Address`, use:

```go
ctx.RemoteTell(addr, message)
anyResp := ctx.RemoteAsk(addr, message, timeout) // returns *anypb.Any; use UnmarshalNew() for proto.Message
```

### Grains

Grain `Tell` and `Ask` fan out to active DC endpoints and use the first successful response. Use the grain API as usual; the system routes across datacenters.

---

## Best Practices

1. **Control plane and discovery**  
   Use a local discovery (e.g. NATS) for the cluster inside each DC, and a separate control plane (NATS or etcd) shared across DCs. Ensure all DCs can reach the control plane.

2. **Datacenter identity**  
   Use a stable `Name` (and optionally `Region`/`Zone`) and the same values when targeting that DC with `WithDataCenter` or when reasoning about routing. `ID()` is zone+region+name.

3. **Timeouts**  
   Cross-DC calls are slower than local. Use larger timeouts for Ask and control plane–related settings where appropriate.

4. **Stale cache**  
   `FailOnStaleCache: true` (default) fails cross-DC operations when the cache is older than `MaxCacheStaleness`, which avoids using outdated endpoint lists. Set to `false` only if you prefer best-effort routing over strict consistency.

5. **Actor kinds**  
   Any kind you spawn on another DC must be registered there (e.g. same `WithKinds()` in that DC’s cluster config).

6. **Remoting**  
   Endpoints are cluster members’ remoting addresses. Ensure remoting is correctly configured (`WithRemote`) and that those addresses are reachable from other datacenters (firewall, DNS, etc.).

---

## Error Handling

Cross-DC spawn and discovery can return:

```go
err := system.SpawnOn(ctx, "actor", new(MyActor), actor.WithDataCenter(targetDC))
if err != nil {
    switch {
    case errors.Is(err, gerrors.ErrDataCenterNotReady):
        // Multi-DC not ready (e.g. controller not started yet)
    case errors.Is(err, gerrors.ErrDataCenterStaleRecords):
        // Cache older than MaxCacheStaleness and FailOnStaleCache is true
    case errors.Is(err, gerrors.ErrDataCenterRecordNotFound):
        // No active record for target DC (wrong name/region/zone or DC down)
    case errors.Is(err, gerrors.ErrTypeNotRegistered):
        // Actor kind not registered in target DC
    default:
        // Remoting or other errors
    }
}
```

If the control plane is down, existing actors and intra-DC traffic keep working; new cross-DC spawns and discovery can fail or use stale cache depending on `FailOnStaleCache`.