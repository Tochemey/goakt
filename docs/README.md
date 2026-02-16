# Welcome to GoAkt

[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/goakt/build.yml?branch=main)](https://github.com/Tochemey/goakt/actions/workflows/build.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tochemey/goakt.svg)](https://pkg.go.dev/github.com/tochemey/goakt)
[![Go Report Card](https://goreportcard.com/badge/github.com/tochemey/goakt)](https://goreportcard.com/report/github.com/tochemey/goakt)
[![codecov](https://codecov.io/gh/Tochemey/goakt/graph/badge.svg?token=J0p9MzwSRH)](https://codecov.io/gh/Tochemey/goakt)

GoAkt is a distributed [Go](https://go.dev/) actor framework designed to build reactive and distributed systems using **protocol buffers** as actor messages.

## What is GoAkt?

GoAkt provides a robust foundation for building concurrent, distributed, and fault-tolerant applications using the actor model. It's highly scalable and available when running in cluster mode, offering all the features necessary to build production-ready distributed systems without sacrificing performance or reliability.

With GoAkt, you can instantly create fast, scalable, distributed systems across a cluster of computers.

## Why the Actor Model?

The actor model is a conceptual model for dealing with concurrent computation. An actor is a computational entity that, in response to a message it receives, can concurrently:

- Send messages to other actors
- Create new actors
- Designate behavior for the next message it receives

If you're not familiar with the actor model, [this blog post](https://www.brianstorti.com/the-actor-model/) by Brian Storti provides an excellent introduction.

## Key Features

### Core Actor Capabilities

- **Actor Model**: Build concurrent and distributed systems using typed protobuf messages
- **Messaging**: Tell/Ask APIs for fire-and-forget or request/response flows
- **Reentrancy**: Async request messaging with configurable modes and per-call overrides
- **Supervision**: One-for-one/one-for-all strategies with fault tolerance
- **Behaviors**: Dynamic behavior changes for state machines and workflows
- **Stashing**: Buffer messages for later processing
- **Passivation**: Automatically stop idle actors and reclaim resources
- **Dependency Injection**: Attach runtime dependencies at spawn time

### Distributed Systems

- **Remoting**: Seamless actor communication across nodes over TCP
- **Clustering**: Multiple discovery backends (Consul, etcd, Kubernetes, NATS, mDNS, static)
- **Location Transparency**: Interact with actors without knowing their physical location
- **Relocation**: Automatic actor relocation on node failure
- **Cluster Singletons**: Run a single instance across the cluster
- **Multi-datacenter**: DC-transparent messaging and cross-DC communication

### Virtual Actors

- **Grains**: Virtual actor capabilities inspired by Orleans
- **Automatic Activation**: Actors created on-demand
- **Location Transparency**: Access grains without manual placement

### Advanced Features

- **Routers**: Round-robin, random, and fan-out routing strategies
- **Scheduling**: Built-in timers and delayed messaging
- **Custom Mailboxes**: Bounded/unbounded, priority-based queues
- **Context Propagation**: Pluggable context propagation for request metadata
- **Observability**: OpenTelemetry metrics, event streams, and dead letters
- **Extensions**: Pluggable APIs for cross-cutting capabilities

## Getting Started

### Installation

```bash
go get github.com/tochemey/goakt/v3
```

### Quick Example

```go
package main

import (
    "context"
    "time"

    "github.com/tochemey/goakt/v3/actors"
    "github.com/tochemey/goakt/v3/goaktpb"
)

// Define your actor
type MyActor struct{}

func (a *MyActor) PreStart(ctx context.Context) error {
    return nil
}

func (a *MyActor) Receive(ctx actors.ReceiveContext) {
    switch ctx.Message().(type) {
    case *goaktpb.PostStart:
        // Actor started
    default:
        // Handle other messages
    }
}

func (a *MyActor) PostStop(ctx context.Context) error {
    return nil
}

func main() {
    ctx := context.Background()

    // Create actor system
    actorSystem, _ := actors.NewActorSystem("MySystem")

    // Start actor system
    _ = actorSystem.Start(ctx)

    // Spawn an actor
    actor, _ := actorSystem.Spawn(ctx, "my-actor", &MyActor{})

    // Send a message
    _ = actors.Tell(ctx, actor, new(goaktpb.PostStart))

    time.Sleep(time.Second)

    // Stop actor system
    _ = actorSystem.Stop(ctx)
}
```

## Documentation Structure

This documentation is organized into the following sections:

- **Actors**: Core actor concepts, messaging, behaviors, and lifecycle
- **Actor System**: System configuration, initialization, and management
- **Remoting**: Node-to-node communication
- **Clustering**: Discovery, membership, and distributed coordination
- **Grains**: Virtual actor patterns and usage
- **Events Stream**: Event streaming and observability
- **PubSub**: Publish-subscribe messaging patterns
- **Testing**: Testing actors and distributed systems

## Who Uses GoAkt?

GoAkt is used in production by:

- **[Baki Money](https://www.baki.money/)**: AI-powered expense tracking platform
- **[Event Processor](https://www.v-app.io/iot-builder-3/)**: Clustered Complex Event Processor (CEP) for IoT data streams

## Community & Support

- **GitHub Discussions**: [Join the conversation](https://github.com/Tochemey/goakt/discussions)
- **Issues**: [Report bugs or request features](https://github.com/Tochemey/goakt/issues)
- **Examples**: [Check out example projects](https://github.com/Tochemey/goakt-examples)

For priority support on complex topics or to request new features, please consider [sponsorship](https://github.com/sponsors/Tochemey).

## Contributing

We welcome contributions! Whether you're fixing a bug, adding a new feature, or improving documentation, your help is appreciated. Check out our [contribution guidelines](https://github.com/Tochemey/goakt#-contribution) to get started.

## License

GoAkt is open source and available under the MIT License.

---

Ready to get started? Head over to the [Actor Overview](actors/overview.md) to learn the fundamentals!
