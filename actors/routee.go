/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actors

import (
	"context"

	"github.com/tochemey/goakt/goaktpb"
	"google.golang.org/protobuf/proto"
)

// routee is a special actor that handles message from a router.
// a routee handles a specific message type
type routee[M proto.Message] struct {
	router  PID
	handler func(ctx context.Context, message M) error
}

// enforce compilation error
var _ Actor = (*routee)(nil)

// newRoutee creates an instance of routee
func newRoutee[M proto.Message](handler func(ctx context.Context, message M) error) *routee[M] {
	return &routee[M]{handler: handler}
}

// PreStart handles the preStart routines.
// For a routee there is no preStart routine
func (r *routee[M]) PreStart(context.Context) error {
	return nil
}

// Receive handles the message received from the router
func (r *routee[M]) Receive(ctx ReceiveContext) {
	switch m := ctx.Message().(type) {
	case *goaktpb.PostStart:
		// a routee only has one watcher
		watcher := ctx.Self().watchers().Get(0).(*watcher)
		// set the router ID
		r.router = watcher.WatcherID
	case M:
		// handle the message and return the error
		if err := r.handler(ctx.Context(), m); err != nil {
			ctx.Err(err)
		}
	default:
		// discard any other message
		ctx.Unhandled()
	}
}

// PostStop handles post stop routines
// A routee does not have any post stop routines
func (r *routee[M]) PostStop(context.Context) error {
	return nil
}

// syncRoutee is a special actor that handles message from a router.
// this type of routee processes the expected message and return the outcome
// back to its router
type syncRoutee[T proto.Message, R proto.Message] struct {
	router  PID
	handler func(ctx context.Context, message T) (response R, err error)
}

// enforce compilation error
var _ Actor = (*syncRoutee)(nil)

// newSyncRoutee creates an instance of syncRoutee
func newSyncRoutee[T proto.Message, R proto.Message](handler func(ctx context.Context, message T) (response R, err error)) *syncRoutee[T, R] {
	return &syncRoutee[T, R]{handler: handler}
}

// PreStart handles the preStart routines.
// For a routee there is no preStart routine
func (r *syncRoutee[T, R]) PreStart(context.Context) error {
	return nil
}

// Receive handles the message received from the router
func (r *syncRoutee[T, R]) Receive(ctx ReceiveContext) {
	switch m := ctx.Message().(type) {
	case *goaktpb.PostStart:
		// a routee only has one watcher
		watcher := ctx.Self().watchers().Get(0).(*watcher)
		// set the router ID
		r.router = watcher.WatcherID
	case T:
		// process the message as expected
		response, err := r.handler(ctx.Context(), m)
		// handle any eventual error
		if err != nil {
			ctx.Err(err)
		}
		// send the response to the router
		ctx.Tell(r.router, response)
	default:
		// discard any other message
		ctx.Unhandled()
	}
}

// PostStop handles post stop routines
// A routee does not have any post stop routines
func (r *syncRoutee[T, R]) PostStop(context.Context) error {
	return nil
}
