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

// zipNSourceActor backs ZipN and ZipWithN. It buffers elements per input slot
// and emits a combined element once each slot has at least one element pending.
// The zip completes when any input completes and its slot buffer becomes empty
// (no further pairs can be formed from that side).
//
// V is the output element type produced by the combine function from a slice of
// length len(subStages) containing one element per slot in slot order.
type zipNSourceActor[T, V any] struct {
	subStages  [][]*stageDesc
	combineFn  func([]T) V
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

// newZipNSourceActor creates a zipNSourceActor over the given sub-source stage
// lists. combineFn is invoked once per emitted tuple with a slice holding one
// element from each slot.
func newZipNSourceActor[T, V any](subStages [][]*stageDesc, combineFn func([]T) V, config StageConfig) *zipNSourceActor[T, V] {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &zipNSourceActor[T, V]{
		subStages: subStages,
		combineFn: combineFn,
		system:    config.System,
		metrics:   m,
		config:    config,
	}
}

func (a *zipNSourceActor[T, V]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, mergeSubValue, mergeSubDone, and streamCancel.
func (a *zipNSourceActor[T, V]) Receive(rctx *actor.ReceiveContext) {
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
			all := make([]*stageDesc, len(sub)+1)
			copy(all, sub)
			all[len(sub)] = sink
			spawnSubPipeline(ctx, a.system, all)
		}

	case *streamRequest:
		a.demand += msg.n
		a.tryEmit(rctx)

	case *mergeSubValue:
		a.metrics.elementsIn.Add(1)
		a.bufs[msg.slot].push(msg.value)
		a.tryEmit(rctx)

	case *mergeSubDone:
		a.done[msg.slot] = true
		a.tryEmit(rctx)

	case *streamCancel:
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// tryEmit emits combined tuples while demand remains and every slot has at
// least one buffered element. Completes once any slot reports done with an
// empty buffer (no further tuples are possible from that side).
func (a *zipNSourceActor[T, V]) tryEmit(rctx *actor.ReceiveContext) {
	for a.demand > 0 && a.allReady() {
		tup := make([]T, len(a.bufs))
		for i := range a.bufs {
			v, ok := a.bufs[i].pop().(T)
			if !ok {
				rctx.Tell(a.downstream, &streamError{
					subID: a.subID,
					err:   fmt.Errorf("stream: ZipN type mismatch on slot %d", i),
				})
				rctx.Shutdown()
				return
			}
			tup[i] = v
		}
		a.seqNo++
		rctx.Tell(a.downstream, &streamElement{
			subID: a.subID,
			value: a.combineFn(tup),
			seqNo: a.seqNo,
		})
		a.demand--
		a.metrics.elementsOut.Add(1)
	}

	for i := range a.bufs {
		if a.done[i] && a.bufs[i].empty() {
			rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
			rctx.Shutdown()
			return
		}
	}
}

// allReady reports whether every slot has at least one buffered element.
func (a *zipNSourceActor[T, V]) allReady() bool {
	for i := range a.bufs {
		if a.bufs[i].empty() {
			return false
		}
	}
	return true
}

func (a *zipNSourceActor[T, V]) PostStop(_ *actor.Context) error { return nil }
