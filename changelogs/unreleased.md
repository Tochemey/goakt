# Unreleased

## 🔧 Fixes

- **Runtime metric instruments report the correct type and the deadletter scrape no longer floods a single mailbox** ([#1322](https://github.com/Tochemey/goakt/issues/1322)). The instruments that report an absolute current value that can fall (`actorsystem.actors.count`, `actorsystem.peers.count`, `actorsystem.uptime`, `actor.children.count`, `actor.stash.size`, `actor.uptime`, `actor.last.received.duration`) were registered as monotonic observable counters, so exporters that derive rates or deltas mishandled every decrease; they are now observable gauges. The per-actor metrics callback asked the deadletter actor for its count once per running actor per scrape, flooding a single mailbox at large populations; it now asks once per scrape for a snapshot of all per-address counts. `crdt.replicator.crossdc.replication.lag` is now reported in milliseconds instead of nanoseconds to match the other duration instruments.
