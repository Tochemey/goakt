# Discovery Providers

Discovery providers enable cluster nodes to automatically find and connect to each other without manual configuration. GoAkt supports multiple discovery mechanisms for different deployment environments.

## Overview

When a clustered actor system starts, it needs to discover other nodes in the cluster. The discovery provider is responsible for:

1. **Registration**: Advertising the node's presence to other nodes
2. **Discovery**: Finding other nodes that are part of the same cluster
3. **Health monitoring**: Keeping track of which nodes are alive
4. **Deregistration**: Removing the node when it shuts down

## Available Providers

GoAkt includes the following discovery providers out of the box:

- **Static**: Pre-configured list of hosts (for testing or small deployments)
- **NATS**: Discovery via NATS messaging system
- **Consul**: HashiCorp Consul service discovery
- **etcd**: CoreOS etcd distributed key-value store
- **Kubernetes**: Native Kubernetes service discovery
- **mDNS**: Multicast DNS for local networks
- **DNS-SD**: DNS service discovery

## Configuration

All discovery providers are configured through the `ClusterConfig`:

```go
import "github.com/tochemey/goakt/v3/actor"

clusterConfig := actor.NewClusterConfig().
    WithDiscovery(discoveryProvider).
    WithDiscoveryPort(3320). // Port for discovery communication
    WithPeersPort(3322)      // Port for cluster peer-to-peer traffic
```

## Static Discovery

The simplest provider for testing or small, fixed-size clusters where node addresses are known in advance.

### Use Cases

- Local development and testing
- Small production deployments with fixed nodes
- Docker Compose setups
- Static cloud deployments

### Configuration

```go
import "github.com/tochemey/goakt/v3/discovery/static"

// Define the list of cluster nodes (gossip port)
config := &static.Config{
    Hosts: []string{
        "192.168.1.10:3320",
        "192.168.1.11:3320",
        "192.168.1.12:3320",
    },
}

disco := static.NewDiscovery(config)
```

### Full Example

```go
package main

import (
    "context"
    "log"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/discovery/static"
)

func main() {
    ctx := context.Background()

    // Static discovery
    disco := static.NewDiscovery(&static.Config{
        Hosts: []string{
            "node1:3320",
            "node2:3320",
            "node3:3320",
        },
    })

    // Cluster configuration
    clusterConfig := actor.NewClusterConfig().
        WithDiscovery(disco).
        WithDiscoveryPort(3320).
        WithPeersPort(3322).
        WithMinimumPeersQuorum(2).
        WithKinds(new(MyActor))

    // Create and start the system
    system, err := actor.NewActorSystem(
        "static-cluster",
        actor.WithCluster(clusterConfig),
        actor.WithRemote("localhost", 3321),
    )
    if err != nil {
        log.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    log.Println("Cluster node started with static discovery")
    select {} // Keep running
}
```

### Limitations

- No dynamic node addition/removal
- Requires updating all nodes when topology changes
- Not suitable for auto-scaling environments

## NATS Discovery

Uses [NATS](https://nats.io/) for dynamic peer discovery. Nodes publish their presence on a NATS subject and subscribe to discover others.

### Use Cases

- Cloud deployments with NATS infrastructure
- Microservices architectures using NATS
- Dynamic clusters with auto-scaling
- Multi-datacenter setups with NATS JetStream

### Prerequisites

- Running NATS server (standalone or cluster)
- Network connectivity from all nodes to NATS

### Configuration

```go
import "github.com/tochemey/goakt/v3/discovery/nats"

config := &nats.Config{
    NatsServer:      "nats://nats-server:4222",  // NATS server URL
    NatsSubject:     "goakt.cluster.discovery",   // Subject for discovery messages
    Host:            "10.0.1.5",                  // This node's host
    DiscoveryPort:   3320,                        // This node's discovery port
    Timeout:         5 * time.Second,             // Discovery timeout
    MaxJoinAttempts: 5,                           // Max connection attempts
    ReconnectWait:   2 * time.Second,             // Backoff between reconnects
}

disco := nats.NewDiscovery(config)
```

### Configuration Options

| Field             | Description                                  | Default  |
|-------------------|----------------------------------------------|----------|
| `NatsServer`      | NATS server URL in format `nats://host:port` | Required |
| `NatsSubject`     | NATS subject for discovery messages          | Required |
| `Host`            | This node's advertised hostname/IP           | Required |
| `DiscoveryPort`   | Port for discovery protocol                  | Required |
| `Timeout`         | Discovery operation timeout                  | None     |
| `MaxJoinAttempts` | Maximum connection attempts to NATS          | 5        |
| `ReconnectWait`   | Time between reconnection attempts           | 2s       |

### Full Example

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/discovery/nats"
)

func main() {
    ctx := context.Background()

    // NATS discovery
    disco := nats.NewDiscovery(&nats.Config{
        NatsServer:      "nats://localhost:4222",
        NatsSubject:     "my-app.cluster",
        Host:            "localhost",
        DiscoveryPort:   3320,
        Timeout:         5 * time.Second,
        MaxJoinAttempts: 5,
        ReconnectWait:   2 * time.Second,
    })

    clusterConfig := actor.NewClusterConfig().
        WithDiscovery(disco).
        WithDiscoveryPort(3320).
        WithPeersPort(3322).
        WithMinimumPeersQuorum(1).
        WithKinds(new(MyActor))

    system, err := actor.NewActorSystem(
        "nats-cluster",
        actor.WithCluster(clusterConfig),
        actor.WithRemote("localhost", 3321),
    )
    if err != nil {
        log.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    log.Println("Cluster node started with NATS discovery")
    select {}
}
```

## Consul Discovery

Integrates with [HashiCorp Consul](https://www.consul.io/) for service discovery and health checking.

### Use Cases

- Consul-based infrastructure
- Service mesh deployments
- Multi-datacenter deployments using Consul federation
- Environments requiring health checks

### Prerequisites

- Running Consul agent or cluster
- Network connectivity to Consul

### Configuration

```go
import "github.com/tochemey/goakt/v3/discovery/consul"

config := &consul.Config{
    Address:         "127.0.0.1:8500",           // Consul agent address
    ActorSystemName: "my-system",                // Service name in Consul
    Host:            "10.0.1.5",                 // This node's host
    DiscoveryPort:   3320,                       // Discovery port
    Datacenter:      "dc1",                      // Consul datacenter
    Token:           "consul-acl-token",         // ACL token (optional)
    Timeout:         10 * time.Second,           // Request timeout
    HealthCheck: &consul.HealthCheck{
        Interval: 10 * time.Second,              // Health check interval
        Timeout:  3 * time.Second,               // Health check timeout
    },
    QueryOptions: &consul.QueryOptions{
        OnlyPassing: true,                       // Only discover healthy services
        AllowStale:  false,                      // Require consistent reads
        WaitTime:    time.Second,                // Blocking query wait
    },
}

disco := consul.NewDiscovery(config)
```

### Configuration Options

| Field             | Description                   | Default               |
|-------------------|-------------------------------|-----------------------|
| `Address`         | Consul agent address          | `127.0.0.1:8500`      |
| `ActorSystemName` | Service name for registration | Required              |
| `Host`            | This node's advertised host   | Required              |
| `DiscoveryPort`   | Discovery port to advertise   | Required              |
| `Datacenter`      | Consul datacenter to use      | Agent's default       |
| `Token`           | Consul ACL token              | None                  |
| `Timeout`         | Request timeout               | 10s                   |
| `HealthCheck`     | Health check configuration    | Enabled, 10s interval |
| `QueryOptions`    | Query filtering options       | See below             |

#### Query Options

- `OnlyPassing`: Return only services passing health checks
- `Near`: Sort by network distance to specified node
- `WaitTime`: Wait duration for blocking queries
- `AllowStale`: Allow stale reads from followers
- `Datacenter`: Target datacenter for queries

### Full Example

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/discovery/consul"
)

func main() {
    ctx := context.Background()

    disco := consul.NewDiscovery(&consul.Config{
        Address:         "localhost:8500",
        ActorSystemName: "my-cluster",
        Host:            "localhost",
        DiscoveryPort:   3320,
        Timeout:         10 * time.Second,
        HealthCheck: &consul.HealthCheck{
            Interval: 10 * time.Second,
            Timeout:  3 * time.Second,
        },
        QueryOptions: &consul.QueryOptions{
            OnlyPassing: true,
        },
    })

    clusterConfig := actor.NewClusterConfig().
        WithDiscovery(disco).
        WithDiscoveryPort(3320).
        WithPeersPort(3322).
        WithMinimumPeersQuorum(1).
        WithKinds(new(MyActor))

    system, err := actor.NewActorSystem(
        "consul-cluster",
        actor.WithCluster(clusterConfig),
        actor.WithRemote("localhost", 3321),
    )
    if err != nil {
        log.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    log.Println("Cluster node started with Consul discovery")
    select {}
}
```

## etcd Discovery

Uses [etcd](https://etcd.io/) distributed key-value store for service registration and discovery.

### Use Cases

- Kubernetes clusters with etcd
- Infrastructure already using etcd
- Strongly consistent discovery requirements
- Multi-datacenter with etcd clusters

### Prerequisites

- Running etcd cluster
- Network connectivity to etcd endpoints

### Configuration

```go
import "github.com/tochemey/goakt/v3/discovery/etcd"

config := &etcd.Config{
    Endpoints:       []string{"http://etcd1:2379", "http://etcd2:2379"},
    ActorSystemName: "my-system",
    Host:            "10.0.1.5",
    DiscoveryPort:   3320,
    TTL:             30,                     // Lease TTL in seconds
    DialTimeout:     5 * time.Second,        // Connection timeout
    Timeout:         10 * time.Second,       // Operation timeout
    Username:        "etcd-user",            // Optional auth
    Password:        "etcd-password",        // Optional auth
    TLS:             tlsConfig,              // Optional TLS
}

disco := etcd.NewDiscovery(config)
```

### Configuration Options

| Field             | Description                   | Default  |
|-------------------|-------------------------------|----------|
| `Endpoints`       | List of etcd server endpoints | Required |
| `ActorSystemName` | Service identifier            | Required |
| `Host`            | This node's advertised host   | Required |
| `DiscoveryPort`   | Discovery port                | Required |
| `TTL`             | Lease time-to-live (seconds)  | Required |
| `DialTimeout`     | Connection timeout            | Required |
| `Timeout`         | Operation timeout             | Required |
| `Username`        | Authentication username       | None     |
| `Password`        | Authentication password       | None     |
| `TLS`             | TLS configuration             | None     |

### Full Example

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/discovery/etcd"
)

func main() {
    ctx := context.Background()

    disco := etcd.NewDiscovery(&etcd.Config{
        Endpoints:       []string{"http://localhost:2379"},
        ActorSystemName: "my-cluster",
        Host:            "localhost",
        DiscoveryPort:   3320,
        TTL:             30,
        DialTimeout:     5 * time.Second,
        Timeout:         10 * time.Second,
    })

    clusterConfig := actor.NewClusterConfig().
        WithDiscovery(disco).
        WithDiscoveryPort(3320).
        WithPeersPort(3322).
        WithMinimumPeersQuorum(1).
        WithKinds(new(MyActor))

    system, err := actor.NewActorSystem(
        "etcd-cluster",
        actor.WithCluster(clusterConfig),
        actor.WithRemote("localhost", 3321),
    )
    if err != nil {
        log.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    log.Println("Cluster node started with etcd discovery")
    select {}
}
```

## Kubernetes Discovery

Native Kubernetes service discovery using the Kubernetes API. Discovers pods via label selectors.

### Use Cases

- Applications running in Kubernetes
- StatefulSets or Deployments
- Kubernetes-native service discovery
- Multi-namespace deployments

### Prerequisites

- Running in a Kubernetes cluster
- Service account with permissions to list pods
- Named ports in pod specifications

### Configuration

```go
import "github.com/tochemey/goakt/v3/discovery/kubernetes"

config := &kubernetes.Config{
    Namespace:         "default",                    // Kubernetes namespace
    DiscoveryPortName: "discovery",                  // Named port for discovery
    RemotingPortName:  "remoting",                   // Named port for remoting
    PeersPortName:     "peers",                      // Named port for clustering
    PodLabels: map[string]string{
        "app":     "my-cluster",
        "cluster": "prod",
    },
}

disco := kubernetes.NewDiscovery(config)
```

### Configuration Options

| Field               | Description                       | Default  |
|---------------------|-----------------------------------|----------|
| `Namespace`         | Kubernetes namespace to search    | Required |
| `DiscoveryPortName` | Named port for discovery protocol | Required |
| `RemotingPortName`  | Named port for actor remoting     | Required |
| `PeersPortName`     | Named port for cluster gossip     | Required |
| `PodLabels`         | Label selector for pods           | Required |

### Kubernetes Deployment Example

```yaml
apiVersion: v1
kind: Service
metadata:
  name: goakt-cluster
  namespace: default
spec:
  clusterIP: None # Headless service
  selector:
    app: my-cluster
    cluster: prod
  ports:
    - name: discovery
      port: 3320
      targetPort: discovery
    - name: remoting
      port: 3321
      targetPort: remoting
    - name: peers
      port: 3322
      targetPort: peers
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: goakt-cluster
  namespace: default
spec:
  serviceName: goakt-cluster
  replicas: 3
  selector:
    matchLabels:
      app: my-cluster
      cluster: prod
  template:
    metadata:
      labels:
        app: my-cluster
        cluster: prod
    spec:
      containers:
        - name: app
          image: my-goakt-app:latest
          ports:
            - name: discovery
              containerPort: 3320
            - name: remoting
              containerPort: 3321
            - name: peers
              containerPort: 3322
```

### Full Example

```go
package main

import (
    "context"
    "log"
    "os"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/discovery/kubernetes"
)

func main() {
    ctx := context.Background()

    disco := kubernetes.NewDiscovery(&kubernetes.Config{
        Namespace:         "default",
        DiscoveryPortName: "discovery",
        RemotingPortName:  "remoting",
        PeersPortName:     "peers",
        PodLabels: map[string]string{
            "app":     "my-cluster",
            "cluster": "prod",
        },
    })

    // Get pod IP from Kubernetes
    podIP := os.Getenv("POD_IP")

    clusterConfig := actor.NewClusterConfig().
        WithDiscovery(disco).
        WithDiscoveryPort(3320).
        WithPeersPort(3322).
        WithMinimumPeersQuorum(2).
        WithKinds(new(MyActor))

    system, err := actor.NewActorSystem(
        "k8s-cluster",
        actor.WithCluster(clusterConfig),
        actor.WithRemote(podIP, 3321),
    )
    if err != nil {
        log.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    log.Println("Cluster node started in Kubernetes")
    select {}
}
```

## mDNS Discovery

Uses multicast DNS for zero-configuration discovery on local networks.

### Use Cases

- Local development
- Testing clusters on the same network
- Small office/home networks
- Environments without centralized discovery

### Configuration

```go
import "github.com/tochemey/goakt/v3/discovery/mdns"

ipv6 := false
config := &mdns.Config{
    ServiceName: "my-cluster",           // Service instance name
    Service:     "_goakt._tcp",          // Service type
    Domain:      "local.",               // Service domain
    Port:        3320,                   // Discovery port
    IPv6:        &ipv6,                  // Use IPv4 (false) or IPv6 (true)
}

disco := mdns.NewDiscovery(config)
```

### Configuration Options

| Field         | Description                        | Default      |
|---------------|------------------------------------|--------------|
| `ServiceName` | Unique service instance name       | Required     |
| `Service`     | Service type (e.g., `_goakt._tcp`) | Required     |
| `Domain`      | mDNS domain                        | Required     |
| `Port`        | Discovery port to advertise        | Required     |
| `IPv6`        | Use IPv6 addresses                 | false (IPv4) |

### Full Example

```go
package main

import (
    "context"
    "log"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/discovery/mdns"
)

func main() {
    ctx := context.Background()

    ipv6 := false
    disco := mdns.NewDiscovery(&mdns.Config{
        ServiceName: "my-cluster-node1",
        Service:     "_goakt._tcp",
        Domain:      "local.",
        Port:        3320,
        IPv6:        &ipv6,
    })

    clusterConfig := actor.NewClusterConfig().
        WithDiscovery(disco).
        WithDiscoveryPort(3320).
        WithPeersPort(3322).
        WithMinimumPeersQuorum(1).
        WithKinds(new(MyActor))

    system, err := actor.NewActorSystem(
        "mdns-cluster",
        actor.WithCluster(clusterConfig),
        actor.WithRemote("localhost", 3321),
    )
    if err != nil {
        log.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    log.Println("Cluster node started with mDNS discovery")
    select {}
}
```

### Limitations

- Only works on local networks supporting multicast
- Not suitable for cloud deployments
- May not work across VLANs or complex network topologies

## DNS-SD Discovery

DNS-based Service Discovery using SRV records.

### Use Cases

- Environments with existing DNS infrastructure
- Cloud providers supporting DNS service discovery (AWS Route53, GCP Cloud DNS)
- Kubernetes with ExternalDNS
- Private DNS servers

### Configuration

```go
import "github.com/tochemey/goakt/v3/discovery/dnssd"

ipv6 := false
config := &dnssd.Config{
    DomainName: "goakt-cluster.example.com",  // DNS domain for service
    IPv6:       &ipv6,                         // Use IPv4 or IPv6
}

disco := dnssd.NewDiscovery(config)
```

### Configuration Options

| Field        | Description                    | Default  |
|--------------|--------------------------------|----------|
| `DomainName` | DNS domain name for SRV lookup | Required |
| `IPv6`       | Prefer IPv6 addresses          | false    |

### DNS Configuration

Set up SRV records pointing to your cluster nodes:

```
_discovery._tcp.goakt-cluster.example.com. 300 IN SRV 0 5 3320 node1.example.com.
_discovery._tcp.goakt-cluster.example.com. 300 IN SRV 0 5 3320 node2.example.com.
_discovery._tcp.goakt-cluster.example.com. 300 IN SRV 0 5 3320 node3.example.com.
```

### Full Example

```go
package main

import (
    "context"
    "log"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/discovery/dnssd"
)

func main() {
    ctx := context.Background()

    ipv6 := false
    disco := dnssd.NewDiscovery(&dnssd.Config{
        DomainName: "goakt-cluster.example.com",
        IPv6:       &ipv6,
    })

    clusterConfig := actor.NewClusterConfig().
        WithDiscovery(disco).
        WithDiscoveryPort(3320).
        WithPeersPort(3322).
        WithMinimumPeersQuorum(2).
        WithKinds(new(MyActor))

    system, err := actor.NewActorSystem(
        "dnssd-cluster",
        actor.WithCluster(clusterConfig),
        actor.WithRemote("node1.example.com", 3321),
    )
    if err != nil {
        log.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    log.Println("Cluster node started with DNS-SD discovery")
    select {}
}
```

## Choosing a Discovery Provider

| Provider       | Best For                       | Pros                                   | Cons                                  |
|----------------|--------------------------------|----------------------------------------|---------------------------------------|
| **Static**     | Testing, fixed clusters        | Simple, no dependencies                | Manual configuration, no auto-scaling |
| **NATS**       | Dynamic clouds, microservices  | Fast, lightweight, scalable            | Requires NATS infrastructure          |
| **Consul**     | Service mesh, health checks    | Rich features, multi-DC, health checks | More complex setup                    |
| **etcd**       | Kubernetes, strong consistency | Strongly consistent, reliable          | Requires etcd cluster                 |
| **Kubernetes** | K8s deployments                | Native K8s integration                 | Kubernetes-only                       |
| **mDNS**       | Local dev, testing             | Zero-config, no dependencies           | Local network only                    |
| **DNS-SD**     | Existing DNS infrastructure    | Leverages DNS, simple                  | Requires DNS management               |

## Best Practices

### General

1. **Match your infrastructure**: Choose the provider that aligns with your existing infrastructure
2. **Test failure scenarios**: Verify discovery recovers from network partitions and node failures
3. **Configure timeouts appropriately**: Balance responsiveness with stability
4. **Monitor discovery health**: Track registration/deregistration events

### Production

1. **Use proven providers**: Consul, etcd, NATS, or Kubernetes for production
2. **Enable health checks**: When available (Consul, Kubernetes)
3. **Configure redundancy**: Use clustered discovery backends (Consul cluster, etcd cluster)
4. **Secure communications**: Use TLS where supported
5. **Set realistic quorums**: Ensure `MinimumPeersQuorum` is achievable during bootstrap

### Development

1. **Use simpler providers**: Static, mDNS, or NATS for local development
2. **Minimize dependencies**: Avoid requiring production infrastructure locally
3. **Document setup**: Provide clear instructions for starting discovery dependencies

## Troubleshooting

### Nodes Can't Discover Each Other

- **Check network connectivity**: Ensure discovery ports are reachable
- **Verify discovery backend**: Confirm the discovery service (NATS, Consul, etc.) is running and accessible
- **Review firewall rules**: Open required ports
- **Check configuration**: Ensure all nodes use the same discovery subject/service name
- **Inspect logs**: Look for discovery provider errors

### Slow Cluster Formation

- **Increase timeouts**: Adjust `BootstrapTimeout` and discovery provider timeouts
- **Check network latency**: High latency affects discovery speed
- **Review quorum settings**: Lower `MinimumPeersQuorum` for testing
- **Monitor discovery backend**: Ensure discovery service is responsive

### Nodes Repeatedly Join/Leave

- **Check health checks**: If using Consul, ensure health checks are properly configured
- **Review TTL settings**: etcd TTL may be too short
- **Network stability**: Intermittent connectivity causes churn
- **Resource constraints**: Node may be restarting due to OOM or CPU throttling

## Next Steps

- [Cluster Overview](overview.md): Learn about clustering fundamentals
- [Cluster Singleton](cluster_singleton.md): Create singleton actors across the cluster
- [Actor Relocation](relocation.md): Understanding automatic actor migration
