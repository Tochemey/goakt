# Use Cases

GoAkt is designed for systems where concurrency, distribution, and fault tolerance are first-class concerns. This page describes the categories of problems the framework solves well, the GoAkt features that apply to each, and pointers to the relevant documentation.

## Production Deployments

GoAkt is used in production today:

| Product                                                | Domain                      | How GoAkt is used                                                   |
| ------------------------------------------------------ | --------------------------- | ------------------------------------------------------------------- |
| [Baki Money](https://www.baki.money/)                  | AI-powered expense tracking | Financial data processing, receipt analysis pipelines               |
| [Event Processor](https://www.v-app.io/iot-builder-3/) | IoT platform                | Clustered Complex Event Processing (CEP) for real-time data streams |

---

## Real-Time Event & Stream Processing

Processing high-volume event streams -- IoT telemetry, clickstreams, log pipelines, or financial market data -- where events must be ingested, transformed, and routed with low latency.

**Why GoAkt fits:**

- Each event source or topic maps naturally to an actor, isolating state and enabling parallel processing without shared-memory coordination
- [Routers](../actors/routers.md) distribute work across a pool of processing actors (round-robin, random, or fan-out)
- [Pub/Sub](../pubsub/overview.md) decouples producers from consumers, allowing event fan-out to multiple downstream processors
- [Clustering](../cluster/overview.md) scales processing horizontally across nodes with automatic discovery and fault tolerance

**Key features:** Routers, Pub/Sub, Clustering, Passivation

---

## Entity Management & Domain Modeling

Modeling domain entities -- users, orders, accounts, devices, sessions -- as individual actors, each owning its own state and processing messages sequentially.

**Why GoAkt fits:**

- [Grains](../grains/overview.md) (virtual actors) are activated on demand and addressed by identity, removing the need for manual lifecycle management
- Each grain processes messages sequentially, eliminating concurrency bugs within a single entity
- [Passivation](../actors/passivation.md) automatically reclaims idle grains, keeping memory usage proportional to active entities
- In cluster mode, grains are transparently located across nodes -- the caller does not need to know where the grain runs

**Key features:** Grains, Passivation, Clustering, Dependencies

---

## Distributed Transaction Coordination

Orchestrating multi-step workflows that span multiple services or actors -- order fulfillment, payment processing, reservation systems -- where each step may need compensation on failure.

**Why GoAkt fits:**

- [Behaviors](../actors/behaviours.md) let an actor change its message handler at each stage of a workflow, modeling the saga as a state machine
- [Reentrancy](../actors/reentrancy.md) enables non-blocking request/response chains across actors without deadlocks
- [Stashing](../actors/stashing.md) buffers messages that arrive during an in-progress step, replaying them once the step completes
- [Supervision](../actors/supervision.md) handles failures at any stage with configurable restart or stop strategies

**Example flow:**

```
Reserve Funds ──► Validate Inventory ──► Create Shipment ──► Charge Payment
      │                   │                    │                   │
      └── Compensate ◄────┘── Compensate ◄─────┘── Compensate ◄───┘
```

**Key features:** Behaviors, Reentrancy, Stashing, Supervision

---

## Session Management

Tracking user sessions, shopping carts, or connection state where each session is short-lived, idle sessions should be cleaned up, and active sessions must respond quickly.

**Why GoAkt fits:**

- Each session is a grain addressed by session ID -- activated on first request, passivated after an idle timeout
- State lives in memory for fast access; [passivation](../actors/passivation.md) reclaims resources automatically
- In a cluster, sessions are location-transparent: any node can handle any session request

**Key features:** Grains, Passivation, Clustering

---

## Worker Pools & Job Processing

Distributing CPU-bound or I/O-bound tasks across a pool of workers -- image processing, report generation, batch ETL, or API call fan-out.

**Why GoAkt fits:**

- [Routers](../actors/routers.md) provide built-in round-robin, random, and fan-out strategies for distributing work
- [PipeTo](../actors/pipeto.md) offloads blocking I/O to background goroutines and pipes results back to actors safely
- [Bounded mailboxes](../actors/mailbox.md) provide natural backpressure when workers fall behind
- [Scheduling](../actors/message_scheduling.md) triggers periodic or delayed jobs without external cron systems

**Key features:** Routers, PipeTo, Mailbox, Scheduling

---

## Event-Driven Architecture

Building systems where components communicate through events rather than direct calls -- decoupling services, enabling audit trails, and supporting eventual consistency.

**Why GoAkt fits:**

- [Pub/Sub](../pubsub/overview.md) provides topic-based messaging: actors subscribe to topics and receive published messages, both locally and across a cluster
- [Events Stream](../events_stream/overview.md) exposes internal lifecycle events (actor started, stopped, restarted, node joined/left, dead letters) for external monitoring
- Protocol Buffers ensure events have a well-defined schema that evolves safely across deployments

**Key features:** Pub/Sub, Events Stream, Clustering

---

## Monitoring, Audit & Compliance

Capturing a complete trail of system activity for operational visibility, regulatory compliance, or debugging.

**Why GoAkt fits:**

- [Events Stream](../events_stream/overview.md) broadcasts every actor lifecycle event and dead letter without requiring changes to business actors
- [Pub/Sub](../pubsub/overview.md) lets dedicated audit actors subscribe to business event topics
- [OpenTelemetry metrics](../observability/metrics.md) expose system-level and per-actor counters (processed messages, failures, restarts, dead letters) to existing monitoring infrastructure
- [Logging](../observability/logging.md) provides structured JSON output via a pluggable logger interface

**Key features:** Events Stream, Pub/Sub, Metrics, Logging

---

## Cluster-Wide Coordination

Running exactly-once logic across a cluster -- leader election, rate limiting, configuration distribution, or sequential task processing.

**Why GoAkt fits:**

- [Cluster Singletons](../cluster/cluster_singleton.md) guarantee a single actor instance across the entire cluster, automatically relocated on node failure
- [Multi-Datacenter](../cluster/multi_datacenters.md) support extends coordination across data centers with DC-aware placement and cross-DC messaging
- [Actor Relocation](../cluster/relocation.md) preserves availability by moving actors to healthy nodes during failures or rolling upgrades

**Key features:** Cluster Singletons, Multi-Datacenter, Relocation

---

## Choosing the Right Pattern

| Problem                            | Primary Pattern         | Start Here                                                                            |
| ---------------------------------- | ----------------------- | ------------------------------------------------------------------------------------- |
| High-volume event ingestion        | Routers + Pub/Sub       | [Routers](../actors/routers.md), [Pub/Sub](../pubsub/overview.md)                     |
| Per-entity state (users, orders)   | Grains                  | [Grains](../grains/overview.md)                                                       |
| Multi-step workflows / sagas       | Behaviors + Reentrancy  | [Behaviors](../actors/behaviours.md), [Reentrancy](../actors/reentrancy.md)           |
| Session tracking with auto-cleanup | Grains + Passivation    | [Grains](../grains/overview.md), [Passivation](../actors/passivation.md)              |
| Parallel job distribution          | Routers                 | [Routers](../actors/routers.md)                                                       |
| Event fan-out and decoupling       | Pub/Sub                 | [Pub/Sub](../pubsub/overview.md)                                                      |
| System observability               | Events Stream + Metrics | [Events Stream](../events_stream/overview.md), [Metrics](../observability/metrics.md) |
| Exactly-once cluster coordination  | Cluster Singletons      | [Cluster Singletons](../cluster/cluster_singleton.md)                                 |
| Cross-datacenter deployment        | Multi-Datacenter        | [Multi-Datacenter](../cluster/multi_datacenters.md)                                   |

## Further Resources

- [GoAkt Examples Repository](https://github.com/Tochemey/goakt-examples): Working code for common patterns
- [Design Principles](../design/principle.md): The reasoning behind GoAkt's architecture
- [Testing](../testing/overview.md): How to test actors and distributed scenarios with the testkit
