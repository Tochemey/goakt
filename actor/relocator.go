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

		peers, err := r.pid.ActorSystem().getCluster().Peers(rctx)
		if err != nil {
			ctx.Err(errors.NewInternalError(err))
			return
		}

		leaderShares, peersShares := r.allocateActors(len(peers)+1, peerState)
		eg, egCtx := errgroup.WithContext(rctx)
		logger := r.pid.getLogger()

		r.relocateActors(egCtx, eg, leaderShares, peersShares, peers)

		if len(peerState.GetGrains()) > 0 {
			leaderGrains, peersGrains := r.allocateGrains(len(peers)+1, peerState)
			r.relocateGrains(egCtx, eg, leaderGrains, peersGrains, peers)
		}

		// only block when there are go routines running
		if len(leaderShares) > 0 || len(peersShares) > 0 || len(peerState.GetGrains()) > 0 {
			if err := eg.Wait(); err != nil {
				logger.Errorf("cluster rebalancing failed: %v", err)
				ctx.Err(err)
				// TODO: let us add the supervisor to handle this error
				return
			}
		}

		ctx.Tell(ctx.Sender(), &internalpb.RebalanceComplete{
			PeerAddress: net.JoinHostPort(peerState.GetHost(), strconv.Itoa(int(peerState.GetPeersPort()))),
		})
	}
}

func (r *relocator) relocateActors(ctx context.Context, eg *errgroup.Group, leaderShares []*internalpb.Actor, peersShares [][]*internalpb.Actor, peers []*cluster.Peer) {
	if len(leaderShares) > 0 {
		eg.Go(func() error {
			for _, actor := range leaderShares {
				addr, err := address.Parse(actor.GetAddress())
				if err != nil {
					return errors.NewSpawnError(err)
				}

				if !isSystemName(addr.Name()) {
					if err := r.recreateLocally(ctx, actor, true); err != nil {
						return errors.NewSpawnError(err)
					}
				}
			}
			return nil
		})
	}

	if len(peersShares) > 0 {
		eg.Go(func() error {
			for i := 1; i < len(peersShares); i++ {
				actors := peersShares[i]
				peer := peers[i-1]
				for _, actor := range actors {
					addr, err := address.Parse(actor.GetAddress())
					if err != nil {
						return errors.NewSpawnError(err)
					}

					notSingleton := actor.GetSingleton() == nil
					if !isSystemName(addr.Name()) && notSingleton && actor.GetRelocatable() {
						if err := r.spawnRemoteActor(ctx, actor, peer); err != nil {
							return err
						}
					}
				}
			}
			return nil
		})
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
		r.logger.Error(err)
		return errors.NewSpawnError(err)
	}
	return nil
}

func (r *relocator) relocateGrains(ctx context.Context, eg *errgroup.Group, leaderGrains []*internalpb.Grain, peersGrains [][]*internalpb.Grain, peers []*cluster.Peer) {
	if len(leaderGrains) > 0 {
		leaderHost := r.pid.ActorSystem().Host()
		leaderPort := int32(r.pid.ActorSystem().Port())
		eg.Go(func() error {
			for _, grain := range leaderGrains {
				if !isSystemName(grain.GetGrainId().GetName()) && !grain.GetDisableRelocation() {
					grain.Host = leaderHost
					grain.Port = leaderPort
					if err := r.pid.ActorSystem().recreateGrain(ctx, grain); err != nil {
						return errors.NewSpawnError(err)
					}
				}
			}
			return nil
		})
	}

	if len(peersGrains) > 0 {
		eg.Go(func() error {
			for i := 1; i < len(peersGrains); i++ {
				grains := peersGrains[i]
				peer := peers[i-1]
				for _, grain := range grains {
					if !isSystemName(grain.GetGrainId().GetName()) && !grain.GetDisableRelocation() {
						if err := r.activateRemoteGrain(ctx, grain, peer); err != nil {
							return err
						}
					}
				}
			}
			return nil
		})
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
		r.logger.Error(err)
		return errors.NewSpawnError(err)
	}

	// Check for proto errors
	if errResp, ok := resp.(*internalpb.Error); ok {
		err := fmt.Errorf("proto error: code=%s, msg=%s", errResp.GetCode(), errResp.GetMessage())
		r.logger.Error(err)
		return errors.NewSpawnError(err)
	}

	return nil
}

// PostStop is executed when the actor is shutting down.
func (r *relocator) PostStop(ctx *Context) error {
	if r.remoting != nil {
		r.remoting.Close()
	}
	ctx.ActorSystem().Logger().Infof("%s stopped successfully", ctx.ActorName())
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
