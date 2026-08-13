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

// channelPoolSize controls the bounded pool for the error channels used
// by the request paths.
const channelPoolSize = 8192

// errorCh is a channel-based bounded pool for error channels.
var errorCh = make(chan chan error, channelPoolSize)

var timers = timer.NewPool()

// emptyAnyCh is a pre-closed channel returned by BatchAsk for empty message slices.
var emptyAnyCh = func() chan any { ch := make(chan any); close(ch); return ch }()

// getResponseChannel returns a fresh buffered (capacity 1) reply
// channel for one synchronous request. Reply channels are deliberately
// not pooled: a process-wide pool serializes every concurrent Ask on
// one channel lock, and a reply racing the asker's timeout could land
// in the channel after the drain that precedes pooling, handing the
// next borrower a stale response. A per-request channel scales with
// concurrency and makes a late reply land in an unreachable channel
// instead.
func getResponseChannel() chan any {
	return make(chan any, 1)
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
