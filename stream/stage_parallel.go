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
	"fmt"

	"github.com/tochemey/goakt/v4/actor"
)

// workerTask is sent by parallelMapActor to a worker func actor carrying one element to transform.
type workerTask struct {
	seqNo   uint64
	value   any
	replyTo *actor.PID
}

// parallelResult is sent back to the parallelMapActor by a worker func actor.
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

// parallelMapActor fans out element processing across a pool of worker func actors.
// Each incoming streamElement is dispatched round-robin to a pre-spawned func actor.
// Workers run the user's fn with panic recovery and reply with parallelResult messages.
// Concurrency is bounded by demand control: the actor requests exactly `workers` elements
// initially and one more per completed result, so at most `workers` tasks are in flight.
type parallelMapActor[In, Out any] struct {
	workersCount int
	fn           func(In) Out
	ordered      bool
	downstream   *actor.PID
	upstream     *actor.PID
	subID        string
	self         *actor.PID
	inFlight     int64
	inputSeqNo   uint64 // seq assigned to each incoming element
	outSeqNo     uint64 // seq assigned to each outgoing element (unordered)
	nextEmit     uint64 // next inputSeqNo to emit (ordered)
	pending      seqHeap
	upstreamDone bool
	workers      []*actor.PID // pre-spawned func actor workers
	nextWorker   int          // round-robin index for worker assignment
	config       StageConfig
}

func newParallelMapActor[In, Out any](n int, fn func(In) Out, ordered bool, cfg StageConfig) *parallelMapActor[In, Out] {
	a := &parallelMapActor[In, Out]{
		workersCount: n,
		fn:           fn,
		ordered:      ordered,
		workers:      make([]*actor.PID, 0, n),
		pending:      seqHeap{},
		config:       cfg,
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
		a.self = rctx.Self()
		// Spawn worker func actors. Each handles one workerTask at a time, running fn
		// with panic recovery and replying with a parallelResult to the parent.
		fn := a.fn
		selfPID := a.self
		sys := rctx.ActorSystem()
		ctx := rctx.Context()
		for i := 0; i < a.workersCount; i++ {
			pid, err := sys.SpawnFromFunc(ctx, func(_ context.Context, rawMsg any) error {
				task, ok := rawMsg.(*workerTask)
				if !ok {
					return nil
				}
				var result parallelResult
				result.seqNo = task.seqNo
				func() {
					defer func() {
						if r := recover(); r != nil {
							if e, ok2 := r.(error); ok2 {
								result.err = e
							} else {
								result.err = fmt.Errorf("stream: worker panic: %v", r)
							}
						}
					}()
					result.value = fn(task.value.(In))
				}()
				return actor.Tell(context.Background(), selfPID, &result)
			})
			if err != nil {
				rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
				rctx.Tell(a.downstream, &streamError{
					subID: a.subID,
					err:   fmt.Errorf("stream: failed to spawn parallel worker: %w", err),
				})
				rctx.Shutdown()
				return
			}
			a.workers = append(a.workers, pid)
		}
		rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: int64(a.workersCount)})

	case *streamElement:
		value, ok := msg.value.(In)
		if !ok {
			rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
			rctx.Tell(a.downstream, &streamError{
				subID: a.subID,
				err:   fmt.Errorf("stream: type mismatch in parallel stage: expected %T, got %T", *new(In), msg.value),
			})
			rctx.Shutdown()
			return
		}
		a.inFlight++
		a.inputSeqNo++
		seqNo := a.inputSeqNo
		// Dispatch to the next worker via round-robin. Demand control guarantees that
		// at most `workers` tasks are in flight, so no worker accumulates a backlog.
		workerPID := a.workers[a.nextWorker]
		a.nextWorker = (a.nextWorker + 1) % a.workersCount
		rctx.Tell(workerPID, &workerTask{seqNo: seqNo, value: value, replyTo: a.self})

	case *parallelResult:
		a.inFlight--
		if msg.err != nil {
			rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
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

// PostStop shuts down all worker func actors when the stage terminates.
func (a *parallelMapActor[In, Out]) PostStop(_ *actor.Context) error {
	for _, workerPID := range a.workers {
		if workerPID != nil && workerPID.IsRunning() {
			_ = workerPID.Shutdown(context.Background())
		}
	}
	return nil
}
