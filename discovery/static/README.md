# Static Discovery Provider

The static discovery provider returns a **fixed list of peer addresses** configured at startup. No external service or dynamic discovery is used. Suitable for small, stable clusters where peer addresses are known in advance.

## Architecture

### Discovery Flow

```
    +---------------------+  DiscoverPeers()   +---------------------+
    | discovery.Provider  |  returns []string  |       Olric         |
    | (static)            |  host:discoveryPort| (cluster engine)    |
    +---------------------+ -----------------> +---------------------+
              |                                         |
              |  No Register/Deregister                   v
              |  Returns config.Hosts             +---------------+
              v                                  |  Memberlist   |
    +---------------------+                      |  (gossip)     |
    |   Config (Hosts)    |                      +---------------+
    |   (in-memory)       |
    +---------------------+
```

- **Discovery provider** is used by Olric **only for bootstrap**: it periodically calls `DiscoverPeers()` to get peer addresses.
- **Static** simply returns the configured `Hosts` list; no registration or external system.
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

- **Register** / **Deregister** / **Close**: No-op.
- **DiscoverPeers**: Returns `config.Hosts` as-is.

---

## Implementation

### How It Works

1. **Initialize**: Validate config (non-empty Hosts, valid addresses).
2. **Register**: No-op.
3. **DiscoverPeers**: Return `config.Hosts`.
4. **Deregister** / **Close**: No-op.

```
    Node A                                    Node B
    ------                                    ------
    Hosts: [B, C]                             Hosts: [A, C]
    DiscoverPeers() -> [B, C]                 DiscoverPeers() -> [A, C]
    Olric joins B, C                          Olric joins A, C
    |                                         |
    +------------------+-----------------------+
                       |
           Gossip propagates full membership
```

Each node typically configures the *other* peers (excluding self). The provider does not filter self; Olric/Memberlist handles deduplication.

---

## Configuration

```go
type Config struct {
    Hosts []string  // Required. Peer addresses in host:discoveryPort format.
}
```

### Example

```go
config := &static.Config{
    Hosts: []string{
        "192.168.1.11:7946",
        "192.168.1.12:7946",
    },
}

provider := static.NewDiscovery(config)
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(provider).
    WithDiscoveryPort(7946).
    WithKinds(myActor)
```

---

## Package Structure

```
discovery/static/
├── config.go       # Config + validation
├── discovery.go    # Provider implementation
└── README.md
```

---

## Scope and Limitations

| Scope           | Works                 |
|-----------------|-----------------------|
| Any environment | Yes                   |
| Dynamic scaling | No (config is static) |

No external dependencies. Peer list must be updated by restarting with new config.

---

## Design Goals

| Goal          | Description                               |
|---------------|-------------------------------------------|
| **Zero deps** | No external services                      |
| **Simple**    | Fixed list, no registration               |
| **Pluggable** | Implements `discovery.Provider` interface |
