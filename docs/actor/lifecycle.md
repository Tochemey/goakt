---
title: Actor Lifecycle
description: Spawn, stop, and lifecycle events.
sidebarTitle: "🔄 Actor Lifecycle"
---

## States

A PID tracks its state through a set of flags. The main states:

| State           | Meaning                                     |
|-----------------|---------------------------------------------|
| **Running**     | Actor is active and processing messages     |
| **Stopping**    | Shutdown or passivation in progress         |
| **Suspended**   | Suspended by the supervisor after a failure |
| **Passivating** | Idle timeout; about to be stopped           |

Use `pid.IsRunning()`, `pid.IsSuspended()`, `pid.IsStopping()` to query state when needed.

## Lifecycle hooks

- **PreStart** — Called once when the actor is spawned. Use for initialization.
- **Receive** — Called for each message. This is where your logic lives.
- **PostStop** — Called once when the actor is about to stop. Use for cleanup.

## Spawn and stop

- **Spawn** — Creates the actor, registers it, calls `PreStart`, and starts the mailbox dispatch loop.
- **Stop** — Sends a stop signal. The actor finishes current work, runs `PostStop`, and is removed from the tree.

When a parent stops, all children stop first (depth-first). The order among descendants is not guaranteed.

## Lifecycle hooks (Actor interface)

The `Actor` interface defines three lifecycle hooks. See [Actor Model](actor-model.md#the-actor-interface) for the full
interface.

| Hook         | When                 | Use for                                                              |
|--------------|----------------------|----------------------------------------------------------------------|
| **PreStart** | Before first message | Initialize DB, caches, recover state. Return error to abort startup. |
| **Receive**  | For each message     | Process messages. State is private; no locks needed.                 |
| **PostStop** | Before termination   | Release resources, flush buffers, notify other systems.              |

If `PreStart` returns an error, the actor is not started and the supervisor handles the failure.

## System messages

The framework injects **system messages** into the actor's mailbox to drive lifecycle and coordination. These are plain
Go structs in the `actor` package. Handle them in `Receive` when you need to react.

### Lifecycle control

| Message        | Role                                                                                                                                                                               |
|----------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **PostStart**  | Delivered as the first message after the actor is spawned. Use it to capture `ctx.Self()`, set up references, or perform logic that must run after the actor is fully started.     |
| **PoisonPill** | Gracefully stops the actor. It is enqueued like any other message—the actor processes all messages already in the mailbox first, then shuts down. Use for clean, ordered shutdown. |

### Death watch

| Message        | Role                                                                                                                                                                                                                                                                     |
|----------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Terminated** | Sent to every actor that is **watching** another actor when that watched actor stops. Use it to clean up references, restart dependents, or trigger failover. Subscribe via `ctx.Watch(targetPID)`; cancel via `ctx.UnWatch(targetPID)`. See [Death Watch](death-watch). |

### Event stream (observability)

These events are published to the [event stream](../advanced/event-streams) for subscribers. You typically do not handle them in `Receive` unless you have a specific need:

| Message                       | When                                |
|-------------------------------|-------------------------------------|
| **ActorStarted**              | Actor has started                   |
| **ActorStopped**              | Actor has stopped                   |
| **ActorChildCreated**         | Child actor was spawned             |
| **ActorPassivated**           | Actor was passivated (idle timeout) |
| **ActorRestarted**            | Actor was restarted by supervisor   |
| **ActorSuspended**            | Actor was suspended after a failure |
| **ActorReinstated**           | Actor was reinstated by supervisor  |
| **NodeJoined** / **NodeLeft** | Cluster membership change           |

### Handling system messages

Add a case for the system messages you care about. For messages you don't handle, call `ctx.Unhandled()` so the
framework can apply default behavior (e.g., logging, event publishing).
