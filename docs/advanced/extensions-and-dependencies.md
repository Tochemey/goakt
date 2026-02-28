---
title: Extensions and Dependencies
description: System-wide extensions and per-actor dependency injection.
sidebarTitle: "🔧 Extensions and Dependencies"
---

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
    Serializable
    // ID returns the unique identifier for the extension.
    //
    // The identifier must:
    //   - Be no more than 255 characters long.
    //   - Start with an alphanumeric character [a-zA-Z0-9].
    //   - Contain only alphanumeric characters, hyphens (-), or underscores (_) thereafter.
    //
    // Identifiers that do not meet these constraints are considered invalid.
    ID() string
}

type Serializable interface {
    encoding.BinaryMarshaler
    encoding.BinaryUnmarshaler
}
```

### Wiring

Dependencies can be injected in two ways:

1. **At spawn time** — Pass via `WithDependencies(dep1, dep2, ...)` as a `SpawnOption`. The actor receives them through `ctx.Dependencies()` or `ctx.Dependency(id)` in `PreStart`, `Receive`, and `PostStop`.

2. **Via the actor system** — Call `system.Inject(dep1, dep2, ...)` after the system has started. This registers the dependency types with the actor system's registry, enabling the framework to restore dependencies during cluster topology changes or when creating actors on remote hosts. You typically combine this with `WithDependencies` when spawning: inject first to register the types, then pass them at spawn so the actor receives them.

### Use cases

- Database client
- External API client
- Configuration provider

Serializability is required because dependencies travel with the actor during relocation. The framework serializes them to the cluster registry and reconstructs them on the target node.
