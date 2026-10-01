# Issue 1423: Spawn right after Kill returns the killed, stopped PID

https://github.com/Tochemey/goakt/issues/1423

`Kill` returns once the actor has run `PostStop`, but the actor's name stays in
the local actor tree until the death watch handles the actor's `Terminated`
message in a later turn. A `Spawn` of the same name inside that window starts a
new actor, fails to add it to the tree because the name is still taken, and
returns the old, stopped PID with a nil error. The death watch then releases
the name, so nothing answers to it any more.

The sample spawns `worker`, then kills it and spawns it again 20 times, on one
processor. After each `Spawn` it checks that the returned PID is a new one and
is running. When it is not, the sample waits for the death watch and checks
whether the name still resolves to an actor.

## Actual (before the fix)

```
BUG: Spawn after Kill returned the killed, stopped PID with a nil error in 20 of 20 rounds.
BUG: the name resolved to no actor afterwards in 20 of those 20 rounds.
exit status 1
```

Without the single-processor pin the same failure shows in 1 to 4 of 500
rounds, which matches the rate in the report.

## Expected (after the fix)

```
OK: Spawn after Kill returned a new running actor in all 20 rounds.
```

## Run

```bash
go run ./playground/issue-1423
```
