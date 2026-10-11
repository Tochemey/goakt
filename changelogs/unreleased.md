# Unreleased

## Fixes

- **`PostStart` is delivered once the actor is fully started, registry record included.**
  - The `PostStart` turn was scheduled while the PID was being built, before the tree attachment and, in a cluster, before the registry write.
  - Since v4.6.0 it had its own slot and usually ran first: `ActorSystem.Partition` for the actor's own name returned 0, and `ActorExists` or a lookup from another node did not find it yet.
  - The turn is now scheduled after the tree attachment and the registry write, as a restart already did.
  - One window remains: a message that reaches the actor through a local name lookup between the tree attachment and the registry write runs `PostStart` before the record exists.
  - A spawn whose publication fails does not schedule `PostStart`.
- **A full `BoundedMailbox` on a remote receiver no longer stalls the connection.**
  - `BoundedMailbox.Enqueue` blocks the producer when the mailbox is full. For a message that arrived over remoting the producer is the connection's inbound dispatcher, so one slow actor with a full mailbox stopped the lane for every receiver on it: asks, tells, credit grants and keepalives, until the mailbox drained.
  - A message that arrived over remoting, by tell or by ask, is now refused with `ErrMailboxFull` when the mailbox is full, like on a `NonBlockingBoundedMailbox`: it goes to dead letters and its credit is returned to the sender; a refused ask is answered by its own timeout.
  - Local senders keep the blocking backpressure the mailbox documents; stream stages are unaffected.
  - See `playground/issue-1453`.
- **The remote credit registry no longer grows behind a message parked in a priority mailbox.**
  - The registry that tracks the credit of remote messages resident in an actor retired entries only from the front and stopped at the first message still queued.
  - On a priority mailbox, one low-priority remote message left waiting behind a steady flow of higher-priority ones pinned one entry per message dispatched after it, for as long as it waited.
  - Released entries behind a parked one are now unlinked in place within the same budget; the parked entry stays until its message is dispatched.
- **A grain admits no new request once its system is stopping.**
  - Shutdown cancels a grain's in-flight requests once, then sends the PoisonPill. A `StashNonReentrant` request issued after that pass, with no timeout, paused the grain past the pill: the pill never ran and `OnDeactivate` was skipped.
  - `RequestGrain` already failed at delivery while stopping. A request whose delivery still succeeds, such as `RequestActor` toward an actor on another node, is now refused at admission with `ErrActorSystemNotStarted`, and the grain deactivates.
- **An actor restart replays the messages a request had stashed.**
  - Messages stashed by a `StashNonReentrant` request stayed in the stash box across a restart, since the restart cancels the request without unstashing, and replayed only when a later blocking request completed.
  - A restart keeps queued messages, and these are queued messages: the new incarnation now handles them, in order, after `PostStart`.
  - A stash the actor made on its own with `WithStashing`, with no request in flight, is left alone, as before.
  - A terminal stop now drops the stashed messages with the mailbox, so an actor restarted later under the same name does not meet the messages its dead incarnation had set aside. The stash buffer stays usable.
- **A CRDT snapshot saves the tombstones.**
  - A snapshot saved the store but not the deletions, so a node restored from it held none of its own tombstones. A key deleted while one node was away, then a rolling restart of the holders inside the tombstone TTL, left no node with the tombstone and the returning node brought the key back.
  - The tombstones within the TTL are now saved with the snapshot and restored with it, as Akka's durable storage keeps deleted keys. A snapshot file of an earlier version loads with no tombstones.
- **A restarted node's first CRDT deltas are no longer dropped as duplicates.**
  - The topic actors deduplicate by sender and message ID for the retention window, and the Replicator's message sequence restarted at one with the same sender path. A node that published n messages and restarted within the window had its next n deltas and tombstones dropped by every peer until anti-entropy repaired them.
  - The sequence now starts at the node's start time, so a new incarnation never reuses an ID of the previous one.
- **The Replicator forgets a deleted key once its tombstone expires.**
  - The key's type and its subscription entry were kept forever, one entry per key ever deleted.
  - Pruning now drops them with the expired tombstone. A key stored again by a coordinated read keeps its type. A tombstone still buffered for a remote datacenter is kept until that datacenter accepts it, as before.
- **`LWWRegister.Set` never ties with or loses to the value it replaces.**
  - Two writes from one node with the same clock reading, or a write from a node whose clock was behind the last writer's, produced a register whose merge depended on the order, which anti-entropy could not repair.
  - `Set` now stamps the write one nanosecond past the register's current timestamp when the given time is not later, as Akka's default clock does. A later timestamp is used as given.
  - This is a semantic change for a caller that passes an earlier timestamp on purpose so the write loses on merge: the write now wins. Replicated and decoded state is untouched.

## Documentation

- **Migration guide and dispatcher tuning moved to the user docs.**
  - The v3 to v4 migration guide is on the [documentation site](https://docs.goakt.dev/reference/migration-v3-to-v4), merged with the v4.0.0 API changes page.
  - `WithThroughputBudget` and `WithDispatcherPoolSize` are covered on the [actor system page](https://docs.goakt.dev/actor/actor-system#tuning-the-dispatcher).
  - The Dispatcher Pool and Context Pools pages were folded into the maintainers' book; their old URLs redirect.
- **[Distributed data](https://docs.goakt.dev/advanced/distributed-data) describes what the Replicator does.**
  - `WithMaxDeltaSize` has no effect: no delta falls back to full-state transfer.
  - A coordinated write does not wait for acknowledgements; `ReadFrom` with `WriteTo` narrows the staleness window without guaranteeing read-your-writes.
  - An update to a tombstoned key is dropped and still answered with a normal `UpdateResponse`.
  - New limitation: a node that restarts with the same node ID and an empty or stale store loses its first writes to a key until anti-entropy pulls the key back.
  - New limitation: `ORMap.Remove` followed by `Set` does not reset the value; peers merge the new value into the old one.
- **[Remoting](https://docs.goakt.dev/advanced/remoting) names the right mailbox for a slow remote receiver.**
  - Any bounded mailbox is the mitigation for a slow remote receiver: when full it refuses the remote overflow and returns the credit.
  - The ask worker pool has no upper bound: each ask to a stalled actor holds a goroutine until its deadline.
- **[Grain reentrancy](https://docs.goakt.dev/grains/reentrancy) limitations match the code.**
  - A bounded mailbox filled during a pause refuses user messages with `ErrMailboxFull` and drops timer ticks; the shutdown `PoisonPill` is a system message and is always accepted.
  - Late replies to a fresh activation are dropped and logged at debug level.
- **[Clustering](https://docs.goakt.dev/clustering/overview) explains `WithMinimumPeersQuorum` as the only partition safeguard.**
  - With the default of 1, each side of a split keeps its own leader and relocates the other side's actors, so an actor or singleton can run twice until the split heals.
  - A majority value keeps the minority from recomputing its routing table and from serving registry requests to its peers; it does not stop the minority's running actors.
