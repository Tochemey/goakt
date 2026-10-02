# Issue 1413: crash recovery keeps retrying after Stop returned

Reproduction for [issue #1413](https://github.com/Tochemey/goakt/issues/1413), and for a second defect of the same crash recovery that the investigation turned up.

When a node dies without a shutdown, the cluster leader recovers its actors on a goroutine of its own (`gateCrashRecovery`). It waits for the cluster to settle, scans the registry for the dead node's actors, and recreates them on the surviving nodes. An attempt that fails is retried after a five second sleep, four attempts in all.

## The two defects

**Defect 1: the recovery of a node that came back is lost.** The leader caches every node's remoting port, because the registry scan needs it to find a dead node's records. The recovery goroutine owns the dead node's cache entry and drops it when it returns. When the node restarts at the same address while the recovery still waits, the recovery gives up, as it should, but drops the entry all the same. By then the entry belongs to the running node: its rejoin had just refreshed it. Nothing puts it back until some other node joins. When the node dies again, every attempt fails with `no cached remoting port`, the recovery gives up, and the node's actors are never recreated.

**Defect 2 (issue #1413): the recovery outlives a stopped node.** The recovery only learns that the node is stopping from `isStopping()`, which reads the `shuttingDown` flag, and `Stop` clears that flag again once it is done. The recovery looks at the flag when it wakes from the five second sleep between two attempts, and every 200ms while it waits for the cluster to settle. A `Stop` that begins and ends between two looks goes unseen. The recovery then carries on against the stopped cluster engine and logs its remaining attempts, up to twenty seconds after the node is gone.

## Scenarios

A node has to die without running its shutdown for crash recovery to start, and a graceful stop cannot stand in for that, so every node is an OS process of its own. The process running the sample is node 1, the leader. It starts the same binary again for the other nodes. They join one cluster through NATS discovery, with the NATS server running inside the sample. Node 2 hosts a relocatable actor named `worker`.

Each scenario runs on a fresh three-node cluster. Before any kill, the sample waits until the leader has announced the join of node 2 and resolves the worker to it.

| # | What happens | What must hold | Guards against |
| --- | --- | --- | --- |
| 1 | Node 2 is killed. The leader keeps running. | The worker is recreated on a surviving node. | A fix that breaks a plain recovery |
| 2 | Node 2 is killed, restarted at the same address while the recovery waits, and killed again. The leader keeps running. | The worker is recreated on a surviving node. | Defect 1 |
| 3 | Node 2 is killed. The leader is stopped while the recovery waits for the cluster to settle, before its first attempt. | The leader's log does not mention node 2 after `Stop` returned. | Defect 2, in the wait |
| 4 | Node 4 joins and dies at once. The leader is stopped while the recovery of node 4 sleeps between two attempts. | The leader's log does not mention node 4 after `Stop` returned. | Defect 2, in the sleep (the case of the issue) |

Scenario 4 needs a recovery that sleeps, and only a failed attempt puts one to sleep. It gets a failed attempt without leaning on defect 1: node 4 ends its process as soon as it has joined, before the leader has announced the join. The leader caches a node's remoting port when it announces the join, so it never cached the port of node 4, and the recovery it starts for that node cannot succeed. This is the case the library's own message calls `never observed alive`. Other ways to fail an attempt were tried and do not work: the registry scan succeeds while another node is dead or frozen, because reads fall back to the replicas.

Run it with:

```bash
go run ./playground/issue-1413
```

It takes about two minutes. Scenario numbers on the command line restrict the run, for example `go run ./playground/issue-1413 2 4`. Set `GOAKT_ISSUE_1413_VERBOSE=1` to copy the leader's log to stderr.

The sample exits with status 1 when a scenario shows a defect, with status 2 when a cluster could not be set up or a scenario could not reach the state it checks (`NOT EXERCISED`), and with status 0 when every scenario passed.

## Output on main at 11abde93

Ports vary, and the leader's name is shortened to `[...]` here.

```text
scenario 1: a node crashes; the leader keeps running
three nodes are up; node 1 is the leader and node 2 hosts the worker
node 2 killed
OK: the leader recreated the worker on a surviving node

scenario 2: a node crashes, rejoins and crashes again; the leader keeps running
three nodes are up; node 1 is the leader and node 2 hosts the worker
node 2 killed; the leader started its crash recovery
node 2 restarted at the same address and hosts the worker; the crash recovery saw it back and gave up
node 2 killed again
  leader: error: leader=[...] could not derive relocation set for node=127.0.0.1:61773 after 4 attempts; skipping rebalance (hint: its actors are not recovered; check cluster health)
  leader: 4 attempt(s) failed with "no cached remoting port"
REPRO (broken): the worker was not recreated on a surviving node

scenario 3: the leader is stopped while a crash recovery waits for the cluster to settle
three nodes are up; node 1 is the leader and node 2 hosts the worker
node 2 killed; the leader started its crash recovery
leader.Stop returned after 418ms; reading the leader's log for 20s
OK: the crash recovery stayed silent after Stop returned

scenario 4: the leader is stopped while a crash recovery sleeps between two attempts
three nodes are up; node 1 is the leader and node 2 hosts the worker
node 4 started: it joins the cluster and dies at once
the first attempt of its crash recovery failed; the recovery sleeps 5s before the next
leader.Stop returned after 398ms; reading the leader's log for 20s
  after Stop: warn: leader=[...] could not read the cluster members before recovering node=127.0.0.1:62028: cluster engine is not running (hint: proceeding as if the node is gone)
  after Stop: warn: node=[...] has no cached remoting port for departed node=127.0.0.1:62028 (never observed alive); cannot derive its registry records
  after Stop: warn: leader=[...] could not derive relocation set for node=127.0.0.1:62028 (attempt 2/4); retrying in 5s
  after Stop: warn: leader=[...] could not read the cluster members before recovering node=127.0.0.1:62028: cluster engine is not running (hint: proceeding as if the node is gone)
  after Stop: warn: node=[...] has no cached remoting port for departed node=127.0.0.1:62028 (never observed alive); cannot derive its registry records
  after Stop: warn: leader=[...] could not derive relocation set for node=127.0.0.1:62028 (attempt 3/4); retrying in 5s
  after Stop: warn: leader=[...] could not read the cluster members before recovering node=127.0.0.1:62028: cluster engine is not running (hint: proceeding as if the node is gone)
  after Stop: warn: node=[...] has no cached remoting port for departed node=127.0.0.1:62028 (never observed alive); cannot derive its registry records
  after Stop: error: leader=[...] could not derive relocation set for node=127.0.0.1:62028 after 4 attempts; skipping rebalance (hint: its actors are not recovered; check cluster health)
REPRO (broken): the crash recovery logged 9 line(s) after Stop returned

2 passed, 2 broken, 0 not exercised
exit status 1
```

Scenario 3 depends on how long `Stop` takes on main. It passed in seven runs where `Stop` took between 216ms and 418ms, and failed in the one run where `Stop` took 150ms, less than the 200ms between two looks at the flag: the recovery then made all four attempts after `Stop` had returned and logged twelve lines.

## Checking a fix

The sample is meant to be run unchanged against a fix. Before the fix was written, it was run that way against candidate changes, applied to the library locally and reverted afterwards:

| Library | 1 | 2 | 3 | 4 | Exit |
| --- | --- | --- | --- | --- | --- |
| main at 11abde93 | OK | broken | OK, or broken when `Stop` is faster than 200ms | broken | 1 |
| The recovery keeps the cache entry when it gives up because the node is back | OK | OK | broken in that run (`Stop` took 150ms) | broken | 1 |
| The recovery treats a system that is no longer started as stopped, and checks before it reads the cluster (the change of PR #1414) | OK | broken | OK | OK | 1 |
| Both changes | OK | OK | OK | OK | 0 |

The two defects are independent: each change turns its own scenarios and leaves the other's as they were. With both, every scenario passes and none ends as `NOT EXERCISED`.

## After the fix

Both defects are fixed in the crash recovery goroutine (`gateCrashRecovery` and `awaitRelocationQuiescence` in `actor/actor_system.go`):

- The recovery asks `isStoppingOrStopped()`, which also holds once the system has stopped (neither started nor starting), at every checkpoint: before each read of the cluster while it waits, when it wakes from the sleep between two attempts, and before it publishes and dispatches.
- The recovery keeps the remoting-port cache entry of a node that rejoined. When it sees the node back it leaves the entry alone. On every other path it drops the entry and then refreshes the cache from the membership, which puts back a node that rejoined unnoticed while the recovery ran; when the membership cannot be read, the entry is kept.

The sample, unchanged, against the fix:

```text
scenario 1: a node crashes; the leader keeps running
three nodes are up; node 1 is the leader and node 2 hosts the worker
node 2 killed
OK: the leader recreated the worker on a surviving node

scenario 2: a node crashes, rejoins and crashes again; the leader keeps running
three nodes are up; node 1 is the leader and node 2 hosts the worker
node 2 killed; the leader started its crash recovery
node 2 restarted at the same address and hosts the worker; the crash recovery saw it back and gave up
node 2 killed again
OK: the leader recreated the worker on a surviving node

scenario 3: the leader is stopped while a crash recovery waits for the cluster to settle
three nodes are up; node 1 is the leader and node 2 hosts the worker
node 2 killed; the leader started its crash recovery
leader.Stop returned after 333ms; reading the leader's log for 20s
OK: the crash recovery stayed silent after Stop returned

scenario 4: the leader is stopped while a crash recovery sleeps between two attempts
three nodes are up; node 1 is the leader and node 2 hosts the worker
node 4 started: it joins the cluster and dies at once
the first attempt of its crash recovery failed; the recovery sleeps 5s before the next
leader.Stop returned after 304ms; reading the leader's log for 20s
OK: the crash recovery stayed silent after Stop returned

4 passed, 0 broken, 0 not exercised
```

## A timing the sample has to respect

The leader announces a join about a tenth of a second after the node shows up among its peers, once the routing table has converged on it, and only then caches the node's remoting port. A node killed inside that window was never known to the leader. Scenario 4 uses exactly that, on purpose, with node 4. For node 2 it would spoil the other scenarios, so the sample waits for the leader to announce each join of node 2 before it kills the node.
