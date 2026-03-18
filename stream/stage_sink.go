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

// Generic terminal stage. Applies consumeFn to each element and manages
// demand signaling back upstream using a sliding-window watermark strategy.

type sinkActor struct {
	consumeFn  func(any) error
	onComplete func() // called once on streamComplete or Shutdown
	upstream   *actor.PID
	subID      string
	// credit tracks how many elements we have signaled upstream.
	credit  int64
	config  StageConfig
	metrics *stageMetrics
	termErr error // set when the stream terminates via *streamError; read by completionWrapper
}

// TermErr returns the terminal error recorded when a *streamError was received,
// or nil on normal completion. Implemented for the terminalErrorActor interface
// so completionWrapper can propagate the error to StreamHandle.
func (a *sinkActor) TermErr() error { return a.termErr }

func newSinkActor(consumeFn func(any) error, onComplete func(), config StageConfig) *sinkActor {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &sinkActor{consumeFn: consumeFn, onComplete: onComplete, metrics: m, config: config}
}

func (a *sinkActor) PreStart(_ *actor.Context) error { return nil }

func (a *sinkActor) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.upstream = msg.upstream
		a.subID = msg.subID
		// Kick off the pipeline by signaling initial demand.
		a.credit = a.config.InitialDemand
		rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: a.credit})

	case *streamElement:
		a.metrics.elementsIn.Add(1)
		a.credit--
		if err := a.consumeFn(msg.value); err != nil {
			a.metrics.errors.Add(1)
			switch a.config.ErrorStrategy {
			case Resume:
				// Drop element: update metrics and notify the OnDrop hook, then refill.
				a.metrics.droppedElements.Add(1)
				if a.config.OnDrop != nil {
					a.config.OnDrop(msg.value, "resume: element processing error")
				}
				if a.credit <= a.config.RefillThreshold {
					refill := a.config.InitialDemand - a.credit
					a.credit += refill
					rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: refill})
				}
				return
			case Retry:
				maxAttempts := a.config.RetryConfig.MaxAttempts
				if maxAttempts < 1 {
					maxAttempts = 1
				}
				var retryErr error
				for attempt := 0; attempt < maxAttempts; attempt++ {
					retryErr = a.consumeFn(msg.value)
					if retryErr == nil {
						break
					}
					a.metrics.errors.Add(1)
				}
				if retryErr != nil {
					a.metrics.droppedElements.Add(1)
					if a.config.OnDrop != nil {
						a.config.OnDrop(msg.value, "retry-exhausted: element processing error")
					}
					rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
					a.callOnComplete()
					rctx.Shutdown()
					return
				}
			case Supervise:
				// Delegate to actor supervision: escalate so the stream supervisor
				// can apply its directive. Behaves like FailFast until a dedicated
				// stream supervisor hierarchy is established.
				rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
				a.callOnComplete()
				rctx.Shutdown()
				return
			default: // FailFast
				rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
				a.callOnComplete()
				rctx.Shutdown()
				return
			}
		}
		a.metrics.elementsOut.Add(1)
		// Refill demand when credit falls below threshold.
		if a.credit <= a.config.RefillThreshold {
			refill := a.config.InitialDemand - a.credit
			a.credit += refill
			rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: refill})
		}

	case *streamComplete:
		a.callOnComplete()
		rctx.Shutdown()

	case *streamError:
		a.termErr = msg.err // preserved for completionWrapper → StreamHandle.Err()
		rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
		a.callOnComplete()
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

func (a *sinkActor) callOnComplete() {
	if a.onComplete != nil {
		a.onComplete()
	}
}

func (a *sinkActor) PostStop(_ *actor.Context) error { return nil }

// Captures only the first element, cancels upstream, and completes immediately.

type firstSinkActor[T any] struct {
	result   *FoldResult[T]
	upstream *actor.PID
	subID    string
	got      bool
	config   StageConfig
	metrics  *stageMetrics
}

func newFirstSinkActor[T any](result *FoldResult[T], config StageConfig) *firstSinkActor[T] {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &firstSinkActor[T]{result: result, metrics: m, config: config}
}

func (a *firstSinkActor[T]) PreStart(_ *actor.Context) error { return nil }

func (a *firstSinkActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.upstream = msg.upstream
		a.subID = msg.subID
		// Request just one element.
		rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: 1})

	case *streamElement:
		a.metrics.elementsIn.Add(1)
		if !a.got {
			if elem, ok := msg.value.(T); ok {
				a.result.mu.Lock()
				a.result.value = elem
				a.result.mu.Unlock()
			}
			a.got = true
			a.metrics.elementsOut.Add(1)
		}
		// Cancel upstream and shut down.
		rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
		a.markDone()
		rctx.Shutdown()

	case *streamComplete:
		a.markDone()
		rctx.Shutdown()

	case *streamError:
		a.markDone()
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

func (a *firstSinkActor[T]) markDone() {
	a.result.once.Do(func() { close(a.result.ready) })
}

func (a *firstSinkActor[T]) PostStop(_ *actor.Context) error { return nil }
