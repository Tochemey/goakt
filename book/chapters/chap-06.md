# 6. Mailboxes

Verified against: `cf7a7c6d` and the uncommitted changes of branch `issue-1432` (2026-10-03): every statement checked against the code

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
- [Exercises](#exercises)

## What you will learn

- What the `Mailbox` interface promises, and the stronger contract the dispatcher silently relies on.
- How the default mailbox works: a lock-free multi-producer, single-consumer list embedded in the PID.
- How each of the nine mailboxes is built, and what it costs: bounded or not, blocking or dropping, FIFO, priority or fair.
- Who owns a `ReceiveContext` once it has been dequeued, and when it goes back to the pool.
- What happens to a mailbox when its actor stops, restarts, or is placed on another node.
- What you must get right to write a mailbox of your own.

Source files: `actor/mailbox.go`, every `actor/*_mailbox.go`, `actor/priority_intake.go`, and the turn loop in `actor/pid.go`.

## 6.1 The contract

`Mailbox` has five methods (`actor/mailbox.go`): `Enqueue`, `Dequeue`, `IsEmpty`, `Len` and `Dispose`. Its comment asks for "a thread-safe FIFO", but the priority and fair mailboxes are not FIFO, so read that as "the order the mailbox defines".

Only user messages go through the mailbox. `doReceive` sends control messages (`PoisonPill`, `Panicking`, `PausePassivation`, `ResumePassivation`, `PanicSignal`, `Terminated`, `SendDeadletter`) to the separate system queue instead (`isControlMessage` in `actor/pid.go`), which a turn serves first (Chapter 7). The async request and response messages used by reentrancy stay in the mailbox, so they keep their place relative to user messages (`isControlMessage` in `actor/pid.go`).

The two sides of the mailbox run on different goroutines:

- **Producers** are whoever sends: `doReceive` calls `Enqueue` on the sender's goroutine (`actor/pid.go`).
- **The consumer** is the actor's turn. `runTurn` calls `Dequeue` until it is empty or the throughput budget is spent (`actor/pid.go`), and `finishOrReclaim` calls `IsEmpty` before releasing the actor. Successive turns may run on different worker goroutines, but never two at once: the dispatch state hands the actor from one worker to the next (Chapter 7).

Reading those call sites gives the contract a mailbox really has to meet. The interface comment states only the first point:

1. **One consumer at a time, many producers.** Every built-in mailbox is designed for this (MPSC).
2. **`Dequeue` returns nil when empty, and does not block.** A blocking `Dequeue` would park a dispatcher worker.
3. **Once `Enqueue` returns, `IsEmpty` must report the message until it is dequeued.** `doReceive` enqueues first and only then tries to schedule the actor (`actor/pid.go`). If that attempt loses because a turn is still running, the message is picked up only because `finishOrReclaim` resets the dispatch state *and then* checks `IsEmpty`. A mailbox whose `IsEmpty` could miss a completed enqueue would leave that message stranded until the next send.
4. **An `Enqueue` error drops the message.** `doReceive` logs the error, turns the message into a dead letter, and returns (`actor/pid.go`). The sender is not told: `Tell` has already returned nil, and an `Ask` waits until it times out.

Together with the single consumer, a FIFO mailbox gives the ordering guarantee users rely on: **messages from one sender to one receiver are handled in the order they were sent**. Nothing is promised across senders. The guarantee is the mailbox's, so it holds for the FIFO mailboxes only; the priority, fair and segmented mailboxes define their own order (§6.5 to §6.7), and control messages overtake user messages (Chapter 7, §7.4).

`Len` is not part of the hot path. The `actor.mailbox.size` metric is computed from two counters the actor keeps, one written by producers and one by the turn, on separate cache lines (`PID.observedMailboxSize` in `actor/pid.go`). The interface comment warns that `Len` on the default mailbox walks the whole list (`Mailbox` in `actor/mailbox.go`).

### Who owns a dequeued context

A `ReceiveContext` taken from a mailbox is still in use while the handler runs, and it is pooled. The rule, stated in `dispatchOne`'s comment (`actor/pid.go`), is that the mailbox releases a context on the **next** `Dequeue`, not the caller:

| Mailbox | When a dequeued context goes back to the pool |
|---|---|
| Default, `UnboundedMailbox` | it stays as the list's sentinel and is recycled on the next `Dequeue` |
| `NonBlockingBoundedMailbox`, the two bounded priority mailboxes, `UnboundedStablePriorityMailbox` | kept in `prev` and recycled on the next `Dequeue` |
| `BoundedMailbox`, `UnboundedPriorityMailBox`, `UnboundedSegmentedMailbox` | never; it is left to the garbage collector |
| `UnboundedFairMailbox` | by the sender's own sub-queue, an `UnboundedMailbox`, on that sender's next message |

A context can be linked into only one mailbox at a time, because the linked mailboxes use its `next` field as the list link (`UnboundedMailbox` in `actor/unbounded_mailbox.go`).

## 6.2 The default mailbox

An actor spawned without `WithMailbox` gets the **embedded mailbox**: the same algorithm as `UnboundedMailbox`, run on two pointers stored in the PID itself, `mailboxHead` and `mailboxTail`. `newPID` seeds both with one sentinel context and installs `(*embeddedMailbox)(pid)`, a pointer conversion that allocates nothing (`actor/pid.go`, `actor/embedded_mailbox.go`). The two pointers sit on the cache lines the PID layout already reserves for the consumer and the producers, which is why they need no padding of their own.

The algorithm is a multi-producer, single-consumer linked list with a sentinel node:

- **`Enqueue`** clears the new context's `next`, atomically swaps it in as the tail, and then links the previous tail to it (`actor/embedded_mailbox.go`). There is no lock and no retry loop: a swap always succeeds.
- **`Dequeue`** reads the head's `next`. If it is nil, the mailbox is empty. Otherwise `next` becomes the new head, and the old head is reset and returned to the pool (`actor/embedded_mailbox.go`). The context it returns is now the sentinel, which is why the caller must not release it.
- **`IsEmpty`** is one load of the head's `next` (`actor/embedded_mailbox.go`).

Between the swap and the link, a producer has made its context the tail but not yet reachable from the head. During that window `Dequeue` and `IsEmpty` see an empty mailbox. Contract point 3 still holds, because the window closes before `Enqueue` returns, and the producer schedules the actor only after that.

The old sentinel is reset with an atomic store to its `next` field, because a worker that read the head just before it moved may still be loading that field through `IsEmpty` (`embeddedMailbox.Dequeue` in `actor/embedded_mailbox.go`).

`UnboundedMailbox` (`actor/unbounded_mailbox.go`) is the same list in a standalone object with padded head and tail. You get it by asking for it with `WithMailbox(NewUnboundedMailbox())`, and the stash uses one as its buffer (`withStash` in `actor/pid_option.go`).

## 6.3 Choosing a mailbox

| Mailbox | Capacity | When full | Order | Producer cost | `Dispose` |
|---|---|---|---|---|---|
| default (embedded), `UnboundedMailbox` | unbounded | n/a | FIFO | one atomic swap | no-op |
| `UnboundedSegmentedMailbox` | unbounded, 256-slot segments | n/a | FIFO | one atomic add, a new segment every 256 messages | no-op |
| `UnboundedFairMailbox` | unbounded | n/a | FIFO per sender, round-robin across senders | map lookup per message | no-op |
| `UnboundedPriorityMailBox` | unbounded | n/a | priority; ties unspecified | mutex and heap push | no-op |
| `UnboundedStablePriorityMailbox` | unbounded | n/a | priority; ties in arrival order | lock-free push | no-op |
| `BoundedMailbox` | rounded up to a power of two | **blocks the sender** | FIFO | spin until a slot frees | disposes the ring |
| `NonBlockingBoundedMailbox` | rounded up to a power of two, at least 2 | `ErrMailboxFull`, message dead-lettered | FIFO | lock-free | no-op |
| `BoundedPriorityMailbox` | exactly `capacity` | `ErrMailboxFull`, message dead-lettered | priority; ties unspecified | lock-free | no-op |
| `BoundedStablePriorityMailbox` | exactly `capacity` | `ErrMailboxFull`, message dead-lettered | priority; ties in arrival order | lock-free | no-op |

Every mailbox is passed as a value to `WithMailbox` (or `WithFuncMailbox` for function actors, `actor/func_actor.go`). Each actor needs its own instance: two actors sharing one would be two consumers on a single-consumer queue.

## 6.4 Bounded mailboxes

### `BoundedMailbox`: blocking

`BoundedMailbox` wraps the ring buffer from `github.com/Workiva/go-datastructures/queue` (`NewBoundedMailbox` in `actor/bounded_mailbox.go`). Three properties come from that library rather than from GoAkt:

- **The capacity is rounded up to a power of two.** `NewBoundedMailbox(10)` holds 16 messages.
- **A full mailbox blocks the sender.** `Put` loops with `runtime.Gosched()` until a slot frees (`BoundedMailbox.Enqueue` in `actor/bounded_mailbox.go`). The sender's goroutine stays busy for that whole time. When the sender is another actor, that goroutine is a dispatcher worker, and it is not available to run any actor, including, possibly, the one it is waiting for.
- **`Dispose` is final.** After it, every `Put` fails with `ErrDisposed`.

`Dequeue` does not block: it calls the ring's blocking `Get` only after checking that the length is positive, and returns nil otherwise (`actor/bounded_mailbox.go`). That is what contract point 2 requires.

### `NonBlockingBoundedMailbox`: dropping

`NonBlockingBoundedMailbox` is Dmitry Vyukov's bounded queue, written in GoAkt (`actor/non_blocking_bounded_mailbox.go`). Every cell carries a sequence number. A producer claims a cell by advancing `enqueuePos` with a compare-and-swap when the cell's sequence equals the position, then stores the message and publishes it by advancing the sequence. When the cell still holds an unconsumed message, the sequence is behind the position, and `Enqueue` returns `ErrMailboxFull` instead of waiting. The consumer does the mirror image (`NonBlockingBoundedMailbox.Dequeue` in `actor/non_blocking_bounded_mailbox.go`).

The capacity is rounded up to a power of two, with a floor of two, so a position maps to a cell with a mask (`nextPowerOfTwo` in `actor/non_blocking_bounded_mailbox.go`). `Len` is the difference between the two positions. A producer that has claimed a cell but not yet published it counts in `Len` while `Dequeue` still returns nil. The turn then sees "not empty" and "nothing to take" at the same time; `finishOrReclaim` keeps the actor scheduled, and the turn retries until the message is published or its budget runs out.

## 6.5 Priority mailboxes

A `PriorityFunc` is `func(msg1, msg2 any) bool`, and returning true means `msg1` goes first (`heap.Less` in `actor/unbounded_priority_mailbox.go`). In the three intake-based mailboxes it is called on the consumer's goroutine, during `Dequeue`, for every comparison the heap makes. In `UnboundedPriorityMailBox` it also runs on the sender's goroutine, because `Enqueue` pushes into the heap under the mutex. It receives every message that enters the mailbox, including the reentrancy messages from §6.1, so it must handle types it does not know.

There are two designs:

- **`UnboundedPriorityMailBox`** pushes into a `container/heap` under a mutex (`UnboundedPriorityMailBox.Enqueue` and `UnboundedPriorityMailBox.Dequeue` in `actor/unbounded_priority_mailbox.go`). A binary heap is not stable, so messages the function ranks equally come out in no particular order. Its comment says so, and recommends the stable variant when ties must keep their order.
- **The other three** split producers from the heap. Producers push onto a lock-free stack, the *intake* (`actor/priority_intake.go`). Before each `Dequeue`, the consumer takes the whole stack in one swap and reverses it into arrival order (`priorityIntake.drain` in `actor/priority_intake.go`), then pushes it into a heap only it touches. Producers never contend with the consumer, and nothing is allocated per message.

The stable variants give each drained message an increasing sequence number and use it to break ties (`stableHeap.less` in `actor/unbounded_stable_priority_mailbox.go`). Deciding a tie takes two calls to the priority function, one in each direction.

The bounded variants admit a message by incrementing the length first and undoing it if that exceeds the capacity (`BoundedPriorityMailbox.Enqueue` in `actor/bounded_priority_mailbox.go`). Unlike the ring-based mailboxes, their capacity is exact. The stable unbounded variant also increments the length before pushing (`UnboundedStablePriorityMailbox.Enqueue` in `actor/unbounded_stable_priority_mailbox.go`), so briefly `IsEmpty` is false while the intake is still empty. That is the same harmless retry as in §6.4.

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

**Restart.** A restart reuses the same PID and the same mailbox (Chapter 4, §4.7), and keeps the messages in it:

- The message in flight when the restart begins finishes on the old incarnation. The turn then stops handing out user messages, as for any stop, and `Restart` waits for it to give the actor up before it re-initializes the actor (`restartSubtree` in `actor/pid.go`).
- `reset` does not dispose of the mailbox, and keeps the two counters behind `actor.mailbox.size`, when the stop belongs to a restart (`actor/pid.go`). That matters for `BoundedMailbox`, whose `Dispose` is final (`actor/bounded_mailbox.go`).
- The new incarnation handles the queued messages after its `PostStart`.

Messages sent while the restart is under way are refused: the actor is not running.

**Other nodes.** A mailbox is a Go value and does not travel. The remote spawn request has no field for it (`actor/spawn.go`), and neither has the record relocation rebuilds an actor from (`wireSpawnOptions`, `actor/spawn.go`). An actor that `SpawnOn` places on another node, that `Spawn` creates there through `WithHostAndPort`, or that relocation re-creates after its node left, runs on the default mailbox, whatever `WithMailbox` said.

## 6.9 Writing your own mailbox

A custom mailbox is passed to `WithMailbox` like the built-in ones. From §6.1, it must:

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
| The default mailbox is FIFO, with many concurrent producers | `TestEmbeddedMailboxFIFOOrder` and `TestEmbeddedMailboxConcurrentProducers` in `actor/embedded_mailbox_test.go` |
| The embedded mailbox keeps a dequeued context as its head, the sentinel, until the next `Dequeue` | `TestEmbeddedMailboxReleaseProtocol` in `actor/embedded_mailbox_test.go` |
| `NonBlockingBoundedMailbox` rounds its capacity to a power of two and returns `ErrMailboxFull` when full | `TestNonBlockingBoundedMailbox` in `actor/non_blocking_bounded_mailbox_test.go` |
| The bounded priority mailboxes return `ErrMailboxFull` when full | `TestBoundedPriorityMailbox` in `actor/bounded_priority_mailbox_test.go` |
| The stable priority mailboxes keep arrival order among equal priorities | `TestUnboundedStablePriorityMailbox` in `actor/unbounded_stable_priority_mailbox_test.go`; `TestBoundedStablePriorityMailbox` in `actor/bounded_stable_priority_mailbox_test.go` |
| The fair mailbox keeps each sender's order and starves no sender | `TestUnboundedFairMailboxPreservesPerSenderOrdering` and `TestUnboundedFairMailboxNoStarvation` in `actor/unbounded_fair_mailbox_test.go` |
| A restart keeps the mailbox: queued messages reach the new incarnation, and a `BoundedMailbox` still accepts messages | `TestRestartKeepsMailbox` in `actor/pid_test.go` |
| The segmented mailbox loses nothing under concurrent producers, and never hands a drained segment to another mailbox | `TestUnboundedSegmentedMailbox_DrainedSegmentIsNotReused` and `TestUnboundedSegmentedMailbox_ConcurrentMailboxesLoseNothing` in `actor/unbounded_segmented_mailbox_test.go` |
| The segmented mailbox keeps FIFO order across segment boundaries | `TestUnboundedSegmentedMailbox_Dequeue_OrderAcrossSegments` in `actor/unbounded_segmented_mailbox_test.go` |

## Implementation details (may change)

- The embedded mailbox and the placement of its two pointers in the PID.
- The segment size of 256, and allocating a new segment instead of reusing one.
- The lock-free intake in front of the priority heaps.
- Which mailboxes recycle contexts.

## Behaviours to know

| Behaviour | Source |
|---|---|
| A message refused by a full bounded mailbox becomes a dead letter; `Tell` still returns nil | `PID.doReceive` in `actor/pid.go` |
| `BoundedMailbox` blocks the sender, and a sending actor's worker with it | `BoundedMailbox.Enqueue` in `actor/bounded_mailbox.go` |
| `BoundedMailbox` and `NonBlockingBoundedMailbox` round the capacity up to a power of two | `NewBoundedMailbox` in `actor/bounded_mailbox.go`; `nextPowerOfTwo` in `actor/non_blocking_bounded_mailbox.go` |
| Messages queued when an actor stops are abandoned, not dead-lettered; a restart keeps them | `PID.runTurn` and `PID.reset` in `actor/pid.go` |
| `WithMailbox` has no effect on an actor placed on another node or relocated | `actorSystem.SpawnOn` in `actor/spawn.go` |
| The fair mailbox keeps a sub-queue per distinct sender for its whole life | `UnboundedFairMailbox.Enqueue` in `actor/unbounded_fair_mailbox.go` |
| A priority function sees the reentrancy messages too | `isControlMessage` in `actor/pid.go` |

## Exercises

1. In the embedded mailbox, a producer has swapped itself in as the tail but has not linked the previous tail yet. Walk through `finishOrReclaim` and `doReceive` and show that the message is still handled.
2. Why may `BoundedMailbox.Dequeue` not call the ring's `Get` directly? What would a worker be doing if it did?
3. A priority function orders `*Order` before everything else. With reentrancy enabled, what does it receive besides `*Order`, and what should it return for those?
4. Write a mailbox that drops the oldest message instead of the newest when full, and check it against each point of §6.9.
5. An actor uses `UnboundedFairMailbox` and receives one message from each of a million short-lived actors. Describe its memory after they have all stopped.
