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
  - New limitation: tombstones are not saved with a snapshot, so a key deleted while a node is away can come back within the TTL after a rolling restart.
- **[Remoting](https://docs.goakt.dev/advanced/remoting) names the right mailbox for a slow remote receiver.**
  - Any bounded mailbox is the mitigation for a slow remote receiver: when full it refuses the remote overflow and returns the credit.
  - The ask worker pool has no upper bound: each ask to a stalled actor holds a goroutine until its deadline.
- **[Grain reentrancy](https://docs.goakt.dev/grains/reentrancy) limitations match the code.**
  - A bounded mailbox filled during a pause refuses user messages with `ErrMailboxFull` and drops timer ticks; the shutdown `PoisonPill` is a system message and is always accepted.
  - Late replies to a fresh activation are dropped and logged at debug level.
- **[Clustering](https://docs.goakt.dev/clustering/overview) explains `WithMinimumPeersQuorum` as the only partition safeguard.**
  - With the default of 1, each side of a split keeps its own leader and relocates the other side's actors, so an actor or singleton can run twice until the split heals.
  - A majority value keeps the minority from recomputing its routing table and from serving registry requests to its peers; it does not stop the minority's running actors.
