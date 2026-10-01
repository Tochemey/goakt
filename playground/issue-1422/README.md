# Issue 1422: Terminated overtakes PostStart and crashes the user guardian

https://github.com/Tochemey/goakt/issues/1422

An actor's first message should be its own `PostStart`. GoAkt queues
`PostStart` in the user mailbox, but `Terminated` is a control message: it
goes to the priority system queue, which every turn drains first. A child
that stops before its parent has handled `PostStart` therefore delivers
`Terminated` to the parent first.

The sample runs two scenarios 20 times each, on one processor:

1. A parent spawns a child, and the child stops at once. The parent reports
   which lifecycle message it handled first.
2. A top-level actor is spawned and killed at once. Its parent is the user
   guardian, which sets its `logger` field only on `PostStart`. On
   `Terminated` it calls `x.logger.Enabled`, panics on the nil logger, and the
   root guardian stops the actor system.

## Actual (before the fix)

```
BUG: a parent handled Terminated before its own PostStart in 20 of 20 attempts.
BUG: the user guardian panicked and took the actor system down in 20 of 20 attempts.
exit status 1
```

With the logger enabled, the second scenario logs:

```
actor=GoAktRootGuardian child=GoAktUserGuardian failing: err=panic: runtime error: invalid memory address or nil pointer dereference
actor=GoAktUserGuardian system=issue1422 is down, going to shutdown.
```

## Expected (after the fix)

```
OK: every actor handled PostStart before any other message and the actor system survived every stop.
```

## Run

```bash
go run ./playground/issue-1422
```
