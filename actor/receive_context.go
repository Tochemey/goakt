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
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/log"
)

// ReceiveContext carries per-message context and operations available to an actor
// while handling a single message. It exposes:
//   - Message metadata (Message, Sender, RemoteSender, SenderAddress, ReceiverAddress)
//   - Actor lifecycle and behavior management (Become, BecomeStacked, UnBecome, UnBecomeStacked,
//     Stash, Unstash, UnstashAll, Stop, Shutdown, Watch, UnWatch, Reinstate, ReinstateNamed)
//   - Messaging operations (Tell, Ask, BatchTell, BatchAsk, SendAsync, SendSync, Forward, ForwardTo,
//     RemoteTell/Ask/BatchTell/BatchAsk/Forward, RemoteLookup)
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
//   - Sender() returns the local PID when known; otherwise it is NoSender.
//   - RemoteSender() is set when the message originates from a remote node.
//   - Prefer SenderAddress() to get a location-transparent sender address.
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
	ctx          context.Context
	message      proto.Message
	sender       *PID
	remoteSender *address.Address
	response     chan proto.Message
	self         *PID
	err          error
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

// Response publishes a reply when handling a message initiated with Ask.
//
// This is the counterpart to Ask when the current message expects a response.
// If there is no pending Ask (no response channel), the call is a no-op.
// The method is non-blocking for the caller; the framework delivers the response.
//
// Use Respond/Response exactly once per Ask-initiated message. Multiple calls may
// be ignored or override each other depending on the underlying channel semantics.
func (rctx *ReceiveContext) Response(resp proto.Message) {
	if rctx.response == nil {
		return
	}
	rctx.response <- resp
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

// Sender returns the local PID of the message sender when known.
//
// Returns NoSender when the sender is unknown, when the message originated remotely,
// or when the message was system-generated. For remote messages, use RemoteSender()
// or SenderAddress() for a location-transparent address.
func (rctx *ReceiveContext) Sender() *PID {
	return rctx.sender
}

// RemoteSender returns the remote address of the sender when the message originated
// from another node.
//
// For local messages this returns address.NoSender(). Prefer SenderAddress() when you
// need a unified view across local and remote messages.
func (rctx *ReceiveContext) RemoteSender() *address.Address {
	return rctx.remoteSender
}

// Message returns the protobuf message being processed.
//
// Treat the returned message as immutable. Use type assertions or pattern matching
// on the message type to implement your actor behavior.
func (rctx *ReceiveContext) Message() proto.Message {
	return rctx.message
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
// Beware that indiscriminate stashing can grow memory usage. Always ensure stashed
// messages are eventually unstashed.
func (rctx *ReceiveContext) Stash() {
	recipient := rctx.self
	if err := recipient.stash(rctx); err != nil {
		rctx.Err(err)
	}
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
func (rctx *ReceiveContext) Tell(to *PID, message proto.Message) {
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
func (rctx *ReceiveContext) BatchTell(to *PID, messages ...proto.Message) {
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
func (rctx *ReceiveContext) Ask(to *PID, message proto.Message, timeout time.Duration) (response proto.Message) {
	self := rctx.self
	ctx := rctx.withoutCancel()
	reply, err := self.Ask(ctx, to, message, timeout)
	if err != nil {
		rctx.Err(err)
	}
	return reply
}

// SendAsync sends an asynchronous message to a named actor.
//
// The actor name is resolved within the system (local or cluster), if supported.
// This call does not block and does not expect a reply.
func (rctx *ReceiveContext) SendAsync(actorName string, message proto.Message) {
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
func (rctx *ReceiveContext) SendSync(actorName string, message proto.Message, timeout time.Duration) (response proto.Message) {
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
func (rctx *ReceiveContext) BatchAsk(to *PID, messages []proto.Message, timeout time.Duration) (responses chan proto.Message) {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	reply, err := recipient.BatchAsk(ctx, to, messages, timeout)
	if err != nil {
		rctx.Err(err)
	}
	return reply
}

// RemoteTell sends a fire-and-forget message to a remote actor address.
//
// Requires remoting to be enabled and properly configured. This call does not block.
func (rctx *ReceiveContext) RemoteTell(to *address.Address, message proto.Message) {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	if err := recipient.RemoteTell(ctx, to, message); err != nil {
		rctx.Err(err)
	}
}

// RemoteAsk sends a synchronous request to a remote actor and waits for a reply.
//
// The response is returned as a protobuf Any. On error or timeout, the error is
// recorded via Err and the returned value may be nil. Ensure both sides agree
// on message types and serialization.
func (rctx *ReceiveContext) RemoteAsk(to *address.Address, message proto.Message, timeout time.Duration) (response *anypb.Any) {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	reply, err := recipient.RemoteAsk(ctx, to, message, timeout)
	if err != nil {
		rctx.Err(err)
	}
	return reply
}

// RemoteBatchTell sends multiple messages to a remote actor in a fire-and-forget manner.
//
// Messages are processed one-by-one in order by the recipient.
func (rctx *ReceiveContext) RemoteBatchTell(to *address.Address, messages []proto.Message) {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	if err := recipient.RemoteBatchTell(ctx, to, messages); err != nil {
		rctx.Err(err)
	}
}

// RemoteBatchAsk sends multiple synchronous requests to a remote actor and waits for replies.
//
// Replies are returned as a slice of Any in the same order as the input messages.
// Use the timeout to bound the overall wait. Errors are recorded via Err.
func (rctx *ReceiveContext) RemoteBatchAsk(to *address.Address, messages []proto.Message, timeout time.Duration) (responses []*anypb.Any) {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	replies, err := recipient.RemoteBatchAsk(ctx, to, messages, timeout)
	if err != nil {
		rctx.Err(err)
	}
	return replies
}

// RemoteLookup resolves an actor name on a remote node to an address.
//
// If the provided actor system is nil, the lookup uses the current actor's system.
// On failure, the error is recorded via Err and the returned address may be nil.
func (rctx *ReceiveContext) RemoteLookup(host string, port int, name string) (addr *address.Address) {
	recipient := rctx.self
	ctx := rctx.withoutCancel()
	remoteAddr, err := recipient.RemoteLookup(ctx, host, port, name)
	if err != nil {
		rctx.Err(err)
	}
	return remoteAddr
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
// The receiver of the forwarded message sees the original Sender/RemoteSender.
// This is only valid within a single-node system where the target PID is known and running.
// No action is taken if the target is not running.
func (rctx *ReceiveContext) Forward(to *PID) {
	message := rctx.Message()
	sender := rctx.Sender()

	if to.IsRunning() {
		ctx := rctx.withoutCancel()
		receiveContext := getContext()
		receiveContext.build(ctx, sender, to, message, true)
		to.doReceive(receiveContext)
	}
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

// RemoteForward forwards the current message to a remote address, preserving the original sender.
//
// If the original sender is local, the message is sent from that sender to the remote address.
// If the original sender is remote and remoting is enabled, the system bridges the forward to
// preserve sender identity across nodes. No action is taken if neither case applies.
func (rctx *ReceiveContext) RemoteForward(to *address.Address) {
	sender := rctx.Sender()
	remoteSender := rctx.RemoteSender()
	remoting := rctx.Self().remoting
	noSender := rctx.Self().ActorSystem().NoSender()

	if !sender.Equals(noSender) {
		message := rctx.Message()
		ctx := rctx.withoutCancel()
		if err := sender.RemoteTell(ctx, to, message); err != nil {
			rctx.Err(err)
		}
		return
	}

	if !remoteSender.Equals(address.NoSender()) && remoting != nil {
		message := rctx.Message()
		ctx := rctx.withoutCancel()
		if err := remoting.RemoteTell(ctx, remoteSender, to, message); err != nil {
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
	me.handleReceivedError(rctx, errors.ErrUnhandled)
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
func (rctx *ReceiveContext) PipeTo(to *PID, task func() (proto.Message, error), opts ...PipeOption) {
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
func (rctx *ReceiveContext) PipeToName(actorName string, task func() (proto.Message, error), opts ...PipeOption) {
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
	return rctx.self.Logger()
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

// SenderAddress returns the location-transparent address of the sender of the current message.
//
// If the sender is unknown, address.NoSender() is returned. Prefer this method over
// Sender/RemoteSender when you only need an address abstraction.
func (rctx *ReceiveContext) SenderAddress() *address.Address {
	from := address.NoSender()
	if rctx.Sender() != nil {
		from = rctx.Sender().Address()
	} else if rctx.RemoteSender() != nil {
		from = rctx.RemoteSender()
	}
	return from
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

// getError returns any error recorded during message processing.
//
// This is intended for internal use by the runtime/supervision logic.
func (rctx *ReceiveContext) getError() error {
	return rctx.err
}

// newReceiveContext constructs a ReceiveContext for internal use by the runtime.
//
// Callers should prefer the context provided by the framework during message delivery.
func newReceiveContext(ctx context.Context, from, to *PID, message proto.Message) *ReceiveContext {
	// create a message receiveContext
	return &ReceiveContext{
		ctx:      ctx,
		message:  message,
		sender:   from,
		response: getResponseChannel(),
		self:     to,
	}
}

// build initializes or reuses a ReceiveContext instance for internal dispatch.
//
// If async is true, the context is derived with context.WithoutCancel to prevent
// premature cancellation during asynchronous forwarding and dispatch.
func (rctx *ReceiveContext) build(ctx context.Context, from, to *PID, message proto.Message, async bool) *ReceiveContext {
	rctx.sender = from
	rctx.self = to
	rctx.message = message

	if async {
		rctx.ctx = context.WithoutCancel(ctx)
		return rctx
	}

	rctx.ctx = ctx
	rctx.response = getResponseChannel()
	return rctx
}

// reset clears all fields so the ReceiveContext can be safely reused by the runtime.
//
// This is an internal optimization to reduce allocations during message dispatch.
func (rctx *ReceiveContext) reset() {
	var pid *PID
	rctx.message = nil
	rctx.self = pid
	rctx.sender = pid
	rctx.err = nil
	rctx.ctx = nil
	rctx.response = nil
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

// withRemoteSender sets the remote sender address for this context.
//
// This is used by the runtime to attach remote sender information during dispatch.
func (rctx *ReceiveContext) withRemoteSender(remoteSender *address.Address) *ReceiveContext {
	rctx.remoteSender = remoteSender
	return rctx
}
