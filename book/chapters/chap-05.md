# 5. Messaging

Verified against: `cf7a7c6d` and the uncommitted changes of branch `issue-1432` (2026-10-03): every statement checked against the code

## Contents

- [What you will learn](#what-you-will-learn)
- [5.1 Three ways to send](#51-three-ways-to-send)
  - [Where the families differ](#where-the-families-differ)
- [5.2 What a message carries](#52-what-a-message-carries)
  - [The ask deadline](#the-ask-deadline)
- [5.3 How a reply gets back](#53-how-a-reply-gets-back)
- [5.4 Dead letters](#54-dead-letters)
  - [NoSender](#nosender)
- [5.5 Forwarding](#55-forwarding)
- [5.6 Sending from inside `Receive`: errors become supervision](#56-sending-from-inside-receive-errors-become-supervision)
- [5.7 `PipeTo`](#57-pipeto)
- [5.8 Sending by name](#58-sending-by-name)
- [Guarantees](#guarantees)
- [Implementation details (may change)](#implementation-details-may-change)
- [Behaviours to know](#behaviours-to-know)
- [Exercises](#exercises)

## What you will learn

- The three families of send functions, and how they differ in sender, validation and error reporting.
- What travels with a message: its sender, its context, and for an `Ask` a reply channel and a deadline.
- How a reply gets back, why it is delivered at most once, and when an `Ask` is skipped instead of handled.
- What a handler's `ctx.Context()` really is, for `Tell` and for `Ask`.
- What happens when a send from inside `Receive` fails: errors become supervision.
- How `Forward`, `PipeTo` and dead letters work, and their edges.

Asynchronous requests with continuations (`ctx.Request`) belong with reentrancy, in Chapter 8. Name-based sends during relocation are covered in Part V.

## 5.1 Three ways to send

| Family | Example | Sender the receiver sees | How it reports failure |
|---|---|---|---|
| Package functions | `actor.Tell(ctx, pid, msg)`, `actor.Ask(...)` | the system's NoSender | returned `error` |
| PID methods | `pid.Tell(ctx, to, msg)`, `pid.Ask(...)`, `pid.SendAsync(ctx, name, msg)` | `pid` | returned `error` |
| ReceiveContext methods, inside `Receive` | `ctx.Tell(to, msg)`, `ctx.Ask(...)`, `ctx.Forward(to)` | the current actor, except `ctx.Forward`, which keeps the original sender (§5.5) | **`ctx.Err`**, which goes to supervision (§5.6) |

The ReceiveContext methods are thin wrappers around the PID methods. Each one detaches cancellation from the handler's context first (`withoutCancel`, `actor/receive_context.go`), so a send started in a handler is not cut short when the turn ends. An example is `ctx.Tell`.

### Where the families differ

| Check | `actor.Tell` / `actor.Ask` | `pid.Tell` / `pid.Ask` |
|---|---|---|
| `to == nil` | `ErrDead` (`Tell` in `actor/api.go`) | **panics** in the caller: `pid.Tell` reads `to.state` without a nil check (`PID.Tell` in `actor/pid.go`) |
| Local target not running | `ErrDead` | `ErrDead` |
| `Ask` with timeout ≤ 0, local target | not validated; times out at once | `ErrInvalidTimeout` (`PID.Ask` in `actor/pid.go`) |


"Not running" includes **suspended, stopping and passivating**. `IsRunning` is false in all three states (`actor/pid.go`), and `pid.Tell` tests the same bits directly. A suspended actor therefore accepts no new messages from any family until it is reinstated. For a remote PID, `IsRunning` is an RPC made with `context.Background()`, so it has no timeout.

The two `BatchAsk`s also take their arguments in a different order: `actor.BatchAsk(ctx, to, timeout, msgs...)` and `pid.BatchAsk(ctx, to, msgs, timeout)`. For a remote target, `pid.BatchTell` and `pid.BatchAsk` send one RPC instead of one per message (`actor/pid.go`).

## 5.2 What a message carries

Every local `Tell`, `Ask` and `Forward` takes a `ReceiveContext` from a pool and fills it in `build` (`actor/receive_context.go`). `PipeTo` is the exception: it delivers its result in a context allocated by `newReceiveContext`.

| Field | `Tell` | `Ask` |
|---|---|---|
| `sender`, `self`, `message` | set | set |
| `ctx` | `context.WithoutCancel(senderCtx)` | `senderCtx` as is |
| `response` | `nil` | a new channel with room for one reply (`getResponseChannel` in `actor/pools.go`) |
| `deadline` | `0` | `askDeadline(ctx, timeout)` (`Ask` in `actor/api.go`) |

The `ctx` row has a consequence handlers depend on. For a `Tell`, the sender's context **values** reach the handler, but its cancellation and deadline do not, so a request-scoped context that ends right after `Tell` returns does not cancel the actor's work.

### The ask deadline

`askDeadline` (`actor/ask_deadline.go`) computes when the asker stops waiting: `timeout` from now, or the sender context's deadline if that is earlier. The result is a reading of a monotonic clock local to the process, not wall time. It is never zero, because zero means "no deadline", and it saturates instead of overflowing for a huge timeout.

The deadline is used twice on the receiving side:

1. **Skipping.** When the message reaches the front of the queue, `dispatchOne` checks `askExpired` and drops it without calling `Receive` if the asker has already given up (`actor/pid.go`). The comment explains why: under load, answering requests nobody reads is what keeps an actor behind. An `Ask` already inside `Receive` when its deadline passes still runs to the end. The test suite checks the skip (`TestActorSkipsAnExpiredAskAtDispatch` in `actor/receive_context_test.go`).
2. **The handler's context.** For an `Ask`, `ctx.Context()` lazily derives a context that ends at the deadline (`actor/receive_context.go`; `askContext` in `actor/ask_deadline.go`), released at the end of the turn (`PID.handleReceived` in `actor/pid.go`). A handler that passes `ctx.Context()` to a database call stops when the asker stops waiting. For a `Tell`, `ctx.Context()` is the stored context with no deadline.

## 5.3 How a reply gets back

`ctx.Response(v)` (`actor/receive_context.go`) has three cases:

1. **No reply channel and no request ID.** The message was a `Tell`, so `Response` does nothing, silently.
2. **A request ID** (`ctx.Request`, Chapter 8). The reply is routed as an asynchronous response message. `nil` is rejected with `ErrInvalidMessage`.
3. **A reply channel** (an `Ask`). A compare-and-swap on `responseClosed` lets only the first `Response` through, and the send on the channel is non-blocking. A second `Response`, or a reply to an asker who has gone, is dropped.

On the asking side, `Ask` captures the reply channel *before* handing the context to the mailbox, because after that the pooled context may be recycled for another message (`actor/api.go`). It then waits on the channel, the timer and `ctx.Done()`. A timeout is also reported as a dead letter with the reason `request timed out`.

## 5.4 Dead letters

A dead letter is a record that a message was not delivered or not handled. `handleReceivedErrorWithMessage` builds one (`actor/pid.go`) and `toDeadletter` sends it to the `GoAktDeadletter` system actor. Lifecycle messages and dead-letter commands themselves are excluded, so a failing dead letter cannot recurse.

| Produces a dead letter | Does not |
|---|---|
| `Ask` timeout or context end (`Ask` in `actor/api.go`) | `Tell`/`Ask` to a stopped or suspended actor, or `actor.Tell`/`actor.Ask` to nil: these return `ErrDead` before anything is built (`pid.Tell` to nil panics, §5.1) |
| `ctx.Unhandled()` (`ReceiveContext.Unhandled` in `actor/receive_context.go`) | A reply to an asker that has gone (dropped silently) |
| Mailbox refuses the message, for example when a bounded mailbox is full (`PID.doReceive` in `actor/pid.go`) | Messages abandoned in a mailbox at `Shutdown` (Chapter 3, §3.5) |
| A message sent while the system is stopping (`PID.doReceive` in `actor/pid.go`) | An `Ask` skipped as expired: its timeout was already recorded by the asker |
| `PipeTo` task failure or timeout (§5.7) | |


The dead-letter actor (`deadLetter.handleDeadletter` in `actor/dead_letter.go`) does three things with each one:

1. Increments a total counter.
2. Publishes a `*Deadletter` event on the system event stream.
3. If the receiver is in the actor tree, records it in two maps: the latest dead letter per receiver address, and a count per (receiver address, message type) (`deadLetter` in `actor/dead_letter.go`). A receiver outside the tree (a stopped actor, a grain, a remote actor) is counted in the total only (`deadLetter.handleDeadletter` in `actor/dead_letter.go`).

The reason travels as text (`err.Error()`), not as an error value, so `errors.Is` is not available to subscribers (`PID.toDeadletter` in `actor/pid.go`).

The maps are bounded by the live actors. Every reader of the per-receiver counts, the metrics and `PID`'s own count, looks actors up in the tree, so an entry for a receiver that has left it can never be read. Once the maps hold 1,024 entries, and then each time their size doubles, `prune` drops the entries of receivers no longer in the tree (`deadLetter.handleDeadletter` in `actor/dead_letter.go`). The cost is amortized over the dead letters that grew the maps. An actor spawned again under the same name starts its count from zero only if a prune ran while the name was free. Until then it has the same address string as its predecessor, and so the same counts.

To observe dead letters, subscribe to the system event stream (`ActorSystem.Subscribe`, `actor/actor_system.go`). The subscriber's `Iterator()` is not a live channel. It **drains what is buffered at the moment of the call into a channel that is already closed** (`eventstream/subscriber.go`), so a consumer has to call it repeatedly. A `select` on `<-sub.Iterator()` receives `nil` as soon as the buffer is empty.

### NoSender

`NoSender` is a real actor, `GoAktNoSender`, whose `Receive` marks every message unhandled (`actor/no_sender.go`). It is the sender of every package-level send. A handler that replies with `ctx.Tell(ctx.Sender(), …)` to a message sent with `actor.Tell` produces a dead letter, not a crash.

## 5.5 Forwarding

`ctx.Forward(to)` (`actor/receive_context.go`) re-sends the current message with the **original sender**:

- **Local target.** A new `Tell`-style context is built with `async = true`, then given the reply route of the message it forwards (`ReceiveContext.inheritReplyRoute` in `actor/receive_context.go`): the asker's reply channel, the routed-reply metadata of an `Ask` that arrived over remoting, and the asker's deadline. For an `Ask`, the final actor's `Response` therefore answers the original asker (`TestReceiveContext` in `actor/receive_context_test.go`). Because the deadline travels too, a forwarded `Ask` whose asker has given up is skipped (§5.2). If both the forwarding actor and the final actor respond, the first reply wins, since the reply channel holds one value.
- **Remote target.** The message is sent as `sender.Tell`, so an `Ask`'s reply is lost; the `Forward` comment says so. If the original sender is NoSender it returns silently.
- **Target not running.** `ErrDead` through `ctx.Err`.

`ctx.ForwardTo(name)` does nothing at all outside cluster mode, and reports nothing (`actor/receive_context.go`).

## 5.6 Sending from inside `Receive`: errors become supervision

`ctx.Err(err)` only records the error. Its comment describes what follows (`actor/receive_context.go`). After the turn, `recovery` submits any recorded error to supervision (`actor/pid.go`). Every ReceiveContext send method reports its failure through `ctx.Err`, so **a failed send is a supervised failure of the sender**:

- `notifyParent` ignores `ErrDead` (`actor/pid.go`), so `ctx.Tell` to a stopped actor leaves the sender running.
- Any other error has no directive in the default supervisor, which knows only `PanicError` and `PanicNilError` (`NewSupervisor` in `supervisor/supervisor.go`), and a missing directive **suspends** the actor (`PID.notifyParent` in `actor/pid.go`). A `ctx.Ask` that times out therefore suspends the actor that asked. A suspended actor accepts no messages (§5.1).

Practical rules for handler code:

- Prefer the PID methods (`ctx.Self().Ask(...)`), which return the error to you, when a failure is expected and should be handled in place.
- Or give the actor a supervisor with a directive for the errors you expect (Chapter 9).
- `ctx.Ask` blocks the dispatcher worker running this turn for up to `timeout` (`actor/receive_context.go`). On a small worker pool, a few actors asking each other synchronously can stall the whole system. `PipeTo` or `ctx.Request` keep the worker free.

## 5.7 `PipeTo`

`pid.PipeTo(ctx, to, task)` (`actor/pid.go`) runs `task` on a new goroutine and delivers its result to `to` as an ordinary message, with the piping actor as sender (`PID.handleCompletion` in `actor/pid.go`). The calling actor's turn is not blocked. The task runs outside the actor, so it must not touch the actor's state.

| Outcome | What happens |
|---|---|
| Task succeeds, `to` running | result delivered to `to`'s mailbox |
| Task returns an error | dead letter, reason = the error |
| `to` stopped meanwhile | dead letter with `ErrDead` |
| `WithTimeout` elapses first | dead letter with `context.DeadlineExceeded`; result never delivered |
| `WithCircuitBreaker` open | dead letter with the breaker's error |

The timeout row needs care. `future.New` starts the task on its own goroutine (`internal/future/future.go`). `Await` returns when the context ends, but a task is `func() (any, error)` with no context, so **nothing stops it**: it runs to completion and its result is discarded. A timeout bounds how long you wait for the result, not how long the work runs.

The only options are `WithTimeout` and `WithCircuitBreaker` (`actor/pipe_option.go`). Their comments say only one may be given, but `newPipeConfig` does not check this: with both, `PID.handleCompletion` bounds the wait with the timeout and runs the task through the breaker. `WithTimeout`'s comment states that the task keeps running, and `ctx.PipeTo`'s comment lists both options (`actor/receive_context.go`).

`PipeToName` checks that the name exists when it is called, but resolves it again when the task finishes, through NoSender's `SendAsync` (`actor/pid.go`). The receiver sees NoSender as the sender, not the piping actor.

## 5.8 Sending by name

`pid.SendAsync(ctx, name, msg)` and `pid.SendSync(ctx, name, msg, timeout)` (`actor/pid.go`) resolve the name and then call `Tell` or `Ask`:

1. Resolve in the local data center: the local tree, then the cluster registry.
2. If not found and data centers are configured, query every active data center's endpoints in parallel and take the first hit (`DiscoverActor`, `actor/pid.go`).

They differ in one way, explained in Part V. While a departed node's actors are being recreated elsewhere, `SendSync` retries across that short handoff window (`PID.deliverAcrossHandoff` in `actor/relocation_handoff.go`), whereas `SendAsync` never waits. It fails at once: with `ErrRelocationInProgress` while the name still resolves to the departed node (`PID.deliverBypassingHandoff` in `actor/relocation_handoff.go`), and with the lookup error, usually `ErrActorNotFound`, once that record has been removed.

## Guarantees

| Statement | Enforced by |
|---|---|
| An `Ask` that expired in the queue is not handled | `TestActorSkipsAnExpiredAskAtDispatch` in `actor/receive_context_test.go` |
| An `Ask` handler's context ends at the ask deadline | `TestReceiveContextCarriesTheAskDeadline` in `actor/receive_context_test.go` |
| `Response` delivers at most once | `ReceiveContext.Response` in `actor/receive_context.go` |
| A message marked unhandled becomes a dead letter | `TestDeadletter` in `actor/dead_letter_test.go` |
| A locally forwarded `Ask` is answered by the final actor | `TestReceiveContext` in `actor/receive_context_test.go` |
| The dead-letter maps keep entries only for receivers in the actor tree | `TestDeadletterBucketsFollowActorTree` in `actor/dead_letter_test.go` |

## Implementation details (may change)

- Pooled `ReceiveContext`s, and a new reply channel for every `Ask` (`getResponseChannel` in `actor/pools.go`).
- The process-local monotonic clock used for ask deadlines.
- The dead-letter registry's two maps and its prune threshold.

## Behaviours to know

| Behaviour | Source |
|---|---|
| A failed send inside `Receive` (any error except `ErrDead`), such as a `ctx.Ask` timeout, suspends the sending actor under the default supervisor | `PID.notifyParent` in `actor/pid.go` |
| `PipeTo`'s timeout does not stop the task | `WithTimeout` in `actor/pipe_option.go` |
| `Forward` to a remote PID loses an `Ask`'s reply | `ReceiveContext.Forward` in `actor/receive_context.go` |
| `pid.Tell(ctx, nil, …)` panics; `actor.Tell` returns `ErrDead` | `PID.Tell` in `actor/pid.go`; `Tell` in `actor/api.go` |
| `actor.Ask` does not reject a non-positive local timeout | `Ask` in `actor/api.go` |
| `ForwardTo` is a silent no-op outside cluster mode | `ReceiveContext.ForwardTo` in `actor/receive_context.go` |

## Exercises

1. Explain why `Ask` reads `receiveContext.response` before calling `doReceive`, and what would go wrong otherwise.
2. A handler calls `ctx.Response(a)` and then `ctx.Response(b)`. Which value does the asker get, and why?
3. `inheritReplyRoute` resets `responseClosed` on the forwarded context. Trace what would happen to a forwarded `Ask` without that reset when the pooled context last served an `Ask` that was answered.
4. Rewrite an actor that uses `ctx.Ask` to call a slow dependency so that it neither blocks a worker nor gets suspended on timeout.
