# mDNS Discovery Provider

The mDNS discovery provider allows GoAkt cluster nodes to discover each other using **multicast DNS** (mDNS/Bonjour) on the local network. Nodes register as services and browse for peers with the same service type. No central server is required.

## Architecture

### Discovery Flow

```
    +---------------------+  DiscoverPeers()   +---------------------+
    | discovery.Provider  |  returns []string  |       Olric         |
    | (mdns)              |  host:discoveryPort| (cluster engine)    |
    +---------------------+ -----------------> +---------------------+
              |                                         |
              |  Register / Browse                       v
              |  mDNS multicast                 +---------------+
              v                                  |  Memberlist   |
    +---------------------+                      |  (gossip)     |
    |   mDNS (multicast)   |                      +---------------+
    |   (local network)   |
    +---------------------+
```

- **Discovery provider** is used by Olric **only for bootstrap**: it periodically calls `DiscoverPeers()` to get peer addresses.
- **mDNS** uses multicast on the local link; nodes advertise and discover services without a central registry.
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

- `DiscoverPeers()` returns addresses in `host:port` format.
- Port comes from the registered service entry.
- The cluster uses `discovery.Node` with `Host`, `DiscoveryPort`, `PeersPort`, `RemotingPort`.

---

## Implementation

### How It Works

1. **Initialize**: Validate config.
2. **Register**: Start an mDNS server advertising a unique instance (ServiceName plus a random suffix) of the configured Service type and Domain on Port, with the cluster name in a TXT record.
3. **DiscoverPeers**: Browse for services matching Service type and Domain; keep the entries whose Port and cluster name match; return addresses.
4. **Deregister**: Shutdown server; close stop channel.

```
    Node A                                    Node B
    ------                                    ------
    Register service                          Register service
    Browse for peers                          Browse for peers
    DiscoverPeers() -> [B]                    DiscoverPeers() -> [A]
    Olric joins B                             Olric joins A
    |                                         |
    +------------------+-----------------------+
                       |
           Gossip propagates full membership
```

### Service Matching

Entries are kept when their `Port` matches, their name ends with the configured `Service` and `Domain`, and their TXT record carries `name=<ServiceName>`. Every node registers its own instance name, so nodes of one cluster do not overwrite each other on the wire.

---

## Configuration

```go
type Config struct {
    ServiceName string  // Required. Cluster name shared by all nodes.
    Service     string  // Required. Service type (e.g. _goakt._tcp).
    Domain      string  // Required. Domain (e.g. local.).
    Port        int     // Required. Port the service listens on.
    IPv6        *bool   // Optional. Prefer IPv6 addresses.
}
```

### Example

```go
config := &mdns.Config{
    ServiceName: "goakt-node-1",
    Service:     "_goakt._tcp",
    Domain:      "local.",
    Port:        7946,
}

provider := mdns.NewDiscovery(config)
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(provider).
    WithDiscoveryPort(7946).
    WithKinds(myActor)
```

---

## Package Structure

```
discovery/mdns/
├── config.go       # Config + validation
├── discovery.go    # Provider implementation
├── option.go       # Provider options
└── README.md
```

---

## Scope and Limitations

| Scope             | Works                    |
|-------------------|--------------------------|
| Same subnet (LAN) | Yes                      |
| Cross-subnet      | No (mDNS is link-local)  |
| Loopback (tests)  | May work on some systems |

Uses github.com/hashicorp/mdns. mDNS is typically link-local.

---

## Design Goals

| Goal                    | Description                               |
|-------------------------|-------------------------------------------|
| **Zero central server** | Multicast-based, no registry              |
| **Standard protocol**   | mDNS/Bonjour                              |
| **Pluggable**           | Implements `discovery.Provider` interface |
