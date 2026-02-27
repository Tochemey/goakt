# Multi-Datacenter

## Overview

GoAkt supports multiple clusters across datacenters. A pluggable **control plane** (NATS JetStream or etcd) propagates topology and placement decisions across sites. Actors can be spawned or messaged across DCs. Each datacenter runs its own cluster; the control plane coordinates which DCs are active and where to route cross-DC traffic.

## Architecture

```
DC-1 (us-east-1)                    Control Plane                    DC-2 (eu-west-1)
----------------                    --------------                    -----------------
Cluster (Olric)                     NATS JetStream / etcd            Cluster (Olric)
     |                                    |                                  |
     | Register + Heartbeat                | ListActive / Watch                | Register + Heartbeat
     +------------------------------------+------------------------------------+
     |                                    |                                    |
     v                                    v                                    v
Leader registers local DC          DC records (ACTIVE, endpoints)         Leader registers local DC
with cluster members'              propagated to all DCs                   with cluster members'
remoting addresses                                                         remoting addresses
```

- **Control plane** — Manages datacenter registration, heartbeats, state transitions, and event watching. Implementations: NATS JetStream, etcd.
- **DC leader** — Only the cluster leader in each DC runs the datacenter controller. It registers the local DC with the control plane using cluster members' remoting addresses.
- **DataCenterRecord** — Each DC has a record: ID, metadata (Name, Region, Zone, Labels), Endpoints (remoting addresses), State, LeaseExpiry, Version.

## When to use

- **Geographic distribution** — Run actors close to users or data in different regions.
- **Disaster recovery and failover** — Replicate or fail over to another DC.
- **DC-aware placement** — Spawn actors in a specific datacenter via `SpawnOn` with `WithDataCenter`.

## Control plane backends

| Backend            | Package                        | Use case                                  |
|--------------------|--------------------------------|-------------------------------------------|
| **NATS JetStream** | `datacenter/controlplane/nats` | Lightweight, good for cloud-native setups |
| **etcd**           | `datacenter/controlplane/etcd` | Strong consistency, Kubernetes-friendly   |

### NATS JetStream

```go
import "github.com/tochemey/goakt/v4/datacenter/controlplane/nats"

natsCfg := &nats.Config{
    URL:             "nats://127.0.0.1:4222",
    Bucket:          "goakt_datacenters",
    TTL:             30 * time.Second,
    Timeout:         5 * time.Second,
    ConnectTimeout:  5 * time.Second,
}
natsCfg.Sanitize()
cp, err := nats.NewControlPlane(natsCfg)
```

### etcd

```go
import "github.com/tochemey/goakt/v4/datacenter/controlplane/etcd"

etcdCfg := &etcd.Config{
    Endpoints:   []string{"http://127.0.0.1:2379"},
    Namespace:   "/goakt/datacenters",
    TTL:         30 * time.Second,
    DialTimeout: 5 * time.Second,
    Timeout:     5 * time.Second,
}
etcdCfg.Sanitize()
cp, err := etcd.NewControlPlane(etcdCfg)
```

## Configuration

Multi-DC requires cluster mode. Configure the datacenter via `WithDataCenter` on the cluster config:

```go
import (
    "github.com/tochemey/goakt/v4/actor"
    "github.com/tochemey/goakt/v4/datacenter"
    "github.com/tochemey/goakt/v4/datacenter/controlplane/nats"
)

// 1. Create control plane
natsCfg := &nats.Config{URL: "nats://127.0.0.1:4222", TTL: 30 * time.Second, ...}
natsCfg.Sanitize()
cp, _ := nats.NewControlPlane(natsCfg)

// 2. Create datacenter config
dcCfg := datacenter.NewConfig()
dcCfg.ControlPlane = cp
dcCfg.DataCenter = datacenter.DataCenter{
    Name:   "dc-1",
    Region: "us-east-1",
    Zone:   "az-a",
}

// 3. Wire into cluster config
clusterCfg := actor.NewClusterConfig().
    WithDiscovery(discoveryProvider).
    WithDataCenter(dcCfg)

system, _ := actor.NewActorSystem("app",
    actor.WithCluster(clusterCfg),
    actor.WithRemote(remoteCfg),
)
```

Endpoints for cross-DC routing are **not** set in config; the cluster leader registers the local DC with the control plane using cluster members' remoting addresses.

## Datacenter config options

| Option                 | Default | Purpose                                                                                                                                        |
|------------------------|---------|------------------------------------------------------------------------------------------------------------------------------------------------|
| `HeartbeatInterval`    | 10s     | How often the DC renews its liveness lease                                                                                                     |
| `CacheRefreshInterval` | 10s     | How often the DC registry cache is refreshed                                                                                                   |
| `MaxCacheStaleness`    | 30s     | Max age of cached DC data before routing is restricted                                                                                         |
| `FailOnStaleCache`     | `true`  | If true, `SpawnOn` with `WithDataCenter` returns `ErrDataCenterStaleRecords` when cache is stale. If false, proceeds with best-effort routing. |
| `WatchEnabled`         | `true`  | Use watch-based cache refresh when the control plane supports it                                                                               |

## Data center states

| State        | Meaning                                             |
|--------------|-----------------------------------------------------|
| `REGISTERED` | Record exists but not yet eligible for routing      |
| `ACTIVE`     | Eligible for routing as long as lease is valid      |
| `DRAINING`   | Avoid new placements; existing traffic may continue |
| `INACTIVE`   | Not eligible for routing                            |

Records are leased; `Heartbeat` renews the lease. Expired records should not be routed to.

## DC-aware actor placement

Use `SpawnOn` with `WithDataCenter` to spawn an actor in a specific datacenter:

```go
targetDC := &datacenter.DataCenter{Name: "dc-2", Region: "eu-west-1"}
err := system.SpawnOn(ctx, "analytics-worker", NewWorkerActor(), actor.WithDataCenter(targetDC))
if err != nil {
    // ErrDataCenterNotReady, ErrDataCenterStaleRecords, ErrDataCenterRecordNotFound, etc.
    return err
}
// Actor is created on a node in dc-2. Use ActorOf to resolve the PID.
pid, err := system.ActorOf(ctx, "analytics-worker")
```

Placement: the target is one of the target DC's `DataCenterRecord.Endpoints`, selected uniformly at random. Which node runs the actor depends on what that DC registered—if it registered all nodes' remoting addresses, the actor is placed on a random node in that DC.

The actor kind must be registered on the target DC's actor systems; otherwise the remote node returns `ErrTypeNotRegistered`.

## Messaging across DCs

Once an actor is spawned (locally or in another DC), messaging is **location-transparent**. Use `Tell`, `Ask`, `Request` as usual; the framework routes to the correct node.

Grains can also be addressed by identity across DCs when multi-DC is enabled: the system uses the control plane’s cached
active datacenter records and attempts delivery to the first endpoint that successfully handles the request.

## Readiness and errors

| API                              | Purpose                                                |
|----------------------------------|--------------------------------------------------------|
| `system.DataCenterReady()`       | Returns whether the multi-DC controller is operational |
| `system.DataCenterLastRefresh()` | Time of last successful DC cache refresh               |

| Error                         | When                                                                |
|-------------------------------|---------------------------------------------------------------------|
| `ErrDataCenterNotReady`       | Controller not ready; `SpawnOn` with `WithDataCenter` fails         |
| `ErrDataCenterStaleRecords`   | Cache older than `MaxCacheStaleness` and `FailOnStaleCache` is true |
| `ErrDataCenterRecordNotFound` | Target DC not in active records or not in `ACTIVE` state            |

## Custom control plane

Implement the `datacenter.ControlPlane` interface:

- `Register`, `Heartbeat`, `SetState`, `ListActive`, `Watch`, `Deregister`

See `datacenter/controlplane/nats` and `datacenter/controlplane/etcd` as reference implementations. Wire it in by passing the control plane to the datacenter config when creating the actor system.

## See also

- [Clustered](clustered.md) — Cluster setup and discovery
- [Relocation](../actor/relocation.md) — Actor relocation within a cluster
- [Remoting](../advanced/remoting.md) — Cross-node messaging
