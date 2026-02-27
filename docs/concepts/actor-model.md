# Actor Model

## What is the actor model?

The actor model treats **actors** as the fundamental unit of computation. Each actor is an isolated entity that:

- Processes messages **sequentially** (one at a time)
- Maintains **private state** (no shared memory with other actors)
- Communicates **exclusively via message passing**

There is no shared state between actors. The only way to interact with one is to send it a message.

## Why use actors?

Actors provide a natural model for concurrent and distributed systems:

- **Encapsulation** — State is private; no locks or mutexes needed inside an actor
- **Location transparency** — You send to a PID; the framework routes locally or remotely
- **Fault isolation** — A failing actor does not corrupt others; supervision handles recovery
- **Scalability** — Actors can be distributed across nodes; the cluster manages placement

## Core concepts

| Concept         | Description                                                                                                            |
|-----------------|------------------------------------------------------------------------------------------------------------------------|
| **Actor**       | The fundamental unit. Receives messages, updates private state, spawns children, sends messages.                       |
| **ActorSystem** | The runtime host. Manages lifecycle, messaging, cluster membership, and remoting. See [Actor System](actor-system.md). |
| **PID**         | Process identifier—a live handle to a running actor. Used for all interactions.                                        |
| **Path**        | The canonical location of an actor: `goakt://system@host:port/path/to/actor`.                                          |
| **Mailbox**     | Each actor has one. Messages wait here until the actor processes them.                                                 |

## Actor hierarchy

Every actor lives inside a tree. GoAkt creates guardian actors at startup:

- **Root** — Top of the tree
- **System** — Parent of internal actors (dead letter, scheduler, etc.)
- **User** — Parent of all user-spawned actors

When a parent stops, all children stop first (depth-first). A parent supervises its children: on failure, the configured
strategy decides whether to resume, restart, stop, or escalate.

## Single-threaded execution

GoAkt guarantees that **only one goroutine processes messages for a given actor at any time**. You never need internal
locks to protect actor state. Message ordering between a sender-receiver pair is FIFO.

## The Actor interface

Every actor must implement the `Actor` interface:

```go
type Actor interface {
    PreStart(ctx *Context) error
    Receive(ctx *ReceiveContext)
    PostStop(ctx *Context) error
}
```

| Method       | When called                                  | Purpose                                                                                                                               |
|--------------|----------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------|
| **PreStart** | Once, before the actor processes any message | Initialize dependencies, caches, or recover persistent state. Return an error to prevent startup; the supervisor handles the failure. |
| **Receive**  | For each message in the mailbox              | Handle messages. Use a type switch on `ctx.Message()`. Call `ctx.Unhandled()` for unknown types.                                      |
| **PostStop** | When the actor is about to shut down         | Release resources, flush logs, notify other systems. Called after the mailbox is drained.                                             |

Keep actor state in unexported fields. Initialize in `PreStart`, not in constructors. The framework guarantees
single-threaded execution per actor, so no locks are needed inside `Receive`.

## Further reading

- [Brian Storti: The Actor Model](https://www.brianstorti.com/the-actor-model/) — Short introduction
- [Messaging](messaging.md) — Tell vs Ask, message types
- [Actor Lifecycle](actor-lifecycle.md) — Spawn, stop, state transitions
