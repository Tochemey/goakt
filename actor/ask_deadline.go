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
	"math"
	"time"
)

// askClockStart is the moment askClock counts from.
var askClockStart = time.Now()

// askClock returns the time elapsed since askClockStart. It reads only the
// monotonic clock, which makes it cheaper than time.Now and immune to wall
// clock adjustments. Its readings only compare with one another, on this node.
func askClock() int64 {
	return int64(time.Since(askClockStart))
}

// askDeadline returns the moment the sender of an ask stops waiting, as an
// askClock reading: timeout from now, or the deadline of the sender's context
// when that comes first. Actors and grains stamp it on the message so the
// receiver can skip an ask nobody waits for. It is never zero, the value that
// stands for no deadline.
func askDeadline(ctx context.Context, timeout time.Duration) int64 {
	if callerDeadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(callerDeadline))
	}

	// a timeout too large to add to the clock, the way a caller asks to wait
	// forever, must not wrap around into a deadline that already passed
	now := askClock()
	if int64(timeout) > math.MaxInt64-now {
		return math.MaxInt64
	}

	return max(now+int64(timeout), 1)
}

// askExpired reports whether the sender of an ask stamped with deadline has
// stopped waiting. It reads the clock only for a message that carries a
// deadline.
func askExpired(deadline int64) bool {
	return deadline != 0 && askClock() > deadline
}

// untilAskDeadline returns the time left before the sender of an ask stamped
// with deadline stops waiting, negative once it has.
func untilAskDeadline(deadline int64) time.Duration {
	return time.Duration(deadline - askClock())
}

// askScope is the context derived for the handler of one ask, with the
// function that releases its timer. cancel is nil when the sender's context
// was kept as is.
type askScope struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// askContext returns the context a handler sees for an ask stamped with
// deadline, and the function that releases its timer. The context ends when
// the sender stops waiting. When ctx already ends by then it is returned as
// is, with a nil release function.
func askContext(ctx context.Context, deadline int64) (context.Context, context.CancelFunc) {
	ends := time.Now().Add(untilAskDeadline(deadline))
	if senderDeadline, ok := ctx.Deadline(); ok && !senderDeadline.After(ends) {
		return ctx, nil
	}

	return context.WithDeadline(ctx, ends)
}
