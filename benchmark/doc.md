## How to run the benchmarks

Run from the repository root. The actors, the shared workload (messages, sizes, `Latch`, `Signal`, the Skynet ranges) and `TestMain` are in `mocks_test.go`; each benchmark is in the file named after it. The grain cluster's ports come from `internal/net` (`dynaport`), as every test in this repository takes them.

```bash
go test -run '^$' -bench . -benchmem -count 10 -benchtime 1s ./benchmark | tee after.txt
```

`Skynet`, `MemoryPerActor` and `GrainMemoryPerGrain` are expensive per iteration and run once per round at `-benchtime 1s`. Run with `-count` of at least 10 and read medians.

### Running one benchmark

Anchor the name in `-bench`, or `Tell` also runs `TellParallel` and `TellFromActor`, and `Ask` the grain asks:

```bash
go test -run '^$' -bench '^BenchmarkTell$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkTellParallel$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkTellFromActor$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkAsk$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkAskParallel$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkPingPong$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkPingPongParallel$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkPingPongPipelined$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkThreadRing$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkForkJoinThroughput$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkBig$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkSkynet$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkMemoryPerActor$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkSpawnStop$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkGrainAsk$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkGrainAskParallel$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkGrainActivate$' -benchmem -count 10 -benchtime 1s ./benchmark
go test -run '^$' -bench '^BenchmarkGrainMemoryPerGrain$' -benchmem -count 10 -benchtime 1s ./benchmark
```

`Skynet`, `MemoryPerActor` and `GrainMemoryPerGrain` run one iteration per round whatever `-benchtime` says, so `-count` is the number of iterations measured.

### Reading the numbers on Apple silicon

Run every benchmark with `-count=10` and read the median, not the best or the last round. On an M1 the rounds of a steady-state benchmark differ by 20 to 30 percent with no change in the code: the chip has four performance and four efficiency cores, Go runs one worker thread per processor, and macOS places some of those threads on efficiency cores, which run this code two to three times slower. A round where a busy worker landed on an efficiency core is the low one. With tracing on, the garbage collector runs the same four or five times in a low round as in a high one, all during setup, and the measured loops below are at 0 B/op, so the collector is not the cause. The signature is visible with `GOMAXPROCS=4`: the 1,000 in-flight ping-pong then runs at either 94 to 96M or 81 to 85M messages/sec, two levels and nothing between, and beats the eight-processor median of 86M because the efficiency cores add cross-core traffic faster than compute. For a stable single number on such a machine, pin `GOMAXPROCS` to the performance-core count.

### What the suite reports

Every benchmark reports the same six figures: the time per operation, the bytes and allocations per operation from `-benchmem`, a throughput in `msgs/s`, `actors/s` or `grains/s` from `ReportRate`, and the live heap per idle actor or grain in `B/actor` and `B/grain` from the two memory benchmarks. `benchmark/cmd/table` renders result files as those six tables, Throughput, Time per operation, Bytes allocated per operation, Allocations per operation, Live heap per idle actor and Live heap per idle grain, with the best value in each row in bold and every column's factor relative to the first; `-match` and `-skip` select benchmarks by name:

```bash
go run ./benchmark/cmd/table "v4.6.6=before.txt" "branch=after.txt"
```

For significance between two runs, use benchstat: `go run golang.org/x/perf/cmd/benchstat@latest before.txt after.txt`.

### The scenarios

The first seven measure one operation each, driven from outside the system or between two actors.

| Benchmark | What it measures |
|---|---|
| `Tell` | Fire-and-forget messages from outside the system to one actor, until the actor has handled all of them. |
| `TellParallel` | Same as `Tell`, with `GOMAXPROCS` goroutines sending to the same actor at once (mailbox contention). |
| `TellFromActor` | Same as `Tell`, but sent by another actor from inside its message handler, so every message carries a sender. |
| `Ask` | Request/response round trips from outside the system to one actor, one at a time. |
| `AskParallel` | Same as `Ask`, with `GOMAXPROCS` goroutines making requests at once. |
| `PingPong` | Round trips between two actors, both inside the system, with each actor replying to the sender. |
| `SpawnStop` | Spawning an actor, stopping it, and waiting until its stop hook has run. |

The other seven are the scenarios actor frameworks are usually compared on, at their sources' sizes.

| Benchmark | Origin | What it measures |
|---|---|---|
| `PingPongParallel` | Savina PingPong, one pair per processor | `GOMAXPROCS` independent pinger/ponger pairs, each with one message in flight. A scheduling number: how cheaply an actor is made runnable and a worker gets to it. |
| `PingPongPipelined` | Akka's PingPong, which batches | The same pairs, each pinger keeping 100 messages in flight. A mailbox number: how fast a producer fills a mailbox while its consumer drains it. |
| `ThreadRing` | Savina ThreadRing | One token passed around a ring of 100 actors, one hop per message. Message latency through a chain of actors with one actor runnable at a time. |
| `ForkJoinThroughput` | Savina Fork Join (throughput) | One sender spreading messages round-robin over 60 actors. Scheduling many runnable actors. |
| `Big` | Savina Big | 120 actors each sending a Ping to a peer and answering every Ping with a Pong. Many-to-many contention. |
| `Skynet` | Skynet (github.com/atemerev/skynet) | A tree of 1,111,111 actors created and stopped with a message tree over them. Reported as actors per second. |
| `MemoryPerActor` | Ergo's memusage; Akka's actors-per-gigabyte | The live heap after a full collection, divided by 50,000 idle actors. Reported in bytes per actor. |

The grain benchmarks run in a single-node cluster started on first use, with `static` discovery listing only the node and one replica; every grain is local, so no message crosses the network. `GrainActivate` and `GrainMemoryPerGrain` start a cluster of their own and activate 10,000 grains before measuring, so a fresh cluster's one-off allocations are not charged to the grains measured.

| Benchmark | What it measures |
|---|---|
| `GrainAsk` | Request/response round trips to one active grain, one at a time, with `AskGrain`. |
| `GrainAskParallel` | Same as `GrainAsk`, with `GOMAXPROCS` goroutines each asking a grain of its own. |
| `GrainActivate` | Activating a grain under a new name and completing its first request/response round trip. Reported as grains per second. |
| `GrainMemoryPerGrain` | The live heap after a full collection, divided by 50,000 idle grains. Reported in bytes per grain. |

The ping-pong benchmarks differ in how many messages are in flight, and that decides what the number means: `PingPong` is the hand-off latency between two idle actors, `PingPongParallel` the same hand-off with every core busy, and `PingPongPipelined` sustained throughput with a hundred messages per wake-up. Never compare a pipelined figure with a one-in-flight figure.

### Methodology

- Every benchmark stops the clock only after the receiving actors have handled every message (`Latch`), so queueing quickly and processing later does not look fast; `SpawnStop` waits for the stop hook (`Signal`).
- The messages are pointers to empty structs, so any allocation reported comes from GoAkt, not the payload.
- One actor system is started in `TestMain`, outside the measurements, with logging off and no networking; the grain cluster starts only when a grain benchmark runs.
- In the scenarios with many actors, each actor counts its own messages and touches the shared latch once, so no contended counter sits in the hot path.
- `Big` picks peers with a fixed pseudo-random walk (`NextPeer`), so every run sends the same messages to the same peers in the same order.

### Soak tests

`scale_test.go`, `grain_scale_test.go` and `spawn_scale_test.go` are not benchmarks but million-actor and million-grain sustained-load tests, behind the `scale` build tag, run on demand with `go test -tags scale -run TestMillion ./benchmark`.
