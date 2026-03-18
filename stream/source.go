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
	"net"
	"time"

	"github.com/tochemey/goakt/v4/actor"
)

// Source[T] is a lazy description of a stream origin that produces elements of type T.
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
				take := int(n)
				if take > len(anys) {
					take = len(anys)
				}
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
			return newChanSourceActor[T](ch, cfg)
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
				for i := int64(0); i < n; i++ {
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
