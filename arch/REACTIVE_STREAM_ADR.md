# GoAkt Reactive Streams — Architecture

**Status:** Implemented
**Target:** GoAkt v4.x
**Author:** GoAkt Core Team

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

GoAkt Streams is a reactive, demand-driven data processing library built on top of the GoAkt actor model. It does **not** replace actors — it **composes** with them. Every stage in a stream pipeline runs inside an actor, which means it inherits GoAkt's supervision, lifecycle management, location transparency, and fault tolerance for free.

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

#### `Source[T]` — the origin of elements

A `Source[T]` is a lazy description of how to produce a stream of `T` values. Nothing runs until materialized via `Run()`. Internally it materializes as a GoAkt actor with a bounded output buffer.

```go
// Source produces elements of type T.
// It is a lazy description — nothing runs until materialized.
type Source[T any] struct {
    stages []*stageDesc
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

#### `Flow[In, Out]` — a transformation stage

A `Flow[In, Out]` is a processing stage that consumes elements of type `In` and produces elements of type `Out`. It is both a **Subscriber** (to its upstream) and a **Publisher** (to its downstream). In GoAkt terms it is a **Processor** actor.

```go
// Flow transforms a stream of In into a stream of Out.
type Flow[In, Out any] struct {
    desc *stageDesc
}

// Configure per-stage behavior:
func (f Flow[In, Out]) WithErrorStrategy(s ErrorStrategy) Flow[In, Out]
func (f Flow[In, Out]) WithRetryConfig(rc RetryConfig) Flow[In, Out]
func (f Flow[In, Out]) WithMailbox(mailbox actor.Mailbox) Flow[In, Out]
func (f Flow[In, Out]) WithName(name string) Flow[In, Out]
func (f Flow[In, Out]) WithTags(tags map[string]string) Flow[In, Out]
func (f Flow[In, Out]) WithTracer(t Tracer) Flow[In, Out]
```

#### `Sink[T]` — the terminal consumer

A `Sink[T]` is the terminal stage of a pipeline. It subscribes to upstream and applies a terminal operation (collect, fold, forward to actor, write to channel, etc.).

```go
// Sink consumes elements of type T.
type Sink[T any] struct {
    desc *stageDesc
}

// Configure per-stage behavior:
func (s Sink[T]) WithErrorStrategy(strategy ErrorStrategy) Sink[T]
func (s Sink[T]) WithRetryConfig(rc RetryConfig) Sink[T]
func (s Sink[T]) WithMailbox(mailbox actor.Mailbox) Sink[T]
func (s Sink[T]) WithName(name string) Sink[T]
func (s Sink[T]) WithTags(tags map[string]string) Sink[T]
func (s Sink[T]) WithTracer(t Tracer) Sink[T]
```

#### `RunnableGraph` — an assembled pipeline ready to run

```go
// RunnableGraph is a fully connected stream pipeline that can be materialized.
type RunnableGraph struct {
    stages     []*stageDesc    // ordered stage list for a linear pipeline
    pipelines  [][]*stageDesc  // multi-pipeline graphs (fan-in/fan-out)
    fusionMode FusionMode
}

// Run materializes the graph within the given ActorSystem.
// Returns a handle for lifecycle control and observability.
func (g RunnableGraph) Run(ctx context.Context, system actor.ActorSystem) (StreamHandle, error)

// WithFusion configures the stage fusion mode for this graph.
func (g RunnableGraph) WithFusion(mode FusionMode) RunnableGraph
```

#### `StreamHandle` — runtime control of a running stream

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

### 1.3 Demand Model — Push/Pull Hybrid

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
- A stage propagates demand upstream **before** its local buffer is fully consumed (watermark-based prefetch).
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

### 2.2 Stage Internals — the Demand Ledger

Each stage maintains a **demand ledger** — an atomic int64 tracking outstanding demand from downstream:

```
┌────────────────────────────────────────────────────────┐
│                   Flow Stage Actor                     │
│                                                        │
│  downstreamDemand  ← atomic int64 (demand from sink)   │
│  inFlight          ← atomic int64 (sent, not acked)    │
│  inputBuffer       ← GC-safe FIFO queue                │
│  outputBuffer      ← GC-safe FIFO queue                │
│                                                        │
│  on Request(n):                                        │
│    downstreamDemand += n                               │
│    tryFlush()   ← emit from outputBuffer if demand > 0 │
│    if inputBuffer below watermark:                     │
│      send Request(refill) upstream                     │
│                                                        │
│  on Element(x):                                        │
│    write x to inputBuffer                              │
│    process x → y (the transformation)                  │
│    write y to outputBuffer                             │
│    tryFlush()                                          │
└────────────────────────────────────────────────────────┘
```

### 2.3 Watermark-Based Prefetch

Rather than waiting until its buffer is empty before requesting more, a stage requests upstream refill when the input buffer drops below a **low watermark** (default: 25% of capacity). This smooths over processing jitter without sacrificing backpressure:

```go
const (
    defaultBufferSize      = 256              // stage output buffer capacity
    defaultInitialDemand   = 224              // 87.5% of buffer — initial demand batch
    defaultRefillThreshold = 64               // refill when consumed >= 64 elements
    defaultPullTimeout     = 5 * time.Second  // actor source pull timeout
)
```

### 2.4 Overflow Strategies for Sources

Sources that produce faster than the pipeline can consume apply a configurable **overflow strategy** to elements they cannot immediately deliver:

```go
// OverflowStrategy controls what happens when a Source's output buffer is full.
type OverflowStrategy int

const (
    // DropHead drops the oldest element in the buffer.
    DropHead OverflowStrategy = iota
    // DropTail drops the newest (incoming) element. This is the default.
    DropTail
    // BackpressureSource blocks the source goroutine until space is available.
    // Only valid for pull-based sources.
    BackpressureSource
    // FailSource terminates the stream with an error.
    FailSource
)
```

### 2.5 Effect on Upstream Actors

When a `Source` is backed by an actor (e.g., pulling from a GoAkt actor's state), the demand signal is delivered as an ordinary actor message. The source actor processes the demand by producing elements and sending them downstream. If it cannot produce immediately (e.g., waiting for external data), it stores the pending demand and fulfils it when data arrives — backpressure propagation is automatic through the actor model.

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
├── graph.go            // RunnableGraph, stageDesc, FusionMode
├── pipeline.go         // LinearGraph[T], From(), ViaLinear()
├── materializer.go     // Execution engine (actor spawning and wiring)
├── handle.go           // StreamHandle interface + streamHandleImpl
├── metrics.go          // StreamMetrics, stageMetrics
├── errors.go           // ErrorStrategy, error sentinels
├── overflow.go         // OverflowStrategy
├── protocol.go         // Internal wire messages
├── queue.go            // GC-safe FIFO buffer
├── config.go           // StageConfig, RetryConfig, defaults
├── tracer.go           // Tracer interface
└── stage_*.go          // Stage actor implementations per type
```

### 3.2 Core Types

```go
package stream

import (
    "context"
    "time"

    "github.com/tochemey/goakt/v4/actor"
)

// Source is the origin of a stream. T is the element type.
// Sources are lazy — no work happens until materialized via Run().
type Source[T any] struct{ stages []*stageDesc }

// Flow is an intermediate transformation stage.
type Flow[In, Out any] struct{ desc *stageDesc }

// Sink is the terminal stage.
type Sink[T any] struct{ desc *stageDesc }

// RunnableGraph is a fully-wired pipeline ready for execution.
type RunnableGraph struct {
    stages     []*stageDesc
    pipelines  [][]*stageDesc
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

// Combine creates a Source by zipping two sources with a combiner function.
func Combine[T, U, V any](left Source[T], right Source[U], combine func(T, U) V) Source[V]

// Broadcast fans out a single Source to N independent sources.
// Every downstream branch receives every element.
// Returns a slice of N Sources sharing the same broadcast hub actor.
func Broadcast[T any](src Source[T], n int) []Source[T]

// Balance distributes elements from a single Source to N sources in round-robin order.
// Returns a slice of N Sources sharing the same balance hub actor.
func Balance[T any](src Source[T], n int) []Source[T]
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
// Broadcast — every branch receives every element
branches := stream.Broadcast[int](src, 2) // []Source[int] of length 2

g1 := stream.From(branches[0]).To(sink1)
g2 := stream.From(branches[1]).To(sink2)

// Materialize both pipelines independently
h1, _ := g1.Run(ctx, sys)
h2, _ := g2.Run(ctx, sys)

// Balance — round-robin distribution across branches
slots := stream.Balance[int](src, 3) // []Source[int] of length 3

// Merge — combine multiple sources into one
combined := stream.Merge[int](src1, src2, src3)
```

### 3.7 Pipeline Assembly — Fluent API

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

```go
import "github.com/tochemey/goakt/v4/stream"

// Parse raw bytes, filter errors, batch, forward to persister actor.
graph := stream.From(stream.FromChannel(rawEvents)).
    Via(stream.Map(parseEvent)).                              // []byte → Event
    Via(stream.Filter(isValid)).                             // drop malformed
    Via(stream.Batch[Event](100, 50*time.Millisecond)).      // micro-batch
    To(stream.ToActor[[]Event](persisterPID))

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

### 4.1 Materialization — Spawning Stage Actors

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

Each stage actor type implements the `actor.Actor` interface. On receiving a `stageWire` message, a stage connects to its upstream and downstream peers and begins pulling elements. Key lifecycle:

```go
// PreStart: initialize internal queue and per-stage state.

// Receive:
//   stageWire      → wire upstream/downstream PIDs; send initial demand to upstream
//   streamRequest  → accumulate downstream demand; flush output queue
//   streamElement  → push to input queue; process → output queue; tryFlush
//   streamComplete → drain remaining; forward complete downstream; stop
//   streamError    → apply ErrorStrategy; propagate or handle
//   streamCancel   → cancel upstream; shutdown

// PostStop:
//   signal downstream with streamComplete (if not already sent)
```

### 4.3 Supervision of Stage Actors

Stage actors are spawned as **children of a stream coordinator actor**. The coordinator watches only the **sink actor** (via a `completionWrapper`). If the sink terminates:

1. The coordinator detects termination via the `completionWrapper`'s `PostStop` callback.
2. `StreamHandle.Err()` is set to the terminal error (or nil on normal completion).
3. `StreamHandle.Done()` is closed.
4. All remaining stage actors are stopped.

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

The coordinator is automatically removed when the stream terminates.

### 4.5 Embedding a Stream in a User Actor

A user actor can spawn and own a stream, tying the stream's lifetime to the actor's lifetime:

```go
type EventProcessorActor struct {
    streamHandle stream.StreamHandle
}

func (a *EventProcessorActor) PreStart(ctx *actor.Context) error {
    pipeline := stream.From(stream.FromActor[RawEvent](ctx.Self())).
        Via(stream.Map(parseEvent)).
        Via(stream.Filter(isValid)).
        To(stream.ToActorNamed[ProcessedEvent](ctx.ActorSystem(), "persister"))

    handle, err := pipeline.Run(ctx.Context(), ctx.ActorSystem())
    if err != nil {
        return err
    }
    a.streamHandle = handle

    // Watch the stream; notify system when it ends
    go func() {
        <-handle.Done()
        ctx.ActorSystem().EventStream().Publish(&StreamTerminated{ID: handle.ID()})
    }()
    return nil
}

func (a *EventProcessorActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *stream.PullRequest:
        // Source actor: respond with elements on demand
        ctx.Response(&stream.PullResponse[RawEvent]{Elements: a.dequeue(msg.N)})
    }
}

func (a *EventProcessorActor) PostStop(ctx *actor.Context) error {
    return a.streamHandle.Stop(ctx.Context())
}
```

---

## 5. Concurrency Model

### 5.1 Single-Threaded Stage Invariant

Each stage actor processes **one message at a time** — the same guarantee GoAkt provides to all actors. This means:

- **No locks required** inside `Receive()`.
- **No data races** on stage-internal state.
- **Message ordering** from a single upstream is guaranteed by GoAkt's mailbox FIFO property.

### 5.2 Goroutines and Actors

GoAkt runs each actor's receive loop in a goroutine from a shared pool (via the scheduler). Stream stages are no different. All internal data structures (queues, demand counters) require only atomic operations, not mutexes.

```
Goroutine pool (GoAkt scheduler)
├── goroutine A → runs Source actor receive loop
├── goroutine B → runs Flow-0 actor receive loop
├── goroutine C → runs Flow-1 actor receive loop
└── goroutine D → runs Sink actor receive loop

Messages between stages travel through GoAkt mailboxes (lock-free MPSC queue).
```

### 5.3 Ordering Guarantees

| Scenario                        | Ordering guarantee                                   |
|---------------------------------|------------------------------------------------------|
| Single source → single flow     | Strictly ordered (FIFO mailbox)                      |
| Single source → fan-out → merge | Per-branch order preserved; merge interleaves fairly |
| Multiple sources → merge        | Interleaved; each source's internal order preserved  |
| `ParallelMap` (N workers)       | **Not** ordered; use `OrderedParallelMap` instead    |

For **`OrderedParallelMap`**, elements are tagged with their sequence number before entering a pool of parallel worker actors and re-sequenced before reaching the downstream:

```
source → sequencer → [worker0, worker1, worker2] → resequencer → sink
                           (parallel, unordered)        (restore order)
```

### 5.4 Throughput vs. Fairness

GoAkt's `UnboundedFairMailbox` is available for stream stages. It prevents a high-volume upstream from starving system messages. For most stream use cases the default `BoundedMailbox` with demand control is sufficient and offers better cache locality.

Configure per-stage:

```go
stream.Map(f).WithMailbox(actor.BoundedMailbox(512))
```

---

## 6. Stream Graph Execution

### 6.1 Graph Representation

Internally a stream is an ordered list of `stageDesc` nodes:

```go
type stageDesc struct {
    id        string
    kind      stageKind  // sourceKind | flowKind | sinkKind
    makeActor func(config StageConfig) actor.Actor
    config    StageConfig
    fuseFn    func(any) (any, bool, error)  // non-nil for fusable stateless stages
}
```

The list is constructed lazily when the user calls `Via()` / `To()`. No actors are spawned until `Run()` is called.

### 6.2 Lazy vs. Eager Execution

GoAkt Streams uses **fully lazy execution**:

- Stream descriptions are pure values — building a graph allocates only heap for the descriptor structs.
- `Run()` triggers materialization: the graph is validated, actors are spawned, and data starts flowing.
- The same `RunnableGraph` can be materialized multiple times (each `Run()` produces an independent stream).

```go
// graph is a pure value, no actors spawned yet
graph := stream.From(stream.Range(0, 1_000_000)).
    Via(stream.Map(expensiveCompute)).
    To(stream.Ignore[Result]())

// run it twice, independently
h1, _ := graph.Run(ctx, sys)
h2, _ := graph.Run(ctx, sys)
```

### 6.3 Graph Validation

Before materialization, `validate()` checks:

1. **Minimum structure**: the pipeline must have at least a source and a sink (`ErrInvalidGraph`).
2. **Type safety**: enforced statically by Go generics at compile time — no runtime type assertions in the hot path.

### 6.4 Optimization Opportunities

| Optimization           | When applied                                                  | Benefit                                        |
|------------------------|---------------------------------------------------------------|------------------------------------------------|
| **Stage fusion**       | Two adjacent stateless map/filter with no buffer between them | Eliminate one actor and mailbox per fused pair |
| **Demand aggregation** | Multiple demand signals arrive before being sent upstream     | Coalesce into single `Request(sum)`            |

Stage fusion is the highest-impact optimization. When two adjacent stages are both stateless and fusable, they are merged into a single actor:

```
Before fusion:
  [Map(f)] → mailbox → [Filter(p)]

After fusion:
  [Map(f) ∘ Filter(p)]   (single actor, no intermediate mailbox)
```

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
    ErrStreamCanceled = errors.New("stream: canceled")
    ErrPullTimeout    = errors.New("stream: pull from actor source timed out")
    ErrInvalidGraph   = errors.New("stream: graph must have at least a source and a sink")
)
```

---

## 8. Performance Considerations

### 8.1 Memory Layout

Stage actors are allocated once per materialization. The hot path (element processing) minimizes heap allocation:

- **GC-safe FIFO queues** for element buffering: head-indexed arrays with automatic compaction when the dead prefix exceeds 50%, preventing GC pinning of unreleased values.
- **Atomic counters** for demand tracking and metrics — no mutex contention on the critical path.
- **Slice reuse** in `Batch`: the batch slice is reset and reused across windows.

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

| Aspect              | Akka Streams                        | GoAkt Streams                                      |
|---------------------|-------------------------------------|----------------------------------------------------|
| Model               | Graph DSL, Blueprints, Materializer | Fluent builder + fan-in/fan-out free functions     |
| Backpressure        | Reactive Streams spec (async pull)  | Actor messages (demand-driven pull)                |
| Stage execution     | Fused actor (Interpreter)           | One actor per stage (fusion optional)              |
| Type safety         | Scala generics, shape types         | Go generics (compile-time safe)                    |
| Error handling      | Supervision, Restart, Resume        | Same: FailFast, Resume, Retry, Supervise           |
| Materialized values | First-class (typed future)          | StreamHandle + Collector/FoldResult                |
| Graph complexity    | Full graph DSL (arbitrary shapes)   | Linear + Broadcast/Balance/Merge (DAG only)        |
| Distribution        | Akka Cluster, Artery                | GoAkt remote/cluster + actor location transparency |
| Performance         | JVM overhead, GC pauses             | Lower GC pressure, no JVM                          |

**Key difference**: Akka Streams fuses multiple stages into a single actor-based interpreter by default. GoAkt Streams keeps stage boundaries visible as distinct actors (optional fusion) for easier supervision and debugging.

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

| Aspect         | Go Channels                        | GoAkt Streams                      |
|----------------|------------------------------------|------------------------------------|
| Backpressure   | Blocking on full channel           | Demand-driven pull with watermarks |
| Composition    | Manual goroutines and selects      | Declarative pipeline DSL           |
| Error handling | Sentinel values, error channels    | Typed error propagation            |
| Fan-out        | Manual goroutines + channel copies | `Broadcast`, `Balance` operators   |
| Lifecycle      | Close channel, WaitGroup           | StreamHandle with ordered shutdown |
| Observability  | None built-in                      | Per-stage metrics, tracing hooks   |
| Supervision    | None                               | GoAkt supervisor trees             |

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

### 10.2 Source Actor — Sensor Feed

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

### 10.3 Sink Actor — Time-Series Persister

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
    case *ReadingWindow:
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
    sys, err := actor.NewActorSystem("sensor-pipeline",
        actor.WithLogger(log.Default()),
    )
    if err != nil {
        log.Fatal(err)
    }
    if err := sys.Start(); err != nil {
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
    //   PersistActor       (slow consumer — backpressure propagates all the way to source)

    intSrc := stream.FromActor[SensorReading](sensorPID)
    validSrc := stream.Via(intSrc,
        stream.TryMap(parseAndValidate).
            WithErrorStrategy(stream.Resume), // skip out-of-range readings
    )

    pipeline := stream.From(validSrc).
        Via(stream.Batch[ValidReading](100, 50*time.Millisecond)).
        Via(stream.Map(windowToAggregate)).
        To(stream.ToActor[ReadingWindow](persisterPID))

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
                fmt.Printf("[metrics] in=%d out=%d dropped=%d bp_wait=%.1fms\n",
                    m.ElementsIn, m.ElementsOut, m.DroppedElements, m.BackpressureMs)
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
t=0ms   Source starts, receives Request(224) from flow actor (initial demand)
t=0ms   Source emits 224 SensorReadings → TryMap actor mailbox
t=1ms   TryMap processes 224 → ~112 valid readings (50% pass rate)
t=2ms   Batch accumulates: flushes window of 100 at t=52ms (50ms timeout)
t=52ms  Aggregator receives batch[100], emits ReadingWindow → Sink
t=52ms  Sink begins 5ms write; does NOT send Request upstream yet
t=57ms  Sink write complete → sends Request(1) upstream → demand propagates
t=57ms  Batch actor has 12 buffered readings, needs 88 more → sends Request(88) to TryMap
t=57ms  TryMap actor: inputBuf=180, above low watermark — does NOT request from Source
t=80ms  inputBuf drops to 64 (low watermark) → TryMap sends Request(160) to Source
t=80ms  Source receives Request(160) → emits 160 more readings
         ── backpressure has throttled source from ~100k/s to ~100 per 57ms ≈ 1750/s
         ── this matches the sink's sustainable rate: 100 readings / 57ms ≈ 1754/s ✓
```

This demonstrates that the slow sink automatically constrains the entire pipeline via demand propagation — no explicit rate-limiting code required.

---

## Appendix A: Protocol Sequence Diagram

```
Source              Flow (TryMap+Batch)      Flow (Map)           Sink

  │                       │                      │                  │
  │                       │◄── stageWire ─────────────────────────  │
  │◄── stageWire ─────────│                      │                  │
  │                       │◄─── Request(224) ────│                  │
  │◄── Request(224) ──────│                      │                  │
  │                       │                      │◄── Request(1) ───│
  │── Element(r1) ───────►│                      │                  │
  │── Element(r2) ───────►│                      │                  │
  │   ... (224 total)     │── Element(v1) ──────►│                  │
  │                       │   (valid only)       │                  │
  │                       │── Element(v2) ──────►│                  │
  │                       │   ...                │── Batch([100])─► │
  │                       │                      │                  │  (5ms write)
  │                       │                      │◄── Request(1) ───│
  │                       │◄── Request(88) ──────│                  │
  │                       │   (need 88 more for  │                  │
  │                       │    next batch)       │                  │
  │◄── Request(160) ──────│   (buf below wmark)  │                  │
  │── Element(r225) ─────►│                      │                  │
  │   ...                 │                      │                  │
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
stream.From(src).WithTracer(otelTracer)
sink.WithTracer(otelTracer)
```

---

## Appendix D: Roadmap

| Feature                          | Status      | Notes                                     |
|----------------------------------|-------------|-------------------------------------------|
| Linear pipeline + basic flows    | Implemented | Core implementation                       |
| Fan-out (Broadcast, Balance)     | Implemented | Required for real workloads               |
| Fan-in (Merge, Combine)          | Implemented | Required for real workloads               |
| Stage fusion                     | Implemented | Major throughput improvement              |
| Ordered parallel map             | Implemented | Parallel workers with resequencer         |
| Remote source/sink               | Implemented | Source/Sink backed by remote GoAkt actors |
| Exactly-once with journaling     | Planned     | Append-only log for at-least-once + dedup |
| Dynamic topology reconfiguration | Planned     | Add/remove stages at runtime              |
| WASM stage execution             | Planned     | Run stage logic in sandboxed WASM module  |
