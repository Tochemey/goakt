<h2 align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/goakt-wordmark-dark.svg">
    <img src="docs/assets/goakt-wordmark-light.svg" alt="GoAkt" width="220"/>
  </picture><br />
</h2>

<p align="center">
  <a href="https://github.com/Tochemey/goakt/actions/workflows/build.yml"><img src="https://img.shields.io/github/actions/workflow/status/Tochemey/goakt/build.yml?branch=main" alt="build" /></a>
  <a href="https://pkg.go.dev/github.com/tochemey/goakt"><img src="https://pkg.go.dev/badge/github.com/tochemey/goakt.svg" alt="Go Reference" /></a>
  <a href="https://codecov.io/gh/Tochemey/goakt"><img src="https://codecov.io/gh/Tochemey/goakt/graph/badge.svg?token=J0p9MzwSRH" alt="codecov" /></a>
  <a href="https://go.dev/doc/install"><img src="https://img.shields.io/github/go-mod/go-version/Tochemey/goakt" alt="GitHub go.mod Go version" /></a>
  <a href="https://github.com/Tochemey/goakt/releases/latest"><img src="https://img.shields.io/github/v/release/Tochemey/goakt?label=release" alt="Latest Release" /></a>
  <a href="./LICENSE"><img src="https://img.shields.io/github/license/Tochemey/goakt" alt="License" /></a>
  <a href="https://github.com/avelino/awesome-go"><img src="https://awesome.re/badge.svg" alt="Awesome" /></a>
  <a href="https://join.slack.com/t/oss-r2l2029/shared_invite/zt-42zcqua8y-unSUH0tFlOQzwT_smzYfOQ"><img src="https://img.shields.io/badge/Slack-Join%20our%20community-4A154B?logo=slack&logoColor=white" alt="Join our Slack" /></a>
</p>

GoAkt is a distributed actor framework for [Go](https://go.dev/), inspired by Erlang and Akka. In development since 2022 and used [in production](#in-production), it lets you build responsive, resilient, and elastic systems with typed actor messages, running as a single process or a cluster of nodes behind the same API.

- **Simpler concurrency.** Actors process one message at a time; you write plain Go with no locks, channel plumbing, or shared-state bugs.
- **Location transparency.** Send a message to a local, remote, or clustered actor with the same API; the framework handles the wire.
- **Resilience by design.** Supervision trees and the "let it crash" model, inspired by Erlang/OTP, keep failures contained and recoverable.
- **Production batteries included.** Clustering, virtual actors (grains), CRDTs, streams, scheduling, passivation, and observability, without re-rolling them yourself.

Teams use GoAkt for event processing, IoT and edge workloads, real-time platforms, and distributed backends that would otherwise need a message broker plus custom coordination code.

New to the actor model? Actors are isolated units of computation that share no memory, each with a mailbox, private state, and one message in flight at a time. On a message, an actor can create more actors, send messages, or change how it handles the next one. For more depth, watch **Carl Hewitt**, the father of the actor model, explain it in his own words:

[![Carl Hewitt on the actor model](https://img.youtube.com/vi/7erJ1DV_Tlo/hqdefault.jpg)](https://www.youtube.com/watch?v=7erJ1DV_Tlo)

See the [documentation](https://docs.goakt.dev) for the full feature reference.

## Installation

```bash
go get github.com/tochemey/goakt/v4
```

## Examples

See the [examples repository](https://github.com/Tochemey/goakt-examples) for runnable code covering local, remote, and clustered actor systems.

## In Production

This framework is used in production by the following projects/companies:

- [Baki Money](https://www.baki.money/): AI-powered expense tracking platform that turns receipts into stories.
- [Event Processor](https://www.v-app.io/iot-builder-3/): Clustered Complex Event Processor (CEP) for IoT data streams.

## Security

Applications using this library should routinely upgrade their **Go version** and refresh **dependencies** as needed to mitigate security vulnerabilities. **GoAkt** will make a best effort to keep dependencies current and perform vulnerability checks whenever necessary. To report a vulnerability, use GitHub's [Report a Vulnerability](https://github.com/Tochemey/goakt/security/advisories/new) form, as described in [SECURITY.md](./SECURITY.md).

## Community

Ask questions and follow the work in [Discussions](https://github.com/Tochemey/goakt/discussions), [Issues](https://github.com/Tochemey/goakt/issues), or on [Slack](https://join.slack.com/t/oss-r2l2029/shared_invite/zt-42zcqua8y-unSUH0tFlOQzwT_smzYfOQ). Feedback on the [tracking issue](https://github.com/Tochemey/goakt/issues/948) helps shape what we work on next.

## Contribution

  <a href="./CLA.md"><img src="https://img.shields.io/badge/CLA-signed%20on%20PR-blue" alt="CLA" /></a>

We welcome contributions: bug fixes, new features, and documentation improvements. Before diving in, read the [Architecture Document](./architecture/ARCHITECTURE.md) to understand the codebase. We use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) and a Docker-backed `Makefile` so contributors only need Docker and Make installed; run `make help` to see the available targets.

See [contributing.md](./CONTRIBUTING.md) for prerequisites, setup, and the full contribution workflow, the other [design documents](./architecture/) covering the dispatcher pool, CRDTs, streams, reentrancy, and reliable delivery, and the [benchmark suite](./benchmark/) for the performance harness.

## Sponsors

GoAkt is free and open source. If you need priority support on complex topics or want to fund new features, consider [sponsorship](https://github.com/sponsors/Tochemey).

<!-- sponsors --><a href="https://github.com/andrew-werdna"><img src="https:&#x2F;&#x2F;github.com&#x2F;andrew-werdna.png" width="60px" alt="User avatar: Andrew Brown" /></a><a href="https://github.com/StringKe"><img src="https:&#x2F;&#x2F;github.com&#x2F;StringKe.png" width="60px" alt="User avatar: StringKe" /></a><!-- sponsors -->

## Credits

The Go gopher was designed by [Renée French](https://reneefrench.blogspot.com/) and is used under the [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/) license.
