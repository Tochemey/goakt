# Service Discovery

In clustered mode, nodes must discover each other. GoAkt uses a pluggable **discovery provider** that implements the `discovery.Provider` interface. Pass the provider to `ClusterConfig.WithDiscovery()` when creating the cluster.

## Provider interface

```go
type Provider interface {
    ID() string
    Initialize() error
    Register() error
    Deregister() error
    DiscoverPeers() ([]string, error)
    Close() error
}
```

| Method            | Purpose                                        |
|-------------------|------------------------------------------------|
| **ID**            | Provider name.                                 |
| **Initialize**    | One-time setup (e.g., connect to backend).     |
| **Register**      | Register this node with the discovery backend. |
| **Deregister**    | Remove this node on shutdown.                  |
| **DiscoverPeers** | Return peer addresses as `host:port` strings.  |
| **Close**         | Release resources.                             |

## Built-in providers

| Provider       | Package                | Use case                        |
|----------------|------------------------|---------------------------------|
| **Consul**     | `discovery/consul`     | HashiCorp Consul.               |
| **etcd**       | `discovery/etcd`       | etcd.                           |
| **Kubernetes** | `discovery/kubernetes` | K8s API for pod discovery.      |
| **NATS**       | `discovery/nats`       | NATS for peer discovery.        |
| **mDNS**       | `discovery/mdns`       | Multicast DNS (local networks). |
| **DNS-SD**     | `discovery/dnssd`      | DNS-based service discovery.    |
| **Static**     | `discovery/static`     | Fixed list of peer addresses.   |

Each provider has its own config type. See the package for constructors and options.

## Configuration

```go
provider, err := consul.New(config)
// or: etcd.New(config), kubernetes.New(config), etc.
clusterConfig := actor.NewClusterConfig().
    WithDiscovery(provider).
    WithKinds(actor1, actor2)

system, _ := actor.NewActorSystem("app",
    actor.WithRemote(remoteConfig),
    actor.WithCluster(clusterConfig),
)
```

The provider is used by the cluster to find peers. Remoting must be enabled (`WithRemote`) for cross-node communication.

## Custom provider

Implement `discovery.Provider` and pass it to `WithDiscovery`. See [Extending GoAkt](../contributing/extending.md#discovery-provider) for the interface and wiring.
