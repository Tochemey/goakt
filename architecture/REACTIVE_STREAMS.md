# GoAkt Reactive Streams Architecture

---

## Table of Contents

1. [Core Concepts](#1-core-concepts)
2. [Backpressure Strategy](#2-backpressure-strategy)
3. [API Design](#3-api-design)
4. [Actor Integration](#4-actor-integration)
5. [Concurrency Model](#5-concurrency-model)
6. [Stream Graph Execution](#6-stream-graph-execution)
7. [Error Handling](#7-error-handling)
8. [Performance Considerations](#8-performance-considerations)
9. [Comparison with Other Systems](#9-comparison-with-other-systems)
10. [Full Example](#10-full-example)

---

## 1. Core Concepts

### 1.1 Philosophy

GoAkt Streams is a reactive, demand-driven data processing library built on top of the GoAkt actor model. It does **not** replace actors; it **composes** with them. Every stage in a stream pipeline runs inside an actor, which means it inherits GoAkt's supervision, lifecycle management, location transparency, and fault tolerance for free.

The design follows four principles:

| Principle         | What it means in GoAkt Streams                                   |
|-------------------|------------------------------------------------------------------|
| **Correctness**   | Exactly-once delivery semantics within a local pipeline          |
| **Backpressure**  | Demand-driven pull prevents unbounded buffering                  |
| **Composability** | Stages are values; pipelines are assembled declaratively         |
| **Actor-native**  | Every stage is an actor; supervision and routing apply naturally |

### 1.2 Primary Abstractions

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         Stream Pipeline                                 │
│                                                                         │
│   ┌──────────┐    demand     ┌──────────┐    demand    ┌──────────┐     │
│   │  Source  │◄──────────────│  Flow    │◄─────────────│  Sink    │     │
│   │ (Actor)  │─── elements ─►│ (Actor)  │── elements ─►│ (Actor)  │     │
│   └──────────┘               └──────────┘              └──────────┘     │
│                                                                         │
│   Publisher                  Processor                  Subscriber      │
└─────────────────────────────────────────────────────────────────────────┘
```

#### `Source[T]`: the origin of elements

A `Source[T]` is a lazy description of how to produce a stream of `T` values. Nothing runs until materialized via `Run()`. Internally it materializes as a GoAkt actor whose output is bounded by downstream demand.

```go
// Source produces elements of type T.
// It is a lazy description; nothing runs until materialized.
type Source[T any] struct {
    stages []*stage
}

// Via attaches a type-preserving Flow stage, returning a new Source.
func (s Source[T]) Via(flow Flow[T, T]) Source[T]

// To attaches a Sink, completing the graph. Returns a RunnableGraph.
func (s Source[T]) To(sink Sink[T]) RunnableGraph

// WithOverflowStrategy configures what happens when the source buffer is full.
func (s Source[T]) WithOverflowStrategy(os OverflowStrategy) Source[T]

// WithTracer attaches a distributed tracing hook to the source stage.
func (s Source[T]) WithTracer(t Tracer) Source[T]
```

For type-changing transformations use the free function `Via[In, Out]`:

```go
// Via wires a Source through a type-changing Flow, returning a new Source.
func Via[In, Out any](src Source[In], flow Flow[In, Out]) Source[Out]
```

#### `Flow[In, Out]`: a transformation stage

A `Flow[In, Out]` is a processing stage that consumes elements of type `In` and produces elements of type `Out`. It is both a **Subscriber** (to its upstream) and a **Publisher** (to its downstream). In GoAkt terms it is a **Processor** actor.

```go
// Flow transforms a stream of In into a stream of Out.
type Flow[In, Out any] struct {
    stage *stage
}

// Configure per-stage behavior:
func (f Flow[In, Out]) WithErrorStrategy(s ErrorStrategy) Flow[In, Out]
func (f Flow[In, Out]) WithRetryConfig(rc RetryConfig) Flow[In, Out]
func (f Flow[In, Out]) WithMailbox(mailbox actor.Mailbox) Flow[In, Out]
func (f Flow[In, Out]) WithName(name string) Flow[In, Out]
func (f Flow[In, Out]) WithTags(tags map[string]string) Flow[In, Out]
func (f Flow[In, Out]) WithTracer(t Tracer) Flow[In, Out]
```

#### `Sink[T]`: the terminal consumer

A `Sink[T]` is the terminal stage of a pipeline. It subscribes to upstream and applies a terminal operation (collect, fold, forward to actor, write to channel, etc.).

```go
// Sink consumes elements of type T.
type Sink[T any] struct {
    desc *stage
}

// Configure per-stage behavior:
func (s Sink[T]) WithErrorStrategy(strategy ErrorStrategy) Sink[T]
func (s Sink[T]) WithRetryConfig(rc RetryConfig) Sink[T]
func (s Sink[T]) WithMailbox(mailbox actor.Mailbox) Sink[T]
func (s Sink[T]) WithName(name string) Sink[T]
func (s Sink[T]) WithTags(tags map[string]string) Sink[T]
func (s Sink[T]) WithTracer(t Tracer) Sink[T]
```

#### `RunnableGraph`: an assembled pipeline ready to run

```go
// RunnableGraph is a fully connected stream pipeline that can be materialized.
type RunnableGraph struct {
    stages     []*stage   // non-nil for a single linear pipeline
    pipelines  [][]*stage // non-nil for multi-pipeline graphs (Graph DSL)
    fusionMode FusionMode
}

// Run materializes the graph within the given ActorSystem.
// Returns a handle for lifecycle control and observability.
func (g RunnableGraph) Run(ctx context.Context, system actor.ActorSystem) (StreamHandle, error)

// WithFusion configures the stage fusion mode for this graph.
func (g RunnableGraph) WithFusion(mode FusionMode) RunnableGraph
```

#### `StreamHandle`: runtime control of a running stream

```go
// StreamHandle provides runtime control of a materialized stream.
type StreamHandle interface {
    // ID returns the unique identifier of this stream.
    ID() string

    // Done returns a channel closed when the stream terminates (normally or by error).
    Done() <-chan struct{}

    // Err returns the terminal error, if any (nil on normal completion).
    Err() error

    // Stop signals an orderly shutdown; in-flight elements are drained.
    Stop(ctx context.Context) error

    // Abort immediately terminates the stream, discarding buffered elements.
    Abort()

    // Metrics returns a snapshot of live observability data.
    Metrics() StreamMetrics
}
```

### 1.3 Demand Model: Push/Pull Hybrid

GoAkt Streams uses a **demand-driven pull** model implemented natively through actor message passing rather than an external reactive streams SPI.

```
Sink ──── Request(n=32) ────► Flow ──── Request(n=32) ────► Source
     ◄─── Element(x) ─────────     ◄─── Element(x) ──────────
     ◄─── Element(x) ─────────     ◄─── Element(x) ──────────
         ...32 times...                  ...32 times...
     ──── Request(n=32) ────►     ──── Request(n=32) ────►
```

Rules:

- A stage only sends elements **after** receiving a demand request.
- Demand is **cumulative**: two consecutive `Request(10)` and `Request(5)` = 15 outstanding demand.
- A stage propagates demand upstream **before** its outstanding credit is fully consumed (credit-based refill, see §2.3).
- `Cancel` terminates the subscription upstream.

---

## 2. Backpressure Strategy

### 2.1 Protocol Messages

All backpressure signaling is done via typed actor messages:

```go
// stageWire wires a stage to its upstream and downstream peers.
type stageWire struct {
    subID      string
    upstream   *actor.PID  // nil for source stages
    downstream *actor.PID  // nil for sink stages
}

// streamRequest is sent downstream-to-upstream: "I can accept n more elements."
type streamRequest struct {
    subID string
    n     int64
}

// streamElement carries one pipeline element upstream-to-downstream.
type streamElement struct {
    subID string
    value any
    seqNo uint64 // monotonic, per-subscription
}

// streamComplete signals normal upstream completion.
type streamComplete struct {
    subID string
}

// streamError signals upstream failure.
type streamError struct {
    subID string
    err   error
}

// streamCancel is sent downstream-to-upstream to cancel the subscription.
type streamCancel struct {
    subID string
}
```

The public actor-source pull protocol:

```go
// PullRequest is sent by the stream runtime to pull elements from a source actor.
type PullRequest struct{ N int64 }

// PullResponse carries elements back to the stream runtime.
type PullResponse[T any] struct{ Elements []T }
```

### 2.2 Stage Internals: the Demand Ledger

Each stage maintains a **demand ledger**: two plain `int64` counters tracking demand from downstream and outstanding credit toward upstream. Because a stage actor processes one message at a time, these counters need no atomics or locks. There is no separate input buffer; the actor mailbox plays that role, and processed outputs wait in a GC-safe FIFO queue until downstream demand arrives:

```
┌──────────────────────────────────────────────────────────┐
│                   Flow Stage Actor                       │
│                                                          │
│  downstreamDemand  ← int64 (demand from downstream)      │
│  upstreamCredit    ← int64 (requested, not yet received) │
│  outputBuf         ← GC-safe FIFO queue                  │
│                                                          │
│  on Request(n):                                          │
│    downstreamDemand += n                                 │
│    tryFlushOutput()  ← emit from outputBuf if demand > 0 │
│    maybeRequestUpstream()                                │
│                                                          │
│  on Element(x):                                          │
│    upstreamCredit--                                      │
│    process x → outs (the transformation)                 │
│    push outs to outputBuf                                │
│    tryFlushOutput()                                      │
│    maybeRequestUpstream()                                │
└──────────────────────────────────────────────────────────┘
```

### 2.3 Credit-Based Refill

Rather than waiting until its outstanding credit is fully consumed before requesting more, a stage refills when its upstream credit drops to the **refill threshold** (default: 64, 25% of the buffer size) and it has capacity left (`InitialDemand - credit - buffered > 0`). It then requests the difference in one batch, restoring the credit window. This smooths over processing jitter without sacrificing backpressure:

```go
const (
    defaultBufferSize      = 256              // stage output buffer capacity
    defaultInitialDemand   = 224              // 87.5% of buffer: initial demand batch
    defaultRefillThreshold = 64               // refill when outstanding credit <= 64
    defaultPullTimeout     = 5 * time.Second  // actor source pull timeout
)
```

### 2.4 Overflow Strategies

The `OverflowStrategy` type controls what happens when a buffering stage cannot keep up with producers:

```go
// OverflowStrategy controls what happens when a source or buffering stage
// cannot keep up with producers.
type OverflowStrategy int

const (
    // DropHead drops the oldest element in the buffer.
    DropHead OverflowStrategy = iota
    // DropTail drops the newest (incoming) element. This is the default.
    DropTail
    // BackpressureSource blocks the producing goroutine until space is available.
    // Only valid for pull-based or goroutine-driven sources.
    BackpressureSource
    // FailSource terminates the stream with an error.
    FailSource
)
```

Today the strategy takes effect in substream routing (`SubFlow.WithSubstreamBuffer`, see §3.6), where `FailSource` terminates the stream with `ErrSubstreamOverflow` and all other strategies drop the newest element. The built-in linear sources are demand-driven: they never emit more than the signaled demand and buffer arrivals internally, so they do not drop elements. `Source.WithOverflowStrategy` and the `Buffer` flow record the strategy in `StageConfig` for stage implementations that consult it.

### 2.5 Effect on Upstream Actors

When a `Source` is backed by an actor (e.g., pulling from a GoAkt actor's state), the demand signal is delivered as an ordinary actor message. The source actor processes the demand by producing elements and sending them downstream. If it cannot produce immediately (e.g., waiting for external data), it stores the pending demand and fulfils it when data arrives; backpressure propagation is automatic through the actor model.

```
Source Actor mailbox is full → upstream producer is blocked at Tell() → natural backpressure
```

---

## 3. API Design

### 3.1 Package Structure

```
stream/
├── stream.go           // PullRequest, PullResponse
├── source.go           // Source[T] struct + constructors
├── sink.go             // Sink[T] struct + constructors + Collector/FoldResult
├── flow.go             // Flow[In,Out] struct + constructors
├── graph.go            // RunnableGraph, stage, FusionMode
├── graph_builder.go    // Graph DSL (named-node fan-in/fan-out topologies)
├── pipeline.go         // LinearGraph[T], ViaLinear()
├── subflow.go          // SubFlow, GroupBy, SplitWhen, SplitAfter
├── materializer.go     // Execution engine (actor spawning and wiring)
├── handle.go           // StreamHandle interface + streamHandleImpl + multiHandle
├── metrics.go          // StreamMetrics, stageMetrics
├── errors.go           // ErrorStrategy, SubstreamErrorStrategy, error sentinels
├── overflow.go         // OverflowStrategy
├── protocol.go         // Internal wire messages
├── queue.go            // GC-safe FIFO buffer
├── config.go           // StageConfig, RetryConfig, defaults
├── tracer.go           // Tracer and MetricsReporter interfaces
├── remote.go           // SourceRef/SinkRef (cross-node streams)
├── remote_protocol.go  // Remote control-plane wire messages
└── stage_*.go          // Stage actor implementations per type
```

### 3.2 Core Types

```go
package stream

import (
    "context"

    "github.com/tochemey/goakt/v4/actor"
)

// Source is the origin of a stream. T is the element type.
// Sources are lazy; no work happens until materialized via Run().
type Source[T any] struct{ stages []*stage }

// Flow is an intermediate transformation stage.
type Flow[In, Out any] struct{ stage *stage }

// Sink is the terminal stage.
type Sink[T any] struct{ desc *stage }

// RunnableGraph is a fully-wired pipeline ready for execution.
type RunnableGraph struct {
    stages     []*stage
    pipelines  [][]*stage
    fusionMode FusionMode
}

// StreamHandle is the live handle to a running stream.
type StreamHandle interface {
    ID()      string
    Done()    <-chan struct{}
    Err()     error
    Stop(ctx context.Context) error
    Abort()
    Metrics() StreamMetrics
}

// StreamMetrics exposes observability data for a running stream.
type StreamMetrics struct {
    ElementsIn      uint64
    ElementsOut     uint64
    DroppedElements uint64
    Errors          uint64
    BackpressureMs  float64 // cumulative ms spent waiting for demand
}
```

`BackpressureMs` is reserved: the field and its aggregation are wired through, but the built-in stage actors do not populate it yet, so it currently reads 0.

### 3.3 Source Constructors

```go
// Of creates a finite Source from a slice of values.
func Of[T any](values ...T) Source[T]

// Range creates a Source of integers from start (inclusive) to end (exclusive).
func Range(start, end int64) Source[int64]

// Unfold creates a Source from a seed value and a step function.
// Step returns (nextSeed, element, hasMore). Terminates when hasMore is false.
func Unfold[S, T any](seed S, step func(S) (S, T, bool)) Source[T]

// FromChannel creates a Source backed by a Go channel.
// The channel is drained until closed.
func FromChannel[T any](ch <-chan T) Source[T]

// FromConn creates a Source that reads raw bytes from a net.Conn.
func FromConn(conn net.Conn, bufSize int) Source[[]byte]

// FromActor creates a Source that pulls elements by sending
// PullRequest messages to a GoAkt actor.
// The actor must handle PullRequest and respond with PullResponse[T].
// An empty Elements slice signals end-of-stream.
func FromActor[T any](pid *actor.PID) Source[T]

// Tick creates a Source that emits the current time on a fixed interval.
func Tick(interval time.Duration) Source[time.Time]

// Merge combines multiple Sources into one, emitting elements as they arrive.
func Merge[T any](sources ...Source[T]) Source[T]

// MergeLatest merges N same-typed sources into a stream of []T snapshots
// holding the latest element seen on every input.
func MergeLatest[T any](sources ...Source[T]) Source[[]T]

// MergeSequence merges N sources whose elements form a contiguous sequence,
// emitting them in ascending sequence order.
func MergeSequence[T any](extractSeq func(T) int64, sources ...Source[T]) Source[T]

// MergePreferred merges N sources, always draining the preferred slot first.
func MergePreferred[T any](preferred int, sources ...Source[T]) Source[T]

// MergePrioritized merges N sources, selecting the next slot by weighted random choice.
func MergePrioritized[T any](weights []int, sources ...Source[T]) Source[T]

// Concat consumes each source in order: sources[i+1] starts only after sources[i] completes.
func Concat[T any](sources ...Source[T]) Source[T]

// Combine creates a Source by zipping two sources with a combiner function.
func Combine[T, U, V any](left Source[T], right Source[U], combine func(T, U) V) Source[V]

// Zip combines N same-typed sources into []T tuples, one element from each input.
func Zip[T any](sources ...Source[T]) Source[[]T]

// ZipWith combines N same-typed sources via the supplied combine function.
func ZipWith[T, V any](combine func([]T) V, sources ...Source[T]) Source[V]

// Broadcast fans out a single Source to N independent sources.
// Every downstream branch receives every element.
// Returns a slice of N Sources sharing the same broadcast hub actor.
func Broadcast[T any](src Source[T], n int) []Source[T]

// Balance distributes elements from a single Source to N sources in round-robin order.
// Returns a slice of N Sources sharing the same balance hub actor.
func Balance[T any](src Source[T], n int) []Source[T]

// Partition routes each element to exactly one of N branches, chosen by partitionFn.
func Partition[T any](src Source[T], n int, partitionFn func(T) int) []Source[T]

// Unzip splits a source into two sources via a function returning (left, right) parts.
func Unzip[T, A, B any](src Source[T], unzipFn func(T) (A, B)) (Source[A], Source[B])
```

### 3.4 Flow Constructors

```go
// Map applies f to each element, preserving order.
func Map[In, Out any](f func(In) Out) Flow[In, Out]

// TryMap applies f to each element; errors are handled per the stage's ErrorStrategy.
func TryMap[In, Out any](f func(In) (Out, error)) Flow[In, Out]

// Filter keeps only elements for which predicate returns true.
func Filter[T any](predicate func(T) bool) Flow[T, T]

// FlatMap applies f to each element, flattening the resulting slices.
func FlatMap[In, Out any](f func(In) []Out) Flow[In, Out]

// Flatten expands slices into individual elements.
func Flatten[T any]() Flow[[]T, T]

// Buffer introduces an asynchronous buffer of the given size.
// Decouples upstream and downstream processing rates.
func Buffer[T any](size int, strategy OverflowStrategy) Flow[T, T]

// Batch groups elements into slices of at most n elements,
// flushing early if maxWait elapses.
func Batch[T any](n int, maxWait time.Duration) Flow[T, []T]

// Throttle limits throughput to n elements per duration.
func Throttle[T any](n int, per time.Duration) Flow[T, T]

// Deduplicate suppresses consecutive duplicate elements (by equality).
func Deduplicate[T comparable]() Flow[T, T]

// Scan applies f cumulatively, emitting each intermediate state.
func Scan[In, State any](zero State, f func(State, In) State) Flow[In, State]

// ParallelMap applies f concurrently using n worker actors. Order is NOT preserved.
func ParallelMap[In, Out any](n int, f func(In) Out) Flow[In, Out]

// OrderedParallelMap applies f concurrently using n worker actors and restores element order.
func OrderedParallelMap[In, Out any](n int, f func(In) Out) Flow[In, Out]

// FlatMapConcat materializes fn's Source per element as a sub-pipeline,
// draining each one fully before the next. Ordering is preserved.
func FlatMapConcat[In, Out any](fn func(In) Source[Out]) Flow[In, Out]

// FlatMapMerge runs up to breadth sub-pipelines concurrently,
// interleaving their outputs as they arrive.
func FlatMapMerge[In, Out any](breadth int, fn func(In) Source[Out]) Flow[In, Out]

// WithContext attaches a context key/value to each element for tracing boundaries.
func WithContext[T any](key, value string) Flow[T, T]
```

### 3.5 Sink Constructors

```go
// ForEach calls f for each element.
func ForEach[T any](f func(T)) Sink[T]

// Collect accumulates all elements into a slice.
// The returned Collector blocks on Items() until the stream completes.
func Collect[T any]() (*Collector[T], Sink[T])

// Fold reduces all elements to a single value.
// The returned FoldResult blocks on Value() until the stream completes.
func Fold[T, U any](zero U, f func(U, T) U) (*FoldResult[U], Sink[T])

// First captures only the first element.
// The returned FoldResult blocks on Value() until the stream completes.
func First[T any]() (*FoldResult[T], Sink[T])

// Ignore discards all elements (useful for side-effecting flows).
func Ignore[T any]() Sink[T]

// ToActor forwards each element to the given GoAkt actor via Tell.
func ToActor[T any](pid *actor.PID) Sink[T]

// ToActorNamed forwards each element to a named GoAkt actor via Tell.
func ToActorNamed[T any](system actor.ActorSystem, name string) Sink[T]

// Chan writes elements to a Go channel. Backpressure applies if ch is full.
// The channel is closed when the stream completes.
func Chan[T any](ch chan<- T) Sink[T]
```

Result types:

```go
// Collector[T] accumulates all stream elements, blocking on Items() until complete.
type Collector[T any] struct{ /* unexported */ }

// Items blocks until the stream completes, then returns a copy of all elements.
func (c *Collector[T]) Items() []T

// FoldResult[U] holds an accumulated or captured value, blocking on Value() until complete.
type FoldResult[U any] struct{ /* unexported */ }

// Value blocks until the stream completes, then returns the result.
func (r *FoldResult[U]) Value() U
```

### 3.6 Fan-In / Fan-Out

For non-linear topologies (broadcast, balance, merge), use the free functions that return slices of `Source[T]`. Wire each branch independently into its own linear pipeline and materialize them together:

```go
// Broadcast: every branch receives every element
branches := stream.Broadcast[int](src, 2) // []Source[int] of length 2

g1 := stream.From(branches[0]).To(sink1)
g2 := stream.From(branches[1]).To(sink2)

// Materialize both pipelines independently
h1, _ := g1.Run(ctx, sys)
h2, _ := g2.Run(ctx, sys)

// Balance: round-robin distribution across branches
slots := stream.Balance[int](src, 3) // []Source[int] of length 3

// Partition: caller-controlled routing to exactly one branch per element
shards := stream.Partition[int](src, 4, func(v int) int { return v % 4 })

// Merge: combine multiple sources into one
combined := stream.Merge[int](src1, src2, src3)
```

Two further non-linear facilities build on the same machinery:

- **Graph DSL** (`NewGraph`, `AddSource`, `AddFlow`, `AddSink`, `MergeInto`, `ConcatInto`, `Build`): a named-node builder for fan-out, fan-in, and diamond topologies. Nodes are type-erased (`Source[any]`, `Flow[any, any]`, `Sink[any]`); `Build()` compiles one pipeline per sink into a single `RunnableGraph`.
- **Substreams** (`GroupBy`, `SplitWhen`, `SplitAfter`): partition a source into independent per-key or consecutive substreams (`SubFlow[K, T]`), transform each with `SubFlowVia`, and fold them back into a `Source[T]` with `MergeSubstreams`. Per-substream buffering and failure handling are configured via `WithSubstreamBuffer` and `WithErrorStrategy` (`SubstreamErrorStrategy`).

### 3.7 Pipeline Assembly: Fluent API

The primary use case is a linear pipeline assembled with method chaining:

```go
// From creates a LinearGraph builder from a Source.
func From[T any](src Source[T]) *LinearGraph[T]

// LinearGraph[T] provides a fluent builder for simple linear pipelines.
type LinearGraph[T any] struct{ src Source[T] }

func (g *LinearGraph[T]) Via(flow Flow[T, T]) *LinearGraph[T]
func (g *LinearGraph[T]) To(sink Sink[T]) RunnableGraph
func (g *LinearGraph[T]) Source() Source[T]

// ViaLinear applies a type-changing flow to a LinearGraph,
// returning a new LinearGraph of the output type.
func ViaLinear[In, Out any](g *LinearGraph[In], flow Flow[In, Out]) *LinearGraph[Out]
```

### 3.8 Example: Defining a Pipeline

Note that `LinearGraph.Via` accepts only type-preserving flows (`Flow[T, T]`). Type-changing flows are applied with the free functions `stream.Via` (on a `Source`) or `stream.ViaLinear` (on a `LinearGraph`), because Go methods cannot introduce additional type parameters.

```go
import "github.com/tochemey/goakt/v4/stream"

// Parse raw bytes, filter errors, batch, forward to persister actor.
events := stream.Via(stream.FromChannel(rawEvents), stream.Map(parseEvent)) // Source[Event]
valid := events.Via(stream.Filter(isValid))                                 // drop malformed
batches := stream.Via(valid, stream.Batch[Event](100, 50*time.Millisecond)) // Source[[]Event]
graph := batches.To(stream.ToActor[[]Event](persisterPID))

handle, err := graph.Run(ctx, actorSystem)
if err != nil {
    log.Fatal(err)
}
<-handle.Done()
if err := handle.Err(); err != nil {
    log.Printf("stream failed: %v", err)
}
```

---

## 4. Actor Integration

### 4.1 Materialization: Spawning Stage Actors

When `RunnableGraph.Run()` is called, the **materializer** walks the stage list and spawns one GoAkt actor per stage as children of a stream coordinator actor:

```
Run() called
  └─► Stream coordinator spawned  (name: "stream-supervisor-{id}")
        ├─► Source actor spawned  (name: "stream-{id}-0")
        ├─► Flow-0 actor spawned  (name: "stream-{id}-1")
        ├─► Flow-1 actor spawned  (name: "stream-{id}-2")
        └─► Sink actor spawned    (name: "stream-{id}-N", wrapped with completionWrapper)
              └─► Each stage receives stageWire with upstream/downstream PIDs
                   └─► Sink sends initial demand on stageWire receipt
                        └─► Pipeline starts executing
```

Stage actor names are deterministic and unique per materialization, making them easy to inspect in dashboards.

### 4.2 Stage Actor Lifecycle

Each stage actor type implements the `actor.Actor` interface. On receiving a `stageWire` message, a stage connects to its upstream and downstream peers. Only the sink sends initial demand at wire time; flow stages request from upstream lazily, in response to demand arriving from downstream. Key lifecycle of a flow stage:

```go
// PreStart: initialize per-stage state.

// Receive:
//   stageWire      → record upstream/downstream PIDs
//   streamRequest  → accumulate downstream demand; flush output queue;
//                    refill upstream credit if below threshold
//   streamElement  → process → output queue; tryFlushOutput; refill credit
//   streamComplete → flush remaining output; forward complete downstream; stop
//   streamError    → forward downstream; stop
//   streamCancel   → cancel upstream; shutdown
```

Element-level errors raised by the transformation itself are handled per the stage's `ErrorStrategy` (see §7.2); under `FailFast` the stage cancels upstream, sends `streamError` downstream, and stops. The sink actor additionally fires its completion hooks in `PostStop` so `Collector`/`FoldResult` waiters are always unblocked, even on `Abort`.

### 4.3 Supervision of Stage Actors

Stage actors are spawned as **children of a stream coordinator actor**. The sink actor is wrapped with a `completionWrapper` whose `PostStop` callback signals the `StreamHandle` directly. When the sink terminates:

1. The `completionWrapper`'s `PostStop` fires and reads the sink's terminal error, if any.
2. `StreamHandle.Err()` is set to the terminal error (or nil on normal completion).
3. `StreamHandle.Done()` is closed.

The coordinator additionally watches the sink to catch crashes that bypass the `completionWrapper`; in that case it signals the handle with an "unexpectedly terminated" error. Source and flow actors are not watched: they stop themselves as completion or cancellation propagates through the pipeline, before the sink's `PostStop` fires.

This model lets the stream propagate completion naturally from sink to source without needing cross-stage supervision wiring.

### 4.4 Stream Actor Tree

```
ActorSystem
└── stream-supervisor-{streamID}   (coordinator, watches sink via completionWrapper)
    ├── stream-{streamID}-0        (source actor)
    ├── stream-{streamID}-1        (flow actor)
    ├── stream-{streamID}-2        (flow actor)
    └── stream-{streamID}-N        (sink actor, wrapped with completionWrapper)
```

`StreamHandle.Abort()` shuts down the coordinator, which cascades to any remaining stage children. On normal completion the stage actors shut themselves down as completion propagates; the coordinator itself remains until it is shut down.

### 4.5 Embedding a Stream in a User Actor

A user actor can spawn and own a stream, tying the stream's lifetime to the actor's lifetime. The actor's own PID is only available inside `Receive` (via `ReceiveContext.Self()`), so the pipeline is materialized in response to a start message rather than in `PreStart`:

```go
type EventProcessorActor struct {
    streamHandle stream.StreamHandle
}

// startPipeline triggers stream materialization once the actor is running.
type startPipeline struct{}

func (a *EventProcessorActor) PreStart(_ *actor.Context) error { return nil }

func (a *EventProcessorActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *startPipeline:
        // This actor is the source: the stream pulls RawEvents from it.
        src := stream.FromActor[RawEvent](ctx.Self())
        parsed := stream.Via(src, stream.Map(parseEvent)) // RawEvent → ProcessedEvent
        graph := parsed.
            Via(stream.Filter(isValid)).
            To(stream.ToActorNamed[ProcessedEvent](ctx.ActorSystem(), "persister"))

        handle, err := graph.Run(ctx.Context(), ctx.ActorSystem())
        if err != nil {
            ctx.Err(err)
            return
        }
        a.streamHandle = handle

    case *stream.PullRequest:
        // Source actor: respond with elements on demand
        ctx.Response(&stream.PullResponse[RawEvent]{Elements: a.dequeue(msg.N)})
    }
}

func (a *EventProcessorActor) PostStop(ctx *actor.Context) error {
    if a.streamHandle != nil {
        return a.streamHandle.Stop(ctx.Context())
    }
    return nil
}
```

---

## 5. Concurrency Model

### 5.1 Single-Threaded Stage Invariant

Each stage actor processes **one message at a time**, the same guarantee GoAkt provides to all actors. This means:

- **No locks required** inside `Receive()`.
- **No data races** on stage-internal state.
- **Message ordering** from a single upstream is guaranteed by GoAkt's mailbox FIFO property.

### 5.2 Goroutines and Actors

GoAkt runs each actor's message processing on a goroutine from a shared worker pool (the dispatcher). Stream stages are no different. Stage-internal state (queues, demand counters) is plain, unsynchronized data guarded by the single-threaded actor invariant; only the metrics counters shared with the `StreamHandle` are atomic.

```
Worker pool (GoAkt dispatcher)
├── worker A → runs Source actor receive processing
├── worker B → runs Flow-0 actor receive processing
├── worker C → runs Flow-1 actor receive processing
└── worker D → runs Sink actor receive processing

Messages between stages travel through GoAkt mailboxes
(bounded blocking MPSC ring buffer by default; see §5.4).
```

### 5.3 Ordering Guarantees

| Scenario                        | Ordering guarantee                                   |
|---------------------------------|------------------------------------------------------|
| Single source → single flow     | Strictly ordered (FIFO mailbox)                      |
| Single source → fan-out → merge | Per-branch order preserved; merge interleaves fairly |
| Multiple sources → merge        | Interleaved; each source's internal order preserved  |
| `ParallelMap` (N workers)       | **Not** ordered; use `OrderedParallelMap` instead    |

For **`OrderedParallelMap`**, the stage actor tags each element with a sequence number, dispatches it round-robin to a pool of pre-spawned worker actors, and re-sequences the results with a min-heap before emitting downstream:

```
upstream → stage actor → [worker0, worker1, worker2] → min-heap resequencer → downstream
                              (parallel, unordered)      (inside the stage actor)
```

### 5.4 Throughput vs. Fairness

GoAkt's `UnboundedFairMailbox` is available for stream stages. It prevents a high-volume upstream from starving system messages. For most stream use cases the default `BoundedMailbox` (sized at `BufferSize*2`) with demand control is sufficient and offers better cache locality.

Configure per-stage:

```go
stream.Map(f).WithMailbox(actor.NewBoundedMailbox(512))
```

---

## 6. Stream Graph Execution

### 6.1 Graph Representation

Internally a stream is an ordered list of `stage` nodes:

```go
type stage struct {
    id      string
    kind    stageKind  // sourceKind | flowKind | sinkKind
    actorFn func(config StageConfig) actor.Actor
    config  StageConfig
    fuseFn  func(any) (any, bool, error)  // non-nil for fusable stateless stages
}
```

The list is constructed lazily when the user calls `Via()` / `To()`. No actors are spawned until `Run()` is called.

### 6.2 Lazy vs. Eager Execution

GoAkt Streams uses **fully lazy execution**:

- Stream descriptions are pure values; building a graph allocates only heap for the descriptor structs.
- `Run()` triggers materialization: the graph is validated, actors are spawned, and data starts flowing.
- The same `RunnableGraph` can be materialized multiple times (each `Run()` produces an independent stream).

```go
// graph is a pure value, no actors spawned yet
graph := stream.Via(stream.Range(0, 1_000_000), stream.Map(expensiveCompute)).
    To(stream.Ignore[Result]())

// run it twice, independently
h1, _ := graph.Run(ctx, sys)
h2, _ := graph.Run(ctx, sys)
```

### 6.3 Graph Validation

Before materialization, `validate()` checks:

1. **Minimum structure**: the pipeline must have at least a source and a sink (`ErrInvalidGraph`).
2. **Stage order**: the first stage must be a source and the last a sink.
3. **Type safety**: enforced statically by Go generics at assembly time; at runtime, stage actors type-assert each element and fail the stream on a mismatch.

### 6.4 Optimization Opportunities

| Optimization               | When applied                                           | Benefit                                                   |
|----------------------------|--------------------------------------------------------|-----------------------------------------------------------|
| **Stage fusion**           | Adjacent stateless map/filter stages with `fuseFn` set | Eliminate one actor and mailbox per fused pair            |
| **Demand refill batching** | Outstanding credit drops to the refill threshold       | One `Request(n)` covers many elements instead of one each |

Stage fusion is the highest-impact optimization. When adjacent flow stages are stateless and fusable (`Map`, `TryMap`, `Filter`), the whole run is merged into a single actor that applies the composed function:

```
Before fusion:
  [Map(f)] → mailbox → [Filter(p)]

After fusion:
  [Map(f) ∘ Filter(p)]   (single actor, no intermediate mailbox)
```

A fused run adopts the configuration of its last stage and always fails fast: per-stage `ErrorStrategy`, retry, and tracer hooks do not apply inside a fused actor. To keep those semantics for a stage, disable its fusion (`StageConfig.Fusion = false`) or run the graph with `FuseNone`.

Fusion is configurable per graph:

```go
graph.WithFusion(stream.FuseStateless)   // default: fuse stateless stages
graph.WithFusion(stream.FuseNone)        // disable for debugging
graph.WithFusion(stream.FuseAggressive)  // fuse all adjacent fusable stages
```

---

## 7. Error Handling

### 7.1 Error Propagation Model

Errors flow **downstream**, not upstream. When a stage encounters an error:

```
Source → Flow → [ERROR in Flow] → downstream receives streamError
                                   upstream receives streamCancel
```

### 7.2 Error Strategies

```go
// ErrorStrategy controls how a stage responds to element-level errors.
type ErrorStrategy int

const (
    // FailFast stops the entire stream on first error (default).
    FailFast ErrorStrategy = iota

    // Resume logs the error and skips the offending element.
    Resume

    // Retry reprocesses the element up to MaxAttempts times before failing.
    Retry

    // Supervise delegates to the stream's supervision strategy.
    // Currently behaves like FailFast until a dedicated stream
    // supervisor hierarchy is wired in.
    Supervise
)
```

Configure per flow or sink:

```go
stream.TryMap(riskyTransform).
    WithErrorStrategy(stream.Retry).
    WithRetryConfig(stream.RetryConfig{
        MaxAttempts: 3,
    })
```

### 7.3 Dropped Element Callback

Elements discarded due to overflow or the `Resume` error strategy can be observed via a per-stage `OnDrop` callback configured in `StageConfig`:

```go
// OnDrop is called when an element is dropped (overflow, exhausted retries).
OnDrop func(value any, reason string)
```

### 7.4 Error Sentinel Values

```go
var (
    ErrStreamCanceled    = errors.New("stream: canceled")
    ErrPullTimeout       = errors.New("stream: pull from actor source timed out")
    ErrInvalidGraph      = errors.New("stream: graph must have at least a source and a sink")
    ErrTooManySubstreams = errors.New("stream: too many substreams")
    ErrSubstreamOverflow = errors.New("stream: substream buffer overflow")
)
```

---

## 8. Performance Considerations

### 8.1 Memory Layout

Stage actors are allocated once per materialization. The hot path (element processing) minimizes heap allocation:

- **GC-safe FIFO queues** for element buffering: head-indexed arrays that zero popped slots immediately and compact when the dead prefix reaches half the backing array, preventing GC pinning of unreleased values.
- **Plain counters** for demand tracking (protected by the single-threaded actor invariant) and **atomic counters** for the metrics shared with the `StreamHandle`; no mutex contention on the critical path.
- **Slice reuse** in `Batch`: the accumulation window is reset and reused across batches; each emitted batch is a fresh copy.

### 8.2 Batching

Batching is the single most effective throughput optimization. Instead of routing one element per actor message (high per-message overhead from mailbox enqueue/dequeue), the `Batch` flow stage accumulates elements before forwarding:

```go
// Batch accumulates up to n elements or flushes after maxWait.
stream.Batch[Event](100, 50*time.Millisecond)
```

### 8.3 Network Source (FromConn)

For `[]byte` payloads from network connections, `FromConn` reads directly into allocated buffers:

```go
// FromConn reads from a net.Conn, emitting byte slices on demand.
func FromConn(conn net.Conn, bufSize int) Source[[]byte]
```

### 8.4 Benchmarking Targets

| Scenario                            | Target throughput      | Notes                           |
|-------------------------------------|------------------------|---------------------------------|
| Source → Sink (no transformation)   | > 10M elements/sec     | Single actor, bounded mailbox   |
| Source → Map → Filter → Sink        | > 5M elements/sec      | Fused (single actor)            |
| Source → Buffer(256) → Sink (async) | > 8M elements/sec      | Decoupled producer/consumer     |
| Source → Batch(100) → ToActor       | > 2M batches/sec       | 200M raw elements/sec effective |
| Fan-out to 10 sinks                 | > 1M elements/sec/sink | Per-branch backpressure         |

---

## 9. Comparison with Other Systems

### 9.1 Akka Streams

| Aspect              | Akka Streams                        | GoAkt Streams                                                    |
|---------------------|-------------------------------------|------------------------------------------------------------------|
| Model               | Graph DSL, Blueprints, Materializer | Fluent builder + fan-in/fan-out free functions                   |
| Backpressure        | Reactive Streams spec (async pull)  | Actor messages (demand-driven pull)                              |
| Stage execution     | Fused actor (Interpreter)           | One actor per stage; stateless runs fused by default             |
| Type safety         | Scala generics, shape types         | Go generics (compile-time safe)                                  |
| Error handling      | Supervision, Restart, Resume        | Same: FailFast, Resume, Retry, Supervise                         |
| Materialized values | First-class (typed future)          | StreamHandle + Collector/FoldResult                              |
| Graph complexity    | Full graph DSL (arbitrary shapes)   | Fluent linear + Graph DSL, fan-out/fan-in, substreams (DAG only) |
| Distribution        | Akka Cluster, Artery                | GoAkt remote/cluster + actor location transparency               |
| Performance         | JVM overhead, GC pauses             | Lower GC pressure, no JVM                                        |

**Key difference**: Akka Streams fuses all stages into a single actor-based interpreter by default. GoAkt Streams fuses only adjacent stateless stages by default (configurable via `WithFusion`) and keeps stateful stage boundaries visible as distinct actors for easier supervision and debugging.

### 9.2 RxGo / ReactiveX

| Aspect         | RxGo                             | GoAkt Streams                              |
|----------------|----------------------------------|--------------------------------------------|
| Model          | Observable/Observer (push-based) | Actor-backed pull with demand signaling    |
| Backpressure   | Limited (operator-dependent)     | First-class, every stage                   |
| Concurrency    | Goroutines via operators         | Actors (supervised, named, observable)     |
| Error handling | OnError terminal                 | Pluggable strategy per stage               |
| Lifecycle      | Unsubscribe                      | StreamHandle + actor supervision           |
| Integration    | Standalone                       | Native GoAkt (PID, Tell, Ask, supervision) |
| Ordering       | Operators may break order        | Explicit ordering guarantees per topology  |

**Key difference**: RxGo does not natively solve the slow-consumer problem in distributed settings. GoAkt Streams provides deterministic backpressure through demand signaling between actors.

### 9.3 Native Go Channels

| Aspect         | Go Channels                        | GoAkt Streams                         |
|----------------|------------------------------------|---------------------------------------|
| Backpressure   | Blocking on full channel           | Demand-driven pull with credit refill |
| Composition    | Manual goroutines and selects      | Declarative pipeline DSL              |
| Error handling | Sentinel values, error channels    | Typed error propagation               |
| Fan-out        | Manual goroutines + channel copies | `Broadcast`, `Balance` operators      |
| Lifecycle      | Close channel, WaitGroup           | StreamHandle with ordered shutdown    |
| Observability  | None built-in                      | Per-stage metrics, tracing hooks      |
| Supervision    | None                               | GoAkt supervisor trees                |

**Key difference**: Go channels are excellent for simple producer/consumer pairs. They become unwieldy for multi-stage, branching, error-handling pipelines at scale. GoAkt Streams provides the structure and guarantees that channels lack, without abandoning the Go concurrency model.

---

## 10. Full Example

This example demonstrates a complete stream:

1. A **source actor** emits `SensorReading` events.
2. A **flow** parses and validates them.
3. A **flow** aggregates into 100-reading windows.
4. A **sink actor** persists the windows to a time-series database.
5. Backpressure is demonstrated by a slow sink.

### 10.1 Message Types

```go
package main

import (
    "context"
    "fmt"
    "log"
    "math/rand"
    "sync/atomic"
    "time"

    "github.com/tochemey/goakt/v4/actor"
    "github.com/tochemey/goakt/v4/stream"
)

// SensorReading is the raw event from a sensor.
type SensorReading struct {
    SensorID  string
    Timestamp time.Time
    Value     float64
}

// ValidReading is a parsed and validated SensorReading.
type ValidReading struct {
    SensorID  string
    Timestamp time.Time
    Value     float64 // guaranteed: -1000 < Value < 1000
}

// ReadingWindow is an aggregated batch of readings.
type ReadingWindow struct {
    Readings  []ValidReading
    WindowEnd time.Time
    Avg       float64
}
```

### 10.2 Source Actor: Sensor Feed

```go
// SensorFeedActor simulates an IoT sensor feed.
type SensorFeedActor struct {
    sensorIDs   []string
    totalSent   atomic.Int64
    maxReadings int64
}

func (a *SensorFeedActor) PreStart(_ *actor.Context) error {
    a.sensorIDs = []string{"sensor-1", "sensor-2", "sensor-3", "sensor-4"}
    return nil
}

func (a *SensorFeedActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *stream.PullRequest:
        // Honour backpressure: produce exactly N elements.
        if a.totalSent.Load() >= a.maxReadings {
            // Signal end of stream
            ctx.Response(&stream.PullResponse[SensorReading]{Elements: nil})
            return
        }

        elements := make([]SensorReading, 0, msg.N)
        for i := int64(0); i < msg.N; i++ {
            if a.totalSent.Load() >= a.maxReadings {
                break
            }
            elements = append(elements, SensorReading{
                SensorID:  a.sensorIDs[rand.Intn(len(a.sensorIDs))],
                Timestamp: time.Now(),
                Value:     rand.Float64()*2000 - 1000, // -1000..1000, some invalid
            })
            a.totalSent.Add(1)
        }
        ctx.Response(&stream.PullResponse[SensorReading]{Elements: elements})
    }
}

func (a *SensorFeedActor) PostStop(_ *actor.Context) error {
    fmt.Printf("SensorFeed: emitted %d readings total\n", a.totalSent.Load())
    return nil
}
```

### 10.3 Sink Actor: Time-Series Persister

```go
// PersistActor writes reading windows to a (simulated) time-series DB.
// It is deliberately slow to demonstrate backpressure.
type PersistActor struct {
    persisted atomic.Int64
    slowMs    int // artificial latency
}

func (a *PersistActor) PreStart(_ *actor.Context) error { return nil }

func (a *PersistActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case ReadingWindow:
        // Simulate slow write (backpressure propagates upstream).
        time.Sleep(time.Duration(a.slowMs) * time.Millisecond)
        a.persisted.Add(int64(len(msg.Readings)))
        fmt.Printf("Persisted window: avg=%.2f readings=%d\n",
            msg.Avg, len(msg.Readings))
    }
}

func (a *PersistActor) PostStop(_ *actor.Context) error {
    fmt.Printf("Persister: stored %d readings total\n", a.persisted.Load())
    return nil
}
```

### 10.4 Pipeline Assembly and Execution

```go
func main() {
    ctx := context.Background()

    // Boot the actor system.
    sys, err := actor.NewActorSystem("sensor-pipeline")
    if err != nil {
        log.Fatal(err)
    }
    if err := sys.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer sys.Stop(ctx)

    // Spawn the source actor.
    sensorPID, err := sys.Spawn(ctx, "sensor-feed", &SensorFeedActor{maxReadings: 1_000_000})
    if err != nil {
        log.Fatal(err)
    }

    // Spawn the sink actor.
    persisterPID, err := sys.Spawn(ctx, "persister", &PersistActor{slowMs: 5})
    if err != nil {
        log.Fatal(err)
    }

    // ── Transformation functions ──────────────────────────────────────────────

    parseAndValidate := func(r SensorReading) (ValidReading, error) {
        if r.Value < -1000 || r.Value > 1000 {
            return ValidReading{}, fmt.Errorf("out-of-range value: %f", r.Value)
        }
        return ValidReading{
            SensorID:  r.SensorID,
            Timestamp: r.Timestamp,
            Value:     r.Value,
        }, nil
    }

    windowToAggregate := func(batch []ValidReading) ReadingWindow {
        var sum float64
        for _, r := range batch {
            sum += r.Value
        }
        return ReadingWindow{
            Readings:  batch,
            WindowEnd: time.Now(),
            Avg:       sum / float64(len(batch)),
        }
    }

    // ── Assemble the pipeline ─────────────────────────────────────────────────
    //
    //   SensorFeedActor
    //        │  (demand-driven pull, ~224 elements at a time)
    //        ▼
    //   parseAndValidate   (TryMap with error → Resume on invalid readings)
    //        │
    //        ▼
    //   Batch(100, 50ms)   (micro-batch; flushes on count OR timeout)
    //        │  (backpressure: waits for demand from sink before filling next batch)
    //        ▼
    //   windowToAggregate  (map batch → ReadingWindow)
    //        │
    //        ▼
    //   PersistActor       (slow consumer; backpressure propagates all the way to source)

    intSrc := stream.FromActor[SensorReading](sensorPID)
    validSrc := stream.Via(intSrc,
        stream.TryMap(parseAndValidate).
            WithErrorStrategy(stream.Resume), // skip out-of-range readings
    )

    // Batch and Map change the element type, so they are applied with the
    // free Via function rather than the type-preserving Via method.
    batched := stream.Via(validSrc, stream.Batch[ValidReading](100, 50*time.Millisecond))
    windows := stream.Via(batched, stream.Map(windowToAggregate))
    pipeline := windows.To(stream.ToActor[ReadingWindow](persisterPID))

    // ── Materialize and run ───────────────────────────────────────────────────

    handle, err := pipeline.
        WithFusion(stream.FuseStateless). // fuse adjacent stateless stages
        Run(ctx, sys)
    if err != nil {
        log.Fatalf("failed to start stream: %v", err)
    }

    // ── Monitor ───────────────────────────────────────────────────────────────

    go func() {
        ticker := time.NewTicker(2 * time.Second)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                m := handle.Metrics()
                fmt.Printf("[metrics] in=%d out=%d dropped=%d errors=%d\n",
                    m.ElementsIn, m.ElementsOut, m.DroppedElements, m.Errors)
            case <-handle.Done():
                return
            }
        }
    }()

    // ── Wait for completion ───────────────────────────────────────────────────

    <-handle.Done()
    if err := handle.Err(); err != nil {
        log.Printf("stream terminated with error: %v", err)
    } else {
        fmt.Println("stream completed successfully")
    }

    finalMetrics := handle.Metrics()
    fmt.Printf("[final] elementsIn=%d elementsOut=%d dropped=%d\n",
        finalMetrics.ElementsIn,
        finalMetrics.ElementsOut,
        finalMetrics.DroppedElements,
    )
}
```

### 10.5 Backpressure Trace

With the slow persister (5ms per window) and 100-element batches:

```
t=0ms   Sink receives stageWire → sends Request(224) to the Map stage
t=0ms   First demand propagates stage by stage: Map, Batch, and TryMap each
        forward Request(224) to their upstream
t=0ms   Source receives Request(224) → sends PullRequest{N: 224} to SensorFeedActor
t=1ms   Source emits 224 SensorReadings → TryMap
t=2ms   TryMap forwards ~112 valid readings (50% pass rate); every dropped
        reading also consumes upstream credit, so TryMap's credit soon falls
        to the 64 threshold and it sends one batched refill Request to Source
t=2ms   Batch accumulates 100 valid readings → flushes a window
t=2ms   Map turns the window into a ReadingWindow → Sink begins a 5ms write
t=7ms   Sink finishes the write; its credit (223 of 224) is far above the
        refill threshold, so no new demand is sent yet
...     Steady state: the sink consumes one window (100 readings) per ~5ms
        and refills its credit in batches of 160 once it drops to 64.
        Upstream stages refill the same way, so the source is only ever
        asked for what the sink can absorb (~100 readings per 5ms ≈ 20k/s)
        instead of producing at its full unconstrained rate.
```

This demonstrates that the slow sink automatically constrains the entire pipeline via demand propagation; no explicit rate-limiting code is required.

---

## Appendix A: Protocol Sequence Diagram

```
Source              Flow (TryMap)        Flow (Batch)          Sink

  │                       │                    │                  │
  │◄─ stageWire ──────────┼────────────────────┼──────────────    │  (materializer wires every stage)
  │                       │                    │◄─ Request(224) ──│  (sink initial demand)
  │                       │◄── Request(224) ───│                  │
  │◄── Request(224) ──────│                    │                  │
  │── Element(r1) ───────►│                    │                  │
  │── Element(r2) ───────►│── Element(v1) ────►│                  │
  │   ... (224 total)     │   (valid only)     │                  │
  │                       │── Element(v2) ────►│                  │
  │                       │   ...              │── Batch([100]) ─►│  (5ms write)
  │◄── Request(n) ────────│                    │                  │
  │   (credit fell to 64: │                    │                  │
  │    batched refill)    │                    │                  │
  │── Element(r225) ─────►│                    │                  │
  │   ...                 │                    │◄─ Request(160) ──│
  │                       │◄── Request(n) ─────│  (after 160      │
  │                       │                    │   windows)       │
```

---

## Appendix B: Stage Configuration Reference

```go
type StageConfig struct {
    // InitialDemand is the first batch of demand the sink sends upstream.
    // Default: 224 (87.5% of BufferSize)
    InitialDemand int64

    // RefillThreshold is the number of elements consumed before requesting a refill.
    // Default: 64 (25% of BufferSize)
    RefillThreshold int64

    // ErrorStrategy controls element-level error handling.
    // Default: FailFast
    ErrorStrategy ErrorStrategy

    // RetryConfig is used when ErrorStrategy == Retry.
    RetryConfig RetryConfig

    // OverflowStrategy is used by Source stages when the source buffer is full.
    // Default: DropTail
    OverflowStrategy OverflowStrategy

    // PullTimeout is the timeout for pulls from actor sources.
    // Default: 5s
    PullTimeout time.Duration

    // System is set by the materializer; allows composite source actors (Merge, Combine)
    // to spawn sub-pipelines at wire time.
    System actor.ActorSystem

    // Metrics is an optional shared metrics collector injected by the materializer.
    // When nil, stage actors use a local, unobserved stageMetrics.
    Metrics *stageMetrics

    // BufferSize is the capacity for this stage's mailbox (BoundedMailbox(BufferSize*2)).
    // Default: 256
    BufferSize int

    // Mailbox overrides the default actor mailbox for this stage.
    // When nil, a BoundedMailbox(BufferSize*2) is used.
    Mailbox actor.Mailbox

    // Name overrides the auto-generated actor name for this stage.
    Name string

    // Tags are propagated to metrics and traces.
    Tags map[string]string

    // Tracer is an optional hook for distributed tracing.
    Tracer Tracer

    // OnDrop is called when an element is dropped (overflow, exhausted retries).
    OnDrop func(value any, reason string)

    // Fusion controls whether this stage may be fused with adjacent stateless stages.
    // Default: true
    Fusion bool
}
```

---

## Appendix C: Observability Hooks

```go
// Tracer is an optional hook for distributed tracing integration.
type Tracer interface {
    // OnElement is called for every element passing through a stage.
    OnElement(stageName string, seqNo uint64, latencyNs int64)
    // OnDemand is called when a stage sends a demand signal upstream.
    OnDemand(stageName string, n int64)
    // OnError is called when a stage encounters an element-level error.
    OnError(stageName string, err error)
    // OnComplete is called when a stage receives a completion signal.
    OnComplete(stageName string)
}

// Attach a tracer to individual stages:
stream.Map(f).WithTracer(otelTracer)
src.WithTracer(otelTracer)  // Source[T] method
sink.WithTracer(otelTracer)
```

Tracer hooks fire only on unfused stages; a fused run of stateless stages bypasses them (see §6.4).

---

## Appendix D: Roadmap

| Feature                                        | Status      | Notes                                         |
|------------------------------------------------|-------------|-----------------------------------------------|
| Linear pipeline + basic flows                  | Implemented | Core implementation                           |
| Fan-out (Broadcast, Balance, Partition, Unzip) | Implemented | Required for real workloads                   |
| Fan-in (Merge variants, Concat, Combine, Zip)  | Implemented | Required for real workloads                   |
| Graph DSL                                      | Implemented | Named-node builder for fan-out/fan-in/diamond |
| Substreams (GroupBy, SplitWhen, SplitAfter)    | Implemented | Per-key pipelines with MergeSubstreams        |
| Stage fusion                                   | Implemented | Major throughput improvement                  |
| Ordered parallel map                           | Implemented | Parallel workers with resequencer             |
| Remote source/sink                             | Implemented | SourceRef/SinkRef wire-portable handles       |
| Exactly-once with journaling                   | Planned     | Append-only log for at-least-once + dedup     |
| Dynamic topology reconfiguration               | Planned     | Add/remove stages at runtime                  |
| WASM stage execution                           | Planned     | Run stage logic in sandboxed WASM module      |
