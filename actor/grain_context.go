/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package actor

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"
)

// pool holds a pool of ReceiveContext
var grainContextPool = sync.Pool{
	New: func() any {
		return new(GrainContext)
	},
}

// getContext retrieves a message from the pool
func getGrainContext() *GrainContext {
	return grainContextPool.Get().(*GrainContext)
}

// releaseContext sends the message context back to the pool
func releaseGrainContext(ctx *GrainContext) {
	ctx.reset()
	grainContextPool.Put(ctx)
}

// GrainContext provides contextual information and operations
// available to an actor when it is processing a message.
//
// It typically carries the incoming message, the grain's identity,
// the actor system managing the grain, and methods to respond to messages.
//
// Example usage:
//
//	func (g *MyGrain) OnReceive(ctx *actor.GrainContext) {
//	    msg := ctx.Message()
//	    switch msg := msg.(type) {
//	    case *MyMessage:
//	        ctx.Respond(&MyResponse{})
//	    }
//	}
type GrainContext struct {
	ctx         context.Context
	self        *GrainIdentity
	actorSystem ActorSystem
	message     proto.Message
	response    chan proto.Message
	err         chan error
	synchronous bool
}

// Context returns the underlying context associated with the GrainContext.
//
// It carries deadlines, cancellation signals, and request-scoped values.
// This method allows direct access to standard context operations.
func (gctx *GrainContext) Context() context.Context {
	return gctx.ctx
}

// Self returns the unique identifier of the Grain instance.
func (gctx *GrainContext) Self() *GrainIdentity {
	return gctx.self
}

// ActorSystem returns the ActorSystem that manages the Grain.
//
// It provides access to system-level services, configuration, and infrastructure components
// that the actor may interact with during its lifecycle.
func (gctx *GrainContext) ActorSystem() ActorSystem {
	return gctx.actorSystem
}

// Message returns the message currently being processed by the Grain.
//
// This method provides access to the incoming protobuf message that triggered the current Grain invocation.
// Use this to inspect, type-assert, or handle the message within your Grain's OnReceive method.
//
// Example:
//
//	func (g *MyGrain) OnReceive(ctx *GrainContext) {
//	    switch msg := ctx.Message().(type) {
//	    case *MyRequest:
//	        // handle MyRequest
//	    default:
//	        ctx.Unhandled()
//	    }
//	}
func (gctx *GrainContext) Message() proto.Message {
	return gctx.message
}

// Err reports an error encountered during message handling without panicking.
//
// This method should be used within a message handler to indicate that the
// message processing failed due to the provided error. It is the preferred way
// to signal failure in the message handler, as it avoids
// crashing the actor or goroutine.
//
// Use Err instead of panicking to enable graceful error handling and reporting,
// particularly when responding to messages or for observability purposes.
//
// Note: Even if the message handler does panic, the framework will catch it and
// report it as an error. However, using Err allows for more controlled error.
//
// Example usage:
//
//	func (a *MyActor) OnReceive(ctx actor.Context) {
//	    switch msg := ctx.Message().(type) {
//	    case DoSomething:
//	        if err := doWork(); err != nil {
//	            ctx.Err(err) // fail gracefully
//	            return
//	        }
//	        ctx.NoErr()
//	    }
//	}
func (gctx *GrainContext) Err(err error) {
	gctx.err <- err
	close(gctx.err)
}

// NoErr marks the successful completion of a message handler without any error.
//
// This method is typically used in actor-style messaging contexts to explicitly
// indicate that the message was processed successfully. It is especially useful
// when:
//   - Handling fire-and-forget (Tell-like) messages where no response is expected.
//   - Handling Ask-like messages where no error and response need to be returned.
//
// Calling NoErr ensures that the framework does not interpret the absence of an
// explicit error as a failure or require a default response.
//
// Example usage:
//
//	func (a *MyActor) OnReceive(ctx actor.Context) {
//	    switch msg := ctx.Message().(type) {
//	    case DoSomething:
//	        // Handle logic...
//	        ctx.NoErr() // explicitly declare success
//	    }
//	}
func (gctx *GrainContext) NoErr() {
	// No error to report, just close the channel
	if gctx.synchronous {
		close(gctx.response)
	}
	close(gctx.err)
}

// Response sets the message response
func (gctx *GrainContext) Response(resp proto.Message) {
	gctx.response <- resp
	close(gctx.response)
}

// Unhandled marks the currently received message as unhandled by the Grain.
//
// This method should be invoked when the Grain does not define a handler for the
// message type it has received. Calling Unhandled informs the runtime that the
// message was not processed and allows the framework to respond accordingly.
//
// This is typically used to log, track, or gracefully ignore unsupported messages
// without causing unexpected behavior.
//
// If Unhandled is called, the caller of TellGrain or AskGrain will receive an
// ErrUnhandledMessage error as a response, signaling that the message could not be processed.
//
// Example use case:
//
//	func (g *MyGrain) OnReceive(ctx *GrainContext) error {
//	    switch msg := ctx.Message().(type) {
//	    case *KnownMessage:
//	        // handle message
//	        return nil
//	    default:
//	        ctx.Unhandled()
//	        return nil
//	    }
//	}
func (gctx *GrainContext) Unhandled() {
	msg := gctx.Message()
	gctx.err <- NewErrUnhandledMessage(fmt.Errorf("unhandled message type %s", msg.ProtoReflect().Descriptor().FullName()))
	close(gctx.err)
}

// build sets the necessary fields of ReceiveContext
func (gctx *GrainContext) build(ctx context.Context, actorSystem ActorSystem, to *GrainIdentity, message proto.Message, synchronous bool) *GrainContext {
	gctx.self = to
	gctx.message = message
	gctx.ctx = ctx
	gctx.actorSystem = actorSystem
	gctx.err = make(chan error, 1)
	gctx.synchronous = synchronous

	if synchronous {
		gctx.response = make(chan proto.Message, 1)
	}

	return gctx
}

// reset resets the fields of ReceiveContext
func (gctx *GrainContext) reset() {
	var id *GrainIdentity
	gctx.message = nil
	gctx.self = id
	gctx.err = nil
	gctx.response = nil
}

func (gctx *GrainContext) getError() <-chan error {
	return gctx.err
}

func (gctx *GrainContext) getResponse() <-chan proto.Message {
	return gctx.response
}
