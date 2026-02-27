# Extensions and Dependencies

GoAkt supports two injection mechanisms: **extensions** (system-wide) and **dependencies** (per-actor).

## Extensions

Extensions are system-wide plugins registered at actor system creation. They provide cross-cutting capabilities (event sourcing, metrics, service registry) accessible from any actor.

### The Extension interface

```go
type Extension interface {
    ID() string
}
```

`ID` must be unique, alphanumeric, up to 255 chars. Add domain-specific methods to your concrete type.

### Wiring

Pass extensions via `WithExtensions(ext1, ext2, ...)` when creating the actor system. Actors access via `ctx.Extension(id)` or `ctx.Extensions()` on `Context`, `ReceiveContext`, or `GrainContext`. Type-assert to access your methods.

### Use cases

- Event sourcing engine
- Metrics recorder
- Service registry client
- Distributed tracing

## Dependencies

Dependencies are per-actor resources injected at spawn time. Unlike extensions, they are scoped to a single actor and must be serializable for cluster relocation.

### The Dependency interface

```go
type Dependency interface {
    Serializable  // encoding.BinaryMarshaler + BinaryUnmarshaler
    ID() string
}
```

### Wiring

Pass via `WithDependencies(dep1, dep2, ...)` as a `SpawnOption`. The actor receives them through `ctx.Dependencies()` or `ctx.Dependency(id)` in `PreStart`, `Receive`, and `PostStop`.

### Use cases

- Database client
- External API client
- Configuration provider

Serializability is required because dependencies travel with the actor during relocation. The framework serializes them to the cluster registry and reconstructs them on the target node.
