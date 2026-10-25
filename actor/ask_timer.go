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
	"sync"
	"sync/atomic"
	"time"
)

// askTimedOut is the value an askTimer delivers into an Ask's reply channel
// when the Ask times out. It is unexported, so no handler can respond with it,
// and an Ask that receives it knows its timeout fired before any reply.
type askTimedOut struct{}

// askTimer times out one actor Ask by delivering askTimedOut into the Ask's
// own reply channel, so the asker waits on that channel and its context only.
//
// Blocking in a select that includes a timer channel makes the runtime arm and
// disarm that timer around every park of the asker, which measured about 180 ns
// per round trip on an 8-core arm64 machine, a fifth of an Ask. A timer that
// runs a function when it expires has no channel to park on. The reply channel
// takes the timeout safely because it is created for one Ask, never reused,
// and written only with non-blocking sends (see ReceiveContext.Response):
// whichever of the reply and the timeout comes first is what the asker reads,
// and the other is dropped with the channel.
type askTimer struct {
	// timer runs fire when the Ask times out. It is created once per askTimer
	// and rearmed with Reset when the askTimer is reused from the pool.
	timer *time.Timer
	// reply holds the reply channel of the Ask being timed. It is atomic
	// because fire reads it on the timer's goroutine.
	reply atomic.Value
}

// askTimers recycles askTimers whose timer was stopped before it fired.
var askTimers sync.Pool

// startAskTimer arms a timeout of d for the Ask whose reply channel is reply.
// The caller must stop it once the Ask is answered or abandoned.
func startAskTimer(reply chan any, d time.Duration) *askTimer {
	x, _ := askTimers.Get().(*askTimer)
	if x == nil {
		x = new(askTimer)
		x.reply.Store(reply)
		x.timer = time.AfterFunc(d, x.fire)
		return x
	}

	x.reply.Store(reply)
	x.timer.Reset(d)
	return x
}

// stop disarms the timer. A timer stopped before it fired goes back to the
// pool. One that fired, or is firing, is left to the garbage collector: its
// fire may still be running and must not see the channel of the next Ask.
func (x *askTimer) stop() {
	if x.timer.Stop() {
		askTimers.Put(x)
	}
}

// fire delivers askTimedOut into the Ask's reply channel unless a reply is
// already there.
func (x *askTimer) fire() {
	reply, _ := x.reply.Load().(chan any)
	select {
	case reply <- askTimedOut{}:
	default:
	}
}
