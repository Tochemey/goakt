# Unreleased

## Performance

- **Local `Tell` is about twice as fast.** The default mailbox stores a `Tell` as values in reusable fixed-size segments instead of in a pooled `ReceiveContext`, and an actor that a turn makes ready runs on that turn's worker. Measured on an 8-core arm64 machine against v4.6.3:
  - Actor-to-actor messaging (`BenchmarkTellPingPong`, new): 63–72 million messages/sec, from 30–46 million, with no allocation per message.
  - Many senders to many actors (`BenchmarkTellPairwise`): 51–55 million messages/sec, from 23–25 million.
  - One sender to one actor (`BenchmarkTellSinglePair`): 21–22 million messages/sec, from 9–12 million.
- **Remote `Tell` with send coalescing is about 45% faster.** A sender whose message fits the coalescer's budget now enqueues it under one lock instead of a channel `select` that every sender sharing a context contended on, and the writer takes a whole batch at a time. Many senders to many remote actors (`BenchmarkRemoteTellThroughput`) deliver 2.11–2.15 million messages/sec, from 1.39–1.45 million, on an 8-core arm64 machine.
- **A coalesced remote `Tell` batch costs about half the memory.** When both peers negotiate capability revision 5, a batch carries each sender and receiver as a per-connection table reference instead of an address string, and the server reads it without a protobuf unmarshal: 280 B and 4.2 allocations per message, from 513 B and 6.2. Peers on an older release negotiate the earlier batch encoding, so mixed-version clusters keep working.
- **A local `Ask` round trip is about 10% faster.** `Ask` no longer waits on a timer channel: its timeout is delivered into its own reply channel. One asker (`BenchmarkAsk`) completes 1.41 million round trips/sec, from 1.27 million (709–711 ns from 784–785 ns per round trip). Many concurrent askers (`BenchmarkAskPairwise`) are unchanged.

## Fixes

- **An actor queued behind a blocked handler no longer waits for it while other workers are idle.** An actor that used up its message budget went back on its worker's local queue without waking an idle worker, so if that worker's next handler blocked, the actor waited for the handler even while other workers slept. An idle worker is now woken to take it.

## Behavior Changes

- **An actor on the default mailbox keeps about 2 KiB of mailbox memory once it has received a message.** An actor that has never received a message uses 32 bytes more than before (1,436 B of heap in `BenchmarkActorMemoryFootprint`, from 1,404 B). One that has received messages keeps its mailbox segment while it lives: about 3.5 KB per idle actor after one message, and about 5.6 KB after a burst of more than 32, against 1.46 KB before. Systems with millions of mostly idle actors that have each received messages should budget for it, or give those actors `WithMailbox(actor.NewUnboundedMailbox())`, a linked-list mailbox that keeps no segment but takes a pooled context per message.
