# Overview

GoAkt is a **distributed actor framework for Go** that uses **protocol buffers** as the message format. It lets you build concurrent, reactive, and fault-tolerant systems that scale from a single process to a cluster of nodes. The actor model is used throughout: actors are isolated units of computation that communicate only by passing messages, so you avoid shared-memory concurrency and get clear boundaries for state and behavior.

## What GoAkt Is

- A **runtime** for the actor model in Go: you implement a small interface (PreStart, Receive, PostStop), spawn actors, and send them messages.
- **Message-first**: Every message is a protocol buffer type. That gives type safety, a well-defined wire format for remoting, and good performance.
- **Optionally distributed**: You can start with a single-node actor system and add remoting and clustering when you need cross-node communication, discovery, and fault tolerance.
- **Production-oriented**: Used in production for high-throughput and distributed workloads; it provides supervision, relocation, observability, and multi-datacenter support.

## Core Components

**Actor system** — The container and runtime. It manages actor lifecycle, provides remoting and clustering (when enabled), mailboxes, schedulers, event streams, and configuration (logging, metrics, extensions). You create one system per process and spawn actors within it.

**Actors** — The main abstraction. An actor has a unique identity (PID locally, or an address when remote), a mailbox for incoming messages, and sequential message processing. It can spawn children, watch other actors, and change behavior. State is private to the actor; there are no locks.

**Messaging** — Tell (fire-and-forget) and Ask (request–response). From inside an actor you use the receive context to tell or ask other actors; from outside you use the system or a PID. Remote and cluster messaging use the same APIs with addresses or names.

**Grains** — Virtual actors: they are addressed by identity and activated on demand. You don’t manage their lifecycle manually; the runtime creates them when first used and can passivate them when idle. Ideal for entity-style modeling (e.g. one grain per user or order).

**Remoting** — TCP-based communication between actor systems on different nodes. Required for clustering. Supports TLS and pluggable context propagation (e.g. tracing, auth).

**Cluster** — Multiple nodes form one logical system. Discovery (NATS, Consul, etcd, Kubernetes, mDNS, static), distributed routing table, placement strategies (round-robin, random, least-load, role-based), and automatic actor relocation when nodes fail or leave.

**Supervision** — Configurable failure handling: resume, restart, stop, or escalate. Strategies (one-for-one, one-for-all) and error-to-directive mapping keep failures local and predictable.

**Mailboxes, passivation, routers** — Each actor has a mailbox (unbounded, bounded, or priority). Passivation stops idle actors to free resources. Routers distribute messages across a pool of routees (round-robin, random, fan-out).

## What GoAkt Enables

- **Concurrent and distributed systems** — Many actors process messages in parallel; the model scales to multiple nodes without changing the programming model.
- **Fault tolerance** — Supervision and relocation: failed actors can be restarted or moved to healthy nodes so the system keeps running.
- **Location transparency** — You address actors by name or identity; the system resolves whether they are local or remote and routes messages accordingly.
- **Virtual actor (grain) patterns** — Huge numbers of logical entities (users, sessions, orders) with on-demand activation and automatic passivation.
- **Event-driven and reactive flows** — Pub/sub for topic-based fan-out; event streams for system and lifecycle events; non-blocking request/response with reentrancy and PipeTo.
- **Operability** — OpenTelemetry metrics, structured logging, coordinated shutdown, and optional multi-datacenter control planes for placement and cross-DC messaging.

## Performance

GoAkt is built for low latency and high throughput:

- **Lock-free where possible** — Atomic operations and concurrent structures reduce contention in the runtime and mailboxes.
- **Efficient mailboxes** — Ring-buffer–style queues; bounded and unbounded options; optional priority and fairness.
- **Binary serialization** — Protocol buffers keep messages compact and fast to marshal/unmarshal, which matters for remoting and clustering.
- **Connection pooling** — Remoting reuses TCP connections instead of opening one per request.
- **Pay for what you use** — Features are opt-in (remoting, cluster, metrics, extensions). A single-node system without clustering carries no cluster overhead.

The framework is designed to handle high-throughput scenarios in both local and distributed deployments without sacrificing responsiveness.

---

For installation, quick start, and links to all documentation topics, see the [main repository README](../README.md) and [SUMMARY.md](SUMMARY.md).
