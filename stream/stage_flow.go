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
	"time"

	"github.com/tochemey/goakt/v4/actor"
)

// flowActor is the generic flow stage actor.
// It receives elements from its upstream, applies transformFn to each one,
// buffers the resulting output elements, and forwards them downstream as demand
// arrives. The output buffer uses the GC-safe queue type so popped slots are
// released immediately without pinning the backing array.
type flowActor struct {
	transformFn      func(any) ([]any, error)
	upstream         *actor.PID
	downstream       *actor.PID
	subID            string
	seqNo            uint64
	outputBuf        queue
	startBuf         []time.Time // per-output start times; populated only when tracer != nil
	upstreamCredit   int64
	downstreamDemand int64
	completing       bool // true once upstream sent streamComplete
	config           StageConfig
	metrics          *stageMetrics
	stageName        string
	tracer           Tracer
	lastInputStart   time.Time // set only when tracer != nil; records start time for the current input
}

// newFlowActor creates a flowActor that applies transformFn to each element.
func newFlowActor(transformFn func(any) ([]any, error), config StageConfig) *flowActor {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &flowActor{
		transformFn: transformFn,
		metrics:     m,
		config:      config,
		stageName:   config.Name,
		tracer:      config.Tracer,
	}
}

func (a *flowActor) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, streamElement, streamComplete,
// streamError, and streamCancel.
func (a *flowActor) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.upstream = msg.upstream
		a.downstream = msg.downstream
		a.subID = msg.subID

	case *streamRequest:
		a.downstreamDemand += msg.n
		a.tryFlushOutput(rctx)
		a.maybeRequestUpstream(rctx)

	case *streamElement:
		// Capture the clock only when tracing is active — time.Now() is not free
		// and would otherwise be paid on every element even with no tracer attached.
		if a.tracer != nil {
			a.lastInputStart = time.Now()
		}
		a.metrics.elementsIn.Add(1)
		outs, err := a.transformFn(msg.value)
		if err != nil {
			a.metrics.errors.Add(1)
			if a.tracer != nil {
				a.tracer.OnError(a.stageName, err)
			}
			switch a.config.ErrorStrategy {
			case Resume:
				// Drop element: update metrics, notify the OnDrop hook, and request a replacement.
				a.upstreamCredit--
				a.metrics.droppedElements.Add(1)
				if a.config.OnDrop != nil {
					a.config.OnDrop(msg.value, "resume: element processing error")
				}
				a.maybeRequestUpstream(rctx)
				return
			case Retry:
				maxAttempts := a.config.RetryConfig.MaxAttempts
				if maxAttempts < 1 {
					maxAttempts = 1
				}
				var retryErr error
				for attempt := 0; attempt < maxAttempts; attempt++ {
					outs, retryErr = a.transformFn(msg.value)
					if retryErr == nil {
						break
					}
					a.metrics.errors.Add(1)
					if a.tracer != nil {
						a.tracer.OnError(a.stageName, retryErr)
					}
				}
				if retryErr != nil {
					rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
					rctx.Tell(a.downstream, &streamError{subID: a.subID, err: retryErr})
					rctx.Shutdown()
					return
				}
				err = nil // successfully retried
			case Supervise:
				// Delegate to actor supervision: escalate the error so the
				// stream supervisor can apply its restart/stop directive.
				// For now this behaves like FailFast until a dedicated stream
				// supervisor actor hierarchy is wired in.
				rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
				rctx.Tell(a.downstream, &streamError{subID: a.subID, err: err})
				rctx.Shutdown()
				return
			default: // FailFast
				rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
				rctx.Tell(a.downstream, &streamError{subID: a.subID, err: err})
				rctx.Shutdown()
				return
			}
		}
		a.upstreamCredit--
		for _, v := range outs {
			a.outputBuf.push(v)
			if a.tracer != nil {
				a.startBuf = append(a.startBuf, a.lastInputStart)
			}
		}
		a.metrics.elementsOut.Add(uint64(len(outs)))
		a.tryFlushOutput(rctx)
		a.maybeRequestUpstream(rctx)

	case *streamComplete:
		// Mark as completing and flush what we can. If the buffer still has
		// elements we keep the actor alive to serve incoming streamRequests
		// so downstream can drain everything before we propagate completion.
		a.completing = true
		if a.tracer != nil {
			a.tracer.OnComplete(a.stageName)
		}
		a.tryFlushOutput(rctx)
		if a.outputBuf.empty() {
			rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
			rctx.Shutdown()
		}

	case *streamError:
		rctx.Tell(a.downstream, msg)
		rctx.Shutdown()

	case *streamCancel:
		rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// tryFlushOutput forwards as many buffered output elements as downstream demand allows.
// When the actor is in the completing state and the buffer drains fully, it
// propagates streamComplete downstream and shuts down.
// Tracer OnElement is fired here — after seqNo is definitively incremented —
// so that multi-output transforms (FlatMap) report the correct seqNo for each
// emitted element rather than a single incorrect value for the batch.
func (a *flowActor) tryFlushOutput(rctx *actor.ReceiveContext) {
	for a.downstreamDemand > 0 && !a.outputBuf.empty() {
		a.seqNo++
		if a.tracer != nil {
			startTime := a.startBuf[0]
			a.startBuf = a.startBuf[1:]
			a.tracer.OnElement(a.stageName, a.seqNo, time.Since(startTime).Nanoseconds())
		}
		rctx.Tell(a.downstream, &streamElement{
			subID: a.subID,
			value: a.outputBuf.pop(),
			seqNo: a.seqNo,
		})
		a.downstreamDemand--
	}
	if a.completing && a.outputBuf.empty() {
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()
	}
}

// maybeRequestUpstream sends a streamRequest upstream when the upstream credit
// falls below the refill threshold and there is capacity to absorb more elements.
// No-op when upstream has already completed.
func (a *flowActor) maybeRequestUpstream(rctx *actor.ReceiveContext) {
	if a.completing {
		return
	}
	available := a.config.InitialDemand - a.upstreamCredit - int64(a.outputBuf.len())
	if available <= 0 {
		return
	}
	if a.upstreamCredit > a.config.RefillThreshold {
		return
	}
	a.upstreamCredit += available
	if a.tracer != nil {
		a.tracer.OnDemand(a.stageName, available)
	}
	rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: available})
}

func (a *flowActor) PostStop(_ *actor.Context) error { return nil }

// fusedFlowActor is a flow actor that applies a pre-composed chain of stateless
// transforms (the result of stage fusion). It has no error strategy or retry
// logic beyond FailFast — fused stages must all be stateless.
type fusedFlowActor struct {
	fn         func(any) (any, bool, error)
	downstream *actor.PID
	upstream   *actor.PID
	subID      string
	seqNo      uint64
	credit     int64 // outstanding upstream credit; refilled in batches like flowActor
	config     StageConfig
}

func newFusedFlowActor(fn func(any) (any, bool, error), config StageConfig) *fusedFlowActor {
	return &fusedFlowActor{fn: fn, config: config}
}

func (a *fusedFlowActor) PreStart(_ *actor.Context) error { return nil }

func (a *fusedFlowActor) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.upstream = msg.upstream
		a.downstream = msg.downstream
		a.subID = msg.subID
		// Send initial demand upstream and track the outstanding credit.
		a.credit = a.config.InitialDemand
		rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: a.credit})

	case *streamElement:
		result, pass, err := a.fn(msg.value)
		if err != nil {
			rctx.Tell(a.downstream, &streamError{subID: a.subID, err: err})
			rctx.Shutdown()
			return
		}
		if pass {
			a.seqNo++
			rctx.Tell(a.downstream, &streamElement{subID: a.subID, value: result, seqNo: a.seqNo})
		}
		// Refill upstream credit in batches (matching flowActor's watermark strategy)
		// instead of sending a streamRequest for every single element. This reduces
		// demand-message allocations by ~RefillThreshold-fold on the fused fast path.
		a.credit--
		if a.credit <= a.config.RefillThreshold {
			refill := a.config.InitialDemand - a.credit
			a.credit += refill
			rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: refill})
		}

	case *streamComplete:
		rctx.Tell(a.downstream, msg)
		rctx.Shutdown()

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

func (a *fusedFlowActor) PostStop(_ *actor.Context) error { return nil }

// batchFlowActor groups incoming elements into slices of at most maxSize,
// flushing early when the GoAkt actor-system scheduler fires a batchFlush after
// maxWait has elapsed with at least one element buffered.
type batchFlowActor[T any] struct {
	maxSize          int
	maxWait          time.Duration
	upstream         *actor.PID
	downstream       *actor.PID
	subID            string
	seqNo            uint64
	window           []T
	upstreamCredit   int64
	downstreamDemand int64
	timerActive      bool
	schedRef         string
	config           StageConfig
	metrics          *stageMetrics
}

// newBatchFlowActor creates a batchFlowActor that collects at most maxSize elements
// or flushes after maxWait, whichever comes first.
func newBatchFlowActor[T any](maxSize int, maxWait time.Duration, config StageConfig) *batchFlowActor[T] {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &batchFlowActor[T]{maxSize: maxSize, maxWait: maxWait, metrics: m, config: config}
}

// PreStart captures the actor name as the schedule reference so it is safe
// to read from PostStop without a data race.
func (a *batchFlowActor[T]) PreStart(ctx *actor.Context) error {
	a.schedRef = ctx.ActorName()
	return nil
}

// Receive handles stageWire, streamRequest, streamElement, batchFlush,
// streamComplete, streamError, and streamCancel.
func (a *batchFlowActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.upstream = msg.upstream
		a.downstream = msg.downstream
		a.subID = msg.subID

	case *streamRequest:
		a.downstreamDemand += msg.n
		a.maybeRequestUpstream(rctx)

	case *streamElement:
		a.metrics.elementsIn.Add(1)
		a.upstreamCredit--
		a.window = append(a.window, msg.value.(T))
		if !a.timerActive && len(a.window) == 1 {
			// Schedule a flush timer on the first element of a new window.
			a.timerActive = true
			_ = rctx.ActorSystem().ScheduleOnce(rctx.Context(), &batchFlush{},
				rctx.Self(), a.maxWait, actor.WithReference(a.schedRef))
		}
		if len(a.window) >= a.maxSize {
			a.flush(rctx)
		}
		a.maybeRequestUpstream(rctx)

	case *batchFlush:
		a.timerActive = false
		if len(a.window) > 0 {
			a.flush(rctx)
		}

	case *streamComplete:
		// Flush any remaining elements before propagating completion.
		if len(a.window) > 0 {
			a.flush(rctx)
		}
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	case *streamError:
		rctx.Tell(a.downstream, msg)
		rctx.Shutdown()

	case *streamCancel:
		_ = rctx.ActorSystem().CancelSchedule(a.schedRef)
		rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// flush emits the current window as a single batch element to downstream,
// provided demand is available.
func (a *batchFlowActor[T]) flush(rctx *actor.ReceiveContext) {
	if a.downstreamDemand <= 0 {
		return
	}
	batch := make([]T, len(a.window))
	copy(batch, a.window)
	a.window = a.window[:0]
	a.seqNo++
	a.metrics.elementsOut.Add(1)
	rctx.Tell(a.downstream, &streamElement{
		subID: a.subID,
		value: batch,
		seqNo: a.seqNo,
	})
	a.downstreamDemand--
}

// maybeRequestUpstream refills upstream credit when it falls below the threshold.
func (a *batchFlowActor[T]) maybeRequestUpstream(rctx *actor.ReceiveContext) {
	available := a.config.InitialDemand - a.upstreamCredit - int64(len(a.window))
	if available <= 0 {
		return
	}
	if a.upstreamCredit > a.config.RefillThreshold {
		return
	}
	a.upstreamCredit += available
	rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: available})
}

// PostStop cancels the pending flush schedule if the actor is stopped externally.
func (a *batchFlowActor[T]) PostStop(_ *actor.Context) error {
	if a.schedRef != "" && a.config.System != nil {
		_ = a.config.System.CancelSchedule(a.schedRef)
	}
	return nil
}

// throttleActor paces output to at most one element per perElement duration.
// It uses the GoAkt actor-system scheduler for periodic throttleTick messages
// so the actor remains responsive to cancellation and completion while waiting
// for the next emission slot. Incoming elements are queued in a GC-safe buffer.
type throttleActor[T any] struct {
	perElement time.Duration
	upstream   *actor.PID
	downstream *actor.PID
	subID      string
	seqNo      uint64
	buf        queue
	credit     int64
	demand     int64
	schedRef   string
	config     StageConfig
	metrics    *stageMetrics
	completed  bool
}

// newThrottleActor creates a throttleActor that emits at most one element per perElement.
func newThrottleActor[T any](perElement time.Duration, config StageConfig) *throttleActor[T] {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &throttleActor[T]{perElement: perElement, metrics: m, config: config}
}

// PreStart captures the actor name as the schedule reference so it is safe
// to read from PostStop without a data race.
func (a *throttleActor[T]) PreStart(ctx *actor.Context) error {
	a.schedRef = ctx.ActorName()
	return nil
}

// Receive handles stageWire, streamRequest, streamElement, throttleTick,
// streamComplete, streamError, and streamCancel.
func (a *throttleActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.upstream = msg.upstream
		a.downstream = msg.downstream
		a.subID = msg.subID
		_ = rctx.ActorSystem().Schedule(rctx.Context(), &throttleTick{},
			rctx.Self(), a.perElement, actor.WithReference(a.schedRef))

	case *streamRequest:
		a.demand += msg.n
		a.maybeRequestUpstream(rctx)

	case *streamElement:
		a.metrics.elementsIn.Add(1)
		a.credit--
		a.buf.push(msg.value)

	case *throttleTick:
		if a.demand > 0 && !a.buf.empty() {
			a.seqNo++
			rctx.Tell(a.downstream, &streamElement{
				subID: a.subID,
				value: a.buf.pop(),
				seqNo: a.seqNo,
			})
			a.demand--
			a.metrics.elementsOut.Add(1)
			a.maybeRequestUpstream(rctx)
		}
		if a.completed && a.buf.empty() {
			a.finish(rctx)
		}

	case *streamComplete:
		a.completed = true
		if a.buf.empty() {
			a.finish(rctx)
		}

	case *streamError:
		_ = rctx.ActorSystem().CancelSchedule(a.schedRef)
		rctx.Tell(a.downstream, msg)
		rctx.Shutdown()

	case *streamCancel:
		_ = rctx.ActorSystem().CancelSchedule(a.schedRef)
		rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// maybeRequestUpstream refills upstream credit when it falls below the threshold.
func (a *throttleActor[T]) maybeRequestUpstream(rctx *actor.ReceiveContext) {
	available := a.config.InitialDemand - a.credit - int64(a.buf.len())
	if available <= 0 || a.credit > a.config.RefillThreshold {
		return
	}
	a.credit += available
	rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: available})
}

// finish cancels the recurring schedule and signals downstream completion.
func (a *throttleActor[T]) finish(rctx *actor.ReceiveContext) {
	_ = rctx.ActorSystem().CancelSchedule(a.schedRef)
	rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
	rctx.Shutdown()
}

// PostStop cancels the recurring schedule if the actor is stopped externally.
func (a *throttleActor[T]) PostStop(_ *actor.Context) error {
	if a.schedRef != "" && a.config.System != nil {
		_ = a.config.System.CancelSchedule(a.schedRef)
	}
	return nil
}
