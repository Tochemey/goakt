# Kubernetes Discovery Provider

The Kubernetes discovery provider allows GoAkt cluster nodes to discover each other when running inside a **Kubernetes cluster**. It uses the Kubernetes API to list pods matching configured labels and derives peer addresses from pod IPs and container ports.

## Architecture

### Discovery Flow

```
    +---------------------+  DiscoverPeers()   +---------------------+
    | discovery.Provider  |  returns []string  |       Olric         |
    | (kubernetes)        |  host:discoveryPort| (cluster engine)    |
    +---------------------+ -----------------> +---------------------+
              |                                         |
              |  List pods by labels                     v
              |  Extract pod IP + port           +---------------+
              v                                  |  Memberlist   |
    +---------------------+                      |  (gossip)     |
    |   Kubernetes API   |                      +---------------+
    |   (in-cluster)      |
    +---------------------+
```

- **Discovery provider** is used by Olric **only for bootstrap**: it periodically calls `DiscoverPeers()` to get peer addresses.
- **Kubernetes** provides pod discovery; the provider lists pods in the configured namespace with matching labels.
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

- `DiscoverPeers()` returns addresses in `podIP:discoveryPort` format.
- Ports are read from the pod's container port definitions (`DiscoveryPortName`, `RemotingPortName`, `PeersPortName`).
- The cluster uses `discovery.Node` with `Host`, `DiscoveryPort`, `PeersPort`, `RemotingPort`.

---

## Implementation

### How It Works

1. **Initialize**: Validate config; no API connection yet.
2. **Register**: Load in-cluster config (`rest.InClusterConfig()`); create Kubernetes client; start background pod watcher.
3. **DiscoverPeers**: List pods in namespace with `PodLabels`; for each pod, get IP and port named `DiscoveryPortName`; return addresses (excluding self).
4. **Deregister**: Stop watcher; release resources.

```
    Pod A                                    Pod B
    -----                                    -----
    In-cluster config                        In-cluster config
    List pods (namespace + labels)            List pods (namespace + labels)
    DiscoverPeers() -> [B]                   DiscoverPeers() -> [A]
    Olric joins B                            Olric joins A
    |                                        |
    +------------------+---------------------+
                       |
           Gossip propagates full membership
```

### Pod Labels

Pods must expose named container ports. The provider matches pods by `PodLabels` (e.g. `app=goakt`) and reads port values from the pod spec.

---

## Configuration

```go
type Config struct {
    Namespace         string            // Required. Kubernetes namespace.
    DiscoveryPortName string            // Required. Container port name for gossip.
    RemotingPortName  string            // Required. Container port name for remoting.
    PeersPortName     string            // Required. Container port name for cluster.
    PodLabels         map[string]string // Required. Labels to match pods.
}
```

### Example

```go
config := &kubernetes.Config{
    Namespace:         "default",
    DiscoveryPortName: "gossip",
    RemotingPortName:  "remoting",
    PeersPortName:     "cluster",
    PodLabels:         map[string]string{"app": "goakt"},
}

provider := kubernetes.NewDiscovery(config)
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(provider).
    WithDiscoveryPort(7946).
    WithKinds(myActor)
```

---

## Package Structure

```
discovery/kubernetes/
├── config.go       # Config + validation
├── discovery.go    # Provider implementation
└── README.md
```

---

## Scope and Limitations

| Scope              | Works                           |
|--------------------|---------------------------------|
| Kubernetes cluster | Yes (in-cluster only)           |
| Outside cluster    | No (requires in-cluster config) |

Runs only inside a Kubernetes cluster. Uses the service account token and in-cluster API server.

---

## Design Goals

| Goal                  | Description                               |
|-----------------------|-------------------------------------------|
| **Kubernetes-native** | Uses Kubernetes API for pod discovery     |
| **Label-based**       | Matches pods by configurable labels       |
| **Pluggable**         | Implements `discovery.Provider` interface |
