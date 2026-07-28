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

	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/log"
)

// GrainReply is the write end of an async request whose reply was
// deferred past the turn that received it, obtained from
// [GrainContext.DeferResponse].
//
// It holds only the reply metadata of the request, never the context that
// carried it, so it can complete the request from a request continuation or a
// later turn long after the original context has been recycled. The reply is
// routed by the system to whoever awaits the request: a blocked ask, an actor,
// or another grain, on this node or across the wire.
//
// A GrainReply is one-shot: the first of Response, Err or NoErr wins and
// every later call is a no-op. All methods are safe on a nil receiver, so code
// holding the result of DeferResponse for an ordinary message can complete it
// unconditionally.
type GrainReply struct {
	system        ActorSystem
	replyTo       *commands.AsyncReplyTo
	correlationID string
	done          atomic.Bool
}

// Response completes the deferred request successfully with the given message.
func (x *GrainReply) Response(message any) {
	x.complete(message, nil)
}

// Err completes the deferred request with a failure.
func (x *GrainReply) Err(err error) {
	x.complete(nil, err)
}

// NoErr completes the deferred request successfully without a payload. The
// requester observes a nil result and a nil error.
func (x *GrainReply) NoErr() {
	x.complete(nil, nil)
}

// complete routes the reply exactly once. Delivery failures are logged at
// debug: the requester's timeout is the authoritative failure signal.
func (x *GrainReply) complete(message any, failure error) {
	if x == nil || !x.done.CompareAndSwap(false, true) {
		return
	}

	if err := x.system.routeAsyncReply(context.Background(), nil, x.replyTo, x.correlationID, message, failure); err != nil {
		if logger := x.system.Logger(); logger.Enabled(log.DebugLevel) {
			logger.Debugf("deferred reply dropped for correlation id=%s: %v", x.correlationID, err)
		}
	}
}
