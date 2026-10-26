# 6. Mailboxes

## Contents

- [What you will learn](#what-you-will-learn)
- [6.1 The contract](#61-the-contract)
  - [Who owns a dequeued context](#who-owns-a-dequeued-context)
- [6.2 The default mailbox](#62-the-default-mailbox)
- [6.3 Choosing a mailbox](#63-choosing-a-mailbox)
- [6.4 Bounded mailboxes](#64-bounded-mailboxes)
  - [`BoundedMailbox`: blocking](#boundedmailbox-blocking)
  - [`NonBlockingBoundedMailbox`: dropping](#nonblockingboundedmailbox-dropping)
- [6.5 Priority mailboxes](#65-priority-mailboxes)
- [6.6 The fair mailbox](#66-the-fair-mailbox)
- [6.7 The segmented mailbox](#67-the-segmented-mailbox)
- [6.8 Stops, restarts and other nodes](#68-stops-restarts-and-other-nodes)
- [6.9 Writing your own mailbox](#69-writing-your-own-mailbox)
- [Guarantees](#guarantees)
- [Implementation details (may change)](#implementation-details-may-change)
- [Behaviours to know](#behaviours-to-know)

## What you will learn

- What the `Mailbox` interface promises, and the stronger contract the dispatcher silently relies on.
- How the default mailbox works: a lock-free multi-producer, single-consumer queue of reusable segments built into the PID, which a local send fills without a pooled context.
- How each of the nine mailboxes is built, and what it costs: bounded or not, blocking or dropping, FIFO, priority or fair.
- Who owns a `ReceiveContext` once it has been dequeued, and when it goes back to the pool.
- What happens to a mailbox when its actor stops, restarts, or is placed on another node.
- What you must get right to write a mailbox of your own.

## 6.1 The contract

`Mailbox` has five methods (`actor/mailbox.go`): `Enqueue`, `Dequeue`, `IsEmpty`, `Len` and `Dispose`. Its comment asks for "a thread-safe FIFO", but the priority and fair mailboxes are not FIFO, so read that as "the order the mailbox defines".

Only user messages go through the mailbox. `doReceive` sends control messages (`PoisonPill`, `Panicking`, `PausePassivation`, `ResumePassivation`, `PanicSignal`, `Terminated`, `SendDeadletter`) to the separate system queue instead (`isControlMessage` in `actor/pid.go`), which a turn serves first ([Chapter 7](chap-07.md)). The async request and response messages used by reentrancy stay in the mailbox, so they keep their place relative to user messages (`isControlMessage` in `actor/pid.go`).

The two sides of the mailbox run on different goroutines:

- **Producers** are whoever sends: `doReceive` calls `Enqueue` on the sender's goroutine (`actor/pid.go`).
- **The consumer** is the actor's turn. `runTurn` calls `Dequeue` until it is empty or the throughput budget is spent (`actor/pid.go`), and `finishOrReclaim` calls `IsEmpty` before releasing the actor. Successive turns may run on different worker goroutines, but never two at once: the dispatch state hands the actor from one worker to the next ([Chapter 7](chap-07.md)).

Reading those call sites gives the contract a mailbox really has to meet. The interface comment states only the first point:

1. **One consumer at a time, many producers.** Every built-in mailbox is designed for this (MPSC).
2. **`Dequeue` returns nil when empty, and does not block.** A blocking `Dequeue` would park a dispatcher worker.
3. **Once `Enqueue` returns, `IsEmpty` must report the message until it is dequeued.** `doReceive` enqueues first and only then tries to schedule the actor (`actor/pid.go`). If that attempt loses because a turn is still running, the message is picked up only because `finishOrReclaim` resets the dispatch state *and then* checks `IsEmpty`. A mailbox whose `IsEmpty` could miss a completed enqueue would leave that message stranded until the next send.
4. **An `Enqueue` error drops the message.** `doReceive` logs the error, turns the message into a dead letter, and returns (`actor/pid.go`). The sender is not told: `Tell` has already returned nil, and an `Ask` waits until it times out.

Together with the single consumer, a FIFO mailbox gives the ordering guarantee users rely on: **messages from one sender to one receiver are handled in the order they were sent**. Nothing is promised across senders. The guarantee is the mailbox's, so it holds for the FIFO mailboxes only; the priority, fair and segmented mailboxes define their own order ([§6.5](#65-priority-mailboxes) to [§6.7](#67-the-segmented-mailbox)), and control messages overtake user messages ([Chapter 7, §7.4](chap-07.md#74-the-system-queue)).

`Len` is not part of the hot path. The `actor.mailbox.size` metric is computed from two counters the actor keeps, one written by producers and one by the turn, on separate cache lines (`PID.observedMailboxSize` in `actor/pid.go`). The interface comment warns that `Len` walks the queue, every node of an `UnboundedMailbox` and every segment of the default mailbox, and is longest when the mailbox is saturated (`Mailbox` in `actor/mailbox.go`).

### Who owns a dequeued context

A `ReceiveContext` taken from a mailbox is still in use while the handler runs, and it is pooled. The rule, stated in `dispatchOne`'s comment (`actor/pid.go`), is that the mailbox releases a context on the **next** `Dequeue`, not the caller:

| Mailbox | When a dequeued context goes back to the pool |
|---|---|
| Default | a context that came in through `Enqueue` is held and recycled on the next `Dequeue`; a message a `Tell` stored as values comes out in the actor's own turn context, which is never pooled ([§6.2](#62-the-default-mailbox)) |
| `UnboundedMailbox` | it stays as the list's sentinel and is recycled on the next `Dequeue` |
| `NonBlockingBoundedMailbox`, the two bounded priority mailboxes, `UnboundedStablePriorityMailbox` | kept in `prev` and recycled on the next `Dequeue` |
| `BoundedMailbox`, `UnboundedPriorityMailBox`, `UnboundedSegmentedMailbox` | never; it is left to the garbage collector |
| `UnboundedFairMailbox` | by the sender's own sub-queue, an `UnboundedMailbox`, on that sender's next message |

A context can be linked into only one mailbox at a time, because the linked mailboxes use its `next` field as the list link (`UnboundedMailbox` in `actor/unbounded_mailbox.go`).

## 6.2 The default mailbox

An actor spawned without `WithMailbox` gets the **embedded mailbox**, built into the PID: two segment pointers, `mailboxHead` and `mailboxTail`, and a small `embeddedMailboxState` that `newPID` allocates, with `(*embeddedMailbox)(pid)` installed as the mailbox, a pointer conversion that allocates nothing (`newPID` in `actor/pid.go`; `embeddedMailbox` in `actor/embedded_mailbox.go`). It is a multi-producer, single-consumer FIFO queue, but not a linked list of contexts: it stores messages in the slots of fixed-size **segments** of 32 (`mailboxSegmentSlots` in `actor/embedded_mailbox.go`), in the manner of the segmented array queues of JCTools and .NET's `ConcurrentQueue`.

Storing messages in slots is what lets a local send skip the context pool. `Tell` writes the message, its sender and its context into a slot as values (`PID.putValue` in `actor/pid.go`; `embeddedMailbox.put` in `actor/embedded_mailbox.go`). When the turn dequeues it, the mailbox fills the actor's one **turn context** with those values and hands that over, refilled for every such message (`embeddedMailbox.take` in `actor/embedded_mailbox.go`; `ReceiveContext.reuse` in `actor/receive_context.go`). A message that needs a full `ReceiveContext`, such as an `Ask`, a message that arrived over remoting, or a stash replay, comes in through `Enqueue` and travels in the slot as its context, in the same queue, so the two kinds keep their order. The reason is recorded in the slot's comment: a pooled context per message meant, under a backlog, one heap allocation per message once the pool ran dry (`mailboxSlot` in `actor/embedded_mailbox.go`).

The algorithm:

- **Claiming a slot.** A producer claims the next slot of the tail segment with a fetch-and-add on the segment's `cursor`, which always succeeds, so many senders to one actor never retry each other's claims. It writes the slot and then publishes it by storing `slotReady` (`embeddedMailbox.claim` and `embeddedMailbox.claimIn` in `actor/embedded_mailbox.go`).
- **Growing.** The producer whose claim lands just past the end of a full segment links the next one, the spare if there is one, and makes it the tail; producers that land further past wait for that link (`embeddedMailbox.link` and `embeddedMailbox.awaitLink` in `actor/embedded_mailbox.go`). The first segment is allocated with the first message, as both head and tail, so an actor that never receives a message holds no segment, and messages that arrive before its first turn are all found from the first segment on (`embeddedMailbox.firstSegment` in `actor/embedded_mailbox.go`).
- **Dequeue.** The consumer reads the slot at its read index. An empty slot means the mailbox is empty; a ready one is emptied and returned. At the end of a segment it moves to the next one (`embeddedMailbox.Dequeue` in `actor/embedded_mailbox.go`).
- **IsEmpty** loads the slot states from the read index on, and nothing else, so a worker that has just given the actor up may call it while the next owner dequeues (`embeddedMailbox.IsEmpty` in `actor/embedded_mailbox.go`).

Segments are reused rather than left to the garbage collector, in two ways:

- **Rewinding.** When the consumer has read every slot claimed in its segment, it restarts the segment at slot 0 with a compare-and-swap on the cursor, which succeeds only if no producer has claimed a slot since. An actor whose backlog never exceeds one segment therefore keeps reusing it (`embeddedMailbox.rewind` in `actor/embedded_mailbox.go`).
- **The spare.** A segment the consumer has finished becomes the spare that the next link takes (`embeddedMailbox.recycle` in `actor/embedded_mailbox.go`). A segment that is still the tail is left alone: its successor is linked but not yet published as the tail, and producers that land past its end are still waiting on it.

Reuse is made safe by a **generation** in the high 32 bits of the cursor, which both kinds of reuse increment. A producer reads the generation before it claims. If the claim comes back with another generation, the segment was reused in between: the producer marks the slot it claimed in the new generation `slotAbandoned`, so the consumer skips it instead of waiting for it, and tries again (`embeddedMailbox.claimIn` in `actor/embedded_mailbox.go`). A producer waiting for the link of a full segment also stops waiting when the generation changes, since the segment may have become the tail again (`embeddedMailbox.awaitLink` in `actor/embedded_mailbox.go`).

The generation alone is not enough. A producer may load the tail, stall while the consumer finishes that segment and keeps it as the spare, and only then read the generation, which is the spare's new one: its claim would match, and its message would land in a segment that is not linked into the queue, to be delivered after later messages if the spare is linked again, and never otherwise. So after reading the generation, a producer checks that the segment is still the tail, and loads the tail again if not (`embeddedMailbox.claimIn` in `actor/embedded_mailbox.go`). A segment that passes the check is either the tail of the generation read, or is reused before the claim, which the generation check catches; one that is linked past before the claim is full, so the claim lands past its end.

Together these keep the mailbox FIFO, which is what lets the actor handle its messages one at a time in the order they were sent. A claim succeeds only in the current tail, slots are claimed in increasing order within a segment, and segments are read in the order they were linked, so the claim order is the delivery order: a producer's messages come out in the order it sent them, and a message whose send returned comes out before any message sent after that. The consumer never reads past a claimed slot whose message is not yet written; it stops there, and skips only abandoned slots, which hold no message.

Between a claim and its publication, the slot is claimed but empty. During that window `Dequeue` and `IsEmpty` see an empty mailbox, and a rewind is refused. Contract point 3 still holds, because the window closes before the send returns, and the producer schedules the actor only after that.

`UnboundedMailbox` (`actor/unbounded_mailbox.go`) is the linked-list alternative, a multi-producer, single-consumer list with a sentinel node in a standalone object with padded head and tail: `Enqueue` swaps the new context in as the tail and links the previous tail to it, and `Dequeue` makes the head's successor the new sentinel and recycles the old one. You get it by asking for it with `WithMailbox(NewUnboundedMailbox())`, and the stash uses one as its buffer (`withStash` in `actor/pid_option.go`).

## 6.3 Choosing a mailbox

| Mailbox | Capacity | When full | Order | Producer cost | `Dispose` |
|---|---|---|---|---|---|
| default (embedded), `UnboundedMailbox` | unbounded | n/a | FIFO | one atomic swap | no-op |
| `UnboundedSegmentedMailbox` | unbounded, 256-slot segments | n/a | FIFO | one atomic add, a new segment every 256 messages | no-op |
| `UnboundedFairMailbox` | unbounded | n/a | FIFO per sender, round-robin across senders | map lookup per message | no-op |
| `UnboundedPriorityMailBox` | unbounded | n/a | priority; ties unspecified | mutex and heap push | no-op |
| `UnboundedStablePriorityMailbox` | unbounded | n/a | priority; ties in arrival order | lock-free push | no-op |
| `BoundedMailbox` | rounded up to a power of two | **blocks a local sender**; a remote message gets `ErrMailboxFull` and is dead-lettered | FIFO | spin until a slot frees (local); retry a lost slot race, refuse a full ring (remote) | disposes the ring |
| `NonBlockingBoundedMailbox` | rounded up to a power of two, at least 2 | `ErrMailboxFull`, message dead-lettered | FIFO | lock-free | no-op |
| `BoundedPriorityMailbox` | exactly `capacity` | `ErrMailboxFull`, message dead-lettered | priority; ties unspecified | lock-free | no-op |
| `BoundedStablePriorityMailbox` | exactly `capacity` | `ErrMailboxFull`, message dead-lettered | priority; ties in arrival order | lock-free | no-op |

Every mailbox is passed as a value to `WithMailbox` (or `WithFuncMailbox` for function actors, `actor/func_actor.go`). Each actor needs its own instance: two actors sharing one would be two consumers on a single-consumer queue.

## 6.4 Bounded mailboxes

### `BoundedMailbox`: blocking

`BoundedMailbox` wraps the ring buffer from `github.com/Workiva/go-datastructures/queue` (`NewBoundedMailbox` in `actor/bounded_mailbox.go`). Three properties come from that library rather than from GoAkt:

- **The capacity is rounded up to a power of two.** `NewBoundedMailbox(10)` holds 16 messages.
- **A full mailbox blocks a local sender.** `Put` loops with `runtime.Gosched()` until a slot frees (`BoundedMailbox.Enqueue` in `actor/bounded_mailbox.go`). The sender's goroutine stays busy for that whole time. When the sender is another actor, that goroutine is a dispatcher worker, and it is not available to run any actor, including, possibly, the one it is waiting for.
- **A full mailbox refuses a remote message.** A message that arrived over remoting, by tell or by ask, carries the `remote` mark of its `ReceiveContext`, set by `actorSystem.handleRemoteTellHeld` and `actorSystem.handleRemoteAsk` in `actor/actor_system.go` and cleared by `ReceiveContext.reset`; `cloneContext` does not copy it, since a clone is enqueued by the actor's own turn. For such a message `Enqueue` loops on the ring's `Offer` instead of `Put`. `Offer` reports false both for a full ring and for a slot race lost to another producer, so the loop retries until it claims a slot or sees the ring full (`Len` at `Cap`), and returns `ErrMailboxFull` only then; `PID.doReceive` then dead-letters the message and repays its credit share ([Chapter 15](chap-15.md)). A refused ask is answered by its own timeout, as on a `NonBlockingBoundedMailbox`. The producer of a remote message is the connection's inbound dispatcher, the read goroutine or its drainer, and a `Put` that spun there stopped the lane for every receiver sharing it, asks and keepalives included, until the slow actor's mailbox drained. `remoteHold` is not the mark: a chunked message or a lease-less path delivers without a hold.
- **`Dispose` is final.** After it, every `Put` fails with `ErrDisposed`.

`Dequeue` does not block: it calls the ring's blocking `Get` only after checking that the length is positive, and returns nil otherwise (`actor/bounded_mailbox.go`). That is what contract point 2 requires.

### `NonBlockingBoundedMailbox`: dropping

`NonBlockingBoundedMailbox` is Dmitry Vyukov's bounded queue, written in GoAkt (`actor/non_blocking_bounded_mailbox.go`). Every cell carries a sequence number. A producer claims a cell by advancing `enqueuePos` with a compare-and-swap when the cell's sequence equals the position, then stores the message and publishes it by advancing the sequence. When the cell still holds an unconsumed message, the sequence is behind the position, and `Enqueue` returns `ErrMailboxFull` instead of waiting. The consumer does the mirror image (`NonBlockingBoundedMailbox.Dequeue` in `actor/non_blocking_bounded_mailbox.go`).

The capacity is rounded up to a power of two, with a floor of two, so a position maps to a cell with a mask (`nextPowerOfTwo` in `actor/non_blocking_bounded_mailbox.go`). `Len` is the difference between the two positions. A producer that has claimed a cell but not yet published it counts in `Len` while `Dequeue` still returns nil. The turn then sees "not empty" and "nothing to take" at the same time; `finishOrReclaim` keeps the actor scheduled, and the turn retries until the message is published or its budget runs out.

## 6.5 Priority mailboxes

A `PriorityFunc` is `func(msg1, msg2 any) bool`, and returning true means `msg1` goes first (`heap.Less` in `actor/unbounded_priority_mailbox.go`). In the three intake-based mailboxes it is called on the consumer's goroutine, during `Dequeue`, for every comparison the heap makes. In `UnboundedPriorityMailBox` it also runs on the sender's goroutine, because `Enqueue` pushes into the heap under the mutex. It receives every message that enters the mailbox, including the reentrancy messages from [§6.1](#61-the-contract), so it must handle types it does not know.

There are two designs:

- **`UnboundedPriorityMailBox`** pushes into a `container/heap` under a mutex (`UnboundedPriorityMailBox.Enqueue` and `UnboundedPriorityMailBox.Dequeue` in `actor/unbounded_priority_mailbox.go`). A binary heap is not stable, so messages the function ranks equally come out in no particular order. Its comment says so, and recommends the stable variant when ties must keep their order.
- **The other three** split producers from the heap. Producers push onto a lock-free stack, the *intake* (`actor/priority_intake.go`). Before each `Dequeue`, the consumer takes the whole stack in one swap and reverses it into arrival order (`priorityIntake.drain` in `actor/priority_intake.go`), then pushes it into a heap only it touches. Producers never contend with the consumer, and nothing is allocated per message.

The stable variants give each drained message an increasing sequence number and use it to break ties (`stableHeap.less` in `actor/unbounded_stable_priority_mailbox.go`). Deciding a tie takes two calls to the priority function, one in each direction.

The bounded variants admit a message by incrementing the length first and undoing it if that exceeds the capacity (`BoundedPriorityMailbox.Enqueue` in `actor/bounded_priority_mailbox.go`). Unlike the ring-based mailboxes, their capacity is exact. The stable unbounded variant also increments the length before pushing (`UnboundedStablePriorityMailbox.Enqueue` in `actor/unbounded_stable_priority_mailbox.go`), so briefly `IsEmpty` is false while the intake is still empty. That is the same harmless retry as in [§6.4](#64-bounded-mailboxes).

Priority is decided only among messages already in the mailbox when the turn takes one: a high-priority message that arrives later still has to wait for the message being handled.

## 6.6 The fair mailbox

`UnboundedFairMailbox` gives every sender its own sub-queue and serves the senders in turn, so a chatty sender cannot starve the others (`actor/unbounded_fair_mailbox.go`):

- The sender key is `Sender().ID()` (`deriveSenderKey` in `actor/unbounded_fair_mailbox.go`). Every package-level `actor.Tell` and `actor.Ask` comes from NoSender, so all of them share one sub-queue.
- Each sub-queue is an `UnboundedMailbox`. A sender becomes *active*, and joins a lock-free queue of active senders, when its count of pending messages goes from zero to one (`UnboundedFairMailbox.Enqueue` in `actor/unbounded_fair_mailbox.go`).
- `Dequeue` takes the next active sender, pops one of its messages, and puts the sender back at the end of the active queue if it has more (`UnboundedFairMailbox.finalizeSender` in `actor/unbounded_fair_mailbox.go`).

`finalizeSender` handles the race where a sender's last message is taken just as a new one arrives. It marks the sender inactive, then checks the pending count again, and re-activates it if a message slipped in between (`actor/unbounded_fair_mailbox.go`).

Nothing removes a sender's sub-queue, so the mailbox keeps one per distinct sender for as long as it lives. Each idle sub-queue also keeps its last message, which stays as the sentinel until that sender sends again. The type's comment warns about this (`UnboundedFairMailbox` in `actor/unbounded_fair_mailbox.go`).

## 6.7 The segmented mailbox

`UnboundedSegmentedMailbox` stores messages in arrays of 256 slots linked into a list (`segmentSize`, `segment` and `newSegment` in `actor/unbounded_segmented_mailbox.go`):

- **`Enqueue`** loads the tail segment, reserves a slot with an atomic add on its write index, and stores the message there. When the index passes 255, the producer links a new segment, or helps move the tail to one another producer linked, and retries (`actor/unbounded_segmented_mailbox.go`).
- **`Dequeue`** reads slots in order. A slot that is reserved but not yet written makes it return nil, "not yet published; treat as empty" (`actor/unbounded_segmented_mailbox.go`).

Two details keep the list consistent under concurrent producers:

- **A segment is left only when all 256 of its slots have been dequeued.** The write index the consumer read may predate reservations made just before a producer linked the next segment. Linking happens only after a reservation past the end, so a successor means every slot here is reserved; leaving on the stale count would abandon the late ones (`UnboundedSegmentedMailbox.Dequeue` in `actor/unbounded_segmented_mailbox.go`).
- **Segments are never reused.** A producer may still hold a pointer to a segment the consumer has drained, because it loaded the tail just before the segment filled. A reused segment would let that producer reserve a slot in another queue. A drained segment is left to the garbage collector; a stale producer finds it full and moves on to the tail (`newSegment` in `actor/unbounded_segmented_mailbox.go`). The cost is one allocation per 256 messages.

## 6.8 Stops, restarts and other nodes

**Stop.** Every stop ends in `reset` (`actor/pid.go`). Once the actor is stopping, a turn that is still running hands out no more user messages (`PID.runTurn` in `actor/pid.go`), so the queued messages stay in the mailbox. On a terminal stop they are abandoned with the PID, and none of them becomes a dead letter; `reset` then disposes of the mailbox. Remote messages among them hold flow-control credit, which `reset` returns to their peers through the actor's hold registry.

**Restart.** A restart reuses the same PID and the same mailbox ([Chapter 4, §4.7](chap-04.md#47-looking-up-killing-and-restarting-by-name)), and keeps the messages in it:

- The message in flight when the restart begins finishes on the old incarnation. The turn then stops handing out user messages, as for any stop, and `Restart` waits for it to give the actor up before it re-initializes the actor (`restartSubtree` in `actor/pid.go`).
- `reset` does not dispose of the mailbox, and keeps the two counters behind `actor.mailbox.size`, when the stop belongs to a restart (`actor/pid.go`). That matters for `BoundedMailbox`, whose `Dispose` is final (`actor/bounded_mailbox.go`).
- The new incarnation handles the queued messages after its `PostStart`.

Messages sent while the restart is under way are refused: the actor is not running.

**Other nodes.** A mailbox is a Go value and does not travel. The remote spawn request has no field for it (`actor/spawn.go`), and neither has the record relocation rebuilds an actor from (`wireSpawnOptions`, `actor/spawn.go`). An actor that `SpawnOn` places on another node, that `Spawn` creates there through `WithHostAndPort`, or that relocation re-creates after its node left, runs on the default mailbox, whatever `WithMailbox` said.

## 6.9 Writing your own mailbox

A custom mailbox is passed to `WithMailbox` like the built-in ones. From [§6.1](#61-the-contract), it must:

1. Accept concurrent `Enqueue` calls and assume one `Dequeue` caller at a time, possibly a different goroutine each turn. Publish its state with atomics or a lock, never with plain fields shared across turns.
2. Return nil from `Dequeue` when empty, without blocking.
3. Never let `IsEmpty` return true for a message whose `Enqueue` has returned and that has not been dequeued.
4. Keep a dequeued context valid until the next `Dequeue`. Recycling it is optional; if you do, use the same reset as the built-ins (`recycleContext`, `actor/context_pool.go`), which is internal to the package.
5. Treat an `Enqueue` error as "drop this message": the actor turns it into a dead letter.
6. Make `Dispose` safe to call on every stop, including the one inside a restart, after which the same mailbox is used again.

## Guarantees

| Statement | Enforced by |
|---|---|
| The default spawn uses the embedded mailbox; `WithMailbox` replaces it | `TestEmbeddedMailboxDefaultSpawnUsesIt` in `actor/embedded_mailbox_test.go` |
| The default mailbox is FIFO across segments, with many concurrent producers, and messages stored as values keep their order with enqueued contexts | `TestEmbeddedMailboxFIFOOrder`, `TestEmbeddedMailboxConcurrentProducers` and `TestEmbeddedMailboxValuesAndContextsKeepOrder` in `actor/embedded_mailbox_test.go` |
| The default mailbox holds an enqueued context until the next `Dequeue` and then recycles it; a value message leaves no context behind, and a read slot keeps no reference to its message | `TestEmbeddedMailboxRecyclesHeldContext` and `TestEmbeddedMailboxTakeClearsTheSlot` in `actor/embedded_mailbox_test.go` |
| The first segment appears with the first message, and messages that fill several segments before the first turn all come out | `TestEmbeddedMailboxAllocatesFirstSegmentOnFirstMessage`, `TestEmbeddedMailboxBacklogBeforeFirstTurn` and `TestEmbeddedMailboxFirstSegmentRaceKeepsWinner` in `actor/embedded_mailbox_test.go` |
| A drained segment is rewound, never past a claimed slot; a finished one becomes the spare the next link takes, unless it is still the tail | `TestEmbeddedMailboxRewindsDrainedSegment`, `TestEmbeddedMailboxDoesNotRewindPastAClaim`, `TestEmbeddedMailboxReusesSpareSegment` and `TestEmbeddedMailboxRecycleSkipsSegmentStillTail` in `actor/embedded_mailbox_test.go` |
| A claim under a stale generation is abandoned and skipped, and a producer waiting on a reused segment stops waiting | `TestEmbeddedMailboxStaleClaimIsAbandoned`, `TestEmbeddedMailboxStaleClaimPastTheEnd` and `TestEmbeddedMailboxAwaitLinkReturns` in `actor/embedded_mailbox_test.go` |
| A producer claims nothing in a segment that is no longer the tail, such as the spare, even when it read the spare's current generation | `TestEmbeddedMailboxClaimRefusesSegmentNoLongerTail` in `actor/embedded_mailbox_test.go` |
| `NonBlockingBoundedMailbox` rounds its capacity to a power of two and returns `ErrMailboxFull` when full | `TestNonBlockingBoundedMailbox` in `actor/non_blocking_bounded_mailbox_test.go` |
| The bounded priority mailboxes return `ErrMailboxFull` when full | `TestBoundedPriorityMailbox` in `actor/bounded_priority_mailbox_test.go` |
| The stable priority mailboxes keep arrival order among equal priorities | `TestUnboundedStablePriorityMailbox` in `actor/unbounded_stable_priority_mailbox_test.go`; `TestBoundedStablePriorityMailbox` in `actor/bounded_stable_priority_mailbox_test.go` |
| The fair mailbox keeps each sender's order and starves no sender | `TestUnboundedFairMailboxPreservesPerSenderOrdering` and `TestUnboundedFairMailboxNoStarvation` in `actor/unbounded_fair_mailbox_test.go` |
| A restart keeps the mailbox: queued messages reach the new incarnation, and a `BoundedMailbox` still accepts messages | `TestRestartKeepsMailbox` in `actor/pid_test.go` |
| The segmented mailbox loses nothing under concurrent producers, and never hands a drained segment to another mailbox | `TestUnboundedSegmentedMailbox_DrainedSegmentIsNotReused` and `TestUnboundedSegmentedMailbox_ConcurrentMailboxesLoseNothing` in `actor/unbounded_segmented_mailbox_test.go` |
| The segmented mailbox keeps FIFO order across segment boundaries | `TestUnboundedSegmentedMailbox_Dequeue_OrderAcrossSegments` in `actor/unbounded_segmented_mailbox_test.go` |

## Implementation details (may change)

- The embedded mailbox, its segments of 32 slots, rewinding and the single spare, and the placement of its two pointers in the PID.
- The segment size of 256, and allocating a new segment instead of reusing one.
- The lock-free intake in front of the priority heaps.
- Which mailboxes recycle contexts.

## Behaviours to know

| Behaviour | Source |
|---|---|
| An actor on the default mailbox holds no segment until its first message, then keeps its segment, about 2 KiB, for as long as it lives, plus at most one spare after a backlog of more than 32 messages. Measured on an 8-core arm64 machine: 1,436 B of heap per idle actor that was never sent a message, about 3.5 KB after one message and about 5.6 KB after a burst of 100, against 1.46 KB for the linked list the default mailbox replaced. Segments of 32 slots were chosen for throughput: 16 halve the extra memory but cost about a fifth of the actor-to-actor throughput | `mailboxSegmentSlots` and `embeddedMailbox.firstSegment` in `actor/embedded_mailbox.go` |
| A message refused by a full bounded mailbox becomes a dead letter; `Tell` still returns nil | `PID.doReceive` in `actor/pid.go` |
| `BoundedMailbox` blocks a local sender, and a sending actor's worker with it | `BoundedMailbox.Enqueue` in `actor/bounded_mailbox.go` |
| A full `BoundedMailbox` refuses a remote message with `ErrMailboxFull` instead of blocking the connection's inbound dispatcher; the overflow is dead-lettered, its credit repaid, and an ask to a sibling on the same lane still answers; a remote message is never refused while the mailbox has room, whatever the contention; a local sender still blocks | `TestBoundedMailboxEnqueueRemoteMessage`, `TestBoundedMailboxEnqueueRemoteUnderContention` and `TestBoundedMailboxEnqueueBlocksLocalSender` in `actor/bounded_mailbox_test.go`; `TestDoReceiveRefusesRemoteOverflowOnBoundedMailbox` in `actor/pid_test.go`; `TestHandleRemoteAskRefusesOverflowOnBoundedMailbox` in `actor/actor_system_test.go`; `TestRemoteTellFullBoundedMailboxKeepsLaneAlive` in `actor/remote_server_test.go`; `playground/issue-1453` |
| `BoundedMailbox` and `NonBlockingBoundedMailbox` round the capacity up to a power of two | `NewBoundedMailbox` in `actor/bounded_mailbox.go`; `nextPowerOfTwo` in `actor/non_blocking_bounded_mailbox.go` |
| Messages queued when an actor stops are abandoned, not dead-lettered; a restart keeps them | `PID.runTurn` and `PID.reset` in `actor/pid.go` |
| `WithMailbox` has no effect on an actor placed on another node or relocated | `actorSystem.SpawnOn` in `actor/spawn.go` |
| The fair mailbox keeps a sub-queue per distinct sender for its whole life | `UnboundedFairMailbox.Enqueue` in `actor/unbounded_fair_mailbox.go` |
| A priority function sees the reentrancy messages too | `isControlMessage` in `actor/pid.go` |
