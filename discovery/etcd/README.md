# Etcd Discovery Provider

The etcd discovery provider allows GoAkt cluster nodes to discover each other and form a cluster using **etcd** as the distributed key-value store. Nodes register under a namespaced prefix and discover peers by listing keys in the cluster.

## Architecture

### Discovery Flow

```
    +---------------------+  DiscoverPeers()   +---------------------+
    | discovery.Provider  |  returns []string  |       Olric         |
    | (etcd)              |  host:discoveryPort| (cluster engine)    |
    +---------------------+ -----------------> +---------------------+
              |                                         |
              |  Put / Lease / Get                       v
              |  List prefix                     +---------------+
              v                                  |  Memberlist   |
    +---------------------+                      |  (gossip)     |
    |   etcd Cluster     |                      +---------------+
    |   (key-value store) |
    +---------------------+
```

- **Discovery provider** is used by Olric **only for bootstrap**: it periodically calls `DiscoverPeers()` to get peer addresses.
- **etcd** stores node registrations under `ActorSystemName/` prefix; nodes use leases for TTL-based lifecycle.
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

1. **Initialize**: Connect to etcd cluster; verify connectivity.
2. **Register**: Create lease with TTL; put key `ActorSystemName/host:port` with keep-alive.
3. **DiscoverPeers**: List keys under `ActorSystemName/` prefix; return peer addresses (excluding self).
4. **Deregister**: Revoke lease; key is removed.

```
    Node A                                    Node B
    ------                                    ------
    Connect to etcd                          Connect to etcd
    Put key with lease                        Put key with lease
    List prefix                               List prefix
    DiscoverPeers() -> [B]                    DiscoverPeers() -> [A]
    Olric joins B                             Olric joins A
    |                                         |
    +------------------+-----------------------+
                       |
           Gossip propagates full membership
```

### Lease and TTL

Nodes use etcd leases with TTL. Keep-alive ensures the key remains while the node is alive; if the node crashes, the lease expires and the key is removed automatically.

---

## Configuration

```go
type Config struct {
    Context       context.Context  // Optional. Default context.Background().
    Endpoints     []string         // Required. Etcd cluster endpoints.
    ActorSystemName string        // Required. Namespace prefix (cluster identifier).
    Host          string          // Required. This node's host.
    DiscoveryPort int             // Required. This node's discovery port.
    TTL           int64           // Required. Lease TTL in seconds.
    TLS           *tls.Config    // Optional. TLS config.
    DialTimeout   time.Duration   // Required. Connection timeout.
    Username      string          // Optional. Auth username.
    Password      string          // Optional. Auth password.
    Timeout       time.Duration   // Optional. Operation timeout.
}
```

### Example

```go
config := &etcd.Config{
    Endpoints:       []string{"http://127.0.0.1:2379"},
    ActorSystemName: "my-app",
    Host:            "192.168.1.10",
    DiscoveryPort:   7946,
    TTL:             60,
    DialTimeout:     5 * time.Second,
}

provider := etcd.NewDiscovery(config)
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(provider).
    WithDiscoveryPort(7946).
    WithKinds(myActor)
```

---

## Package Structure

```
discovery/etcd/
├── config.go       # Config + validation
├── discovery.go    # Provider implementation
├── config_test.go
├── discovery_test.go
└── README.md
```

---

## Scope and Limitations

| Scope             | Works                  |
|-------------------|------------------------|
| Same subnet (LAN) | Yes                    |
| Cross-subnet      | Yes (etcd cluster)     |
| Cloud             | Yes                    |
| Loopback (tests)  | Yes (run etcd locally) |

Requires a running etcd cluster. Supports TLS and basic auth.

---

## Design Goals

| Goal                    | Description                               |
|-------------------------|-------------------------------------------|
| **Distributed store**   | etcd as cluster-wide registration         |
| **TTL-based lifecycle** | Lease expiry removes crashed nodes        |
| **Pluggable**           | Implements `discovery.Provider` interface |
