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

// concatSourceActor backs Concat. It consumes sub-source pipelines sequentially:
// only after the current sub-source completes (mergeSubDone) does it spawn the
// next one. Elements from each sub-source are forwarded in order, preserving
// per-source ordering across the boundary between sub-sources.
//
// Unlike mergeSourceActor (which spawns all sub-pipelines up front and
// interleaves their elements), concatSourceActor materializes only one
// sub-pipeline at a time. This bounds the in-flight resource cost at one
// sub-pipeline regardless of how many sources are concatenated.
type concatSourceActor[T any] struct {
	subStages  [][]*stageDesc
	system     actor.ActorSystem
	downstream *actor.PID
	subID      string
	seqNo      uint64
	buf        queue
	demand     int64
	current    int  // index of the currently active sub-source; -1 before the first spawn
	done       bool // every sub-source has completed
	metrics    *stageMetrics
	config     StageConfig
}

// newConcatSourceActor creates a concatSourceActor for the given sub-source
// stage lists, consumed in the order provided.
func newConcatSourceActor[T any](subStages [][]*stageDesc, config StageConfig) *concatSourceActor[T] {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &concatSourceActor[T]{
		subStages: subStages,
		system:    config.System,
		metrics:   m,
		config:    config,
		current:   -1,
	}
}

func (a *concatSourceActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, mergeSubValue, mergeSubDone, and streamCancel.
func (a *concatSourceActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
		if len(a.subStages) == 0 {
			rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
			rctx.Shutdown()
			return
		}
		a.spawnNext(rctx)

	case *streamRequest:
		a.demand += msg.n
		a.tryFlush(rctx)

	case *mergeSubValue:
		// Only the currently active sub-source can produce values: its
		// mergeSubDone is delivered after all of its values (FIFO per sender),
		// and the next sub-source is only spawned after that done arrives.
		a.metrics.elementsIn.Add(1)
		a.buf.push(msg.value)
		a.tryFlush(rctx)

	case *mergeSubDone:
		if a.current+1 < len(a.subStages) {
			a.spawnNext(rctx)
			return
		}
		a.done = true
		a.tryFlush(rctx)

	case *streamCancel:
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// spawnNext materializes the next sub-source pipeline with an internal sink
// that forwards elements and completion back to this actor.
func (a *concatSourceActor[T]) spawnNext(rctx *actor.ReceiveContext) {
	a.current++
	sub := a.subStages[a.current]
	sink := makeMergeSinkDesc(rctx.Self(), a.current)
	all := make([]*stageDesc, len(sub)+1)
	copy(all, sub)
	all[len(sub)] = sink
	spawnSubPipeline(rctx.Context(), a.system, all)
}

// tryFlush forwards buffered elements while demand remains, then completes
// once every sub-source has produced its mergeSubDone and the buffer is empty.
func (a *concatSourceActor[T]) tryFlush(rctx *actor.ReceiveContext) {
	for a.demand > 0 && !a.buf.empty() {
		a.seqNo++
		rctx.Tell(a.downstream, &streamElement{
			subID: a.subID,
			value: a.buf.pop(),
			seqNo: a.seqNo,
		})
		a.demand--
	}

	if a.done && a.buf.empty() {
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()
	}
}

func (a *concatSourceActor[T]) PostStop(_ *actor.Context) error { return nil }
