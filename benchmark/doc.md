## How to run the benchmarks

Run from the repository root. See `BENCHMARKS.md` for what each one
measures and how to cite the numbers.

### Tell — async send, parallel

```
go test -run=^$ -bench=^BenchmarkTell$ -count=10 ./benchmark/
```

### TellPayload — async send swept by payload size (0 B … 64 KiB)

```
go test -run=^$ -bench=^BenchmarkTellPayload$ -count=10 -benchmem ./benchmark/
```

### Request — Tell + Request reply, parallel

```
go test -run=^$ -bench=^BenchmarkRequest$ -count=10 ./benchmark/
```

### Ask — sync request/reply, sequential

```
go test -run=^$ -bench=^BenchmarkAsk$ -count=10 ./benchmark/
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

### RemoteTellThroughput — TCP send across 10 systems for 10 s

```
go test -run=^$ -bench=^BenchmarkRemoteTellThroughput$ -benchtime=1x ./benchmark/
```
