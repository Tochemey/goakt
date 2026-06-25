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

// slotSelector chooses which non-empty slot to drain on the next emission.
// It must return -1 when every slot is empty.
type slotSelector func(bufs []queue) int

// weightedMergeSourceActor backs MergePreferred and MergePrioritized. Unlike
// mergeSourceActor (which interleaves all slots into a single FIFO buffer),
// this actor keeps a per-slot buffer so a selectSlot strategy can decide
// which slot to drain on each emission. Completion semantics match plain
// merge: complete only after every slot is done and every per-slot buffer
// is drained.
type weightedMergeSourceActor[T any] struct {
	subStages  [][]*stage
	selectSlot slotSelector
	system     actor.ActorSystem
	downstream *actor.PID
	subID      string
	seqNo      uint64
	bufs       []queue
	done       []bool
	demand     int64
	metrics    *stageMetrics
	config     StageConfig
}

// newWeightedMergeSourceActor creates an actor that merges N sub-source
// pipelines using selectSlot to pick which slot to drain on each emission.
func newWeightedMergeSourceActor[T any](subStages [][]*stage, selectSlot slotSelector, config StageConfig) *weightedMergeSourceActor[T] {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &weightedMergeSourceActor[T]{
		subStages:  subStages,
		selectSlot: selectSlot,
		system:     config.System,
		metrics:    m,
		config:     config,
	}
}

func (a *weightedMergeSourceActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, mergeSubValue, mergeSubDone, and streamCancel.
func (a *weightedMergeSourceActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
		if len(a.subStages) == 0 {
			rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
			rctx.Shutdown()
			return
		}
		a.bufs = make([]queue, len(a.subStages))
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
		a.tryFlush(rctx)

	case *mergeSubValue:
		a.metrics.elementsIn.Add(1)
		a.bufs[msg.slot].push(msg.value)
		a.tryFlush(rctx)

	case *mergeSubDone:
		a.done[msg.slot] = true
		a.tryFlush(rctx)

	case *streamCancel:
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// tryFlush emits one element per call to selectSlot until either demand is
// exhausted or every slot is empty. Completes once every slot is done and
// every buffer is drained.
func (a *weightedMergeSourceActor[T]) tryFlush(rctx *actor.ReceiveContext) {
	for a.demand > 0 {
		slot := a.selectSlot(a.bufs)
		if slot < 0 {
			break
		}
		a.seqNo++
		rctx.Tell(a.downstream, &streamElement{
			subID: a.subID,
			value: a.bufs[slot].pop(),
			seqNo: a.seqNo,
		})
		a.demand--
	}

	if a.allDoneAndEmpty() {
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()
	}
}

// allDoneAndEmpty reports whether every sub-source has signalled done and
// every per-slot buffer has been fully drained.
func (a *weightedMergeSourceActor[T]) allDoneAndEmpty() bool {
	for i := range a.bufs {
		if !a.done[i] || !a.bufs[i].empty() {
			return false
		}
	}
	return true
}

func (a *weightedMergeSourceActor[T]) PostStop(_ *actor.Context) error { return nil }
