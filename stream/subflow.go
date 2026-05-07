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
	"github.com/tochemey/goakt/v4/actor"
)

// defaultSubstreamPerKeyBuffer is the default per-key in-flight cap.
// It matches defaultBufferSize so a single substream behaves like a regular
// flow stage; users tune it via SubFlow.WithSubstreamBuffer.
const defaultSubstreamPerKeyBuffer = defaultBufferSize

// splitMode selects how the splitter actor derives a substream key from each
// upstream element. GroupBy uses keyFn(value); SplitWhen / SplitAfter ignore
// the key function and instead route to a monotonically-increasing counter,
// rotating to the next counter value on a predicate-true element.
type splitMode int8

const (
	splitModeGroupBy splitMode = iota
	splitModeWhen
	splitModeAfter
)

// SubFlow is a lazy description of a "stream of substreams" — elements of an
// upstream Source partitioned into independent per-substream pipelines.
//
// SubFlow is produced by three constructors with different routing semantics:
//
//   - GroupBy(src, max, keyFn) — partition by a key function; one substream
//     per distinct key, materialised lazily.
//   - SplitWhen(src, pred) — consecutive substreams; a new substream begins
//     when pred(v) is true. The triggering element is placed at the START of
//     the new substream.
//   - SplitAfter(src, pred) — consecutive substreams; the current substream
//     ends after a pred(v)-true element. The triggering element is placed at
//     the END of the current substream.
//
// A SubFlow on its own is not runnable; it must be terminated by
// MergeSubstreams to obtain a Source[T] (which can then be wired to a Sink).
// Per-substream transformations are appended with SubFlowVia, and apply
// independently to each substream's pipeline — so e.g. a Scan inside the
// substream chain carries its own accumulator per substream.
//
// Backpressure: each substream has a per-substream in-flight cap (see
// WithSubstreamBuffer). When a substream is at capacity new elements for
// it are handled by the configured OverflowStrategy — by default dropped
// (DropTail) — and reported via the OnDrop hook / metrics. Substream
// pipelines acknowledge consumption to the splitter in batches, keeping
// per-substream memory bounded without per-element protocol overhead.
//
// Errors: when a per-substream pipeline fails the SubstreamErrorStrategy
// decides how to react. The default (SubstreamFailAll) terminates the
// whole stream — matching the FailFast contract of linear flows.
//
// T is the type currently emitted by each substream's pipeline (changes as
// flows are appended via SubFlowVia). For GroupBy the key function is
// captured against the original element type and type-erased so the
// routing path stays valid as the substream output type evolves; for
// SplitWhen / SplitAfter the predicate plays the same role.
type SubFlow[K comparable, T any] struct {
	upstream      []*stageDesc
	sub           []*stageDesc
	mode          splitMode
	keyFn         func(any) K    // used only when mode == splitModeGroupBy
	splitPred     func(any) bool // used only when mode == splitModeWhen / splitModeAfter
	maxSubs       int
	perKeyBuffer  int
	overflow      OverflowStrategy
	errorStrategy SubstreamErrorStrategy
}

// WithSubstreamBuffer overrides the per-substream in-flight cap. Pass
// perKeyBuffer ≥ 1; non-positive values fall back to the default. The
// OverflowStrategy controls behaviour when a substream reaches the cap:
// DropTail (default), DropHead, BackpressureSource — all currently
// implemented as drop-newest — or FailSource which terminates the stream
// with ErrSubstreamOverflow.
func (sf SubFlow[K, T]) WithSubstreamBuffer(perKeyBuffer int, strategy OverflowStrategy) SubFlow[K, T] {
	sf.perKeyBuffer = perKeyBuffer
	sf.overflow = strategy
	return sf
}

// WithErrorStrategy overrides the SubstreamErrorStrategy applied when a
// per-substream pipeline fails. Default is SubstreamFailAll.
func (sf SubFlow[K, T]) WithErrorStrategy(strategy SubstreamErrorStrategy) SubFlow[K, T] {
	sf.errorStrategy = strategy
	return sf
}

// GroupBy partitions src by keyFn, producing a SubFlow[K, T] in which each
// distinct key materializes its own per-substream pipeline. maxSubstreams caps
// the number of concurrently-open substreams; exceeding it terminates the
// stream with ErrTooManySubstreams. Pass 0 for an unbounded cap.
func GroupBy[T any, K comparable](src Source[T], maxSubstreams int, keyFn func(T) K) SubFlow[K, T] {
	upstream := make([]*stageDesc, len(src.stages))
	copy(upstream, src.stages)

	erasedKeyFn := func(rawValue any) K {
		typedValue, _ := rawValue.(T)
		return keyFn(typedValue)
	}

	return SubFlow[K, T]{
		upstream:     upstream,
		mode:         splitModeGroupBy,
		keyFn:        erasedKeyFn,
		maxSubs:      maxSubstreams,
		perKeyBuffer: defaultSubstreamPerKeyBuffer,
		overflow:     DropTail,
	}
}

// SplitWhen partitions src into a sequence of substreams. A new substream
// begins as soon as predicate(v) is true; the triggering element becomes the
// FIRST element of the new substream. The first element of the source always
// lands in substream 0 regardless of the predicate's value (there is nothing
// to split off from yet).
//
// The synthetic substream key is a monotonically-increasing int — useful for
// metrics / tracing but otherwise opaque.
func SplitWhen[T any](src Source[T], predicate func(T) bool) SubFlow[int, T] {
	return newSplitSubFlow(src, predicate, splitModeWhen)
}

// SplitAfter partitions src into a sequence of substreams. The current
// substream ends AFTER a predicate(v)-true element; that element is the LAST
// element of the substream it terminates, and the next element starts a new
// substream.
func SplitAfter[T any](src Source[T], predicate func(T) bool) SubFlow[int, T] {
	return newSplitSubFlow(src, predicate, splitModeAfter)
}

// newSplitSubFlow is the shared constructor for SplitWhen / SplitAfter.
func newSplitSubFlow[T any](src Source[T], predicate func(T) bool, mode splitMode) SubFlow[int, T] {
	upstream := make([]*stageDesc, len(src.stages))
	copy(upstream, src.stages)

	erasedPredicate := func(rawValue any) bool {
		typedValue, _ := rawValue.(T)
		return predicate(typedValue)
	}

	return SubFlow[int, T]{
		upstream:     upstream,
		mode:         mode,
		splitPred:    erasedPredicate,
		perKeyBuffer: defaultSubstreamPerKeyBuffer,
		overflow:     DropTail,
	}
}

// SubFlowVia appends flow to the per-substream pipeline of sf. The flow is
// applied independently within each substream, so e.g. a Scan stage carries
// its own accumulator per substream. Mirrors the package-level Via for Source.
func SubFlowVia[K comparable, In, Out any](sf SubFlow[K, In], flow Flow[In, Out]) SubFlow[K, Out] {
	sub := make([]*stageDesc, len(sf.sub)+1)
	copy(sub, sf.sub)
	sub[len(sf.sub)] = flow.desc

	return SubFlow[K, Out]{
		upstream:      sf.upstream,
		sub:           sub,
		mode:          sf.mode,
		keyFn:         sf.keyFn,
		splitPred:     sf.splitPred,
		maxSubs:       sf.maxSubs,
		perKeyBuffer:  sf.perKeyBuffer,
		overflow:      sf.overflow,
		errorStrategy: sf.errorStrategy,
	}
}

// MergeSubstreams collapses a SubFlow back into a flat Source by interleaving
// elements from every per-substream pipeline as they become available.
// Order across substreams is non-deterministic; order within a substream is
// preserved.
func MergeSubstreams[K comparable, T any](sf SubFlow[K, T]) Source[T] {
	upstream := sf.upstream
	sub := sf.sub
	mode := sf.mode
	keyFn := sf.keyFn
	splitPred := sf.splitPred
	maxSubs := sf.maxSubs
	perKeyBuffer := sf.perKeyBuffer
	overflow := sf.overflow
	errorStrategy := sf.errorStrategy

	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newSubFlowSourceActor(upstream, sub, mode, keyFn, splitPred, maxSubs, perKeyBuffer, overflow, errorStrategy, cfg)
		},
		config: config,
	}
	return Source[T]{stages: []*stageDesc{desc}}
}
