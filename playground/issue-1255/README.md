# Issue-1255

For more context check the github [issue-1255](https://github.com/Tochemey/goakt/issues/1255).

Regression harness for the relocation refactoring: it asserts that a node departure at scale
(10,000 actors by default) recovers fully on the surviving nodes, and it prints the wall-clock
time from departure to full recovery. The companion unit test `TestRelocationRPCScaling`
(in `actor/relocation_worker_test.go`) pins the O(peers) RPC guarantee itself: 10,000 actors
over 10 peers must cost exactly 20 `RelocateBatch` RPCs and zero per-actor round trips.

The harness runs two scenarios against a three-node cluster (replica count 2):

1. **Graceful departure**: one node shuts down cleanly; relocation runs from its
   graceful-shutdown snapshot. Every actor it hosted must resolve as local on a survivor.
2. **Crash (SIGKILL)**: the departing node runs as a child OS process and is killed with
   SIGKILL, so no snapshot exists; the leader must derive the relocation set from the
   replicated registry (crash-recovery path).

The program exits non-zero if any actor is not recovered within two minutes.

To run it:

`go run ./playground/issue-1255`

The actor count can be tuned for quicker runs:

`GOAKT_1255_ACTORS=2000 go run ./playground/issue-1255`
