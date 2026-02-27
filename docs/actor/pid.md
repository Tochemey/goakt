# PID (Process Identifier)

A **PID** is the sole actor reference in GoAkt. It is a live handle to a running actor—either local (same process) or remote (another node). All messaging goes through a PID.

## Role

| Aspect                   | Description                                                                    |
|--------------------------|--------------------------------------------------------------------------------|
| **Unified reference**    | One type for local and remote actors. No separate `ActorRef`.                  |
| **Location-transparent** | Use `Tell` and `Ask` the same way regardless of location.                      |
| **Identity**             | `Path()` returns the canonical address. `Name()`, `Kind()` identify the actor. |

## Local vs Remote

| Type           | Description                                                                                                                      |
|----------------|----------------------------------------------------------------------------------------------------------------------------------|
| **Local PID**  | Live mailbox, supervision state, full actor-system reference, etc.... Created by `Spawn`.                                        |
| **Remote PID** | Lightweight handle with address and remoting; messaging via remoting layer. Returned by `ActorOf` when actor is on another node. |

Use `pid.IsLocal()` or `pid.IsRemote()` when location matters. `pid.ActorSystem()` returns `nil` for remote PIDs—guard before use.

## Key methods

| Method                           | Purpose                                                                        |
|----------------------------------|--------------------------------------------------------------------------------|
| `Path()`                         | Canonical actor identity (host, port, name, system). Returns `Path` interface. |
| `Name()`                         | Actor name within its parent.                                                  |
| `Kind()`                         | Reflected type name of the actor. Empty for remote PIDs.                       |
| `Tell(ctx, to, message)`         | Fire-and-forget to another PID.                                                |
| `Ask(ctx, to, message, timeout)` | Request-response. Blocks until reply or timeout.                               |
| `IsRunning()`                    | Actor is active and processing.                                                |
| `IsSuspended()`                  | Actor suspended by supervisor.                                                 |
| `IsStopping()`                   | Shutdown in progress.                                                          |
| `Watch(cid)`                     | Receive `Terminated` when `cid` stops. Local only.                             |
| `UnWatch(cid)`                   | Stop watching.                                                                 |

## Obtaining a PID

- **Spawn** — `system.Spawn(ctx, name, actor)` returns a local PID.
- **ActorOf** — `system.ActorOf(ctx, name)` returns local or remote PID.
- **Context** — `ctx.Self()` (current actor), `ctx.Sender()` (message sender).

## NoSender

`system.NoSender()` returns a special PID for messages without a sender (e.g., scheduled messages, external clients). Use it when sending from outside the actor system. `NoSender().Path()` may be used for wire encoding.
