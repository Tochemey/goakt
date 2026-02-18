# Cluster Overview

GoAkt clustering lets multiple actor systems form a distributed network: actors can run on any node, communicate across nodes, and benefit from automatic placement, lookup, and fault tolerance.

## Table of Contents

- 🤔 [What is Clustering?](#what-is-clustering)
- 📐 [Core Concepts](#core-concepts)
- ⚙️ [Cluster Configuration](#cluster-configuration)
- 🚀 [Starting a Cluster](#starting-a-cluster)
- 🎭 [Spawning & Lookup](#spawning--lookup)
- 📢 [Cluster Events & Shutdown](#cluster-events--shutdown)
- ✅ [Best Practices](#best-practices)
- 🔧 [Troubleshooting](#troubleshooting)

---

## What is Clustering?

- **Scale horizontally**: Distribute actors across nodes.
- **High availability**: Actors can be relocated when nodes fail.
- **Location transparency**: Message by actor name; system resolves location.
- **Grains**: Virtual actors (grains) for globally unique entities across the cluster.
- **Multi-datacenter**: Optional support for multiple datacenters.

## Core Concepts

- **Cluster node**: An actor system that has joined the cluster (unique identity, discovery, remoting).
- **Routing table**: [Olric](https://github.com/tochemey/olric)-based distributed store for actor/grain metadata and kinds; replicated via quorum.
- **Partitioning**: Consistent hashing over actor names; default partition count 271; rebalanced when nodes join/leave.
- **Quorum**: `WithReplicaCount`, `WithWriteQuorum`, `WithReadQuorum`. For consistency: `readQuorum + writeQuorum > replicaCount`. Typical: `replicaCount=3, writeQuorum=2, readQuorum=2`.
- **Leader**: One node coordinates rebalancing and singletons; `cluster.IsLeader(ctx)`.
- **Roles**: `WithRoles("web", "entity")`; spawn on role with `SpawnOn(ctx, name, actor, actor.WithRole("entity"))`.

## Cluster Configuration

Default settings are sufficient for most cases. Provide discovery and optionally host/ports; override quorum, timeouts, and partition count only when needed.

```go
disco := nats.NewDiscovery(&nats.Config{
    NatsServer: "nats://localhost:4222", NatsSubject: "my-cluster",
    Host: "localhost", DiscoveryPort: 3320,
})
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(disco).
    WithDiscoveryPort(3320).
    WithPeersPort(3322).
    WithMinimumPeersQuorum(2).
    WithReplicaCount(3).WithWriteQuorum(2).WithReadQuorum(2).
    WithKinds(new(MyActor)).
    WithGrains(new(MyGrain)) // optional

system, err := actor.NewActorSystem("my-system",
    actor.WithCluster(clusterConfig),
    actor.WithRemote("localhost", 3321), // required for clustering
)
```

| Area         | Options                                                                              |
|--------------|--------------------------------------------------------------------------------------|
| Discovery    | `WithDiscovery`, `WithDiscoveryPort`, `WithPeersPort`                                |
| Quorum       | `WithMinimumPeersQuorum`, `WithReplicaCount`, `WithWriteQuorum`, `WithReadQuorum`    |
| Registration | `WithKinds`, `WithGrains`                                                            |
| Partitioning | `WithPartitionCount` (default 271)                                                   |
| Timeouts     | `WithBootstrapTimeout`, `WithShutdownTimeout`, `WithWriteTimeout`, `WithReadTimeout` |
| Sync         | `WithClusterStateSyncInterval`, `WithClusterBalancerInterval`                        |
| Other        | `WithRoles`, `WithGrainActivationBarrier`, `WithDataCenter`, `WithTableSize`         |

## Starting a Cluster

```go
if err := system.Start(ctx); err != nil { log.Fatal(err) }
defer system.Stop(ctx)
```

Bootstrap: discovery → peer discovery → wait for `MinimumPeersQuorum` (up to `BootstrapTimeout`) → Olric starts → routing table sync → ready.

## Spawning & Lookup

- **Local:** `system.Spawn(ctx, "name", new(MyActor))`
- **Distributed:** `system.SpawnOn(ctx, "name", new(MyActor), actor.WithPlacement(actor.RoundRobin))` (or `Random`, `LeastLoad`, `Local`); or `actor.WithRole("entity")` to target nodes with that role.
- **Singleton:** `system.SpawnSingleton(ctx, "name", new(MyActor))` — one instance on the leader, relocated if leader changes.

**Lookup:** `addr, pid, err := system.ActorOf(ctx, "actor-name")`. If `pid != nil` the actor is local (use `pid.Tell`); otherwise it’s remote (use cluster client or from inside an actor `ctx.RemoteTell(addr, msg)` / `ctx.RemoteAsk(addr, msg, timeout)`). Also: `system.ActorExists(ctx, "name")`, `system.RemoteActor(ctx, "name")`.

## Cluster Events & Shutdown

**Events:** Subscribe to lifecycle/topology via `system.Subscribe()`; iterate for `*goaktpb.NodeJoined`, `*goaktpb.NodeLeft`, etc. Call `system.Unsubscribe(subscriber)` when done.

**Shutdown:** `system.Stop(ctx)`. Process: stop new spawns → drain messages → stop local actors → remove from cluster state → leave memberlist → shutdown Olric and remoting. Respects `WithShutdownTimeout` and coordinated shutdown hooks.

## Best Practices

- **Config:** Use prime partition count; set quorums so `readQuorum + writeQuorum > replicaCount`; register all kinds that nodes will host.
- **Network:** Stable identities; tune timeouts; use TLS in production; separate discovery, peers, and remoting ports.
- **Placement:** Use roles to segregate workloads; prefer `SpawnOn` for cluster-wide actors; choose placement strategy (RoundRobin, Random, LeastLoad, Local).
- **Operations:** Monitor cluster events; use `system.Running()` and `system.InCluster()` for health; plan rolling updates with quorum and grain activation barrier.

## Troubleshooting

- **Won’t bootstrap:** Check minimum quorum, discovery provider, and network (discovery/peers ports); increase bootstrap timeout if needed.
- **Actor not found:** Ensure kind is registered on nodes that can host it; cluster ready; name unique; roles match if using `WithRole`.
- **Split brain:** Use majority quorums; monitor partitions and discovery health.
- **Performance:** Tune sync/balancer intervals; review partition count and network latency; prefer local refs when possible.
