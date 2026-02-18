# Actor Overview

## Table of Contents

- 🤔 [What is an Actor?](#what-is-an-actor)
- 📐 [The Actor Model](#the-actor-model)
- 🔌 [Actor Interface](#actor-interface)
- 🏗️ [Creating Actors](#creating-actors)
- 🚀 [Spawning Actors](#spawning-actors)
- 🆔 [Identity and References](#identity-and-references)
- 🌳 [Hierarchy](#hierarchy)
- 💾 [State Management](#state-management)
- 🔒 [Immutability Requirement](#immutability-requirement)
- 📨 [Message Processing](#message-processing)
- ✅ [Best Practices](#actor-best-practices)
- ⚠️ [Error Handling](#error-handling)

---

## What is an Actor?

An actor is a lightweight, concurrent, and isolated unit of computation that encapsulates both **state** and **behavior**. Actors communicate exclusively through asynchronous message passing, making them ideal for building concurrent and distributed systems.

In GoAkt, actors are the fundamental building blocks for creating reactive, fault-tolerant applications. Each actor:

- Processes one message at a time (ensuring thread safety)
- Maintains its own private state
- Communicates only via messages
- Has a unique identity: a **PID** when local, or an **Address** when referenced from another node (e.g. in a cluster)
- Can be supervised for fault tolerance
- Has a lifecycle (short-lived or long-lived)
- Is thread-safe
- Can be stateful or stateless
- Can have children and supervise them
- Can watch/unwatch other actors for their termination to take some business decisions - see [watch and unwatch](watch.md)
- Can adopt various forms using behaviours - see [behaviours](behaviours.md)

## The Actor Model

The actor model is a conceptual model for handling concurrent computation. When an actor receives a message, it can:

1. **Send messages** to other actors
2. **Create new actors** (spawn child actors)
3. **Designate behavior** for the next message (change state/behavior)
4. **Watch/Unwatch** another actor
5. **Persist** state if it is a stateful actor

This simple set of rules enables powerful patterns for building scalable, fault-tolerant systems.

## Actor Interface

Every actor implements the `Actor` interface:

```go
type Actor interface {
	PreStart(ctx *Context) error
	Receive(ctx *ReceiveContext)
	PostStop(ctx *Context) error
}
```

### Lifecycle Hooks

**PreStart** — Called once before the actor processes messages. Use it to initialize dependencies (DB, caches, connections), recover persistent state, or set up resources. Return an error to prevent the actor from starting (the supervisor will handle it). On success, a `goaktpb.PostStart` system message is sent to `Receive` as the first message. Do setup that needs `ctx.Self()` (subscriptions, registration) in `Receive` when handling `PostStart`, since `Self()` is not available in `PreStart`.

**Receive** — Where all messages are handled. Use a type switch on `ctx.Message()` and handle `*goaktpb.PostStart` first for one-time setup. Use `ctx.Response(...)` to reply to requesters and `ctx.Unhandled()` for unhandled types.

**PostStop** — Called on shutdown. Release resources (connections, files, goroutines), flush logs or metrics, and notify other systems. Return an error to log shutdown failures.

## Spawning Actors

Actors are spawned within an `ActorSystem`. GoAkt supports several ways to create actors:

| Method           | Where          | Use when                                                                                                                                                         |
|------------------|----------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Spawn`          | Actor system   | Spawn locally on this node (default).                                                                                                                            |
| `SpawnOn`        | Actor system   | Spawn in cluster (local or remote/datacenter). Use **ActorOf** to get PID or address. [Cluster](../cluster/overview.md), [Relocation](../cluster/relocation.md). |
| `ctx.Spawn`      | Inside Receive | Spawn a **child** of the current actor; check `ctx.Err()` and nil PID on failure.                                                                                |
| `SpawnChild`     | From a PID     | Spawn a **child** of the given actor.                                                                                                                            |
| `SpawnFromFunc`  | Actor system   | Spawn from a **receive function**. **SpawnNamedFromFunc** for a fixed name.                                                                                      |
| `SpawnRouter`    | Actor system   | Spawn a **router** (pool of routees). [Routers](routers.md).                                                                                                     |
| `SpawnSingleton` | Actor system   | **Cluster singleton** (one instance in cluster). [Cluster Singleton](../cluster/cluster_singleton.md).                                                           |

All spawn options (mailbox, supervisor, passivation, reentrancy, etc.) apply to `Spawn`, `SpawnOn`, and `ctx.Spawn` / `SpawnChild` where relevant. Create an actor system with `NewActorSystem`, call `Start`, then spawn with `Spawn` to get a PID.

### Spawn Options

You can customize actor behavior with spawn options.

| Option                    | Description                                                                                   | See                                                                       |
|---------------------------|-----------------------------------------------------------------------------------------------|---------------------------------------------------------------------------|
| `WithMailbox`             | Mailbox implementation (unbounded, bounded, priority, etc.).                                  | [Mailbox](mailbox.md)                                                     |
| `WithSupervisor`          | Failure-handling strategy (restart, stop, resume).                                            | [Supervisor](supervisor.md)                                               |
| `WithPassivationStrategy` | When to passivate idle actors (time-based, long-lived, message-count).                        | [Passivation](passivation.md)                                             |
| `WithPassivateAfter`      | _(Deprecated)_ Use `WithPassivationStrategy` with `TimeBasedStrategy` instead.                | [Passivation](passivation.md)                                             |
| `WithLongLived`           | Actor is never passivated; lives until system shutdown.                                       | [Passivation](passivation.md)                                             |
| `WithStashing`            | Enable a stash for messages the actor cannot process yet.                                     | [Mailbox](mailbox.md#stashing)                                            |
| `WithReentrancy`          | Enable async Request/RequestName and set policy (e.g. AllowReentrant).                        | [Reentrancy](reentrancy.md)                                               |
| `WithDependencies`        | Inject dependencies into the actor at spawn.                                                  | [Dependencies](dependencies.md)                                           |
| `WithPlacement`           | Where to spawn in cluster: `RoundRobin`, `Random`, `Local`, `LeastLoad`. Only with `SpawnOn`. | [Cluster](../cluster/overview.md), [Relocation](../cluster/relocation.md) |
| `WithRole`                | Spawn only on nodes that advertise this role. Only with `SpawnOn`.                            | [Cluster](../cluster/overview.md)                                         |
| `WithRelocationDisabled`  | Do not relocate actor to another node on failure (cluster).                                   | [Relocation](../cluster/relocation.md)                                    |
| `WithDataCenter`          | Spawn in a specific datacenter. Only with `SpawnOn` and datacenter-aware cluster.             | [Cluster](../cluster/overview.md)                                         |

Combine options as needed: `WithPassivationStrategy`, `WithMailbox`, `WithSupervisor`, `WithReentrancy`, etc. See the linked docs for each.

## Identity and References

Spawning an actor returns a unique reference. There are two ways to refer to an actor for messaging:

### PID (Process ID)

- Full local reference (system, address, metadata).
- Use for **local** actors and any API that takes a PID: `Tell`, `Ask`, `ctx.Forward`.
- Useful methods: `IsRunning`, `Name`, `Address`, `Shutdown`.

### Address

- Network identity (host, port, name). Use when the actor is **remote** (e.g. on another cluster node).
- Remote APIs take an address: `RemoteTell`, `RemoteAsk`. Get the address from a PID with `Address`.

## Hierarchy

Actors in GoAkt are organized in a hierarchy:

```
ActorSystem
  └── RootGuardian
        ├── SystemGuardian (system actors)
        └── UserGuardian (user actors)
              ├── Actor1
              │     ├── ChildActor1
              │     └── ChildActor2
              └── Actor2
```

### Parent-Child Relationships

- **Parents supervise children**: If a child fails, the parent decides how to handle it
- **Children are stopped when parent stops**: Ensures clean shutdown
- **Hierarchical path**: Children inherit their parent's path

### Spawning Child Actors

From inside `Receive`, call `ctx.Spawn` to create a child supervised by the current actor. Check the returned error and PID; on failure report with `ctx.Err(err)` if appropriate. See [Spawning](spawn.md) for options.

## State Management

Actors encapsulate state that is:

- **Private**: Only accessible within the actor
- **Thread-safe**: Messages processed one at a time
- **Mutable**: State can change between messages

### Best Practices

Keep fields private (unexported), initialize in `PreStart`, modify only in `Receive`, and clean up in `PostStop`. State is only accessed inside the actor, so no locks are needed.

## Immutability Requirement

Actor structs must be **immutable from the outside**: use unexported fields and initialize in `PreStart`, not via constructors. Avoid passing in dependencies (e.g. DB) in a struct literal; open or inject them in `PreStart` (or via `WithDependencies`). This keeps supervision and lifecycle consistent.

## Message Processing

Actors process messages **one at a time** from their mailbox:

1. Message arrives and is queued in mailbox
2. Actor dequeues message
3. `Receive` is called with message context
4. Message is processed
5. Next message is dequeued

This sequential processing eliminates the need for locks and makes actors thread-safe by design.

## Best Practices

### Do's ✅

- Keep actors small and focused (single responsibility)
- Use immutable messages (protocol buffers)
- Handle all message types explicitly
- Use supervision for fault tolerance
- Clean up resources in `PostStop`
- Use typed messages with protocol buffers

### Don'ts ❌

- Don't share state between actors (use messages instead)
- Don't make synchronous calls to external systems in `Receive` (use `PipeTo` instead)
- Don't block indefinitely in message handlers
- Don't expose actor internals via exported fields
- Don't create actors in constructors (use `PreStart`)

## Error Handling

Actors handle errors in four ways: 
- (1) return an error from **PreStart** so the actor does not start and the supervisor handles it; 
- (2) **supervision** so parents handle child failures; 
- (3) **ctx.Err(err)** in `Receive` to report non-fatal errors (prefer this over panic when you know the error type); 
- (4) **panic recovery** — panics are turned into supervision signals. See [Supervisor](supervisor.md) for directives and configuration.

