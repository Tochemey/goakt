# Issue 1405: a full bounded mailbox rejects the shutdown PoisonPill

https://github.com/Tochemey/goakt/issues/1405

One grain with a mailbox of one. The first message parks inside `OnReceive`,
the second one fills the mailbox, and the actor system stops while it is
full. Once the first message is released, both messages must have been
handled and answered, `OnDeactivate` must have run and `Stop` must return
nil.

## Actual (before the fix)

The pill was refused like any other message, so `Stop` reported the grain as
dropped without `OnDeactivate`:

```
BUG: Stop returned an error: grain=main.slowgrain/slow: OnDeactivate skipped, mailbox is full
BUG: OnDeactivate never ran; the full mailbox rejected the shutdown pill.
exit status 1
```

## Expected (after the fix)

The pill goes into the mailbox past its capacity, behind the queued message,
so the grain handles both messages, then deactivates:

```
OK: both messages handled, OnDeactivate ran and Stop returned nil although the mailbox was full when the system stopped.
```

## Run

```bash
go run ./playground/issue-1405
```

`TestPoisonAllGrainsDrainsAFullMailbox` in `actor` pins the same sequence
deterministically, and `TestPoisonAllGrainsGivesUpOnAStuckGrainAtTheDeadline`
shows that the shutdown context, not the mailbox, still bounds `Stop`.
