# Issue 1396: grain activated on a stopping node is never released

Reproduction for [issue #1396](https://github.com/Tochemey/goakt/issues/1396).

## The issue

When a node shuts down, it keeps accepting grain activations until the very end. A grain activated during that time gets a registry record that names the stopping node as its owner, and nothing cleans that record up after the node leaves.

Other nodes then look the grain up, find the old owner, and send to it over a connection that is still open. The stopped node answers `remoting is not enabled`. The caller does not treat that answer as "the owner is gone", so it never releases the record and never activates the grain somewhere else. The grain stays unreachable.

## What the sample does

The sample runs a three-node cluster in one process, with static discovery:

- the **caller node** activates grains and sends to them;
- the **stopping node** is shut down in the middle of the sample;
- the **third node** only keeps the cluster at three members.

The race is narrow in production. The sample makes it happen on every run by holding two things open with channels:

- the stopping node has a shutdown hook that waits, so the caller node can act while the stopping node is shutting down;
- the grain's `OnActivate` waits on the stopping node, so the activation is still running when the stopping node cleans up its grains and leaves the cluster.

Steps:

1. Start the three nodes in one cluster.
2. Start stopping the stopping node. Its shutdown hook pauses the shutdown.
3. The caller node calls `GrainOf` with round-robin placement for up to ten grains, so some are sent to the stopping node.
   - With the bug, the stopping node accepts one even though it is shutting down. The caller node stops waiting after one second and keeps the registry record naming the stopping node.
   - With the fix, the stopping node refuses, and the caller node activates the grain on a live node.
4. Let the stopping node finish shutting down. With the bug, the held activation is still running, so the stopping node does not clean up its record. The caller node confirms the stopping node has left the cluster.
5. The caller node calls `GrainOf` and `AskGrain` for every grain it activated, up to five times each, one second apart.

## Running it

```bash
go run ./playground/issue-1396
```

Exit status: `1` means the bug is present, `0` means every grain is reachable, `2` means the setup failed.

## Output with the bug

```text
step 1: the caller node, the stopping node and the third node are in one cluster
step 2: the stopping node is shutting down (paused in its shutdown hook)
step 3: the stopping node accepted an activation of "grain-1" while shutting down
step 4: the stopping node finished shutting down and left the cluster
step 5: grain-1: attempt 1: GrainOf failed: remoting is not enabled
step 5: grain-1: attempt 2: GrainOf failed: remoting is not enabled
step 5: grain-1: attempt 3: GrainOf failed: remoting is not enabled
step 5: grain-1: attempt 4: GrainOf failed: remoting is not enabled
step 5: grain-1: attempt 5: GrainOf failed: remoting is not enabled

BUG: 1 grain(s) unreachable. Their registry record still names the stopping node, which is gone.
```

## Output with the fix

```text
step 1: the caller node, the stopping node and the third node are in one cluster
step 2: the stopping node is shutting down (paused in its shutdown hook)
step 3: the stopping node refused new activations; 10 grains were activated on live nodes
step 4: the stopping node finished shutting down and left the cluster
step 5: all 10 grains answered

OK: every grain is reachable after the stopping node left.
```

In both cases, a first attempt in step 5 sometimes fails with `cluster registry read timed out` while the cluster settles after the stopping node leaves. The next attempt goes through.

## Where it happens in the code

- `actorSystem.shutdown` in `actor/actor_system.go` sets `shuttingDown` first, but `remotingEnabled` only turns off at the end of `shutdownRemoting`.
- `remoteActivateGrainHandler` in `actor/remote_server.go` checks `remotingEnabled` and not `shuttingDown`, so a stopping node still accepts activations.
- `cleanupCluster` in `actor/actor_system.go` only releases grains that are already in the node's grain list, so an activation still running at that point is missed.
- `TCPServer.Shutdown` in `internal/net/tcp_server.go` closes the listener but not the connections already open, so the caller node still reaches the stopping node.
- `releaseUnreachableGrainOwner` in `actor/grain_engine.go` only releases a record after a transport failure, and `remoting is not enabled` is not one.

## Note on the registry record

In this sample, the caller node writes the record for the stopping node before it sends the activation. That is how round-robin placement works. When the caller node stops waiting, it keeps the record on purpose, expecting a later call to release it if the owner is gone. That release never happens because of the last point above. The reporter's scenario, where the stopping node writes the record itself during its shutdown, ends in the same state.
