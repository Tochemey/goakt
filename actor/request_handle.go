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
	"context"
	"sync"
	"time"

	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/ds"
)

// RequestHandle represents a pending async request started via Request/RequestName.
//
// Design decision: callbacks run on the actor's mailbox thread to preserve
// single-threaded state access. Call Then from within Receive to avoid executing
// callbacks outside the actor's message loop.
type RequestHandle interface {
	// Then registers a continuation to run when the response arrives or fails.
	Then(func(proto.Message, error))
	// Cancel aborts the pending request. Cancel is idempotent.
	Cancel() error
}

type asyncCall struct {
	state *asyncCallState
}

// Then is safe to call multiple times; only the first callback is kept.
//
// Design decision: if the call already completed, the callback executes immediately
// to avoid forcing callers to handle a separate "already done" state.
func (c *asyncCall) Then(callback func(proto.Message, error)) {
	if c == nil || c.state == nil || callback == nil {
		return
	}
	c.state.setCallback(callback)
}

// Cancel returns ErrInvalidMessage when the call handle is not usable.
//
// Design decision: cancellation is delivered through the actor mailbox so that
// continuations run on the actor's processing thread.
func (c *asyncCall) Cancel() error {
	if c == nil || c.state == nil {
		return gerrors.ErrInvalidMessage
	}
	return c.state.cancel()
}

// RequestOption configures a single async request.
//
// Design decision: request-level options let callers refine behavior without
// changing actor-wide defaults.
type RequestOption func(*requestConfig)

// WithReentrancyMode overrides the actor-level reentrancy policy for this request.
//
// Design decision: per-call overrides allow safe, targeted deviations from the
// actor default (e.g., stashing a single request while keeping general throughput).
func WithReentrancyMode(mode ReentrancyMode) RequestOption {
	return func(config *requestConfig) {
		if config == nil {
			return
		}

		config.mode = mode
		config.modeSet = true
	}
}

// WithRequestTimeout sets a timeout for the async request.
//
// Design decision: timeouts are per-request to avoid forcing a global timeout
// policy on the actor. A value <= 0 disables the timeout for the request.
func WithRequestTimeout(timeout time.Duration) RequestOption {
	return func(config *requestConfig) {
		if config == nil {
			return
		}

		if timeout <= 0 {
			config.timeout = nil
			return
		}

		config.timeout = &timeout
	}
}

type requestConfig struct {
	mode    ReentrancyMode
	modeSet bool
	timeout *time.Duration
}

// newRequestConfig applies request options with zero-value defaults.
//
// Design decision: zero values mean "use actor defaults" to keep call sites concise.
func newRequestConfig(opts ...RequestOption) *requestConfig {
	config := new(requestConfig)
	for _, opt := range opts {
		opt(config)
	}
	return config
}

type asyncCallState struct {
	id   string
	mode ReentrancyMode
	pid  *PID

	mu              sync.Mutex
	completed       bool
	result          proto.Message
	err             error
	callback        func(proto.Message, error)
	cancelRequested bool
	stopTimeout     func()
}

// newAsyncCallState initializes tracking for a single in-flight request.
//
// Design decision: state is keyed by correlation ID to support concurrent calls.
func newAsyncCallState(id string, mode ReentrancyMode, pid *PID) *asyncCallState {
	return &asyncCallState{
		id:   id,
		mode: mode,
		pid:  pid,
	}
}

// setCallback installs the continuation, or runs it immediately if already completed.
//
// Design decision: immediate execution avoids extra scheduling for completed calls.
func (s *asyncCallState) setCallback(callback func(proto.Message, error)) {
	s.mu.Lock()
	if s.callback != nil {
		s.mu.Unlock()
		return
	}

	if s.completed {
		s.callback = callback
		result := s.result
		err := s.err
		s.mu.Unlock()
		callback(result, err)
		return
	}

	s.callback = callback
	s.mu.Unlock()
}

// complete marks the call as done; returns the callback to execute if any.
//
// Design decision: completion is idempotent to tolerate duplicate responses.
func (s *asyncCallState) complete(result proto.Message, err error) (func(proto.Message, error), bool) {
	s.mu.Lock()
	if s.completed {
		s.mu.Unlock()
		return nil, false
	}
	s.completed = true
	s.result = result
	s.err = err
	callback := s.callback
	s.mu.Unlock()
	return callback, true
}

// cancel enqueues a cancellation error on the actor's mailbox.
//
// Design decision: mailbox delivery preserves single-threaded actor state access.
func (s *asyncCallState) cancel() error {
	s.mu.Lock()
	if s.completed || s.cancelRequested {
		s.mu.Unlock()
		return nil
	}
	s.cancelRequested = true
	s.mu.Unlock()
	return s.pid.enqueueAsyncError(context.Background(), s.id, gerrors.ErrRequestCanceled)
}

// startTimeout triggers a timeout error via the shared timer pool.
//
// Design decision: using the timer pool reduces allocations under load.
func (s *asyncCallState) startTimeout(timeout time.Duration) {
	if timeout <= 0 {
		return
	}

	stopCh := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(stopCh)
		})
	}

	s.mu.Lock()
	s.stopTimeout = stop
	s.mu.Unlock()

	go func() {
		timer := timers.Get(timeout)
		defer timers.Put(timer)
		select {
		case <-timer.C:
			_ = s.pid.enqueueAsyncError(context.Background(), s.id, gerrors.ErrRequestTimeout)
		case <-stopCh:
			return
		}
	}()
}

// stopTimeoutIfSet cancels any active timeout goroutine.
//
// Design decision: timeouts are stoppable to avoid spurious errors after completion.
func (s *asyncCallState) stopTimeoutIfSet() {
	s.mu.Lock()
	stop := s.stopTimeout
	s.stopTimeout = nil
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
}

type reentrancyState struct {
	mode          ReentrancyMode
	maxInFlight   int
	calls         *ds.Map[string, *asyncCallState]
	inFlightCount atomic.Int64
	blockingCount atomic.Int64
}

// newReentrancyState initializes per-actor reentrancy counters and storage.
//
// Design decision: counters are atomic to avoid blocking the mailbox loop.
func newReentrancyState(mode ReentrancyMode, maxInFlight int) *reentrancyState {
	return &reentrancyState{
		mode:        mode,
		maxInFlight: maxInFlight,
		calls:       ds.NewMap[string, *asyncCallState](),
	}
}

// reset clears all in-flight state after shutdown or restart.
//
// Design decision: full reset prevents stale async calls from leaking across lifecycles.
func (s *reentrancyState) reset() {
	if s == nil {
		return
	}
	s.calls.Reset()
	s.inFlightCount.Store(0)
	s.blockingCount.Store(0)
}
