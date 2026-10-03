# Issue 1435: AskGrain and TellGrain release a stale grain owner record

https://github.com/Tochemey/goakt/issues/1435

Before, only `GrainOf` and `GrainIdentity` released the registry record of a
grain owner that was stopping or had left the cluster. A bare `AskGrain` or
`TellGrain` was handed the refusal of the stopping node, or kept failing on a
node that was gone, and the caller had to call `GrainOf` and send again.

`AskGrain` and `TellGrain` now release the record themselves. When the message
did not run, because the owner refused it or had left before the request could
be sent, the same call delivers it to the grain re-activated on the calling
node.

This sample is a load test. It puts a cluster through a rolling restart under
load:

- round 1: three nodes and 150 grains spread over them. Callers send asks,
  tells and one-way tells without pause while node 3 goes away;
- a replacement node joins and 150 more grains are spread over the cluster;
- round 2: the same load on the 300 grains while node 2 goes away.

It has two modes:

- **graceful** (the default): every node runs in one process and a node goes
  away through `Stop`. 16 callers run on each of the two nodes that stay.
- **crash**: node 1 runs in the sample's process and every other node in a
  process of its own, which is killed with `SIGKILL`. 16 callers run on node 1.

The grains write every handler run and every activation to a ledger: a map in
graceful mode, and one file per node in crash mode, written before the grain
replies, so the runs of a killed node are not lost. After each round the sample
sends a bare `AskGrain` to every grain. `GrainOf` is called only to create the
grains.

The sample fails when:

- a message ran more than once;
- an ask or an acknowledged tell that returned no error did not run exactly
  once;
- a grain is active on two live nodes;
- a caller was handed the refusal of a stopping node;
- a call that started after the node had left the cluster failed;
- a grain could not be reached with `AskGrain` after the node left.

## Run

```
go run ./playground/issue-1435
go run ./playground/issue-1435 crash
```

Exit status 0 means every check held, 1 means one did not, and 2 means the
setup failed. The counts vary from run to run.

## Graceful mode

### Actual (before the feature)

```
round 1: 150 grains spread over three nodes, node 3 stops under load
  16 callers on each of 2 node(s)
  the node left the cluster in 282ms
  sent 116337 asks, 116338 tells and 116338 one-way tells
  OK: the 232636 acknowledged asks and tells each ran exactly once
  BUG: 44 calls were handed the refusal of the stopping node
  OK: no call that started after the node had left the cluster failed
  over the round, 0 calls timed out and 0 failed otherwise
  OK: all 150 grains answered AskGrain without GrainOf (0 asks had to be repeated)

round 2: 300 grains, a replacement node joined, node 2 stops under load
  16 callers on each of 2 node(s)
  the node left the cluster in 249ms
  sent 109345 asks, 109344 tells and 109344 one-way tells
  OK: the 218601 acknowledged asks and tells each ran exactly once
  BUG: 53 calls were handed the refusal of the stopping node
  OK: no call that started after the node had left the cluster failed
  over the round, 0 calls timed out and 54 failed otherwise
    53 x context canceled
    1 x redis: client is closed
  OK: all 300 grains answered AskGrain without GrainOf (0 asks had to be repeated)

whole run:
  OK: none of the 677307 messages that ran did so more than once
  OK: no grain was active on two live nodes at the same time

FAIL: a rolling restart under load broke at least one guarantee
exit status 1
```

### Expected (with the feature)

```
round 1: 150 grains spread over three nodes, node 3 stops under load
  16 callers on each of 2 node(s)
  the node left the cluster in 283ms
  sent 111381 asks, 111382 tells and 111381 one-way tells
  OK: the 222763 acknowledged asks and tells each ran exactly once
  OK: no caller was handed the refusal of the stopping node
  OK: no call that started after the node had left the cluster failed
  over the round, 0 calls timed out and 0 failed otherwise
  OK: all 150 grains answered AskGrain without GrainOf (0 asks had to be repeated)

round 2: 300 grains, a replacement node joined, node 2 stops under load
  16 callers on each of 2 node(s)
  the node left the cluster in 350ms
  sent 114288 asks, 114287 tells and 114288 one-way tells
  OK: the 228575 acknowledged asks and tells each ran exactly once
  OK: no caller was handed the refusal of the stopping node
  OK: no call that started after the node had left the cluster failed
  over the round, 0 calls timed out and 0 failed otherwise
  OK: all 300 grains answered AskGrain without GrainOf (0 asks had to be repeated)

whole run:
  OK: none of the 677434 messages that ran did so more than once
  OK: no grain was active on two live nodes at the same time

PASS: through a rolling restart under load no message ran twice, no acknowledged message was lost, no grain ran on two nodes, no caller saw a refusal or a failure once the node had left, and every grain stayed reachable without GrainOf
```

## Crash mode

### Actual (before the feature)

The grains of the killed node stay unreachable: their records still name it,
and nothing but `GrainOf` releases them.

```
round 1: 150 grains spread over three nodes, node 3 is killed under load
  16 callers on each of 1 node(s)
  the node left the cluster in 6.78s
  sent 62797 asks, 62798 tells and 62798 one-way tells
  OK: the 93200 acknowledged asks and tells each ran exactly once
  OK: no caller was handed the refusal of the stopping node
  BUG: 47799 calls that started after the node had left the cluster failed
    47799 x dial tcp 127.0.0.1:54126: connect: connection refused
  over the round, 0 calls timed out and 48591 failed otherwise
    50 x cluster registry read timed out: context deadline exceeded
    234 x dial tcp 127.0.0.1:54125: connect: connection refused
    48294 x dial tcp 127.0.0.1:54126: connect: connection refused
    10 x read tcp 127.0.0.1:54155->127.0.0.1:54126: read: connection reset by peer
    3 x redis: client is closed
  BUG: 50 of 150 grains did not answer AskGrain after the node left

round 2: 300 grains, a replacement node joined, node 2 is killed under load
  16 callers on each of 1 node(s)
  the node left the cluster in 5.868s
  sent 72655 asks, 72656 tells and 72655 one-way tells
  OK: the 83939 acknowledged asks and tells each ran exactly once
  OK: no caller was handed the refusal of the stopping node
  BUG: 82791 calls that started after the node had left the cluster failed
    55191 x dial tcp 127.0.0.1:54123: connect: connection refused
    27600 x dial tcp 127.0.0.1:54126: connect: connection refused
  over the round, 0 calls timed out and 92057 failed otherwise
    32 x cluster registry read timed out: context deadline exceeded
    179 x dial tcp 127.0.0.1:54122: connect: connection refused
    55599 x dial tcp 127.0.0.1:54123: connect: connection refused
    36234 x dial tcp 127.0.0.1:54126: connect: connection refused
    5 x read tcp 127.0.0.1:54154->127.0.0.1:54123: read: connection reset by peer
    8 x redis: client is closed
  BUG: 150 of 300 grains did not answer AskGrain after the node left

whole run:
  OK: none of the 265964 messages that ran did so more than once
  OK: no grain was active on two live nodes at the same time

FAIL: a rolling restart under load broke at least one guarantee
exit status 1
```

### Expected (with the feature)

```
round 1: 150 grains spread over three nodes, node 3 is killed under load
  16 callers on each of 1 node(s)
  the node left the cluster in 5.726s
  sent 39514 asks, 39515 tells and 39515 one-way tells
  OK: the 78730 acknowledged asks and tells each ran exactly once
  OK: no caller was handed the refusal of the stopping node
  OK: no call that started after the node had left the cluster failed
  2 calls that started after the node had left ran into the registry still converging (retryable)
  over the round, 0 calls timed out and 448 failed otherwise
    1 x EOF
    49 x cluster registry read timed out: context deadline exceeded
    216 x dial tcp 127.0.0.1:60450: connect: connection refused
    171 x dial tcp 127.0.0.1:60451: connect: connection refused
    11 x redis: client is closed
  OK: all 150 grains answered AskGrain without GrainOf (0 asks had to be repeated)

round 2: 300 grains, a replacement node joined, node 2 is killed under load
  16 callers on each of 1 node(s)
  the node left the cluster in 8.771s
  sent 35384 asks, 35384 tells and 35384 one-way tells
  OK: the 70478 acknowledged asks and tells each ran exactly once
  OK: no caller was handed the refusal of the stopping node
  OK: no call that started after the node had left the cluster failed
  over the round, 4 calls timed out and 429 failed otherwise
    36 x cluster registry read timed out: context deadline exceeded
    124 x dial tcp 127.0.0.1:60447: connect: connection refused
    246 x dial tcp 127.0.0.1:60448: connect: connection refused
    1 x dial tcp 127.0.0.1:60448: connect: connection refused
    14 x read tcp 127.0.0.1:60479->127.0.0.1:60448: read: connection reset by peer
    8 x redis: client is closed
  OK: all 300 grains answered AskGrain without GrainOf (0 asks had to be repeated)

whole run:
  OK: none of the 224274 messages that ran did so more than once
  OK: no grain was active on two live nodes at the same time

PASS: through a rolling restart under load no message ran twice, no acknowledged message was lost, no grain ran on two nodes, no caller saw a refusal or a failure once the node had left, and every grain stayed reachable without GrainOf
```

## The calls that still fail in crash mode

A killed node cannot say it is gone. The cluster needs a few seconds to notice
(5 to 9 seconds here), and until then:

- calls to the grains of the killed node fail with `connection refused` or
  `connection reset by peer`. The node may only be unreachable, so its grains
  stay where they are until the membership confirms it has left;
- registry reads and writes routed to the killed node fail or time out.

These are the failures listed under "over the round". Once the membership has
dropped the node, a call to one of its grains releases the record and delivers
the message in the same call.

Right after that, the registry itself can still be converging, and a read can
run into its timeout. The sample reports these calls as "ran into the registry
still converging". The registry documents that error, `ErrClusterRegistryTimeout`,
as inconclusive and retryable.
