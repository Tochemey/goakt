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

// flatMapStreamActor backs FlatMapConcat (breadth=1) and FlatMapMerge
// (breadth>1). For each upstream element it calls the user-supplied
// fn(In) Source[Out], materialises the returned source as a sub-pipeline
// terminated by subMergeSinkActor, and forwards the sub-pipeline's elements
// to its own downstream.
//
// Demand is bounded by the breadth: total in-flight upstream credit plus
// active sub-pipelines never exceeds breadth, so backpressure flows
// naturally back to the original source. With breadth=1 only one sub-pipeline
// is active at a time, giving FlatMapConcat its strict ordering guarantee.
//
// Sub-pipeline output is buffered in outBuf and forwarded as downstream
// demand allows. Active sub-pipeline handles are tracked so the actor can
// abort them on cancellation or failure rather than leaking goroutines.
type flatMapStreamActor[In, Out any] struct {
	fn      func(In) Source[Out]
	breadth int

	upstream   *actor.PID
	downstream *actor.PID
	subID      string
	seqNo      uint64
	system     actor.ActorSystem

	outBuf           queue
	downstreamDemand int64

	upstreamCredit int64
	activeSubs     int
	upstreamDone   bool

	children   map[int]StreamHandle
	subCounter int

	failed    bool
	completed bool

	config  StageConfig
	metrics *stageMetrics
}

func newFlatMapStreamActor[In, Out any](breadth int, fn func(In) Source[Out], config StageConfig) *flatMapStreamActor[In, Out] {
	metrics := config.Metrics
	if metrics == nil {
		metrics = &stageMetrics{}
	}

	if breadth < 1 {
		breadth = 1
	}

	return &flatMapStreamActor[In, Out]{
		fn:       fn,
		breadth:  breadth,
		system:   config.System,
		children: make(map[int]StreamHandle),
		config:   config,
		metrics:  metrics,
	}
}

func (a *flatMapStreamActor[In, Out]) PreStart(_ *actor.Context) error { return nil }

func (a *flatMapStreamActor[In, Out]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.upstream = msg.upstream
		a.downstream = msg.downstream
		a.subID = msg.subID
		a.upstreamCredit = int64(a.breadth)
		rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: a.upstreamCredit})

	case *streamRequest:
		a.downstreamDemand += msg.n
		a.tryFlush(rctx)

	case *streamElement:
		a.metrics.elementsIn.Add(1)
		a.upstreamCredit--

		input, ok := msg.value.(In)
		if !ok {
			a.fail(rctx, fmt.Errorf("stream: FlatMap got unexpected type %T, want %T", msg.value, *new(In)))
			return
		}

		a.spawnInner(rctx, input)

	case *subOut:
		a.outBuf.push(msg.value)
		a.tryFlush(rctx)

	case *subDone:
		if key, ok := msg.key.(int); ok {
			delete(a.children, key)
		}

		a.activeSubs--
		a.maybeRequestMore(rctx)
		a.tryFlush(rctx)

	case *subErr:
		a.fail(rctx, msg.err)

	case *streamComplete:
		a.upstreamDone = true
		a.maybeComplete(rctx)

	case *streamError:
		a.fail(rctx, msg.err)

	case *streamCancel:
		a.cancelChildren()
		if a.upstream != nil {
			rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
		}

		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

func (a *flatMapStreamActor[In, Out]) PostStop(_ *actor.Context) error {
	a.cancelChildren()
	return nil
}

// spawnInner materialises fn(input) as a sub-pipeline ending in a
// subMergeSinkActor that forwards every produced element back to this actor
// as *subOut and reports completion / failure as *subDone / *subErr.
func (a *flatMapStreamActor[In, Out]) spawnInner(rctx *actor.ReceiveContext, input In) {
	innerSource := a.fn(input)
	a.subCounter++
	subKey := a.subCounter

	stages := make([]*stageDesc, 0, len(innerSource.stages)+1)
	stages = append(stages, innerSource.stages...)
	stages = append(stages, makeSubMergeSinkDesc(rctx.Self(), subKey))

	handle, err := materialize(rctx.Context(), a.system, stages)
	if err != nil {
		a.fail(rctx, err)
		return
	}

	a.children[subKey] = handle
	a.activeSubs++
}

// tryFlush forwards buffered sub-source output to downstream while demand
// remains, then checks whether the stream can complete.
func (a *flatMapStreamActor[In, Out]) tryFlush(rctx *actor.ReceiveContext) {
	for a.downstreamDemand > 0 && !a.outBuf.empty() {
		a.seqNo++
		a.metrics.elementsOut.Add(1)
		rctx.Tell(a.downstream, &streamElement{
			subID: a.subID,
			value: a.outBuf.pop(),
			seqNo: a.seqNo,
		})
		a.downstreamDemand--
	}

	a.maybeComplete(rctx)
}

// maybeRequestMore tops up upstream credit so total in-flight (credit +
// active sub-pipelines) reaches breadth again. Called after a sub-pipeline
// completes since that frees a breadth slot.
func (a *flatMapStreamActor[In, Out]) maybeRequestMore(rctx *actor.ReceiveContext) {
	if a.upstreamDone || a.failed {
		return
	}

	inFlight := a.upstreamCredit + int64(a.activeSubs)
	if inFlight >= int64(a.breadth) {
		return
	}

	refill := int64(a.breadth) - inFlight
	a.upstreamCredit += refill
	rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: refill})
}

// maybeComplete emits streamComplete downstream when upstream is done,
// every sub-pipeline has finished, and the output buffer has drained.
func (a *flatMapStreamActor[In, Out]) maybeComplete(rctx *actor.ReceiveContext) {
	if a.completed || a.failed {
		return
	}

	if !a.upstreamDone || a.activeSubs > 0 || !a.outBuf.empty() {
		return
	}

	a.completed = true
	rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
	rctx.Shutdown()
}

func (a *flatMapStreamActor[In, Out]) fail(rctx *actor.ReceiveContext, err error) {
	if a.failed {
		return
	}

	a.failed = true
	a.cancelChildren()

	if a.upstream != nil {
		rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
	}

	rctx.Tell(a.downstream, &streamError{subID: a.subID, err: err})
	rctx.Shutdown()
}

// cancelChildren aborts every active sub-pipeline. Used on cancellation,
// failure, and PostStop so sub-pipeline coordinators (which are top-level
// actors, independent of the parent stream's coordinator) do not outlive
// the FlatMap stage.
func (a *flatMapStreamActor[In, Out]) cancelChildren() {
	for _, handle := range a.children {
		handle.Abort()
	}

	a.children = nil
}
