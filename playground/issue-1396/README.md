# Issue 1396: stale owner record after a node accepted activations while stopping

https://github.com/Tochemey/goakt/issues/1396

Two nodes. Node 1 activates 200 grains round-robin, so every other
activation is sent to node 2, while node 2 stops. Once node 2 is gone, node 1
sends to every grain: each must answer, from wherever it lives now.

## Actual (before the fix)

Node 2 kept accepting activations until the end of its shutdown, so some
grains were claimed on it after it had started to leave, and their registry
records outlived it. Node 1 still held its connection to node 2 and was
answered `remoting is not enabled` over it, which did not count as a
departed owner, so the records were never released:

```
FAIL: grain-25: remoting is not enabled
FAIL: 1 of 200 grains unreachable after node 2 left
```

## Expected (after the fix)

A stopping node refuses activations with `actor system is shutting down`,
and a node that answers that it is shutting down or has remoting off is
treated as gone: its record is released and the grain activates on a live
node.

```
PASS: all 200 grains answered after node 2 left
```

## Run

```bash
go run ./playground/issue-1396
```

The race is timing-dependent; on the author's machine the sample fails on
every run before the fix. `TestGrainIdentity_StaleRecordOfAStoppedNodeIsReleasedOverAnOpenConnection`
in `actor` pins the same failure deterministically by planting the record a
late activation leaves behind.
