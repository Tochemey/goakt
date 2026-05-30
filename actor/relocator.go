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
	"net"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/chunk"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/slices"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

// defaultRelocationConcurrency bounds the number of concurrent spawn and grain
// activation operations performed during a single cluster rebalance. It caps the
// fan-out of remote RPCs so relocating a node that hosted a large number of
// actors does not trigger an unbounded burst of goroutines and peer connections.
const defaultRelocationConcurrency = 10

// relocator is a system actor that helps rebalance cluster
// when the cluster topology changes
type relocator struct {
	remoting remoteclient.Client
	pid      *PID
	logger   log.Logger
}

// enforce compilation error
var _ Actor = (*relocator)(nil)

// newRelocator creates an instance of relocator
func newRelocator(remoting remoteclient.Client) *relocator {
	return &relocator{
		remoting: remoting,
	}
}

// PreStart pre-starts the actor.
func (r *relocator) PreStart(*Context) error {
	return nil
}

// Receive handles messages sent to the relocator
func (r *relocator) Receive(ctx *ReceiveContext) {
	if _, ok := ctx.Message().(*PostStart); ok {
		r.pid = ctx.Self()
		r.logger = ctx.Logger()
		r.logger.Infof("%s started successfully", r.pid.Name())
		ctx.Become(r.Relocate)
	}
}

// Relocate behavior
func (r *relocator) Relocate(ctx *ReceiveContext) {
	if msg, ok := ctx.Message().(*internalpb.Rebalance); ok {
		rctx := context.WithoutCancel(ctx.Context())
		peerState := msg.GetPeerState()

		address := net.JoinHostPort(peerState.GetHost(), strconv.Itoa(int(peerState.GetPeersPort())))
		actors := peerState.GetActors()
		grains := peerState.GetGrains()

		peers, err := r.pid.ActorSystem().getCluster().Peers(rctx)
		if err != nil {
			r.abortRelocation(ctx, address, actors, grains, errors.NewInternalError(err))
			return
		}

		leaderShares, peersShares := r.allocateActors(len(peers)+1, peerState)
		eg, egCtx := errgroup.WithContext(rctx)
		eg.SetLimit(defaultRelocationConcurrency)
		logger := r.pid.getLogger()

		r.relocateActors(egCtx, eg, leaderShares, peersShares, peers)

		if len(peerState.GetGrains()) > 0 {
			leaderGrains, peersGrains := r.allocateGrains(len(peers)+1, peerState)
			r.relocateGrains(egCtx, eg, leaderGrains, peersGrains, peers)
		}

		// only block when there are go routines running
		if len(leaderShares) > 0 || len(peersShares) > 0 || len(peerState.GetGrains()) > 0 {
			if err := eg.Wait(); err != nil {
				logger.Errorf("cluster rebalancing failed: %v (hint: check cluster quorum, peer connectivity)", err)
				r.abortRelocation(ctx, address, actors, grains, err)
				return
			}
		}

		ctx.Tell(ctx.Sender(), &internalpb.RebalanceComplete{
			PeerAddress: net.JoinHostPort(peerState.GetHost(), strconv.Itoa(int(peerState.GetPeersPort()))),
		})
	}
}

// abortRelocation gives up on a failed rebalance. Relocation is deliberately not
// retried: re-running it would block the serialized relocator behind backoff and
// re-do already-completed spawns, lengthening the recovery window for every other
// node. Instead the failure is surfaced as a RelocationFailed event on the system
// event stream so the application can subscribe and decide how to react.
//
// Releasing the flag is essential: the success path clears it via
// RebalanceComplete in the system guardian, so a failed rebalance that returns
// early would otherwise leave relocating set forever and permanently block every
// future cluster rebalance. The departed node's peer state is removed from the
// cluster store, mirroring the success path: relocation is not retried and the
// RelocationFailed event already carries the affected actors and grains, so the
// snapshot has no remaining consumer.
func (r *relocator) abortRelocation(ctx *ReceiveContext, address string, actors map[string]*internalpb.Actor, grains map[string]*internalpb.Grain, err error) {
	r.pid.ActorSystem().completeRelocation()
	r.publishRelocationFailed(address, actors, grains, err)

	if derr := r.pid.ActorSystem().getClusterStore().DeletePeerState(context.WithoutCancel(ctx.Context()), address); derr != nil {
		r.pid.getLogger().Errorf("failed to remove peer=%s state after failed relocation: %v (hint: check cluster store)", address, derr)
	}

	ctx.Err(err)
}

// publishRelocationFailed emits a RelocationFailed event describing the departed
// node and the actors and grains that could not be relocated.
func (r *relocator) publishRelocationFailed(address string, actors map[string]*internalpb.Actor, grains map[string]*internalpb.Grain, err error) {
	if r.pid.eventsStream == nil {
		return
	}

	actorNames := make([]string, 0, len(actors))
	for _, actor := range actors {
		actorNames = append(actorNames, actor.GetAddress())
	}

	grainIDs := make([]string, 0, len(grains))
	for _, grain := range grains {
		grainIDs = append(grainIDs, grain.GetGrainId().GetValue())
	}

	r.pid.eventsStream.Publish(eventsTopic, NewRelocationFailed(address, time.Now().UTC(), actorNames, grainIDs, err))
}

func (r *relocator) relocateActors(ctx context.Context, eg *errgroup.Group, leaderShares []*internalpb.Actor, peersShares [][]*internalpb.Actor, peers []*cluster.Peer) {
	// recreate the leader's share locally, one bounded goroutine per actor
	for _, actor := range leaderShares {
		eg.Go(func() error {
			addr, err := address.Parse(actor.GetAddress())
			if err != nil {
				return errors.NewSpawnError(err)
			}

			if isSystemName(addr.Name()) {
				return nil
			}

			if err := r.recreateLocally(ctx, actor, true); err != nil {
				return errors.NewSpawnError(err)
			}
			return nil
		})
	}

	// spawn each peer's share on its target peer, one bounded goroutine per actor
	for i := 1; i < len(peersShares); i++ {
		actors := peersShares[i]
		peer := peers[i-1]
		for _, actor := range actors {
			eg.Go(func() error {
				addr, err := address.Parse(actor.GetAddress())
				if err != nil {
					return errors.NewSpawnError(err)
				}

				notSingleton := actor.GetSingleton() == nil
				if isSystemName(addr.Name()) || !notSingleton || !actor.GetRelocatable() {
					return nil
				}

				return r.spawnRemoteActor(ctx, actor, peer)
			})
		}
	}
}

func (r *relocator) spawnRemoteActor(ctx context.Context, actor *internalpb.Actor, peer *cluster.Peer) error {
	remoteHost := peer.Host
	remotingPort := peer.RemotingPort
	actorSystem := r.pid.ActorSystem()
	cluster := actorSystem.getCluster()
	addr, err := address.Parse(actor.GetAddress())
	if err != nil {
		return errors.NewInternalError(err)
	}

	exists, err := cluster.ActorExists(ctx, addr.Name())
	if err != nil {
		return errors.NewInternalError(err)
	}
	if exists {
		if err := cluster.RemoveActor(ctx, addr.Name()); err != nil {
			return errors.NewInternalError(err)
		}
	}

	dependencies, err := actorSystem.getReflection().dependenciesFromProto(actor.GetDependencies()...)
	if err != nil {
		return err
	}

	spawnRequest := &remote.SpawnRequest{
		Name:                addr.Name(),
		Kind:                actor.GetType(),
		Singleton:           nil,
		Relocatable:         true,
		Dependencies:        dependencies,
		PassivationStrategy: codec.DecodePassivationStrategy(actor.GetPassivationStrategy()),
		EnableStashing:      actor.GetEnableStash(),
		Role:                actor.Role,
		Reentrancy:          codec.DecodeReentrancy(actor.GetReentrancy()),
	}

	if actor.GetSupervisor() != nil {
		spawnRequest.Supervisor = codec.DecodeSupervisor(actor.GetSupervisor())
	}

	if _, err := r.remoting.RemoteSpawn(ctx, remoteHost, remotingPort, spawnRequest); err != nil {
		r.logger.Errorf("remote spawn failed: %v (hint: check target node reachable, actor type registered)", err)
		return errors.NewSpawnError(err)
	}
	return nil
}

func (r *relocator) relocateGrains(ctx context.Context, eg *errgroup.Group, leaderGrains []*internalpb.Grain, peersGrains [][]*internalpb.Grain, peers []*cluster.Peer) {
	if len(leaderGrains) > 0 {
		leaderHost := r.pid.ActorSystem().Host()
		leaderPort := int32(r.pid.ActorSystem().Port())
		// activate the leader's share locally, one bounded goroutine per grain
		for _, grain := range leaderGrains {
			eg.Go(func() error {
				if isSystemName(grain.GetGrainId().GetName()) || grain.GetDisableRelocation() {
					return nil
				}
				grain.Host = leaderHost
				grain.Port = leaderPort
				if err := r.pid.ActorSystem().recreateGrain(ctx, grain); err != nil {
					return errors.NewSpawnError(err)
				}
				return nil
			})
		}
	}

	// activate each peer's share on its target peer, one bounded goroutine per grain
	for i := 1; i < len(peersGrains); i++ {
		grains := peersGrains[i]
		peer := peers[i-1]
		for _, grain := range grains {
			eg.Go(func() error {
				if isSystemName(grain.GetGrainId().GetName()) || grain.GetDisableRelocation() {
					return nil
				}
				return r.activateRemoteGrain(ctx, grain, peer)
			})
		}
	}
}

func (r *relocator) activateRemoteGrain(ctx context.Context, grain *internalpb.Grain, peer *cluster.Peer) error {
	remoteHost := peer.Host
	remotingPort := peer.RemotingPort
	remoting := r.pid.ActorSystem().getRemoting()

	exist, err := r.pid.ActorSystem().getCluster().GrainExists(ctx, grain.GetGrainId().GetName())
	if err != nil {
		return errors.NewInternalError(err)
	}
	if exist {
		if err := r.pid.ActorSystem().getCluster().RemoveGrain(ctx, grain.GetGrainId().GetName()); err != nil {
			return errors.NewInternalError(err)
		}
	}

	grain.Host = remoteHost
	grain.Port = int32(remotingPort)

	// Use proto TCP client
	client := remoting.NetClient(remoteHost, remotingPort)
	request := &internalpb.RemoteActivateGrainRequest{
		Grain: grain,
	}

	resp, err := client.SendProto(ctx, request)
	if err != nil {
		r.logger.Errorf("activate remote grain failed: %v (hint: check grain OnActivate, target node reachable)", err)
		return errors.NewSpawnError(err)
	}

	// Check for proto errors
	if errResp, ok := resp.(*internalpb.Error); ok {
		err := fmt.Errorf("proto error: code=%s, msg=%s", errResp.GetCode(), errResp.GetMessage())
		r.logger.Errorf("activate remote grain proto error: %v (hint: check target node, grain registration)", err)
		return errors.NewSpawnError(err)
	}

	return nil
}

// PostStop is executed when the actor is shutting down.
func (r *relocator) PostStop(ctx *Context) error {
	if r.remoting != nil {
		r.remoting.Close()
	}
	ctx.ActorSystem().Logger().Infof("actor=%s stopped successfully", ctx.ActorName())
	return nil
}

// allocateActors build the list of actors to create on the leader node and the peers in the cluster
func (r *relocator) allocateActors(totalPeers int, nodeLeftState *internalpb.PeerState) (leaderShares []*internalpb.Actor, peersShares [][]*internalpb.Actor) {
	actors := nodeLeftState.GetActors()
	actorsCount := len(actors)

	// Collect all actors to be rebalanced
	toRebalances := make([]*internalpb.Actor, 0, actorsCount)
	for _, actorProps := range actors {
		toRebalances = append(toRebalances, actorProps)
	}

	// Separate singleton actors to be assigned to the leader
	leaderShares = slices.Filter(toRebalances, func(actor *internalpb.Actor) bool {
		return actor.GetSingleton() != nil
	})

	// Remove singleton actors from the list
	toRebalances = slices.Filter(toRebalances, func(actor *internalpb.Actor) bool {
		return actor.GetSingleton() == nil
	})

	// Distribute remaining actors among peers
	actorsCount = len(toRebalances)
	quotient := actorsCount / totalPeers
	remainder := actorsCount % totalPeers

	// Assign remainder actors to the leader
	leaderShares = append(leaderShares, toRebalances[:remainder]...)

	// Chunk the remaining actors for peers
	chunks := chunk.Chunkify(toRebalances[remainder:], quotient)

	// Ensure leader takes the first chunk
	if len(chunks) > 0 {
		leaderShares = append(leaderShares, chunks[0]...)
	}

	return leaderShares, chunks
}

// recreateLocally recreates the actor
func (r *relocator) recreateLocally(ctx context.Context, props *internalpb.Actor, enforceSingleton bool) error {
	addr, err := address.Parse(props.GetAddress())
	if err != nil {
		return errors.NewInternalError(err)
	}

	// remove the given actor from the cluster
	if err := r.pid.ActorSystem().getCluster().RemoveActor(ctx, addr.Name()); err != nil {
		return errors.NewInternalError(err)
	}

	actor, err := r.pid.ActorSystem().getReflection().instantiateActor(props.GetType())
	if err != nil {
		return err
	}

	if enforceSingleton && props.GetSingleton() != nil {
		// define singleton options
		singletonOpts := []ClusterSingletonOption{
			WithSingletonSpawnTimeout(props.GetSingleton().GetSpawnTimeout().AsDuration()),
			WithSingletonSpawnWaitInterval(props.GetSingleton().GetWaitInterval().AsDuration()),
			WithSingletonSpawnRetries(int(props.GetSingleton().GetMaxRetries())),
		}

		if props.GetRole() != "" {
			singletonOpts = append(singletonOpts, WithSingletonRole(props.GetRole()))
		}

		// spawn the singleton actor
		_, err := r.pid.ActorSystem().SpawnSingleton(ctx, addr.Name(), actor, singletonOpts...)
		return err
	}

	if !props.GetRelocatable() {
		return nil
	}

	spawnOpts := []SpawnOption{
		WithPassivationStrategy(codec.DecodePassivationStrategy(props.GetPassivationStrategy())),
	}

	if props.GetEnableStash() {
		spawnOpts = append(spawnOpts, WithStashing())
	}

	if props.GetRole() != "" {
		spawnOpts = append(spawnOpts, WithRole(props.GetRole()))
	}

	if props.GetReentrancy() != nil {
		reentrancy := codec.DecodeReentrancy(props.GetReentrancy())
		spawnOpts = append(spawnOpts, WithReentrancy(reentrancy))
	}

	if props.GetSupervisor() != nil {
		if decoded := codec.DecodeSupervisor(props.GetSupervisor()); decoded != nil {
			spawnOpts = append(spawnOpts, WithSupervisor(decoded))
		}
	}

	if len(props.GetDependencies()) > 0 {
		dependencies, err := r.pid.ActorSystem().getReflection().dependenciesFromProto(props.GetDependencies()...)
		if err != nil {
			return err
		}
		spawnOpts = append(spawnOpts, WithDependencies(dependencies...))
	}

	_, err = r.pid.ActorSystem().Spawn(ctx, addr.Name(), actor, spawnOpts...)
	return err
}

// allocateGrains distributes grains among the leader and peers for rebalancing.
//
// It returns two values:
//   - leaderShares: grains to be created on the leader node
//   - peersShares: a slice of grain slices, each assigned to a peer node
func (r *relocator) allocateGrains(totalPeers int, nodeLeftState *internalpb.PeerState) (leaderShares []*internalpb.Grain, peersShares [][]*internalpb.Grain) {
	grains := nodeLeftState.GetGrains()
	grainCount := len(grains)

	// Collect all grains to be rebalanced
	toRebalances := make([]*internalpb.Grain, 0, grainCount)
	for _, grain := range grains {
		toRebalances = append(toRebalances, grain)
	}

	quotient := grainCount / totalPeers
	remainder := grainCount % totalPeers

	// Assign the remainder grains to the leader
	leaderShares = append(leaderShares, toRebalances[:remainder]...)

	// Chunk the remaining actors for peers
	peersShares = chunk.Chunkify(toRebalances[remainder:], quotient)

	// Ensure leader takes the first chunk
	if len(peersShares) > 0 {
		leaderShares = append(leaderShares, peersShares[0]...)
	}

	return leaderShares, peersShares
}
