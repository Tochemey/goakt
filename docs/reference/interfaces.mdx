---
title: Interfaces Reference
description: Actor, Grain, and extension interface definitions.
sidebarTitle: "📐 Interfaces Reference"
---

A quick reference for the key interfaces implemented by users and collaborators. See the linked pages for detailed
explanations.

## Actor

```go
type Actor interface {
    PreStart(ctx *Context) error
    Receive(ctx *ReceiveContext)
    PostStop(ctx *Context) error
}
```

Required for all actors. See [Actor Model](../actor/actor-model.md#the-actor-interface).

## Grain

```go
type Grain interface {
    OnActivate(ctx context.Context, props *GrainProps) error
    OnReceive(ctx *GrainContext)
    OnDeactivate(ctx context.Context, props *GrainProps) error
}
```

Required for virtual actors. See [Grains](../grains/overview.md#the-grain-interface).

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

Optional; for custom mailbox implementations. See [Extending GoAkt](../contributing/extending.md#mailbox).

## Serializer

```go
type Serializer interface {
    Serialize(message any) ([]byte, error)
    Deserialize(data []byte) (any, error)
}
```

For custom message serialization over the wire.
See [Serialization](../advanced/serialization.md#the-serializer-interface).

## Extension

```go
type Extension interface {
    ID() string
}
```

System-wide plugin. See [Extending GoAkt](../contributing/extending.md#extension-system-wide).

## Dependency

```go
type Dependency interface {
    Serializable  // BinaryMarshaler + BinaryUnmarshaler
    ID() string
}
```

Per-actor injected resource. Must be serializable for relocation.
See [Extending GoAkt](../contributing/extending.md#dependency-per-actor).

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

For cluster peer discovery. See [Extending GoAkt](../contributing/extending.md#discovery-provider).

For custom passivation behavior. See [Passivation](../actor/passivation.md#the-strategy-interface)
and [Extending GoAkt](../contributing/extending.md#passivation-strategy).

## Path

```go
type Path interface {
    Host() string
    HostPort() string
    Port() int
    Name() string
    Parent() Path
    String() string
    System() string
    Equals(other Path) bool
}
```

Returned by `pid.Path()`. Location-transparent actor identity.
See [Location Transparency](../actor/location-transparency.md#the-path-interface).
