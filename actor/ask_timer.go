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
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// askTimedOut is the value an askTimer delivers into an Ask's reply channel
// when the Ask times out. It is unexported, so no handler can respond with it,
// and an Ask that receives it knows its timeout fired before any reply.
type askTimedOut struct{}

// errAckTimedOut is the error an ackTimer delivers into a grain Tell's
// acknowledgment channel when the Tell times out. It is unexported, so no
// handler can fail with it, and a Tell that receives it knows its timeout
// fired before any acknowledgment; the caller is given ErrRequestTimeout.
var errAckTimedOut = errors.New("acknowledgment timed out")

// replyTimer times out one request by delivering a timed-out value into the
// request's own reply channel, so the caller waits on that channel and its
// context only. T is the channel element: any for an actor or grain Ask reply,
// error for a grain Tell acknowledgment.
//
// Blocking in a select that includes a timer channel makes the runtime arm and
// disarm that timer around every park of the caller, which measured about 180 ns
// per round trip on an 8-core arm64 machine, a fifth of an Ask. A timer that
// runs a function when it expires has no channel to park on. The reply channel
// takes the timeout safely because it serves one request at a time and is
// written only with non-blocking sends: whichever of the reply and the timeout
// comes first is what the caller reads. The channel goes back to its pool only
// when the timer was stopped before it fired, so the other write is dropped
// with an abandoned channel, never read by a later request.
type replyTimer[T any] struct {
	// timer runs fire when the request times out. It is created once per
	// replyTimer and rearmed with Reset when the replyTimer is reused from
	// its pool.
	timer *time.Timer
	// reply holds the reply channel of the request being timed. It is atomic
	// because fire reads it on the timer's goroutine.
	reply atomic.Value
	// timedOut is the value fire delivers.
	timedOut T
	// pool is where a timer stopped before it fired goes back to.
	pool *sync.Pool
}

// askTimer times out an Ask with askTimedOut.
type askTimer = replyTimer[any]

// ackTimer times out a grain Tell with errAckTimedOut.
type ackTimer = replyTimer[error]

// askTimers recycles askTimers whose timer was stopped before it fired.
var askTimers sync.Pool

// ackTimers recycles ackTimers whose timer was stopped before it fired.
var ackTimers sync.Pool

// startAskTimer arms a timeout of d for the Ask whose reply channel is reply.
// The caller must stop it once the Ask is answered or abandoned.
func startAskTimer(reply chan any, d time.Duration) *askTimer {
	return startReplyTimer[any](&askTimers, reply, askTimedOut{}, d)
}

// startAckTimer arms a timeout of d for the grain Tell whose acknowledgment
// channel is ack. The caller must stop it once the Tell is acknowledged or
// abandoned.
func startAckTimer(ack chan error, d time.Duration) *ackTimer {
	return startReplyTimer(&ackTimers, ack, errAckTimedOut, d)
}

// startReplyTimer takes a timer from pool, or creates one, and arms it to
// deliver timedOut into reply after d.
func startReplyTimer[T any](pool *sync.Pool, reply chan T, timedOut T, d time.Duration) *replyTimer[T] {
	x, _ := pool.Get().(*replyTimer[T])
	if x == nil {
		x = &replyTimer[T]{timedOut: timedOut, pool: pool}
		x.reply.Store(reply)
		x.timer = time.AfterFunc(d, x.fire)
		return x
	}

	x.reply.Store(reply)
	x.timer.Reset(d)
	return x
}

// stop disarms the timer and reports whether it did so before the timer
// fired. A timer stopped before it fired goes back to its pool, and its reply
// channel can safely be reused. One that fired, or is firing, is left to the
// garbage collector: its fire may still be running and must not see the
// channel of the next request, which is why the caller must then abandon the
// channel too.
func (x *replyTimer[T]) stop() bool {
	stopped := x.timer.Stop()
	if stopped {
		x.pool.Put(x)
	}

	return stopped
}

// fire delivers the timed-out value into the request's reply channel unless
// a reply is already there.
func (x *replyTimer[T]) fire() {
	reply, _ := x.reply.Load().(chan T)
	select {
	case reply <- x.timedOut:
	default:
	}
}
