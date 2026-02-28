# NATS Discovery Provider

The NATS discovery provider allows GoAkt cluster nodes to discover each other and form a cluster using a **NATS server**
as the discovery backend. Nodes register with NATS, subscribe to a shared subject, and discover peers via request-reply
messaging.

## Architecture

### Discovery Flow

```
    +---------------------+  DiscoverPeers()   +---------------------+
    | discovery.Provider  |  returns []string  |       Olric         |
    | (nats)              |  host:discoveryPort | (cluster engine)    |
    +---------------------+ -----------------> +---------------------+
              |                                         |
              |  Register / Subscribe                    v
              |  PublishRequest / Reply          +---------------+
              v                                  |  Memberlist   |
    +---------------------+                      |  (gossip)     |
    |   NATS Server       |                      +---------------+
    |   (message broker)  |
    +---------------------+
```

- **Discovery provider** is used by Olric **only for bootstrap**: it periodically calls `DiscoverPeers()` to get peer
  addresses.
- **NATS** acts as the message broker: nodes subscribe to a subject, publish registration/deregistration, and use
  request-reply for peer discovery.
- **Memberlist** (HashiCorp) handles ongoing membership via SWIM gossip. Once a node joins (via any discovered peer), it
  learns about all other members through gossip.

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

1. **Initialize**: Connect to the NATS server with retry and exponential backoff.
2. **Register**: Subscribe to the configured subject; handle REGISTER, DEREGISTER, and REQUEST messages. On REQUEST,
   reply with this node's address.
3. **DiscoverPeers**: Publish a REQUEST to the subject with a reply inbox; collect RESPONSE messages from peers within
   the timeout.
4. **Deregister**: Unsubscribe and publish a DEREGISTER message to notify peers.

```
    Node A                                    Node B
    ------                                    ------
    Connect to NATS                           Connect to NATS
    Subscribe to subject                       Subscribe to subject
    PublishRequest (discovery)                 PublishRequest (discovery)
    <-- recv B's RESPONSE                      <-- recv A's RESPONSE
    DiscoverPeers() -> [B]                     DiscoverPeers() -> [A]
    Olric joins B                              Olric joins A
    |                                          |
    +------------------+-----------------------+
                       |
           Gossip propagates full membership
```

### Protocol

- **Message types**: REGISTER, DEREGISTER, REQUEST, RESPONSE (protobuf `NatsMessage`).
- **Subject**: Configurable via `NatsSubject`; all nodes in the cluster use the same subject.
- **Request-reply**: `DiscoverPeers()` uses `PublishRequest` with a reply inbox; peers respond with their address.

---

## Configuration

```go
type Config struct {
    NatsServer       string        // Required. NATS server URL (e.g. nats://host:port).
    NatsSubject      string        // Required. Subject for discovery messages.
    Host             string        // Required. This node's host.
    DiscoveryPort    int           // Required. This node's discovery port.
    Timeout          time.Duration // Optional. Discovery timeout. Default 1s.
    MaxJoinAttempts  int           // Optional. Max connection attempts. Default 5.
    ReconnectWait    time.Duration // Optional. Reconnect backoff. Default 2s.
}
```

### Example

```go
config := &nats.Config{
    NatsServer:      "nats://host:4222",
    NatsSubject:     "goakt.discovery",
    Host:            "192.168.1.10",
    DiscoveryPort:   7946,
    Timeout:        2 * time.Second,
    MaxJoinAttempts: 5,
}

provider := nats.NewDiscovery(config, nats.WithLogger(logger))
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(provider).
    WithDiscoveryPort(7946).
    WithKinds(myActor)
```

---

## Package Structure

```
discovery/nats/
├── config.go        # Config + validation
├── discovery.go      # Provider implementation
├── option.go         # Options (e.g. WithLogger)
├── config_test.go
├── discovery_test.go
├── option_test.go
└── README.md
```

---

## Scope and Limitations

| Scope             | Works                         |
|-------------------|-------------------------------|
| Same subnet (LAN) | Yes                           |
| Cross-subnet      | Yes (NATS routes messages)    |
| Cloud             | Yes (NATS is cloud-friendly)  |
| Loopback (tests)  | Yes (run NATS server locally) |

Requires a running NATS server. For zero-infrastructure discovery, use the self-managed provider.

---

## Design Goals

| Goal                     | Description                                  |
|--------------------------|----------------------------------------------|
| **Centralized registry** | NATS server as discovery backend             |
| **Request-reply**        | Uses NATS request-reply for peer discovery   |
| **Resilient**            | Retry with backoff; reconnects on disconnect |
| **Pluggable**            | Implements `discovery.Provider` interface    |
