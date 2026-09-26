# Issue 1386: non-relocatable actor ownership survives owner crash

Reproduction for [issue #1386](https://github.com/Tochemey/goakt/issues/1386).

In cluster mode, the registry record of a named actor is the claim on its name. When the node hosting an actor spawned with `WithRelocationDisabled()` is killed, crash recovery on the leader skips the actor, since it is not relocated, and skips its record with it. Nothing else removes that record after a crash: `cleanupCluster` runs only on a graceful `Stop()`, and `cleanupStaleLocalActors` runs only when the same node address restarts. With a replica count above 1 the record outlives the node, so the dead incarnation keeps owning the name. Since #1384 made ownership incarnation-aware, a fresh spawn of that name fails with `ErrActorAlreadyExists` instead of silently overwriting the stale record.

## Scenario

The sample runs two OS processes, because an abrupt crash cannot be simulated in-process: a graceful stop would remove the record. The parent process is the survivor, the child process (the same binary, selected through an environment variable) is the owner. Both join one cluster through static discovery with `WithReplicaCount(2)`.

1. The owner spawns `stable-worker` with `WithRelocationDisabled()` and prints a ready line.
2. The survivor resolves the actor remotely, then kills the owner with SIGKILL and waits until membership reports zero peers.
3. The survivor calls `ActorOf` once right after the departure, then polls `ActorExists` for up to 45 seconds waiting for the name to be reported free. The budget covers the crash-recovery gate, which scans the registry only once olric's partition repair has been quiet for three seconds and waits at most thirty for that.
4. The survivor spawns a fresh non-relocatable actor under the same name with `SpawnOn(..., WithPlacement(Local), WithRelocationDisabled())`.

Run it with:

```bash
go run ./playground/issue-1386
```

## Expected vs actual

- **Expected**: `WithRelocationDisabled()` keeps the actor from being recreated automatically, and nothing more. Once membership has dropped the owner, the name is released, `ActorExists` reports it free, and a normal named spawn can claim it again. The program prints an `OK` line and exits with status 0.
- **Actual on main at f04698a2**: the name is never released, the respawn fails with `ErrActorAlreadyExists`, and the program prints `REPRO (broken)` and exits with status 1. The `ActorOf` call right after the departure varies between runs: it either returns the dead owner as if it were alive, or the registry read times out during olric's partition repair with the `context.DeadlineExceeded` identity lost on the way (issue #1385). Both are symptoms described in the issue; the two lines that follow are the defect and are the same on every run.

Output before the fix (ports vary):

```text
owner before crash: goakt://issue1386@127.0.0.1:59883/stable-worker (remote=true)
owner process killed; survivor membership now reports zero peers
ActorOf right after departure: err=failed to fetch remote actor=stable-worker: deadline exceeded
name released within 45s: false
recreate same name: err=actor=(stable-worker) actor already exists
REPRO (broken): the dead non-relocatable actor still blocks reuse of its stable name
exit status 1
```

Exit status 2 means the setup itself failed (ports, process start, membership never converging) rather than the defect under test.

Set `GOAKT_ISSUE_1386_VERBOSE=1` to have the survivor log at debug level to stderr, which shows the leader's crash recovery and the release of the claim.

## After the fix (branch issue-1386)

Two things change. A spawn that meets a name held by a node that is no longer a member reclaims it on the spot, so the `recreate same name` line succeeds on every run. And once the leader's crash recovery has confirmed the node is gone, it releases the claims of the node's non-relocatable actors, so `ActorExists` reports the name free and the program prints the OK line:

```text
owner before crash: goakt://issue1386@127.0.0.1:60531/stable-worker (remote=true)
owner process killed; survivor membership now reports zero peers
ActorOf right after departure: err=failed to fetch remote actor=stable-worker: deadline exceeded
name released within 45s: true
recreate same name: goakt://issue1386@127.0.0.1:60528/stable-worker (local=true relocatable=false)
OK: the dead incarnation no longer owns the name and a fresh non-relocatable actor claimed it
```

Verifying this sample exposed a second defect on the same branch. The leader-side release depends on the actor system receiving the cluster's NodeLeft event, and the cluster engine used to drop that event whenever an olric rebalance epoch that started before the crash completed after it: the survivor logged `dropping stale departure of node=...: the routing table converged with it as a member`, no crash recovery ran, and `name released` stayed false in three of four runs even with the registry fix in place. The engine now checks the current membership before dropping such a departure, and the sample passes on every run.
