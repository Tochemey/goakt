# Issue 1386: non-relocatable actor ownership survives owner crash

Reproduction for [issue #1386](https://github.com/Tochemey/goakt/issues/1386).

In cluster mode, the registry record of a named actor is the claim on its name. When the node hosting an actor spawned with `WithRelocationDisabled()` is killed, crash recovery on the leader skips the actor, since it is not relocated, and skips its record with it. Nothing else removes that record after a crash: `cleanupCluster` runs only on a graceful `Stop()`, and `cleanupStaleLocalActors` runs only when the same node address restarts. With a replica count above 1 the record outlives the node, so the dead incarnation keeps owning the name. Since #1384 made ownership incarnation-aware, a fresh spawn of that name fails with `ErrActorAlreadyExists` instead of silently overwriting the stale record. A grain activated with `WithGrainDisableRelocation()` keeps its directory entry the same way, so `TellGrain` and `AskGrain` keep failing against the dead node until the grain is activated again.

After the registry fix landed (#1387), the reporter came back with a second reproducer ([notrodans/goakt@308d566](https://github.com/notrodans/goakt/commit/308d566166943a4bb29ba78799e67df11547d557)) that looks the name up as soon as membership reports the node gone. The leader releases the record only once olric's partition repair has been quiet for three seconds, and until then `ActorOf` still resolved the name to the dead node and `ActorExists` still reported it as taken. That reproducer was run unchanged against the fix before its check was folded into this sample; the outputs below are from both.

## Scenario

The sample runs two OS processes, because an abrupt crash cannot be simulated in-process: a graceful stop would remove the records. The parent process is the survivor, the child process (the same binary, selected through an environment variable) is the owner. Both join one cluster through static discovery with `WithReplicaCount(2)`.

1. The owner spawns `eager-worker` and `stable-worker` with `WithRelocationDisabled()`, activates `pinned-grain` with `WithGrainDisableRelocation()`, and prints a ready line.
2. The survivor resolves both actors remotely, resolves the grain identity and sends to it once, then kills the owner with SIGKILL and waits until membership reports zero peers.
3. The survivor looks `stable-worker` up right away with `ActorOf` and `ActorExists`. Neither may report the dead owner. A read that fails while the registry is under repair says nothing about the name, so it is retried within a 2 second probe window; the window is kept shorter than the three quiet seconds of the crash-recovery gate, so the leader's release can never pass for a departure-aware lookup.
4. The survivor spawns a fresh `eager-worker` right away, retrying for up to 10 seconds because the registry is under repair just after a crash. This is the reclaim on spawn: a name held by a node that is no longer a member is released by the spawn itself.
5. The survivor sends to `pinned-grain` with the identity it resolved before the crash until a message is delivered, for up to 45 seconds. This is the leader's crash recovery releasing the grain's directory entry, after which the next message re-creates the grain locally.
6. The survivor polls `ActorExists` for `stable-worker` for up to 45 seconds until the name is reported free, which the leader's crash recovery does once the node is confirmed gone, then spawns a fresh `stable-worker`.

The 45 second budget covers the crash-recovery gate, which scans the registry only once olric's partition repair has been quiet for three seconds and waits at most thirty for that.

Run it with:

```bash
go run ./playground/issue-1386
```

Set `GOAKT_ISSUE_1386_VERBOSE=1` to have the survivor log at debug level to stderr, which shows the leader's crash recovery and the releases.

## Expected vs actual

- **Expected**: `WithRelocationDisabled()` and `WithGrainDisableRelocation()` keep the instance from being recreated automatically, and nothing more. Once membership has dropped the owner, no lookup reports it, and the names and the entry are released and reusable. The program prints an `OK` line and exits with status 0.
- **Actual on main at f04698a2**: no name is ever released, both respawns fail with `ErrActorAlreadyExists`, the grain stays unreachable, and the program prints `REPRO (broken)` and exits with status 1.
- **Actual on main at d0c06c98, after the registry fix**: the names are released and reusable, but the lookups made right after the departure still report the dead owner for about 3.3 seconds, until the leader's release lands. The reporter's second reproducer exited with status 1 in three runs out of three.

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

Output of the reporter's second reproducer, unchanged, on main at d0c06c98 with the registry fix in place (ports vary):

```text
owner before crash: goakt://non-relocatable-crash-repro@127.0.0.1:53430/stable-worker (remote=true)
owner process killed; survivor membership now reports zero peers
ActorOf after departure: goakt://non-relocatable-crash-repro@127.0.0.1:53430/stable-worker (remote=true)
ActorExists after departure: true
REPRO (broken): lookup still reports an actor owned by a departed endpoint after membership reports zero peers
exit status 1
```

Exit status 2 means the setup itself failed (ports, process start, membership never converging) rather than the defect under test, or that the registry gave a lookup no answer within the probe window, which proves nothing either way; rerun the sample in that case.

## After the fix

```text
owner before crash: goakt://issue1386@127.0.0.1:57187/eager-worker, goakt://issue1386@127.0.0.1:57187/stable-worker, grain main.pinnedgrain/pinned-grain
owner process killed; survivor membership now reports zero peers
ActorOf right after departure: actor not found
ActorExists right after departure: false
immediate respawn of eager-worker: goakt://issue1386@127.0.0.1:57184/eager-worker after 2.006s (local=true relocatable=false)
pinned-grain reachable again within 45s: true after 2.629s
stable-worker released within 45s: true
recreate stable-worker: goakt://issue1386@127.0.0.1:57184/stable-worker (local=true relocatable=false)
OK: the dead node owns nothing anymore; the lookups never reported it, both names were respawned and the grain is reachable again
```

The lookups answer first: `ActorOf` and `ActorExists` check the cluster membership before reporting a non-relocatable actor, so the dead owner is not reported even before the leader's release. The timings then show the order of the two release mechanisms: the eager actor came back two seconds after the departure, before the leader had run, and the grain became reachable at about three seconds, when the leader's crash recovery released the entries of everything the dead node owned. The reporter's second reproducer, unchanged, exits with status 0 against the fix in seven runs out of seven.

Verifying this sample exposed a second defect on the same branch. The leader-side release depends on the actor system receiving the cluster's NodeLeft event, and the cluster engine used to drop that event whenever an olric rebalance epoch that started before the crash completed after it: the survivor logged `dropping stale departure of node=...: the routing table converged with it as a member`, no crash recovery ran, and the release never happened in three of four runs even with the registry fix in place. The engine now checks the current membership before dropping such a departure, and the sample passes on every run.
