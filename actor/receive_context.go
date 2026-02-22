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
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/address"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/log"
)

// ReceiveContext carries per-message context and operations available to an actor
// while handling a single message. It exposes:
//   - Message metadata (Message, Sender, RemoteSender, SenderAddress, ReceiverAddress)
//   - Actor lifecycle and behavior management (Become, BecomeStacked, UnBecome, UnBecomeStacked,
//     Stash, Unstash, UnstashAll, Stop, Shutdown, Watch, UnWatch, Reinstate, ReinstateNamed)
//   - Messaging operations (Tell, Ask, Request/RequestName, BatchTell, BatchAsk, SendAsync, SendSync,
//     Forward, ForwardTo, RemoteTell/Ask/BatchTell/BatchAsk/Forward, RemoteLookup)
//   - Utilities (Context, Logger, Err, Response for Ask, PipeTo/PipeToName, ActorSystem access,
//     Extension/Extensions)
//
// Concurrency and lifecycle:
//   - A ReceiveContext instance is created by the runtime for each delivered message and is
//     only valid within the scope of handling that message. Do not retain it beyond the current
//     Receive call.
//   - Methods that send messages are non-blocking unless explicitly documented (e.g., Ask/SendSync).
//   - The underlying actor processes messages one-at-a-time, preserving mailbox order.
//
// Context propagation:
//   - ReceiveContext.Context() is the message-scoped context. Blocking calls invoked through
//     ReceiveContext use a non-cancelable derivative (context.WithoutCancel) so that finishing
//     work is not inadvertently canceled by the caller context. Use timeouts on synchronous calls.
//
// Message immutability:
//   - Message returns a proto.Message. Treat it as immutable; copy before mutation.
//
// Sender semantics:
//   - Sender() returns the PID of the message sender — local or remote.
//   - For remote messages, Sender() holds a lightweight remote PID created from
//     the sender address embedded in the wire message.
//   - Prefer SenderAddress() to get a location-transparent address abstraction.
//
// Examples:
//
//	func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
//	    switch msg := ctx.Message().(type) {
//	    case *Ping:
//	        ctx.Respond(&Pong{}) // Ask reply
//	    case *Work:
//	        ctx.PipeToName("worker", func() (proto.Message, error) {
//	            return doWork(msg), nil
//	        })
//	    default:
//	        ctx.Unhandled()
//	    }
//	}
type ReceiveContext struct {
	// inUse is an atomic flag to prevent concurrent build/reset operations.
	// This protects against sync.Pool races when a context is returned to the pool
	// while another goroutine is retrieving it. Only build() and reset() need to check this.
	inUse atomic.Bool
	// stashed indicates that this context has been placed into the actor's stash
	// buffer by the user's behavior (via ctx.Stash()). When set, the process loop
	// must NOT release this context back to the pool because the stash still holds
	// a live reference. The flag is cleared when the context is unstashed.
	stashed        atomic.Bool
	ctx            context.Context
	message        any
	sender         *PID
	response       chan any
	responseClosed atomic.Bool
	requestID      string
	requestReplyTo string
	self           *PID
	err            error
}

// Self returns the PID of the currently executing actor.
//
// The returned PID is the logical identity of the recipient processing the current
// message. It can be used to access actor-level facilities (ActorSystem, Logger, etc.).
// Self may be nil only in internal bootstrap flows; within Receive it is non-nil.
func (rctx *ReceiveContext) Self() *PID {
	return rctx.self
}

// Err records a non-fatal error observed during message handling.
//
// Use Err to report issues to the runtime without panicking. Supervisors or
// the actor system may log, escalate, or apply policies based on this error.
// Calling Err does not stop message processing immediately.
//
// Typical usage:
//
//	if err != nil {
//	    rctx.Err(err)
//	    return
//	}
func (rctx *ReceiveContext) Err(err error) {
	rctx.err = err
}

// Response publishes a reply to the sender of the current message.
//
// Design decision:
//   - If the message was initiated via Ask, Response completes that request.
//   - If the message was initiated via Request/RequestName, Response sends an async
//     reply to the stored correlation ID and reply address.
//   - If neither applies, Response is a no-op.
//
// Use Respond/Response exactly once per Ask-initiated message. Multiple calls may
// be ignored or override each other depending on the underlying channel semantics.
func (rctx *ReceiveContext) Response(resp any) {
	if rctx.response == nil {
		if rctx.requestID == "" {
			return
		}

		if err := rctx.self.sendAsyncResponse(rctx.withoutCancel(), rctx.requestReplyTo, rctx.requestID, resp, nil); err != nil {
			rctx.Err(err)
		}
		return
	}
	// For Ask-based replies, guard against late responses after the caller timed out.
	// This prevents pooled response channels from receiving stale replies that could
	// be consumed by a later Ask call.
	if !rctx.responseClosed.CompareAndSwap(false, true) {
		return
	}
	select {
	case rctx.response <- resp:
	default:
		// Channel is full or caller is gone; drop the response.
	}
}

// Context returns the context associated with the current message.
//
// This context is bound to the delivery of this message. Blocking calls dispatched
// via ReceiveContext typically derive a non-cancelable context to avoid accidental
// cancellation from the caller, so prefer using explicit timeouts on synchronous APIs.
//
// Do not store this context beyond the current Receive invocation.
func (rctx *ReceiveContext) Context() context.Context {
	return rctx.ctx
}

// Sender returns the PID of the message sender.
//
// For local messages this is the sending actor's PID. For messages that
// originated from a remote node, this is a lightweight remote PID constructed
// from the wire sender address. Returns NoSender when the sender is unknown
// or the message was system-generated.
func (rctx *ReceiveContext) Sender() *PID {
	return rctx.sender
}

// Message returns the protobuf message being processed.
//
// Treat the returned message as immutable. Use type assertions or pattern matching
// on the message type to implement your actor behavior.
func (rctx *ReceiveContext) Message() any {
	return rctx.message
}

// CorrelationID returns the async correlation ID when handling a Request/RequestName message.
//
// Design decision: correlation IDs are internal to the async envelope and exposed
// for tracing/debugging without changing the user message contract.
// For non-async messages this returns an empty string.
func (rctx *ReceiveContext) CorrelationID() string {
	return rctx.requestID
}

// BecomeStacked pushes a new behavior on top of the current one.
//
// The current message continues to be processed by the existing behavior;
// subsequent messages are handled by the newly stacked behavior until
// UnBecomeStacked is called.
//
// Use this to model temporary protocol phases (e.g., awaiting response).
func (rctx *ReceiveContext) BecomeStacked(behavior Behavior) {
	rctx.self.setBehaviorStacked(behavior)
}

// UnBecomeStacked pops the most recently stacked behavior.
//
// After calling UnBecomeStacked, the actor resumes the previous behavior (the one
// that was active before the last BecomeStacked call). No effect if there is no stack.
func (rctx *ReceiveContext) UnBecomeStacked() {
	rctx.self.unsetBehaviorStacked()
}

// UnBecome resets the actor behavior to its default (initial) behavior,
// clearing any stacked or currently swapped behavior.
//
// Use this to return the actor to its baseline protocol state.
func (rctx *ReceiveContext) UnBecome() {
	rctx.self.resetBehavior()
}

// Become replaces the current behavior with a new one.
//
// The current message finishes under the old behavior. The new behavior will be
// used for subsequent messages. This does not maintain a stack; use BecomeStacked
// when you need to restore the previous behavior later.
func (rctx *ReceiveContext) Become(behavior Behavior) {
	rctx.self.setBehavior(behavior)
}

// Stash buffers the current message to be processed later.
//
// This is useful when the message cannot be handled under the current behavior
// (e.g., waiting for some state or handshake). Messages are preserved in arrival order.
//
// Stash requires a stash buffer (enable via WithStashing or a stash-mode reentrancy
// request); otherwise ErrStashBufferNotSet is recorded.
//
// Beware that indiscriminate stashing can grow memory usage. Always ensure stashed
// messages are eventually unstashed.
func (rctx *ReceiveContext) Stash() {
	recipient := rctx.self
	if err := recipient.stash(rctx); err != nil {
		rctx.Err(err)
		return
	}
	// Mark the context as stashed so the process loop does not release it
	// back to the pool. The stash now owns this context until Unstash/UnstashAll.
	rctx.stashed.Store(true)
}

// Unstash dequeues the oldest stashed message and prepends it to the mailbox.
//
// The unstashed message will be processed before any newly arriving messages,
// preserving the original arrival order relative to other stashed messages.
func (rctx *ReceiveContext) Unstash() {
	recipient := rctx.self
	if err := recipient.unstash(); err != nil {
		rctx.Err(err)
	}
}

// UnstashAll moves all stashed messages back to the mailbox in arrival order.
//
// Older stashed messages are delivered before newer ones. Use this after a behavior
// transition that enables processing of the previously deferred messages.
func (rctx *ReceiveContext) UnstashAll() {
	recipient := rctx.self
	if err := recipient.unstashAll(); err != nil {
		rctx.Err(err)
	}
}

// Tell sends an asynchronous message to the target PID.
//
// Delivery preserves ordering per-sender. This method does not block.
// Use Ask when a reply is needed.
func (rctx *ReceiveContext) Tell(to *PID, message any) {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	if err := recipient.Tell(ctx, to, message); err != nil {
		rctx.Err(err)
	}
}

// BatchTell sends multiple messages asynchronously to the target PID.
//
// Messages are enqueued and processed one at a time in the order provided.
// This preserves the actor model's single-threaded processing guarantee.
// For a single message, this is equivalent to Tell.
func (rctx *ReceiveContext) BatchTell(to *PID, messages ...any) {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	if err := recipient.BatchTell(ctx, to, messages...); err != nil {
		rctx.Err(err)
	}
}

// Ask sends a synchronous request to another actor and waits for a reply.
//
// The call blocks until a response arrives or the timeout elapses. On timeout or
// delivery error, the error is recorded via Err and the returned response may be nil.
// Choose timeouts carefully to avoid false positives.
//
// Prefer small timeouts and design protocols so that timeouts are treated as
// expected failures.
func (rctx *ReceiveContext) Ask(to *PID, message any, timeout time.Duration) (response any) {
	self := rctx.self
	ctx := rctx.withoutCancel()
	reply, err := self.Ask(ctx, to, message, timeout)
	if err != nil {
		rctx.Err(err)
	}
	return reply
}

// Request sends a message to another actor and returns a RequestCall.
//
// Request is the non-blocking counterpart of Ask. Use it when the current actor must
// remain responsive (e.g., fan-out, long I/O via PipeTo, or avoiding call cycles like
// A -> B -> A). Unlike Ask/SendSync, this method does not wait for a reply.
//
// Reply delivery and continuations:
//   - The reply is delivered back through this actor’s mailbox (single-threaded), so
//     any continuation registered on the returned handle executes within the actor
//     processing model.
//   - Use the returned RequestCall to register a continuation (e.g., Then) and/or
//     cancel the request.
//
// Reentrancy requirement:
//   - The actor must be spawned with WithReentrancy enabled; otherwise the request may
//     be rejected and Err will be set.
//
// Timeouts and options:
//   - Per-call behavior (timeout, mode/policy) can be customized via RequestOption.
//     Defaults may be configured at actor/system level.
//
// On failure to initiate the request, Err is set and the returned call is nil.
func (rctx *ReceiveContext) Request(to *PID, message any, opts ...RequestOption) RequestCall {
	self := rctx.self
	ctx := rctx.withoutCancel()
	call, err := self.request(ctx, to, message, opts...)
	if err != nil {
		rctx.Err(err)
		return nil
	}
	return call
}

// RequestName sends a message to an actor identified by name and returns a RequestCall.
//
// This is the non-blocking, location-transparent counterpart of SendSync: the target may be
// local or remote depending on the actor system configuration (e.g., cluster/remoting).
// Name resolution happens once at call time (local registry and/or cluster lookup, if enabled).
//
// Reply delivery and continuations:
//   - Replies are delivered back to the caller through this actor’s mailbox, preserving the
//     single-threaded actor processing model.
//   - Use the returned RequestCall to register continuations (e.g., Then) and/or cancel
//     the request.
//
// Reentrancy requirement:
//   - The actor must be spawned with reentrancy enabled; otherwise the request may be rejected
//     and Err will be set.
//
// Options:
//   - Per-call behavior (timeout, mode/policy, etc.) can be customized via RequestOption.
//     Defaults may be configured at actor/system level.
//
// On failure to initiate the request, Err is set and the returned call is nil.
func (rctx *ReceiveContext) RequestName(actorName string, message any, opts ...RequestOption) RequestCall {
	self := rctx.self
	ctx := rctx.withoutCancel()
	call, err := self.requestName(ctx, actorName, message, opts...)
	if err != nil {
		rctx.Err(err)
		return nil
	}
	return call
}

// SendAsync sends an asynchronous message to a named actor.
//
// The actor name is resolved within the system (local or cluster), if supported.
// This call does not block and does not expect a reply.
func (rctx *ReceiveContext) SendAsync(actorName string, message any) {
	self := rctx.self
	ctx := rctx.withoutCancel()
	if err := self.SendAsync(ctx, actorName, message); err != nil {
		rctx.Err(err)
	}
}

// SendSync sends a synchronous request to a named actor and waits for a reply.
//
// The actor may be local or remote, depending on system configuration. Blocks until
// a response is received or the timeout expires. On error or timeout, the error is
// recorded via Err and the returned response may be nil.
func (rctx *ReceiveContext) SendSync(actorName string, message any, timeout time.Duration) (response any) {
	self := rctx.self
	ctx := rctx.withoutCancel()
	reply, err := self.SendSync(ctx, actorName, message, timeout)
	if err != nil {
		rctx.Err(err)
	}
	return reply
}

// BatchAsk sends multiple synchronous requests to a target PID and waits for replies.
//
// Replies are returned on a channel in the same order as the input messages.
// Use the provided timeout to bound the overall wait. Errors are recorded via Err.
func (rctx *ReceiveContext) BatchAsk(to *PID, messages []any, timeout time.Duration) (responses chan any) {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	reply, err := recipient.BatchAsk(ctx, to, messages, timeout)
	if err != nil {
		rctx.Err(err)
	}
	return reply
}

// RemoteLookup resolves an actor name on a remote node to an address.
//
// If the provided actor system is nil, the lookup uses the current actor's system.
// On failure, the error is recorded via Err and the returned address may be nil.
func (rctx *ReceiveContext) RemoteLookup(host string, port int, name string) *PID {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	cid, err := recipient.RemoteLookup(ctx, host, port, name)
	if err != nil {
		rctx.Err(err)
	}
	return cid
}

// Shutdown gracefully stops the current actor.
//
// The actor completes processing of already enqueued messages (subject to
// system configuration) before terminating. All child actors are also shut down.
// Errors are recorded via Err.
func (rctx *ReceiveContext) Shutdown() {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	if err := recipient.Shutdown(ctx); err != nil {
		rctx.Err(err)
	}
}

// Spawn creates a named child actor and returns its PID.
//
// On failure, Err is set and a nil PID may be returned. Options can be used to
// configure mailbox, supervision, etc.
func (rctx *ReceiveContext) Spawn(name string, actor Actor, opts ...SpawnOption) *PID {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	pid, err := recipient.SpawnChild(ctx, name, actor, opts...)
	if err != nil {
		rctx.Err(err)
	}
	return pid
}

// Children returns the list of all live child PIDs of the current actor.
//
// The returned slice represents a snapshot at the time of the call.
func (rctx *ReceiveContext) Children() []*PID {
	return rctx.self.Children()
}

// Child returns the PID of a named child actor if it exists and is alive.
//
// On failure, Err is set and a nil PID may be returned.
func (rctx *ReceiveContext) Child(name string) *PID {
	recipient := rctx.self
	pid, err := recipient.Child(name)
	if err != nil {
		rctx.Err(err)
	}
	return pid
}

// Stop requests a child actor to terminate after it finishes its current message.
//
// If the child is already stopped, this is a no-op. Errors stopping the child are
// recorded via Err.
func (rctx *ReceiveContext) Stop(child *PID) {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	if err := recipient.Stop(ctx, child); err != nil {
		rctx.Err(err)
	}
}

// Forward forwards the current message to another local PID, preserving the original sender.
//
// The receiver of the forwarded message sees the original Sender.
// This is only valid within a single-node system where the target PID is known and running.
// No action is taken if the target is not running.
func (rctx *ReceiveContext) Forward(to *PID) {
	message := rctx.Message()
	sender := rctx.Sender()
	ctx := rctx.withoutCancel()

	if to.IsRemote() {
		noSender := rctx.Self().ActorSystem().NoSender()
		if sender.Equals(noSender) {
			return
		}
		if err := sender.Tell(ctx, to, message); err != nil {
			rctx.Err(err)
		}
		return
	}

	if !to.IsRunning() {
		rctx.Err(gerrors.ErrDead)
		return
	}

	receiveContext := getContext()
	receiveContext.build(ctx, sender, to, message, true)
	to.doReceive(receiveContext)
}

// ForwardTo forwards the current message to a named actor, preserving the original sender.
//
// This is location transparent within a clustered system. No action is taken if
// the system is not in cluster mode.
func (rctx *ReceiveContext) ForwardTo(actorName string) {
	message := rctx.Message()
	sender := rctx.Sender()
	ctx := rctx.withoutCancel()
	if rctx.ActorSystem().InCluster() {
		if err := sender.SendAsync(ctx, actorName, message); err != nil {
			rctx.Err(err)
		}
	}
}

// Unhandled marks the current message as unhandled without raising a panic.
//
// The system may log or route this condition depending on configuration and
// supervision strategy. Prefer Unhandled when unknown messages are expected.
func (rctx *ReceiveContext) Unhandled() {
	me := rctx.self
	me.handleReceivedError(rctx, gerrors.ErrUnhandled)
}

// RemoteReSpawn restarts (re-spawns) a named actor on a remote node.
//
// Useful for administrative recovery or orchestration scenarios. Errors are
// recorded via Err.
func (rctx *ReceiveContext) RemoteReSpawn(host string, port int, name string) {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	if err := recipient.RemoteReSpawn(ctx, host, port, name); err != nil {
		rctx.Err(err)
	}
}

// PipeTo runs a task asynchronously and sends its successful result to the target PID.
//
// The calling actor remains responsive while the task executes. On success, the
// returned proto.Message is delivered to the target's mailbox. On failure, behavior
// is controlled by PipeOptions (e.g., error mapping, retries).
//
// The task runs outside the actor's mailbox thread. Avoid mutating the actor's
// internal state inside the task. Communicate results via the returned message.
func (rctx *ReceiveContext) PipeTo(to *PID, task func() (any, error), opts ...PipeOption) {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	if err := recipient.PipeTo(ctx, to, task, opts...); err != nil {
		rctx.Err(err)
	}
}

// PipeToName runs a task asynchronously and sends its successful result to a named actor.
//
// Unlike PipeTo, the target is resolved by name (location transparent).
// Semantics otherwise match PipeTo. Use this to decouple background work from a specific PID.
func (rctx *ReceiveContext) PipeToName(actorName string, task func() (any, error), opts ...PipeOption) {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	if err := recipient.PipeToName(ctx, actorName, task, opts...); err != nil {
		rctx.Err(err)
	}
}

// Logger returns the actor system logger.
//
// Use this for structured logging tied to the actor system's logging configuration.
func (rctx *ReceiveContext) Logger() log.Logger {
	return rctx.self.getLogger()
}

// Watch subscribes the current actor to termination notifications for the given PID.
//
// When the watched actor terminates, a Terminated message is sent to the watcher.
// Idempotent: watching an already watched PID has no adverse effect.
func (rctx *ReceiveContext) Watch(cid *PID) {
	rctx.self.Watch(cid)
}

// UnWatch stops watching the given PID for termination notifications.
//
// Idempotent: unwatching a PID that is not watched has no adverse effect.
func (rctx *ReceiveContext) UnWatch(cid *PID) {
	rctx.self.UnWatch(cid)
}

// ActorSystem returns the ActorSystem hosting the current actor.
//
// Use this to access system-level configuration, tools, or registries.
func (rctx *ReceiveContext) ActorSystem() ActorSystem {
	return rctx.self.ActorSystem()
}

// Extensions returns all extensions registered in the ActorSystem associated with this context.
//
// Extensions provide cross-cutting services (metrics, tracing, persistence, etc.)
// that can be accessed by actors at runtime.
func (rctx *ReceiveContext) Extensions() []extension.Extension {
	return rctx.self.ActorSystem().Extensions()
}

// Extension returns the extension registered under the given ID, or nil if not found.
//
// Use type assertion to access the concrete extension API. The returned value is
// shared system-wide; treat it as read-safe or follow its concurrency contract.
func (rctx *ReceiveContext) Extension(extensionID string) extension.Extension {
	return rctx.self.ActorSystem().Extension(extensionID)
}

// Reinstate transitions a previously suspended actor (by PID) back to active.
//
// The actor resumes processing its mailbox and continues from its prior internal state.
// Typical callers include supervisors and administrative actors. Errors are recorded via Err.
func (rctx *ReceiveContext) Reinstate(cid *PID) {
	recipient := rctx.self
	if err := recipient.Reinstate(cid); err != nil {
		rctx.Err(err)
	}
}

// ReinstateNamed transitions a previously suspended actor (by name) back to active.
//
// This is useful in cluster mode where actors are addressed by name and a PID may
// not be directly available. Errors are recorded via Err.
func (rctx *ReceiveContext) ReinstateNamed(actorName string) {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	if err := recipient.ReinstateNamed(ctx, actorName); err != nil {
		rctx.Err(err)
	}
}

// SenderAddress returns the location-transparent address of the message sender.
//
// Works for both local and remote senders. Returns address.NoSender() when
// the sender is unknown or system-generated.
func (rctx *ReceiveContext) SenderAddress() *address.Address {
	if rctx.sender == nil {
		return address.NoSender()
	}
	return rctx.sender.Address()
}

// ReceiverAddress returns the address of the current (receiver) actor.
//
// Returns address.NoSender() only in rare internal states where Self is nil.
func (rctx *ReceiveContext) ReceiverAddress() *address.Address {
	if rctx.self == nil {
		return address.NoSender()
	}
	return rctx.self.Address()
}

// Dependencies returns a slice containing all dependencies currently registered
// within the given actor
//
// These dependencies are typically injected at actor initialization (via SpawnOptions)
// and made accessible during the actor's lifecycle. They can include services, clients,
// or other resources that the actor may utilize while processing messages.
//
// Returns:
//   - []extension.Dependency: All registered dependencies in the actor.
func (rctx *ReceiveContext) Dependencies() []extension.Dependency {
	return rctx.self.Dependencies()
}

// Dependency retrieves a specific dependency registered in the actor by its unique ID.
//
// This allows actors to access injected services or resources directly from the ReceiveContext.
//
// Parameters:
//   - dependencyID: A unique string identifier used when the dependency was registered.
//
// Returns:
//   - extension.Dependency: The corresponding dependency if found, or nil otherwise.
//
// Example:
//
//	myService := rctx.Dependency("dependencyID").(MyService)
func (rctx *ReceiveContext) Dependency(dependencyID string) extension.Dependency {
	return rctx.self.Dependency(dependencyID)
}

// getError returns any error recorded during message processing.
//
// This is intended for internal use by the runtime/supervision logic.
func (rctx *ReceiveContext) getError() error {
	return rctx.err
}

// newReceiveContext constructs a ReceiveContext for internal use by the runtime.
//
// Callers should prefer the context provided by the framework during message delivery.
func newReceiveContext(ctx context.Context, from, to *PID, message any) *ReceiveContext {
	// create a message receiveContext
	rc := &ReceiveContext{
		ctx:      ctx,
		message:  message,
		sender:   from,
		response: getResponseChannel(),
		self:     to,
	}
	rc.responseClosed.Store(false)
	return rc
}

// build initializes or reuses a ReceiveContext instance for internal dispatch.
//
// If async is true, the context is derived with context.WithoutCancel to prevent
// premature cancellation during asynchronous forwarding and dispatch.
func (rctx *ReceiveContext) build(ctx context.Context, from, to *PID, message any, async bool) *ReceiveContext {
	// Mark context as in-use to prevent concurrent reset().
	// This protects against sync.Pool races where a context is returned
	// to the pool while another goroutine is retrieving and building it.
	// A simple Store suffices because the stash fix (stashed flag + conditional
	// releaseContext) guarantees no concurrent build/reset on the same context.
	rctx.inUse.Store(true)

	rctx.sender = from
	rctx.self = to
	rctx.message = message

	if async {
		// Fast path: if the context is already non-cancelable (e.g. context.Background()),
		// skip the allocation from context.WithoutCancel.
		if ctx.Done() == nil {
			rctx.ctx = ctx
		} else {
			rctx.ctx = context.WithoutCancel(ctx)
		}
		return rctx
	}

	rctx.responseClosed.Store(false)
	rctx.ctx = ctx
	rctx.response = getResponseChannel()
	return rctx
}

// reset clears all fields so the ReceiveContext can be safely reused by the runtime.
//
// This is an internal optimization to reduce allocations during message dispatch.
func (rctx *ReceiveContext) reset() {
	rctx.ctx = nil
	rctx.message = nil
	rctx.self = nil
	rctx.sender = nil
	rctx.response = nil
	rctx.err = nil
	rctx.requestID = ""
	rctx.requestReplyTo = ""

	// Note: responseClosed and stashed are not reset here because:
	// - responseClosed: set explicitly by build() in the sync path;
	//   for async (Tell) it is never checked, so a stale value is harmless.
	// - stashed: releaseContext() skips stashed contexts, so any context
	//   reaching reset() already has stashed == false.
	// Avoiding these two atomic stores shaves ~10ns per message.

	// Mark context as available for reuse.
	// This must be the last operation to ensure all fields are fully
	// reset before another goroutine can acquire this context via build().
	rctx.inUse.Store(false)
}

// withoutCancel safely derives a non-cancelable context from the current context.
//
// This avoids propagating cancellation from the inbound message context to long-lived
// operations, while still honoring deadlines if set on the underlying context.
func (rctx *ReceiveContext) withoutCancel() context.Context {
	if rctx.ctx == nil {
		return context.Background()
	}

	if rctx.ctx.Done() == nil {
		return rctx.ctx
	}

	return context.WithoutCancel(rctx.ctx)
}

// withRequestMeta sets async request metadata for internal dispatch.
//
// Design decision: metadata stays off the user message type to keep protobuf
// payloads stable and backward compatible.
func (rctx *ReceiveContext) withRequestMeta(correlationID, replyTo string) *ReceiveContext {
	rctx.requestID = correlationID
	rctx.requestReplyTo = replyTo
	return rctx
}
