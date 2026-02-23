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

// contextPoolSize controls the bounded channel-based pool for ReceiveContext.
// Unlike sync.Pool, items in a channel survive GC cycles, which eliminates
// the "pool thrashing" pattern that dominates CPU time (56% madvise + 23%
// pool overhead in profiling). The size should be large enough to absorb
// burst traffic without overflowing to heap allocation.
const contextPoolSize = 512

// contextCh is a channel-based bounded pool for ReceiveContext objects.
// It survives GC cycles and provides stable allocation behavior without
// the cross-P thrashing inherent to sync.Pool.
var contextCh = make(chan *ReceiveContext, contextPoolSize)

// responseCh is a channel-based bounded pool for response channels.
// Survives GC cycles unlike sync.Pool, eliminating cross-P thrashing
// for synchronous (Ask) message paths.
var responseCh = make(chan chan any, contextPoolSize)

// errorCh is a channel-based bounded pool for error channels.
// Survives GC cycles unlike sync.Pool.
var errorCh = make(chan chan error, contextPoolSize)

var timers = timer.NewPool()

// getContext retrieves a ReceiveContext from the channel-based pool.
// Falls back to heap allocation if the pool is empty.
func getContext() *ReceiveContext {
	select {
	case ctx := <-contextCh:
		return ctx
	default:
		return new(ReceiveContext)
	}
}

// releaseContext sends the message context back to the channel-based pool.
// If the context was stashed by the user's behavior (ctx.Stash()),
// it is still owned by the stash and must not be returned to the pool.
// If the pool is full, the context is dropped for GC collection.
func releaseContext(receiveContext *ReceiveContext) {
	if receiveContext.stashed.Load() {
		return
	}
	receiveContext.reset()
	select {
	case contextCh <- receiveContext:
	default:
		// Pool is full; let GC collect the excess context.
	}
}

func getResponseChannel() chan any {
	select {
	case ch := <-responseCh:
		return ch
	default:
		return make(chan any, 1)
	}
}

func putResponseChannel(ch chan any) {
	// Drain any stale response (e.g. from a timed-out Ask where the actor
	// replied after the caller gave up).
	for {
		select {
		case <-ch:
			continue
		default:
		}
		break
	}
	select {
	case responseCh <- ch:
	default:
	}
}

func getErrorChannel() chan error {
	select {
	case ch := <-errorCh:
		return ch
	default:
		return make(chan error, 1)
	}
}

func putErrorChannel(ch chan error) {
	// Drain any stale error.
	for {
		select {
		case <-ch:
			continue
		default:
		}
		break
	}
	select {
	case errorCh <- ch:
	default:
	}
}
