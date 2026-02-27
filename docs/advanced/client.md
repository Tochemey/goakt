# Client

The `client` package provides a standalone client for interacting with a GoAkt cluster from outside the actor system. Use it when your application (CLI, API server, batch job, etc.) needs to send messages to actors or grains, spawn actors, or query cluster state without running an actor system itself.

## Requirements

- **Cluster with remoting** — Target nodes must run GoAkt with clustering and remoting enabled.
- **At least one node** — `client.New` requires a non-empty slice of nodes; it fails with "nodes are required" otherwise.
- **Node addresses** — Each node is specified as `host:port` (remoting host and port, e.g. `127.0.0.1:8080`). Addresses are validated as TCP addresses.
- **Serialization** — The client must use the same serializers and compression as the cluster. Pass `remote.Config` via `WithRemoteConfig` when creating nodes. Without it, the node uses default remoting settings, which may not match the cluster.

## Creating a client

```go
import (
    "context"
    "github.com/tochemey/goakt/v4/client"
    "github.com/tochemey/goakt/v4/remote"
)

ctx := context.Background()

nodes := []*client.Node{
    client.NewNode("127.0.0.1:8080", client.WithRemoteConfig(remote.NewConfig("127.0.0.1", 0,
        remote.WithCompression(remote.ZstdCompression),
        remote.WithSerializers(remote.NewProtoSerializer()),
    ))),
    client.NewNode("127.0.0.1:8081", client.WithRemoteConfig(remote.NewConfig("127.0.0.1", 0,
        remote.WithCompression(remote.ZstdCompression),
        remote.WithSerializers(remote.NewProtoSerializer()),
    ))),
}

cl, err := client.New(ctx, nodes)
if err != nil {
    return err
}
defer cl.Close()
```

Each `Node` represents a cluster node. Call `Close()` when done to release connection pools. The client uses a load balancer to distribute requests across nodes. The `remote.Config` must match the cluster (compression, serializers).

### Node options

| Option                     | Purpose                                                                                       |
|----------------------------|-----------------------------------------------------------------------------------------------|
| `WithRemoteConfig(config)` | Set remoting config (compression, serializers, timeouts). Required for correct serialization. |
| `WithWeight(weight)`       | Set initial node weight for load balancing (used by `LeastLoadStrategy`).                     |

## Messaging

| Method                                          | Purpose                                                                                                                                                            |
|-------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Tell(ctx, actorName, message)`                 | Fire-and-forget to an actor. `message` must be `proto.Message`. Returns `ErrActorNotFound` if the actor is not found.                                              |
| `Ask(ctx, actorName, message, timeout)`         | Request-response to an actor. Blocks until reply or timeout. `message` accepts `any` (must be serializable). Returns `ErrActorNotFound` if the actor is not found. |
| `TellGrain(ctx, grainRequest, message)`         | Fire-and-forget to a grain. `message` must be `proto.Message`. The grain kind must be registered on the cluster via `RegisterGrainKind`.                           |
| `AskGrain(ctx, grainRequest, message, timeout)` | Request-response to a grain. `message` accepts `any`. The grain kind must be registered on the cluster.                                                            |

The client performs a cluster lookup by actor name (or grain identity) and routes the message to the correct node. For grains, use `remote.GrainRequest{Name: "id", Kind: "MyGrain"}`. `Name` and `Kind` are required and are trimmed of whitespace.

## Spawning and lifecycle

| Method                                       | Purpose                                                                                                  |
|----------------------------------------------|----------------------------------------------------------------------------------------------------------|
| `Spawn(ctx, spawnRequest)`                   | Spawn an actor on a node (uses default balancer). Returns error only; does not return the actor address. |
| `SpawnBalanced(ctx, spawnRequest, strategy)` | Spawn with a specific balancing strategy for that call.                                                  |
| `ReSpawn(ctx, actorName)`                    | Restart an actor (stop and spawn again). If the actor is not found, returns `nil` (no error).            |
| `Stop(ctx, actorName)`                       | Stop an actor. If the actor is not found, returns `nil` (no error).                                      |
| `Reinstate(ctx, actorName)`                  | Reinstate a suspended actor. If the actor is not found, returns `nil` (no error).                        |

The actor kind must be registered on the cluster via `WithKinds` when creating the actor system. `SpawnRequest` requires `Name` and `Kind`; optional fields include `Singleton`, `Relocatable`, `PassivationStrategy`, `Supervisor`, `Dependencies`, `EnableStashing`, `Role`, and `Reentrancy`.

## Discovery and inspection

| Method                   | Purpose                                                                                                                                                       |
|--------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Exists(ctx, actorName)` | Check if an actor is registered in the cluster. Returns `(false, nil)` when the actor does not exist; returns an error only on lookup failure (e.g. network). |
| `Kinds(ctx)`             | List registered actor kinds. Queries one node (via balancer) and returns kinds from the cluster.                                                              |

## Options

| Option                           | Purpose                                                                                                                      |
|----------------------------------|------------------------------------------------------------------------------------------------------------------------------|
| `WithBalancerStrategy(strategy)` | Set load-balancing strategy (default: round-robin).                                                                          |
| `WithRefresh(interval)`          | Periodically refresh node weights by querying each node's load. Used by `LeastLoadStrategy` to prefer the least-loaded node. |

## Balancer strategies

| Strategy             | Behavior                                                                                                                  |
|----------------------|---------------------------------------------------------------------------------------------------------------------------|
| `RoundRobinStrategy` | Cycle through nodes in order.                                                                                             |
| `RandomStrategy`     | Pick a node at random.                                                                                                    |
| `LeastLoadStrategy`  | Prefer the node with the lowest load. Requires `WithRefresh` to update weights; otherwise weights stay at initial values. |

## Example

```go
// Tell (fire-and-forget)
err := cl.Tell(ctx, "my-actor", &mypb.MyMessage{Id: "123"})

// Ask (request-response)
reply, err := cl.Ask(ctx, "my-actor", &mypb.GetRequest{}, 5*time.Second)

// Ask a grain
grainReq := &remote.GrainRequest{Name: "user-42", Kind: "UserGrain"}
reply, err := cl.AskGrain(ctx, grainReq, &mypb.GetUser{}, 5*time.Second)

// Spawn an actor
err := cl.Spawn(ctx, &remote.SpawnRequest{
    Name: "worker-1",
    Kind: "WorkerActor",
})
```

## Thread safety

The client is safe for concurrent use by multiple goroutines.

## Errors

| Error                  | When                                                                                      |
|------------------------|-------------------------------------------------------------------------------------------|
| `ErrActorNotFound`     | `Tell` or `Ask` when the actor is not found in the cluster.                               |
| `"nodes are required"` | `New` when the nodes slice is nil or empty.                                               |
| Proto errors           | Various errors from the remote layer (e.g. `CODE_UNAVAILABLE`, `CODE_DEADLINE_EXCEEDED`). |
| Network errors         | Connection failures, timeouts, etc.                                                       |

## See also

- [Remoting](remoting.md) — Cluster remoting configuration
- [Serialization](serialization.md) — Message serialization (client must match cluster)
- [Code Map](../architecture/code-map.md) — `client/` package overview
