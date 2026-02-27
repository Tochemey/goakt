# GoAkt v4.0.0 Documentation

GoAkt is a distributed actor framework for Go, inspired by Erlang and Akka. It enables you to build reactive and
distributed systems with typed actor messages. From **v4.0.0** onward, any Go type can be used as actor messages—no
protocol buffers required.

This documentation is designed for both **users** getting started with the framework and **collaborators** contributing
to the project. It focuses on concepts, architecture, and efficient usage patterns rather than exhaustive code examples.

## What's New in v4.0.0

- **Unified APIs**: Single actor reference (`*PID`), single lookup (`ActorOf`), unified scheduler
- **Type flexibility**: `any` replaces `proto.Message` across all message-passing surfaces
- **Pluggable serialization**: ProtoSerializer (default) and CBORSerializer for arbitrary Go types
- **Simplified remoting**: Config-only public API; client is internal
- **Path interface**: Location-transparent actor identity replacing `*address.Address`

## Requirements

- Go 1.26 or higher

## Quick Links

- [Getting Started](getting-started/installation.md) — Installation and first actor
- [Actor System](concepts/actor-system.md) — Runtime, API, configuration
- [Core Concepts](concepts/actor-model.md) — Actor model, messaging, lifecycle
- [Architecture](architecture/overview.md) — System design and component map
- [Contributing](contributing/overview.md) — How to contribute to GoAkt
