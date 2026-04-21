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
	"github.com/tochemey/goakt/v4/internal/timer"
)

// contextPoolSize controls the bounded channel-based pool for
// ReceiveContext. The channel pool out-performs sync.Pool on the
// Tell hot path: ReceiveContexts produced by sender goroutines are
// consumed on the worker goroutine, a producer/consumer split that
// turns sync.Pool's per-P cache into constant cross-P stealing. A
// shared channel removes the steal but introduces a mutex; sized
// large enough that producers find items waiting in steady state and
// consumers do not drop when releasing.
const contextPoolSize = 8192

// contextCh is a channel-based bounded pool for ReceiveContext objects.
// Pre-warmed at package init so the first burst of Tell calls finds
// items waiting and does not fall through to mallocgc on every send.
var contextCh = func() chan *ReceiveContext {
	ch := make(chan *ReceiveContext, contextPoolSize)
	for range contextPoolSize {
		ch <- new(ReceiveContext)
	}
	return ch
}()

// responseCh is a channel-based bounded pool for response channels.
var responseCh = make(chan chan any, contextPoolSize)

// errorCh is a channel-based bounded pool for error channels.
var errorCh = make(chan chan error, contextPoolSize)

var timers = timer.NewPool()

// emptyAnyCh is a pre-closed channel returned by BatchAsk for empty message slices.
var emptyAnyCh = func() chan any { ch := make(chan any); close(ch); return ch }()

// getContext retrieves a ReceiveContext from the channel pool, falling
// back to a fresh allocation on momentary contention/empty.
func getContext() *ReceiveContext {
	select {
	case ctx := <-contextCh:
		return ctx
	default:
		return new(ReceiveContext)
	}
}

// cloneContext returns a fresh ReceiveContext populated with src's
// message-scoped fields. Required when a context must enter a second
// mailbox: a ReceiveContext can be linked into only one mailbox at a
// time via its intrusive `next` field, so callers enqueue the clone
// and let the original complete its current lifecycle.
func cloneContext(src *ReceiveContext) *ReceiveContext {
	dst := getContext()
	dst.ctx = src.ctx
	dst.message = src.message
	dst.sender = src.sender
	dst.self = src.self
	dst.response = src.response
	dst.requestID = src.requestID
	dst.requestReplyTo = src.requestReplyTo
	dst.err = src.err
	return dst
}

// getResponseChannel returns a buffered (capacity 1) reply channel from
// the pool, allocating a fresh one on miss. Callers obtain the channel
// before issuing an Ask and return it via putResponseChannel after the
// reply is consumed (or the caller gives up).
func getResponseChannel() chan any {
	select {
	case ch := <-responseCh:
		return ch
	default:
		return make(chan any, 1)
	}
}

// putResponseChannel returns ch to the pool after draining any stale
// reply that may have arrived from an actor that responded after the
// caller's deadline expired. A full pool drops the excess for GC.
func putResponseChannel(ch chan any) {
	drainAnyChannel(ch)
	select {
	case responseCh <- ch:
	default:
	}
}

// getErrorChannel returns a buffered (capacity 1) error channel from
// the pool, allocating a fresh one on miss. Used by request paths that
// pipe an asynchronous error back to the caller.
func getErrorChannel() chan error {
	select {
	case ch := <-errorCh:
		return ch
	default:
		return make(chan error, 1)
	}
}

// putErrorChannel returns ch to the pool after draining any stale
// error. A full pool drops the excess for GC.
func putErrorChannel(ch chan error) {
	drainErrorChannel(ch)
	select {
	case errorCh <- ch:
	default:
	}
}

// drainAnyChannel non-blockingly empties ch so it can be returned to a
// pool in a clean state.
func drainAnyChannel(ch chan any) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// drainErrorChannel non-blockingly empties ch so it can be returned to
// a pool in a clean state.
func drainErrorChannel(ch chan error) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
