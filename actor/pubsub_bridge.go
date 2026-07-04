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

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/supervisor"
)

// pubsubBridgeNamePrefix names the internal actor spawned per SubscribeTopic call.
// It intentionally does not start with the reserved "GoAkt" prefix (see reservedNamesPrefix)
// so the bridge actor can be shut down like any ordinary actor from Subscription.Unsubscribe.
const pubsubBridgeNamePrefix = "goaktPubSubBridge"

// Subscription represents a non-actor subscription to a pub/sub topic created via
// ActorSystem.SubscribeTopic. It wraps an internally managed bridge actor that forwards
// messages published to the topic to the handler supplied at subscription time.
type Subscription interface {
	// Topic returns the topic this subscription is bound to.
	Topic() string
	// Unsubscribe stops the subscription: no further messages are delivered to the handler
	// and the underlying bridge actor is shut down. It is safe to call more than once; only
	// the first call has any effect.
	Unsubscribe() error
	// Close is an alias for Unsubscribe, provided so a Subscription can be used with defer
	// alongside other closer-style resources.
	Close() error
}

// pubsubBridgeSubscription is the default Subscription implementation returned by
// ActorSystem.SubscribeTopic.
type pubsubBridgeSubscription struct {
	topic string
	pid   *PID
	once  sync.Once
	err   error
}

// enforce compilation error
var _ Subscription = (*pubsubBridgeSubscription)(nil)

// Topic returns the topic this subscription is bound to.
func (s *pubsubBridgeSubscription) Topic() string { return s.topic }

// Unsubscribe implements Subscription.
func (s *pubsubBridgeSubscription) Unsubscribe() error {
	s.once.Do(func() {
		s.err = s.pid.Shutdown(context.Background())
	})
	return s.err
}

// Close implements Subscription.
func (s *pubsubBridgeSubscription) Close() error {
	return s.Unsubscribe()
}

// pubsubBridgeActor forwards messages published to a topic to a plain callback handler,
// without requiring the caller to define and spawn an Actor of their own. It exists purely
// to bridge the actor-based topic pub/sub machinery (see topicActor) to non-actor code, e.g.
// a websocket/SSE gateway handler.
//
// It subscribes to its topic exactly like any other actor subscriber (Subscribe/Watch/
// Terminated), so dedup, retention, and cross-node dissemination semantics owned by topicActor
// are untouched. A panic raised by the handler is recovered by the generic per-message panic
// recovery every actor already has (see PID.recovery) and resumed by this actor's supervisor,
// so a misbehaving handler never tears down the subscription.
type pubsubBridgeActor struct {
	topic         string
	topicActorPID *PID
	handler       func(ctx context.Context, message proto.Message)
	logger        log.Logger
}

// enforce compilation error
var _ Actor = (*pubsubBridgeActor)(nil)

// newPubSubBridgeActor creates a new pub/sub bridge actor for the given topic.
func newPubSubBridgeActor(topic string, topicActorPID *PID, handler func(ctx context.Context, message proto.Message)) Actor {
	return &pubsubBridgeActor{
		topic:         topic,
		topicActorPID: topicActorPID,
		handler:       handler,
	}
}

// PreStart is called before the actor starts.
func (x *pubsubBridgeActor) PreStart(*Context) error {
	return nil
}

// Receive is called to process the message.
func (x *pubsubBridgeActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
		x.logger = ctx.Logger()
		ctx.Tell(x.topicActorPID, NewSubscribe(x.topic))
	case *SubscribeAck, *UnsubscribeAck, *Terminated:
		// subscription lifecycle signals; nothing to forward to the handler
	default:
		x.dispatch(ctx)
	}
}

// dispatch forwards a delivered topic message to the registered handler.
func (x *pubsubBridgeActor) dispatch(ctx *ReceiveContext) {
	message, ok := ctx.Message().(proto.Message)
	if !ok {
		x.logger.Warnf("pubsub bridge for topic=%q dropped a non-proto message of type %T", x.topic, ctx.Message())
		return
	}
	x.handler(ctx.Context(), message)
}

// PostStop is called when the actor is stopped.
func (x *pubsubBridgeActor) PostStop(*Context) error {
	return nil
}

// SubscribeTopic registers a plain callback as a subscriber of the given topic. See the
// ActorSystem interface documentation for details.
func (x *actorSystem) SubscribeTopic(topic string, handler func(ctx context.Context, message proto.Message)) (Subscription, error) {
	if !x.Running() {
		return nil, gerrors.ErrActorSystemNotStarted
	}

	if handler == nil {
		return nil, gerrors.ErrSubscribeHandlerRequired
	}

	topicActorPID := x.TopicActor()
	if topicActorPID == nil {
		return nil, gerrors.ErrPubSubDisabled
	}

	name := fmt.Sprintf("%s-%s", pubsubBridgeNamePrefix, uuid.NewString())

	// The bridge is spawned via configPID directly (the same path topicActor and replicator
	// use) rather than the public Spawn, and deliberately skips putActorOnCluster: it is a
	// purely local construct that topicActor only ever addresses by its in-memory *PID, so it
	// never needs cluster-wide resolution. Routing it through Spawn would register it in the
	// cluster's actor directory on creation but, because asSystem() marks it as a system actor,
	// the death watch's cluster cleanup on termination is skipped for system actors - leaking
	// one cluster-directory entry per SubscribeTopic/Unsubscribe cycle for the lifetime of the
	// cluster, which matters for exactly the churn-heavy gateway workloads this API targets.
	pid, err := x.configPID(context.Background(), name,
		newPubSubBridgeActor(topic, topicActorPID, handler),
		asSystem(),
		WithLongLived(),
		WithRelocationDisabled(),
		WithSupervisor(
			supervisor.NewSupervisor(
				supervisor.WithStrategy(supervisor.OneForOneStrategy),
				supervisor.WithAnyErrorDirective(supervisor.ResumeDirective),
			),
		),
	)
	if err != nil {
		return nil, err
	}

	if err := x.actors.addNode(x.systemGuardian, pid); err != nil {
		return nil, err
	}
	x.actors.addWatcher(pid, x.deathWatch)

	return &pubsubBridgeSubscription{topic: topic, pid: pid}, nil
}
