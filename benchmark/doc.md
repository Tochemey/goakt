## How to run the benchmarks

Run from the repository root.

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

### GrainAsk — sync ask to a grain, sequential

```
go test -run=^$ -bench=^BenchmarkGrainAsk$ -count=10 ./benchmark/
```

### GrainTellFanOut — async tell across 256 grains, round-robin

```
go test -run=^$ -bench=^BenchmarkGrainTellFanOut$ -count=10 ./benchmark/
```

### Throughput-budget sweeps — `WithThroughputBudget` ∈ {8, 32, 64, 128, 256}

```
go test -run=^$ -bench='Throughput$' -count=5 ./benchmark/
```

### ActorMemoryFootprint — bytes per spawned actor

```
go test -run=^$ -bench=^BenchmarkActorMemoryFootprint$ -benchmem ./benchmark/
```

### RemoteTellThroughput — one shared client fans TCP tells over 10 systems for 10 s

```
go test -run=^$ -bench=^BenchmarkRemoteTellThroughput$ -benchtime=1x ./benchmark/
```

### MillionActorsSustainedLoad — 1M actors processing under sustained load

A single-node scale test (not a benchmark) that spawns one million actors, keeps every one of them processing messages for a fixed window, and reports memory (bytes/actor, HeapInuse, GC) and CPU (consumed CPU time, average cores, GC CPU fraction, throughput). It is build-tagged behind `scale` so it stays out of the normal suite, and needs a machine with enough memory: one supervision goroutine per actor means ~1M goroutines, so budget several GB of RAM.

```
go test -tags=scale -run TestMillionActorsSustainedLoad -v -timeout 30m ./benchmark/
```
