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
	"github.com/tochemey/goakt/v3/internal/codec"
	"github.com/tochemey/goakt/v3/internal/ds"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/reentrancy"
)

// RequestCall represents a handle to a pending asynchronous request started via
// Request or RequestName.
//
// A RequestCall lets callers:
//   - register exactly one completion continuation (Then), and
//   - attempt to cancel the in-flight request (Cancel).
//
// # Concurrency and execution model
//
// To preserve single-threaded access to an actor's internal state, continuations
// registered with Then are intended to run on the actor's mailbox (processing)
// thread when the request completes.
//
// Call Then from within Receive (or otherwise from the actor's message loop) to
// ensure the continuation executes on the mailbox thread. If Then is registered
// after the request has already completed, the continuation is invoked
// synchronously in the caller's goroutine.
//
// Then is one-shot: only the first call registers a continuation; subsequent
// calls are ignored.
//
// # Cancellation
//
// Cancel requests cancellation of the pending request. Cancellation is best-effort:
// the request may still complete successfully or with an error if it races with
// completion or if the underlying operation cannot be interrupted.
//
// Cancel is idempotent: it may be called multiple times. If the request is
// already completed or cancellation has already been requested, Cancel returns nil.
//
// When possible, cancellation is delivered through the actor's mailbox so that
// any completion continuation runs on the actor's processing thread.
type RequestCall interface {
	// Then registers a continuation to be invoked when the request completes.
	//
	// The callback is passed either the response message (as proto.Message) and
	// a nil error, or a nil message and a non-nil error.
	//
	// Execution:
	//   - If registered before completion, the continuation is intended to run on
	//     the actor's mailbox thread (see the type-level comment).
	//   - If called after completion, the continuation runs immediately and
	//     synchronously in the caller's goroutine.
	//
	// Only the first call to Then is honored; subsequent calls are ignored.
	Then(func(proto.Message, error))

	// Cancel requests cancellation of the pending request.
	//
	// Cancel is safe to call multiple times. It returns nil if the request has
	// already completed or cancellation has already been requested.
	//
	// Cancellation is best-effort and may race with completion. When possible,
	// the cancellation is delivered via the actor's mailbox so that any
	// continuation runs on the actor's processing thread.
	Cancel() error
}

// RequestOption configures a single async request.
//
// Design decision: request-level options let callers refine behavior without
// changing actor-wide defaults.
type RequestOption func(*requestConfig)

// WithReentrancyMode overrides the actor-level reentrancy policy for this request.
//
// This is a per-call override: it affects only the async request being started
// (via Request/RequestName) and does not mutate the actor's configured default.
//
// Use this to temporarily opt into stricter or looser reentrancy semantics for a
// single interaction—for example, select reentrancy.StashNonReentrant for a
// blocking/serialized request—while leaving the actor's overall behavior unchanged.
//
// Note: while any StashNonReentrant request is in flight, user messages are stashed
// until the last blocking request completes; per-call overrides do not bypass this.
//
// If the actor does not have reentrancy enabled, Request/RequestName still return
// ErrReentrancyDisabled regardless of this option. If mode is reentrancy.Off, the
// request is rejected with ErrReentrancyDisabled.
//
// Design decision: per-call overrides allow safe, targeted deviations from the
// actor default (e.g., stashing a single request while keeping general throughput).
func WithReentrancyMode(mode reentrancy.Mode) RequestOption {
	return func(config *requestConfig) {
		if config != nil {
			config.mode = mode
			config.modeSet = true
		}
	}
}

// WithRequestTimeout sets a timeout for a single async request.
//
// If the timeout elapses before a response is observed, the request completes
// with ErrRequestTimeout. The timeout signal is delivered through the requester's
// mailbox, so completion timing depends on mailbox processing. It is best-effort
// and may race with a successful response (whichever completion wins).
//
// A value <= 0 disables the timeout for this request (no automatic expiry).
//
// Notes:
//   - This option is per-request; there is no implicit/global default timeout.
//   - On completion (success, error, cancellation, or timeout) any registered
//     continuation (Then) is invoked according to RequestCall's execution rules.
//   - Cancel and timeout are independent signals; either may "win" depending on timing.
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

type requestHandle struct {
	state *requestState
}

// Then registers a continuation to run when the request completes.
// If Then is called after completion, the callback runs synchronously in
// the caller's goroutine. Additional calls are ignored.
//
// Design decision: if the call already completed, the callback executes immediately
// to avoid forcing callers to handle a separate "already done" state.
func (x *requestHandle) Then(callback func(proto.Message, error)) {
	if x == nil || x.state == nil || callback == nil {
		return
	}
	x.state.setCallback(callback)
}

// Cancel requests cancellation of the pending request.
// It is safe to call multiple times. If the call already completed or has a
// cancellation pending, Cancel returns nil. It returns ErrInvalidMessage when
// the handle is not usable.
//
// Design decision: cancellation is delivered through the actor mailbox so that
// continuations run on the actor's processing thread.
func (x *requestHandle) Cancel() error {
	if x == nil || x.state == nil {
		return gerrors.ErrInvalidMessage
	}
	return x.state.cancel()
}

type requestConfig struct {
	mode    reentrancy.Mode
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

type requestState struct {
	id   string
	mode reentrancy.Mode
	pid  *PID

	mu              sync.Mutex
	completed       bool
	result          proto.Message
	err             error
	callback        func(proto.Message, error)
	cancelRequested bool
	stopTimeout     func()
}

// newRequestState initializes tracking for a single in-flight request.
//
// Design decision: state is keyed by correlation ID to support concurrent calls.
func newRequestState(id string, mode reentrancy.Mode, pid *PID) *requestState {
	return &requestState{
		id:   id,
		mode: mode,
		pid:  pid,
	}
}

// setCallback installs the continuation, or runs it immediately if already completed.
//
// Design decision: immediate execution avoids extra scheduling for completed calls.
func (s *requestState) setCallback(callback func(proto.Message, error)) {
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
func (s *requestState) complete(result proto.Message, err error) (func(proto.Message, error), bool) {
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
func (s *requestState) cancel() error {
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
func (s *requestState) startTimeout(timeout time.Duration) {
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
func (s *requestState) stopTimeoutIfSet() {
	s.mu.Lock()
	stop := s.stopTimeout
	s.stopTimeout = nil
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
}

type reentrancyState struct {
	mode          reentrancy.Mode
	maxInFlight   int
	requestStates *ds.Map[string, *requestState]
	inFlightCount atomic.Int64
	blockingCount atomic.Int64
}

// newReentrancyState initializes per-actor reentrancy counters and storage.
//
// Design decision: counters are atomic to avoid blocking the mailbox loop.
func newReentrancyState(mode reentrancy.Mode, maxInFlight int) *reentrancyState {
	return &reentrancyState{
		mode:          mode,
		maxInFlight:   maxInFlight,
		requestStates: ds.NewMap[string, *requestState](),
	}
}

// reset clears all in-flight state after shutdown or restart.
//
// Design decision: full reset prevents stale async calls from leaking across lifecycles.
func (s *reentrancyState) reset() {
	if s == nil {
		return
	}
	s.requestStates.Reset()
	s.inFlightCount.Store(0)
	s.blockingCount.Store(0)
}

func (s *reentrancyState) toProto() *internalpb.ReentrancyConfig {
	reentrancy := reentrancy.New(
		reentrancy.WithMode(s.mode),
		reentrancy.WithMaxInFlight(s.maxInFlight),
	)
	return codec.EncodeReentrancy(reentrancy)
}
