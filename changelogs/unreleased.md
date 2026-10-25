# Unreleased

## ⚡ Performance

- **Local `Tell` is about twice as fast.** The default mailbox stores a `Tell` as values in reusable fixed-size segments instead of in a pooled `ReceiveContext`, and an actor that a turn makes ready runs on that turn's worker. Measured on an 8-core arm64 machine against v4.6.3:
  - Actor-to-actor messaging (`BenchmarkTellPingPong`, new): 63–72 million messages/sec, from 30–46 million, with no allocation per message.
  - Many senders to many actors (`BenchmarkTellPairwise`): 51–55 million messages/sec, from 23–25 million.
  - One sender to one actor (`BenchmarkTellSinglePair`): 21–22 million messages/sec, from 9–12 million.
  - `Ask` is unchanged.

## 🔧 Fixes

- **An actor queued behind a blocked handler no longer waits for it while other workers are idle.** An actor that used up its message budget went back on its worker's local queue without waking an idle worker, so if that worker's next handler blocked, the actor waited for the handler even while other workers slept. An idle worker is now woken to take it.

## ⚠️ Behavior Changes

- **An actor on the default mailbox keeps about 2 KiB of mailbox memory once it has received a message.** An actor that has never received a message uses 32 bytes more than before (1,436 B of heap in `BenchmarkActorMemoryFootprint`, from 1,404 B). One that has received messages keeps its mailbox segment while it lives: about 3.5 KB per idle actor after one message, and about 5.6 KB after a burst of more than 32, against 1.46 KB before. Systems with millions of mostly idle actors that have each received messages should budget for it, or give those actors `WithMailbox(actor.NewUnboundedMailbox())`, a linked-list mailbox that keeps no segment but takes a pooled context per message.
