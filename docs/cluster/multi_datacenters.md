# Multi-Datacenter

GoAkt supports deploying actor systems across multiple datacenters (DCs). With multi-DC enabled you can:

- **Send messages to actors in other datacenters** — Tell and Ask work across DCs via remoting.
- **Create actors in other datacenters** — Spawn actors on a specific DC using `SpawnOn` with `WithDataCenter`.

Endpoints used for cross-DC routing are **not** set in configuration. They are derived automatically from each datacenter’s cluster members (remoting addresses). The cluster leader in each DC registers those endpoints with the control plane.

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Configuration](#configuration)
- [Creating Actors on Another Datacenter](#creating-actors-on-another-datacenter)
- [Sending Messages to Actors in Other Datacenters](#sending-messages-to-actors-in-other-datacenters)
- [Best Practices](#best-practices)
- [Error Handling](#error-handling)
- [Next Steps](#next-steps)

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

You define the **local** datacenter’s identity. Target DCs are identified by the same metadata when spawning or routing.

```go
import "github.com/tochemey/goakt/v3/datacenter"

dc := datacenter.DataCenter{
    Name:   "dc-us-east-1",   // Required: datacenter identifier
    Region: "us-east-1",      // Optional
    Zone:   "us-east-1a",     // Optional
    Labels: map[string]string{"env": "production"}, // Optional
}
```

Routing and placement use `DataCenter.ID()` (zone + region + name). Use consistent names when spawning on or messaging a specific DC.

### Control plane

You must choose and configure a control plane implementation.

#### NATS JetStream

```go
import (
    "github.com/tochemey/goakt/v3/datacenter/controlplane/nats"
)

controlPlane, err := nats.NewControlPlane(&nats.Config{
    URL:            "nats://nats-server:4222",
    Bucket:         "goakt_datacenters",
    TTL:            30 * time.Second,
    Timeout:        5 * time.Second,
    ConnectTimeout: 5 * time.Second,
})
```

NATS server must have JetStream enabled and be reachable from all datacenters.

#### etcd

```go
import (
    "github.com/tochemey/goakt/v3/datacenter/controlplane/etcd"
)

controlPlane, err := etcd.NewControlPlane(&etcd.Config{
    Endpoints:   []string{"http://etcd1:2379", "http://etcd2:2379"},
    Namespace:   "/goakt/datacenters",
    TTL:         30 * time.Second,
    DialTimeout: 5 * time.Second,
    Timeout:     5 * time.Second,
})
```

### Datacenter config (no endpoints)

Multi-DC config does **not** include an endpoint list. Endpoints are taken from the cluster by the leader.

```go
dcConfig := datacenter.NewConfig()
dcConfig.ControlPlane = controlPlane
dcConfig.DataCenter = datacenter.DataCenter{
    Name:   "dc-us-east-1",
    Region: "us-east-1",
    Zone:   "us-east-1a",
}
// Optional tuning
dcConfig.HeartbeatInterval = 10 * time.Second
dcConfig.CacheRefreshInterval = 10 * time.Second
dcConfig.MaxCacheStaleness = 30 * time.Second
dcConfig.LeaderCheckInterval = 5 * time.Second
dcConfig.RequestTimeout = 5 * time.Second
dcConfig.WatchEnabled = true
dcConfig.FailOnStaleCache = true
```

| Field                  | Description                                    | Default  |
| ---------------------- | ---------------------------------------------- | -------- |
| `ControlPlane`         | Control plane implementation                   | Required |
| `DataCenter`           | Local DC metadata (`Name` required)            | Required |
| `HeartbeatInterval`    | Liveness heartbeat frequency                   | 10s      |
| `CacheRefreshInterval` | How often the DC cache is refreshed            | 10s      |
| `MaxCacheStaleness`    | Max acceptable cache age for cross-DC ops      | 30s      |
| `LeaderCheckInterval`  | How often leader status is rechecked           | 5s       |
| `RequestTimeout`       | Control plane operation timeout                | 5s       |
| `WatchEnabled`         | Use watch-based cache updates when supported   | true     |
| `FailOnStaleCache`     | If true, cross-DC ops fail when cache is stale | true     |

### Attaching to the cluster

Attach the datacenter config to the cluster config. Remoting must be enabled (each node’s remoting address becomes part of the DC record when this node is leader).

```go
disco := nats.NewDiscovery(&nats.Config{
    NatsServer:    "nats://local-nats:4222",
    NatsSubject:   "goakt.dc-us-east-1",
    Host:          "node1.us-east-1",
    DiscoveryPort: 3320,
})

clusterConfig := actor.NewClusterConfig().
    WithDiscovery(disco).
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

The cluster leader will register the local DC with the control plane using the current cluster members’ remoting addresses as endpoints. You do not set those endpoints in config.

---

## Creating Actors on Another Datacenter

Use `SpawnOn` with `WithDataCenter` to create an actor in a specific datacenter.

```go
targetDC := &datacenter.DataCenter{
    Name:   "eu-west-1",
    Region: "eu-west",
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

Behavior:

1. The system ensures the datacenter controller is ready; otherwise returns `ErrDataCenterNotReady`.
2. It loads active datacenter records from the controller. If the cache is stale and `FailOnStaleCache` is true, it returns `ErrDataCenterStaleRecords`.
3. It finds the target DC by matching `targetDC.ID()` to a record in `ACTIVE` state; if none, returns `ErrDataCenterRecordNotFound`.
4. It picks one of that record’s endpoints at random and sends a `RemoteSpawn` to that address.

The actor kind must be registered in the target datacenter’s cluster (e.g. via `WithKinds()`). The target DC’s endpoints are those its leader registered (the remoting addresses of its cluster members).

---

## Sending Messages to Actors in Other Datacenters

You can send messages to actors that live in other datacenters. The implementation discovers the actor across DCs and then uses remoting.

### Regular actors (PID)

From a PID you can use `TellTo` / `AskTo` by actor name; they discover the actor across active DCs and then send the message:

```go
// Fire-and-forget
err := pid.TellTo(ctx, "user-data-processor", message)

// Request-response
reply, err := pid.AskTo(ctx, "user-data-processor", request, 10*time.Second)
```

Discovery queries all active datacenter endpoints in parallel. If the cache is stale and `FailOnStaleCache` is true, discovery can return `ErrDataCenterStaleRecords`; otherwise it proceeds with best-effort (stale) cache.

From inside an actor, if you already have the remote address you can use:

```go
ctx.RemoteTell(addr, message)
response := ctx.RemoteAsk(addr, message, timeout)
```

### Grains

For grains, Tell and Ask are implemented to fan out to all active DC endpoints and use the first successful response. No separate discovery step is needed; use the grain API as usual and the system routes across datacenters.

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

---

## Next Steps

- [Cluster Overview](overview.md) — Clustering and discovery
- [Discovery Providers](discovery.md) — Configure discovery for each DC
