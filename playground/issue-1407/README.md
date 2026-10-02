# Issue 1407: a grain handles asks whose sender stopped waiting

https://github.com/Tochemey/goakt/issues/1407

The timeout of `AskGrain` is a timer on the sender's side only. A grain hands an
ask to `OnReceive` even when its sender gave up while the message waited in the
mailbox, and the handler's `Context()` does not carry the ask deadline, so the
handler cannot tell that nobody waits for its answer.

The sample runs a three-node cluster. The worker grains live on node 3 and are
asked from node 1, node 2 and node 3, on both ask paths: the channel path of a
grain without reentrancy and the envelope path of a grain with reentrancy. For
each of the six combinations it checks two things:

- **expired ask**: the worker is kept from reading its mailbox, an ask with a
  100 ms timeout times out while it waits there, then the worker resumes
  another 100 ms later. The ask must not be handed to `OnReceive`.
- **context deadline**: an ask handled in time must see a deadline on the
  handler's `Context()` that ends within the ask timeout.

No handler blocks. A worker stops reading its mailbox the way the runtime itself
does it: it sends a `StashNonReentrant` request to a gate grain, which defers its
reply until the sample opens it. For the channel path the worker turns its
reentrancy off once it is on hold, so the asks that follow take the path of a
grain without reentrancy.

## Actual (before the fix)

```
BUG: channel path, asked from node 1, expired ask: handed to OnReceive after its sender timed out
OK: channel path, asked from node 1, context deadline: the handler's Context() ends within the ask timeout
BUG: channel path, asked from node 2, expired ask: handed to OnReceive after its sender timed out
OK: channel path, asked from node 2, context deadline: the handler's Context() ends within the ask timeout
BUG: channel path, asked from node 3, expired ask: handed to OnReceive after its sender timed out
BUG: channel path, asked from node 3, context deadline: the handler's Context() has no deadline
BUG: envelope path, asked from node 1, expired ask: handed to OnReceive after its sender timed out
BUG: envelope path, asked from node 1, context deadline: the handler's Context() has no deadline
BUG: envelope path, asked from node 2, expired ask: handed to OnReceive after its sender timed out
BUG: envelope path, asked from node 2, context deadline: the handler's Context() has no deadline
BUG: envelope path, asked from node 3, expired ask: handed to OnReceive after its sender timed out
BUG: envelope path, asked from node 3, context deadline: the handler's Context() has no deadline
FAIL: 10 of 12 checks show an ask that ignores its sender's timeout
exit status 1
```

The two checks that already pass are the channel path asked from another node:
the remoting client sends the ask timeout as a deadline and the receiving node
puts it on the context it hands to the grain.

## Expected (after the fix)

```
OK: channel path, asked from node 1, expired ask: not handed to OnReceive
OK: channel path, asked from node 1, context deadline: the handler's Context() ends within the ask timeout
OK: channel path, asked from node 2, expired ask: not handed to OnReceive
OK: channel path, asked from node 2, context deadline: the handler's Context() ends within the ask timeout
OK: channel path, asked from node 3, expired ask: not handed to OnReceive
OK: channel path, asked from node 3, context deadline: the handler's Context() ends within the ask timeout
OK: envelope path, asked from node 1, expired ask: not handed to OnReceive
OK: envelope path, asked from node 1, context deadline: the handler's Context() ends within the ask timeout
OK: envelope path, asked from node 2, expired ask: not handed to OnReceive
OK: envelope path, asked from node 2, context deadline: the handler's Context() ends within the ask timeout
OK: envelope path, asked from node 3, expired ask: not handed to OnReceive
OK: envelope path, asked from node 3, context deadline: the handler's Context() ends within the ask timeout
PASS: a grain skips an ask whose sender stopped waiting and its context carries the ask deadline
```

## Run

```bash
go run ./playground/issue-1407
```
