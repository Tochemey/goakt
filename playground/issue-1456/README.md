# Issue 1456: Partition returns 0 on failure

https://github.com/Tochemey/goakt/issues/1456

`ActorSystem.Partition` returned `0` for every failure: when the actor system
was not clustered, when it was stopped, and when the name had no registry
record. Partition `0` is a valid partition, so a caller could not tell a
failure from an actor whose registry record lives in partition `0`.

The sample runs a three-node cluster with seven registry partitions. Node 1
spawns actors until the registry places one of them in partition `0`. Every
node then asks for the partition of that actor and of a name that was never
spawned. The sample also asks a standalone actor system and a stopped node.

With the fix, `Partition(ctx, actorName)` returns the partition the registry
reports for the actor's record, and an error when there is none:
`ErrActorNotFound` for a name without a record, `ErrClusterDisabled` for a
standalone system and `ErrActorSystemNotStarted` for a stopped one.

## Actual (before the fix)

Run with the previous `Partition(actorName) uint64` signature, whose result
the sample reads as a partition with no error:

```
BUG: standalone system: Partition returned 0 and error <nil>, expected cluster is not enabled
OK: node 1, spawned actor-2: partition 0
BUG: node 1, never spawned never-spawned: Partition returned 0 and error <nil>, expected actor not found
OK: node 2, spawned actor-2: partition 0
BUG: node 2, never spawned never-spawned: Partition returned 0 and error <nil>, expected actor not found
OK: node 3, spawned actor-2: partition 0
BUG: node 3, never spawned never-spawned: Partition returned 0 and error <nil>, expected actor not found
BUG: stopped node: Partition returned 0 and error <nil>, expected actor system is not running
FAIL: 5 partition lookups did not return the registry partition or an error
exit status 1
```

## Expected (after the fix)

```
OK: standalone system: cluster is not enabled
OK: node 1, spawned actor-2: partition 0
OK: node 1, never spawned never-spawned: (actor=never-spawned) actor not found
OK: node 2, spawned actor-2: partition 0
OK: node 2, never spawned never-spawned: (actor=never-spawned) actor not found
OK: node 3, spawned actor-2: partition 0
OK: node 3, never spawned never-spawned: (actor=never-spawned) actor not found
OK: stopped node: actor system is not running
PASS: Partition returns the registry partition on every node, and an error when there is none
```

## Run

```bash
go run ./playground/issue-1456
```
