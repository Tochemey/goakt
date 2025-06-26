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

	"github.com/tochemey/goakt/v3/extension"
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

// Extensions returns a slice of all extensions registered within the ActorSystem
// associated with the GrainContext.
//
// This allows system-level introspection or iteration over all available extensions.
// It can be useful for message processing.
//
// Returns:
//   - []extension.Extension: All registered extensions in the ActorSystem.
func (gctx *GrainContext) Extensions() []extension.Extension {
	return gctx.ActorSystem().Extensions()
}

// Extension retrieves a specific extension registered in the ActorSystem by its unique ID.
//
// This allows actors to access shared functionality injected into the system, such as
// event sourcing, metrics, tracing, or custom application services, directly from the Context.
//
// Example:
//
//	logger := x.Extension("extensionID").(MyExtension)
//
// Parameters:
//   - extensionID: A unique string identifier used when the extension was registered.
//
// Returns:
//   - extension.Extension: The corresponding extension if found, or nil otherwise.
func (gctx *GrainContext) Extension(extensionID string) extension.Extension {
	return gctx.ActorSystem().Extension(extensionID)
}

// Message returns the message being processed by the Grain.
// This is the message that triggered the current grain invocation.
func (gctx *GrainContext) Message() proto.Message {
	return gctx.message
}

// Err is used instead of panicking within a message handler.
func (gctx *GrainContext) Err(err error) {
	gctx.err <- err
	close(gctx.err)
}

// NoErr is used to indicate that there is no error to report.
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

// Unhandled is called when the message is not handled by the Grain.
// This method can be used to log the unhandled message or take other actions.
// It is typically invoked when the Grain does not have a handler for the received message type.
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
