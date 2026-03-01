<h2 align="center">
  <img src="docs/assets/goakt-messaging-distributed-go.png" alt="GoAkt - Distributed Actor framework for Go" width="800"/><br />
  Distributed Actor framework for Go
</h2>

---

[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/goakt/build.yml?branch=main)](https://github.com/Tochemey/goakt/actions/workflows/build.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tochemey/goakt.svg)](https://pkg.go.dev/github.com/tochemey/goakt)
[![Go Report Card](https://goreportcard.com/badge/github.com/tochemey/goakt)](https://goreportcard.com/report/github.com/tochemey/goakt)
[![codecov](https://codecov.io/gh/Tochemey/goakt/graph/badge.svg?token=J0p9MzwSRH)](https://codecov.io/gh/Tochemey/goakt)
[![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/Tochemey/goakt)](https://go.dev/doc/install)
[![Awesome](https://awesome.re/badge.svg)](https://github.com/avelino/awesome-go)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/9248/badge)](https://www.bestpractices.dev/projects/9248)

Distributed [Go](https://go.dev/) actor framework to build a reactive and distributed system in Go with typed actor messages. 
GoAkt is highly scalable and available when running in cluster mode. It comes with the necessary features require to
build a distributed actor-based system without sacrificing performance and reliability. With GoAkt, you can instantly create a fast, scalable, distributed system
across a cluster of computers.

If you are not familiar with the actor model, the blog post from Brian Storti [here](https://www.brianstorti.com/the-actor-model/) is an excellent and short introduction to the actor model.
Also, check the reference section at the end of the post for more material regarding the actor model.

> **Version & branches:** The stable release (**v3.x**) uses protocol buffers for actor messages. **v4.0.0** (in development on `main`) introduces typed messages with `any`—unified APIs, pluggable serializers (Proto/CBOR), and config-only remoting. See [CHANGELOG_V400.md](./CHANGELOG_V400.md) for the full roadmap and migration guide; [Docs](https://docs.goakt.dev/) for API reference. Bug fixes for v3.x are on `release/v3.14`.

## 🚀 Features

- **Actor Model**: Build concurrent and distributed systems using the actor model with typed messages.
- **Messaging**: Tell/Ask APIs for fire-and-forget or request/response flows.
- **Reentrancy**: Reentrancy-enabled async request messaging with configurable modes and per-call overrides.
- **Supervision**: One-for-one/one-for-all strategies, directives, and retry windows for fault tolerance.
- **Remoting**: Seamless communication between actors across nodes over TCP.
- **Context Propagation**: Pluggable context propagation for request-scoped metadata.
- **Clustering**: Multiple discovery backends (Consul, etcd, Kubernetes, NATS, mDNS, static).
- **Location Transparency**: Interact with actors without needing to know their physical location.
- **Relocation**: Automatic actor relocation on node failure with configurable policies.
- **Cluster Singletons**: Run a single instance across the cluster with guardian-based lifecycle.
- **Grains**: Virtual actors capabilities.
- **Passivation**: Automatically stop idle actors and reclaim resources.
- **Routers**: Routing strategies such as round robin, random, and fan-out.
- **Scheduling**: Timers and delayed messaging built into the runtime.
- **Stashing & Mailboxes**: Stash buffers and customizable mailboxes (bounded/unbounded, priority).
- **Dependency Injection**: Attach runtime dependencies to actors at spawn time.
- **Observability**: OpenTelemetry metrics, event stream, and dead letters.
- **Extensions**: Pluggable APIs for cross-cutting capabilities.
- **Data Center**: Multi-datacenter support with DC-transparent messaging, pluggable control plane (NATS JetStream, Etcd), DC-aware placement (SpawnOn with WithDataCenter), and cross-DC actor/grain communication.

## 📥 Installation

```bash
# v3.x — stable, used in production
go get github.com/tochemey/goakt/v3

# v4.0.0 — usable in production; heavy testing and refactoring ongoing
go get github.com/tochemey/goakt/v4
```

## 📚 Documentations

- **v4.0.0** (upcoming): [v4.0.0](https://docs.goakt.dev)
- **v3.x** (stable): [v3.14.0](https://tochemey.gitbook.io/goakt)

## 💡 Examples

Kindly check out the [examples](https://github.com/Tochemey/goakt-examples)' repository.

## 💙 Support

GoAkt is free and open source. If you need priority support on complex topics or request new features, please consider [sponsorship](https://github.com/sponsors/Tochemey).

## 🔒 Security

Applications using this library should routinely upgrade their **Go version** and refresh **dependencies** as needed to mitigate security vulnerabilities. **GoAkt** will make a best effort to keep dependencies current and perform vulnerability checks whenever necessary.

## 🌍 Community

You can join these groups and chat to discuss and ask GoAkt related questions on:

[![GitHub Discussions](https://img.shields.io/github/discussions/Tochemey/goakt)](https://github.com/Tochemey/goakt/discussions)
[![GitHub Issues or Pull Requests](https://img.shields.io/github/issues/Tochemey/goakt)](https://github.com/Tochemey/goakt/issues)

## 🤝 Contribution

We welcome contributions—bug fixes, new features, and documentation improvements. Before diving in, read the [Architecture Document](./ARCHITECTURE.md) to understand the codebase. We use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) and [Earthly](https://earthly.dev/get-earthly) for builds.

See [contributing.md](./contributing.md) for prerequisites, setup, and the full contribution workflow.

## 🏭 In Production

This framework is used in production by the following projects/companies:

- [Baki Money](https://www.baki.money/): AI-powered Expense Tracking platform that turns receipts into stories...
- [Event Processor](https://www.v-app.io/iot-builder-3/): Clustered Complex Event Processor (CEP) for IoT data streams.

## 📣 Feedback

Kindly use this [issue](https://github.com/Tochemey/goakt/issues/948) to give us your feedback that can help us enhance the framework.

## 📊 Benchmark

One can find the benchmark tests here: [Benchmark](./bench/)

## 💰 Sponsors

<!-- sponsors --><a href="https://github.com/andrew-werdna"><img src="https:&#x2F;&#x2F;github.com&#x2F;andrew-werdna.png" width="60px" alt="User avatar: Andrew Brown" /></a><!-- sponsors -->
