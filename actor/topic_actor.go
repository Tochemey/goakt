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
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/ds"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/supervisor"
)

type remotePeer struct {
	host string
	port int
}

// key is a struct that holds the track id of a message
type key struct {
	senderID  string
	topic     string
	messageID string
}

// topicActor is a system actor that manages a registry of actors that subscribe to topics.
// This actor must be started when cluster mode is enabled in all nodesMap before any actor subscribes
type topicActor struct {
	pid *PID
	// topics holds the list of all topics and their subscribers
	topics    *ds.Map[string, *ds.Map[string, *PID]]
	processed *ds.Map[key, registry.Unit]
	logger    log.Logger

	cluster     cluster.Cluster
	actorSystem ActorSystem
	remoting    remote.Remoting
}

// ensure topic actor implements the Actor interface
var _ Actor = (*topicActor)(nil)

// newTopicActor creates a new cluster pubsub mediator.
func newTopicActor(remoting remote.Remoting) Actor {
	return &topicActor{
		topics:    ds.NewMap[string, *ds.Map[string, *PID]](),
		processed: ds.NewMap[key, registry.Unit](),
		remoting:  remoting,
	}
}

// PreStart is called before the actor starts
func (x *topicActor) PreStart(*Context) error {
	x.topics.Reset()
	x.processed.Reset()
	return nil
}

// Receive is called to process the message
func (x *topicActor) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *goaktpb.PostStart:
		x.handlePostStart(ctx)
	case *goaktpb.Subscribe:
		x.handleSubscribe(ctx)
	case *goaktpb.Unsubscribe:
		x.handleUnsubscribe(ctx)
	case *goaktpb.Publish:
		x.handlePublish(ctx)
	case *internalpb.TopicMessage:
		x.handleTopicMessage(ctx)
	case *goaktpb.Terminated:
		x.handleTerminated(msg)
	default:
		ctx.Unhandled()
	}
}

// PostStop is called when the actor is stopped
func (x *topicActor) PostStop(ctx *Context) error {
	x.topics.Reset()
	x.processed.Reset()
	ctx.ActorSystem().Logger().Infof("%s stopped successfully", ctx.ActorName())
	return nil
}

// handlePublish handles Publish message
func (x *topicActor) handlePublish(ctx *ReceiveContext) {
	if publish, ok := ctx.Message().(*goaktpb.Publish); ok {
		topic := publish.GetTopic()
		message := publish.GetMessage()
		messageID := publish.GetId()

		var senderID string
		if sender := ctx.Sender(); sender != nil {
			senderID = sender.ID()
		}

		id := key{
			senderID:  senderID,
			topic:     topic,
			messageID: messageID,
		}

		// we don't want to process the same message twice
		if _, ok := x.processed.Get(id); ok {
			return
		}

		// mark the message as processed
		x.processed.Set(id, registry.Unit{})

		cctx := context.WithoutCancel(ctx.Context())
		var wg sync.WaitGroup
		actorName := x.actorSystem.reservedName(topicActorType)

		// this will be sent to the local subscribers
		msg, _ := message.UnmarshalNew()

		// send the message to all local subscribers in a separate goroutine
		wg.Add(1)
		go func() {
			defer wg.Done()
			x.sendToLocalSubscribers(cctx, topic, msg, &wg)
		}()

		// send the message to all remote subscribers in a separate goroutine
		// this can only be done if the actor system is clustered
		if x.actorSystem.InCluster() {
			peers, err := x.cluster.Peers(cctx)
			if err != nil {
				ctx.Err(errors.NewInternalError(err))
				return
			}

			remotePeers := x.buildRemotePeers(peers)

			wg.Add(1)
			go func() {
				defer wg.Done()
				x.sendToRemoteTopicActors(cctx, remotePeers, actorName, messageID, topic, message, &wg)
			}()
		}

		// wait for all messages to be sent to all subscribers
		wg.Wait()
	}
}

func (x *topicActor) buildRemotePeers(peers []*cluster.Peer) []remotePeer {
	var remotePeers []remotePeer
	for _, peer := range peers {
		remotePeers = append(remotePeers, remotePeer{
			host: peer.Host,
			port: peer.RemotingPort,
		})
	}
	return remotePeers
}

func (x *topicActor) sendToLocalSubscribers(cctx context.Context, topic string, msg proto.Message, wg *sync.WaitGroup) {
	if subscribers, ok := x.topics.Get(topic); ok && subscribers.Len() != 0 {
		for _, subscriber := range subscribers.Values() {
			subscriber := subscriber
			// make sure subscriber does exist
			_, ok := x.actorSystem.tree().node(subscriber.ID())
			if ok && subscriber.IsRunning() {
				wg.Add(1)
				go func(subscriber *PID) {
					defer wg.Done()
					if err := x.pid.Tell(cctx, subscriber, msg); err != nil {
						x.logger.Warnf("failed to publish message to local actor %s: %s",
							subscriber.Name(), err.Error())
					}
				}(subscriber)
			} else {
				// remove the subscriber if it does not exist
				subscribers.Delete(subscriber.ID())
			}
		}
	}
}

func (x *topicActor) sendToRemoteTopicActors(cctx context.Context, remotePeers []remotePeer, actorName, messageID, topic string, message *anypb.Any, wg *sync.WaitGroup) {
	if len(remotePeers) > 0 {
		for _, peer := range remotePeers {
			peer := peer
			wg.Add(1)
			go func(peer remotePeer) {
				defer wg.Done()
				to, err := x.remoting.RemoteLookup(cctx, peer.host, peer.port, actorName)
				if err != nil {
					x.logger.Warnf("failed to lookup actor %s on remote=[host=%s, port=%d]: %s",
						actorName, peer.host, peer.port, err.Error())
					return
				}

				toSend := &internalpb.TopicMessage{
					Id:      messageID,
					Topic:   topic,
					Message: message,
				}

				from := x.pid.Address()
				if err := x.remoting.RemoteTell(cctx, from, to, toSend); err != nil {
					x.logger.Warnf("failed to publish message to actor %s on remote=[host=%s, port=%d]: %s",
						actorName, peer.host, peer.port, err.Error())
					return
				}

				x.logger.Debugf("successfully published message to actor %s on remote=[host=%s, port=%d]",
					actorName, peer.host, peer.port)
			}(peer)
		}
	}
}

// handleTerminated handles Terminated message
// This is called when a subscriber actor is terminated.
// We remove the subscriber from all topics it is subscribed to.
// This is important to avoid memory leaks and ensure that we do not send messages to terminated actors.
func (x *topicActor) handleTerminated(msg *goaktpb.Terminated) {
	for topic, subscribers := range x.topics.Values() {
		// remove the subscriber from the topics
		actorID := msg.GetAddress()
		if subscriber, ok := subscribers.Get(actorID); ok {
			subscribers.Delete(subscriber.ID())
			x.logger.Debugf("removed actor %s from topic %s", subscriber.Name(), topic)
		}
	}
}

// handleUnsubscribe handles Unsubscribe message
func (x *topicActor) handleUnsubscribe(ctx *ReceiveContext) {
	sender := ctx.Sender()
	if message, ok := ctx.Message().(*goaktpb.Unsubscribe); ok {
		topic := message.GetTopic()
		if subscribers, ok := x.topics.Get(topic); ok {
			subscribers.Delete(sender.ID())
			ctx.Tell(sender, &goaktpb.UnsubscribeAck{Topic: topic})
		}
	}
}

// handleSubscribe handles Subscribe message
func (x *topicActor) handleSubscribe(ctx *ReceiveContext) {
	sender := ctx.Sender()
	if message, ok := ctx.Message().(*goaktpb.Subscribe); ok && sender.IsRunning() {
		topic := message.GetTopic()
		// check if the topic exists
		if subscribers, ok := x.topics.Get(topic); ok && subscribers.Len() != 0 {
			subscribers.Set(sender.ID(), sender)
			ctx.Watch(sender)
			ctx.Tell(sender, &goaktpb.SubscribeAck{Topic: topic})
			return
		}

		// here the topic does not exist
		subscribers := ds.NewMap[string, *PID]()
		subscribers.Set(sender.ID(), sender)
		x.topics.Set(topic, subscribers)
		ctx.Watch(sender)
		ctx.Tell(sender, &goaktpb.SubscribeAck{Topic: topic})
	}
}

// handlePostStart handles PostStart message
func (x *topicActor) handlePostStart(ctx *ReceiveContext) {
	x.pid = ctx.Self()
	x.logger = ctx.Logger()
	x.cluster = ctx.ActorSystem().getCluster()
	x.actorSystem = ctx.ActorSystem()
	x.logger.Infof("%s started successfully", x.pid.Name())
}

// handleTopicMessage sends a cluster pubsub message to the local
// subscribers of the given message topic.
// Disseminate message is sent by another TopicActor.
// If we already processed the message we discard it.
func (x *topicActor) handleTopicMessage(ctx *ReceiveContext) {
	if topicMessage, ok := ctx.Message().(*internalpb.TopicMessage); ok {
		topic := topicMessage.GetTopic()
		message, _ := topicMessage.GetMessage().UnmarshalNew()
		messageID := topicMessage.GetId()
		senderID := ctx.RemoteSender().String()

		id := key{
			senderID:  senderID,
			topic:     topic,
			messageID: messageID,
		}

		// we don't want to process the same message twice
		if _, ok := x.processed.Get(id); ok {
			return
		}

		cctx := context.WithoutCancel(ctx.Context())
		// send the message to all local subscribers
		if subscribers, ok := x.topics.Get(topic); ok && subscribers.Len() != 0 {
			var wg sync.WaitGroup
			for _, subscriber := range subscribers.Values() {
				subscriber := subscriber
				// make sure subcriber does exist
				_, ok := x.actorSystem.tree().node(subscriber.ID())
				if ok && subscriber.IsRunning() {
					wg.Add(1)
					go func(subscriber *PID) {
						defer wg.Done()
						if err := x.pid.Tell(cctx, subscriber, message); err != nil {
							x.logger.Warnf("failed to publish message to local actor %s: %s", subscriber.Name(), err.Error())
						}
					}(subscriber)
				} else {
					// remove the subscriber if it does not exist
					subscribers.Delete(subscriber.ID())
				}
			}
			// wait for all messages to be sent to all subscribers
			wg.Wait()
		}
	}
}

// spawnTopicActor spawns a new topic actor
func (x *actorSystem) spawnTopicActor(ctx context.Context) error {
	// only start the topic actor when cluster is enabled or pubsub is enabled
	if !x.clusterEnabled.Load() && !x.pubsubEnabled.Load() {
		return nil
	}

	actorName := x.reservedName(topicActorType)
	x.topicActor, _ = x.configPID(ctx,
		actorName,
		newTopicActor(x.remoting),
		asSystem(),
		WithLongLived(),
		WithSupervisor(
			supervisor.NewSupervisor(
				supervisor.WithStrategy(supervisor.OneForOneStrategy),
				supervisor.WithAnyErrorDirective(supervisor.ResumeDirective),
			),
		),
	)

	// the topic actor is a child actor of the system guardian
	return x.actors.addNode(x.systemGuardian, x.topicActor)
}
