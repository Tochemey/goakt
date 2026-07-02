<h2 align="center">
  <img src="docs/assets/goakt-messaging-distributed-go.png" alt="GoAkt - Distributed Actor framework for Go" width="500"/><br />
  Distributed Actor framework for Go
</h2>

<p align="center">
  <a href="https://github.com/Tochemey/goakt/actions/workflows/build.yml"><img src="https://img.shields.io/github/actions/workflow/status/Tochemey/goakt/build.yml?branch=main" alt="build" /></a>
  <a href="https://pkg.go.dev/github.com/tochemey/goakt"><img src="https://pkg.go.dev/badge/github.com/tochemey/goakt.svg" alt="Go Reference" /></a>
  <a href="https://codecov.io/gh/Tochemey/goakt"><img src="https://codecov.io/gh/Tochemey/goakt/graph/badge.svg?token=J0p9MzwSRH" alt="codecov" /></a>
  <a href="https://go.dev/doc/install"><img src="https://img.shields.io/github/go-mod/go-version/Tochemey/goakt" alt="GitHub go.mod Go version" /></a>
  <!-- <a href="https://github.com/Tochemey/goakt/releases/latest"><img src="https://img.shields.io/github/v/release/Tochemey/goakt" alt="Latest Release" /></a> -->
  <!-- <a href="./LICENSE"><img src="https://img.shields.io/github/license/Tochemey/goakt" alt="License" /></a> -->
  <a href="./CLA.md"><img src="https://img.shields.io/badge/CLA-signed%20on%20PR-blue" alt="CLA" /></a>
  <a href="https://github.com/avelino/awesome-go"><img src="https://awesome.re/badge.svg" alt="Awesome" /></a>
  <!-- <a href="https://www.bestpractices.dev/projects/9248"><img src="https://www.bestpractices.dev/projects/9248/badge" alt="OpenSSF Best Practices" /></a> -->
</p>

GoAkt is a distributed actor framework for [Go](https://go.dev/) that lets you build reactive systems with typed messages. It runs as a single process or a cluster of nodes behind the same API, and ships with the primitives (supervision, remoting, clustering, CRDTs, streams, observability) needed to put actor systems into production without re-rolling them yourself.

New to the actor model? Brian Storti's [short introduction](https://www.brianstorti.com/the-actor-model/) is a good starting point; the references at the end of that post go deeper.

## Features

### Core

- Actor model: concurrent actors with typed messages
- Grains: virtual actors with their own context and lifecycle
- Hierarchies: parent and child trees via `SpawnChild`, with child-to-parent navigation
- Lifecycle: `PreStart`, `PostStop`, graceful stop, and poison-pill shutdown
- Behavior switching: `Become` and `UnBecome`, plus stacked `BecomeStacked` and `UnBecomeStacked` for protocol phases
- Messaging: `Tell`, `Ask`, `BatchTell`, and `BatchAsk` for fire-and-forget, request-response, and bulk delivery
- Forward and `PipeTo`: forward messages preserving the original sender, and pipe async task results back to an actor
- Reentrancy: non-blocking async `Request` with configurable modes and in-flight limits
- Stashing: `Stash`, `Unstash`, and `UnstashAll` to defer messages during transient states
- Watch and Terminated: monitor actor lifecycle and receive `Terminated` on death
- PubSub: topic-based publish and subscribe via a dedicated `TopicActor`, cluster-aware with cross-node dissemination over remoting
- Mailboxes: unbounded FIFO, bounded, priority, and fair (segmented)
- Supervision: one-for-one and one-for-all strategies with `Stop`, `Resume`, `Restart`, and `Escalate` directives, plus retry windows
- Passivation: auto-stop idle actors via a time-based strategy
- Reinstate: bring a previously stopped actor back online by PID or name
- Circuit breaker: `PipeTo` integrates the `breaker` package to short-circuit calls to unhealthy dependencies
- Dead letters: unhandled messages captured and published on the event stream

### Routing

- Routers: round-robin, random, and fan-out strategies

### Scheduling

- Timers: `ScheduleOnce`, recurring `Schedule`, and `ScheduleWithCron` for cron-driven message delivery
- Schedule lifecycle: `PauseSchedule`, `ResumeSchedule`, and `CancelSchedule` on existing references

### Cluster

- Remoting: TCP actor communication across nodes with pluggable serializers (Proto, CBOR, and custom)
- TLS and mTLS: configurable transport security for remoting
- Discovery: Consul, etcd, Kubernetes, NATS, mDNS, and static
- Location transparency: address actors without knowing their node
- Relocation: automatic actor relocation on node failure
- Cluster singletons: single instance cluster-wide with guardian lifecycle
- Multi-datacenter: DC-transparent messaging, pluggable control plane (NATS JetStream, etcd), and DC-aware placement

### CRDTs & Streams

- CRDTs: `GCounter`, `PNCounter`, `LWWRegister`, `MVRegister`, `ORSet`, `ORMap`, and `Flag`, with delta replication, anti-entropy sync, tombstone deletion, and snapshots
- Reactive streams: backpressure-aware processing with a composable DSL (`map`, `filter`, `flatMap`, `batch`, `throttle`, fan-out and fan-in), stage fusion, and built-in metrics and tracing

### Observability

- OpenTelemetry metrics, event stream, and dead letters
- Event stream: in-process topic-based publish and subscribe for system and user events
- Context propagation: pluggable propagation for request-scoped metadata

### Extensibility

- Extensions: register system-wide capabilities with `WithExtensions` on the actor system, then resolve them from any actor or grain via `Extension` on the receive context
- Dependency injection: attach serializable dependencies to an actor at spawn time with `WithDependencies`, accessed inside `Receive` via `Dependency` and `Dependencies`; the actor system can also `Inject` dependency types for cluster-wide reconstruction on relocation

See [Documentation](https://docs.goakt.dev) for the full feature reference.

## Installation

```bash
go get github.com/tochemey/goakt/v4
```

## Examples

See the [examples repository](https://github.com/Tochemey/goakt-examples) for runnable code covering local, remote, and clustered actor systems.

## Architecture

Architecture documents: [Architecture](./architecture/).

## Benchmark

Benchmark suite: [Benchmark](./benchmark/).

## Support

GoAkt is free and open source. If you need priority support on complex topics or request new features, please consider [sponsorship](https://github.com/sponsors/Tochemey).

## Security

Applications using this library should routinely upgrade their **Go version** and refresh **dependencies** as needed to mitigate security vulnerabilities. **GoAkt** will make a best effort to keep dependencies current and perform vulnerability checks whenever necessary.

## Community

You can join these groups and chat to discuss and ask GoAkt related questions on:

[![GitHub Discussions](https://img.shields.io/github/discussions/Tochemey/goakt)](https://github.com/Tochemey/goakt/discussions)
[![GitHub Issues or Pull Requests](https://img.shields.io/github/issues/Tochemey/goakt)](https://github.com/Tochemey/goakt/issues)

## Contribution

We welcome contributions: bug fixes, new features, and documentation improvements. Before diving in, read the [Architecture Document](./arch/ARCHITECTURE.md) to understand the codebase. We use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) and a Docker-backed `Makefile` so contributors only need Docker and Make installed; run `make help` to see the available targets.

See [contributing.md](./CONTRIBUTING.md) for prerequisites, setup, and the full contribution workflow.

## In Production

This framework is used in production by the following projects/companies:

- [Baki Money](https://www.baki.money/): AI-powered expense tracking platform that turns receipts into stories.
- [Event Processor](https://www.v-app.io/iot-builder-3/): Clustered Complex Event Processor (CEP) for IoT data streams.

## Sponsors

<!-- sponsors --><a href="https://github.com/andrew-werdna"><img src="https:&#x2F;&#x2F;github.com&#x2F;andrew-werdna.png" width="60px" alt="User avatar: Andrew Brown" /></a><!-- sponsors -->

## Feedback

Share feedback on this [tracking issue](https://github.com/Tochemey/goakt/issues/948); it helps shape what we work on next.
