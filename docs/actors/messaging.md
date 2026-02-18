# Messaging

Messaging is the foundation of actor communication in GoAkt. Actors interact exclusively through asynchronous message passing using **protocol buffers** as the message format.

## Table of Contents

- 📦 [Introduction](#introduction)
- 📨 [Messaging Patterns](#messaging-patterns)
- ↩️ [Responding to Messages](#responding-to-messages)
- 📤 [Batch Messaging](#batch-messaging)
- 📋 [Message Context](#message-context)
- 🔀 [Actor-to-Actor Messaging](#actor-to-actor-messaging)
- 🌐 [Remote Messaging](#remote-messaging)
- ➡️ [Forwarding Messages](#forwarding-messages)
- ⏰ [Message Scheduling](#message-scheduling)
- 📬 [Request Pattern](#request-pattern)
- 🔗 [PipeTo Pattern](#pipeto-pattern)
- 📊 [Message Ordering Guarantees](#message-ordering-guarantees)
- ⚠️ [Error Handling](#error-handling)
- ✅ [Best Practices](#best-practices)

---

## Introduction

Communication between actors is exclusively message passing. GoAkt uses **protocol buffers** for messages (schema, serialization, versioning).

**Benefits:**
- **Type safety** — Compile-time type checking
- **Serialization** — Efficient wire format for remoting
- **Versioning** — Forward and backward compatibility
- **Interoperability** — Protobuf works across languages

Define messages in `.proto` files (proto3); generate Go with `protoc`. Messages are serialized for both local and remote use.

## Messaging Patterns

GoAkt provides two primary messaging patterns:

### 1. Tell (Fire-and-Forget)

**actor.Tell(ctx, pid, message)** — Sends asynchronously and returns immediately. Non-blocking; no response. Use for commands, notifications, events. Check the returned error (e.g. actor dead).

### 2. Ask (Request-Response)

**actor.Ask(ctx, pid, message, timeout)** — Sends and blocks until the actor replies or timeout. Returns `proto.Message` (type-assert) or an error.
- Receiver must call **ctx.Response(reply)** in `Receive` for the request to complete.
- Use for queries and request-reply. From inside another actor’s `Receive`, prefer [PipeTo](pipeto.md) or [Request pattern](#request-pattern) to avoid blocking.

## Responding to Messages

- **Ask** completes when the receiver calls **ctx.Response(reply)** once with a proto message; the caller gets it as the return value of `Ask`.
- You can call `Response` from a goroutine or after async work if you keep the `ReceiveContext`.
- Do not call `Response` twice for the same request.

## Batch Messaging

- **BatchTell** — Sends multiple messages in one call; the actor processes them one at a time in order.
- **BatchAsk** — Sends multiple requests and returns a channel of responses (same order as requests). Consume the channel until closed or timeout.

## Message Context

**ReceiveContext** provides:

- **Message()** — The current message being processed.
- **Sender()** — PID of the sender (may be nil, e.g. for system messages).
- **SenderAddress()** / **RemoteSender()** — Sender identity; use **RemoteSender()** when the sender is remote.
- **Context()** — Request context (cancellation, timeout).
- **Logger()** — Actor logger.

**Replying:** When the sender is present, use **ctx.Tell(ctx.Sender(), reply)** for fire-and-forget, or **ctx.Response(reply)** when handling an **Ask** so the caller receives the response.

## Actor-to-Actor Messaging

- **From inside Receive:** Use **ctx.Tell(pid, message)** and **ctx.Ask(pid, message, timeout)**. Store PIDs in actor state (e.g. from `PreStart` or lookups).
- **Ask:** Check for nil response on error; type-assert to your proto type.
- Prefer **Tell**; use **Ask** only when you need a reply in this handler. For non-blocking request-response, use [Request pattern](#request-pattern) or [PipeTo](pipeto.md).

## Remote Messaging

- **Resolve:** **ctx.RemoteLookup(host, port, actorName)** or **pid.RemoteLookup(ctx, host, port, actorName)** to get an address.
- **From inside Receive:** **ctx.RemoteTell(addr, message)** and **ctx.RemoteAsk(addr, message, timeout)**. `RemoteAsk` returns `*anypb.Any`; unmarshal to your type.
- **From outside an actor:** **pid.RemoteTell(ctx, addr, message)** (and corresponding Ask variants).

### SendSync and SendAsync

These methods send to an actor **by name**; the system resolves the target (local or remote). They are the **name-based** counterparts of **Tell** and **Ask**.

| Method                                    | Behavior                                                                                              | Use when                                                                   |
|-------------------------------------------|-------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------|
| **SendAsync**(actorName, message)         | Fire-and-forget; does not block or expect a reply.                                                    | Like Tell, but target is an actor **name** (e.g. in cluster).              |
| **SendSync**(actorName, message, timeout) | Blocks until response or timeout; returns response (or nil). From Receive, errors are in `ctx.Err()`. | Like Ask, but target is an actor **name**. Receiver uses `ctx.Response()`. |

- **From inside Receive:** `ctx.SendAsync(actorName, message)` (fire-and-forget; check `ctx.Err()` on failure) and `ctx.SendSync(actorName, message, timeout)` (blocks; check `ctx.Err()` and nil response).
- **From outside an actor:** `pid.SendAsync(ctx, actorName, message)` and `pid.SendSync(ctx, actorName, message, timeout)`.
- **See also:** [Tell and Ask](#1-tell-fire-and-forget), [Ask Errors](#ask-errors); [Request Pattern](#request-pattern) and [Reentrancy](reentrancy.md) for non-blocking request-response.

## Forwarding Messages

- **ctx.Forward(pid)** — Forwards the current message to another actor; original sender is preserved so the recipient sees the real sender.
- **ctx.ForwardTo(actorName)** — Forwards by actor name (cluster-aware).
- Use when routing or proxying messages.

## Message Scheduling

- **ScheduleOnce** — One-shot delivery after a delay.
- **ScheduleWithCron** — Recurring delivery by cron expression.
- See [Message Scheduling](message_scheduling.md) for details.

## Request Pattern

The **request pattern** provides **async request-response**: the sender sends a request and gets the response later via a **continuation** (callback), without blocking the actor’s mailbox. This allows the actor to keep processing other messages while the request is in flight.

**Requirements:**

- The **sender actor** must be spawned with **reentrancy enabled**. Otherwise `Request` / `RequestName` fail (return `nil` and set `ctx.Err()`). See [Reentrancy](reentrancy.md) for modes, configuration, and behavior.
- The **receiver** responds with `ctx.Response(...)` just as for Ask; the runtime routes that response back to the sender and invokes the sender’s continuation.

**When to use:**

- You need a **reply** from another actor but do not want to **block** (unlike Ask).
- You want the actor to handle **other messages** while waiting (e.g. multiple pending requests, or queries while a long-running request is outstanding).

### Request

Send a request to a PID and handle the response in a continuation via the returned `RequestCall`:

```go
func (a *Client) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *FetchData:
        call := ctx.Request(a.serverPID, &DataQuery{Id: msg.GetId()})
        if call == nil {
            // Request failed (e.g. reentrancy disabled); check ctx.Err()
            return
        }
        call.Then(func(response proto.Message, err error) {
            if err != nil {
                ctx.Logger().Error("Request failed", "error", err)
                return
            }
            dataResp := response.(*DataResponse)
            a.handleData(dataResp)
        })
        // Actor can process more messages; response is handled in Then()
    }
}
```

The receiver uses `ctx.Response()` as with Ask:

```go
case *DataQuery:
    ctx.Response(&DataResponse{Data: a.loadData(msg.GetId())})
```

### RequestName

Same as `Request`, but the target is identified by **actor name** (resolved at call time; works with cluster/remoting):

```go
call := ctx.RequestName("data-server", &DataQuery{Id: 123})
if call != nil {
    call.Then(func(response proto.Message, err error) {
        // handle response or err
    })
}
```

For reentrancy modes (AllowReentrant, StashNonReentrant), timeouts, and full examples, see **[Reentrancy](reentrancy.md)**.

## PipeTo Pattern

Execute async work and pipe the result back to an actor:

```go
func (a *Worker) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *HeavyTask:
        // Execute expensive operation in background
        ctx.PipeTo(a.resultHandler, func() (proto.Message, error) {
            // Long-running operation
            result := doExpensiveComputation(msg.GetData())
            return &TaskResult{Data: result}, nil
        })
    }
}
```

See [PipeTo Pattern](pipeto.md) for more details.

## Message Ordering Guarantees

GoAkt provides the following ordering guarantees:

### Per-Actor Ordering

Messages sent to the **same actor** are processed **in order**:

```go
// These messages will be processed in order
actor.Tell(ctx, pid, &Message1{})
actor.Tell(ctx, pid, &Message2{})
actor.Tell(ctx, pid, &Message3{})
// Actor receives: Message1 → Message2 → Message3
```

### No Global Ordering

Messages from **different senders** to the **same actor** have no guaranteed order:

```go
// From sender A
actor.Tell(ctx, pid, &MessageA{})

// From sender B (concurrent)
actor.Tell(ctx, pid, &MessageB{})

// Order is non-deterministic: could be A→B or B→A
```

### Point-to-Point Ordering

Messages between **two specific actors** maintain order:

```go
// In ActorA
ctx.Tell(actorB, &Msg1{})
ctx.Tell(actorB, &Msg2{})
// ActorB receives in order: Msg1 → Msg2
```

## Error Handling

- **Tell** can return **ErrDead** (actor not running), mailbox full (bounded), or system shutting down. **Ask** can return **ErrRequestTimeout** or **ErrDead**.
- Use **errors.Is(err, actor.ErrDead)** and **errors.Is(err, actor.ErrRequestTimeout)** to detect and handle.
- **From inside Receive:** `ctx.Ask` returns nil on error; check **ctx.Err()** for the cause.

## Best Practices

### Message Design

✅ **Do:**
- Use immutable protocol buffer messages
- Keep messages small and focused
- Include all necessary data in message
- Version your messages properly
- Use specific message types

❌ **Don't:**
- Share mutable state via messages
- Send large payloads (consider batching)
- Reuse message instances
- Use generic "Any" messages

### Messaging Patterns

✅ **Do:**
- Use Tell for fire-and-forget
- Use Ask for queries
- Set reasonable timeouts for Ask
- Handle errors from messaging calls
- Use Request/PipeTo for async patterns

❌ **Don't:**
- Block indefinitely with Ask
- Ignore errors from Tell
- Create request-response loops
- Use Ask in hot paths (prefer Tell)

### Performance Tips

1. **Prefer Tell over Ask**: Tell is faster and doesn't block
2. **Batch when possible**: Use BatchTell for multiple messages
3. **Set appropriate timeouts**: Don't use excessive timeout values
4. **Use bounded mailboxes**: Prevent memory issues
5. **Monitor dead letters**: Track undeliverable messages