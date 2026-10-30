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

// askReplyChannelPool recycles the reply channels of actor asks, in the
// sharded ring the grain ask path uses (see grainReplyChannelPool). A ring
// per shard keeps concurrent asks off a shared lock, and a channel is
// returned only once its ask holds the reply and has stopped its timeout
// before it fired, so a late reply or timeout can never reach the next
// borrower; a reply is written at most once per channel because forwarding
// hands the reply guard over with the channel (see
// ReceiveContext.inheritReplyRoute). Together these answer why the actor
// ask did not pool its channels before, and spare every steady-state Ask
// the 128 bytes of a fresh channel.
var askReplyChannelPool = newChannelPool[any]()

// emptyAnyCh is a pre-closed channel returned by BatchAsk for empty message slices.
var emptyAnyCh = func() chan any { ch := make(chan any); close(ch); return ch }()

// getResponseChannel returns a buffered (capacity 1) reply channel for one
// synchronous actor request, from the shard of askReplyChannelPool that
// shard names, the target actor's context-pool shard. An empty shard
// allocates a fresh channel.
func getResponseChannel(shard uint32) chan any {
	return askReplyChannelPool.get(shard)
}

// putResponseChannel returns ch to the shard it was fetched from. Only an
// Ask that has its reply in hand and has stopped its timeout before it fired
// may return a channel (see PID.Ask): a channel a late reply or a late
// timeout could still reach is abandoned to the garbage collector instead,
// so no borrower ever reads a value meant for another request.
func putResponseChannel(shard uint32, ch chan any) {
	askReplyChannelPool.put(shard, ch)
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
	drainChannel(ch)
}

// drainChannel non-blockingly empties ch, whatever its element type, so
// it can be returned to a pool in a clean state.
func drainChannel[T any](ch chan T) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
