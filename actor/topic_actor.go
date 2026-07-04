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
	"time"

	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/supervisor"
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

// topicPresenceQuery asks the local topic actor for the cluster-wide
// subscriber addresses of a topic. Unlike internalpb.TopicPresenceRequest
// (the wire-level message peers exchange), this query is only ever sent
// in-process and triggers the fan-out to every peer's topic actor.
type topicPresenceQuery struct {
	topic   string
	timeout time.Duration
}

// topicPresenceReply carries the cluster-wide subscriber addresses gathered
// in response to a topicPresenceQuery.
type topicPresenceReply struct {
	subscribers []string
}

// topicListQuery asks the local topic actor for the cluster-wide set of
// active topics. Unlike internalpb.TopicListRequest (the wire-level message
// peers exchange), this query is only ever sent in-process and triggers the
// fan-out to every peer's topic actor.
type topicListQuery struct {
	timeout time.Duration
}

// topicListReply carries the cluster-wide topic names gathered in response
// to a topicListQuery.
type topicListReply struct {
	topics []string
}

// topicActor is a system actor that manages a registry of actors that subscribe to topics.
// This actor must be started when cluster mode is enabled in all nodesMap before any actor subscribes
type topicActor struct {
	pid *PID
	// topics holds the list of all topics and their subscribers
	topics *xsync.Map[string, *xsync.Map[string, *PID]]
	// processed deduplicates recently delivered messages. Entries expire after
	// the configured retention window so the map stays bounded under sustained
	// publishing instead of growing for the lifetime of the actor system.
	processed *xsync.TTLMap[key, types.Unit]
	logger    log.Logger

	cluster     cluster.Cluster
	actorSystem ActorSystem
	remoting    remoteclient.Client
}

// ensure topic actor implements the Actor interface
var _ Actor = (*topicActor)(nil)

// newTopicActor creates a new cluster pubsub mediator.
//
// retention bounds how long a delivered message identifier is remembered for
// deduplication before its entry expires.
func newTopicActor(remoting remoteclient.Client, retention time.Duration) Actor {
	return &topicActor{
		topics:    xsync.NewMap[string, *xsync.Map[string, *PID]](),
		processed: xsync.NewTTLMap[key, types.Unit](retention),
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
	case *PostStart:
		x.handlePostStart(ctx)
	case *Subscribe:
		x.handleSubscribe(ctx)
	case *Unsubscribe:
		x.handleUnsubscribe(ctx)
	case *Publish:
		x.handlePublish(ctx)
	case *internalpb.TopicMessage:
		x.handleTopicMessage(ctx)
	case *topicPresenceQuery:
		x.handleTopicPresenceQuery(ctx)
	case *internalpb.TopicPresenceRequest:
		x.handleTopicPresenceRequest(ctx)
	case *topicListQuery:
		x.handleTopicListQuery(ctx)
	case *internalpb.TopicListRequest:
		x.handleTopicListRequest(ctx)
	case *Terminated:
		x.handleTerminated(msg)
	default:
		ctx.Unhandled()
	}
}

// PostStop is called when the actor is stopped
func (x *topicActor) PostStop(ctx *Context) error {
	x.topics.Reset()
	x.processed.Reset()
	ctx.ActorSystem().Logger().Infof("actor=%s stopped successfully", ctx.ActorName())
	return nil
}

// handlePublish handles Publish message
func (x *topicActor) handlePublish(ctx *ReceiveContext) {
	if publish, ok := ctx.Message().(*Publish); ok {
		topic := publish.Topic()
		message := publish.Message()
		messageID := publish.ID()

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
		x.processed.Set(id, types.Unit{})

		cctx := context.WithoutCancel(ctx.Context())
		var wg sync.WaitGroup
		actorName := reservedName(topicActorType)

		// this will be sent to the local subscribers
		msg := message

		// send the message to all local subscribers in a separate goroutine
		wg.Go(func() {
			x.sendToLocalSubscribers(cctx, topic, msg, &wg)
		})

		// send the message to all remote subscribers in a separate goroutine
		// this can only be done if the actor system is clustered
		if x.actorSystem.InCluster() {
			peers, err := x.cluster.Peers(cctx)
			if err != nil {
				ctx.Err(errors.NewInternalError(err))
				return
			}

			remotePeers := buildRemotePeers(peers)

			wg.Go(func() {
				x.sendToRemoteTopicActors(cctx, remotePeers, actorName, messageID, topic, message, &wg)
			})
		}

		// wait for all messages to be sent to all subscribers
		wg.Wait()
	}
}

func buildRemotePeers(peers []*cluster.Peer) []remotePeer {
	var remotePeers []remotePeer
	for _, peer := range peers {
		remotePeers = append(remotePeers, remotePeer{
			host: peer.Host,
			port: peer.RemotingPort,
		})
	}
	return remotePeers
}

func (x *topicActor) sendToLocalSubscribers(cctx context.Context, topic string, msg any, wg *sync.WaitGroup) {
	if subscribers, ok := x.topics.Get(topic); ok && subscribers.Len() != 0 {
		for _, subscriber := range subscribers.Values() {
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

func (x *topicActor) sendToRemoteTopicActors(cctx context.Context, remotePeers []remotePeer, actorName, messageID, topic string, message any, wg *sync.WaitGroup) {
	if len(remotePeers) > 0 {
		for _, peer := range remotePeers {
			wg.Add(1)
			go func(peer remotePeer) {
				defer wg.Done()
				to, err := x.remoting.RemoteLookup(cctx, peer.host, peer.port, actorName)
				if err != nil {
					x.logger.Warnf("failed to lookup actor %s on remote=[host=%s, port=%d]: %s",
						actorName, peer.host, peer.port, err.Error())
					return
				}

				serializer := x.remoting.Serializer(message)
				if serializer == nil {
					x.logger.Warnf("failed to get serializer for message type %T", message)
					return
				}

				marshaled, err := serializer.Serialize(message)
				if err != nil {
					x.logger.Warnf("failed to serialize message: %s", err.Error())
					return
				}

				toSend := &internalpb.TopicMessage{
					Id:      messageID,
					Topic:   topic,
					Message: marshaled,
				}

				from := pathToAddress(x.pid.Path())
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

// localSubscribers returns the canonical addresses of the still-alive local
// subscribers of topic, pruning any that have terminated without going
// through handleTerminated (e.g. a stale entry left by a race with Watch).
func (x *topicActor) localSubscribers(topic string) []string {
	subscribers, ok := x.topics.Get(topic)
	if !ok {
		return nil
	}

	var ids []string
	for _, subscriber := range subscribers.Values() {
		_, exists := x.actorSystem.tree().node(subscriber.ID())
		if exists && subscriber.IsRunning() {
			ids = append(ids, subscriber.ID())
			continue
		}
		// remove the subscriber if it does not exist
		subscribers.Delete(subscriber.ID())
	}
	return ids
}

// localTopics returns the names of the topics that currently have at least
// one local subscriber.
func (x *topicActor) localTopics() []string {
	var topics []string
	x.topics.Range(func(topic string, subscribers *xsync.Map[string, *PID]) {
		if subscribers.Len() != 0 {
			topics = append(topics, topic)
		}
	})
	return topics
}

// handleTopicPresenceRequest answers a peer topic actor's query for this
// node's local subscribers of a topic. It never fans the request out any
// further: cluster-wide aggregation is driven by the querying node's
// handleTopicPresenceQuery.
func (x *topicActor) handleTopicPresenceRequest(ctx *ReceiveContext) {
	if request, ok := ctx.Message().(*internalpb.TopicPresenceRequest); ok {
		ctx.Response(&internalpb.TopicPresenceResponse{Subscribers: x.localSubscribers(request.GetTopic())})
	}
}

// handleTopicPresenceQuery aggregates the cluster-wide subscriber addresses
// of a topic: the local view plus a fan-out TopicPresenceRequest to every
// peer's topic actor, reusing the same peer discovery handlePublish uses to
// disseminate messages.
func (x *topicActor) handleTopicPresenceQuery(ctx *ReceiveContext) {
	query, ok := ctx.Message().(*topicPresenceQuery)
	if !ok {
		return
	}

	subscribers := x.localSubscribers(query.topic)

	if x.actorSystem.InCluster() {
		cctx := context.WithoutCancel(ctx.Context())
		peers, err := x.cluster.Peers(cctx)
		if err != nil {
			ctx.Err(errors.NewInternalError(err))
			return
		}

		remoteSubscribers := x.queryRemotePeers(cctx, buildRemotePeers(peers), &internalpb.TopicPresenceRequest{Topic: query.topic}, query.timeout,
			func(resp any) []string {
				presence, ok := resp.(*internalpb.TopicPresenceResponse)
				if !ok || presence == nil {
					return nil
				}
				return presence.GetSubscribers()
			})
		subscribers = append(subscribers, remoteSubscribers...)
	}

	ctx.Response(&topicPresenceReply{subscribers: subscribers})
}

// handleTopicListRequest answers a peer topic actor's query for this node's
// locally known topics. It never fans the request out any further:
// cluster-wide aggregation is driven by the querying node's
// handleTopicListQuery.
func (x *topicActor) handleTopicListRequest(ctx *ReceiveContext) {
	if _, ok := ctx.Message().(*internalpb.TopicListRequest); ok {
		ctx.Response(&internalpb.TopicListResponse{Topics: x.localTopics()})
	}
}

// handleTopicListQuery aggregates the cluster-wide set of active topics: the
// local view plus a fan-out TopicListRequest to every peer's topic actor.
func (x *topicActor) handleTopicListQuery(ctx *ReceiveContext) {
	query, ok := ctx.Message().(*topicListQuery)
	if !ok {
		return
	}

	// the same topic name can be active on more than one node, so dedupe
	// while merging the local view with each peer's.
	seen := make(map[string]types.Unit)
	var topics []string
	addTopics := func(names []string) {
		for _, name := range names {
			if _, exists := seen[name]; !exists {
				seen[name] = types.Unit{}
				topics = append(topics, name)
			}
		}
	}
	addTopics(x.localTopics())

	if x.actorSystem.InCluster() {
		cctx := context.WithoutCancel(ctx.Context())
		peers, err := x.cluster.Peers(cctx)
		if err != nil {
			ctx.Err(errors.NewInternalError(err))
			return
		}

		remoteTopics := x.queryRemotePeers(cctx, buildRemotePeers(peers), &internalpb.TopicListRequest{}, query.timeout,
			func(resp any) []string {
				list, ok := resp.(*internalpb.TopicListResponse)
				if !ok || list == nil {
					return nil
				}
				return list.GetTopics()
			})
		addTopics(remoteTopics)
	}

	ctx.Response(&topicListReply{topics: topics})
}

// queryRemotePeers asks every remote peer's topic actor the given request and
// merges the string values that extract returns out of each reply. Peers that
// cannot be reached or fail to answer within timeout are skipped: presence
// queries degrade to a partial view rather than failing outright.
func (x *topicActor) queryRemotePeers(cctx context.Context, remotePeers []remotePeer, request any, timeout time.Duration, extract func(resp any) []string) []string {
	if len(remotePeers) == 0 {
		return nil
	}

	actorName := reservedName(topicActorType)
	from := pathToAddress(x.pid.Path())

	var (
		mu      sync.Mutex
		results []string
		wg      sync.WaitGroup
	)

	for _, peer := range remotePeers {
		wg.Add(1)
		go func(peer remotePeer) {
			defer wg.Done()
			to, err := x.remoting.RemoteLookup(cctx, peer.host, peer.port, actorName)
			if err != nil {
				x.logger.Warnf("failed to lookup actor %s on remote=[host=%s, port=%d]: %s",
					actorName, peer.host, peer.port, err.Error())
				return
			}

			resp, err := x.remoting.RemoteAsk(cctx, from, to, request, timeout)
			if err != nil {
				x.logger.Warnf("failed to query topic actor %s on remote=[host=%s, port=%d]: %s",
					actorName, peer.host, peer.port, err.Error())
				return
			}

			values := extract(resp)
			if len(values) == 0 {
				return
			}

			mu.Lock()
			results = append(results, values...)
			mu.Unlock()
		}(peer)
	}

	wg.Wait()
	return results
}

// handleTerminated handles Terminated message
// This is called when a subscriber actor is terminated.
// We remove the subscriber from all topics it is subscribed to.
// This is important to avoid memory leaks and ensure that we do not send messages to terminated actors.
func (x *topicActor) handleTerminated(msg *Terminated) {
	actorID := msg.ActorPath().String()
	x.topics.Range(func(topic string, subscribers *xsync.Map[string, *PID]) {
		if subscriber, ok := subscribers.Get(actorID); ok {
			subscribers.Delete(subscriber.ID())
			x.logger.Debugf("removed actor=%s from topic=%s", subscriber.Name(), topic)
		}
	})
}

// handleUnsubscribe handles Unsubscribe message
func (x *topicActor) handleUnsubscribe(ctx *ReceiveContext) {
	sender := ctx.Sender()
	if message, ok := ctx.Message().(*Unsubscribe); ok {
		topic := message.Topic()
		if subscribers, ok := x.topics.Get(topic); ok {
			subscribers.Delete(sender.ID())
			ctx.Tell(sender, NewUnsubscribeAck(topic))
		}
	}
}

// handleSubscribe handles Subscribe message
func (x *topicActor) handleSubscribe(ctx *ReceiveContext) {
	sender := ctx.Sender()
	if message, ok := ctx.Message().(*Subscribe); ok && sender.IsRunning() {
		topic := message.Topic()
		// check if the topic exists
		if subscribers, ok := x.topics.Get(topic); ok && subscribers.Len() != 0 {
			subscribers.Set(sender.ID(), sender)
			ctx.Watch(sender)
			ctx.Tell(sender, NewSubscribeAck(topic))
			return
		}

		// here the topic does not exist
		subscribers := xsync.NewMap[string, *PID]()
		subscribers.Set(sender.ID(), sender)
		x.topics.Set(topic, subscribers)
		ctx.Watch(sender)
		ctx.Tell(sender, NewSubscribeAck(topic))
	}
}

// handlePostStart handles PostStart message
func (x *topicActor) handlePostStart(ctx *ReceiveContext) {
	x.pid = ctx.Self()
	x.logger = ctx.Logger()
	x.cluster = ctx.ActorSystem().getCluster()
	x.actorSystem = ctx.ActorSystem()
	x.logger.Infof("actor=%s started successfully", x.pid.Name())
}

// handleTopicMessage sends a cluster pubsub message to the local
// subscribers of the given message topic.
// Disseminate message is sent by another TopicActor.
// If we already processed the message we discard it.
func (x *topicActor) handleTopicMessage(ctx *ReceiveContext) {
	if topicMessage, ok := ctx.Message().(*internalpb.TopicMessage); ok {
		topic := topicMessage.Topic

		serializer := x.remoting.Serializer(nil)
		if serializer == nil {
			x.logger.Warnf("failed to get deserializer for topic message")
			return
		}

		message, err := serializer.Deserialize(topicMessage.Message)
		if err != nil {
			x.logger.Warnf("failed to deserialize message: %s", err.Error())
			return
		}

		messageID := topicMessage.GetId()
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

		cctx := context.WithoutCancel(ctx.Context())
		// send the message to all local subscribers
		if subscribers, ok := x.topics.Get(topic); ok && subscribers.Len() != 0 {
			var wg sync.WaitGroup
			for _, subscriber := range subscribers.Values() {
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

	actorName := reservedName(topicActorType)
	x.topicActor, _ = x.configPID(ctx,
		actorName,
		newTopicActor(x.remoting, x.messageRetention),
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
