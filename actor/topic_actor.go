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
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/collection/syncmap"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/types"
	"github.com/tochemey/goakt/v3/log"
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
	topics    *syncmap.Map[string, *syncmap.Map[string, *PID]]
	processed *syncmap.Map[key, types.Unit]
	logger    log.Logger

	cluster     cluster.Interface
	actorSystem ActorSystem
	remoting    *Remoting
}

// ensure topic actor implements the Actor interface
var _ Actor = (*topicActor)(nil)

// newTopicActor creates a new cluster pubsub mediator.
func newTopicActor(remoting *Remoting) Actor {
	return &topicActor{
		topics:    syncmap.New[string, *syncmap.Map[string, *PID]](),
		processed: syncmap.New[key, types.Unit](),
		remoting:  remoting,
	}
}

// PreStart is called before the actor starts
func (x *topicActor) PreStart(context.Context) error {
	x.topics.Reset()
	x.processed.Reset()
	return nil
}

// Receive is called to process the message
func (x *topicActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		x.handlePostStart(ctx)
	case *goaktpb.Subscribe:
		x.handleSubscribe(ctx)
	case *goaktpb.Unsubscribe:
		x.handleUnsubscribe(ctx)
	case *goaktpb.Publish:
		x.handlePublish(ctx)
	case *internalpb.Disseminate:
		x.handleDisseminate(ctx)
	default:
		ctx.Unhandled()
	}
}

// PostStop is called when the actor is stopped
func (x *topicActor) PostStop(context.Context) error {
	x.topics.Reset()
	x.processed.Reset()
	x.logger.Infof("%s stopped successfully", x.pid.Name())
	return nil
}

// handlePublish handles Publish message
func (x *topicActor) handlePublish(ctx *ReceiveContext) {
	if publish, ok := ctx.Message().(*goaktpb.Publish); ok {
		topic := publish.GetTopic()
		message := publish.GetMessage()
		messageID := publish.GetId()
		senderID := ctx.Sender().ID()

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
		x.processed.Set(id, types.Unit{})

		cctx := context.WithoutCancel(ctx.Context())
		peers, err := x.cluster.Peers(cctx)
		if err != nil {
			ctx.Err(NewInternalError(err))
			return
		}

		remotePeers := x.buildRemotePeers(peers)
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
		wg.Add(1)
		go func() {
			defer wg.Done()
			x.sendToRemoteSubscribers(cctx, remotePeers, actorName, messageID, topic, message, &wg)
		}()

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

func (x *topicActor) sendToRemoteSubscribers(cctx context.Context, remotePeers []remotePeer, actorName, messageID, topic string, message *anypb.Any, wg *sync.WaitGroup) {
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

				toSend := &internalpb.Disseminate{
					Id:      messageID,
					Topic:   topic,
					Message: message,
				}

				from := x.pid.Address()
				if err := x.remoting.RemoteTell(cctx, from, to, toSend); err != nil {
					x.logger.Warnf("failed to publish message to actor %s on remote=[host=%s, port=%d]: %s",
						actorName, peer.host, peer.port, err.Error())
				}

				x.logger.Debugf("successfully published message to actor %s on remote=[host=%s, port=%d]",
					actorName, peer.host, peer.port)
			}(peer)
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
			ctx.Tell(sender, &goaktpb.SubscribeAck{Topic: topic})
			return
		}

		// here the topic does not exist
		subscribers := syncmap.New[string, *PID]()
		subscribers.Set(sender.ID(), sender)
		x.topics.Set(topic, subscribers)
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

// handleDisseminate sends a cluster pubsub message to the local
// subscribers of the given message topic.
// Disseminate message is sent by another TopicActor.
// If we already processed the message we discard it.
func (x *topicActor) handleDisseminate(ctx *ReceiveContext) {
	if disseminate, ok := ctx.Message().(*internalpb.Disseminate); ok {
		topic := disseminate.GetTopic()
		message, _ := disseminate.GetMessage().UnmarshalNew()
		messageID := disseminate.GetId()
		senderID := ctx.RemoteSender().ID()

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
	// only start the singleton manager when clustering is enabled
	if !x.pubsubEnabled.Load() {
		return nil
	}

	actorName := x.reservedName(topicActorType)
	x.topicActor, _ = x.configPID(ctx,
		actorName,
		newTopicActor(x.remoting),
		WithSupervisor(
			NewSupervisor(
				WithStrategy(OneForOneStrategy),
				WithAnyErrorDirective(RestartDirective),
			),
		),
	)

	// the topic actor is a child actor of the system guardian
	_ = x.actors.addNode(x.systemGuardian, x.topicActor)
	return nil
}
