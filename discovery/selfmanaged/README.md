# Self-Managed Discovery Provider

The self-managed discovery provider allows GoAkt cluster nodes to discover each other and form a cluster **without any third-party infrastructure** (no Consul, etcd, NATS, Kubernetes API, or mDNS) and **without configuring any peer IPs**. Nodes discover each other automatically via UDP broadcast on the LAN.

## Architecture

### Discovery Flow

```
    +---------------------+  DiscoverPeers()   +---------------------+
    | discovery.Provider  |  returns []string  |       Olric         |
    | (selfmanaged)       |  host:discoveryPort| (cluster engine)    |
    +---------------------+ -----------------> +---------------------+
                                                       |
                                                       v
                                               +---------------+
                                               |  Memberlist   |
                                               |  (gossip)     |
                                               +---------------+
```

- **Discovery provider** is used by Olric **only for bootstrap**: it periodically calls `DiscoverPeers()` to get peer addresses.
- **Memberlist** (HashiCorp) handles ongoing membership via SWIM gossip. Once a node joins (via any discovered peer), it learns about all other members through gossip.
- **Key insight**: A new node needs at least one reachable peer address to bootstrap. The self-managed provider supplies this via UDP broadcast discovery—no manual peer config needed.

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

1. **Announce**: Each node periodically broadcasts its presence (`host:discoveryPort`) on a well-known UDP port.
2. **Listen**: Each node listens for broadcasts from other nodes on the same subnet.
3. **DiscoverPeers**: Returns peers discovered via broadcast (excluding self).
4. **Register/Deregister**: No-op (no external registry).

```
    Node A                    |    Node B
    --------------------------+--------------------------
    broadcast "A:7946"        |    broadcast "B:7946"
    listen for broadcasts     |    listen for broadcasts
    <-- recv B's broadcast    |    <-- recv A's broadcast
    DiscoverPeers() -> [B]    |    DiscoverPeers() -> [A]
    Olric joins B             |    Olric joins A
    --------------------------+--------------------------
                              |
                  Gossip propagates full membership
```

- **First node**: Broadcasts, listens, gets no peers. Runs as single-node cluster.
- **Second node**: Broadcasts, listens. A receives B, B receives A. Each discovers the other. They join.
- **Third node**: Same pattern. Discovers A and B. Joins. Gossip propagates.

### Protocol

- **Packet format**: `goakt-v1|<clusterName>|<host:discoveryPort>`
- **Cluster name**: Ensures different clusters on the same LAN don't mix.
- **Peer cache**: Evicts peers not seen within 3× broadcast interval.

### Platform-Specific Behavior

Loopback discovery (e.g. for tests) behaves differently per OS:

| Platform    | Loopback Discovery                                                       |
|-------------|--------------------------------------------------------------------------|
| **Linux**   | Bind to `127.255.255.255`, send to `127.255.255.255` with `SO_BROADCAST` |
| **macOS**   | Multicast `224.0.0.1` (UDP broadcast to `127.255.255.255` unreliable)    |
| **Windows** | Bind to `127.0.0.1`, send to `127.255.255.255` with `SO_BROADCAST`       |

---

## Configuration

```go
type Config struct {
    ClusterName       string        // Required. Identifies the cluster.
    SelfAddress       string        // Required. Our address (host:discoveryPort).
    BroadcastPort     int           // Optional. Default 7947.
    BroadcastInterval time.Duration // Optional. Default 5s.
    BroadcastAddress  net.IP        // Optional. Default 255.255.255.255. Use 127.0.0.1 for loopback tests.
}
```

**No peer IPs or seeds.** Config contains only operational parameters.

### Example

```go
config := &selfmanaged.Config{
    ClusterName:       "my-app",
    SelfAddress:       "192.168.1.10:7946",
    BroadcastPort:     7947,
    BroadcastInterval: 5 * time.Second,
    BroadcastAddress:  net.IPv4(255, 255, 255, 255),  // LAN; use 127.0.0.1 for loopback tests
}

provider := selfmanaged.NewDiscovery(config)
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(provider).
    WithDiscoveryPort(7946).
    WithKinds(myActor)
```

---

## Package Structure

```
discovery/selfmanaged/
├── doc.go                      # Package documentation
├── config.go                   # Config + validation
├── discovery.go                # Provider implementation
├── broadcast.go                # UDP broadcast send/listen logic
├── listen_udp_reuseport.go     # darwin, linux: SO_REUSEPORT
├── listen_udp_windows.go       # windows: SO_REUSEADDR
├── listen_udp_default.go       # other platforms
├── listen_udp_multicast.go     # darwin: multicast for loopback
├── listen_udp_multicast_default.go
├── sockopt_unix.go             # SO_BROADCAST (Unix)
├── sockopt_windows.go          # SO_BROADCAST (Windows)
├── broadcast_test.go
├── config_test.go
├── discovery_test.go
└── README.md
```

---

## Scope and Limitations

| Scope              | Works                                                |
|--------------------|------------------------------------------------------|
| Same subnet (LAN)  | Yes                                                  |
| Cross-subnet       | No (broadcast doesn't route)                         |
| Cloud (restricted) | Some clouds restrict broadcast; may not work         |
| Loopback (tests)   | Linux, Windows: yes; macOS: may skip on some systems |

For environments where broadcast doesn't work (e.g. cross-subnet, restricted cloud), use Consul, etcd, NATS, or static providers instead.

---

## Design Goals

| Goal                    | Description                                                    |
|-------------------------|----------------------------------------------------------------|
| **Zero peer config**    | No peer IPs, no seeds—nodes discover each other automatically. |
| **Zero external deps**  | No Consul, etcd, NATS, K8s API, or mDNS.                       |
| **Self-forming**        | Nodes announce and listen; cluster forms automatically.        |
| **First-node friendly** | First node runs alone; others join as they start.              |
| **Pluggable**           | Implements `discovery.Provider` interface.                     |
