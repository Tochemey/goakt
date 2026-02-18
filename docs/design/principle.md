# Design Principles

GoAkt is purpose-built to offer a robust, efficient, and straightforward actor framework for building distributed systems in Go. The design decisions behind GoAkt reflect a commitment to simplicity, performance, and seamless integration.

## Table of Contents

- 🎯 [Simplicity at Its Core](#simplicity-at-its-core)
- 💡 [Ease of Use](#ease-of-use)
- 📦 [Protocol Buffers as the Message Contract](#protocol-buffers-as-the-message-contract)
- 📚 [Leverage Battle-Tested Libraries](#leverage-battle-tested-libraries)
- ⚡ [High Performance](#high-performance)
- 🔌 [Extensibility Through Interfaces](#extensibility-through-interfaces)
- 📋 [Summary](#summary)

---

## Simplicity at Its Core

GoAkt targets the fundamental components of an actor framework as originally envisioned by the pioneers of the actor model. An actor is a computational entity that, in response to a message:

- Sends messages to other actors
- Creates new actors
- Designates behavior for the next message it receives

GoAkt implements exactly this -- nothing more. By focusing solely on the essentials, the framework avoids unnecessary abstractions and hidden complexity. The `Actor` interface captures this philosophy in three methods:

```go
type Actor interface {
    PreStart(ctx *Context) error
    Receive(ctx *ReceiveContext)
    PostStop(ctx *Context) error
}
```

There are no annotations, no code generation for actors, no configuration files, and no magic. You implement an interface, spawn it, and send messages.

## Ease of Use

GoAkt is engineered with developer ergonomics in mind. Its API surface is intentionally small so that the learning curve stays flat:

**Minimal setup** -- A working actor system requires only a few lines:

```go
system, _ := actor.NewActorSystem("my-system")
_ = system.Start(ctx)
pid, _ := system.Spawn(ctx, "greeter", new(Greeter))
actor.Tell(ctx, pid, &SayHello{Name: "World"})
```

**Two messaging patterns** -- `Tell` for fire-and-forget, `Ask` for request-response. These cover the vast majority of use cases without forcing developers to learn a large API.

**Progressive complexity** -- Start with a single-node system, add remoting when you need cross-node communication, enable clustering when you need discovery and fault tolerance. Each capability is opt-in through composable options:

```go
// Single node
system, _ := actor.NewActorSystem("orders")

// Add remoting
system, _ := actor.NewActorSystem("orders",
    actor.WithRemoting("0.0.0.0", 50051),
)

// Add clustering
system, _ := actor.NewActorSystem("orders",
    actor.WithRemoting("0.0.0.0", 50051),
    actor.WithClustering(provider, 3),
)
```

## Protocol Buffers as the Message Contract

Every message in GoAkt -- whether local, remote, or across a cluster -- must be a `proto.Message`. This is a deliberate, opinionated design choice.

### Why Protocol Buffers?

- **Type safety** — Concrete protobuf types; the compiler catches mismatches at build time. No untyped `interface{}` or `any` payloads.
- **Explicit serialization** — No reflection or runtime codecs; protobuf gives a deterministic binary format. Sender and receiver share the same `.proto`, so the wire format is agreed.
- **Schema evolution** — Field numbering gives forward and backward compatibility. Add fields without breaking older actors; important for rolling deployments.
- **Performance** — Compact, fast marshal/unmarshal. Better size and speed than JSON for high-throughput systems.
- **Cross-language** — `.proto` files generate code in many languages; messages are a language-neutral contract and portable.

### In Practice

Define messages in `.proto` files:

```protobuf
syntax = "proto3";
package myapp;
option go_package = "github.com/myorg/myapp/messages";

message CreateOrder {
  string order_id = 1;
  string customer = 2;
  repeated Item items = 3;
}

message OrderCreated {
  string order_id = 1;
  int64  created_at = 2;
}
```

Then use them directly in actors:

```go
func (a *OrderActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *messages.CreateOrder:
        order := a.createOrder(msg)
        ctx.Response(&messages.OrderCreated{
            OrderId:   order.ID,
            CreatedAt: time.Now().Unix(),
        })
    }
}
```

The same message types work unchanged for local sends, remote calls, cluster-wide pub/sub, and grain interactions.

## Leverage Battle-Tested Libraries

GoAkt builds on existing, proven libraries within the Go ecosystem rather than reimplementing common infrastructure:

| Concern            | Library                                                                                                                          |
|--------------------|----------------------------------------------------------------------------------------------------------------------------------|
| Logging            | [Zap](https://github.com/uber-go/zap)                                                                                            |
| Metrics            | [OpenTelemetry Go SDK](https://opentelemetry.io/docs/languages/go/)                                                              |
| Discovery          | [NATS](https://nats.io/), [Consul](https://www.consul.io/), [etcd](https://etcd.io/), [Kubernetes](https://kubernetes.io/), mDNS |
| Cluster membership | [Olric](https://github.com/tochemey/olric)                                                                                       |
| Serialization      | [Protocol Buffers](https://protobuf.dev/)                                                                                        |

This approach accelerates development, reduces maintenance burden, and inherits production-hardened reliability. When a dependency releases a performance improvement or security fix, GoAkt benefits immediately.

## High Performance

Performance is a paramount goal. GoAkt is optimized for speed and low latency:

- **Lock-free where possible** -- Atomic operations and concurrent data structures minimize contention
- **Efficient mailboxes** -- Bounded and unbounded queues backed by ring buffers
- **Binary serialization** -- Protobuf encoding avoids the overhead of text formats
- **Connection pooling** -- Reusable TCP connections for remoting
- **Callback-based metrics** -- OpenTelemetry observable counters avoid per-message instrumentation overhead

The framework is designed to handle high-throughput scenarios in both local and distributed deployments without compromising responsiveness.

## Extensibility Through Interfaces

GoAkt exposes well-defined interfaces at every integration point. Rather than shipping opinionated implementations that cover every possible use case, the framework provides contracts and lets you plug in what you need.

### Key Extension Points

| Interface                  | Purpose                                                 |
|----------------------------|---------------------------------------------------------|
| `actor.Actor`              | Define actor behavior (three methods)                   |
| `actor.Mailbox`            | Custom message queuing strategies                       |
| `log.Logger`               | Plug in any logging backend                             |
| `discovery.Provider`       | Add custom cluster discovery mechanisms                 |
| `remote.ContextPropagator` | Propagate custom metadata across nodes                  |
| `passivation.Strategy`     | Define custom idle-actor passivation rules              |
| `eventstream.Subscriber`   | Consume internal system events                          |
| `extension.Extension`      | Inject cross-cutting capabilities into the actor system |
| `extension.Dependency`     | Attach runtime dependencies to actors                   |

Each interface is small (typically one to three methods), making implementations straightforward and testable.

### Focused API Surface

GoAkt keeps the core API lean. Features are composed, not inherited:

```go
system, _ := actor.NewActorSystem("my-system",
    actor.WithLogger(myLogger),           // Custom logging
    actor.WithMetrics(),                   // OpenTelemetry metrics
    actor.WithRemoting("0.0.0.0", 50051), // Enable remoting
    actor.WithExtensions(myExtension),     // Custom extension
)
```

You pay for only the features you enable. An actor system that does not need clustering carries no clustering overhead. One that does not need metrics registers no instruments. This keeps the binary small and the runtime focused.

## Summary

| Principle                     | How GoAkt delivers                                             |
|-------------------------------|----------------------------------------------------------------|
| **Simplicity**                | Three-method `Actor` interface; no magic                       |
| **Ease of use**               | Small API, progressive complexity, composable options          |
| **Clear contracts**           | Protocol Buffers for all messages -- typed, versioned, fast    |
| **Battle-tested foundations** | Zap, OpenTelemetry, NATS, Protocol Buffers                     |
| **Performance**               | Lock-free primitives, binary serialization, connection pooling |
| **Extensibility**             | Small interfaces at every integration point                    |
