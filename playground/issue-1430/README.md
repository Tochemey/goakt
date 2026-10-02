# Issue 1430: an actor handles asks whose sender stopped waiting

https://github.com/Tochemey/goakt/issues/1430

https://github.com/Tochemey/goakt/issues/1407 reports this for grains. Actors
had the same gap: the timeout of `Ask` was a timer on the sender's side only. An
actor handled an ask even when its sender gave up while the message waited, and
the handler's `Context()` did not carry the ask deadline, so the handler could
not tell that nobody waited for its answer.

The sample runs a three-node cluster. The worker actors live on node 3 and are
asked from node 1, node 2 and node 3. For each asker it checks three things:

- **timeout error**: an ask that times out must fail with `ErrRequestTimeout`,
  whichever node it is sent from.
- **expired ask**: the worker is kept from handling its messages, an ask with a
  100 ms timeout times out while it waits, then the worker resumes. The ask must
  not be handed to `Receive`.
- **context deadline**: an ask handled in time must see a deadline on the
  handler's `Context()` that ends within the ask timeout.

No handler blocks. A worker stops handling its messages the way the runtime
itself does it: it sends a `StashNonReentrant` request to a gate actor that never
answers, and resumes when that request times out after 500 ms.

## Actual (before the fix)

```
BUG: asked from node 1, timeout error: not ErrRequestTimeout: context deadline exceeded
BUG: asked from node 1, expired ask: handed to Receive after its sender timed out
OK: asked from node 1, context deadline: the handler's Context() ends within the ask timeout
BUG: asked from node 2, timeout error: not ErrRequestTimeout: context deadline exceeded
BUG: asked from node 2, expired ask: handed to Receive after its sender timed out
OK: asked from node 2, context deadline: the handler's Context() ends within the ask timeout
OK: asked from node 3, timeout error: ErrRequestTimeout
BUG: asked from node 3, expired ask: handed to Receive after its sender timed out
BUG: asked from node 3, context deadline: the handler's Context() has no deadline
FAIL: 6 of 9 checks show an ask whose timeout is not honored
exit status 1
```

Before the fix two things differed by node:

- From another node the handler's context already had the deadline: the
  remoting client sends the ask timeout as a deadline and the receiving node
  puts it on the context it hands to the actor.
- From another node a timed out ask failed with `context deadline exceeded`
  only. The remoting client returned the transport error as is; for a grain it
  already added `ErrRequestTimeout`.

## Expected (after the fix)

```
OK: asked from node 1, timeout error: ErrRequestTimeout
OK: asked from node 1, expired ask: not handed to Receive
OK: asked from node 1, context deadline: the handler's Context() ends within the ask timeout
OK: asked from node 2, timeout error: ErrRequestTimeout
OK: asked from node 2, expired ask: not handed to Receive
OK: asked from node 2, context deadline: the handler's Context() ends within the ask timeout
OK: asked from node 3, timeout error: ErrRequestTimeout
OK: asked from node 3, expired ask: not handed to Receive
OK: asked from node 3, context deadline: the handler's Context() ends within the ask timeout
PASS: an actor skips an ask whose sender stopped waiting, its context carries the ask deadline, and a timeout is ErrRequestTimeout from every node
```

## Run

```bash
go run ./playground/issue-1430
```
