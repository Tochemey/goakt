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
	"container/heap"
	"fmt"

	"github.com/tochemey/goakt/v4/actor"
)

// mergeSeqEntry pairs a sequence number with its element value for heap ordering.
type mergeSeqEntry struct {
	seq   int64
	value any
}

// mergeSeqHeap is a min-heap of mergeSeqEntry ordered by seq.
type mergeSeqHeap []mergeSeqEntry

func (h mergeSeqHeap) Len() int           { return len(h) }
func (h mergeSeqHeap) Less(i, j int) bool { return h[i].seq < h[j].seq }
func (h mergeSeqHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *mergeSeqHeap) Push(x any) {
	*h = append(*h, x.(mergeSeqEntry))
}

func (h *mergeSeqHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// mergeSequenceSourceActor backs MergeSequence. It buffers elements from each
// input in a min-heap keyed by their extracted sequence number, then emits in
// strict ascending sequence order. The next-expected sequence starts at 0 and
// increments on every emission.
//
// Inputs must collectively produce a contiguous range of sequence numbers
// starting from 0. If every input completes while the heap still holds
// elements whose smallest seq is not the expected one, the actor emits a
// streamError describing the missing sequence number.
type mergeSequenceSourceActor[T any] struct {
	subStages  [][]*stage
	extractFn  func(any) int64
	system     actor.ActorSystem
	downstream *actor.PID
	subID      string
	seqNo      uint64
	pending    mergeSeqHeap
	expected   int64
	done       []bool
	doneCount  int
	demand     int64
	metrics    *stageMetrics
	config     StageConfig
}

// newMergeSequenceSourceActor creates an actor that merges N sub-source
// pipelines into a single ordered output by extracted sequence number.
func newMergeSequenceSourceActor[T any](subStages [][]*stage, extractFn func(any) int64, config StageConfig) *mergeSequenceSourceActor[T] {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &mergeSequenceSourceActor[T]{
		subStages: subStages,
		extractFn: extractFn,
		system:    config.System,
		metrics:   m,
		config:    config,
	}
}

func (a *mergeSequenceSourceActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, mergeSubValue, mergeSubDone, and streamCancel.
func (a *mergeSequenceSourceActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
		if len(a.subStages) == 0 {
			rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
			rctx.Shutdown()
			return
		}
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
		heap.Push(&a.pending, mergeSeqEntry{
			seq:   a.extractFn(msg.value),
			value: msg.value,
		})
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

// tryEmit drains contiguous elements from the heap while demand remains, then
// terminates with completion or a missing-sequence error once every input is
// done.
func (a *mergeSequenceSourceActor[T]) tryEmit(rctx *actor.ReceiveContext) {
	for a.demand > 0 && a.pending.Len() > 0 && a.pending[0].seq == a.expected {
		e := heap.Pop(&a.pending).(mergeSeqEntry)
		a.seqNo++
		rctx.Tell(a.downstream, &streamElement{
			subID: a.subID,
			value: e.value,
			seqNo: a.seqNo,
		})
		a.expected++
		a.demand--
		a.metrics.elementsOut.Add(1)
	}

	if a.doneCount == len(a.subStages) {
		if a.pending.Len() == 0 {
			rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
			rctx.Shutdown()
			return
		}
		// All inputs are done but the next expected sequence number was never
		// produced — the upstream contiguity contract was violated.
		if a.demand > 0 {
			rctx.Tell(a.downstream, &streamError{
				subID: a.subID,
				err:   fmt.Errorf("stream: MergeSequence: missing sequence number %d", a.expected),
			})
			rctx.Shutdown()
		}
	}
}

func (a *mergeSequenceSourceActor[T]) PostStop(_ *actor.Context) error { return nil }
