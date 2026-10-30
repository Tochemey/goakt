# Unreleased

## Highlights

- **Named dispatchers.** A group of actors or grains whose handlers block can run on a worker pool of its own, so the default pool keeps its size and its speed for everything else.

## Features

- **Named dispatchers** ([#1478](https://github.com/Tochemey/goakt/issues/1478)). `WithDispatcherPool(name, workers)` declares a worker pool on the actor system; `WithDispatcher(name)` assigns an actor to it at spawn and `WithGrainDispatcher(name)` a grain at activation, as Akka's `Props.withDispatcher` does. The actors and grains on a dispatcher share its workers and no others.
  - Blocking stays where it belongs. 200 actors that each hold their worker for 10 ms, under 1,000 concurrent asks on a node with `GOMAXPROCS=2`, take 5.5 s on the default pool and stall an unrelated actor for the same 5.5 s; on a dedicated pool of 256 they take 56 ms and the unrelated actor answers at once. Growing the default pool to 256 instead gets the same 56 ms but slows every ask on the node; with 64 blocking actors present, asks complete at 2.14 million/sec with a dedicated pool against 1.87 million/sec with an enlarged default one.
  - A spawn or activation that names a dispatcher the system did not declare fails with `ErrDispatcherNotFound`. An empty name is the default dispatcher. A child does not inherit its parent's dispatcher.
  - The name travels with the actor's and the grain's wire record, and with `SpawnOn`, remote spawn and remote activation requests, so an actor or grain relocated or placed on another node runs on that node's dispatcher of the same name. Declare the same dispatchers on every node; a node that lacks the name refuses the record.
  - Dispatchers are isolated: a send between actors on different dispatchers takes the slower shared-queue path (about a third slower in pipelined ping-pong), and workers do not steal across pools. Keep the number of dispatchers small: the default for everything that computes and returns, and one per group that blocks.
  - Message throughput, ask latency and the heap per actor and per grain are unchanged from v4.6.5.
