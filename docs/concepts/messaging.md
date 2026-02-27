# Messaging

## Tell vs Ask

| Pattern  | Use case         | Response             |
|----------|------------------|----------------------|
| **Tell** | Fire-and-forget  | None                 |
| **Ask**  | Request-response | Future you can await |

Use **Tell** when you don't need a reply—logging, notifications, commands that trigger side effects. Use **Ask** when
you need a result—queries, computations, or any flow that depends on the response.

## Message types (v4)

From v4.0.0, all message-passing APIs accept `any`. You can send:

- Plain Go structs (with CBOR serialization when remote)
- Protocol buffer messages (default ProtoSerializer)
- Any type registered with the serializer

The framework handles serialization when the target is on another node. Locally, messages are passed by reference
without serialization.

## Message ordering

Messages between a specific sender-receiver pair are delivered in the order they were sent (FIFO). This follows from the
mailbox being a FIFO queue and the single-threaded processing guarantee per actor.

## Sender context

In `Receive`, you can access the sender via `ctx.Sender()`. It returns a `*PID` or `nil` (e.g., for scheduled messages
or when the sender is unknown). Use `ctx.Sender().Path()` when you need the sender's address for a reply.

## ReceiveContext messaging methods

Inside `Receive`, the `ReceiveContext` provides these messaging operations:

| Method                                 | Purpose                                                                               |
|----------------------------------------|---------------------------------------------------------------------------------------|
| `Tell(to *PID, message any)`           | Fire-and-forget. Does not block.                                                      |
| `Ask(to *PID, message any, timeout)`   | Request-response. Blocks until reply or timeout.                                      |
| `Response(resp any)`                   | Reply to an Ask. Call exactly once per Ask message.                                   |
| `Request(to, message, opts...)`        | Non-blocking Ask; use continuations for the reply.                                    |
| `PipeTo(to, task, opts...)`            | Run task asynchronously; deliver result to `to`. See [PipeTo](../advanced/pipeto.md). |
| `PipeToName(actorName, task, opts...)` | Same, target by name.                                                                 |

Use `Sender()` to get the sender's PID when replying. Use `ActorSystem().ActorOf(ctx, name)` to resolve an actor by name
before sending.

## No shared state

Actors never share memory. All communication is through immutable (or effectively immutable) messages. This eliminates
whole classes of concurrency bugs and makes reasoning about behavior easier.
