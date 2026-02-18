# GoAkt Documentation

This folder contains the reference documentation for [GoAkt](https://github.com/Tochemey/goakt). For installation, quick start, and project overview, see the [main repository README](../README.md).

## How to navigate

- **[SUMMARY.md](SUMMARY.md)** — Full table of contents: all sections and pages in one place.
- **Suggested starting points:**
  - [Actors › Overview](actors/overview.md) — Core concepts, messaging, and lifecycle
  - [Design Principles](design/principle.md) — Design goals and constraints
  - [Use Cases](usecases/overview.md) — When and how to use GoAkt

## Documentation layout

| Section           | Description                                                                              |
|-------------------|------------------------------------------------------------------------------------------|
| **Actors**        | Messaging, behaviors, spawning, supervision, routers, stashing, reentrancy, dependencies |
| **Actor System**  | Configuration, extensions, coordinated shutdown                                          |
| **Grains**        | Virtual actors: overview, stateful grains, usage patterns                                |
| **Remoting**      | Cross-node communication, TLS, context propagation                                       |
| **Cluster**       | Discovery, singletons, cluster client, relocation, multi-datacenter                      |
| **PubSub**        | Topic-based publish-subscribe                                                            |
| **Events Stream** | System and lifecycle events                                                              |
| **Observability** | Logging and metrics                                                                      |
| **Testing**       | Testing actors and best practices                                                        |

Use [SUMMARY.md](SUMMARY.md) to jump to any topic.
