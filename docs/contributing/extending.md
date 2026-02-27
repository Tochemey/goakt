# Extending GoAkt

This page lists the interfaces to implement when extending the framework. Wire implementations via the indicated
options.

## Discovery Provider

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

| Method            | Purpose                                       |
|-------------------|-----------------------------------------------|
| **ID**            | Provider name                                 |
| **Initialize**    | One-time setup                                |
| **Register**      | Register this node with the discovery backend |
| **Deregister**    | Remove this node on shutdown                  |
| **DiscoverPeers** | Return peer addresses (`host:port` strings)   |
| **Close**         | Release resources                             |

Create a sub-package under `discovery/`. Pass the provider to `ClusterConfig` when creating the actor system.

## Mailbox

```go
type Mailbox interface {
    Enqueue(msg *ReceiveContext) error
    Dequeue() *ReceiveContext
    IsEmpty() bool
    Len() int64
    Dispose()
}
```

| Method                | Purpose                                                            |
|-----------------------|--------------------------------------------------------------------|
| **Enqueue**           | Push a message. Return error when full (bounded mailbox).          |
| **Dequeue**           | Fetch the next message. Called only from the processing goroutine. |
| **IsEmpty** / **Len** | Query mailbox state                                                |
| **Dispose**           | Free resources, unblock any waiters                                |

Create a file in `actor/`. Pass via `WithMailbox(mailbox)` as a SpawnOption. Reference `unbounded_mailbox.go` and
`unbounded_priority_mailbox.go`.

## Extension (system-wide)

```go
type Extension interface {
    ID() string
}
```

`ID` must be unique, alphanumeric, up to 255 chars. Add domain-specific methods to your concrete type. Pass via
`WithExtensions(ext)` when creating the actor system. Actors access via `ctx.Extension(id)` and type assertion. See [Extensions and Dependencies](../advanced/extensions-and-dependencies.md) for usage.

## Dependency (per-actor)

```go
type Dependency interface {
    Serializable  // encoding.BinaryMarshaler + BinaryUnmarshaler
    ID() string
}
```

Dependencies must be serializable for cluster relocation. Pass via `WithDependencies(dep)` as a SpawnOption. Actors
access via `ctx.Dependencies()` in PreStart, Receive, PostStop. See [Extensions and Dependencies](../advanced/extensions-and-dependencies.md) for usage.
