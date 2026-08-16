# Issue 1315: per-actor heap footprint and GC mark cost

Living sample for [issue #1315](https://github.com/Tochemey/goakt/issues/1315).

Before the fix, every spawned actor eagerly allocated three bookkeeping maps that usually stayed empty, every `SpawnChild` allocated a fresh default supervisor and passivation strategy, every spawn allocated a logger that was immediately replaced, and metrics-enabled systems registered one OTel callback and one instrument set per actor. At large resident populations this multiplied into a measurable heap floor that the Go collector re-traversed on every mark phase, and under spawn/stop churn the discarded allocations drove extra GC cycles.

## What the sample does

The sample reruns the issue's reproduction and guards the fix with deterministic object-count thresholds. Each threshold sits between the value measured before the fix (commit `625c187a`) and the value measured after it, so a regression on any of the removed allocations fails the run with a non-zero exit code.

1. Prints the sizes of the core per-actor structs.
2. Measures the allocation and GC cycle cost of 20k spawn/stop cycles.
3. Measures the retained live heap per idle actor for 50k flat spawns.
4. Measures the retained live heap per idle child for 50k `SpawnChild` actors, the path that allocated a supervisor and strategy per child before the fix.

## Run

```bash
go run ./playground/issue-1315
```

## Measured impact

Same machine, darwin/arm64, Go 1.26.0, retained-live-heap method with forced GC around each measurement.

Resident footprint per idle actor:

| Scenario    | Before                   | After                    | Reduction                  |
|-------------|--------------------------|--------------------------|----------------------------|
| Flat spawns | 2471 bytes, 19.0 objects | 2354 bytes, 17.0 objects | 4.7% bytes, 10.5% objects  |
| SpawnChild  | 3108 bytes, 25.0 objects | 2496 bytes, 19.0 objects | 19.7% bytes, 24.0% objects |

Spawn/stop churn, 20k cycles:

| Metric              | Before                     | After                      | Reduction                |
|---------------------|----------------------------|----------------------------|--------------------------|
| Allocated per cycle | 24792 bytes, 312.1 objects | 22684 bytes, 287.1 objects | 8.5% bytes, 8.0% objects |
| GC cycles           | 265                        | 239                        | 9.8%                     |

The GC gains follow directly from those numbers: the collector marks 10.5% to 24% fewer objects per resident actor on every cycle, and under churn the process triggers 9.8% fewer collections because each spawn allocates 2.1 KB less garbage.

Metrics-enabled systems additionally drop from N+1 meter callback registrations and N instrument sets to a constant 2 registrations and 1 instrument set; the per-actor cost is a cached 4-entry attribute slice. `unsafe.Sizeof(PID{})` grew from 456 to 464 bytes because the cached attribute slice header is 8 bytes larger than the registration handle it replaced; the retained-footprint numbers above already include that.

## Expected output

```text
struct sizes:
  PID:              464 bytes
  ReceiveContext:   128 bytes
  UnboundedMailbox: 144 bytes

spawn/stop churn (20000 cycles): 22684 bytes per cycle, 239 GC cycles
  objects per cycle               287.1 (limit 300.0) ok

flat population (50000 idle actors): 2408 bytes per actor
  objects per actor                17.0 (limit 18.0) ok

child population (50000 idle children): 2459 bytes per child
  objects per child                19.0 (limit 21.0) ok

PASS: resident footprint and churn allocation guards hold
```

Byte counts vary slightly per machine and run; the object-count guards are the deterministic part.
