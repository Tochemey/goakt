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

import "time"

// Deadletter defines the deadletter event
type Deadletter struct {
	sender   string
	receiver string
	message  any
	sendTime time.Time
	reason   string
}

// NewDeadletter creates a new Deadletter event.
func NewDeadletter(sender, receiver string, message any, sendTime time.Time, reason string) *Deadletter {
	return &Deadletter{
		sender:   sender,
		receiver: receiver,
		message:  message,
		sendTime: sendTime,
		reason:   reason,
	}
}

// Sender returns the sender's address.
func (d *Deadletter) Sender() string { return d.sender }

// Receiver returns the receiver's address.
func (d *Deadletter) Receiver() string { return d.receiver }

// Message returns the original message that could not be delivered.
func (d *Deadletter) Message() any { return d.message }

// SendTime returns the time the message was sent.
func (d *Deadletter) SendTime() time.Time { return d.sendTime }

// Reason returns the reason the message was dead-lettered.
func (d *Deadletter) Reason() string { return d.reason }

// ActorStarted defines the actor started event
type ActorStarted struct {
	address   string
	startedAt time.Time
}

// NewActorStarted creates a new ActorStarted event stamped with the current UTC time.
func NewActorStarted(address string) *ActorStarted {
	return &ActorStarted{address: address, startedAt: time.Now().UTC()}
}

// Address returns the actor's address.
func (a *ActorStarted) Address() string { return a.address }

// StartedAt returns the time the actor started.
func (a *ActorStarted) StartedAt() time.Time { return a.startedAt }

// ActorStopped defines the actor stopped event
type ActorStopped struct {
	address   string
	stoppedAt time.Time
}

// NewActorStopped creates a new ActorStopped event stamped with the current UTC time.
func NewActorStopped(address string) *ActorStopped {
	return &ActorStopped{address: address, stoppedAt: time.Now().UTC()}
}

// Address returns the actor's address.
func (a *ActorStopped) Address() string { return a.address }

// StoppedAt returns the time the actor stopped.
func (a *ActorStopped) StoppedAt() time.Time { return a.stoppedAt }

// ActorPassivated defines the actor passivated event
type ActorPassivated struct {
	address      string
	passivatedAt time.Time
}

// NewActorPassivated creates a new ActorPassivated event stamped with the current UTC time.
func NewActorPassivated(address string) *ActorPassivated {
	return &ActorPassivated{address: address, passivatedAt: time.Now().UTC()}
}

// Address returns the actor's address.
func (a *ActorPassivated) Address() string { return a.address }

// PassivatedAt returns the time the actor was passivated.
func (a *ActorPassivated) PassivatedAt() time.Time { return a.passivatedAt }

// ActorChildCreated defines the child actor created event
type ActorChildCreated struct {
	address   string
	parent    string
	createdAt time.Time
}

// NewActorChildCreated creates a new ActorChildCreated event stamped with the current UTC time.
func NewActorChildCreated(address, parent string) *ActorChildCreated {
	return &ActorChildCreated{address: address, parent: parent, createdAt: time.Now().UTC()}
}

// Address returns the child actor's address.
func (a *ActorChildCreated) Address() string { return a.address }

// Parent returns the parent actor's address.
func (a *ActorChildCreated) Parent() string { return a.parent }

// CreatedAt returns the time the child actor was created.
func (a *ActorChildCreated) CreatedAt() time.Time { return a.createdAt }

// ActorRestarted defines the actor restarted event
type ActorRestarted struct {
	address     string
	restartedAt time.Time
}

// NewActorRestarted creates a new ActorRestarted event stamped with the current UTC time.
func NewActorRestarted(address string) *ActorRestarted {
	return &ActorRestarted{address: address, restartedAt: time.Now().UTC()}
}

// Address returns the actor's address.
func (a *ActorRestarted) Address() string { return a.address }

// RestartedAt returns the time the actor was restarted.
func (a *ActorRestarted) RestartedAt() time.Time { return a.restartedAt }

// ActorSuspended defines the actor suspended event
type ActorSuspended struct {
	address     string
	suspendedAt time.Time
	reason      string
}

// NewActorSuspended creates a new ActorSuspended event stamped with the current UTC time.
func NewActorSuspended(address, reason string) *ActorSuspended {
	return &ActorSuspended{address: address, suspendedAt: time.Now().UTC(), reason: reason}
}

// Address returns the actor's address.
func (a *ActorSuspended) Address() string { return a.address }

// SuspendedAt returns the time the actor was suspended.
func (a *ActorSuspended) SuspendedAt() time.Time { return a.suspendedAt }

// Reason returns the suspension reason.
func (a *ActorSuspended) Reason() string { return a.reason }

// ActorReinstated is triggered when an actor is reinstated
type ActorReinstated struct {
	address      string
	reinstatedAt time.Time
}

// NewActorReinstated creates a new ActorReinstated event stamped with the current UTC time.
func NewActorReinstated(address string) *ActorReinstated {
	return &ActorReinstated{address: address, reinstatedAt: time.Now().UTC()}
}

// Address returns the actor's address.
func (a *ActorReinstated) Address() string { return a.address }

// ReinstatedAt returns the time the actor was reinstated.
func (a *ActorReinstated) ReinstatedAt() time.Time { return a.reinstatedAt }

// NodeJoined defines the node joined event
type NodeJoined struct {
	address   string
	timestamp time.Time
}

// NewNodeJoined creates a new NodeJoined event.
func NewNodeJoined(address string, timestamp time.Time) *NodeJoined {
	return &NodeJoined{address: address, timestamp: timestamp}
}

// Address returns the node's address.
func (n *NodeJoined) Address() string { return n.address }

// Timestamp returns the time the node joined.
func (n *NodeJoined) Timestamp() time.Time { return n.timestamp }

// NodeLeft defines the node left event
type NodeLeft struct {
	address   string
	timestamp time.Time
}

// NewNodeLeft creates a new NodeLeft event.
func NewNodeLeft(address string, timestamp time.Time) *NodeLeft {
	return &NodeLeft{address: address, timestamp: timestamp}
}

// Address returns the node's address.
func (n *NodeLeft) Address() string { return n.address }

// Timestamp returns the time the node left.
func (n *NodeLeft) Timestamp() time.Time { return n.timestamp }

// Terminated is a lifecycle notification message sent to all actors
// that are watching a given actor when it has stopped or been terminated.
//
// This message allows supervising or dependent actors to react to the shutdown
// of the actor they were observing—for example, by cleaning up resources,
// restarting the actor, or triggering failover behavior.
type Terminated struct {
	address      string
	terminatedAt time.Time
}

// NewTerminated creates a new Terminated message stamped with the current UTC time.
func NewTerminated(address string) *Terminated {
	return &Terminated{address: address, terminatedAt: time.Now().UTC()}
}

// Address returns the address of the terminated actor.
func (t *Terminated) Address() string { return t.address }

// TerminatedAt returns the time the actor was terminated.
func (t *Terminated) TerminatedAt() time.Time { return t.terminatedAt }

// PoisonPill is a special control message used to gracefully stop an actor.
//
// When an actor receives a PoisonPill, it will initiate a controlled shutdown sequence.
// The PoisonPill is enqueued in the actor's mailbox like any other message, meaning:
//   - It will not interrupt message processing.
//   - It will only be handled after all previously enqueued messages are processed.
//
// This allows the actor to finish processing in-flight work before termination,
// ensuring clean shutdown semantics without abrupt interruptions.
type PoisonPill struct{}

// PostStart is used when an actor has successfully started
type PostStart struct{}

// Broadcast is used to send message to a router via Tell.
// The router will broadcast the message to all its routees
// given the routing strategy or the type of router used.
type Broadcast struct {
	message any
}

// NewBroadcast creates a new Broadcast message.
func NewBroadcast(message any) *Broadcast {
	return &Broadcast{message: message}
}

// Message returns the inner message to broadcast to all routees.
func (b *Broadcast) Message() any { return b.message }

// Subscribe is used to subscribe to a topic by an actor.
// The actor will receive an acknowledgement message
// when the subscription is successful.
type Subscribe struct {
	topic string
}

// NewSubscribe creates a new Subscribe message for the given topic.
func NewSubscribe(topic string) *Subscribe {
	return &Subscribe{topic: topic}
}

// Topic returns the topic to subscribe to.
func (s *Subscribe) Topic() string { return s.topic }

// Unsubscribe is used to unsubscribe from a topic by an actor.
// The actor will receive an acknowledgement message
// when the unsubscription is successful.
type Unsubscribe struct {
	topic string
}

// NewUnsubscribe creates a new Unsubscribe message for the given topic.
func NewUnsubscribe(topic string) *Unsubscribe {
	return &Unsubscribe{topic: topic}
}

// Topic returns the topic to unsubscribe from.
func (u *Unsubscribe) Topic() string { return u.topic }

// SubscribeAck is used to acknowledge a successful subscription
// to a topic by an actor.
type SubscribeAck struct {
	topic string
}

// NewSubscribeAck creates a new SubscribeAck message for the given topic.
func NewSubscribeAck(topic string) *SubscribeAck {
	return &SubscribeAck{topic: topic}
}

// Topic returns the topic that was subscribed to.
func (s *SubscribeAck) Topic() string { return s.topic }

// UnsubscribeAck is used to acknowledge a successful unsubscription
// from a topic by an actor.
type UnsubscribeAck struct {
	topic string
}

// NewUnsubscribeAck creates a new UnsubscribeAck message for the given topic.
func NewUnsubscribeAck(topic string) *UnsubscribeAck {
	return &UnsubscribeAck{topic: topic}
}

// Topic returns the topic that was unsubscribed from.
func (u *UnsubscribeAck) Topic() string { return u.topic }

// Publish is used to send a message to a topic
// by the TopicActor. The message
// will be broadcasted to all actors that are subscribed
// to the topic in the cluster.
type Publish struct {
	id      string
	topic   string
	message any
}

// NewPublish creates a new Publish message.
func NewPublish(id, topic string, message any) *Publish {
	return &Publish{id: id, topic: topic, message: message}
}

// ID returns the message unique identifier.
func (p *Publish) ID() string { return p.id }

// Topic returns the topic to publish to.
func (p *Publish) Topic() string { return p.topic }

// Message returns the message payload.
func (p *Publish) Message() any { return p.message }

// NoMessage is used to indicate that no message was sent
type NoMessage struct{}

// PanicSignal is a system-level message used in actor-based systems to notify a parent actor
// that one of its child actors has encountered a critical failure or unhandled condition.
// This message is automatically sent when the Escalate supervision directive is invoked,
// indicating that the issue cannot be handled at the child level.
//
// The child actor is suspended, and the parent actor is expected to take appropriate action.
// Upon receiving a PanicSignal, the parent actor can decide how to handle the failure,
// such as restarting the child, stopping it, escalating further, or applying custom recovery logic.
type PanicSignal struct {
	message   any
	reason    string
	timestamp time.Time
}

// NewPanicSignal creates a new PanicSignal.
func NewPanicSignal(message any, reason string, timestamp time.Time) *PanicSignal {
	return &PanicSignal{message: message, reason: reason, timestamp: timestamp}
}

// Message returns the original message that triggered the failure.
func (p *PanicSignal) Message() any { return p.message }

// Reason returns the human-readable explanation of the failure.
func (p *PanicSignal) Reason() string { return p.reason }

// Timestamp returns when the PanicSignal event occurred.
func (p *PanicSignal) Timestamp() time.Time { return p.timestamp }

// PausePassivation is a system-level message used to pause the passivation of an actor.
// One can send this message to an actor to prevent it from being passivated when passivation is enabled.
// This is useful in scenarios where an actor needs to remain active for a certain period while processing
// critical messages or performing important tasks. This is a fire-and-forget message, so it does not expect a response.
// This will no-op if the actor does not have passivation enabled.
type PausePassivation struct{}

// ResumePassivation is a system-level message used to resume the passivation of an actor.
// One can send this message to an actor to allow it to be passivated again after it has been paused.
// This is useful in scenarios where an actor has temporarily paused its passivation
// to complete critical tasks or handle important messages, and now it can return to its normal
// passivation behavior. This is a fire-and-forget message, so it does not expect a response.
//
// This will no-op if the actor does not have passivation enabled.
// If the actor is not created with a custom passivation timeout, it will use the default passivation timeout.
type ResumePassivation struct{}

// StatusFailure is used to indicate a failure status with an error message
type StatusFailure struct {
	err     string
	message any
}

// NewStatusFailure creates a new StatusFailure with the given error message and optional original message.
func NewStatusFailure(err string, message any) *StatusFailure {
	return &StatusFailure{err: err, message: message}
}

// Error returns the error message.
func (s *StatusFailure) Error() string { return s.err }

// Message returns the original message that caused the failure.
func (s *StatusFailure) Message() any { return s.message }

// GetRoutees requests a snapshot of the router's current routee set.
//
// Usage:
//   - Send with Ask to a router PID to retrieve the list of active routee names.
//   - The router replies with a Routees message. If no routees are available, the list is empty.
//
// Scope and consistency:
//   - Routers are local to an actor system; this message is only valid within the same process/system.
//   - The returned list is a point-in-time snapshot.
//
// Resolving names:
//   - Use ActorSystem.LocalActor (or LocalActor) to resolve a returned name to a local PID/reference.
//   - Names are unique within the router's namespace.
type GetRoutees struct{}

// Routees contains the set of routee names managed by a router at the time of the GetRoutees request.
//
// Semantics:
//   - Names represents the router's best-effort snapshot when the request was handled.
//   - A name can be resolved to a local PID/reference via ActorSystem.LocalActor (or LocalActor).
//   - Since routers are locally scoped, these names are not valid across different actor systems or nodes.
//
// Example (Go):
//
//	resp, err := ctx.Ask(routerPID, &actor.GetRoutees{}, 500*time.Millisecond)
//	if err == nil {
//	    r := resp.(*actor.Routees)
//	    for _, name := range r.Names() {
//	        if pid := ctx.ActorSystem().LocalActor(name); pid != nil {
//	            ctx.Tell(pid, &MyMessage{})
//	        }
//	    }
//	}
type Routees struct {
	names []string
}

// NewRoutees creates a new Routees response with the given routee names.
func NewRoutees(names []string) *Routees {
	return &Routees{names: names}
}

// Names returns the list of routee actor names (unique within the router's scope).
func (r *Routees) Names() []string { return r.names }

// AdjustRouterPoolSize requests a runtime delta resize of a router's routee pool.
//
// Semantics (delta-based):
//   - PoolSize > 0: Scale up by PoolSize (spawn that many additional routees).
//   - PoolSize < 0: Scale down by |PoolSize| (stop that many existing routees).
//   - PoolSize == 0: No-op.
//
// Notes:
//   - The resulting pool size never goes below 0. If |PoolSize| exceeds the current size, the pool becomes 0.
//   - This is not idempotent: sending +N twice grows by 2N. Prefer absolute-size semantics at the call site if needed.
//   - Resizes are processed in mailbox order; intermediate sizes may be observed while scaling is in progress.
//   - Routing continues during resize using currently available routees.
//
// Constraints:
//   - Negative values are allowed and indicate downsizing.
//   - Routees are local to the actor system hosting the router.
//   - Supervisory directives (restart/resume/stop) apply to failures, not to normal downsizing.
//
// Verification:
//   - Use GetRoutees after some delay to observe effective membership.
//
// Examples (Go):
//
//	// Grow by 5
//	ctx.Tell(routerPID, actor.NewAdjustRouterPoolSize(5))
//	// Shrink by 3
//	ctx.Tell(routerPID, actor.NewAdjustRouterPoolSize(-3))
type AdjustRouterPoolSize struct {
	poolSize int32
}

// NewAdjustRouterPoolSize creates an AdjustRouterPoolSize message with the given signed delta.
//
//   - poolSize > 0 increases the pool by that many routees.
//   - poolSize < 0 decreases the pool by the absolute value.
//   - poolSize == 0 is a no-op.
func NewAdjustRouterPoolSize(poolSize int32) *AdjustRouterPoolSize {
	return &AdjustRouterPoolSize{poolSize: poolSize}
}

// PoolSize returns the signed delta to apply to the router's pool.
func (a *AdjustRouterPoolSize) PoolSize() int32 { return a.poolSize }
