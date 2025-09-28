/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/log"
)

const peerStatesTopic = "goakt.peerStates.topic"

type peerStatesWriter struct {
	clusterStore cluster.Store
	logger       log.Logger
	hostAddress  string
}

var _ Actor = (*peerStatesWriter)(nil)

func newPeerStatesWriter() Actor {
	return &peerStatesWriter{}
}

func (s *peerStatesWriter) PostStop(ctx *Context) error {
	if err := s.clusterStore.Close(); err != nil {
		return fmt.Errorf("%s failed to close cluster store: %w", ctx.ActorName(), err)
	}
	ctx.ActorSystem().Logger().Infof("%s cluster store closed successfully", ctx.ActorName())
	return nil
}

func (s *peerStatesWriter) PreStart(ctx *Context) error {
	ctx.ActorSystem().Logger().Infof("%s starting cluster storage...", ctx.ActorName())
	var err error
	s.clusterStore, err = cluster.NewBoltStore()
	if err != nil {
		return fmt.Errorf("%s initialize cluster store: %w", ctx.ActorName(), err)
	}
	ctx.ActorSystem().Logger().Infof("%s started cluster storage successfully", ctx.ActorName())
	return nil
}

func (s *peerStatesWriter) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *goaktpb.PostStart:
		s.handlePostStart(ctx)
	case *goaktpb.SubscribeAck:
		ctx.Logger().Debugf("subscribed to topic %s", msg.GetTopic())
	case *internalpb.PersistPeerActor:
		s.handlePersistPeerActor(ctx)
	case *internalpb.PersistPeerGrain:
		s.handlePersistPeerGrain(ctx)
	case *internalpb.RemovePeerActor:
		s.handleRemovePeerActor(ctx)
	case *internalpb.RemovePeerGrain:
		s.handleRemovePeerGrain(ctx)
	case *internalpb.GetPeerState:
		s.handleGetPeerState(ctx)
	case *internalpb.DeletePeerState:
		s.handleDeletePeerState(ctx)
	default:
		ctx.Unhandled()
	}
}

func (s *peerStatesWriter) handlePostStart(ctx *ReceiveContext) {
	s.hostAddress = ctx.ActorSystem().PeersAddress()
	s.logger = ctx.Logger()
	s.logger.Debugf("subscribing to state topic %s", peerStatesTopic)
	topicActor := ctx.ActorSystem().TopicActor()
	ctx.Tell(topicActor, &goaktpb.Subscribe{Topic: peerStatesTopic})
}

func (s *peerStatesWriter) handleGetPeerState(ctx *ReceiveContext) {
	msg := ctx.Message().(*internalpb.GetPeerState)
	peerState, err := s.getPeerState(ctx.withoutCancel(), msg.GetPeerAddress())
	if err != nil {
		ctx.Err(err)
		return
	}
	ctx.Response(peerState)
}

func (s *peerStatesWriter) handleDeletePeerState(ctx *ReceiveContext) {
	msg := ctx.Message().(*internalpb.DeletePeerState)
	if err := s.deletePeerState(ctx.withoutCancel(), msg.GetPeerAddress()); err != nil {
		ctx.Err(err)
		return
	}
	ctx.Response(new(goaktpb.NoMessage))
}

func (s *peerStatesWriter) handlePersistPeerActor(ctx *ReceiveContext) {
	msg := ctx.Message().(*internalpb.PersistPeerActor)
	if err := s.persistPeerActor(ctx.withoutCancel(), msg); err != nil {
		ctx.Err(err)
	}
}

func (s *peerStatesWriter) handlePersistPeerGrain(ctx *ReceiveContext) {
	msg := ctx.Message().(*internalpb.PersistPeerGrain)
	if err := s.persistPeerGrain(ctx.withoutCancel(), msg); err != nil {
		ctx.Err(err)
	}
}

func (s *peerStatesWriter) handleRemovePeerActor(ctx *ReceiveContext) {
	msg := ctx.Message().(*internalpb.RemovePeerActor)
	if err := s.removePeerActor(ctx.withoutCancel(), msg.GetActorName(), msg.GetPeerAddress()); err != nil {
		ctx.Err(err)
	}
}

func (s *peerStatesWriter) handleRemovePeerGrain(ctx *ReceiveContext) {
	msg := ctx.Message().(*internalpb.RemovePeerGrain)
	if err := s.removePeerGrain(ctx.withoutCancel(), msg.GetGrainId(), msg.GetPeerAddress()); err != nil {
		ctx.Err(err)
	}
}

func (s *peerStatesWriter) persistPeerActor(ctx context.Context, state *internalpb.PersistPeerActor) error {
	peerAddress := net.JoinHostPort(state.GetHost(), strconv.Itoa(int(state.GetPeersPort())))
	if s.hostAddress == peerAddress {
		return nil
	}

	s.logger.Infof("(%s) processing peer=(%s)'s state", s.hostAddress, peerAddress)
	peerState := s.getOrInitPeerState(ctx, state.GetHost(), state.GetRemotingPort(), state.GetPeersPort())
	actors := peerState.GetActors()
	if actors == nil {
		actors = map[string]*internalpb.Actor{}
	}

	key := state.GetActor().GetAddress().GetName()
	actors[key] = state.GetActor()
	peerState.Actors = actors

	s.logger.Debugf("(%s) persisting peer=(%s)'s state: [Actors count=(%d), Grains count=(%d)]", s.hostAddress, peerAddress, len(peerState.GetActors()), len(peerState.GetGrains()))
	if err := s.clusterStore.PersistPeerState(ctx, peerState); err != nil {
		return fmt.Errorf("persist peer state (actors) [%s]: %w", peerAddress, err)
	}

	s.logger.Infof("(%s) processed peer=(%s) successfully ", s.hostAddress, peerAddress)
	return nil
}

func (s *peerStatesWriter) persistPeerGrain(ctx context.Context, state *internalpb.PersistPeerGrain) error {
	peerAddress := net.JoinHostPort(state.GetHost(), strconv.Itoa(int(state.GetPeersPort())))

	if s.hostAddress == peerAddress {
		return nil
	}

	s.logger.Infof("(%s) processing peer=(%s)'s state", s.hostAddress, peerAddress)
	peerState := s.getOrInitPeerState(ctx, state.GetHost(), state.GetRemotingPort(), state.GetPeersPort())
	grains := peerState.GetGrains()
	if grains == nil {
		grains = map[string]*internalpb.Grain{}
	}

	key := state.GetGrain().GetGrainId().GetValue()
	grains[key] = state.GetGrain()
	peerState.Grains = grains

	s.logger.Debugf("(%s) persisting peer=(%s)'s state: [Actors count=(%d), Grains count=(%d)]", s.hostAddress, peerAddress, len(peerState.GetActors()), len(peerState.GetGrains()))
	if err := s.clusterStore.PersistPeerState(ctx, peerState); err != nil {
		return fmt.Errorf("persist peer state (grains) [%s]: %w", peerAddress, err)
	}

	s.logger.Infof("(%s) processed peer=(%s) successfully ", s.hostAddress, peerAddress)
	return nil
}

func (s *peerStatesWriter) removePeerActor(ctx context.Context, actorName string, peerAddress string) error {
	if s.hostAddress == peerAddress {
		return nil
	}

	peerState, ok := s.clusterStore.GetPeerState(ctx, peerAddress)
	if !ok {
		return nil
	}

	actors := peerState.GetActors()
	delete(actors, actorName)

	s.logger.Debugf("(%s) persisting peer=(%s)'s state: [Actors count=(%d), Grains count=(%d)]", s.hostAddress, peerAddress, len(peerState.GetActors()), len(peerState.GetGrains()))
	if err := s.clusterStore.PersistPeerState(ctx, peerState); err != nil {
		return fmt.Errorf("persist peer state (grains) [%s]: %w", peerAddress, err)
	}

	s.logger.Infof("(%s) processed peer=(%s) successfully ", s.hostAddress, peerAddress)
	return nil
}

func (s *peerStatesWriter) removePeerGrain(ctx context.Context, grainID *internalpb.GrainId, peerAddress string) error {
	if s.hostAddress == peerAddress {
		return nil
	}

	peerState, ok := s.clusterStore.GetPeerState(ctx, peerAddress)
	if !ok {
		return nil
	}

	grains := peerState.GetGrains()
	delete(grains, grainID.GetValue())

	s.logger.Debugf("(%s) persisting peer=(%s)'s state: [Actors count=(%d), Grains count=(%d)]", s.hostAddress, peerAddress, len(peerState.GetActors()), len(peerState.GetGrains()))
	if err := s.clusterStore.PersistPeerState(ctx, peerState); err != nil {
		return fmt.Errorf("persist peer state (grains) [%s]: %w", peerAddress, err)
	}

	s.logger.Infof("(%s) processed peer=(%s) successfully ", s.hostAddress, peerAddress)
	return nil
}

// getOrInitPeerState fetches an existing peer state or initializes a new one.
func (s *peerStatesWriter) getOrInitPeerState(ctx context.Context, host string, remotingPort, peersPort int32) *internalpb.PeerState {
	address := net.JoinHostPort(host, strconv.Itoa(int(peersPort)))
	if ps, ok := s.clusterStore.GetPeerState(ctx, address); ok {
		return ps
	}
	return &internalpb.PeerState{
		Host:         host,
		RemotingPort: remotingPort,
		PeersPort:    peersPort,
	}
}

// getPeerState returns the peer state tracked for the requested address.
func (s *peerStatesWriter) getPeerState(ctx context.Context, peerAddress string) (*internalpb.PeerState, error) {
	peerState, ok := s.clusterStore.GetPeerState(ctx, peerAddress)
	if !ok {
		return nil, errors.New("peer state not found") // TODO review this
	}

	return peerState, nil
}

// deletePeerState removes any stored state for the given peer address.
func (s *peerStatesWriter) deletePeerState(ctx context.Context, peerAddress string) error {
	return s.clusterStore.DeletePeerState(ctx, peerAddress)
}

// spawnPeerStatesWriter spawns the state writer actor
func (x *actorSystem) spawnPeerStatesWriter(ctx context.Context) error {
	if !x.clusterEnabled.Load() {
		return nil
	}

	if !x.relocationEnabled.Load() {
		return nil
	}

	actorName := x.reservedName(peersStatesWriterType)
	x.peerStatesWriter, _ = x.configPID(ctx,
		actorName,
		newPeerStatesWriter(),
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
	return x.actors.addNode(x.systemGuardian, x.peerStatesWriter)
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
		PeerAddress: x.PeersAddress(),
		ActorName:   actorName,
	}))
}

func (x *actorSystem) removePeerGrain(ctx context.Context, grainID *internalpb.GrainId) error {
	return x.publish(ctx, getPublication(&internalpb.RemovePeerGrain{
		PeerAddress: x.PeersAddress(),
		GrainId:     grainID,
	}))
}

func getPublication(message proto.Message) *goaktpb.Publish {
	anyMg, _ := anypb.New(message)
	return &goaktpb.Publish{
		Id:      uuid.NewString(),
		Topic:   peerStatesTopic,
		Message: anyMg,
	}
}

func (x *actorSystem) publish(ctx context.Context, message *goaktpb.Publish) error {
	topicActor := x.TopicActor()
	noSender := x.NoSender()
	return noSender.Tell(ctx, topicActor, message)
}
