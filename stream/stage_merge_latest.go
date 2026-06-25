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

	"github.com/tochemey/goakt/v4/actor"
)

// mergeLatestSourceActor backs MergeLatest. It maintains a per-slot cache of
// the most recent value seen on each input. Each upstream emission queues one
// snapshot for emission, but the first snapshot can only fire once every slot
// has produced at least one element. Completes when every input has signalled
// done and every queued snapshot has been emitted.
type mergeLatestSourceActor[T any] struct {
	subStages        [][]*stage
	system           actor.ActorSystem
	downstream       *actor.PID
	subID            string
	seqNo            uint64
	cache            []any  // last value per slot; element types must be T
	seen             []bool // whether each slot has emitted at least once
	seenCount        int
	done             []bool
	doneCount        int
	pendingSnapshots int64 // snapshots queued but not yet emitted downstream
	demand           int64
	metrics          *stageMetrics
	config           StageConfig
}

// newMergeLatestSourceActor creates an actor that emits a snapshot of the
// latest value on each slot whenever any slot produces a new element.
func newMergeLatestSourceActor[T any](subStages [][]*stage, config StageConfig) *mergeLatestSourceActor[T] {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &mergeLatestSourceActor[T]{
		subStages: subStages,
		system:    config.System,
		metrics:   m,
		config:    config,
	}
}

func (a *mergeLatestSourceActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, mergeSubValue, mergeSubDone, and streamCancel.
func (a *mergeLatestSourceActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
		if len(a.subStages) == 0 {
			rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
			rctx.Shutdown()
			return
		}
		a.cache = make([]any, len(a.subStages))
		a.seen = make([]bool, len(a.subStages))
		a.done = make([]bool, len(a.subStages))
		self := rctx.Self()
		ctx := rctx.Context()
		for i, sub := range a.subStages {
			sink := makeMergeSinkDesc(self, i)
			all := make([]*stage, len(sub)+1)
			copy(all, sub)
			all[len(sub)] = sink
			spawnSubPipeline(ctx, a.system, all)
		}

	case *streamRequest:
		a.demand += msg.n
		a.tryEmit(rctx)

	case *mergeSubValue:
		a.metrics.elementsIn.Add(1)
		a.cache[msg.slot] = msg.value
		if !a.seen[msg.slot] {
			a.seen[msg.slot] = true
			a.seenCount++
		}
		// Only queue snapshots once every slot has emitted at least one value.
		if a.seenCount == len(a.subStages) {
			a.pendingSnapshots++
		}
		a.tryEmit(rctx)

	case *mergeSubDone:
		if !a.done[msg.slot] {
			a.done[msg.slot] = true
			a.doneCount++
		}
		a.tryEmit(rctx)

	case *streamCancel:
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// tryEmit emits one snapshot per available demand unit while snapshots are
// queued, then completes once every input is done and the queue is drained.
func (a *mergeLatestSourceActor[T]) tryEmit(rctx *actor.ReceiveContext) {
	for a.demand > 0 && a.pendingSnapshots > 0 {
		snap := make([]T, len(a.cache))
		for i, v := range a.cache {
			t, ok := v.(T)
			if !ok {
				rctx.Tell(a.downstream, &streamError{
					subID: a.subID,
					err:   fmt.Errorf("stream: MergeLatest type mismatch on slot %d", i),
				})
				rctx.Shutdown()
				return
			}
			snap[i] = t
		}
		a.seqNo++
		rctx.Tell(a.downstream, &streamElement{
			subID: a.subID,
			value: snap,
			seqNo: a.seqNo,
		})
		a.demand--
		a.pendingSnapshots--
		a.metrics.elementsOut.Add(1)
	}

	if a.doneCount == len(a.subStages) && a.pendingSnapshots == 0 {
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()
	}
}

func (a *mergeLatestSourceActor[T]) PostStop(_ *actor.Context) error { return nil }
