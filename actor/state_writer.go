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

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/internalpb"
)

const stateTopic = "goakt.state"

type stateWriter struct{}

var _ Actor = (*stateWriter)(nil)

func newStateWriter() Actor {
	return &stateWriter{}
}

func (s *stateWriter) PostStop(ctx *Context) error {
	ctx.ActorSystem().Logger().Infof("%s stopped successfully", ctx.ActorName())
	return nil
}

func (s *stateWriter) PreStart(*Context) error {
	return nil
}

func (s *stateWriter) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *goaktpb.PostStart:
		ctx.Logger().Debugf("subscribing to state topic %s", stateTopic)
		topicActor := ctx.ActorSystem().TopicActor()
		ctx.Tell(topicActor, &goaktpb.Subscribe{Topic: stateTopic})
	case *goaktpb.SubscribeAck:
		ctx.Logger().Debugf("subscribed to topic %s", msg.GetTopic())
	case *goaktpb.UnsubscribeAck:
		ctx.Logger().Debugf("unsubscribed from topic %s", msg.GetTopic())
	case *internalpb.PersistPeerActor:
		cluster := ctx.ActorSystem().getCluster()
		ctx.Err(cluster.PersistPeerActor(ctx.withoutCancel(), msg))
	case *internalpb.PersistPeerGrain:
		cluster := ctx.ActorSystem().getCluster()
		ctx.Err(cluster.PersistPeerGrain(ctx.withoutCancel(), msg))
	case *internalpb.RemovePeerActor:
		cluster := ctx.ActorSystem().getCluster()
		ctx.Err(cluster.RemovePeerActor(ctx.withoutCancel(), msg.GetActorName(), msg.GetPeerAddress()))
	case *internalpb.RemovePeerGrain:
		cluster := ctx.ActorSystem().getCluster()
		ctx.Err(cluster.RemovePeerGrain(ctx.withoutCancel(), msg.GetGrainId(), msg.GetPeerAddress()))
	default:
		ctx.Unhandled()
	}
}

// spawnStateWriter spawns the state writer actor
func (x *actorSystem) spawnStateWriter(ctx context.Context) error {
	if !x.clusterEnabled.Load() {
		return nil
	}

	if !x.relocationEnabled.Load() {
		return nil
	}

	actorName := x.reservedName(stateWriterType)
	x.stateWriter, _ = x.configPID(ctx,
		actorName,
		newStateWriter(),
		asSystem(),
		WithLongLived(),
		WithSupervisor(
			NewSupervisor(
				WithStrategy(OneForOneStrategy),
				WithAnyErrorDirective(ResumeDirective),
			),
		),
	)

	// the state writer is a child actor of the system guardian
	return x.actors.addNode(x.systemGuardian, x.stateWriter)
}

func (x *actorSystem) publishPersistPeerActor(ctx context.Context, actor *internalpb.Actor) error {
	return x.publish(ctx, getPublication(&internalpb.PersistPeerActor{
		Actor:        actor,
		Host:         x.Host(),
		RemotingPort: int32(x.Port()),
		PeersPort:    int32(x.PeersPort()),
	}))
}

func (x *actorSystem) publishPersistPeerGrain(ctx context.Context, grain *internalpb.Grain) error {
	return x.publish(ctx, getPublication(&internalpb.PersistPeerGrain{
		Grain:        grain,
		Host:         x.Host(),
		RemotingPort: int32(x.Port()),
		PeersPort:    int32(x.PeersPort()),
	}))
}

func (x *actorSystem) removePeerActor(ctx context.Context, actorName string) error {
	return x.publish(ctx, getPublication(&internalpb.RemovePeerActor{
		PeerAddress: x.PeerAddress(),
		ActorName:   actorName,
	}))
}

func (x *actorSystem) removePeerGrain(ctx context.Context, grainID *internalpb.GrainId) error {
	return x.publish(ctx, getPublication(&internalpb.RemovePeerGrain{
		PeerAddress: x.PeerAddress(),
		GrainId:     grainID,
	}))
}

func getPublication(message proto.Message) *goaktpb.Publish {
	anyMg, _ := anypb.New(message)
	return &goaktpb.Publish{
		Id:      uuid.NewString(),
		Topic:   stateTopic,
		Message: anyMg,
	}
}

func (x *actorSystem) publish(ctx context.Context, message *goaktpb.Publish) error {
	topicActor := x.TopicActor()
	noSender := x.NoSender()
	return noSender.Tell(ctx, topicActor, message)
}
