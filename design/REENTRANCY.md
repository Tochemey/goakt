# Reentrancy (Request Scheduling)

> How actors and grains issue non-blocking requests and keep processing while a reply is in flight.

---

## Table of Contents

1. [Overview](#1-overview)
2. [Components](#2-components)
3. [The Envelope Protocol](#3-the-envelope-protocol)
4. [Request Lifecycle](#4-request-lifecycle)
5. [Modes and the Pause Machinery](#5-modes-and-the-pause-machinery)
6. [Grain Turn Loop Integration](#6-grain-turn-loop-integration)
7. [Reply Ownership: Asks and DeferResponse](#7-reply-ownership-asks-and-deferresponse)
8. [Runtime Toggles](#8-runtime-toggles)
9. [Remote and Cluster Paths](#9-remote-and-cluster-paths)
10. [Passivation Interaction](#10-passivation-interaction)
11. [Shutdown and Teardown](#11-shutdown-and-teardown)
12. [Observable Guarantees](#12-observable-guarantees)

---

## 1. Overview

By default an actor or grain that needs an answer from another process blocks its handler in `Ask`, which stalls its mailbox for the round trip. Reentrancy (the Orleans "request scheduling" pattern) replaces that blocking wait with a **correlation-ID request**: the handler calls `Request` / `RequestName` / `RequestGrain` / `RequestActor`, gets back a `RequestCall` handle, registers a continuation with `Then`, and returns. The reply arrives later as an ordinary queue item and runs the continuation **on the process's own turn**, so handler code stays single-threaded in every mode.

Both process kinds share one request machinery (`requestState`, `reentrancyState`, the envelope structs and serializers, the reply router). They differ only in how envelopes enter and leave the process: actors receive them through their system mailbox dispatch, grains through a dedicated response queue beside the user mailbox.

The four request edges (actor to actor, actor to grain, grain to grain, grain to actor) all work locally and across nodes.

---

## 2. Components

| File                                                                                | Responsibility                                                                                                             |
|-------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------|
| [`reentrancy/reentrancy.go`](../reentrancy/reentrancy.go)                           | Public config: `Mode` (`Off` / `AllowAll` / `StashNonReentrant`), `MaxInFlight`.                                           |
| [`actor/reentrancy.go`](../actor/reentrancy.go)                                     | Shared machinery: `RequestCall`, `requestHandle`, `requestConfig`, `requestState`, `reentrancyState`, `installReentrancy`. |
| [`actor/pid.go`](../actor/pid.go)                                                   | Actor arms: `request`, `requestName`, `requestGrain`, `handleAsyncRequest/Response`, register/deregister, stash.           |
| [`actor/grain_pid.go`](../actor/grain_pid.go)                                       | Grain arms: `admitRequest`, `enqueueEnvelope`, `handleAsyncRequest/Response`, turn loop, passivation pill, teardown.       |
| [`actor/grain_context.go`](../actor/grain_context.go)                               | `RequestGrain` / `RequestActor`, `DeferResponse`, `CorrelationID`, request-aware reply methods, context modes.             |
| [`actor/grain_reply.go`](../actor/grain_reply.go)                                   | `GrainReply`: the one-shot write end handed out by `DeferResponse`.                                                        |
| [`actor/async_reply.go`](../actor/async_reply.go)                                   | `routeAsyncReply`: the single reply router for client, actor, and grain targets.                                           |
| [`actor/grain_engine.go`](../actor/grain_engine.go)                                 | `envelopeAsk` (external ask against a reentrant grain), `deliverAsyncEnvelope` (local-or-forward delivery).                |
| [`actor/remote_server.go`](../actor/remote_server.go)                               | `remoteTellGrainHandler` envelope branch: inbound wire envelopes bypass the blocking send path.                            |
| [`internal/commands/async.go`](../internal/commands/async.go)                       | `AsyncRequest`, `AsyncResponse`, `AsyncReplyTo`: the envelope structs.                                                     |
| [`internal/commands/async_serializer.go`](../internal/commands/async_serializer.go) | Wire serializers for the three envelope kinds, magic-framed so they never collide with proto payloads.                     |
| [`internal/pendingasks/table.go`](../internal/pendingasks/table.go)                 | System-wide correlation table for external asks against reentrant grains.                                                  |

---

## 3. The Envelope Protocol

Every request travels as a `commands.AsyncRequest` and every reply as a `commands.AsyncResponse`:

```go
type AsyncRequest struct {
    CorrelationID string        // uuid, keys the requester's requestStates map
    ReplyTo       *AsyncReplyTo // where the reply goes; nil for external asks
    Message       any           // the user payload
}

type AsyncResponse struct {
    CorrelationID string
    Message       any           // reply payload, may be nil
    Error         string        // failure, empty on success
}

type AsyncReplyTo struct {
    Kind  ReplyToKind      // ReplyToActor | ReplyToGrain
    Actor *address.Address // typed address, actor targets
    Grain string           // identity string, grain targets (registry key)
}
```

Three protocol rules matter to maintainers:

- **Empty response means success with a nil payload.** An `AsyncResponse` with no `Message` and no `Error` is the wire form of `NoErr`. Every decode site (`envelopeAsk`, `grainPID.handleAsyncResponse`, `PID.handleAsyncResponse`) completes it as `(nil, nil)`. Do not add a validation guard that rejects empty responses; one existed once and it contradicted `NoErr`.
- **Error identity is restored from strings.** Errors cross the wire as strings; `asyncErrorFromString` maps them back so `errors.Is` works on the requester for `ErrRequestTimeout`, `ErrRequestCanceled`, and `ErrUnhanledMessage` (exact match plus the `errors.Join` prefix form). Other errors arrive as plain string errors.
- **Envelope frames never decode through the proto registry.** The serializers prepend a magic marker (framed via `MarshalAppend`, no length arithmetic), and the grain reply target is carried as the **identity string**, structurally validated at the node boundary (two non-empty halves around the separator). `internal/commands` stays a leaf package: it must not import `actor`.

---

## 4. Request Lifecycle

```
requester turn                                          target
--------------                                          ------
Request*(to, msg, opts...)
  | resolve mode: per-call override or state default
  | reject: Off -> ErrReentrancyDisabled
  |         invalid -> ErrInvalidReentrancyMode
  |         inFlightCount >= maxInFlight -> ErrReentrancyInFlightLimit
  | registerRequestState (correlation = uuid.NewString())
  | deliver AsyncRequest ------------------------------> handler runs,
  |   on failure: deregister, return pre-completed        replies or defers
  |   handle carrying the delivery error                        |
  | arm timeout (only after delivery succeeded)                 |
  | return handle; handler continues / returns                  |
  ...                                                           |
response envelope <------------------------ routeAsyncReply ---+
  | completeRequest: one-shot CAS on requestState
  | deregisterRequestState: Dec counters, stop timeout
  + continuation (Then) runs on the requester's own turn
```

Key properties:

- **Per-request mode snapshot.** `requestState` records the mode it was admitted with. Runtime retunes and disables never affect an in-flight request: it completes, unpauses, and unstashes under its original mode.
- **Timeouts arm only after delivery succeeds**, so a failed delivery can never race a timeout completion. The default is `DefaultGrainRequestTimeout` for grain-issued requests (an explicit non-positive `WithRequestTimeout` disables it); actor-issued requests have no implicit default.
- **Timeouts and `Cancel` are queue-routed.** They do not touch state directly from the timer or caller goroutine; they enqueue an error `AsyncResponse` (`enqueueAsyncError`) that flows through the normal response path on the owner's turn. This is also what wakes a parked paused process (the zombie guard).
- **Completion is exactly-once.** `requestState.complete` is CAS-guarded; a late genuine reply after a timeout or cancel drops as an unknown correlation at debug level, touching nothing.
- **Guard failures return a pre-completed handle** (`completedRequestCall`): `Then` fires immediately with the error, and on grains `gctx.Err` is never invoked for request-API failures, so supervision is not triggered by a rejected request.

---

## 5. Modes and the Pause Machinery

`reentrancyState` is shared by both process kinds:

```go
type reentrancyState struct {
    mode          atomic.Int32  // default policy; atomics because runtime
    maxInFlight   atomic.Int64  // toggles retune them mid-flight
    requestStates *xsync.Map[string, *requestState]
    inFlightCount atomic.Int64
    blockingCount atomic.Int64  // stash-mode requests currently in flight
}
```

| Mode                | Admission                                                                        | While a request is in flight                                                       |
|---------------------|----------------------------------------------------------------------------------|------------------------------------------------------------------------------------|
| `Off`               | Rejected (`ErrReentrancyDisabled`), unless the call carries `WithReentrancyMode` | n/a                                                                                |
| `AllowAll`          | Counted                                                                          | The process keeps consuming its mailbox; any message may interleave between turns. |
| `StashNonReentrant` | Counted, `blockingCount++`                                                       | User-mailbox consumption stops entirely; only response envelopes are processed.    |

The grain stash is **pause-based, not copy-based**: nothing is moved to a side buffer. `paused()` is simply `blockingCount > 0`, and the turn loop skips the user mailbox while it holds. Buffered user messages (including timer ticks and `PoisonPill`) wait in place and replay in exact arrival order when the last blocking request completes. The actor side keeps its pre-existing stash implementation; the shared part is the counter contract.

Consequence worth remembering (decision 12): `TellGrain` keeps its acknowledgement semantics, so a tell against a paused grain can return `ErrRequestTimeout` to the caller even though the message is delivered and processes after resume.

---

## 6. Grain Turn Loop Integration

A reentrant grain owns two queues: the user `grainMailbox` and a dedicated `responses` grainMailbox (constructed unconditionally in `newGrainPID`, so the pointer never changes at runtime). `enqueueEnvelope` routes by type: `AsyncRequest` into the user mailbox (it is an ordinary incoming message), `AsyncResponse` into `responses`.

`runTurn` consults responses first on **every** iteration:

```go
for range budget {
    grainContext := pid.dequeueResponse()

    if grainContext == nil && !pid.paused() {
        grainContext = pid.mailbox.Dequeue()
    }
    ...
}
```

- Responses outrank user messages, so a paused grain can always reach the completions that end its pause.
- `paused()` is re-evaluated per iteration: a request registered mid-turn pauses immediately, and the last completion resumes within the same budget.
- `hasPendingWork` (the idle-reclaim predicate) deliberately ignores buffered user messages while paused; counting them would make the reclaim path spin across workers for the whole pause.

Envelope-borne messages are dispatched with a **channel-less** `GrainContext` (`grainEnvelope` mode; the other modes are `grainTell` with an error channel and `grainAsk` with both channels). The context carries `requestID` and `requestReplyTo`; `recovery` uses the presence of a `requestID` to reply a panic as an error envelope instead of logging it into the void.

---

## 7. Reply Ownership: Asks and DeferResponse

A grain handling an envelope request answers through its normal reply methods (`Response`, `Err`, `NoErr`, `Unhandled`), which are one-shot and route through `routeAsyncReply`. Two mechanisms extend this:

- **`DeferResponse()`** transfers reply ownership out of the turn. It returns a `*GrainReply` (nil for non-request messages, and nil-receiver safe, so callers can complete unconditionally) and marks the context `replyDeferred`: in-turn replies become no-ops. The handle is a one-shot CAS wrapper that completes from any goroutine, typically a `Then` continuation. A deferred obligation does **not** block passivation; only in-flight requests do.
- **`envelopeAsk`** serves external `AskGrain` calls against a grain whose live mode is reentrant (`reentrantEnabled`). Instead of parking the caller on the context's channels (which would hold the grain's turn hostage), it registers the correlation in the system-wide `pendingasks.Table`, enqueues the envelope, and blocks only the **caller**. The reply completes the slot via the router's client arm. Timeout abandons the slot with `LoadAndDelete` semantics: exactly one of reply and abandon wins, a late reply after abandonment is dropped. Non-reentrant grains keep the legacy channel ask bit for bit and never touch the table.

`routeAsyncReply(ctx, from, replyTo, response)` is the single reply router:

| Target         | Arm                                                                                                                                                             |
|----------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| nil `replyTo`  | External ask: `pendingAsks.Complete(correlation)`.                                                                                                              |
| `ReplyToActor` | Resolve the recorded **address** via `pidOf` and tell; works with remoting-without-cluster (name resolution would not). `NoSender` when the replier has no PID. |
| `ReplyToGrain` | Rebuild the identity with `toIdentity` and hand to `deliverAsyncEnvelope`.                                                                                      |

---

## 8. Runtime Toggles

`EnableReentrancy(config) error` and `DisableReentrancy()` exist on both `ReceiveContext` and `GrainContext`, so a process spawned without reentrancy can enable it during message processing when the capability is needed only for a particular case.

- The state holder is `atomic.Pointer[reentrancyState]` on both `PID` and `grainPID`. `installReentrancy` CAS-installs the state **at most once and never removes it**; a second enable retunes (`mode`, `maxInFlight`) the existing object.
- **Disable flips the default mode to `Off`; it never tears down state.** In-flight requests carry their own mode, so they complete, unpause, and unstash normally across any retune or disable. Only new requests observe the current default, and a per-call `WithReentrancyMode` override still admits requests while disabled, exactly matching the spawn-time `Off` behavior.
- Fields inside the state are atomics because the toggle runs on the processing turn while off-turn readers observe them concurrently: the envelope-ask gate in `localSend`, envelope delivery, shutdown cancellation, and wire snapshots.

---

## 9. Remote and Cluster Paths

Outbound, `deliverAsyncEnvelope` on the actor system is the single entry for any grain-targeted envelope:

```
deliverAsyncEnvelope(identity, envelope)
  | gate: !started || isStopping -> error
  | ensureGrainProcess(identity)
  |   +- local hit ----------> pid.enqueueEnvelope (queues + wakeup)
  |   +- owner mismatch -----> sendRemoteTellGrainRequest(owner, envelope)
```

Inbound, `remoteTellGrainHandler` has an envelope branch (after the identity and system-name guards, before the blocking `localSend`): a wire payload that decodes to `AsyncRequest` or `AsyncResponse` goes straight to `deliverAsyncEnvelope`, so remote envelope delivery never blocks a transport handler on a grain turn. Actor-targeted envelopes ride the ordinary remote tell path; their serializers are registered with the remoting layer like any other message type.

Reentrancy config travels with the grain's wire record:

- `internalpb.Grain.Reentrancy` (field 10) is encoded by `wireGrain`, and `toWireGrain` overrides it with the **live** state so runtime-enabled reentrancy survives eager relocation.
- `recreateGrainOnce` decodes the field into a `WithGrainReentrancy` option; remote activation carries it via `remote.GrainRequest.Reentrancy`.
- Reactivation by a **bare send** on a stored identity is a fresh reflective zero-value activation with default config: no reentrancy until activated with options again or re-enabled from a handler. This is documented behavior, not an oversight.

In-flight requests are process-local state: they do not survive requester relocation or node crash. A late reply addressed to a fresh activation drops as an unknown correlation.

---

## 10. Passivation Interaction

The passivation manager must neither deactivate a grain with in-flight requests nor race the grain's turn stream. Two mechanisms, both on-turn:

- **Pause and resume.** `registerRequestState` calls `passivationManager.Pause(pid)` on the 0 to 1 in-flight transition; `deregisterRequestState` calls `Resume` when the count returns to zero, falling back to `startPassivation` (a fresh registration) when the entry was deleted in the meantime.
- **Turn-serialized deactivation.** For any grain whose reentrancy state exists (`reentrancy.Load() != nil`; note this is deliberately **not** `reentrantEnabled`, since a runtime-disabled grain can still hold in-flight requests), `passivationTry` does not deactivate on the manager goroutine. It enqueues an internal `grainPassivationPill` through the user mailbox (returning true so the manager deletes its entry; on a full bounded mailbox it touches activity and returns false so the refreshed deadline lands a full `deactivateAfter` away). The on-turn handler re-checks everything against current state:

| Check on the turn                          | Outcome                                         |
|--------------------------------------------|-------------------------------------------------|
| Inactive, or a `PoisonPill` is in progress | Drop the pill.                                  |
| In flight, or paused                       | Drop; the completion path owns re-registration. |
| Activity since the pill was enqueued       | Re-register a fresh passivation entry.          |
| Genuinely idle past the deadline           | Deactivate.                                     |

Because a pause delays the pill exactly like any other user message, a pending request naturally keeps the grain alive. `handlePoisonPill` carries an `isActive` guard so a pill-then-poison sequence runs `OnDeactivate` exactly once. Non-reentrant grains keep the original direct deactivation path untouched.

---

## 11. Shutdown and Teardown

Shutdown must unblock paused grains before the `PoisonPill` can reach them. `poisonAllGrains` runs a **cancellation pre-pass** (`enqueueInFlightCancellations`: off-turn, queue-routed, one error envelope per in-flight request) before enqueuing the pill. When the pill is processed, `handlePoisonPill` runs `teardownInFlightRequests` inline: every remaining state completes with `ErrRequestCanceled` (callback panics are contained per-request by `runTeardownCallback`), counters are zeroed via `reset`, and only then does `deactivate` run.

The **re-pause window** is the one race this leaves open by design: a user message queued ahead of the pill can start a fresh blocking request after the pre-pass already ran. The pill then waits behind the new pause, and the request's own timeout lifts it, so deactivation completes within the request timeout. This is why stash mode should keep a finite timeout: a `StashNonReentrant` request with `WithRequestTimeout(<= 0)` whose reply is lost pauses the grain until shutdown.

---

## 12. Observable Guarantees

1. **Single-threaded handlers, always.** Continuations, replies, timeouts, cancellations, teardown, and the passivation decision all execute on the owning process's turn. No user code runs concurrently with a handler.
2. **Exactly one completion per request.** CAS-guarded; late replies drop as unknown correlations without side effects.
3. **Counters are exact.** Admission and completion are turn-serialized; `inFlightCount` and `blockingCount` drain to zero, and `maxInFlight` admits exactly its configured number per burst.
4. **Stash preserves arrival order.** Pause is a consumption gate, not a copy; buffered messages (timer ticks included) replay in order on resume.
5. **`Off` is indistinguishable from "no config".** Whether configured, defaulted, or runtime-disabled, the legacy send paths are used bit for bit and no pending-ask entry is created.
6. **In-flight requests are activation-scoped.** They do not survive requester relocation or crash; config survives eager relocation and remote activation, but not reactivation by bare send.
7. **A rejected or failed request never trips supervision.** Guard failures complete the handle; on grains `Err` is not recorded for request-API failures.
