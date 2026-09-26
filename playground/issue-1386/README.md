# Issue 1386: non-relocatable actor ownership survives owner crash

Reproduction for [issue #1386](https://github.com/Tochemey/goakt/issues/1386).

In cluster mode, the registry record of a named actor is the claim on its name. When the node hosting an actor spawned with `WithRelocationDisabled()` is killed, crash recovery on the leader skips the actor, since it is not relocated, and skips its record with it. Nothing else removes that record after a crash: `cleanupCluster` runs only on a graceful `Stop()`, and `cleanupStaleLocalActors` runs only when the same node address restarts. With a replica count above 1 the record outlives the node, so the dead incarnation keeps owning the name. Since #1384 made ownership incarnation-aware, a fresh spawn of that name fails with `ErrActorAlreadyExists` instead of silently overwriting the stale record. A grain activated with `WithGrainDisableRelocation()` keeps its directory entry the same way, so `TellGrain` and `AskGrain` keep failing against the dead node until the grain is activated again.

## Scenario

The sample runs two OS processes, because an abrupt crash cannot be simulated in-process: a graceful stop would remove the records. The parent process is the survivor, the child process (the same binary, selected through an environment variable) is the owner. Both join one cluster through static discovery with `WithReplicaCount(2)`.

1. The owner spawns `eager-worker` and `stable-worker` with `WithRelocationDisabled()`, activates `pinned-grain` with `WithGrainDisableRelocation()`, and prints a ready line.
2. The survivor resolves both actors remotely, resolves the grain identity and sends to it once, then kills the owner with SIGKILL and waits until membership reports zero peers.
3. The survivor spawns a fresh `eager-worker` right away, retrying for up to 10 seconds because the registry is under repair just after a crash. This is the reclaim on spawn: a name held by a node that is no longer a member is released by the spawn itself.
4. The survivor sends to `pinned-grain` with the identity it resolved before the crash until a message is delivered, for up to 45 seconds. This is the leader's crash recovery releasing the grain's directory entry, after which the next message re-creates the grain locally.
5. The survivor polls `ActorExists` for `stable-worker` for up to 45 seconds until the name is reported free, which the leader's crash recovery does once the node is confirmed gone, then spawns a fresh `stable-worker`.

The 45 second budget covers the crash-recovery gate, which scans the registry only once olric's partition repair has been quiet for three seconds and waits at most thirty for that.

Run it with:

```bash
go run ./playground/issue-1386
```

Set `GOAKT_ISSUE_1386_VERBOSE=1` to have the survivor log at debug level to stderr, which shows the leader's crash recovery and the releases.

## Expected vs actual

- **Expected**: `WithRelocationDisabled()` and `WithGrainDisableRelocation()` keep the instance from being recreated automatically, and nothing more. Once membership has dropped the owner, the names and the entry are released and reusable. The program prints an `OK` line and exits with status 0.
- **Actual on main at f04698a2**: no name is ever released, both respawns fail with `ErrActorAlreadyExists`, the grain stays unreachable, and the program prints `REPRO (broken)` and exits with status 1.

Output before the fix, from the first version of this sample, which covered the `stable-worker` path only (ports vary):

```text
owner before crash: goakt://issue1386@127.0.0.1:59883/stable-worker (remote=true)
owner process killed; survivor membership now reports zero peers
ActorOf right after departure: err=failed to fetch remote actor=stable-worker: deadline exceeded
name released within 45s: false
recreate same name: err=actor=(stable-worker) actor already exists
REPRO (broken): the dead non-relocatable actor still blocks reuse of its stable name
exit status 1
```

The `ActorOf` call right after the departure varied between runs: it either returned the dead owner as if it were alive, or the registry read timed out during olric's partition repair with the `context.DeadlineExceeded` identity lost on the way (issue #1385). Both are symptoms described in the issue.

Exit status 2 means the setup itself failed (ports, process start, membership never converging) rather than the defect under test.

## After the fix (branch issue-1386)

```text
owner before crash: goakt://issue1386@127.0.0.1:62501/eager-worker, goakt://issue1386@127.0.0.1:62501/stable-worker, grain main.pinnedgrain/pinned-grain
owner process killed; survivor membership now reports zero peers
immediate respawn of eager-worker: goakt://issue1386@127.0.0.1:62498/eager-worker after 2.006s (local=true relocatable=false)
pinned-grain reachable again within 45s: true after 3.039s
stable-worker released within 45s: true
recreate stable-worker: goakt://issue1386@127.0.0.1:62498/stable-worker (local=true relocatable=false)
OK: the dead node owns nothing anymore; both names were respawned and the grain is reachable again
```

The timings show the order of the two mechanisms: the eager actor came back two seconds after the departure, before the leader had run, and the grain became reachable at three seconds, when the leader's crash recovery released the entries of everything the dead node owned.

Verifying this sample exposed a second defect on the same branch. The leader-side release depends on the actor system receiving the cluster's NodeLeft event, and the cluster engine used to drop that event whenever an olric rebalance epoch that started before the crash completed after it: the survivor logged `dropping stale departure of node=...: the routing table converged with it as a member`, no crash recovery ran, and the release never happened in three of four runs even with the registry fix in place. The engine now checks the current membership before dropping such a departure, and the sample passes on every run.
