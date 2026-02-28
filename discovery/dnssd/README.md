# DNS-SD Discovery Provider

The DNS-SD discovery provider resolves peer addresses by performing **DNS lookups** on a configured domain name. It queries the default DNS resolver for A and AAAA records. Suitable when peers are exposed via a DNS name (e.g. load balancer, service discovery DNS).

## Architecture

### Discovery Flow

```
    +---------------------+  DiscoverPeers()   +---------------------+
    | discovery.Provider  |  returns []string  |       Olric         |
    | (dnssd)             |  IP addresses      | (cluster engine)    |
    +---------------------+ -----------------> +---------------------+
              |                                         |
              |  LookupIPAddr / LookupIP                 v
              |  (DNS A/AAAA)                    +---------------+
              v                                  |  Memberlist   |
    +---------------------+                      |  (gossip)     |
    |   DNS Resolver      |                      +---------------+
    |   (system/default)  |
    +---------------------+
```

- **Discovery provider** is used by Olric **only for bootstrap**: it periodically calls `DiscoverPeers()` to get peer addresses.
- **DNS** provides the mapping from domain name to IP addresses.
- **Memberlist** (HashiCorp) handles ongoing membership via SWIM gossip. Once a node joins (via any discovered peer), it learns about all other members through gossip.

### Provider Interface

```go
type Provider interface {
    ID() string
    Initialize() error
    Register() error
    Deregister() error
    DiscoverPeers() ([]string, error)  // IP addresses from DNS
    Close() error
}
```

### Address Format

- `DiscoverPeers()` returns IP addresses from DNS lookup.
- When `IPv6` is true, returns IPv6 addresses only; otherwise returns all resolved addresses.
- The cluster expects `host:discoveryPort` format; ensure the DNS name or usage matches cluster configuration.

---

## Implementation

### How It Works

1. **Initialize**: Validate config.
2. **Register**: No-op (marks initialized).
3. **DiscoverPeers**: Use `net.Resolver.LookupIPAddr` (or `LookupIP` for IPv6) on `DomainName`; return unique IPs.
4. **Deregister** / **Close**: No-op.

```
    Node A                                    Node B
    ------                                    ------
    LookupIPAddr(domain)                      LookupIPAddr(domain)
    DiscoverPeers() -> [ip1, ip2, ...]        DiscoverPeers() -> [ip1, ip2, ...]
    Olric joins peers                          Olric joins peers
    |                                          |
    +------------------+-----------------------+
                       |
           Gossip propagates full membership
```

### Use Cases

- DNS round-robin or load balancer returning multiple A/AAAA records.
- Service discovery systems that publish DNS names for cluster members.
- Kubernetes headless services with DNS-based pod discovery.

---

## Configuration

```go
type Config struct {
    DomainName string  // Required. DNS name to resolve (e.g. goakt-cluster.local).
    IPv6       *bool   // Optional. If true, resolve IPv6 only; else all addresses.
}
```

### Example

```go
v6 := false
config := &dnssd.Config{
    DomainName: "goakt-cluster.default.svc.cluster.local",
    IPv6:       &v6,
}

provider := dnssd.NewDiscovery(config)
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(provider).
    WithDiscoveryPort(7946).
    WithKinds(myActor)
```

---

## Package Structure

```
discovery/dnssd/
├── config.go       # Config + validation
├── discovery.go    # Provider implementation
└── README.md
```

---

## Scope and Limitations

| Scope           | Works                        |
|-----------------|------------------------------|
| Any environment | Yes (with DNS resolution)    |
| IPv4 / IPv6     | Configurable via IPv6 option |

Uses the system/default DNS resolver. No external client libraries beyond Go standard library.

---

## Design Goals

| Goal          | Description                               |
|---------------|-------------------------------------------|
| **DNS-based** | Resolves domain to IPs via standard DNS   |
| **Simple**    | No registration; lookup only              |
| **Pluggable** | Implements `discovery.Provider` interface |
