# Consul Discovery Provider

The Consul discovery provider allows GoAkt cluster nodes to discover each other and form a cluster using **HashiCorp Consul** as the service registry. Nodes register as services with health checks and discover peers by querying the Consul catalog.

## Architecture

### Discovery Flow

```
    +---------------------+  DiscoverPeers()   +---------------------+
    | discovery.Provider  |  returns []string  |       Olric         |
    | (consul)            |  host:discoveryPort| (cluster engine)    |
    +---------------------+ -----------------> +---------------------+
              |                                         |
              |  Register / Health check                 v
              |  Catalog query                   +---------------+
              v                                  |  Memberlist   |
    +---------------------+                      |  (gossip)     |
    |   Consul Agent      |                      +---------------+
    |   (service registry)|
    +---------------------+
```

- **Discovery provider** is used by Olric **only for bootstrap**: it periodically calls `DiscoverPeers()` to get peer addresses.
- **Consul** stores service registrations and health status; nodes register as services and query the catalog for peers.
- **Memberlist** (HashiCorp) handles ongoing membership via SWIM gossip. Once a node joins (via any discovered peer), it learns about all other members through gossip.

### Provider Interface

```go
type Provider interface {
    ID() string
    Initialize() error
    Register() error
    Deregister() error
    DiscoverPeers() ([]string, error)  // host:discoveryPort format
    Close() error
}
```

### Address Format

- `DiscoverPeers()` returns addresses in `host:discoveryPort` format.
- `discoveryPort` is the Memberlist/gossip port (configured via `ClusterConfig.WithDiscoveryPort()`).
- The cluster uses `discovery.Node` with `Host`, `DiscoveryPort`, `PeersPort`, `RemotingPort`.

---

## Implementation

### How It Works

1. **Initialize**: Create Consul client, verify connection via `Agent().Self()`.
2. **Register**: Register this node as a service with Consul; configure health check (HTTP or TTL).
3. **DiscoverPeers**: Query Consul catalog for services matching `ActorSystemName`; return healthy peers.
4. **Deregister**: Remove service registration from Consul.

```
    Node A                                    Node B
    ------                                    ------
    Connect to Consul                         Connect to Consul
    Register service                          Register service
    Query catalog                             Query catalog
    DiscoverPeers() -> [B]                    DiscoverPeers() -> [A]
    Olric joins B                             Olric joins A
    |                                         |
    +------------------+-----------------------+
                       |
           Gossip propagates full membership
```

### Health Checks

Consul performs health checks on registered services. Unhealthy nodes are excluded from discovery. Configure `HealthCheck.Interval` and `HealthCheck.Timeout` for failure detection speed.

---

## Configuration

```go
type Config struct {
    Context         context.Context  // Optional. Default context.Background().
    Address         string          // Required. Consul agent address (e.g. 127.0.0.1:8500).
    Datacenter      string          // Optional. Consul datacenter.
    Token           string          // Optional. ACL token for auth.
    Timeout         time.Duration   // Optional. Request timeout. Default 10s.
    ActorSystemName string          // Required. Service name (cluster identifier).
    Host            string          // Required. This node's host.
    DiscoveryPort   int             // Required. This node's discovery port.
    QueryOptions    *QueryOptions   // Optional. Catalog query options.
    HealthCheck     *HealthCheck   // Optional. Service health check config.
}
```

### Example

```go
config := &consul.Config{
    Address:         "127.0.0.1:8500",
    ActorSystemName: "my-app",
    Host:            "192.168.1.10",
    DiscoveryPort:   7946,
    Timeout:         10 * time.Second,
    HealthCheck: &consul.HealthCheck{
        Interval: 10 * time.Second,
        Timeout:  3 * time.Second,
    },
}

provider := consul.NewDiscovery(config)
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(provider).
    WithDiscoveryPort(7946).
    WithKinds(myActor)
```

---

## Package Structure

```
discovery/consul/
├── config.go       # Config + validation
├── discovery.go    # Provider implementation
├── config_test.go
├── discovery_test.go
└── README.md
```

---

## Scope and Limitations

| Scope             | Works                          |
|-------------------|--------------------------------|
| Same subnet (LAN) | Yes                            |
| Cross-subnet      | Yes (Consul routes)            |
| Cloud             | Yes                            |
| Loopback (tests)  | Yes (run Consul agent locally) |

Requires a running Consul agent. Supports TLS and ACL authentication.

---

## Design Goals

| Goal                 | Description                               |
|----------------------|-------------------------------------------|
| **Service registry** | Consul as centralized service catalog     |
| **Health-aware**     | Excludes unhealthy nodes from discovery   |
| **Pluggable**        | Implements `discovery.Provider` interface |
