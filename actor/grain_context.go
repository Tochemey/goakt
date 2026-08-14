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
	"time"

	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/future"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
)

// grainContextMode selects which reply channels build attaches to a GrainContext.
type grainContextMode uint8

const (
	// grainTell carries only the error channel: the caller blocks for an ack.
	grainTell grainContextMode = iota
	// grainAsk carries the response and error channels: the caller blocks for a reply.
	grainAsk
	// grainEnvelope carries no channels: replies to envelope messages are
	// routed by the system from the envelope metadata, so nothing can block on
	// the context and nothing needs draining before it recycles.
	grainEnvelope
)

// grainReplyError wraps a handler-reported failure for transport on the ask
// reply channel. Successes and failures share that single channel; the
// wrapper keeps a failure distinguishable from a response payload that
// happens to implement error.
type grainReplyError struct {
	err error
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
	// next is the intrusive mailbox link. Accessed atomically by the
	// producer (Enqueue) and the single consumer (Dequeue). Kept as the
	// first field to isolate its cache line from the cold fields below
	// under heavy producer contention.
	next           atomic.Pointer[GrainContext]
	ctx            context.Context
	self           *GrainIdentity
	actorSystem    ActorSystem
	message        any
	response       chan any
	err            chan error
	synchronous    bool
	pid            *grainPID
	responseClosed atomic.Bool

	// requestID and requestReplyTo carry async request metadata stamped by
	// handleAsyncRequest: the correlation ID of the envelope being processed
	// and the requester awaiting the reply (nil when a blocked ask on this
	// node awaits it). Zero for ordinary messages.
	requestID      string
	requestReplyTo *commands.AsyncReplyTo

	// replyDeferred records that DeferResponse transferred reply ownership to
	// a handle: the turn's own reply methods become no-ops. replySent makes
	// the async reply one-shot, mirroring the CAS guard of the channel path.
	// Both are turn-owned and need no synchronization.
	replyDeferred bool
	replySent     bool

	// poolShard is the home shard of this context in grainContextPool:
	// recycling always returns it to that ring. Stamped by the pool on
	// first allocation and preserved across reset so a context keeps its
	// home for its whole lifetime.
	poolShard uint32
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
func (gctx *GrainContext) Message() any {
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
	if gctx.requestID != "" {
		gctx.sendAsyncReply(nil, err)
		return
	}

	if gctx.synchronous {
		gctx.sendReply(grainReplyError{err: err})
		return
	}

	if gctx.err == nil {
		return
	}
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
	// Envelope path: success without a payload travels as an empty response.
	if gctx.requestID != "" {
		gctx.sendAsyncReply(nil, nil)
		return
	}

	if gctx.synchronous {
		// Ask path: success without a payload is a nil reply.
		gctx.sendReply(nil)
		return
	}

	if gctx.err == nil {
		return
	}
	// Asynchronous (Tell) path: only the error channel is used.
	gctx.err <- nil
}

// Response sets the message response
func (gctx *GrainContext) Response(resp any) {
	if gctx.requestID != "" {
		gctx.sendAsyncReply(resp, nil)
		return
	}

	gctx.sendReply(resp)
}

// sendReply delivers value on the ask reply channel exactly once. The
// responseClosed CAS guards against late replies after the caller timed out:
// a stale reply is dropped here instead of landing in a channel a later ask
// could observe. The non-blocking send tolerates a caller that is gone.
func (gctx *GrainContext) sendReply(value any) {
	if !gctx.responseClosed.CompareAndSwap(false, true) {
		return
	}

	select {
	case gctx.response <- value:
	default:
	}
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
	failure := errors.NewErrUnhandledMessage(fmt.Errorf("unhandled message type %T", gctx.Message()))

	if gctx.requestID != "" {
		gctx.sendAsyncReply(nil, failure)
		return
	}

	if gctx.synchronous {
		gctx.sendReply(grainReplyError{err: failure})
		return
	}

	if gctx.err == nil {
		return
	}
	gctx.err <- failure
}

// CorrelationID returns the correlation ID of the async request being
// processed, or the empty string when the current message is not an async
// request.
func (gctx *GrainContext) CorrelationID() string {
	return gctx.requestID
}

// DeferResponse transfers ownership of the current async request's reply from
// this turn to the returned handle. After the call, the context's own
// Response, Err, NoErr and Unhandled become no-ops for this message; exactly
// one reply owner exists at any time.
//
// The handle stays valid indefinitely: it holds only the reply metadata, never
// the context, so it can complete the request from a continuation or a later
// turn long after this context has been recycled. Completing the handle more
// than once is a no-op.
//
// For a message that did not arrive as an async request (a Tell, an Ask
// against a grain without reentrancy, or a timer tick) there is nothing to
// defer: DeferResponse returns nil and the reply belongs to the turn as usual.
// The returned nil handle is safe to complete; the calls do nothing.
func (gctx *GrainContext) DeferResponse() *GrainReply {
	if gctx.requestID == "" {
		if logger := gctx.actorSystem.Logger(); logger.Enabled(log.DebugLevel) {
			logger.Debugf("grain=%s DeferResponse ignored: message is not an async request", gctx.self.String())
		}
		return nil
	}

	gctx.replyDeferred = true

	return &GrainReply{
		system:        gctx.actorSystem,
		replyTo:       gctx.requestReplyTo,
		correlationID: gctx.requestID,
	}
}

// sendAsyncReply routes the turn's reply to whoever awaits the async request
// currently being processed. It is one-shot and yields entirely once
// DeferResponse has transferred reply ownership. Delivery failures are logged
// at debug: the requester's timeout is the authoritative failure signal.
func (gctx *GrainContext) sendAsyncReply(message any, failure error) {
	if gctx.replyDeferred || gctx.replySent {
		return
	}
	gctx.replySent = true

	if err := gctx.actorSystem.routeAsyncReply(context.WithoutCancel(gctx.ctx), nil, gctx.requestReplyTo, gctx.requestID, message, failure); err != nil {
		if logger := gctx.actorSystem.Logger(); logger.Enabled(log.DebugLevel) {
			logger.Debugf("grain=%s failed to deliver reply for correlation id=%s: %v", gctx.self.String(), gctx.requestID, err)
		}
	}
}

// EnableReentrancy enables or retunes the grain's async request policy at
// runtime, from inside a message handler. It is the runtime counterpart of
// the WithGrainReentrancy activation option, for grains activated without
// reentrancy because the capability is only needed for a particular case.
//
// It takes effect for requests issued from this point on; requests already in
// flight keep the mode they were admitted with. Asks against the grain switch
// to the envelope path, which also makes DeferResponse available for messages
// received afterward. Returns an error when the configuration is nil or
// invalid.
func (gctx *GrainContext) EnableReentrancy(config *reentrancy.Reentrancy) error {
	return gctx.pid.enableReentrancy(config)
}

// DisableReentrancy restores the grain's default request policy to Off at
// runtime. Requests already in flight complete normally, a paused grain still
// unpauses, asks revert to the legacy channel path, and new Request calls
// fail with ErrReentrancyDisabled until a later EnableReentrancy. As with a
// policy configured Off at activation, a per-call WithReentrancyMode override
// still admits an individual request. A no-op when reentrancy was never
// enabled.
func (gctx *GrainContext) DisableReentrancy() {
	gctx.pid.disableReentrancy()
}

// RequestGrain sends an asynchronous request to another Grain and returns a
// RequestCall without blocking the current turn.
//
// The grain keeps processing according to its reentrancy mode while the
// request is in flight, which is what lets request cycles (A asks B while B
// asks A back) complete instead of deadlocking until a timeout. Register a
// continuation with Then on the returned call; it runs on this grain's turn
// when the response arrives, so grain state stays single-threaded.
//
// The calling grain must be configured with reentrancy; otherwise the
// returned call is already completed with ErrReentrancyDisabled. Failures
// never abort the current message: they surface through the returned call's
// Then continuation, immediately when the failure is known at call time.
//
// Requests default to DefaultGrainRequestTimeout; use WithRequestTimeout to
// change it per call (a non-positive value disables the timeout) and
// WithReentrancyMode to override the grain's mode per call.
func (gctx *GrainContext) RequestGrain(to *GrainIdentity, message any, opts ...RequestOption) RequestCall {
	pid := gctx.pid
	if !pid.isActive() {
		return completedRequestCall(errors.ErrDead)
	}

	if message == nil {
		return completedRequestCall(errors.ErrInvalidMessage)
	}

	reentrant := pid.reentrancy.Load()
	if reentrant == nil {
		return completedRequestCall(errors.ErrReentrancyDisabled)
	}

	if to == nil {
		return completedRequestCall(errors.ErrInvalidGrainIdentity)
	}

	if err := to.Validate(); err != nil {
		return completedRequestCall(errors.NewErrInvalidGrainIdentity(err))
	}

	state, config, err := pid.admitRequest(reentrant, opts...)
	if err != nil {
		return completedRequestCall(err)
	}

	envelope := &commands.AsyncRequest{
		CorrelationID: state.id,
		ReplyTo:       &commands.AsyncReplyTo{Kind: commands.ReplyToGrain, Grain: gctx.Self().String()},
		Message:       message,
	}

	if err := gctx.actorSystem.deliverAsyncEnvelope(context.WithoutCancel(gctx.ctx), to, envelope); err != nil {
		pid.deregisterRequestState(state)
		return completedRequestCall(err)
	}

	if timeout := config.grainTimeout(); timeout > 0 {
		state.startTimeout(timeout)
	}

	return &requestHandle{state: state}
}

// RequestActor sends an asynchronous request to a named actor and returns a
// RequestCall without blocking the current turn.
//
// It is the actor-targeted counterpart of RequestGrain and follows the same
// rules: the calling grain must have reentrancy enabled, failures surface
// through the returned call, continuations run on this grain's turn, and the
// request defaults to DefaultGrainRequestTimeout. The target actor replies
// with Response exactly as it does for actor-to-actor requests.
func (gctx *GrainContext) RequestActor(actorName string, message any, opts ...RequestOption) RequestCall {
	pid := gctx.pid
	if !pid.isActive() {
		return completedRequestCall(errors.ErrDead)
	}

	if message == nil {
		return completedRequestCall(errors.ErrInvalidMessage)
	}

	reentrant := pid.reentrancy.Load()
	if reentrant == nil {
		return completedRequestCall(errors.ErrReentrancyDisabled)
	}

	ctx := context.WithoutCancel(gctx.ctx)

	cid, err := gctx.actorSystem.ActorOf(ctx, actorName)
	if err != nil {
		return completedRequestCall(err)
	}

	state, config, err := pid.admitRequest(reentrant, opts...)
	if err != nil {
		return completedRequestCall(err)
	}

	envelope := &commands.AsyncRequest{
		CorrelationID: state.id,
		ReplyTo:       &commands.AsyncReplyTo{Kind: commands.ReplyToGrain, Grain: gctx.Self().String()},
		Message:       message,
	}

	// Tell routes to the network itself when ActorOf resolved a remote handle.
	if err := gctx.actorSystem.NoSender().Tell(ctx, cid, envelope); err != nil {
		pid.deregisterRequestState(state)
		return completedRequestCall(err)
	}

	if timeout := config.grainTimeout(); timeout > 0 {
		state.startTimeout(timeout)
	}

	return &requestHandle{state: state}
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
func (gctx *GrainContext) AskActor(actorName string, message any, timeout time.Duration) (any, error) {
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
func (gctx *GrainContext) TellActor(actorName string, message any) error {
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
func (gctx *GrainContext) AskGrain(to *GrainIdentity, message any, timeout time.Duration) (any, error) {
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
func (gctx *GrainContext) TellGrain(to *GrainIdentity, message any) error {
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
func (gctx *GrainContext) PipeToGrain(to *GrainIdentity, task func() (any, error), opts ...PipeOption) error {
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
func (gctx *GrainContext) PipeToActor(actorName string, task func() (any, error), opts ...PipeOption) error {
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
func (gctx *GrainContext) PipeToSelf(task func() (any, error), opts ...PipeOption) error {
	return gctx.PipeToGrain(gctx.Self(), task, opts...)
}

// ScheduleOnce registers a volatile timer that delivers message to this Grain
// exactly once after delay. A non-positive delay fires immediately.
//
// The tick is delivered through the Grain's mailbox and processed like any other
// message: it is serialized with the Grain's other messages and OnReceive sees
// the scheduled message via Message(). Tick handlers run with a background
// context (no deadline, no cancellation), and an error reported via Err is
// logged, since no caller is waiting for a reply.
//
// Grain timers are activation-scoped: they are all cancelled when the Grain
// deactivates, they are never persisted, and they never reactivate a passivated
// Grain. A tick does not reset the Grain's passivation clock unless the timer is
// registered with WithTimerKeepAlive.
//
// It returns the timer's reference, auto-generated unless WithTimerReference is
// used. References are scoped to this Grain; registering a timer under a
// reference already in use cancels and replaces the existing timer. Use
// CancelSchedule to stop the timer before it fires.
//
// Returns ErrGrainTimersStopped when the Grain is deactivating.
func (gctx *GrainContext) ScheduleOnce(message any, delay time.Duration, opts ...GrainTimerOption) (string, error) {
	registry, err := gctx.pid.timerRegistry()
	if err != nil {
		return "", err
	}
	return registry.scheduleOnce(message, delay, opts...)
}

// Schedule registers a volatile timer that delivers message to this Grain
// repeatedly at the given fixed interval, with the first tick after one
// interval.
//
// The cadence is fixed, matching ActorSystem.Schedule: ticks of a handler
// slower than the interval queue up in the mailbox, while execution itself
// never overlaps because the Grain is single-threaded. The timer fires until
// cancelled or the Grain deactivates.
//
// See ScheduleOnce for the timer semantics shared by all Grain timers.
//
// Returns ErrInvalidTimerInterval when interval is not strictly positive, and
// ErrGrainTimersStopped when the Grain is deactivating.
func (gctx *GrainContext) Schedule(message any, interval time.Duration, opts ...GrainTimerOption) (string, error) {
	registry, err := gctx.pid.timerRegistry()
	if err != nil {
		return "", err
	}
	return registry.scheduleInterval(message, interval, opts...)
}

// ScheduleWithCron registers a volatile timer that delivers message to this
// Grain at the instants described by cronExpression.
//
// The expression is evaluated in the process's local timezone. Unlike
// ActorSystem.ScheduleWithCron, no cluster-wide arbitration or explicit
// reference is required: a Grain has exactly one activation cluster-wide, so
// each tick fires exactly once by construction.
//
// See ScheduleOnce for the timer semantics shared by all Grain timers.
//
// Returns the cron parsing error for an invalid expression, and
// ErrGrainTimersStopped when the Grain is deactivating.
func (gctx *GrainContext) ScheduleWithCron(message any, cronExpression string, opts ...GrainTimerOption) (string, error) {
	registry, err := gctx.pid.timerRegistry()
	if err != nil {
		return "", err
	}
	return registry.scheduleCron(message, cronExpression, opts...)
}

// CancelSchedule cancels the Grain timer registered under reference.
//
// A tick of the cancelled timer already sitting in the mailbox is dropped
// instead of being delivered. A one-shot timer that already fired has left the
// registry, so cancelling it returns ErrScheduledReferenceNotFound, matching
// ActorSystem.CancelSchedule.
//
// Returns ErrScheduledReferenceNotFound for an unknown reference, and
// ErrGrainTimersStopped when the Grain is deactivating.
func (gctx *GrainContext) CancelSchedule(reference string) error {
	registry, err := gctx.pid.timerRegistry()
	if err != nil {
		return err
	}
	return registry.cancel(reference)
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
//
// Deprecated: use the package-level GrainOf function instead, for example:
//
//	id, err := actor.GrainOf[*MyGrain](context.WithoutCancel(gctx.Context()), gctx.ActorSystem(), name, opts...)
//
// GrainOf derives the grain kind from its type parameter and requires no factory.
// The factory-based path remains functional but will be removed in a future major release.
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
	TellGrain(ctx context.Context, identity *GrainIdentity, message any) error
	Logger() log.Logger
}

type grainTaskCompletion struct {
	Target *GrainIdentity
	Task   func() (any, error)
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
	runTask := func() (any, error) {
		if config != nil && config.circuitBreaker != nil {
			outcome, err := config.circuitBreaker.Execute(ctx, func(ctx context.Context) (any, error) {
				return fut.Await(ctx)
			})
			if err != nil {
				return nil, err
			}
			// no need to check the type since the future.Await returns proto.Message
			// if there is no error
			return outcome, nil
		}
		return fut.Await(ctx)
	}

	result, err := runTask()
	if err != nil {
		failure := NewStatusFailure(err.Error(), nil)
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
func (gctx *GrainContext) build(ctx context.Context, pid *grainPID, actorSystem ActorSystem, to *GrainIdentity, message any, mode grainContextMode) *GrainContext {
	gctx.self = to
	gctx.message = message
	gctx.ctx = ctx
	gctx.actorSystem = actorSystem
	gctx.synchronous = mode == grainAsk
	gctx.pid = pid
	gctx.requestID = ""
	gctx.requestReplyTo = nil
	gctx.replyDeferred = false
	gctx.replySent = false

	// Reset CAS guard so Response()/NoErr() succeed for the new message.
	gctx.responseClosed.Store(false)

	switch mode {
	case grainAsk:
		// Ask replies, success and failure alike, travel on the single
		// response channel (see sendReply); no error channel is attached.
		gctx.err = nil
		gctx.response = getResponseChannel()
	case grainTell:
		gctx.err = getGrainErrorChannel(gctx.poolShard)
		gctx.response = nil
	default:
		gctx.err = nil
		gctx.response = nil
	}

	return gctx
}

// reset clears all fields so the GrainContext can be returned to the pool.
// Nil-ing pointer / interface fields breaks reference chains and lets the GC
// collect the objects they pointed to while the context sits idle in the pool.
func (gctx *GrainContext) reset() {
	gctx.ctx = nil
	gctx.self = nil
	gctx.actorSystem = nil
	gctx.message = nil
	gctx.response = nil
	gctx.err = nil
	gctx.synchronous = false
	gctx.pid = nil
	gctx.requestID = ""
	gctx.requestReplyTo = nil
	gctx.replyDeferred = false
	gctx.replySent = false
	// Note: responseClosed is not reset here because build() always sets it
	// to false for the next message. Avoiding this atomic store saves ~5ns
	// per message on the release path.
}
