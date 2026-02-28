---
title: Glossary
description: Key terms and concepts.
sidebarTitle: "📖 Glossary"
---

| Term                   | Definition                                                                                                                                                                                       |
|------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Actor**              | Fundamental unit of computation. Isolated entity that processes messages sequentially and communicates via message passing.                                                                      |
| **ActorSystem**        | Top-level runtime hosting actors. Manages lifecycle, messaging, clustering, remoting. See [Actor System](../actor/actor-system).                                                                 |
| **PID**                | Process identifier. Live handle to a running actor. Used for Tell, Ask, and all interactions.                                                                                                    |
| **Path**               | Canonical location of an actor: `goakt://system@host:port/path/to/actor`. Returned by `pid.Path()`.                                                                                              |
| **Grain**              | Virtual actor. Identity-addressed; framework manages activation and passivation.                                                                                                                 |
| **Mailbox**            | Per-actor message queue. Messages enqueued by senders, dequeued by the processing goroutine.                                                                                                     |
| **Guardian**           | Built-in actor serving as root of the actor tree. Root, system, and user guardians.                                                                                                              |
| **Supervisor**         | Parent's failure-handling policy. Defines resume, restart, stop, or escalate.                                                                                                                    |
| **Directive**          | Specific supervision action: Resume, Restart, Stop, Escalate.                                                                                                                                    |
| **Passivation**        | Automatic shutdown of actors to reclaim resources. Two types: per-actor (idle-based) and system eviction (capacity-based). See [Passivation](../actor/passivation).                              |
| **System eviction**    | Node-wide limit on active actors. When exceeded, passivates by LRU, LFU, or MRU. Configured via `WithEvictionStrategy`.                                                                          |
| **Reentrancy**         | Ability to process other messages while awaiting an async Ask response.                                                                                                                          |
| **Stash**              | Side buffer where an actor can park messages for later processing. Requires `WithStashing()`. See [Stashing](../actor/stashing).                                                                 |
| **Behavior**           | A function `func(ctx *ReceiveContext)` that handles messages. Actors can switch behaviors via `Become`/`UnBecome`. See [Behaviors](../actor/behaviors).                                          |
| **PipeTo**             | Runs a task asynchronously and delivers the result to a target actor. See [PipeTo](../actor/pipeto).                                                                                             |
| **TestKit**            | Test helpers: throwaway actor system, Probe, GrainProbe. See [TestKit](../actor/testkit).                                                                                                        |
| **Dead Letter**        | Message sent to a non-existent or stopped actor. Captured by the dead-letter actor.                                                                                                              |
| **Death Watch**        | Subscription to an actor's termination; receive `Terminated` when it stops.                                                                                                                      |
| **Cluster Singleton**  | Actor with exactly one instance across the cluster. Hosted on the coordinator (oldest node); relocated when that node leaves. See [Singletons](../actor/singletons).                             |
| **Relocation**         | Automatic migration of actors and grains from a departed node to remaining cluster nodes. See [Relocation](../actor/relocation).                                                                 |
| **Discovery Provider** | Pluggable backend for finding peer nodes (Consul, etcd, K8s, NATS, mDNS, static).                                                                                                                |
| **Extension**          | System-wide plugin registered at ActorSystem creation. Accessible via `ctx.Extension(id)`.                                                                                                       |
| **Dependency**         | Per-actor resource injected at spawn. Must be serializable for relocation.                                                                                                                       |
| **Event Stream**       | In-process mechanism for system and cluster events (ActorStarted, NodeJoined, Deadletter, etc.). Subscribers use `Subscribe()` and `Iterator()`. See [Event Streams](../advanced/event-streams). |
| **PubSub**             | Application-level topic-based messaging. Topic actor manages Subscribe/Unsubscribe/Publish. Requires `WithPubSub()` or cluster. See [PubSub](../advanced/pubsub).                                |
| **Client**             | Standalone package for interacting with a GoAkt cluster from outside the actor system (CLI, API server, batch job). Tell, Ask, Spawn, grains. See [Client](../advanced/client).                  |
