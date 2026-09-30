# Issue 1405: a full bounded mailbox rejects the shutdown PoisonPill

https://github.com/Tochemey/goakt/issues/1405

One grain with a mailbox of one. The first message parks inside `OnReceive`,
the second one fills the mailbox, and the actor system stops while it is
full. Once `OnReceive` is released, both messages must have been handled
and `OnDeactivate` must have run.

## Actual (before the fix)

The pill was refused like any other message and `Stop` reported the grain
as dropped without `OnDeactivate`:

```
FAIL: stop: grain=main.slowgrain/slow: OnDeactivate skipped, mailbox is full
```

## Expected (after the fix)

```
PASS: both messages handled and OnDeactivate ran although the mailbox was full when the system stopped
```

## Run

```bash
go run ./playground/issue-1405
```

`TestPoisonAllGrainsDrainsAFullMailbox` in `actor` pins the same sequence
deterministically, and `TestPoisonAllGrainsGivesUpOnAStuckGrainAtTheDeadline`
shows that the shutdown context, not the mailbox, still bounds `Stop`.
