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
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/future"
	"github.com/tochemey/goakt/v3/log"
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
	pid         *grainPID
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
	// No error to report
	if gctx.synchronous {
		gctx.response <- nil
	}
	gctx.err <- nil
}

// Response sets the message response
func (gctx *GrainContext) Response(resp proto.Message) {
	gctx.response <- resp
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
	gctx.err <- errors.NewErrUnhandledMessage(fmt.Errorf("unhandled message type %s", msg.ProtoReflect().Descriptor().FullName()))
}

// AskActor sends a message to another actor by name and waits for a response.
//
// This method performs a synchronous request (Ask pattern) to the specified actor,
// using the provided message and timeout. It returns the response message or an error
// if the operation times out or fails.
//
// Example:
//
//	resp, err := ctx.AskActor("my-actor", &MyRequest{}, 2*time.Second)
//	if err != nil {
//	    // handle error
//	}
func (gctx *GrainContext) AskActor(actorName string, message proto.Message, timeout time.Duration) (proto.Message, error) {
	ctx := context.WithoutCancel(gctx.Context())
	return gctx.actorSystem.NoSender().SendSync(ctx, actorName, message, timeout)
}

// TellActor sends a message to another actor by name without waiting for a response.
//
// This method performs an asynchronous send (Tell pattern) to the specified actor.
// It returns an error if the message could not be delivered.
//
// Example:
//
//	err := ctx.TellActor("my-actor", &MyNotification{})
//	if err != nil {
//	    // handle error
//	}
func (gctx *GrainContext) TellActor(actorName string, message proto.Message) error {
	ctx := context.WithoutCancel(gctx.Context())
	return gctx.actorSystem.NoSender().SendAsync(ctx, actorName, message)
}

// AskGrain sends a message to another Grain and waits for a response.
//
// This method performs a synchronous request (Ask pattern) to the specified Grain,
// using the provided message and timeout. It returns the response message or an error
// if the operation times out or fails.
//
// Example:
//
//	resp, err := ctx.AskGrain(otherGrainID, &MyRequest{}, 2*time.Second)
//	if err != nil {
//	    // handle error
//	}
func (gctx *GrainContext) AskGrain(to *GrainIdentity, message proto.Message, timeout time.Duration) (proto.Message, error) {
	ctx := context.WithoutCancel(gctx.Context())
	return gctx.actorSystem.AskGrain(ctx, to, message, timeout)
}

// TellGrain sends a message to another Grain without waiting for a response.
//
// This method performs an asynchronous send (Tell pattern) to the specified Grain.
// It returns an error if the message could not be delivered.
//
// Example:
//
//	err := ctx.TellGrain(otherGrainID, &MyNotification{})
//	if err != nil {
//	    // handle error
//	}
func (gctx *GrainContext) TellGrain(to *GrainIdentity, message proto.Message) error {
	ctx := context.WithoutCancel(gctx.Context())
	return gctx.actorSystem.TellGrain(ctx, to, message)
}

// PipeToGrain runs a task asynchronously and delivers its result to the target Grain.
//
// While the task is executing, the calling Grain can continue processing other messages.
// On success, the task result is sent to the target Grain as a normal message.
// On failure, a StatusFailure message containing the error is delivered to the target Grain.
//
// The task runs outside the Grain's message loop. Avoid mutating Grain state inside
// the task; communicate results via the returned message instead.
//
// Use PipeOptions (e.g., WithTimeout, WithCircuitBreaker) to control execution.
//
// Returns an error when the task is nil or the target identity is invalid.
func (gctx *GrainContext) PipeToGrain(to *GrainIdentity, task func() (proto.Message, error), opts ...PipeOption) error {
	if task == nil {
		return errors.ErrUndefinedTask
	}

	if to == nil {
		return errors.ErrInvalidGrainIdentity
	}

	if err := to.Validate(); err != nil {
		return errors.NewErrInvalidGrainIdentity(err)
	}

	ctx := context.WithoutCancel(gctx.Context())
	var config *pipeConfig
	if len(opts) > 0 {
		config = newPipeConfig(opts...)
	}

	go handleGrainCompletion(ctx, gctx.actorSystem, config, &grainTaskCompletion{
		Target: to,
		Task:   task,
	})

	return nil
}

// PipeToActor runs a task asynchronously and delivers its result to the named actor.
//
// While the task is executing, the calling Grain can continue processing other messages.
// On success, the task result is sent to the target actor as a normal message.
// On failure, the error is handled according to PipeTo semantics (e.g., deadletter).
//
// The task runs outside the Grain's message loop. Avoid mutating Grain state inside
// the task; communicate results via the returned message instead.
//
// Use PipeOptions (e.g., WithTimeout, WithCircuitBreaker) to control execution.
func (gctx *GrainContext) PipeToActor(actorName string, task func() (proto.Message, error), opts ...PipeOption) error {
	ctx := context.WithoutCancel(gctx.Context())
	return gctx.actorSystem.NoSender().PipeToName(ctx, actorName, task, opts...)
}

// PipeToSelf runs a task asynchronously and delivers its result to this Grain.
//
// While the task is executing, the calling Grain can continue processing other messages.
// On success, the task result is sent to this Grain as a normal message.
// On failure, a StatusFailure message containing the error is delivered to this Grain.
//
// The task runs outside the Grain's message loop. Avoid mutating Grain state inside
// the task; communicate results via the returned message instead.
//
// Use PipeOptions (e.g., WithTimeout, WithCircuitBreaker) to control execution.
func (gctx *GrainContext) PipeToSelf(task func() (proto.Message, error), opts ...PipeOption) error {
	return gctx.PipeToGrain(gctx.Self(), task, opts...)
}

// GrainIdentity creates or retrieves a unique identity for a Grain instance.
//
// This method is used to generate a GrainIdentity for a given grain name and factory,
// optionally applying additional GrainOptions. It is typically used when you need to
// reference or interact with another grain from within a grain's logic.
//
// Arguments:
//   - name: The unique name of the grain type or instance.
//   - factory: The GrainFactory used to instantiate the grain if it does not already exist.
//   - opts: Optional GrainOption values to customize grain creation or configuration.
//
// Returns:
//   - *GrainIdentity: The unique identity representing the target grain.
//   - error: Non-nil if the identity could not be created or resolved.
//
// Example:
//
//	id, err := ctx.GrainIdentity("my-grain", MyGrainFactory)
//	if err != nil {
//	    // handle error
//	}
//	err = ctx.TellGrain(id, &MyMessage{})
func (gctx *GrainContext) GrainIdentity(name string, factory GrainFactory, opts ...GrainOption) (*GrainIdentity, error) {
	ctx := context.WithoutCancel(gctx.Context())
	return gctx.actorSystem.GrainIdentity(ctx, name, factory, opts...)
}

// Dependencies returns a slice containing all dependencies currently registered
// within the Grain's local context.
//
// These dependencies are typically injected at grain initialization (via GrainOptions)
// and can include services, repositories, or other components the grain relies on.
//
// Returns:
//   - []extension.Dependency: All registered dependencies in the Grain's context.
func (gctx *GrainContext) Dependencies() []extension.Dependency {
	return gctx.pid.dependencies.Values()
}

// Dependency retrieves a specific dependency registered in the Grain's context by its unique ID.
//
// This allows grains to access shared functionality injected into their context,
// such as services, repositories, or application components.
//
// Example:
//
//	db := x.Dependency("database").(DatabaseService)
//
// Parameters:
//   - dependencyID: A unique string identifier used when the dependency was registered.
//
// Returns:
//   - extension.Dependency: The corresponding dependency if found, or nil otherwise.
func (gctx *GrainContext) Dependency(dependencyID string) extension.Dependency {
	if dependency, ok := gctx.pid.dependencies.Get(dependencyID); ok {
		return dependency
	}
	return nil
}

// Extensions returns a slice of all extensions registered within the ActorSystem
// associated with the GrainContext.
//
//	This allows system-level introspection or iteration over all available extensions.
//
// It can be useful for message processing.
//
// Returns:
//   - []extension.Extension: All registered extensions in the ActorSystem.
func (gctx *GrainContext) Extensions() []extension.Extension {
	return gctx.ActorSystem().Extensions()
}

// Extension retrieves a specific extension registered in the ActorSystem by its unique ID.
//
// This allows grains to access shared functionality injected into the system, such as
// event sourcing, metrics, tracing, or custom application services, directly from the GrainContext.
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

type grainPipeSystem interface {
	TellGrain(ctx context.Context, identity *GrainIdentity, message proto.Message) error
	Logger() log.Logger
}

type grainTaskCompletion struct {
	Target *GrainIdentity
	Task   func() (proto.Message, error)
}

// handleGrainCompletion processes a long-running task and delivers its result to a Grain.
func handleGrainCompletion(ctx context.Context, system grainPipeSystem, config *pipeConfig, completion *grainTaskCompletion) error {
	// apply timeout if provided
	var cancel context.CancelFunc
	if config != nil && config.timeout != nil {
		ctx, cancel = context.WithTimeout(ctx, *config.timeout)
		defer cancel()
	}

	// wrap the provided completion task into a future
	fut := future.New(completion.Task)

	// execute the task, optionally via circuit breaker
	runTask := func() (proto.Message, error) {
		if config != nil && config.circuitBreaker != nil {
			outcome, err := config.circuitBreaker.Execute(ctx, func(ctx context.Context) (any, error) {
				return fut.Await(ctx)
			})
			if err != nil {
				return nil, err
			}
			// no need to check the type since the future.Await returns proto.Message
			// if there is no error
			return outcome.(proto.Message), nil
		}
		return fut.Await(ctx)
	}

	result, err := runTask()
	if err != nil {
		failure := &goaktpb.StatusFailure{Error: err.Error()}
		if sendErr := system.TellGrain(ctx, completion.Target, failure); sendErr != nil {
			system.Logger().Error(sendErr)
			return sendErr
		}
		return nil
	}

	if sendErr := system.TellGrain(ctx, completion.Target, result); sendErr != nil {
		system.Logger().Error(sendErr)
		return sendErr
	}

	return nil
}

// build sets the necessary fields of GrainContext.
func (gctx *GrainContext) build(ctx context.Context, pid *grainPID, actorSystem ActorSystem, to *GrainIdentity, message proto.Message, synchronous bool) *GrainContext {
	gctx.self = to
	gctx.message = message
	gctx.ctx = ctx
	gctx.actorSystem = actorSystem
	gctx.err = getErrorChannel()
	gctx.synchronous = synchronous
	gctx.pid = pid

	if synchronous {
		gctx.response = getResponseChannel()
	}

	return gctx
}

// reset resets the fields of GrainContext.
func (gctx *GrainContext) reset() {
	var id *GrainIdentity
	gctx.message = nil
	gctx.self = id
	gctx.err = nil
	gctx.response = nil
	gctx.pid = nil
}
