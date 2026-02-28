---
title: Location Transparency
description: Interact with actors without knowing their physical location.
sidebarTitle: "🌐 Location Transparency"
---

## Concept

You interact with actors through a `*PID`. You don't need to know whether the actor is local (same process) or remote (
another node). The same Tell/Ask APIs work in both cases.

## How it works

- **Local** — Message is enqueued directly into the actor's mailbox.
- **Remote** — Message is serialized, sent over TCP, deserialized on the target node, and enqueued there.

The caller uses the same `pid.Tell(msg)` or `pid.Ask(ctx, msg, timeout)` regardless of location.

## The Path interface

`pid.Path()` returns a `Path` interface—the location-transparent actor identity:

```go
type Path interface {
    Host() string
    HostPort() string
    Port() int
    Name() string
    Parent() Path
    String() string
    System() string
    Equals(other Path) bool
}
```

Use `Path()` for host, port, name, and string representation. Guard for nil when the sender is unknown (e.g.,
`NoSender`).

## When location matters

Use `pid.IsLocal()` or `pid.IsRemote()` when you need location-specific behavior (e.g., avoiding remote calls for hot
paths, or logging).

## ActorOf

`ActorOf(ctx, name)` returns a `*PID` whether the actor is local or remote. The framework looks up the actor in the
local tree first, then in the cluster registry. You get a unified handle either way.
