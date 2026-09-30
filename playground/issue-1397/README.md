# Issue 1397: grain passivation runs OnDeactivate during a message

Reproduction for [issue #1397](https://github.com/Tochemey/goakt/issues/1397).

## The issue

A grain is written against three promises: one activation per identity, one message at a time, and `OnDeactivate` last. Passivation broke all three.

- For a grain without reentrancy, the passivation manager called `OnDeactivate` on its own goroutine as soon as the grain was idle past its timeout, even while `OnReceive` was still handling a slow message. The grain was removed from the node's grain list, so the next message started a second instance while the first was still running.
- For a grain with reentrancy, passivation went through the mailbox, but the messages queued behind it were still handed to the instance whose `OnDeactivate` had already run.

## What the sample does

The sample runs one node. The bug is local to a grain, so no cluster is needed.

The grain never blocks inside `OnReceive`. Its slow message is a request to a second grain, the gate, which stands in for a database that answers only when the sample tells it to. The grain uses `StashNonReentrant` reentrancy, so it takes no other message while that request is in flight, and it answers the caller of the slow message from the request's continuation with a reply it took ownership of through `DeferResponse`. The gate hands the reply of every request it gets to the sample through a channel; its own `OnReceive` returns at once.

1. Start the node.
2. Activate the gate and a grain that passivates after 300ms idle.
3. Send the grain a slow message: it sends the gate a request and waits on the reply for longer than the idle timeout.
4. While the slow message is still in progress, send two more messages.
5. Let the gate answer, so the slow message finishes, then report what the grain saw.

## Running it

```bash
go run ./playground/issue-1397
```

Exit status: `1` means the bug is present, `0` means every promise held, `2` means the setup failed.

## Output before the fix

The failure needed a message held inside `OnReceive`, which the sample no longer does: a grain waiting on a request was already left alone by the passivation manager before the fix, so the sample passes on the commit before the fix as well. It keeps the three promises under watch with a handler that does not block.

## Output with the fix

```text
step 2: grain activated, it passivates after 300ms idle
step 3: the slow message has been in progress for 900ms, longer than the idle timeout
step 4: sent two more messages while the slow one is still in progress
step 5: message 1 was answered
step 5: message 2 was answered

OK: one activation at a time, one message at a time, and OnDeactivate last.
```

Passivation leaves the grain alone while its request is in flight. When the gate answers, the two other messages are already waiting in the mailbox, so the grain is not idle: the same instance answers both. The grain passivates later, once it really is idle.

## Where it happens in the code

- `grainPID.passivationTry` in `actor/grain_pid.go` deactivated a grain without reentrancy directly, on the passivation manager's goroutine.
- `grainPID.handlePassivationPill` in `actor/grain_pid.go` deactivated the grain even when messages were waiting in its mailbox.
- `grainPID.runTurn` in `actor/grain_pid.go` keeps taking messages after the grain deactivated, and `handleGrainContext` passed them to `OnReceive` without checking that the grain was still active. They are now sent to a fresh activation.
