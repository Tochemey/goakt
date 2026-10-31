## How to run the benchmarks

Run from the repository root.

### Reading the numbers on Apple silicon

Run every benchmark with `-count=10` and read the median, not the best or the last round. On an M1 the rounds of a steady-state benchmark differ by 20 to 30 percent with no change in the code: the chip has four performance and four efficiency cores, Go runs one worker thread per processor, and macOS places some of those threads on efficiency cores, which run this code two to three times slower. A round where a busy worker landed on an efficiency core is the low one. With tracing on, the garbage collector runs the same four or five times in a low round as in a high one, all during setup, and the measured loops below are at 0 B/op, so the collector is not the cause. The signature is visible with `GOMAXPROCS=4`: the 1,000 in-flight ping-pong then runs at either 94 to 96M or 81 to 85M messages/sec, two levels and nothing between, and beats the eight-processor median of 86M because the efficiency cores add cross-core traffic faster than compute. For a stable single number on such a machine, pin `GOMAXPROCS` to the performance-core count.

### Tell — async send, parallel

All producers race on a single receiver, so the reported rate is bounded by that one consumer's drain rate. Use the pair benchmarks below for send-path contention and aggregate scaling.

```
go test -run=^$ -bench=^BenchmarkTell$ -count=10 ./benchmark/
```

### TellSinglePair — async send, one producer to one receiver

One goroutine tells one actor, timed until the receiver has processed every message. The enqueue side is contention-free, so this isolates the per-message dispatch cost. Cite this as the single-actor throughput number.

```
go test -run=^$ -bench=^BenchmarkTellSinglePair$ -count=10 ./benchmark/
```

### TellPairwise — async send across GOMAXPROCS sender/receiver pairs

Every producer goroutine tells its own private receiver; the reported messages/sec aggregates all pairs. Aggregate throughput should approach pair-count times the TellSinglePair rate; a flat curve indicates a process-wide bottleneck on the send path. Cite this as the multi-actor (aggregate) throughput number.

```
go test -run=^$ -bench=^BenchmarkTellPairwise$ -count=10 ./benchmark/
```

### TellPingPong — actor-to-actor async send with a bounded number of messages in flight

Each client actor keeps a fixed number of messages in flight to its own echo actor and sends the next batch once every reply of the current one is back; every request and every reply counts as one message. This is the shape actor frameworks commonly use to report messaging throughput. The in-flight bound keeps every mailbox short, so the number reflects delivery and dispatch rather than the cost of buffering a backlog, which the goroutine-driven Tell benchmarks build up. The sweep runs GOMAXPROCS pairs and eight times as many, with 100 and 1,000 messages in flight per pair. Cite this as the actor-to-actor throughput number.

```
go test -run=^$ -bench=^BenchmarkTellPingPong$ -count=10 -benchmem ./benchmark/
```

### TellPingPongSinglePair: the hand-off between two actors, nothing else running

One client and one echo actor with one message in flight, so every message wakes an idle actor and the number is the cost of handing the turn from one actor to the other, twice per round trip. It is the one-pair, one-in-flight case of TellPingPong, kept apart because it answers a latency question. Cite this as the actor-to-actor latency number.

```
go test -run=^$ -bench=^BenchmarkTellPingPongSinglePair$ -count=10 -benchmem ./benchmark/
```

### ThreadRing: one token around a ring of 100 actors

One token passed around a ring of 100 actors, one hop per message, until it has made b.N hops, as in the Savina ThreadRing benchmark. Only one actor is runnable at any time, so the number is the latency of handing the turn from one actor to the next through a long chain.

```
go test -run=^$ -bench=^BenchmarkThreadRing$ -count=10 -benchmem ./benchmark/
```

### ForkJoin: one sender spreading messages over 60 actors

One goroutine outside the system tells 60 actors round-robin, timed until every actor has handled its share, as in the Savina fork-join throughput benchmark. Each message readies an idle actor, so the number is the cost of waking a worker for an actor with one message, many times over, with the sender never yielding.

```
go test -run=^$ -bench=^BenchmarkForkJoin$ -count=10 -benchmem ./benchmark/
```

### TellPayload — async send swept by payload size (0 B … 64 KiB)

```
go test -run=^$ -bench=^BenchmarkTellPayload$ -count=10 -benchmem ./benchmark/
```

### MailboxTell: async send through each mailbox implementation, parallel

Measures Tell dispatch throughput into an actor backed by each mailbox added alongside the default unbounded mailbox. Run all four sub-benchmarks:

```
go test -run=^$ -bench=^BenchmarkMailboxTell$ -count=10 -benchmem ./benchmark/
```

Run a single mailbox with the sub-benchmark filter:

```
go test -run=^$ -bench='^BenchmarkMailboxTell$/NonBlockingBounded' -count=10 -benchmem ./benchmark/
go test -run=^$ -bench='^BenchmarkMailboxTell$/UnboundedStablePriority' -count=10 -benchmem ./benchmark/
go test -run=^$ -bench='^BenchmarkMailboxTell$/BoundedPriority' -count=10 -benchmem ./benchmark/
go test -run=^$ -bench='^BenchmarkMailboxTell$/BoundedStablePriority' -count=10 -benchmem ./benchmark/
```

### Request — Tell + Request reply, parallel

```
go test -run=^$ -bench=^BenchmarkRequest$ -count=10 ./benchmark/
```

### Ask — sync request/reply, sequential

One asker and one receiver doing sequential round trips: the single-pair request/reply number. A sequential round trip is floored by the cost of two goroutine handoffs, so cite this as latency-bound throughput.

```
go test -run=^$ -bench=^BenchmarkAsk$ -count=10 ./benchmark/
```

### AskPairwise — sync request/reply across GOMAXPROCS asker/receiver pairs

Every asker goroutine does sequential round trips against its own private receiver; the reported messages/sec aggregates all pairs. Aggregate throughput should approach pair-count times the Ask rate; a flat curve indicates a process-wide bottleneck on the ask path. Cite this as the multi-actor (aggregate) request/reply number.

```
go test -run=^$ -bench=^BenchmarkAskPairwise$ -count=10 ./benchmark/
```

### AskTailLatencyUnderLoad — request/reply latency percentiles, saturated system

One probe pair does sequential Ask round trips while GOMAXPROCS background pairs flood the dispatcher with Tells. Reports p50/p90/p99/p99.9/max nanoseconds per round trip. The probe actor is separate from the loaded actors, so the tail measures scheduler interference (waiting for dispatcher attention behind throughput turns), not shared-mailbox backlog. Compare p50 against the unloaded Ask to read the interference cost.

```
go test -run=^$ -bench=^BenchmarkAskTailLatencyUnderLoad$ -count=5 ./benchmark/
```

### SendAsync — async send by name, parallel

```
go test -run=^$ -bench=^BenchmarkSendAsync$ -count=10 ./benchmark/
```

### SendSync — sync send by name, sequential

```
go test -run=^$ -bench=^BenchmarkSendSync$ -count=10 ./benchmark/
```

### GrainTell — async tell to a grain, parallel

```
go test -run=^$ -bench=^BenchmarkGrainTell$ -count=10 ./benchmark/
```

### GrainTellOneWay — one-way tell to a grain, parallel

Same shape as GrainTell with `WithOneWay()`: each TellGrain returns once the message is enqueued instead of blocking for the processed acknowledgment, so producers only pay the mailbox enqueue. The timer runs until the grain has processed every message, so messages/sec stays end-to-end and comparable with GrainTell.

```
go test -run=^$ -bench=^BenchmarkGrainTellOneWay$ -count=10 ./benchmark/
```

### GrainAsk — sync ask to a grain, sequential

```
go test -run=^$ -bench=^BenchmarkGrainAsk$ -count=10 ./benchmark/
```

### GrainTellFanOut — async tell across 256 grains, round-robin

```
go test -run=^$ -bench=^BenchmarkGrainTellFanOut$ -count=10 ./benchmark/
```

### GrainTellPairwise — tell across GOMAXPROCS producer/grain pairs

Every producer goroutine tells its own private grain; the reported messages/sec aggregates all pairs. Each TellGrain blocks for the processed acknowledgment, so per-pair throughput is round-trip bound; a flat curve relative to GrainTell indicates a process-wide bottleneck shared across grains. Cite this as the multi-grain (aggregate) throughput number.

```
go test -run=^$ -bench=^BenchmarkGrainTellPairwise$ -count=10 ./benchmark/
```

### GrainTellOneWayPairwise — one-way tell across GOMAXPROCS producer/grain pairs

Same shape as GrainTellPairwise with `WithOneWay()`. A producer returning only proves its messages were enqueued, so every grain counts what it processed and the timer runs until all of them are done; messages/sec stays end-to-end. The gap to GrainTellPairwise is the per-message acknowledgment round trip.

```
go test -run=^$ -bench=^BenchmarkGrainTellOneWayPairwise$ -count=10 ./benchmark/
```

### GrainAskPairwise — sync ask across GOMAXPROCS asker/grain pairs

Every asker goroutine does sequential AskGrain round trips against its own private grain; the reported messages/sec aggregates all pairs. Aggregate throughput should approach pair-count times the GrainAsk rate. Cite this as the multi-grain (aggregate) request/reply number.

```
go test -run=^$ -bench=^BenchmarkGrainAskPairwise$ -count=10 ./benchmark/
```

### Throughput-budget sweeps — `WithThroughputBudget` ∈ {8, 32, 64, 128, 256}

```
go test -run=^$ -bench='Throughput$' -count=5 ./benchmark/
```

### ActorMemoryFootprint: live heap bytes per idle actor

Spawns 100,000 empty actors into a started actor system, waits for their PostStart to drain, forces a collection and reports the live-heap growth per actor against the empty system's collected baseline. The system's fixed cost is excluded, so the number is the term a million-actor node scales with; the report also extrapolates it to one million actors. Runs twice: the default spawn, which registers every actor with the passivation manager, and a long-lived spawn, which does not. The difference between the two is the passivation bookkeeping per actor. The run fails when the live heap per actor exceeds the ceiling pinned in `bench_actor_memory_footprint_test.go`.

```
go test -run=^$ -bench=^BenchmarkActorMemoryFootprint$ -benchtime=1x ./benchmark/
```

### GrainMemoryFootprint: live heap bytes per idle grain

The grain counterpart of ActorMemoryFootprint. Activates 100,000 empty grains through `GrainOf` into a started actor system, forces a collection and reports the live-heap growth per grain against the empty system's collected baseline, extrapolated to one million grains. Activation is synchronous, so nothing is left in flight and no settle step is needed. Runs twice: the default activation, which registers every grain with the passivation manager, and a long-lived activation, which does not. The difference between the two is the passivation bookkeeping per grain. The run fails when the live heap per grain exceeds the ceiling pinned in `bench_grain_memory_footprint_test.go`.

```
go test -run=^$ -bench=^BenchmarkGrainMemoryFootprint$ -benchtime=1x ./benchmark/
```

### RemoteTellThroughput — one shared client fans TCP tells over 10 systems for 10 s

```
go test -run=^$ -bench=^BenchmarkRemoteTellThroughput$ -benchtime=1x ./benchmark/
```

### SpawnTopLevel: local spawn, sequential

One goroutine spawns empty actors under names no earlier spawn used, into a started system; the reported latency is the framework's own cost of creating an actor (address validation, PID construction, tree and passivation registration, the first dispatch). Every iteration leaves an actor alive, so the cost is averaged over a growing population; run a fixed number of iterations to compare runs. Cite this as the cost of a local spawn.

```
go test -run=^$ -bench=^BenchmarkSpawnTopLevel$ -benchtime=100000x -count=5 -benchmem ./benchmark/
```

### SpawnChild: child spawn under a parent 1, 10 and 20 levels deep

Same shape as SpawnTopLevel through `PID.SpawnChild`, under a parent with 1, 10 or 20 ancestors. A child spawn validates its address once per ancestor, so the slope across the depths is what a deep hierarchy adds to a spawn.

```
go test -run=^$ -bench=^BenchmarkSpawnChild$ -benchtime=100000x -count=5 -benchmem ./benchmark/
```

### SpawnTopLevelParallel: local spawn from GOMAXPROCS goroutines

GOMAXPROCS goroutines spawn distinct names at once. The aggregate spawns/sec against SpawnTopLevel shows how far spawns overlap; anything the spawn path serialises across actors, such as the actor tree's lock, shows up as an aggregate below GOMAXPROCS times the sequential rate.

```
go test -run=^$ -bench=^BenchmarkSpawnTopLevelParallel$ -benchtime=100000x -count=5 -benchmem ./benchmark/
```

### ClusterSpawn: named spawn in a three-node cluster, sequential and parallel

A spawn in cluster mode claims the actor name in the registry before it returns, so on top of the local activation it pays registry round trips over the network. Three nodes run in one process on loopback; one of them spawns actors under names no earlier spawn used, so every spawn is a first claim. The sequential variant is the latency of one spawn; cite it as the cost of a spawn in a cluster. The parallel variant spawns from GOMAXPROCS goroutines at once, and its aggregate rate shows how far spawns of distinct names overlap in the registry. Both variants and every repeat share one cluster, so the registry holds every actor spawned so far; run a fixed number of iterations to keep that population bounded.

```
go test -run=^$ -bench=^BenchmarkClusterSpawn$ -benchtime=500x -count=5 ./benchmark/
```

### MillionActorsSustainedLoad — 1M actors processing under sustained load

A single-node scale test (not a benchmark) that spawns one million actors, keeps every one of them processing messages for a fixed window, and reports memory (bytes/actor, HeapInuse, GC) and CPU (consumed CPU time, average cores, GC CPU fraction, throughput). It is build-tagged behind `scale` so it stays out of the normal suite, and needs a machine with enough memory: one supervision goroutine per actor means ~1M goroutines, so budget several GB of RAM.

```
go test -tags=scale -run TestMillionActorsSustainedLoad -v -timeout 30m ./benchmark/
```

### MillionGrainsSustainedLoad — 1M grains processing under sustained load

The grain counterpart of MillionActorsSustainedLoad, so the two populations are measured the same way: activates one million long-lived grains, reports memory per grain (bytes/grain, HeapInuse) once they are idle, then keeps every grain processing messages for the same fixed window and reports CPU and throughput. Each producer's TellGrain returns when the grain has processed the message, so there is no backlog cap. Build-tagged behind `scale` like the actor test; budget about 1 GB of RAM.

```
go test -tags=scale -run TestMillionGrainsSustainedLoad -v -timeout 30m ./benchmark/
```

### ActorCreationTree — 2²⁰−1 actors created as a binary tree

A single-node scale test (not a benchmark) in which every actor spawns its two children, so creation is concurrent across subtrees and hierarchical in depth, the actor-creation workload actor frameworks compare on. It reports the wall time of the whole build and the creation rate. The sequential Spawn benchmarks measure one spawn's latency; this measures what the spawn path serialises across actors. Build-tagged behind `scale`; budget about 2 GB of RAM for the million long-lived actors.

```
go test -tags=scale -run TestActorCreationTree -v -timeout 30m ./benchmark/
```
