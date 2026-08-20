# Issue 1317: passivation heap retains passivated actors

Living sample for [pull request #1317](https://github.com/Tochemey/goakt/pull/1317).

Before the fix, `passivationHeap.Pop` shrank the heap slice without clearing the popped slot. Shrinking a slice only changes its length, so the backing array kept a strong reference to every popped `*passivationEntry`. Each entry pins its target PID and the PID pins the actor instance, its mailbox, and its state. Since the passivation manager lives as long as the actor system, every actor or grain that was ever passivated stayed reachable through the heap backing array and was never reclaimed by the GC.

## What the sample does

The sample makes reclamation directly observable instead of inferring it from heap sizes.

1. Starts a quiet actor system and records the baseline live heap.
2. Spawns 20k idle actors, each carrying a 4 KB ballast buffer and a `runtime.AddCleanup` hook that counts the instance once the GC proves it unreachable.
3. Waits until the whole population has passivated.
4. Runs GC rounds until the reclaimed count settles, then guards two invariants:
   - at least 19,000 of the 20,000 actor instances must be reclaimed (before the fix: 0),
   - the heap retained over baseline must stay under 32 MB (before the fix: about 113 MB, dominated by the 78 MB of ballast).

Either guard failing exits non-zero and means popped passivation entries are pinning passivated actors again.

## Run

```bash
go run ./playground/issue-1317
```

## Measured impact

Same machine, darwin/arm64, Go 1.26.0. Before is `main` at commit `4b719412`, after is the fix.

| Metric                    | Before    | After         |
|---------------------------|-----------|---------------|
| Actor instances reclaimed | 0 / 20000 | 19999 / 20000 |
| Retained over baseline    | 113.15 MB | 5.27 MB       |

The one unreclaimed instance in the after run is cleanup callback scheduling, not retention; the guard threshold tolerates it.
