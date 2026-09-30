# Issue 1397: passivation during a turn

https://github.com/Tochemey/goakt/issues/1397

A grain whose `OnReceive` outlasts `deactivateAfter` gets three messages at
once: the first holds the turn open past the idle deadline, the other two
queue behind it. Run once without reentrancy and once with it.

## Actual (before the fix)

- Default mode: the passivation manager calls `deactivate` on its own
  goroutine, so `OnDeactivate` runs while the first message is still inside
  `OnReceive`.
- Reentrancy mode: the passivation pill goes through the mailbox, but the
  turn keeps draining after it, so the two queued messages are handled by the
  instance whose `OnDeactivate` already ran.

## Expected (after the fix)

- The passivation decision always goes through the mailbox and executes
  after the message in progress: `OnDeactivate` never overlaps `OnReceive`.
- A message queued behind the pill is refused with `ErrDead`, so its sender
  retries against a fresh activation, instead of being handled by the
  deactivated grain.

## Run

```bash
go run ./playground/issue-1397
```

The sample prints `PASS` lines and exits 0 with the fix; before the fix it
prints the violations it observed and exits 1.
