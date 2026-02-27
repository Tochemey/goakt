# Passivation

Passivation automatically stops actors to reclaim memory and resources. GoAkt supports two distinct mechanisms: **per-actor passivation** (idle-based) and **system eviction** (capacity-based).

---

## Per-actor passivation

Per-actor passivation is configured per actor at spawn. The passivation manager tracks each actor and stops it when its strategy triggers. Only actors with a passivation strategy (other than `LongLivedStrategy`) are tracked.

### Strategies

| Strategy                       | Constructor                                             | Trigger                                                                      |
|--------------------------------|---------------------------------------------------------|------------------------------------------------------------------------------|
| **TimeBasedStrategy**          | `passivation.NewTimeBasedStrategy(timeout)`             | No message within the configured duration                                    |
| **MessagesCountBasedStrategy** | `passivation.NewMessageCountBasedStrategy(maxMessages)` | Actor has processed at least `maxMessages` since the strategy was registered |
| **LongLivedStrategy**          | `passivation.NewLongLivedStrategy()`                    | Never passivate (opt-out)                                                    |

### Usage

```go
pid, err := system.Spawn(ctx, "cache", actor,
    actor.WithPassivationStrategy(passivation.NewTimeBasedStrategy(5*time.Minute)))
```

Or use `WithLongLived()` to opt out:

```go
pid, err := system.Spawn(ctx, "service", actor, actor.WithLongLived())
```

### When to use

- Grains and virtual actors (default for many grains)
- Actors with large per-instance state
- High churn of short-lived entities

### When to avoid

- Long-lived services that must stay up
- Actors that handle infrequent but critical messages

### Strategy interface

```go
type Strategy interface {
    fmt.Stringer
    Name() string
}
```

---

## System eviction

**System eviction** is a node-wide mechanism that limits the total number of active actors. When the count exceeds the configured limit, the framework passivates actors according to an eviction policy (LRU, LFU, or MRU). This is independent of per-actor passivation—system eviction can stop any user actor, including those with `LongLivedStrategy`.

### Eviction policies

| Policy  | Description                                                                 |
|---------|-----------------------------------------------------------------------------|
| **LRU** | Least Recently Used — passivate actors with the oldest `LatestActivityTime` |
| **LFU** | Least Frequently Used — passivate actors with the lowest `ProcessedCount`   |
| **MRU** | Most Recently Used — passivate actors with the newest `LatestActivityTime`  |

### Configuration

```go
strategy, err := actor.NewEvictionStrategy(1000, actor.LRU, 20)
// limit=1000, policy=LRU, percentage=20
system, _ := actor.NewActorSystem("app",
    actor.WithEvictionStrategy(strategy, 5*time.Second))
```

| Parameter      | Purpose                                                                           |
|----------------|-----------------------------------------------------------------------------------|
| **limit**      | Maximum number of active actors before eviction runs. Must be > 0.                |
| **policy**     | `actor.LRU`, `actor.LFU`, or `actor.MRU`                                          |
| **percentage** | Percentage of actors to passivate when over limit (0–100). Clamped automatically. |
| **interval**   | How often the eviction engine runs (e.g. `5*time.Second`)                         |

### Behavior

- The eviction loop runs at the configured interval.
- When `NumActors() > limit`, actors are selected by policy and passivated via `Shutdown`.
- The number of actors to evict is the greater of: (total − limit) and (percentage × total / 100).
- System actors (reserved names) are excluded. Only user actors are eligible for eviction.

### When to use

- Memory or resource constraints on a node
- Bounding actor count regardless of per-actor strategy
- Coarse-grained capacity management

---

## Comparison

| Aspect        | Per-actor passivation                     | System eviction                                            |
|---------------|-------------------------------------------|------------------------------------------------------------|
| **Scope**     | Per actor                                 | Node-wide                                                  |
| **Trigger**   | Idle timeout or message count             | Total actors > limit                                       |
| **Config**    | `WithPassivationStrategy(strat)` at spawn | `WithEvictionStrategy(strat, interval)` at system creation |
| **LongLived** | Opts out of per-actor passivation         | Does not opt out; can still be evicted                     |
