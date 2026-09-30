# Issue 1397: grain passivation runs OnDeactivate during a message

Reproduction for [issue #1397](https://github.com/Tochemey/goakt/issues/1397).

## The issue

A grain is written against three promises: one activation per identity, one message at a time, and `OnDeactivate` last. Passivation broke all three.

- For a grain without reentrancy, the passivation manager called `OnDeactivate` on its own goroutine as soon as the grain was idle past its timeout, even while `OnReceive` was still handling a slow message. The grain was removed from the node's grain list, so the next message started a second instance while the first was still running.
- For a grain with reentrancy, passivation went through the mailbox, but the messages queued behind it were still handed to the instance whose `OnDeactivate` had already run.

## What the sample does

The sample runs one node. The bug is local to a grain, so no cluster is needed.

1. Start the node.
2. Activate a grain that passivates after 300ms idle.
3. Send it a slow message that holds `OnReceive` open for longer than the idle timeout.
4. While the slow message is still running, send two more messages.
5. Let the slow message finish, then report what the grain saw.

## Running it

```bash
go run ./playground/issue-1397
```

Exit status: `1` means the bug is present, `0` means every promise held, `2` means the setup failed.

## Output with the bug

```text
step 2: grain activated, it passivates after 300ms idle
step 3: the slow message has been running for 900ms, longer than the idle timeout
step 4: sent two more messages while the slow one is still running
step 5: message 1 was answered
step 5: message 2 was answered
step 5: a message sent again was answered by a fresh activation

BUG: OnDeactivate ran while OnReceive was still handling the slow message.
BUG: 2 instances of the grain handled messages at the same time.
```

## Output with the fix

```text
step 2: grain activated, it passivates after 300ms idle
step 3: the slow message has been running for 900ms, longer than the idle timeout
step 4: sent two more messages while the slow one is still running
step 5: message 1 was answered
step 5: message 2 was answered

OK: one activation at a time, one message at a time, and OnDeactivate last.
```

With the fix, passivation waits for the slow message to finish. When it runs, the two other messages are already waiting in the mailbox, so the grain is not idle: passivation is skipped and the same instance answers both. The grain passivates later, once it really is idle.

## Where it happens in the code

- `grainPID.passivationTry` in `actor/grain_pid.go` deactivated a grain without reentrancy directly, on the passivation manager's goroutine.
- `grainPID.handlePassivationPill` in `actor/grain_pid.go` deactivated the grain even when messages were waiting in its mailbox.
- `grainPID.runTurn` in `actor/grain_pid.go` keeps taking messages after the grain deactivated, and `handleGrainContext` passed them to `OnReceive` without checking that the grain was still active. They are now sent to a fresh activation.
