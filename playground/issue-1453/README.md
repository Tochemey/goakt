# Issue 1453: a full blocking `BoundedMailbox` on a remote receiver stalls the lane

https://github.com/Tochemey/goakt/issues/1453

A `BoundedMailbox` blocks the producer when it is full. For a message that
arrives over remoting, the producer is the connection's inbound dispatcher:
the read goroutine, or the single drainer that feeds from it. Once that
goroutine blocks in `Enqueue`, the lane stops dispatching every frame behind
it, for every receiver that shares the lane: asks, tells, credit grants and
keepalives. The peer sees missed pongs, closes the connection and redials,
and the next tell to the same actor repeats the stall.

The remoting documentation recommends a bounded mailbox as the mitigation for
a slow consumer. That only holds for `NonBlockingBoundedMailbox` and the
bounded priority mailboxes, which refuse with `ErrMailboxFull` and return the
credit.

The sample runs one receiver node with two actors on the default single
ordinary lane: `slow`, on a `BoundedMailbox` of capacity 2, which spends one
second on each message, and `echo`, which answers every ask at once. A sender
confirms that `echo` answers, floods `slow` with 20 tells, then asks `echo`
again and reads the receiver's dead-letter count. It passes when the ask is
answered and the 17 overflow tells were refused into dead letters.

The slow actor holds each message with `pause.For`. A bounded mailbox is full
only while its consumer is slower than its producer, and no state of the
runtime keeps a mailbox full without a turn: a suspended, passivating or
stopping actor is not running, and a remote tell to it is refused before the
mailbox. The pause models the slow turn and nothing else.

## Actual (before the fix)

```
OK: echo answers before the flood
sent 20 tells to the slow actor (0 failed on the sender)
BUG: echo did not answer within 3s while the slow actor's mailbox was full: context deadline exceeded
request timed out
BUG: 17 overflow tells were absorbed by the connection instead of being refused (dead letters: 0, expected at least 17)
FAIL: a full blocking BoundedMailbox on a remote receiver stalls the lane for every receiver on it
exit status 1
```

## Expected (after the fix)

```
OK: echo answers before the flood
sent 20 tells to the slow actor (0 failed on the sender)
OK: echo answered in 1ms while the slow actor's mailbox was full
OK: the overflow was refused and routed to dead letters (dead letters: 17, expected at least 17)
PASS: a full blocking BoundedMailbox on a remote receiver refuses the overflow and keeps the lane alive
```

## Run

```bash
go run ./playground/issue-1453
```
