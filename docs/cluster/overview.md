# Cluster Overview

GoAkt provides a powerful clustering capability that allows multiple actor systems to form a distributed network, enabling actors to communicate across nodes and providing automatic actor management, load distribution, and fault tolerance.

## What is Clustering?

Clustering in GoAkt allows you to:

- **Scale horizontally**: Distribute actors across multiple nodes for better resource utilization
- **High availability**: Automatic actor relocation when nodes fail
- **Location transparency**: Send messages to actors without knowing their physical location
- **Distributed state**: Use grains (virtual actors) for globally unique distributed entities
- **Multi-datacenter support**: Deploy across multiple datacenters with coordinated operations

## Core Concepts

### Cluster Node

A cluster node is an actor system instance that has joined a cluster. Each node:

- Has a unique identity derived from its host and ports
- Participates in peer discovery via a discovery provider
- Maintains a consistent view of actor locations via a distributed routing table
- Can host actors and grains
- Communicates with other nodes via remoting

### Distributed Routing Table

The cluster uses [Olric](https://github.com/buraksezer/olric), a distributed in-memory key-value store built on consistent hashing, to maintain:

- **Actor metadata**: Name, type, address, configuration, and location
- **Grain metadata**: Identity, type, host, and state
- **Kind registrations**: Actor types available across the cluster

This routing table is replicated across nodes according to the configured replication factor, ensuring high availability.

### Partitioning

The cluster divides the actor namespace into partitions using consistent hashing:

- Actors and grains are assigned to partitions based on their name/identity
- Partitions are distributed across cluster nodes
- When nodes join or leave, partitions are automatically rebalanced
- The default partition count is 271 (a prime number for better distribution)

### Quorum-Based Replication

GoAkt uses quorum-based replication for cluster state operations:

- **Replica Count**: Number of copies maintained for each partition's data
- **Write Quorum**: Minimum replicas that must acknowledge a write
- **Read Quorum**: Number of replicas consulted to satisfy a read

For consistency (avoiding stale reads), configure: `readQuorum + writeQuorum > replicaCount`

Common configurations:

- **Single replica**: `replicaCount=1, writeQuorum=1, readQuorum=1` (no redundancy)
- **Majority quorum**: `replicaCount=3, writeQuorum=2, readQuorum=2` (recommended for production)

### Cluster Leader

One node in the cluster acts as the coordinator (leader):

- Manages cluster-wide operations like rebalancing
- Spawns singleton actors (when requested)
- Coordinates actor relocation when nodes leave
- Leadership is automatically managed by the underlying Olric memberlist

You can check leadership status using:

```go
isLeader := cluster.IsLeader(ctx)
```

### Node Roles

Nodes can advertise roles to define their responsibilities:

- Roles are labels like `"web"`, `"entity"`, `"projection"`, `"worker"`
- Use `WithRoles()` in cluster configuration to advertise roles
- Use `SpawnOn()` with `WithRole()` option to spawn actors on specific role-bearing nodes
- Enables workload segregation and independent scaling

Example:

```go
// Node advertises "entity" role
clusterConfig := actor.NewClusterConfig().
    WithRoles("entity", "projection")

// Spawn actor on nodes with "entity" role
err := system.SpawnOn(ctx, "user-123", userActor, actor.WithRole("entity"))
```

## Cluster Configuration

Create a cluster configuration using `NewClusterConfig()`:

```go
import (
    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/discovery/nats"
)

// Create NATS discovery provider
disco := nats.NewDiscovery(&nats.Config{
    NatsServer:    "nats://localhost:4222",
    NatsSubject:   "my-cluster",
    Host:          "localhost",
    DiscoveryPort: 3320,
})

// Configure the cluster
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(disco).
    WithDiscoveryPort(3320).
    WithPeersPort(3322).
    WithMinimumPeersQuorum(2).
    WithReplicaCount(3).
    WithWriteQuorum(2).
    WithReadQuorum(2).
    WithPartitionCount(271).
    WithKinds(new(MyActor), new(AnotherActor))

// Create actor system with clustering
system, err := actor.NewActorSystem(
    "my-system",
    actor.WithCluster(clusterConfig),
    actor.WithRemote("localhost", 3321),
)
```

### Essential Configuration Options

#### Discovery Configuration

```go
// WithDiscovery sets the discovery provider for peer detection
WithDiscovery(discovery discovery.Provider)

// WithDiscoveryPort sets the port for discovery communication
WithDiscoveryPort(port int)

// WithPeersPort sets the port for cluster peer-to-peer communication
WithPeersPort(port int)
```

#### Quorum and Replication

```go
// WithMinimumPeersQuorum sets the minimum nodes required for cluster bootstrap
// The cluster won't start until this many nodes have joined
WithMinimumPeersQuorum(quorum uint32)

// WithReplicaCount sets the number of replicas for each partition
// Higher values improve fault tolerance but increase write overhead
WithReplicaCount(count uint32)

// WithWriteQuorum sets the minimum replicas that must acknowledge a write
WithWriteQuorum(count uint32)

// WithReadQuorum sets the number of replicas consulted for reads
WithReadQuorum(count uint32)
```

#### Actor Registration

```go
// WithKinds registers actor types that can be spawned across the cluster
// These must be registered on all nodes that may host these actors
WithKinds(kinds ...Actor)

// WithGrains registers grain types for virtual actor functionality
WithGrains(grains ...Grain)
```

#### Partitioning

```go
// WithPartitionCount sets the number of partitions (should be a prime number)
// Default is 271. Higher values provide better distribution but increase overhead
WithPartitionCount(count uint64)
```

### Advanced Configuration Options

#### Timeouts

```go
// WithBootstrapTimeout sets how long to wait for cluster formation
// Default: 10 seconds
WithBootstrapTimeout(timeout time.Duration)

// WithShutdownTimeout sets the maximum time for graceful cluster shutdown
// Default: 3 minutes
WithShutdownTimeout(timeout time.Duration)

// WithWriteTimeout sets the timeout for cluster write operations
// Default: 1 second
WithWriteTimeout(timeout time.Duration)

// WithReadTimeout sets the timeout for cluster read operations
// Default: 1 second
WithReadTimeout(timeout time.Duration)
```

#### Cluster Synchronization

```go
// WithClusterStateSyncInterval sets how often routing tables sync across nodes
// Should be greater than write timeout. Default: 1 minute
WithClusterStateSyncInterval(interval time.Duration)

// WithClusterBalancerInterval sets how often the balancer runs
// Should be shorter than state sync interval. Default: 1 second
WithClusterBalancerInterval(interval time.Duration)
```

#### Storage

```go
// WithTableSize sets the in-memory storage size for the distributed map
// Default: 4MB
WithTableSize(size uint64)
```

#### Node Roles

```go
// WithRoles sets the roles advertised by this node
// Roles enable workload segregation and targeted placement
WithRoles(roles ...string)
```

#### Grain Activation

```go
// WithGrainActivationBarrier delays grain activation until cluster reaches quorum
// Useful during startup and rolling deployments
// timeout == 0: wait indefinitely for quorum
// timeout > 0: proceed after timeout even if quorum not reached
WithGrainActivationBarrier(timeout time.Duration)
```

#### Multi-Datacenter

```go
// WithDataCenter configures multi-datacenter support
// Requires a control plane implementation and datacenter metadata
WithDataCenter(config *datacenter.Config)
```

## Starting a Cluster

```go
ctx := context.Background()

// Create the actor system with cluster configuration
system, err := actor.NewActorSystem(
    "my-system",
    actor.WithCluster(clusterConfig),
    actor.WithRemote("localhost", 3321), // Remoting is required for clustering
)
if err != nil {
    log.Fatal(err)
}

// Start the system - automatically joins the cluster
if err := system.Start(ctx); err != nil {
    log.Fatal(err)
}

// Wait for cluster to be ready
// The system will wait up to BootstrapTimeout for minimum quorum
```

### Bootstrap Process

When a clustered actor system starts:

1. **Discovery initialization**: The discovery provider is initialized and registers the node
2. **Peer discovery**: The node discovers other cluster members
3. **Quorum wait**: The system waits for `MinimumPeersQuorum` nodes to join (up to `BootstrapTimeout`)
4. **Olric startup**: The distributed storage engine starts and joins the memberlist
5. **Routing table sync**: The node fetches and synchronizes the cluster routing table
6. **Ready**: The system is ready to spawn actors and process messages

## Spawning Actors in a Cluster

### Local Spawn

Use `Spawn()` to create an actor on the local node:

```go
pid, err := system.Spawn(ctx, "local-actor", new(MyActor))
```

### Distributed Spawn

Use `SpawnOn()` to create an actor anywhere in the cluster:

```go
// Spawn with placement strategy
err := system.SpawnOn(ctx, "distributed-actor", new(MyActor),
    actor.WithPlacement(actor.RoundRobin), // or Random, LeastLoad, Local
)

// Spawn on a specific role
err := system.SpawnOn(ctx, "entity-actor", new(MyActor),
    actor.WithRole("entity"),
)

// After spawning, look up the actor
addr, pid, err := system.ActorOf(ctx, "distributed-actor")
```

### Singleton Actors

Singleton actors are guaranteed to have only one instance across the cluster:

```go
err := system.SpawnSingleton(ctx, "cluster-coordinator", new(CoordinatorActor))
```

Singletons are always spawned on the cluster leader and automatically relocated if the leader changes.

## Actor Lookup

### ActorOf

Find an actor anywhere in the cluster:

```go
addr, pid, err := system.ActorOf(ctx, "actor-name")
if err != nil {
    // Actor not found
}

if pid != nil {
    // Actor is local - you have a PID reference
    pid.Tell(message)
} else {
    // Actor is remote: from inside an actor use ctx.RemoteTell(addr, message) or
    // response := ctx.RemoteAsk(addr, message, timeout); from outside use the cluster client:
    // cl.Ask(ctx, "actor-name", message, timeout) or cl.Tell(ctx, "actor-name", message)
}
```

### ActorExists

Check if an actor exists without retrieving its reference:

```go
exists, err := system.ActorExists(ctx, "actor-name")
```

### RemoteActor

Get the address of a remote actor (fails if actor is local):

```go
addr, err := system.RemoteActor(ctx, "actor-name")
```

## Cluster Events

Monitor cluster topology changes by subscribing to cluster events:

```go
subscriber, err := system.Subscribe()
if err != nil {
    log.Fatal(err)
}
defer system.Unsubscribe(subscriber)

for event := range subscriber.Iterator() {
    switch e := event.(type) {
    case *goaktpb.NodeJoined:
        fmt.Printf("Node joined: %s\n", e.GetAddress())
    case *goaktpb.NodeLeft:
        fmt.Printf("Node left: %s\n", e.GetAddress())
    }
}
```

## Cluster Shutdown

Gracefully stop a cluster node:

```go
ctx := context.Background()
if err := system.Stop(ctx); err != nil {
    log.Fatal(err)
}
```

The shutdown process:

1. Stops accepting new actor spawns
2. Drains in-flight messages
3. Stops all local actors
4. Removes actor registrations from cluster state
5. Leaves the cluster memberlist
6. Deregisters from discovery provider
7. Shuts down Olric and remoting

The shutdown respects `WithShutdownTimeout` and `WithCoordinatedShutdown` hooks.

## Complete Example

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/discovery/nats"
    "google.golang.org/protobuf/proto"
)

type WorkerActor struct{}

func (w *WorkerActor) PreStart(ctx *actor.Context) error {
    ctx.ActorSystem().Logger().Info("Worker started")
    return nil
}

func (w *WorkerActor) Receive(ctx *actor.ReceiveContext) {
    // Handle messages
}

func (w *WorkerActor) PostStop(ctx *actor.Context) error {
    ctx.ActorSystem().Logger().Info("Worker stopped")
    return nil
}

func main() {
    ctx := context.Background()

    // Configure NATS discovery
    disco := nats.NewDiscovery(&nats.Config{
        NatsServer:    "nats://localhost:4222",
        NatsSubject:   "my-cluster",
        Host:          "localhost",
        DiscoveryPort: 3320,
    })

    // Configure the cluster
    clusterConfig := actor.NewClusterConfig().
        WithDiscovery(disco).
        WithDiscoveryPort(3320).
        WithPeersPort(3322).
        WithMinimumPeersQuorum(2).
        WithReplicaCount(3).
        WithWriteQuorum(2).
        WithReadQuorum(2).
        WithKinds(new(WorkerActor)).
        WithRoles("worker")

    // Create clustered actor system
    system, err := actor.NewActorSystem(
        "worker-system",
        actor.WithCluster(clusterConfig),
        actor.WithRemote("localhost", 3321),
    )
    if err != nil {
        log.Fatal(err)
    }

    // Start the system
    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    log.Println("Cluster node started")

    // Spawn a distributed actor
    err = system.SpawnOn(ctx, "worker-1", new(WorkerActor),
        actor.WithPlacement(actor.RoundRobin),
    )
    if err != nil {
        log.Fatal(err)
    }

    // Look up the actor
    addr, pid, err := system.ActorOf(ctx, "worker-1")
    if err != nil {
        log.Fatal(err)
    }

    if pid != nil {
        log.Println("Worker is local")
    } else {
        log.Printf("Worker is remote at %s\n", addr.String())
    }

    // Keep running
    select {}
}
```

## Best Practices

### Configuration

1. **Choose appropriate partition count**: Use prime numbers for better distribution. Default (271) works for most cases
2. **Set realistic quorums**: `MinimumPeersQuorum` should be ≤ `ReplicaCount`. Start with 2 or 3 for production
3. **Balance consistency vs availability**: Higher quorums provide stronger consistency but reduce availability
4. **Register all actor kinds**: Ensure all nodes register the actor types they need to host

### Network

1. **Use stable network identities**: Avoid dynamic IPs without proper DNS or service discovery
2. **Configure appropriate timeouts**: Network latency varies; adjust read/write timeouts accordingly
3. **Enable TLS**: Use `WithTLS()` for production clusters to encrypt inter-node communication
4. **Separate discovery and remoting ports**: Use different ports for discovery, peers, and remoting

### Placement

1. **Use roles for workload segregation**: Separate compute-intensive actors from I/O-bound actors
2. **Prefer SpawnOn for cluster-wide actors**: Use `Spawn()` only when you specifically need the actor local
3. **Use appropriate placement strategies**:
   - `RoundRobin`: Even distribution
   - `Random`: Simple load balancing
   - `LeastLoad`: Best for heterogeneous clusters (requires metrics)
   - `Local`: Pin to current node

### Operations

1. **Monitor cluster events**: Subscribe to `NodeJoined` and `NodeLeft` events for observability
2. **Implement health checks**: Use `system.Running()` and `system.InCluster()` for readiness probes
3. **Plan for rolling updates**: Use `MinimumPeersQuorum` and `GrainActivationBarrier` to avoid churn
4. **Test failure scenarios**: Simulate node failures and verify actor relocation works as expected

### Performance

1. **Tune sync intervals**: Balance consistency freshness with overhead
2. **Adjust table size**: Increase `WithTableSize()` if storing large actor metadata
3. **Use local references when possible**: `PID.Tell()` is faster than remote messaging
4. **Batch operations**: Minimize cluster state updates by batching related spawns

## Troubleshooting

### Cluster Won't Bootstrap

- **Check minimum quorum**: Ensure enough nodes are starting simultaneously
- **Verify discovery provider**: Confirm nodes can discover each other
- **Review network connectivity**: Check that discovery and peers ports are reachable
- **Increase bootstrap timeout**: Some discovery providers need more time

### Actor Not Found

- **Check actor kind registration**: Use `WithKinds()` on all nodes
- **Verify cluster is ready**: Wait for bootstrap to complete
- **Review actor name**: Names must be unique across the cluster
- **Check node roles**: If using roles, ensure target nodes advertise the required role

### Split Brain

- **Ensure proper quorum settings**: Use majority quorums (`replicaCount=3, writeQuorum=2, readQuorum=2`)
- **Check network partitions**: Use monitoring to detect partial network failures
- **Review discovery provider health**: Ensure the discovery backend is stable

### Performance Issues

- **Check routing table sync interval**: May be too frequent for large clusters
- **Review partition count**: Too few partitions limit parallelism; too many increase overhead
- **Monitor network latency**: High latency between nodes affects cluster operations
- **Analyze actor distribution**: Use metrics to detect hot spots

## Next Steps

- [Discovery Providers](discovery.md): Learn about available discovery mechanisms
- [Cluster Singleton](cluster_singleton.md): Deep dive into singleton actors
- [Multi-Datacenter](multi_datacenters.md): Deploy across multiple datacenters
- [Actor Relocation](relocation.md): Understanding automatic actor migration
