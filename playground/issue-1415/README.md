# Issue 1415: Stop fails on a node whose listed peers are gone

Reproduction for [issue #1415](https://github.com/Tochemey/goakt/issues/1415), and for a second defect of the same shutdown that the investigation turned up.

A node that stops does two things for the cluster before it leaves it (`shutdownCluster`):

1. It hands a snapshot of its actors and grains to its peers (`persistPeerStateToPeers`), so that the leader can recreate them on a surviving node.
2. It removes its own records from the cluster registry (`cleanupCluster`). The registry is spread over the cluster nodes.

`Stop` returned the error of both steps.

## The two defects

**Defect 1 (issue #1415): `Stop` fails when no listed peer can take the snapshot.** When the peers the node still lists are gone or leaving themselves, the handover fails against every one of them and `Stop` returned `failed to replicate state to any peer`. A peer in that state answers in one of three ways:

- `remoting is not enabled`: it has stopped, and answers on a connection that was open before it stopped. Closing the remoting server only closes its listener.
- `cluster is not enabled`: it has left the cluster and has not closed its remoting yet.
- `connection refused`: it is gone, and a new connection is attempted.

None of them is a node the snapshot could have gone to. The handover goes to the three oldest peers first; with the fix, when all three are leaving it moves on to the next oldest ones, and it is only skipped when every listed peer is leaving. A peer that does not answer in time is different: it may be a running node behind a network fault, and then the snapshot is really lost.

**Defect 2: `Stop` fails when a registry record cannot be removed.** The record of an actor is removed when the node leaves the cluster (`cleanupCluster`), the record of a grain when the grain is deactivated (`grainPID.deactivate`), which a stopping node does for every grain it hosts. The removal fails when the node holding the record is gone, or is stopping at the same time. The error changes with the moment: `context canceled`, `connection refused`, `redis: client is closed`, `cluster registry read timed out`, and `grain deactivation failed` once per grain. A record that stays behind names a node that has left, and the cluster already handles such records, as it must after a crash: a lookup skips an actor's record (`getActorRecord`), the next spawn of the name writes over it (`departedClaim`), and so does the recreation of a relocated actor (`putActorOnCluster`); a grain's record is replaced by the next activation of the grain (`releaseUnreachableGrainOwner`).

## Scenarios

Each scenario runs on a fresh three-node cluster joined through NATS discovery, with the NATS server running inside the sample. Node 3 is the node that stops. It runs in the sample's process and hosts relocatable actors named `worker-0`, `worker-1` and so on, and grains named `session-0`, `session-1` and so on.

In scenarios 2 and 3 a node has to be gone while membership still lists it. A node that stops gracefully is dropped from membership before its remoting goes away, so that node is an OS process of its own there, and the sample kills it: membership keeps a killed node for a few seconds. Before the kill node 3 asks an actor on it, so that node 3 holds an open remoting connection to it, as a node of a running cluster does.

| # | What happens | What must hold | Guards against |
| --- | --- | --- | --- |
| 1 | Node 1 and node 2 keep running while node 3 stops. | `Stop` returns nil and the workers are recreated on them. | A fix that breaks a handover that has a node to go to |
| 2 | Node 1 and node 2 are killed. Node 3, which hosts workers and grains, stops while it still lists both. | `Stop` returns nil. | Defect 1 and defect 2 |
| 3 | Node 2 is killed. Node 3, which hosts workers and grains, stops while it still lists it. Node 1 keeps running. | `Stop` returns nil. Node 1 recreates every worker, and every grain answers when asked from node 1, although node 3 could not remove most of its records. | Defect 2, and a fix of it that would strand the actors or the grains |
| 4 | The three nodes, each hosting workers and grains, stop at the same time, three clusters in a row. | Every `Stop` returns nil. | Defect 2, and defect 1 when the shutdowns interleave that way |

Scenario 3 is what shows that a record left behind does no harm. It ends as `NOT EXERCISED` when node 3 removed every actor record or every grain record, so a pass always means that records of both kinds stayed behind, that the workers were recreated and that the grains answer all the same.

Run it with:

```bash
go run ./playground/issue-1415
```

It takes about a minute. Scenario numbers on the command line restrict the run, for example `go run ./playground/issue-1415 2 3`. Set `GOAKT_ISSUE_1415_VERBOSE=1` to copy the log of node 3 to stderr.

The sample exits with status 1 when a scenario shows a defect, with status 2 when a cluster could not be set up or a scenario could not reach the state it checks (`NOT EXERCISED`), and with status 0 when every scenario passed.

## Output on main at 969c1d89

Ports vary and are shortened to `ADDR` here. `Stop` joins the errors of its steps, one line or more each; the sample prints one line and counts the rest.

```text
scenario 1: node 3 stops while node 1 and node 2 keep running
node 3 hosts the workers; node 1 and node 2 are running
PASS: Stop of node 3 returned nil
PASS: all 3 workers were recreated on another node

scenario 2: node 3 stops while it still lists node 1 and node 2, which are gone
node 3 hosts the workers and the grains, and has asked an actor on node 1 and on node 2
node 1 and node 2 killed; node 3 still lists both
BROKEN: Stop of node 3 returned: redis: client is closed; failed to replicate state to any peer: dial tcp ADDR: connect: connection refused; dial tcp ADDR: connect: connection refused [and 59 more lines]

scenario 3: node 3 stops while it still lists node 2, which is gone; node 1 keeps running
node 3 hosts the workers and the grains, and has asked an actor on node 2; node 1 is running
node 2 killed; node 3 still lists it
BROKEN: Stop of node 3 returned: grain=main.session/session-0: grain deactivation failed [and 55 more lines]
node 3 could not remove 27 of its 30 actor records and 28 of its 30 grain records
PASS: all 30 workers were recreated on another node
PASS: all 30 grains answer from another node

scenario 4: the three nodes stop at the same time
BROKEN: round 2: Stop of node 2 returned: context canceled
BROKEN: round 3: Stop of node 1 returned: context canceled
BROKEN: round 3: Stop of node 2 returned: context canceled

1 passed, 3 broken, 0 not exercised
exit status 1
```

In scenario 2 the line shown carries the handover error (defect 1); the 59 other lines are the grains whose deactivation failed and the actor record that could not be removed (defect 2). In scenario 3 the workers are recreated and the grains answer on main as well: only the return value of `Stop` is wrong there. Scenario 4 depends on how the three shutdowns interleave: which nodes fail, and with which error, changes from run to run, but at least one `Stop` failed in every run on main.

## Output with the fix

```text
scenario 1: node 3 stops while node 1 and node 2 keep running
node 3 hosts the workers; node 1 and node 2 are running
PASS: Stop of node 3 returned nil
PASS: all 3 workers were recreated on another node

scenario 2: node 3 stops while it still lists node 1 and node 2, which are gone
node 3 hosts the workers and the grains, and has asked an actor on node 1 and on node 2
node 1 and node 2 killed; node 3 still lists both
PASS: Stop of node 3 returned nil

scenario 3: node 3 stops while it still lists node 2, which is gone; node 1 keeps running
node 3 hosts the workers and the grains, and has asked an actor on node 2; node 1 is running
node 2 killed; node 3 still lists it
PASS: Stop of node 3 returned nil
node 3 could not remove 25 of its 30 actor records and 27 of its 30 grain records
PASS: all 30 workers were recreated on another node
PASS: all 30 grains answer from another node

scenario 4: the three nodes stop at the same time
PASS: every Stop returned nil in 3 rounds

4 passed, 0 broken, 0 not exercised
```

## Checking the fix

The fix has two parts, one per defect. The sample was run unchanged against each part alone, applied to the library locally and reverted afterwards, twice each:

| Library | 1 | 2 | 3 | 4 | Exit |
| --- | --- | --- | --- | --- | --- |
| main at 969c1d89 | pass | broken (handover and registry) | broken (registry) | broken | 1 |
| A peer that is leaving or gone is not a failed handover | pass | broken (registry) | broken (registry) | broken (registry) | 1 |
| The registry removal is best effort while the node stops, for actors and grains | pass | broken (handover: `failed to replicate state to any peer`) | pass | pass | 1 |
| Both parts | pass | pass | pass | pass | 0 |

Scenario 2 needs both parts: each one removes its own error and leaves the other. With both, every scenario passed in five runs in a row and none ended as `NOT EXERCISED`.

## What the sample does not stage

The answer the issue reports, `remoting is not enabled` from a peer that stopped gracefully and is still listed, needs the stopping node to miss the peer's leave message, which a sample cannot force. That answer is covered by a unit test instead (`TestPersistPeerStateToPeers`, "returns nil when a stopped peer answers on a connection left open"): it stops a real node, sends it a peer state over a connection opened before the stop, checks that the answer is `remoting is not enabled`, and checks that the handover treats it as a peer that is leaving.
