# Issue 1377: concurrent SpawnOn can create duplicate actors with the same name

Reproduction for [issue #1377](https://github.com/Tochemey/goakt/issues/1377).

In cluster mode, a named spawn first asks the registry whether the name is taken (`checkSpawnPreconditions` → `ActorExists`), then creates the actor, then writes the registry record with `PutActor`, which overwrites any existing record. Nothing makes the check and the write one step. Two nodes that spawn the same name at the same time both see "absent", both create the actor, and both write; the second write replaces the first. Both actors stay alive, and because `ActorOf` returns a local actor before it looks at the registry, each node resolves its own copy.

## Scenarios

The sample starts three nodes (A, B and an observer) in one process with static discovery, runs scenarios 1 to 3, then starts a separate two-node cluster for scenario 4.

| # | Scenario | Deterministic | What it shows |
| --- | --- | --- | --- |
| 1 | race | yes | A and B call `SpawnOn(name, WithPlacement(Local))` together. Each spawn's `PreStart` waits until the other spawn has also entered `PreStart`. `PreStart` runs after the duplicate check and before the registry write, so both spawns pass the duplicate check before either writes to the registry. Both calls succeed and two actors stay alive. |
| 2 | cleanup | yes | Continues scenario 1. It stops the duplicate whose record was overwritten. Its death watch calls `RemoveActor(name)` without checking who owns the record, so the running actor's record is deleted. The running actor is then unresolvable from every other node, and `ActorExists` reports the name as free. |
| 3 | reliable | yes | Before the fix, reliable endpoints were the one kind published with an if-absent write, so one of two concurrent spawns already failed with `ErrActorAlreadyExists`. The loser was rolled back with `Shutdown`, and its death watch then deleted the winner's record. So a conditional write alone is not enough: cleanup must be fenced by the incarnation that owns the record. |
| 4 | issue recipe | no | The reporter's setup: 2 nodes, 32 concurrent `SpawnOn` calls split across them, `WithPlacement(LeastLoad)`, `WithRelocationDisabled()`, 20 runs. It counts the runs where both nodes still resolve a local actor after 3 seconds. |

## Expected vs actual

- **Expected**: at most one live actor per name in the cluster. The losing spawn returns `ErrActorAlreadyExists` and leaves no live actor. Cleaning up the loser never deletes the winner's registry record.
- **Actual before the fix (main at 74b7aad2 and at ecacd908)**: scenarios 1, 2 and 3 print `REPRO (broken)` on every run. In scenario 4, 4, 2, 1 and 7 of 20 runs left two live actors in four separate executions.
- **After the fix (branch issue-1377)**: scenarios 1, 3 and 4 print `OK`, scenario 2 is skipped, and the program exits with status 0.

Output of scenarios 1 to 3 before the fix (ports vary):

```text
== scenario 1: race (SpawnOn, WithPlacement(Local), both spawns wait for each other in PreStart)
   both spawns were in PreStart at the same time (both passed the duplicate check, neither had written to the registry): true
   node A SpawnOn: err=<nil>
   node B SpawnOn: err=<nil>
   ActorOf from node A:   goakt://issue1377@127.0.0.1:54348/race
   ActorOf from node B:   goakt://issue1377@127.0.0.1:54351/race
   ActorOf from observer: goakt://issue1377@127.0.0.1:54351/race (the one registry record)
   node A instance running: true, node B instance running: true
REPRO (broken): both concurrent SpawnOn calls succeeded; two live actors share one name and nodes A and B resolve different PIDs
== scenario 2: cleanup (stop the duplicate that does not own the registry record)
   registry record owner: goakt://issue1377@127.0.0.1:54351/race
   stopping the non-owner: goakt://issue1377@127.0.0.1:54348/race
   survivor goakt://issue1377@127.0.0.1:54351/race running: true
   ActorOf from survivor's node: goakt://issue1377@127.0.0.1:54351/race
   ActorOf from stopped node:    error: (actor=race) actor not found
   ActorOf from observer:        error: (actor=race) actor not found
   ActorExists from observer:    false
REPRO (broken): the stopped duplicate's death watch cleanup deleted the registry record of the running actor; it is now unresolvable from every other node and its name looks free
== scenario 3: reliable endpoint (loser rollback must leave the winner's record)
   both spawns were in PreStart at the same time: true
   node A SpawnOn: err=<nil>
   node B SpawnOn: err=actor=(reliable-consumer) actor already exists
   winner goakt://issue1377@127.0.0.1:54348/reliable-consumer running: true
   ActorOf from observer:     error: (actor=reliable-consumer) actor not found
   ActorExists from observer: false
REPRO (broken): the losing spawn's rollback deleted the winner's registry record; if-absent publication alone does not protect the winner
```

In scenario 4, the count of successful `SpawnOn` calls per run is not the defect. Concurrent calls that land on the same node are merged by the node's local spawn serialization, and those calls get the existing actor back, which is the documented `Spawn` behavior. A run is a defect only when both nodes resolve a local actor (a duplicate) or a node cannot resolve the name at all.

## After the fix

The registry write of a spawn claims the name with a conditional write that only one of two concurrent spawns can win, and every later update or removal of the record is accepted only from the activation that owns it. Scenario 1 prints `OK`: exactly one spawn succeeds, the other fails with `ErrActorAlreadyExists`, and the observer still resolves the winner once the loser has been cleaned up. Scenario 2 is skipped, because there is no duplicate to stop. Scenario 3 prints `OK`: the loser's rollback leaves the winner's record in place. Scenario 4 prints `OK`: every run ends with both nodes resolving the same PID.

Output of scenarios 1 to 3 on the fix (ports vary):

```text
== scenario 1: race (SpawnOn, WithPlacement(Local), both spawns wait for each other in PreStart)
   both spawns were in PreStart at the same time (both passed the duplicate check, neither had written to the registry): true
   node A SpawnOn: err=actor=(race) actor already exists
   node B SpawnOn: err=<nil>
   winner goakt://issue1377@127.0.0.1:58363/race running: true
   ActorOf from observer: goakt://issue1377@127.0.0.1:58363/race
OK: exactly one of the two concurrent spawns succeeded and the registry resolves the winner
== scenario 2: cleanup: skipped, scenario 1 left no duplicate to clean up
== scenario 3: reliable endpoint (loser rollback must leave the winner's record)
   both spawns were in PreStart at the same time: true
   node A SpawnOn: err=<nil>
   node B SpawnOn: err=actor=(reliable-consumer) actor already exists
   winner goakt://issue1377@127.0.0.1:58360/reliable-consumer running: true
   ActorOf from observer:     goakt://issue1377@127.0.0.1:58360/reliable-consumer
   ActorExists from observer: true
OK: the losing spawn's rollback left the winner's registry record in place
```

In scenario 4 the number of successful `SpawnOn` calls per run still varies: calls that reach the hosting node after the actor exists get it back, as documented for `Spawn`, and calls that overlapped the duplicate check fail with `ErrActorAlreadyExists`. Every run ends with one actor that both nodes resolve to the same PID.

## Run

```bash
go run ./playground/issue-1377
```

It takes about 70 seconds. Exit status 1 means at least one defect was observed. Exit status 2 means the cluster could not be set up.
