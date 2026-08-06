# Point-to-Point Reliable Delivery

> Confirmed, ordered, flow-controlled message transfer between one producer actor and one consumer actor, layered above GoAkt's at-most-once transport.

---

## Table of Contents

1. [Overview](#1-overview)
2. [Design Principles](#2-design-principles)
3. [Delivery Semantics](#3-delivery-semantics)
4. [Components](#4-components)
5. [Public API](#5-public-api)
6. [Wire Protocol](#6-wire-protocol)
7. [The Delivery Handshake](#7-the-delivery-handshake)
8. [Sessions and Restart Resync](#8-sessions-and-restart-resync)
9. [Flow Control and Confirmation Batching](#9-flow-control-and-confirmation-batching)
10. [Timers and Liveness](#10-timers-and-liveness)
11. [ProducerController State Machine](#11-producercontroller-state-machine)
12. [ConsumerController State Machine](#12-consumercontroller-state-machine)
13. [The Durable Producer Queue](#13-the-durable-producer-queue)
14. [Companion Spawning and Identity](#14-companion-spawning-and-identity)
15. [Cluster Publication and Resolution](#15-cluster-publication-and-resolution)
16. [Relocation and Reconstruction](#16-relocation-and-reconstruction)
17. [Failure Classification](#17-failure-classification)
18. [Configuration Defaults](#18-configuration-defaults)
19. [Limitations](#19-limitations)

---

## 1. Overview

Ordinary GoAkt messaging is at-most-once: a `Tell` that races a crash, a full bounded mailbox, or a network fault is silently lost. Reliable delivery adds a **confirmed, ordered, flow-controlled flow** between exactly two user actors, a producer and a consumer, without changing the transport underneath.

The mechanism is a pair of unexported controller actors that the actor system spawns and manages next to the user's own actors:

- a **producerController** attached to the producer endpoint, and
- a **consumerController** attached to the consumer endpoint.

Users enable the feature with one spawn option per side (`AsReliableProducer`, `AsReliableConsumer`). The controllers sequence messages, grant demand, resend after loss, deduplicate after restart, and optionally persist producer state in a pluggable `DurableProducerQueue`. Application work still enters the producer through ordinary `Tell`; nothing outside the two endpoints knows the flow exists.

```
           Producer node                                          Consumer node
┌─────────────────────────────────┐                    ┌─────────────────────────────────┐
│  producer actor (user code)     │                    │  consumer actor (user code)     │
│      │                 ▲        │                    │      ▲                 │        │
│   Produced        RequestNext   │                    │   Delivery        Confirmed     │
│   StoredAck       Stored        │                    │      │                 │        │
│      ▼                 │        │                    │      │                 ▼        │
│  ┌───────────────────────────┐  │                    │  ┌───────────────────────────┐  │
│  │    producerController     │──┼──SequencedMessage──┼─►│    consumerController     │  │
│  │    (system companion)     │◄─┼──RegisterConsumer──┼──│    (system companion)     │  │
│  └─────────────┬─────────────┘  │    Request, Ack    │  └───────────────────────────┘  │
│                ▼                │                    │                                 │
│       DurableProducerQueue      │                    │                                 │
│       (optional, pluggable)     │                    │                                 │
└─────────────────────────────────┘                    └─────────────────────────────────┘
```

The feature lives entirely inside the `actor` package, with wire types in `internal/commands` and `protos/internal/delivery.proto`. The dispatcher, mailboxes, `Tell`, and the remoting frame format are untouched.

---

## 2. Design Principles

**Transport stays at-most-once.** No sequence or acknowledgement fields were added to [`RemoteMessage`](../protos/internal/remoting.proto), and [`remote_server.go`](../actor/remote_server.go) is unchanged. Reliability is a controller protocol layered above `Tell` and remoting, so every existing send path keeps its cost and semantics.

**Controllers are invisible infrastructure.** The controllers are unexported, carry reserved `GoAkt`-prefixed names scoped to one endpoint incarnation, and are excluded from `Actors`, `ActorOf`, `Kill`, `ReSpawn`, relocation candidates, and every other public actor-management API. The public surface is two spawn options, per-side option types, six local protocol messages, the durable-queue contract, one failure event, and a handful of error sentinels.

**Demand is consumer-driven.** The producer controller never sends a sequenced message beyond the demand the consumer controller has granted. This bounds the consumer-side buffer and prevents a fast producer from overwhelming a slow consumer's mailbox.

**Confirmation is business-level.** The consumer sends `Confirmed` only after processing a `Delivery`, not when the message is enqueued. A message is resent until confirmed; it is never dropped, skipped, or marked successful without confirmation.

**MessageID is the deduplication identity.** The producer generates a `MessageID` once, when work enters its pending buffer, and preserves it across retries, controller restarts, and durable recovery. `Seq` orders messages within one sequencing history; `MessageID` identifies the same business message across histories. A consumer that needs exactly-once effects deduplicates on `MessageID` and commits it together with the business mutation.

**Immutable value objects.** Every protocol, queue-state, and event struct introduced by this feature has unexported fields, validating constructors, value-receiver accessors, no setters, and defensive copies at each mutable boundary. Serialized forms exist only at the wire boundary and are converted to immutable values immediately.

**Tell-only.** Every hop inside the machinery is a `Tell`. The controllers never `Ask`, so nothing blocks on a synchronous reply or inherits request-response timeout semantics.

---

## 3. Delivery Semantics

| Condition                                              | Guarantee                                                                                                          |
|--------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------|
| Neither side crashes; controllers connected            | Effectively-once, in-order, within the consumer-driven window                                                      |
| Resend after loss or consumerController restart        | At-least-once (the consumer handler must be idempotent)                                                            |
| producerController restart without a durable queue     | Controller-unconfirmed messages are lost; the consumer resumes cleanly on the new session                          |
| producerController restart with a `DurableProducerQueue` | Stored messages reload and redeliver; unacknowledged producer items are retained and resubmitted (at-least-once) |

Effectively-once is a no-fault-path property: while neither controller nor endpoint restarts and no `Delivery` or `Confirmed` is lost, each sequence number is presented to the consumer once, in order. Any lost message, controller restart, or relocation permits redelivery and degrades to at-least-once processing. The framework never claims exactly-once business effects; those require the consumer to commit `MessageID` and the business mutation in one transaction.

The reliability guarantee begins at the producer's handoff to its controller, not at the producer's inbox. The hop into the producer is ordinary at-most-once messaging. A producer that needs ingress reliability feeds itself from its own durable source and removes items only at the acceptance boundary described in [section 13](#13-the-durable-producer-queue).

---

## 4. Components

| File                                                                                       | Responsibility                                                                                                     |
|--------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------|
| [`actor/delivery_protocol.go`](../actor/delivery_protocol.go)                              | Public protocol values (`RequestNext`, `Produced`, `Stored`, `StoredAck`, `Delivery`, `Confirmed`), `ReliablePayload`, queue value types, `ReliableDeliveryFailed`, role/stage enums, `MaxFlowControlWindow`. |
| [`actor/delivery_producer_controller.go`](../actor/delivery_producer_controller.go)        | producerController actor: registration fencing, credit loop, durable-operation lane, resend.                       |
| [`actor/delivery_consumer_controller.go`](../actor/delivery_consumer_controller.go)        | consumerController actor: registration, session adoption, receive buffer, confirmation batching.                   |
| [`actor/delivery_durable_queue.go`](../actor/delivery_durable_queue.go)                    | `DurableProducerQueue` contract and `QueueEpoch` fencing.                                                          |
| [`actor/delivery_options.go`](../actor/delivery_options.go)                                | `AsReliableProducer` / `AsReliableConsumer` spawn options and the per-side option types.                           |
| [`actor/delivery_config.go`](../actor/delivery_config.go)                                  | In-memory reliable-delivery configuration and its wire round trip.                                                 |
| [`actor/delivery_companion.go`](../actor/delivery_companion.go)                            | Companion identity derivation, local-first resolution, ownership validation, spawn transaction, relocation cleanup.|
| [`actor/reserved.go`](../actor/reserved.go)                                                | Reserved controller name prefixes.                                                                                 |
| [`actor/defaults.go`](../actor/defaults.go)                                                | Flow-control, resend, retry, and lookup-timeout defaults.                                                          |
| [`internal/commands/delivery.go`](../internal/commands/delivery.go)                        | Cross-node commands: `RegisterConsumer`, `RegistrationAck`, `Request`, `Ack`, `SequencedMessage`.                  |
| [`internal/commands/delivery_serializer.go`](../internal/commands/delivery_serializer.go)  | Payload-aware protocol serializer, auto-registered by the actor system.                                            |
| [`protos/internal/delivery.proto`](../protos/internal/delivery.proto)                      | Stable wire form for the delivery control fields and nested serialized payload bytes.                              |
| [`protos/internal/actor.proto`](../protos/internal/actor.proto)                            | `Actor.reliable_delivery` (endpoint configuration) and `Actor.reliable_companion` (controller ownership record).   |
| [`remote/reliable_delivery.go`](../remote/reliable_delivery.go)                            | `remote.ReliableDeliverySpec` carried by `remote.SpawnRequest` for remote endpoint placement.                      |
| [`errors/errors.go`](../errors/errors.go)                                                  | `ErrQueueFenced`, `ErrQueueConflict`, `ErrReliableStore`, `ErrReliableAccept`, `ErrReliableConfirm`, `ErrReliableSpawnUnsupported`. |

---

## 5. Public API

### 5.1 Spawn options

Reliable delivery attaches to the user's own actors as native spawn options on the existing `Spawn` call. Each side names its peer's user-visible actor name; there are no controller names or handles anywhere in user code.

```go
func AsReliableProducer(consumerName string, opts ...ReliableProducerOption) SpawnOption
func AsReliableConsumer(producerName string, opts ...ReliableConsumerOption) SpawnOption
```

Producer options: `WithDurableQueue`, `WithQueueRetry`, `WithLocalRetryInterval`. Consumer options: `WithFlowControlWindow`, `WithResendInterval`. Both options reject finite passivation; endpoints and controllers are long-lived.

### 5.2 The producer contract

The producer buffers incoming work, hands over one message per granted credit, and keeps the pending head until the controller acknowledges storage:

- `RequestNext` grants one tokenized send permission. The producer answers with `Produced`, built through `NewProduced(request, messageID, payload)`.
- `Stored` acknowledges the submitted message. The producer removes the pending head and replies with `NewStoredAck(stored)`.
- Duplicate `RequestNext` or `Stored` deliveries must be answered idempotently: resend the same `Produced`, re-ack the same `Stored`.
- `RequestNext` and `Stored` must be honored only from the flow's own controller. `RequestNext.IsAuthorizedFor(ctx.Self(), ctx.Sender())` and the equivalent on `Stored` and `Delivery` validate the bound endpoint and companion PIDs, so a spoofed message can be rejected without knowing any internal name.

### 5.3 The consumer contract

The consumer processes `Delivery` and confirms afterward:

```go
case *actor.Delivery:
    if !msg.IsAuthorizedFor(ctx.Self(), ctx.Sender()) {
        return
    }

    order := msg.Payload().(*Order)
    c.processIdempotently(msg.MessageID(), order)

    confirmed, err := actor.NewConfirmed(msg)
    if err != nil {
        ctx.Err(err)
        return
    }

    ctx.Tell(ctx.Sender(), confirmed)
```

Processing must be idempotent because redelivery is possible after any loss or restart. All other message types on both endpoints stay free for business use; being a reliable endpoint adds no restrictions on who may message the actor.

### 5.4 Assembly

Single node:

```go
producer, _ := system.Spawn(ctx, "orders-producer", &OrdersProducer{},
    actor.AsReliableProducer("orders-consumer"),
)

_, _ = system.Spawn(ctx, "orders-consumer", &OrdersConsumer{},
    actor.AsReliableConsumer("orders-producer",
        actor.WithFlowControlWindow(50),
    ),
)

_ = actor.Tell(ctx, producer, &Order{ID: "o-1"})
```

Across nodes, both actor systems must join the same GoAkt cluster; controller discovery and endpoint relocation use the cluster registry, so remoting without clustering is insufficient. Each node registers the payload types and, for a relocatable custom durable queue, the queue dependency type. The protocol's own serializers are registered by the actor system automatically.

Proto payloads use the default serializer and need no configuration. Non-Proto payloads must be registered through `remote.WithSerializables`, which in the current API also enables remoting even for a local-only flow. This limitation is documented rather than worked around.

### 5.5 Ask at the edges

The flow carries one-way transfers. `Ask` still works at its boundaries with bounded meaning: a caller may `Ask` the producer, but the producer can only answer from local knowledge ("accepted into my buffer"), never "delivered". The consumer cannot reply to the original submitter through the flow, because `Delivery`'s sender is the consumerController. A reply path needs app-level correlation in the payload or a second flow in the opposite direction.

---

## 6. Wire Protocol

### 6.1 Message inventory

Cross-node messages travel as internal commands whose serializers the actor system registers itself when remoting starts, exactly like `AsyncRequest`:

| Message                                                            | Direction | Purpose                                                        |
|--------------------------------------------------------------------|-----------|----------------------------------------------------------------|
| `RegisterConsumer(Nonce)`                                          | CC to PC  | Announce or re-announce the consumer; the CC is the sender.    |
| `RegistrationAck(SessionID, NextSeq, Nonce)`                       | PC to CC  | Adopt or reassert the session for the echoed nonce.            |
| `Request(SessionID, Nonce, ConfirmedSeq, RequestUpToSeq, ViaTimeout)` | CC to PC | Grant demand, confirm cumulatively, optionally request resend. |
| `Ack(SessionID, Nonce, ConfirmedSeq)`                              | CC to PC  | Cumulative confirmation without new demand.                    |
| `SequencedMessage(SessionID, MessageID, Seq, ReliablePayload)`     | PC to CC  | One sequenced application message.                             |

Local-only messages never cross nodes and target user actors:

| Message                                     | Direction        | Purpose                                          |
|---------------------------------------------|------------------|--------------------------------------------------|
| `RequestNext(SessionID, Token)`             | PC to producer   | Grant one send credit.                           |
| `Produced(SessionID, Token, MessageID, Payload)` | producer to PC | Hand over one message for the outstanding token. |
| `Stored(SessionID, Token, MessageID, Seq)`  | PC to producer   | Acknowledge storage and sequence assignment.     |
| `StoredAck(SessionID, Token, MessageID)`    | producer to PC   | Producer completed its retention handoff.        |
| `Delivery(SessionID, MessageID, Seq, Payload)` | CC to consumer | Present one message for processing.              |
| `Confirmed(SessionID, MessageID, Seq)`      | consumer to CC   | Business-level confirmation.                     |

There is no separate resend message: resend is requested through `Request{ViaTimeout: true}`.

### 6.2 Payload encoding

Every application message is encoded into an immutable `ReliablePayload` before sequence assignment or storage, including on local-only flows. Encoding uses the serializer returned by the existing remoting serializer lookup, so reliable payloads follow the same dispatch rules as ordinary remote messages. The codec snapshots the serializer output once; the frame is self-describing, so no separate manifest is stored. Decoding produces a fresh value for every `Delivery`.

`ReliablePayload` is exported because the durable-queue contract stores it, but applications normally never construct one. It encapsulates its bytes; `Bytes()` is the single mutable boundary and returns a clone. Copying the value does not copy the frame.

A missing serializer or a payload decode failure is terminal (`ReliableDeliveryStageProtocol`): both are deterministic, so they signal a serializer registration asymmetry that neither retry nor restart can repair.

---

## 7. The Delivery Handshake

The full path of one message through a durable flow:

```
 producer            producerController                     consumerController           consumer
    │                        │                                      │                       │
    │                        │◄────── RegisterConsumer(nonce) ──────┤                       │
    │                        ├─── RegistrationAck(s1,next,nonce) ──►│                       │
    │                        │◄─ Request(s1,nonce,0,50,viaTimeout) ─┤                       │
    │◄── RequestNext(s1,t1) ─┤                                      │                       │
    ├── Produced(s1,t1,m1) ─►│                                      │                       │
    │                        │ encode payload, Store(m1) → seq 1    │                       │
    │◄── Stored(s1,t1,m1,1) ─┤                                      │                       │
    ├─ StoredAck(s1,t1,m1) ─►│                                      │                       │
    │                        │ Accept(m1)                           │                       │
    │                        ├───── SequencedMessage(s1,m1,1) ─────►│                       │
    │                        │                                      ├── Delivery(s1,m1,1) ─►│
    │                        │                                      │◄─ Confirmed(s1,m1,1) ─┤
    │                        │◄────────── Ack(s1,nonce,1) ──────────┤                       │
    │◄── RequestNext(s1,t2) ─┤                                      │                       │
```

The `Stored` / `StoredAck` exchange is the producer's retention handoff: an in-memory producer removes its pending head on `Stored`; a producer backed by a recoverable source durably marks or removes the item before sending `StoredAck`. Only after `StoredAck` does the controller record durable acceptance and emit the sequenced message. Without a durable queue, storage and acceptance complete synchronously in the same logical order.

---

## 8. Sessions and Restart Resync

Every restart permutation reduces to the rules in this section.

### 8.1 Session identity

Each producerController incarnation generates a random `SessionID`. With a durable queue, `Load` restores the sequence and confirmation state; without one, sequencing starts from zero. `SessionID` is never persisted. Every CC-to-PC message must match the current `SessionID` and registration nonce.

### 8.2 Adoption rules on the consumer controller

Session adoption happens only through `RegistrationAck`, gated by a registration nonce. Session IDs are unordered UUIDs, so a delayed ack from a dead incarnation must not be able to roll the consumer onto a stale session. The CC generates a fresh nonce for every `RegisterConsumer` and remembers only the latest; the PC echoes it.

1. A `RegistrationAck` whose nonce is not the CC's latest is dropped. This check runs before everything else.
2. A nonce-valid `RegistrationAck` with a different `SessionID` (or when no session exists) is adopted: set `expectedSeq = NextSeq`, clear the receive buffer, discard in-flight tracking, send an initial timeout `Request`.
3. A nonce-valid `RegistrationAck` with the current `SessionID` reasserts demand for the fresh nonce. Treating it as a no-op would leave producer-side demand reset to zero.
4. Any `SequencedMessage` whose `SessionID` differs from the current one is dropped.

### 8.3 Registration rules on the producer controller

1. The PC is constructed with the expected consumer endpoint name. Before accepting `RegisterConsumer`, it resolves that endpoint's current incarnation-scoped consumerController and requires it to equal the sender. Resolution runs under `DefaultRegistrationLookupTimeout` so a slow registry cannot stall the controller mailbox. A timeout or a mixed endpoint/companion pair drops the registration; the CC retries. This rejects both an unrelated live consumer and a delayed pre-relocation controller.
2. A duplicate from the same PID and nonce is an idempotent ping. A verified new PID or nonce starts a new registration generation: overwrite the CC reference, reset demand, retain unconfirmed data, and reply `RegistrationAck{SessionID, NextSeq: confirmedSeq + 1, Nonce}`.
3. `Request` and `Ack` are accepted only from the registered CC with a matching session and nonce.
4. `Produced` is accepted only from the bound local producer. Old-session messages are dropped; a current-session token or `MessageID` violation is a terminal protocol failure.

### 8.4 Restart permutations

| Scenario                                | Behavior                                                                                                                                                          |
|-----------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| PC restarts, durable queue present      | `Load` restores state under a new session. The CC's tick re-registers, adopts the new session at `confirmedSeq + 1`, and requests with `ViaTimeout`; unconfirmed messages are resent. Messages processed but never durably confirmed are redelivered: at-least-once. |
| PC restarts between `Store` and `StoredAck` | The producer resubmits the same `MessageID`. Durable `Store` returns that ID's original sequence and first-write payload, so one message never acquires two durable sequence numbers. |
| PC restarts, no durable queue           | Same handshake with `NextSeq = 1`; controller-unconfirmed messages are lost. An item that never received `Stored` is retained by the producer and resubmitted.       |
| CC restarts                             | The fresh CC re-resolves the producer's current controller, registers with a new nonce, adopts the unchanged session, and requests with `ViaTimeout`; the in-flight unconfirmed delivery is delivered again. |
| Both restart                            | Composition of the above; the CC-side adoption rule handles it with nothing extra.                                                                                   |
| Node loss / relocation                  | See [section 16](#16-relocation-and-reconstruction).                                                                                                                 |

---

## 9. Flow Control and Confirmation Batching

Demand is granted only by a `Request` carrying the current registration nonce. `RequestUpToSeq` is the highest sequence the producer controller may send; its credit is `RequestUpToSeq - currentSeq`. Registration itself grants no demand. After adopting a session the CC sends `Request{ConfirmedSeq: expectedSeq - 1, RequestUpToSeq: expectedSeq - 1 + window, ViaTimeout: true}`.

Confirmations are batched; there is no per-message remote acknowledgement. On each `Confirmed` from the consumer, the CC applies exactly one of, in order:

1. If `RequestUpToSeq - confirmedSeq <= window/2`: send a top-up `Request{ConfirmedSeq, RequestUpToSeq: confirmedSeq + window, ViaTimeout: false}`. The top-up carries the confirmation.
2. Else if the receive buffer is empty and no delivery is in flight (the stream is drained): send `Ack{ConfirmedSeq}` immediately, so an idle producer controller does not sit on unconfirmed state.
3. Else: defer; a later `Confirmed` hits rule 1 or 2.

With the default window of 50, a 30-message burst produces a top-up `Request` at sequence 25 and an idle `Ack` at 30; a single message produces one immediate `Ack`; a saturated stream settles into one top-up `Request` every `window/2` confirmations, each carrying the confirmation.

**Resend is consumer-driven with one rule**: the PC resends unconfirmed messages in `[ConfirmedSeq + 1, min(currentSeq, RequestUpToSeq)]`, in sequence order, if and only if `Request.ViaTimeout == true`. Ordinary top-up requests never trigger resend, because those messages are usually already queued at the consumer side.

On a valid `Request` or `Ack`, the PC advances `confirmedSeq` and drops the volatile prefix. Durable confirmation is watermark-coalesced: values at or below the persisted watermark are ignored, and while a `Confirm` is pending only the highest dirty watermark is retained. Duplicate or idle control traffic cannot grow the queue-operation lane.

**Range validation.** `MaxFlowControlWindow` is 10,000. The PC checks `0 <= ConfirmedSeq <= currentSeq` and `ConfirmedSeq <= RequestUpToSeq <= ConfirmedSeq + MaxFlowControlWindow` with checked arithmetic. `WithFlowControlWindow` must be in `[1, 10_000]`. After sender, flow, and session validation, the CC re-acks `1 <= Seq < expectedSeq` as a duplicate, accepts `expectedSeq <= Seq <= requestUpToSeq`, and drops everything else.

**Producer-side invariant**: the PC never sends a `SequencedMessage` with `seq > demandUpTo`. This is what keeps the consumer's buffer bounded regardless of producer speed.

---

## 10. Timers and Liveness

Watch and `Terminated` are a fast path only: remote watch registration can fail silently, so the protocol never depends on it. The recovery of record is a pair of generation-fenced recurring timers, one per controller, created on `PostStart` and cancelled on `PostStop`. Stale queued ticks from a previous generation are ignored after a restart.

On each consumer-controller tick, exactly one of:

1. If there is no session yet, or no valid producer-controller message arrived since the previous tick: re-resolve the producer endpoint's current controller and send `RegisterConsumer` with a fresh nonce. Resolution or send failures retry on the next tick. This one rule covers lost registration, producer-controller restart, endpoint respawn on another node, and idle liveness.
2. Else if a `Delivery` is in flight and unconfirmed: re-`Tell` the same `Delivery` to the consumer. This recovers a bounded-mailbox drop or a lost `Confirmed` and intentionally permits duplicate business processing.
3. Else if a gap is open: send `Request{ViaTimeout: true}`.

Gap fast path: when a `SequencedMessage` arrives above `expectedSeq` and the buffered head is not the next undeliverable sequence, the CC buffers it and sends `Request{ViaTimeout: true}` immediately, rate-limited to one gap-triggered request per resend interval. Contiguous pipelined arrivals during an in-flight delivery do not trigger resend requests.

On each producer-controller local-retry tick, exactly one of:

1. If `RequestNext` is outstanding: resend it with the same session and token. The producer treats duplicate tokens idempotently and resends the same `Produced`.
2. If `Stored` awaits `StoredAck`: resend the same `Stored`. The producer re-acks duplicates without popping another item.
3. Otherwise do nothing.

Bounded user mailboxes are part of the fault model: controller mailboxes are forced unbounded, but `RequestNext`, `Stored`, and `Delivery` target user actors and therefore rely on these protocol retries.

---

## 11. ProducerController State Machine

The producerController is spawned on the producer's node by the companion machinery; the producer PID is bound at construction and must be local. Its state:

- `sessionID`, `currentSeq`, `confirmedSeq`
- an unconfirmed buffer ordered by ascending contiguous sequence (append on successful store, pop-front on confirmation); a slice rather than a map, because resend needs ordered iteration
- `demandUpTo` from the latest `Request`, the CC reference, and the current registration nonce
- at most one local handshake: an outstanding `RequestNext` or a `Stored` awaiting `StoredAck`
- an operation counter and one serialized durable-operation lane with a coalesced confirmation watermark
- the optional `DurableProducerQueue`

Behavior:

1. `PreStart` resets incarnation state and generates the session. With a queue, `Load` restores sequence and confirmation state and validates unique `MessageID`s, immutable payloads, and contiguous sequences.
2. `PostStart` watches the producer and creates the generation-fenced retry timer.
3. Credit discipline: at most one local handshake exists at a time. `RequestNext` is issued only with demand available and no store/accept handshake open, and the next credit is not issued until acceptance completes.
4. On the first matching `Produced`, the controller requires a non-empty `MessageID`, encodes the payload once, caches the `{token, MessageID, ReliablePayload}` triple, releases the application payload reference, and enqueues `Store`. An exact duplicate while the store is pending is ignored without re-encoding; a changed `MessageID` for the same token is a protocol violation.
5. Durable operations run as `PipeTo` tasks whose retry backoff sleeps in the task goroutine. Results return as data messages fenced by session and a checked monotonic operation ID; only the pending tuple is accepted.
6. `Store` returns the assigned sequence and the authoritative first-write payload. A new `MessageID` appends at `currentSeq + 1`; an existing one reuses its original sequence and payload even if this incarnation's serializer emitted different bytes. The controller commits volatile state only for a new append, sends `Stored`, and waits for `StoredAck` before anything else.
7. On a matching `StoredAck`, the controller enqueues durable `Accept`. On acceptance it clears the handshake, emits `SequencedMessage` with the store-result payload (asserting `seq <= demandUpTo`), and re-evaluates credit. A crash between acceptance and emission is safe: the unconfirmed queue entry survives, and the next timeout `Request` resends it.
8. On `Confirm` success it advances the persisted watermark and enqueues one higher dirty watermark if any exists.
9. On CC `Terminated` it clears registration and demand while retaining durable and handshake state. On producer `Terminated` it stops itself. `PostStop` cancels the timer.

Fencing and conflict errors from the queue are terminal. Other queue failures escalate for a supervised restart that reloads authoritative queue state. Current-session sender, token, or `MessageID` violations are terminal protocol failures that stop only the controller.

---

## 12. ConsumerController State Machine

The consumerController is spawned on the consumer's node; the consumer PID is bound at construction and must be local. Its state:

- the current `sessionID` (empty until first adoption), `expectedSeq`, and `confirmedSeq`
- a single sequence-ordered receive buffer with capacity equal to the window, holding messages that arrived above `expectedSeq` or while a delivery is in flight; one buffer, one drain path, no stashing
- at most one in-flight unconfirmed `Delivery`
- `requestUpToSeq` and tick bookkeeping

The consumer PID and the producer endpoint name are constructor state and survive restarts. The resolved producer controller, registration nonce, timer generation, and all delivery state reset in `PreStart`.

Behavior:

1. `PreStart` resets session state and the timer generation; it performs no PID operations.
2. `PostStart` watches the consumer, attempts resolution and registration (failure is tolerated; the tick retries), and creates the recurring generation-bearing tick. The controller also watches the resolved producer controller on registration as a liveness fast path; the tick's silence rule remains the recovery of record.
3. `RegistrationAck` applies the nonce and session rules from [section 8](#8-sessions-and-restart-resync).
4. `SequencedMessage` handling:
   - `seq < expectedSeq`: duplicate; re-send `Ack` with the current session, nonce, and `confirmedSeq`.
   - `seq == expectedSeq` with no in-flight delivery: `Tell` the consumer a `Delivery` and mark it in flight.
   - `seq` equal to the in-flight sequence: drop; it was already handed to the consumer.
   - Otherwise insert into the deduplicated receive buffer. If the buffer is full, keep the existing lower sequences, drop the arriving one without advancing `expectedSeq`, and open the rate-limited gap request; the producer controller still holds the unconfirmed message and timeout resend recovers it.
5. `Confirmed` from the bound consumer must match the in-flight `MessageID`, `SessionID`, and `Seq`; then the controller advances, purges the buffer below `expectedSeq`, applies the confirmation batching rules, and drains the next deliverable message.
6. On consumer `Terminated` it stops itself. On producer-controller `Terminated` it clears the resolved PID, session, and registration, and keeps ticking. `PostStop` cancels the schedule.

A payload decode failure is terminal (`ReliableDeliveryStageProtocol`): decoding is deterministic, so no resend can repair it.

---

## 13. The Durable Producer Queue

`DurableProducerQueue` makes producer state survive crashes and relocation. It is optional: without it, a producer-controller restart loses controller-unconfirmed messages.

```go
type DurableProducerQueue interface {
    // Durable queues use GoAkt's existing dependency reconstruction contract.
    // ID is the reconstruction key; MarshalBinary carries a reconstruction
    // descriptor, never queued messages.
    extension.Dependency

    // Load restores state at controller (re)start and acquires writership:
    // the returned epoch invalidates every earlier one.
    Load(ctx context.Context) (DurableQueueState, QueueEpoch, error)

    // Store atomically indexes by MessageID and sequence. A new MessageID must
    // use CurrentSeq+1. An existing MessageID returns its original sequence
    // and authoritative first-write payload; the proposed bytes are ignored.
    Store(ctx context.Context, epoch QueueEpoch, request StoreRequest) (StoreResult, error)

    // Accept records that the producer received Stored and durably removed
    // the offer from any recoverable source. Monotonic and idempotent.
    Accept(ctx context.Context, epoch QueueEpoch, messageID string) error

    // Confirm records the highest confirmed sequence under the writer's
    // epoch. Monotonic and idempotent.
    Confirm(ctx context.Context, epoch QueueEpoch, upToSeq int64) error
}
```

The value types (`DurableQueueState`, `UnconfirmedMessage`, `StoreRequest`, `StoreResult`) are immutable, constructor-validated, and defensively copied. `DurableQueueState` validation requires `0 <= ConfirmedSeq <= CurrentSeq` and strictly ascending, gap-free unconfirmed entries covering `(ConfirmedSeq, CurrentSeq]`.

**Fencing.** `Load`, `Store`, `Accept`, and `Confirm` are linearizable. `Load` atomically snapshots state and acquires single-writer ownership; each successful `Load` returns a higher `QueueEpoch` and fences every earlier one. Operations under an old epoch return `gerrors.ErrQueueFenced`. This is what makes relocation safe: the replacement controller's `Load` fences the departed incarnation's writes.

**First write wins.** The first successful `Store` for a `MessageID` is authoritative: retries return the stored snapshot, so nondeterministic serializers cannot create conflicts or mutate an accepted message. A new `MessageID` proposing anything other than `CurrentSeq + 1` returns `gerrors.ErrQueueConflict`.

**Acceptance boundary.** Durable acceptance begins when `Store` returns nil. The producer's retention handoff (removing or durably marking the item) happens before `StoredAck`; only after `Accept` may an implementation compact `MessageID` metadata, and only once the sequence is also confirmed. Normal traffic therefore does not grow a permanent index. A crash inside the `Stored` / `StoredAck` / `Accept` window conservatively retains that one mapping until the producer resubmits and acceptance completes.

**Throughput cost.** The credit loop is strictly serialized per message: durable `Store`, the local `Stored` / `StoredAck` exchange, durable `Accept`, then the next `RequestNext`. Throughput per flow is bounded by roughly two durable backend round trips per message, plus amortized `Confirm` writes on the same lane. Backend latency directly bounds per-flow throughput; horizontal scaling requires multiple independent flows.

**Relocation.** A durable queue is an ordinary user dependency referenced by ID from the endpoint's reliable-delivery configuration. It must return the same sequence, confirmation, `MessageID`, and payload state on every node, and its type must be registered on every node eligible to host the producer.

---

## 14. Companion Spawning and Identity

### 14.1 Incarnation-scoped identity

Every `internal/address.Address` receives an incarnation ID (a UUID) at construction, carried by `Path` and `PID`, serialized on the actor record, and stable across `ReSpawn` because the PID keeps its address. Controller companions derive their reserved names from the endpoint's incarnation:

```
GoAktReliableProducerController-<endpointIncarnationID>
GoAktReliableConsumerController-<endpointIncarnationID>
```

The existing `GoAkt` reserved prefix prevents user actors from occupying these names; the incarnation ID makes each identity unique to one endpoint incarnation, so a delayed message addressed to a dead incarnation's controller can never reach its successor. No controller-name helper is exported and no user-visible naming convention is introduced.

### 14.2 The spawn transaction

`AsReliableProducer` and `AsReliableConsumer` write an in-memory configuration into `spawnConfig`. Validation mirrors the controller constructor guards (window bounds, positive intervals, queue retry policy, finite passivation rejected), so a configuration that passes validation always builds a controller.

The companion transaction hooks `completeSpawn` through `ensureReliableCompanion` in [`actor/delivery_companion.go`](../actor/delivery_companion.go), so `Spawn`, `SpawnChild`, and singleton spawns all get it uniformly. `completeSpawn` is split into `attachAndPublish` (the shared attach and publish core) plus the companion step, and companion creation calls the core directly rather than re-entering the endpoint logic.

The controller is spawned as a **child of the endpoint**:

- endpoint shutdown stops it through the normal child teardown,
- endpoint restart carries it through the subtree restart,
- `rollbackSpawn` stops the endpoint when controller creation fails, so a failed spawn leaves nothing behind.

The one private stop path is a metadata gate in `PID.Shutdown`: a PID carrying the unexported companion spec (role, endpoint name, endpoint incarnation) may be stopped while the system runs, although `Shutdown` rejects reserved names otherwise. This single gate serves rollback, subtree shutdown, supervised restart, and the controllers' terminal self-stop. Companions stay unreachable through every public API.

`ActorSystem.ReSpawn` of a reliable endpoint runs `ensureReliableCompanion` after the endpoint restarts: a live companion restarted with the subtree is left untouched, a terminally stopped one is recreated under the same incarnation identity, and a companion still tearing down returns a retryable error while the endpoint keeps running. This is the supported recovery action after a terminal failure; for a durable producer it acquires a fresh queue epoch through `Load`.

Producer endpoints retain their durable queue instance on the PID so `ReSpawn` can rebuild the controller with its storage.

### 14.3 Wire representation

The endpoint's replicated actor record carries the configuration as a first-class `Actor.reliable_delivery` field, and companion records carry `Actor.reliable_companion` (role, owning endpoint name, endpoint incarnation), both in [`protos/internal/actor.proto`](../protos/internal/actor.proto). The protobuf forms exist only at the wire boundary; in memory the settings live in the configuration structs of [`actor/delivery_config.go`](../actor/delivery_config.go), restored through validating constructors so a malformed record can never produce a trusted spec.

---

## 15. Cluster Publication and Resolution

### 15.1 Publication

When clustering is enabled, the endpoint publishes through `PutActorIfAbsent`, a conditional single-key registry operation that provides atomic cluster-wide name uniqueness for reliable endpoints. Ordinary actors keep overwrite semantics, which their restarts rely on.

Companion records flow through the same spawn funnel: `putActorOnCluster` publishes reserved-name actors when they carry the companion metadata, the same gate `PID.Shutdown` and the death watch use. The `Actors` registry merge filters reserved names, so companions never appear in public listings. The endpoint publishes before its companion exists, which makes the endpoint-visible-companion-missing window normative; it is covered by the consumer controller's transient retry.

With clustering disabled, local attachment is the complete publication step; no registry write or remoting is required.

### 15.2 Local-first resolution

`resolveReliableCompanion(ctx, endpointName, role)` is fully defined for both deployment modes:

1. Look up the endpoint in the local actor tree. If a live endpoint exists, read its incarnation ID, derive the role-specific companion identity, look up that local PID, and validate runtime kind, owning endpoint name, role, incarnation, and liveness. Return only a validated pair.
2. If no local endpoint exists and clustering is disabled, report not-found; the controller timer retries. A single-node flow resolves entirely through the local tree.
3. If no local endpoint exists and clustering is enabled, load the endpoint record from the registry, derive the companion identity from that record's incarnation, load the companion record, validate the same ownership fields plus same-node pair placement, and construct the remote PID. Records pointing at the resolving node itself are rejected.
4. Never fall back from a present-but-invalid local pair to an older cluster record: a mixed local activation means a spawn or restart is in flight, and an older registry record must not win over it.

`DefaultRegistrationLookupTimeout` (500ms) bounds the producer controller's ownership lookup so a slow registry cannot stall its mailbox; a timeout is treated as a dropped registration and recovered by the consumer's retry loop. After resolution, remote controller traffic uses the ordinary `RemoteTell` path addressed at the companion PID directly.

---

## 16. Relocation and Reconstruction

No second relocation subsystem exists. Reliable endpoints ride the existing relocation machinery; only the wiring below was added.

1. The replicated actor record already carries the reliable-delivery configuration ([section 14.3](#143-wire-representation)) and the durable queue as an ordinary user dependency. Spawn normalization appends the queue to the dependency list exactly once, guarded by dependency ID and independent of option order.
2. `reliableSpawnOptionFromWire` is the shared dependency-to-options helper: it decodes the wire configuration, resolves the queue instance by ID among the reconstructed dependencies, and returns one spawn option. Both `wireSpawnOptions` (relocation) and the remote spawn handler (initial remote placement) use it, so the two paths cannot drift. A missing or mistyped queue dependency fails reconstruction instead of silently degrading to a volatile flow.
3. Before the respawn, `recreateActorFromWire` releases the departed incarnation's companion record, deriving the identity from the record's role and incarnation and using the same read-compare-delete ownership check as `releaseDepartedEntry` (`removeActorIfIncarnation`). The release can never touch the fresh controller. Reconstruction failure, for example a queue type not registered on the survivor, restores the departed endpoint record for a later relocation retry.
4. The regular spawn-completion path then creates a new controller child bound to the new local endpoint PID. Controllers are deliberately **non-relocatable**: they hold incarnation state and constructor-bound PIDs, so only their endpoint relocates and reconstructs them.
5. The relocated producer controller's `PreStart` `Load` returns the recovered durable state under a new epoch, fencing the departed writer. The relocated consumer controller has no durable state; its normal registration and session protocol recovers in-flight delivery.
6. Non-relocatable reliable endpoints get their registry records withdrawn on node loss instead of leaking. This is required, not hygiene: reliable endpoints publish with if-absent semantics, so a leaked record would block the endpoint name cluster-wide.
7. Graceful shutdown removes the companion record next to each reliable endpoint's record in `cleanupCluster`, because companions are absent from the user-actor list and per-actor death-watch removal is disabled while the system stops. `cleanupStaleLocalActors` removes orphaned companion records from a previous node incarnation while keeping a live controller's record; companions are never recovered, endpoint recovery spawns a fresh one under a new incarnation.

Placement restrictions: `SpawnOn` rejects reliable options combined with `WithDataCenter`, because a cross-datacenter endpoint could never resolve its pair in the local cluster registry. The standalone cluster client rejects reliable spawn requests with `gerrors.ErrReliableSpawnUnsupported`, since its caller does not participate in the delivery protocol. Remote `SpawnChild` rejects reliable options because its request cannot carry the settings. Remote endpoint placement itself travels as `remote.ReliableDeliverySpec` on `remote.SpawnRequest`, validated with the request and mutually exclusive with `Singleton`.

Operational requirement: relocation of reliable endpoints needs a registry replica count of at least 2. With a single copy, registry partitions primaried on the departed node are lost with it, and a lost peer-endpoint record wedges companion resolution permanently.

Without an external durable queue, relocation has the same loss boundary as a producer process crash: controller-unconfirmed messages may be lost.

---

## 17. Failure Classification

These outcomes are normative:

- Lookup, remoting, watch, and local `Tell` failures are treated as message loss. The controller stays alive and recovers through the timers.
- An unexpected controller panic applies the restart directive; a queue-backed producer controller must `Load` before processing further traffic.
- Wrong senders, stale sessions or nonces, and old operation results are untrusted or obsolete traffic: dropped without state changes, with debug diagnostics.
- Exact duplicates are idempotent and repeat the prior response where the protocol requires one.
- An authenticated current-incarnation invariant violation from a bound endpoint or controller (an unexpected `Produced`, a changed `MessageID` for an outstanding token, an illegal demand range, an impossible sequence) is a contract failure: stop only the detecting controller, publish `ReliableDeliveryFailed` with `ReliableDeliveryStageProtocol`, and leave the user endpoint alive. The controller is not recreated until `ActorSystem.ReSpawn` is explicitly invoked for the endpoint.
- `Store`, `Accept`, or `Confirm` backend errors other than fencing and conflict use the queue retry policy. Exhaustion wraps the cause in `gerrors.ErrReliableStore`, `gerrors.ErrReliableAccept`, or `gerrors.ErrReliableConfirm` and restarts the controller, which reloads authoritative queue state. These recoverable failures do not publish the terminal event.
- `gerrors.ErrQueueFenced` and `gerrors.ErrQueueConflict` are deterministic ownership and integrity failures: no retry, no restart. The controller stops, publishes `ReliableDeliveryFailed` with the matching stage, and requires operator action followed by an explicit endpoint `ReSpawn`.
- `Load` retries before controller publication. Exhaustion during initial or remote spawn rolls back the endpoint-and-companion transaction and returns the error; during relocation it restores the departed endpoint record for a later retry; during a supervised restart it stops the controller and publishes `ReliableDeliveryFailed` with `ReliableDeliveryStageLoad` rather than entering an unbounded restart loop.
- A `Confirmed` from the bound consumer that no longer matches the in-flight delivery is a stale application reply and is dropped, not treated as a violation.
- Payload encode or decode failure and a missing serializer are terminal protocol failures, because they are deterministic and neither retry nor restart can fix them.

Every terminal controller stop publishes exactly one `ReliableDeliveryFailed` on the event stream. Its accessors (`EndpointName()`, `ControllerRole()`, `Stage()`, `Err()`, `Timestamp()`) identify the disabled flow by the user-visible endpoint name without exposing any internal companion identity. `ReliableControllerRole` has `Producer` and `Consumer` values; `ReliableDeliveryStage` has `Load`, `Store`, `Accept`, `Confirm`, and `Protocol` values.

---

## 18. Configuration Defaults

| Setting                    | Side     | Default                            | Meaning                                                                                                             |
|----------------------------|----------|------------------------------------|----------------------------------------------------------------------------------------------------------------------|
| `WithFlowControlWindow`    | Consumer | `DefaultFlowControlWindow` (50)    | Demand granted per `Request`; also the consumer controller's receive buffer capacity. The producer-side unconfirmed bound follows from demand and is not configured. |
| `WithResendInterval`       | Consumer | `DefaultResendInterval` (2s)       | Consumer controller tick: re-registration and `ViaTimeout` resend cadence.                                            |
| `WithDurableQueue`         | Producer | none                               | Durable queue for producer-crash survival.                                                                            |
| `WithQueueRetry`           | Producer | 3 attempts, 100ms initial backoff  | Durable `Store` / `Accept` / `Confirm` retry before the controller raises a reliability error.                        |
| `WithLocalRetryInterval`   | Producer | `DefaultLocalRetryInterval` (500ms)| `RequestNext` and `Stored` retry cadence toward the producer actor.                                                   |

`MaxFlowControlWindow` is the exported constant `10_000`. `DefaultRegistrationLookupTimeout` (500ms) bounds the producer controller's endpoint-and-companion ownership lookup; a timeout counts as a dropped registration and is recovered by the consumer's retry loop.

Both spawn options reject finite passivation. Endpoints keep normal relocation defaults; controller children are always long-lived and non-relocatable.

---

## 19. Limitations

- The hop into the producer is at-most-once. Reliability starts at the producer's handoff to its controller; with a durable queue, at the point the message is stored.
- Non-Proto payloads require serializer registration through `remote.WithSerializables`, which in the current API also enables remoting and binds the configured listener even for a local-only flow.
- A durable flow performs `Store` plus `Accept` before the next credit, so durable backend latency bounds per-flow throughput. Scale horizontally with independent flows.
- Cross-node flows require both systems in the same GoAkt cluster. Remoting-only cross-node flows are out of scope because there is no registry from which to resolve the peer endpoint's activation.
