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

package actor

import (
	"sync/atomic"

	"github.com/tochemey/goakt/v4/internal/types"
)

// supervisionWork pairs a failing actor with the signal describing its failure.
// The PID is carried directly so the consumer can act on it without a registry
// lookup.
type supervisionWork struct {
	pid    *PID
	signal *supervisionSignal
}

// supervision is a single shared consumer that handles actor failures for the
// whole system. It replaces the previous per-actor supervision goroutine: one
// goroutine drains failure signals from every actor, keeping the goroutine count
// bounded and independent of the actor population. Failures are rare and the
// per-signal work is non-blocking (notifyParent only enqueues to the parent
// mailbox), so a single consumer never head-of-line stalls.
type supervision struct {
	queue   chan supervisionWork
	stopCh  chan types.Unit
	done    chan types.Unit
	started atomic.Bool
	stopped atomic.Bool
}

// newSupervision constructs an unstarted shared supervision consumer.
func newSupervision() *supervision {
	return &supervision{
		queue:  make(chan supervisionWork, 1024),
		stopCh: make(chan types.Unit),
		done:   make(chan types.Unit),
	}
}

// start spawns the consumer goroutine. Idempotent: subsequent calls are no-ops.
func (s *supervision) start() {
	if !s.started.CompareAndSwap(false, true) {
		return
	}
	go s.run()
}

// stop signals the consumer to exit. It is fire-and-forget to mirror
// dispatcher.signalStop, which does not join its workers either. Idempotent.
func (s *supervision) stop() {
	if s.stopped.CompareAndSwap(false, true) {
		close(s.stopCh)
	}
}

// Submit hands a failure signal to the shared consumer. It is called from the
// dispatcher worker inside recovery(). The send is back-pressured rather than
// dropped, with a stop escape so a producer never wedges during shutdown.
func (s *supervision) Submit(pid *PID, signal *supervisionSignal) {
	if pid == nil || signal == nil || !s.started.Load() {
		return
	}

	select {
	case s.queue <- supervisionWork{pid: pid, signal: signal}:
	case <-s.stopCh:
	}
}

// run drains the failure queue until stopped.
func (s *supervision) run() {
	defer close(s.done)
	for {
		select {
		case work := <-s.queue:
			// IsRunning is false once an actor is suspended or stopping. This both
			// drops signals for actors torn down between enqueue and dispatch and
			// reproduces the prior behavior where only the first failure is acted on:
			// notifyParent suspends on the first signal and any later in-flight signal
			// is skipped here.
			if work.pid.IsRunning() {
				work.pid.notifyParent(work.signal)
			}
		case <-s.stopCh:
			return
		}
	}
}
