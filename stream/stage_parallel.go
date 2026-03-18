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
	"context"

	"github.com/tochemey/goakt/v4/actor"
)

// parallelResult is sent back to the parallelMapActor by a worker goroutine.
type parallelResult struct {
	seqNo uint64
	value any
	err   error
}

// seqHeap is a min-heap of parallelResult ordered by seqNo for resequencing.
type seqHeap []parallelResult

func (h seqHeap) Len() int           { return len(h) }
func (h seqHeap) Less(i, j int) bool { return h[i].seqNo < h[j].seqNo }
func (h seqHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *seqHeap) Push(x any)        { *h = append(*h, x.(parallelResult)) }
func (h *seqHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

type parallelMapActor[In, Out any] struct {
	workers      int
	fn           func(In) Out
	ordered      bool
	downstream   *actor.PID
	upstream     *actor.PID
	subID        string
	selfPID      *actor.PID
	inFlight     int64
	inputSeqNo   uint64 // seq assigned to each incoming element
	outSeqNo     uint64 // seq assigned to each outgoing element (unordered)
	nextEmit     uint64 // next inputSeqNo to emit (ordered)
	pending      seqHeap
	upstreamDone bool
	sem          chan struct{} // worker slot limiter
	config       StageConfig
}

func newParallelMapActor[In, Out any](n int, fn func(In) Out, ordered bool, cfg StageConfig) *parallelMapActor[In, Out] {
	a := &parallelMapActor[In, Out]{
		workers: n,
		fn:      fn,
		ordered: ordered,
		sem:     make(chan struct{}, n),
		pending: seqHeap{},
		config:  cfg,
	}
	heap.Init(&a.pending)
	return a
}

func (a *parallelMapActor[In, Out]) PreStart(_ *actor.Context) error { return nil }

func (a *parallelMapActor[In, Out]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.upstream = msg.upstream
		a.downstream = msg.downstream
		a.subID = msg.subID
		a.selfPID = rctx.Self()
		rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: int64(a.workers)})

	case *streamElement:
		a.inFlight++
		a.inputSeqNo++
		seqNo := a.inputSeqNo
		value := msg.value.(In)
		selfPID := a.selfPID
		fn := a.fn
		a.sem <- struct{}{}
		go func() {
			defer func() { <-a.sem }()
			out := fn(value)
			_ = actor.Tell(context.Background(), selfPID, &parallelResult{
				seqNo: seqNo,
				value: out,
			})
		}()

	case *parallelResult:
		a.inFlight--
		if msg.err != nil {
			rctx.Tell(a.downstream, &streamError{subID: a.subID, err: msg.err})
			rctx.Shutdown()
			return
		}
		if a.ordered {
			heap.Push(&a.pending, *msg)
			a.flushOrdered(rctx)
		} else {
			a.outSeqNo++
			rctx.Tell(a.downstream, &streamElement{subID: a.subID, value: msg.value, seqNo: a.outSeqNo})
		}
		if !a.upstreamDone {
			rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: 1})
		}
		if a.upstreamDone && a.inFlight == 0 {
			if a.ordered {
				a.flushOrdered(rctx)
			}
			rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
			rctx.Shutdown()
		}

	case *streamComplete:
		a.upstreamDone = true
		if a.inFlight == 0 {
			if a.ordered {
				a.flushOrdered(rctx)
			}
			rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
			rctx.Shutdown()
		}

	case *streamError:
		rctx.Tell(a.downstream, msg)
		rctx.Shutdown()

	case *streamCancel:
		rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

func (a *parallelMapActor[In, Out]) flushOrdered(rctx *actor.ReceiveContext) {
	for len(a.pending) > 0 {
		top := a.pending[0]
		if top.seqNo != a.nextEmit+1 {
			break
		}
		heap.Pop(&a.pending)
		a.nextEmit++
		a.outSeqNo++
		rctx.Tell(a.downstream, &streamElement{subID: a.subID, value: top.value, seqNo: a.outSeqNo})
	}
}

func (a *parallelMapActor[In, Out]) PostStop(_ *actor.Context) error { return nil }
