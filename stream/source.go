// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package stream

import (
	"fmt"
	mrand "math/rand/v2"
	"net"
	"time"

	"github.com/tochemey/goakt/v4/actor"
)

// Source is a lazy description of a stream origin that produces elements of type T.
// Nothing executes until the graph is materialized via RunnableGraph.Run().
//
// Build pipelines using Via (type-preserving) or the free function stream.Via (type-changing),
// then terminate with To to obtain a RunnableGraph.
type Source[T any] struct {
	stages []*stageDesc // ordered: [source, flow0, flow1, ...]
}

// Via applies a type-preserving Flow to this Source, returning a new Source[T].
// For type-changing transformations use the package-level Via free function.
func (s Source[T]) Via(flow Flow[T, T]) Source[T] {
	return Via(s, flow)
}

// To attaches a Sink[T], completing the graph and returning a RunnableGraph.
func (s Source[T]) To(sink Sink[T]) RunnableGraph {
	stages := make([]*stageDesc, len(s.stages)+1)
	copy(stages, s.stages)
	stages[len(s.stages)] = sink.desc
	return RunnableGraph{stages: stages}
}

// WithOverflowStrategy returns a new Source using the given overflow strategy.
func (s Source[T]) WithOverflowStrategy(os OverflowStrategy) Source[T] {
	return s.withSourceConfig(func(c *StageConfig) { c.OverflowStrategy = os })
}

// WithTracer returns a new Source with the given tracer.
func (s Source[T]) WithTracer(t Tracer) Source[T] {
	return s.withSourceConfig(func(c *StageConfig) { c.Tracer = t })
}

// withSourceConfig returns a Source with the first stage's config modified by fn.
func (s Source[T]) withSourceConfig(fn func(*StageConfig)) Source[T] {
	if len(s.stages) == 0 {
		return s
	}
	newStages := make([]*stageDesc, len(s.stages))
	copy(newStages, s.stages)
	newDesc := *newStages[0]
	fn(&newDesc.config)
	prevMake := newStages[0].makeActor
	newDesc.makeActor = func(_ StageConfig) actor.Actor { return prevMake(newDesc.config) }
	newStages[0] = &newDesc
	return Source[T]{stages: newStages}
}

// From starts a LinearGraph fluent pipeline builder from the given source.
// It mirrors the Akka Streams naming convention and signals intent to assemble
// a linear pipeline rather than compose raw Source values.
//
//	handle, err := stream.From(stream.Of(1, 2, 3)).
//	    Via(stream.Map(double)).
//	    To(sink).
//	    Run(ctx, sys)
func From[T any](src Source[T]) *LinearGraph[T] {
	return &LinearGraph[T]{src: src}
}

// Via pipes Source[In] through Flow[In, Out], returning Source[Out].
// This free function is required for type-changing flows because Go methods
// cannot introduce additional type parameters.
func Via[In, Out any](src Source[In], flow Flow[In, Out]) Source[Out] {
	stages := make([]*stageDesc, len(src.stages)+1)
	copy(stages, src.stages)
	stages[len(src.stages)] = flow.desc
	return Source[Out]{stages: stages}
}

// Of creates a finite Source that emits the given values in order, then completes.
func Of[T any](values ...T) Source[T] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			anys := make([]any, len(values))
			for i, v := range values {
				anys[i] = v
			}
			return newPullSourceActor(func(n int64) ([]any, bool) {
				if len(anys) == 0 {
					return nil, false
				}
				take := min(int(n), len(anys))
				batch := anys[:take]
				anys = anys[take:]
				return batch, len(anys) > 0
			}, cfg)
		},
		config: config,
	}
	return Source[T]{stages: []*stageDesc{desc}}
}

// Range creates a Source that emits integers from start (inclusive) to end (exclusive).
func Range(start, end int64) Source[int64] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			cur := start
			return newPullSourceActor(func(n int64) ([]any, bool) {
				if cur >= end {
					return nil, false
				}
				take := n
				if cur+take > end {
					take = end - cur
				}
				batch := make([]any, take)
				for i := range batch {
					batch[i] = cur
					cur++
				}
				return batch, cur < end
			}, cfg)
		},
		config: config,
	}
	return Source[int64]{stages: []*stageDesc{desc}}
}

// FromChannel creates a Source backed by a Go channel.
// Elements are emitted as they arrive; the stream completes when ch is closed.
// Backpressure naturally limits how fast the channel goroutine advances.
func FromChannel[T any](ch <-chan T) Source[T] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newChanSourceActor(ch, cfg)
		},
		config: config,
	}
	return Source[T]{stages: []*stageDesc{desc}}
}

// FromActor creates a Source that pulls elements from a GoAkt actor by sending
// PullRequest messages and expecting PullResponse[T] replies.
// The target actor must implement the pull protocol (see PullRequest / PullResponse).
func FromActor[T any](pid *actor.PID) Source[T] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newActorSourceActor[T](pid, cfg)
		},
		config: config,
	}
	return Source[T]{stages: []*stageDesc{desc}}
}

// Tick creates a Source that emits the current time on a fixed interval.
// The stream runs indefinitely until stopped via StreamHandle.Stop.
func Tick(interval time.Duration) Source[time.Time] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newTickSourceActor(interval, cfg)
		},
		config: config,
	}
	return Source[time.Time]{stages: []*stageDesc{desc}}
}

// Merge combines multiple Sources into one, emitting elements as they arrive.
// The merged source completes only when all input sources have completed.
// Elements are emitted in arrival order (non-deterministic across sources).
func Merge[T any](sources ...Source[T]) Source[T] {
	config := defaultStageConfig()
	// Capture the stage lists of all sub-sources.
	subStages := make([][]*stageDesc, len(sources))
	for i, src := range sources {
		subStages[i] = src.stages
	}
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newMergeSourceActor[T](subStages, cfg)
		},
		config: config,
	}
	return Source[T]{stages: []*stageDesc{desc}}
}

// MergeLatest merges N same-typed sources into a stream of []T snapshots.
// Each upstream emission queues one snapshot containing the latest element
// seen on every input. The first snapshot is only emitted once every input
// has produced at least one element. Completes when every input has
// completed and every queued snapshot has been emitted.
func MergeLatest[T any](sources ...Source[T]) Source[[]T] {
	config := defaultStageConfig()
	subStages := make([][]*stageDesc, len(sources))
	for i, src := range sources {
		subStages[i] = src.stages
	}
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newMergeLatestSourceActor[T](subStages, cfg)
		},
		config: config,
	}
	return Source[[]T]{stages: []*stageDesc{desc}}
}

// MergeSequence merges N same-typed sources whose elements collectively form a
// contiguous range of sequence numbers starting from 0. extractSeq pulls the
// sequence number from each element. The output emits elements in ascending
// sequence order, buffering out-of-order elements internally.
//
// If all inputs complete while the buffer still holds elements whose smallest
// sequence number is not the next expected one, the stream fails with a
// missing-sequence error.
func MergeSequence[T any](extractSeq func(T) int64, sources ...Source[T]) Source[T] {
	config := defaultStageConfig()
	subStages := make([][]*stageDesc, len(sources))
	for i, src := range sources {
		subStages[i] = src.stages
	}
	erased := func(v any) int64 {
		t, ok := v.(T)
		if !ok {
			return -1
		}
		return extractSeq(t)
	}
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newMergeSequenceSourceActor[T](subStages, erased, cfg)
		},
		config: config,
	}
	return Source[T]{stages: []*stageDesc{desc}}
}

// MergePreferred merges N sources but always drains the slot at index
// preferred first when that slot has buffered elements. If the preferred
// slot is empty, it falls back to the lowest-indexed non-empty slot. This
// is useful when one input represents a higher-priority feed that should be
// drained ahead of background traffic.
//
// Panics if preferred is out of range. Returns an empty source when sources
// is empty.
func MergePreferred[T any](preferred int, sources ...Source[T]) Source[T] {
	if len(sources) == 0 {
		return Merge[T]()
	}
	if preferred < 0 || preferred >= len(sources) {
		panic(fmt.Sprintf("stream: MergePreferred: preferred index %d out of range [0,%d)", preferred, len(sources)))
	}
	pref := preferred
	selectSlot := func(bufs []queue) int {
		if !bufs[pref].empty() {
			return pref
		}
		for i := range bufs {
			if !bufs[i].empty() {
				return i
			}
		}
		return -1
	}
	return newWeightedMergeSource(sources, selectSlot)
}

// MergePrioritized merges N sources and selects the next slot to drain by
// weighted random choice: slot i is chosen with probability weights[i] /
// sum(weights[j] for j with non-empty buffer). Slots whose buffers are empty
// are skipped. Slots with weight 0 are only selected when they are the only
// slots with data.
//
// weights must have the same length as sources. Panics on length mismatch.
// Returns an empty source when sources is empty.
func MergePrioritized[T any](weights []int, sources ...Source[T]) Source[T] {
	if len(sources) == 0 {
		return Merge[T]()
	}
	if len(weights) != len(sources) {
		panic(fmt.Sprintf("stream: MergePrioritized: weights length %d != sources length %d", len(weights), len(sources)))
	}
	w := make([]int, len(weights))
	copy(w, weights)
	rng := mrand.New(mrand.NewPCG(mrand.Uint64(), mrand.Uint64())) // #nosec G404 -- non-security: weighted slot selection for stream merging
	selectSlot := func(bufs []queue) int {
		total := 0
		for i := range bufs {
			if !bufs[i].empty() {
				total += w[i]
			}
		}
		if total <= 0 {
			for i := range bufs {
				if !bufs[i].empty() {
					return i
				}
			}
			return -1
		}
		r := rng.IntN(total)
		for i := range bufs {
			if bufs[i].empty() {
				continue
			}
			r -= w[i]
			if r < 0 {
				return i
			}
		}
		return -1
	}
	return newWeightedMergeSource(sources, selectSlot)
}

// newWeightedMergeSource is the shared body of MergePreferred and
// MergePrioritized: it captures the sub-source stage lists and wires a
// weightedMergeSourceActor with the given selector strategy.
func newWeightedMergeSource[T any](sources []Source[T], selectSlot slotSelector) Source[T] {
	config := defaultStageConfig()
	subStages := make([][]*stageDesc, len(sources))
	for i, src := range sources {
		subStages[i] = src.stages
	}
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newWeightedMergeSourceActor[T](subStages, selectSlot, cfg)
		},
		config: config,
	}
	return Source[T]{stages: []*stageDesc{desc}}
}

// Concat creates a Source that consumes elements from each input source in order:
// it pulls from sources[i+1] only after sources[i] completes. Per-source ordering
// is preserved, and the concatenation completes when the last source completes.
//
// Unlike Merge (which interleaves), Concat materializes at most one sub-pipeline
// at a time, bounding the in-flight resource cost regardless of how many
// sources are concatenated.
func Concat[T any](sources ...Source[T]) Source[T] {
	config := defaultStageConfig()
	subStages := make([][]*stageDesc, len(sources))
	for i, src := range sources {
		subStages[i] = src.stages
	}
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newConcatSourceActor[T](subStages, cfg)
		},
		config: config,
	}
	return Source[T]{stages: []*stageDesc{desc}}
}

// Zip combines N same-typed sources into a single source whose elements are
// []T tuples holding one element from each input in source order. It emits one
// tuple per matched group and completes when any input completes with an empty
// buffer (no further tuples can be formed from that side).
//
// Inputs share the element type T. For two sources of different types, use
// Combine.
func Zip[T any](sources ...Source[T]) Source[[]T] {
	return ZipWith(func(s []T) []T {
		out := make([]T, len(s))
		copy(out, s)
		return out
	}, sources...)
}

// ZipWith combines N same-typed sources via the supplied combine function.
// combine receives a fresh slice of length N on every call, ordered to match
// the input sources. ZipWith completes when any input completes with an empty
// buffer.
//
// Inputs share the element type T. For two sources of different types, use
// Combine.
func ZipWith[T, V any](combine func([]T) V, sources ...Source[T]) Source[V] {
	config := defaultStageConfig()
	subStages := make([][]*stageDesc, len(sources))
	for i, src := range sources {
		subStages[i] = src.stages
	}
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newZipNSourceActor(subStages, combine, cfg)
		},
		config: config,
	}
	return Source[V]{stages: []*stageDesc{desc}}
}

// Combine creates a Source that pairs elements from two sources using a combine function.
// It emits one combined element per pair (zip semantics) and completes when
// either source is exhausted.
func Combine[T, U, V any](left Source[T], right Source[U], combine func(T, U) V) Source[V] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newCombineSourceActor(left.stages, right.stages, combine, cfg)
		},
		config: config,
	}
	return Source[V]{stages: []*stageDesc{desc}}
}

// Broadcast fans out a single Source to n independent branches. Each returned
// Source[T] represents one branch: all N branches receive every element emitted
// by src. A shared broadcastHubActor enforces backpressure by pulling from src
// only at the rate of the slowest branch.
//
// Each branch must be independently materialized by calling Run on its
// RunnableGraph. The upstream pipeline is spawned once the last branch
// materializes. Branches are decoupled: a fast branch is never blocked by a
// slow sibling beyond the natural demand window.
//
// If n < 1, Broadcast returns an empty slice and src is not consumed.
func Broadcast[T any](src Source[T], n int) []Source[T] {
	if n < 1 {
		return nil
	}
	shared := newSharedBroadcast[T](n, src.stages)
	sources := make([]Source[T], n)
	for i := range sources {
		slot := i
		config := defaultStageConfig()
		desc := &stageDesc{
			id:   newStageID(),
			kind: sourceKind,
			makeActor: func(cfg StageConfig) actor.Actor {
				return &broadcastSlotActor[T]{shared: shared, slot: slot, config: cfg}
			},
			config: config,
		}
		sources[i] = Source[T]{stages: []*stageDesc{desc}}
	}
	return sources
}

// Balance distributes elements from src across n independent branches using
// round-robin routing with backpressure. Each element is delivered to exactly
// one branch — the next branch with available demand. This differs from
// Broadcast (which sends every element to every branch) and is suited for
// parallelising work across N independent consumers.
//
// If n < 1, Balance returns an empty slice and src is not consumed.
func Balance[T any](src Source[T], n int) []Source[T] {
	if n < 1 {
		return nil
	}
	shared := newSharedBalance[T](n, src.stages)
	sources := make([]Source[T], n)
	for i := range sources {
		slot := i
		config := defaultStageConfig()
		desc := &stageDesc{
			id:   newStageID(),
			kind: sourceKind,
			makeActor: func(cfg StageConfig) actor.Actor {
				return &balanceSlotActor[T]{shared: shared, slot: slot, config: cfg}
			},
			config: config,
		}
		sources[i] = Source[T]{stages: []*stageDesc{desc}}
	}
	return sources
}

// Partition fans out a single Source to n independent branches, routing each
// element to exactly one branch as determined by partitionFn. Unlike Broadcast
// (which sends every element to every branch) and Balance (which round-robins),
// Partition lets the caller decide where each element goes.
//
// partitionFn must return an index in [0, n). Out-of-range results, as well as
// elements destined for branches that have already cancelled, are dropped
// silently.
//
// Each branch must be independently materialized by calling Run on its
// RunnableGraph. The upstream pipeline is spawned once the last branch
// materializes. Backpressure is conservative: the hub pulls from src only when
// every active branch has outstanding demand, ensuring the branch selected by
// partitionFn has capacity for any element it routes.
//
// If n < 1, Partition returns an empty slice and src is not consumed.
func Partition[T any](src Source[T], n int, partitionFn func(T) int) []Source[T] {
	if n < 1 {
		return nil
	}
	erased := func(v any) int {
		t, ok := v.(T)
		if !ok {
			return -1
		}
		return partitionFn(t)
	}
	shared := newSharedPartition[T](n, src.stages, erased)
	sources := make([]Source[T], n)
	for i := range sources {
		slot := i
		config := defaultStageConfig()
		desc := &stageDesc{
			id:   newStageID(),
			kind: sourceKind,
			makeActor: func(cfg StageConfig) actor.Actor {
				return &partitionSlotActor[T]{shared: shared, slot: slot, config: cfg}
			},
			config: config,
		}
		sources[i] = Source[T]{stages: []*stageDesc{desc}}
	}
	return sources
}

// Unzip splits a single source into two sources of potentially different
// element types. unzipFn receives each input element and returns the (left,
// right) parts; the returned sources emit the left and right parts
// respectively.
//
// Both branches must be independently materialized, the same way as Broadcast.
// The upstream pipeline is shared — every input element is delivered to both
// branches with backpressure paced by the slowest branch.
func Unzip[T, A, B any](src Source[T], unzipFn func(T) (A, B)) (Source[A], Source[B]) {
	branches := Broadcast(src, 2)
	left := Via(branches[0], Map(func(v T) A { a, _ := unzipFn(v); return a }))
	right := Via(branches[1], Map(func(v T) B { _, b := unzipFn(v); return b }))
	return left, right
}

// FromConn creates a Source that reads []byte frames from a net.Conn.
// Each call to Read produces one element; demand controls how many reads
// are batched per upstream request. The source completes when Read returns
// io.EOF and errors on any other read error.
func FromConn(conn net.Conn, bufSize int) Source[[]byte] {
	if bufSize <= 0 {
		bufSize = 4096
	}
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newConnSourceActor(conn, bufSize, cfg)
		},
		config: config,
	}
	return Source[[]byte]{stages: []*stageDesc{desc}}
}

// Unfold creates a Source from a seed value and a step function.
// step receives the current seed and returns (nextSeed, element, hasMore).
// The source terminates when hasMore is false.
func Unfold[S, T any](seed S, step func(S) (S, T, bool)) Source[T] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			cur := seed
			return newPullSourceActor(func(n int64) ([]any, bool) {
				batch := make([]any, 0, n)
				for range n {
					next, elem, more := step(cur)
					cur = next
					batch = append(batch, elem)
					if !more {
						return batch, false
					}
				}
				return batch, true
			}, cfg)
		},
		config: config,
	}
	return Source[T]{stages: []*stageDesc{desc}}
}
