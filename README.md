<h2 align="center">
  <img src="docs/assets/goakt-messaging-distributed-go.png" alt="GoAkt - Distributed Actor framework for Go" width="800"/><br />
  Distributed Actor framework for Go
</h2>

<p align="center">
  <a href="https://github.com/Tochemey/goakt/actions/workflows/build.yml"><img src="https://img.shields.io/github/actions/workflow/status/Tochemey/goakt/build.yml?branch=main" alt="build" /></a>
  <a href="https://pkg.go.dev/github.com/tochemey/goakt"><img src="https://pkg.go.dev/badge/github.com/tochemey/goakt.svg" alt="Go Reference" /></a>
  <a href="https://goreportcard.com/report/github.com/tochemey/goakt"><img src="https://goreportcard.com/badge/github.com/tochemey/goakt" alt="Go Report Card" /></a>
  <a href="https://codecov.io/gh/Tochemey/goakt"><img src="https://codecov.io/gh/Tochemey/goakt/graph/badge.svg?token=J0p9MzwSRH" alt="codecov" /></a>
  <a href="https://go.dev/doc/install"><img src="https://img.shields.io/github/go-mod/go-version/Tochemey/goakt" alt="GitHub go.mod Go version" /></a>
  <a href="https://github.com/avelino/awesome-go"><img src="https://awesome.re/badge.svg" alt="Awesome" /></a>
  <a href="https://www.bestpractices.dev/projects/9248"><img src="https://www.bestpractices.dev/projects/9248/badge" alt="OpenSSF Best Practices" /></a>
  <a href="https://github.com/Tochemey/goakt/releases/latest"><img src="https://img.shields.io/github/v/release/Tochemey/goakt" alt="Latest Release" /></a>
  <a href="./LICENSE"><img src="https://img.shields.io/github/license/Tochemey/goakt" alt="License" /></a>
</p>

Distributed [Go](https://go.dev/) actor framework to build a reactive and distributed system in Go with typed actor messages.
GoAkt is highly scalable and available when running in cluster mode. It comes with the necessary features require to
build a distributed actor-based system without sacrificing performance and reliability. With GoAkt, you can instantly create a fast, scalable, distributed system
across a cluster of computers.

If you are not familiar with the actor model, the blog post from Brian Storti [here](https://www.brianstorti.com/the-actor-model/) is an excellent and short introduction to the actor model.
Also, check the reference section at the end of the post for more material regarding the actor model.

## Features

- **Core & Messaging**
  - Actor model — concurrent, distributed actors with typed messages
  - Grains — virtual actors with their own context and lifecycle
  - Actor hierarchies — parent/child trees via `SpawnChild`, with child/parent navigation
  - Lifecycle hooks — `PreStart` and `PostStop`; graceful stop and poison-pill shutdown
  - Behavior switching — `Become` / `UnBecome` plus stacked `BecomeStacked` / `UnBecomeStacked` for protocol phases
  - Messaging — `Tell` / `Ask` for fire-and-forget and request/response flows, plus `BatchTell` / `BatchAsk` for bulk delivery
  - PubSub — topic-based publish/subscribe via a dedicated `TopicActor`, cluster-aware with cross-node dissemination over remoting
  - Forward & PipeTo — forward messages preserving the original sender; pipe async task results back to an actor
  - Reentrancy — non-blocking async `Request` with configurable modes and in-flight limits
  - Watch & Terminated — monitor actor lifecycle and receive `Terminated` on death
  - Stashing — `Stash` / `Unstash` / `UnstashAll` to defer messages during transient states
  - Dependency injection — attach runtime dependencies to actors at spawn time
- **Supervision & Fault Tolerance**
  - Supervision — one-for-one / one-for-all strategies with `Stop` / `Resume` / `Restart` / `Escalate` directives and retry windows
  - Passivation — auto-stop idle actors via a time-based strategy
  - Reinstate — bring a previously stopped actor back online by PID or name
  - Circuit breaker — `PipeTo` integrates the `breaker` package to short-circuit calls to unhealthy dependencies
  - Dead letters — unhandled messages captured and published on the event stream
- **Scheduling**
  - Timers — `ScheduleOnce`, recurring `Schedule`, and `ScheduleWithCron` for cron-driven delivery
  - Schedule lifecycle — `PauseSchedule` / `ResumeSchedule` / `CancelSchedule` on existing references
- **Routing & Mailboxes**
  - Routers — round-robin, random, and fan-out routing strategies
  - Mailboxes — unbounded FIFO, bounded, priority, and fair (segmented) mailboxes
- **Cluster & Topology**
  - Remoting — TCP actor communication across nodes with pluggable serializers (Proto, CBOR and custom)
  - Clustering — Consul, etcd, Kubernetes, NATS, mDNS, and static discovery
  - Location transparency — address actors without knowing their node
  - Relocation — automatic actor relocation on node failure
  - Cluster singletons — single instance cluster-wide with guardian lifecycle
  - Multi-datacenter — DC-transparent messaging, pluggable control plane (NATS JetStream, Etcd), DC-aware placement
- **State & Streams**
  - Distributed data — CRDTs (GCounter, PNCounter, LWWRegister, MVRegister, ORSet, ORMap, Flag) with delta replication, anti-entropy sync, tombstone deletion, and snapshots
  - Reactive streams — backpressure-aware stream processing with a composable DSL (map, filter, flatMap, batch, throttle, fan-out/in), stage fusion, and built-in metrics/tracing
- **Observability & Extensibility**
  - Observability — OpenTelemetry metrics, event stream, dead letters
  - Event stream — in-process topic-based pub/sub for system and user events
  - Context propagation — pluggable propagation for request-scoped metadata
  - Extensions — pluggable APIs for cross-cutting capabilities
  - TLS / mTLS — configurable transport security for remoting

See [docs.goakt.dev](https://docs.goakt.dev) for the full feature reference.

## Installation

```bash
go get github.com/tochemey/goakt/v4
```

## Documentations

- **v4**: [docs.goakt.dev](https://docs.goakt.dev)
- **v3** (legacy): [tochemey.gitbook.io/goakt](https://tochemey.gitbook.io/goakt)

## Examples

Kindly check out the [examples](https://github.com/Tochemey/goakt-examples)' repository.

## Support

GoAkt is free and open source. If you need priority support on complex topics or request new features, please consider [sponsorship](https://github.com/sponsors/Tochemey).

## Security

Applications using this library should routinely upgrade their **Go version** and refresh **dependencies** as needed to mitigate security vulnerabilities. **GoAkt** will make a best effort to keep dependencies current and perform vulnerability checks whenever necessary.

## Community

You can join these groups and chat to discuss and ask GoAkt related questions on:

[![GitHub Discussions](https://img.shields.io/github/discussions/Tochemey/goakt)](https://github.com/Tochemey/goakt/discussions)
[![GitHub Issues or Pull Requests](https://img.shields.io/github/issues/Tochemey/goakt)](https://github.com/Tochemey/goakt/issues)

## Contribution

We welcome contributions—bug fixes, new features, and documentation improvements. Before diving in, read the [Architecture Document](./arch/ARCHITECTURE.md) to understand the codebase. We use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) and a Docker-backed `Makefile` so contributors only need Docker and Make installed — run `make help` to see the available targets.

See [contributing.md](./CONTRIBUTING.md) for prerequisites, setup, and the full contribution workflow.

## In Production

This framework is used in production by the following projects/companies:

- [Baki Money](https://www.baki.money/): AI-powered Expense Tracking platform that turns receipts into stories...
- [Event Processor](https://www.v-app.io/iot-builder-3/): Clustered Complex Event Processor (CEP) for IoT data streams.

## Feedback

Kindly use this [issue](https://github.com/Tochemey/goakt/issues/948) to give us your feedback that can help us enhance the framework.

## Benchmark

One can find the benchmark tests here: [Benchmark](./benchmark/)

## Architecture

One can find the architecture documents here: [Architecture](./architecture/)

## Sponsors

<!-- sponsors --><a href="https://github.com/andrew-werdna"><img src="https:&#x2F;&#x2F;github.com&#x2F;andrew-werdna.png" width="60px" alt="User avatar: Andrew Brown" /></a><!-- sponsors -->
