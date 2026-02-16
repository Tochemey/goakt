# Multi-Datacenter

GoAkt supports deploying actor systems across multiple datacenters (DCs), enabling globally distributed applications with location-aware actor placement and cross-DC communication.

## Table of Contents

- 📖 [Overview](#overview)
- 🏗️ [Architecture](#architecture)
- ⚙️ [Configuration](#configuration)
- 💡 [Complete Example](#complete-example)
- 📍 [Cross-Datacenter Actor Placement](#cross-datacenter-actor-placement)
- 📤 [Cross-Datacenter Communication](#cross-datacenter-communication)
- 🔄 [Datacenter Lifecycle States](#datacenter-lifecycle-states)
- 💾 [Cache and Consistency](#cache-and-consistency)
- ✅ [Best Practices](#best-practices)
- ⚠️ [Error Handling](#error-handling)
- 🔧 [Advanced Topics](#advanced-topics)
- 🔧 [Troubleshooting](#troubleshooting)
- ➡️ [Next Steps](#next-steps)

---

## Overview

Multi-datacenter support allows you to:

- **Deploy across geographic regions**: Run actor systems in multiple regions for lower latency and disaster recovery
- **Location-aware placement**: Spawn actors in specific datacenters
- **Cross-DC communication**: Send messages to actors in other datacenters
- **Coordinated lifecycle**: Register, discover, and manage datacenters via a control plane
- **High availability**: Survive datacenter-level failures
- **Compliance and data residency**: Keep data within specific geographic regions

## Architecture

### Components

1. **Control Plane**: Centralized coordination service (NATS or etcd) that stores datacenter metadata
2. **Datacenter Records**: Registrations containing datacenter name, region, zone, state, and endpoints
3. **Local Cluster**: Within each datacenter, nodes form a cluster using normal discovery
4. **Cross-DC Routing**: Special routing for actors placed in specific datacenters

### How It Works

```
Datacenter 1 (us-east-1)              Datacenter 2 (eu-west-1)
┌─────────────────────────┐          ┌─────────────────────────┐
│  Node1   Node2   Node3  │          │  Node4   Node5   Node6  │
│    ↕       ↕       ↕    │          │    ↕       ↕       ↕    │
│  Local Cluster (NATS)   │          │  Local Cluster (NATS)   │
└───────────┬─────────────┘          └───────────┬─────────────┘
            │                                    │
            └────────────────┬───────────────────┘
                             │
                    ┌────────▼───────┐
                    │ Control Plane  │
                    │  (NATS/etcd)   │
                    └────────────────┘
```

Each datacenter:

1. Runs a local cluster with its own discovery
2. Registers itself with the control plane
3. Periodically heartbeats to maintain liveness
4. Queries the control plane to discover other datacenters

## Configuration

### Datacenter Metadata

Define your datacenter's identity and routing endpoints:

```go
import "github.com/tochemey/goakt/v3/datacenter"

dc := datacenter.DataCenter{
    Name:   "dc-us-east-1",           // Datacenter identifier
    Region: "us-east-1",               // AWS region (optional)
    Zone:   "us-east-1a",              // Availability zone (optional)
    Labels: map[string]string{         // Custom labels (optional)
        "env": "production",
        "geo": "north-america",
    },
}
```

### Control Plane

Choose a control plane implementation:

#### NATS JetStream Control Plane

Uses NATS JetStream KeyValue for datacenter coordination.

```go
import (
    "github.com/tochemey/goakt/v3/datacenter"
    "github.com/tochemey/goakt/v3/datacenter/controlplane/nats"
)

// Create NATS control plane
controlPlane, err := nats.NewControlPlane(&nats.Config{
    URL:            "nats://nats-server:4222",
    Bucket:         "goakt_datacenters",      // KV bucket name
    TTL:            30 * time.Second,          // Lease duration
    Timeout:        5 * time.Second,           // Operation timeout
    ConnectTimeout: 5 * time.Second,           // Connection timeout
})
if err != nil {
    log.Fatal(err)
}
```

**Configuration Options:**

| Field            | Description              | Default             |
|------------------|--------------------------|---------------------|
| `URL`            | NATS server URL          | Required            |
| `Bucket`         | JetStream KV bucket name | `goakt_datacenters` |
| `TTL`            | Record time-to-live      | Required (≥ 1s)     |
| `Timeout`        | Operation timeout        | Required            |
| `ConnectTimeout` | Connection timeout       | Required            |

**Prerequisites:**

- NATS server with JetStream enabled
- Network connectivity from all datacenters to NATS

#### etcd Control Plane

Uses etcd for datacenter coordination.

```go
import (
    "github.com/tochemey/goakt/v3/datacenter"
    "github.com/tochemey/goakt/v3/datacenter/controlplane/etcd"
)

// Create etcd control plane
controlPlane, err := etcd.NewControlPlane(&etcd.Config{
    Endpoints:   []string{"http://etcd1:2379", "http://etcd2:2379"},
    Namespace:   "/goakt/datacenters",         // Key namespace
    TTL:         30 * time.Second,              // Lease duration
    DialTimeout: 5 * time.Second,               // Connection timeout
    Timeout:     5 * time.Second,               // Operation timeout
    Username:    "etcd-user",                   // Optional auth
    Password:    "etcd-password",               // Optional auth
    TLS:         tlsConfig,                     // Optional TLS
})
if err != nil {
    log.Fatal(err)
}
```

**Configuration Options:**

| Field         | Description              | Default              |
|---------------|--------------------------|----------------------|
| `Endpoints`   | etcd server endpoints    | Required             |
| `Namespace`   | Key prefix for isolation | `/goakt/datacenters` |
| `TTL`         | Record time-to-live      | Required (≥ 1s)      |
| `DialTimeout` | Connection timeout       | Required             |
| `Timeout`     | Operation timeout        | Required             |
| `Username`    | Authentication username  | None                 |
| `Password`    | Authentication password  | None                 |
| `TLS`         | TLS configuration        | None                 |

**Prerequisites:**

- etcd cluster accessible from all datacenters
- Proper network security (firewall rules, ACLs)

### Datacenter Configuration

Configure multi-DC support:

```go
import (
    "github.com/tochemey/goakt/v3/datacenter"
    "github.com/tochemey/goakt/v3/actor"
)

// Create datacenter config
dcConfig := datacenter.NewConfig()
dcConfig.ControlPlane = controlPlane
dcConfig.DataCenter = datacenter.DataCenter{
    Name:   "dc-us-east-1",
    Region: "us-east-1",
    Zone:   "us-east-1a",
}
// Advertise remoting endpoints for cross-DC actor placement
dcConfig.Endpoints = []string{
    "node1.us-east-1.example.com:3321",
    "node2.us-east-1.example.com:3321",
    "node3.us-east-1.example.com:3321",
}

// Configure optional tuning parameters
dcConfig.HeartbeatInterval = 10 * time.Second       // Liveness heartbeat frequency
dcConfig.CacheRefreshInterval = 10 * time.Second    // DC cache refresh frequency
dcConfig.MaxCacheStaleness = 30 * time.Second       // Max acceptable cache staleness
dcConfig.LeaderCheckInterval = 5 * time.Second      // Leader check frequency
dcConfig.RequestTimeout = 5 * time.Second           // Control plane operation timeout
dcConfig.WatchEnabled = true                         // Enable watch-based updates
dcConfig.FailOnStaleCache = true                     // Fail cross-DC ops on stale cache
```

### Configuration Options Reference

| Field                  | Description                                     | Default   |
|------------------------|-------------------------------------------------|-----------|
| `ControlPlane`         | Control plane implementation                    | Required  |
| `DataCenter`           | Local DC metadata                               | Required  |
| `Endpoints`            | Remoting endpoints for cross-DC actor placement | None      |
| `HeartbeatInterval`    | Liveness heartbeat frequency                    | 10s       |
| `CacheRefreshInterval` | Cache refresh polling interval                  | 10s       |
| `MaxCacheStaleness`    | Maximum tolerable cache age                     | 30s       |
| `LeaderCheckInterval`  | Leader status check frequency                   | 5s        |
| `RequestTimeout`       | Control plane API timeout                       | 5s        |
| `JitterRatio`          | Jitter applied to periodic loops                | 0.1 (10%) |
| `MaxBackoff`           | Max exponential backoff cap                     | 30s       |
| `WatchEnabled`         | Use watch-based updates (if supported)          | true      |
| `FailOnStaleCache`     | Fail cross-DC ops when cache is stale           | true      |

### Attaching to Cluster

Integrate datacenter config with cluster configuration:

```go
// Local cluster discovery (within this datacenter)
disco := nats.NewDiscovery(&nats.Config{
    NatsServer:    "nats://local-nats:4222",  // Local NATS for intra-DC clustering
    NatsSubject:   "goakt.dc-us-east-1",
    Host:          "node1.us-east-1",
    DiscoveryPort: 3320,
})

// Cluster configuration
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(disco).
    WithDiscoveryPort(3320).
    WithPeersPort(3322).
    WithMinimumPeersQuorum(2).
    WithKinds(new(MyActor)).
    WithDataCenter(dcConfig)  // Attach multi-DC config

// Create actor system
system, err := actor.NewActorSystem(
    "my-system",
    actor.WithCluster(clusterConfig),
    actor.WithRemote("node1.us-east-1.example.com", 3321),
)
```

## Complete Example

### Datacenter 1 (US East)

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/datacenter"
    "github.com/tochemey/goakt/v3/datacenter/controlplane/nats"
    discoveryNats "github.com/tochemey/goakt/v3/discovery/nats"
)

func main() {
    ctx := context.Background()

    // Control plane (shared across all DCs)
    controlPlane, _ := nats.NewControlPlane(&nats.Config{
        URL:            "nats://global-nats:4222",
        Bucket:         "goakt_datacenters",
        TTL:            30 * time.Second,
        Timeout:        5 * time.Second,
        ConnectTimeout: 5 * time.Second,
    })

    // Datacenter config
    dcConfig := datacenter.NewConfig()
    dcConfig.ControlPlane = controlPlane
    dcConfig.DataCenter = datacenter.DataCenter{
        Name:   "us-east-1",
        Region: "us-east",
    }
    dcConfig.Endpoints = []string{
        "node1.us-east-1:3321",
        "node2.us-east-1:3321",
    }

    // Local cluster discovery
    disco := discoveryNats.NewDiscovery(&discoveryNats.Config{
        NatsServer:    "nats://local-nats-us-east:4222",
        NatsSubject:   "goakt.us-east-1",
        Host:          "node1.us-east-1",
        DiscoveryPort: 3320,
    })

    // Cluster with multi-DC
    clusterConfig := actor.NewClusterConfig().
        WithDiscovery(disco).
        WithDiscoveryPort(3320).
        WithPeersPort(3322).
        WithMinimumPeersQuorum(1).
        WithKinds(new(MyActor)).
        WithDataCenter(dcConfig)

    system, _ := actor.NewActorSystem(
        "us-east-1-system",
        actor.WithCluster(clusterConfig),
        actor.WithRemote("node1.us-east-1", 3321),
    )

    system.Start(ctx)
    defer system.Stop(ctx)

    log.Println("Datacenter US-East-1 started")
    select {}
}
```

### Datacenter 2 (EU West)

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/datacenter"
    "github.com/tochemey/goakt/v3/datacenter/controlplane/nats"
    discoveryNats "github.com/tochemey/goakt/v3/discovery/nats"
)

func main() {
    ctx := context.Background()

    // Same control plane (shared)
    controlPlane, _ := nats.NewControlPlane(&nats.Config{
        URL:            "nats://global-nats:4222",
        Bucket:         "goakt_datacenters",
        TTL:            30 * time.Second,
        Timeout:        5 * time.Second,
        ConnectTimeout: 5 * time.Second,
    })

    // Different datacenter metadata
    dcConfig := datacenter.NewConfig()
    dcConfig.ControlPlane = controlPlane
    dcConfig.DataCenter = datacenter.DataCenter{
        Name:   "eu-west-1",
        Region: "eu-west",
    }
    dcConfig.Endpoints = []string{
        "node1.eu-west-1:3321",
        "node2.eu-west-1:3321",
    }

    // Local cluster discovery (different NATS)
    disco := discoveryNats.NewDiscovery(&discoveryNats.Config{
        NatsServer:    "nats://local-nats-eu-west:4222",
        NatsSubject:   "goakt.eu-west-1",
        Host:          "node1.eu-west-1",
        DiscoveryPort: 3320,
    })

    clusterConfig := actor.NewClusterConfig().
        WithDiscovery(disco).
        WithDiscoveryPort(3320).
        WithPeersPort(3322).
        WithMinimumPeersQuorum(1).
        WithKinds(new(MyActor)).
        WithDataCenter(dcConfig)

    system, _ := actor.NewActorSystem(
        "eu-west-1-system",
        actor.WithCluster(clusterConfig),
        actor.WithRemote("node1.eu-west-1", 3321),
    )

    system.Start(ctx)
    defer system.Stop(ctx)

    log.Println("Datacenter EU-West-1 started")
    select {}
}
```

## Cross-Datacenter Actor Placement

### Spawn Actor in Specific Datacenter

Use `SpawnOn()` with `WithDataCenter()` to place an actor in a specific datacenter:

```go
import "github.com/tochemey/goakt/v3/datacenter"

// Define target datacenter
targetDC := &datacenter.DataCenter{
    Name:   "eu-west-1",
    Region: "eu-west",
}

// Spawn actor in EU datacenter
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

### How Cross-DC Placement Works

1. **Datacenter lookup**: System queries control plane for active EU-West-1 datacenter record
2. **Endpoint selection**: Randomly selects one of the advertised endpoints from that DC
3. **Remote spawn**: Issues `RemoteSpawn` request to the selected endpoint
4. **Actor creation**: Target datacenter creates the actor locally
5. **Registration**: Actor is registered in cluster routing table

### Placement Guarantees

- **Random placement**: Actor is placed on a random node in the target DC (from advertised endpoints)
- **Actor kind required**: The actor type must be registered in the target DC's cluster config
- **Stale cache handling**: Controlled by `FailOnStaleCache` option

## Cross-Datacenter Communication

### Lookup and Messaging

```go
// Look up actor (may be in any datacenter)
addr, pid, err := system.ActorOf(ctx, "user-data-processor")
if err != nil {
    log.Fatal(err)
}

// Send message (transparently handles cross-DC routing)
if pid != nil {
    // Actor is local
    pid.Tell(message)
} else {
    // Actor is remote (possibly in another DC): from inside an actor use ctx.RemoteTell(addr, message);
    // from outside use the cluster client: cl.Tell(ctx, "user-data-processor", message)
}
```

### Request-Response Across Datacenters

From inside an actor: `response := ctx.RemoteAsk(addr, &ProcessDataRequest{UserID: "user-123"}, 10*time.Second)` (check `response == nil` and `ctx.Err()`). From outside use the cluster client:

```go
response, err := cl.Ask(ctx, "user-data-processor", &ProcessDataRequest{UserID: "user-123"}, 10*time.Second)
if err != nil {
    log.Fatal(err)
}
```

## Datacenter Lifecycle States

A datacenter record transitions through these states:

```
REGISTERED -> ACTIVE -> DRAINING/INACTIVE
```

### States

- **REGISTERED**: Datacenter exists in control plane but not yet eligible for routing
- **ACTIVE**: Datacenter is healthy and can receive actor placements
- **DRAINING**: Datacenter should avoid new placements (for graceful shutdown)
- **INACTIVE**: Datacenter is not eligible for routing

### Automatic State Management

The datacenter controller (running on the cluster leader) automatically manages state transitions:

1. **Registration**: Sets state to `REGISTERED`
2. **Activation**: Transitions to `ACTIVE` after successful registration
3. **Heartbeating**: Maintains liveness by renewing lease
4. **Shutdown**: Can transition to `DRAINING` then `INACTIVE` on graceful stop

## Cache and Consistency

### Cache Refresh

The datacenter cache is refreshed:

- **Periodically**: Every `CacheRefreshInterval` (default 10s)
- **Watch-based**: Immediately when supported (`WatchEnabled: true`)
- **On leader election**: When this node becomes cluster leader

### Stale Cache Handling

If the cache hasn't been refreshed within `MaxCacheStaleness`:

- **FailOnStaleCache = true** (default): Cross-DC operations return `ErrDataCenterStaleRecords`
- **FailOnStaleCache = false**: Operations proceed with best-effort routing (warning logged)

### Cache Warmup

When a node starts or becomes leader:

1. Immediately fetches active datacenter records
2. Populates the cache
3. Marks cache as fresh
4. Begins periodic refresh cycle

## Best Practices

### Network Architecture

1. **Separate local and global NATS/etcd**: Use local instances for intra-DC clustering, shared instance for control plane
2. **Low-latency control plane**: Control plane should have good connectivity to all datacenters
3. **Advertise stable endpoints**: Use load balancer VIPs or stable DNS names in `Endpoints`
4. **Enable TLS**: Encrypt control plane and remoting traffic

### Configuration

1. **Tune timeouts for WAN latency**: Cross-DC operations take longer than local
2. **Set appropriate TTL**: Balance liveness detection speed with heartbeat overhead
3. **Enable watch when available**: Reduces cache staleness
4. **Consider FailOnStaleCache**: Decide if you prefer strict consistency or availability

### Actor Placement

1. **Co-locate with data**: Place actors in the same DC as their data sources
2. **Respect data residency**: Use datacenter placement for compliance (GDPR, etc.)
3. **Minimize cross-DC calls**: Design actors to work primarily within their DC
4. **Use async patterns**: Cross-DC communication is slower; avoid blocking

### Monitoring

1. **Track datacenter health**: Monitor control plane connectivity and heartbeat success
2. **Watch cache staleness**: Alert if cache becomes stale frequently
3. **Measure cross-DC latency**: Track RemoteAsk/RemoteTell latencies
4. **Monitor endpoint reachability**: Ensure advertised endpoints are reachable

### Operations

1. **Test failure scenarios**: Simulate control plane failures, network partitions
2. **Plan for control plane outages**: Understand that existing actors continue working, but new cross-DC placements fail
3. **Graceful shutdown**: Implement coordinated shutdown hooks to transition to `DRAINING` state
4. **Rolling updates**: Update one datacenter at a time

## Error Handling

### Common Errors

```go
err := system.SpawnOn(ctx, "actor", new(MyActor), actor.WithDataCenter(targetDC))
if err != nil {
    switch {
    case errors.Is(err, gerrors.ErrDataCenterNotReady):
        // Control plane not initialized yet
    case errors.Is(err, gerrors.ErrDataCenterStaleRecords):
        // Cache is stale and FailOnStaleCache = true
    case errors.Is(err, gerrors.ErrDataCenterRecordNotFound):
        // Target datacenter not found in control plane
    case errors.Is(err, gerrors.ErrTypeNotRegistered):
        // Actor kind not registered in target DC
    default:
        // Network, timeout, or other errors
    }
}
```

### Control Plane Failures

**Symptoms:**

- Cross-DC placement fails with timeout errors
- Cache refresh failures in logs
- `ErrDataCenterNotReady` errors

**Impact:**

- Existing actors and intra-DC operations continue normally
- New cross-DC actor placements fail
- Cross-DC routing relies on cached records

**Mitigation:**

- Run control plane with high availability (clustered NATS, etcd cluster)
- Set realistic TTLs to allow longer control plane outages
- Design applications to tolerate temporary cross-DC placement failures

## Advanced Topics

### Multi-Region Deployment with Role Segregation

```go
// In US datacenter: spawn workers
clusterConfig := actor.NewClusterConfig().
    WithRoles("worker").
    WithDataCenter(usEastConfig)

// In EU datacenter: spawn workers
clusterConfig := actor.NewClusterConfig().
    WithRoles("worker").
    WithDataCenter(euWestConfig)

// Spawn worker in specific region
system.SpawnOn(ctx, "worker-1", new(WorkerActor),
    actor.WithDataCenter(&datacenter.DataCenter{Name: "eu-west-1"}),
    actor.WithRole("worker"),
)
```

### Custom Endpoints for Load Balancing

```go
// Advertise a load balancer instead of individual nodes
dcConfig.Endpoints = []string{
    "lb.us-east-1.example.com:3321",
}
```

When actors are spawned in this DC, they'll always go through the load balancer, which distributes to healthy nodes.

### Datacenter Labels for Routing Policies

```go
dcConfig.DataCenter = datacenter.DataCenter{
    Name:   "us-east-1",
    Region: "us-east",
    Labels: map[string]string{
        "tier":        "premium",
        "compliance":  "pci-dss",
        "environment": "production",
    },
}
```

Use labels for custom placement logic or filtering in your application code.

## Troubleshooting

### Datacenters Not Discovering Each Other

- **Check control plane connectivity**: Verify all DCs can reach the control plane
- **Review configuration**: Ensure `ControlPlane` and `DataCenter.Name` are set correctly
- **Check heartbeats**: Look for heartbeat errors in logs
- **Verify TTL**: If TTL is too short, records may expire between heartbeats

### Cross-DC Spawns Failing

- **Actor kind not registered**: Use `WithKinds()` in target DC's cluster configuration
- **Stale cache**: Check if cache is being refreshed; may need to increase `MaxCacheStaleness`
- **Network issues**: Verify remoting endpoints are reachable from source DC
- **Endpoints not advertised**: Ensure `dcConfig.Endpoints` contains reachable addresses

### High Cross-DC Latency

- **Geographic distance**: Cross-DC calls traverse WANs; consider caching or async patterns
- **Network congestion**: Monitor network utilization and QoS
- **Overloaded control plane**: Check control plane resource usage
- **Too many cross-DC calls**: Redesign to minimize cross-DC communication

### Cache Staleness Warnings

- **Control plane unreachable**: Check connectivity and control plane health
- **RefreshInterval too long**: Decrease `CacheRefreshInterval`
- **Watch not working**: Verify `WatchEnabled` is true and control plane supports watch
- **MaxCacheStaleness too short**: Increase if control plane has brief outages

## Next Steps

- [Cluster Overview](overview.md): Learn about clustering fundamentals
- [Discovery Providers](discovery.md): Configure discovery for local clusters
- [Actor Relocation](relocation.md): Understanding actor migration across nodes
