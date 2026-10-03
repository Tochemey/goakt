# Issue 1437: a node away longer than the tombstone TTL brings a deleted key back

https://github.com/Tochemey/goakt/issues/1437

A deleted key is protected by a tombstone for the tombstone TTL
(`crdt.WithTombstoneTTL`). A node that was away for longer than that and still
holds the key brings it back when it returns: no node retains the tombstone any
more, so anti-entropy treats the returning node's copy as a key the others are
missing.

The sample runs a three-node cluster with a tombstone TTL of 3 seconds. Node 3
keeps its store on disk through CRDT snapshots, so it comes back with the keys
it held when it left. Two keys are on every node when node 3 leaves:

- **beyond**: deleted on node 1 right after node 3 left. Node 3 returns after
  its tombstone expired. It must stay deleted on every node.
- **within**: deleted on node 1 just before node 3 returns, while its tombstone
  is live. It must stay deleted on every node. This is the control:
  anti-entropy carries a live tombstone to the returning node.

## Actual (before the fix)

```
BUG: node 1, beyond (deleted longer than the tombstone TTL before node 3 returned): back with value 1
OK: node 1, within (deleted within the tombstone TTL before node 3 returned): stays deleted
BUG: node 2, beyond (deleted longer than the tombstone TTL before node 3 returned): back with value 1
OK: node 2, within (deleted within the tombstone TTL before node 3 returned): stays deleted
BUG: node 3, beyond (deleted longer than the tombstone TTL before node 3 returned): back with value 1
OK: node 3, within (deleted within the tombstone TTL before node 3 returned): stays deleted
FAIL: 3 of 6 checks show a deleted key back after node 3 returned
exit status 1
```

## Expected (after the fix)

```
OK: node 1, beyond (deleted longer than the tombstone TTL before node 3 returned): stays deleted
OK: node 1, within (deleted within the tombstone TTL before node 3 returned): stays deleted
OK: node 2, beyond (deleted longer than the tombstone TTL before node 3 returned): stays deleted
OK: node 2, within (deleted within the tombstone TTL before node 3 returned): stays deleted
OK: node 3, beyond (deleted longer than the tombstone TTL before node 3 returned): stays deleted
OK: node 3, within (deleted within the tombstone TTL before node 3 returned): stays deleted
PASS: a deleted key stays deleted when a node returns, whether its tombstone has expired or not
```

## Run

```bash
go run ./playground/issue-1437
```
