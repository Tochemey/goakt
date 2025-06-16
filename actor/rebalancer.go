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
	"net"
	"strconv"

	"connectrpc.com/connect"
	"golang.org/x/sync/errgroup"

	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/collection"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
)

// rebalancer is a system actor that helps rebalance cluster
// when the cluster topology changes
type rebalancer struct {
	remoting *Remoting
	pid      *PID
	logger   log.Logger
}

// enforce compilation error
var _ Actor = (*rebalancer)(nil)

// newRebalancer creates an instance of rebalancer
func newRebalancer(remoting *Remoting) *rebalancer {
	return &rebalancer{
		remoting: remoting,
	}
}

// PreStart pre-starts the actor.
func (r *rebalancer) PreStart(*Context) error {
	return nil
}

// Receive handles messages sent to the rebalancer
func (r *rebalancer) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		r.pid = ctx.Self()
		r.logger = ctx.Logger()
		r.logger.Infof("%s started successfully", r.pid.Name())
		ctx.Become(r.Rebalance)
	default:
		ctx.Unhandled()
	}
}

// Rebalance behavior
func (r *rebalancer) Rebalance(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *internalpb.Rebalance:
		rctx := context.WithoutCancel(ctx.Context())
		peerState := msg.GetPeerState()

		// grab all our active peers
		peers, err := r.pid.ActorSystem().getCluster().Peers(rctx)
		if err != nil {
			ctx.Err(NewInternalError(err))
			return
		}

		leaderShares, peersShares := r.allocateActors(len(peers)+1, peerState)
		eg, egCtx := errgroup.WithContext(rctx)
		logger := r.pid.Logger()

		if len(leaderShares) > 0 {
			eg.Go(func() error {
				for _, actor := range leaderShares {
					// never redistribute system actors
					if !isReservedName(actor.GetAddress().GetName()) {
						if err := r.recreateLocally(egCtx, actor, true); err != nil {
							return NewSpawnError(err)
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
						// never redistribute system actors and singleton actors
						if !isReservedName(actor.GetAddress().GetName()) && !actor.GetIsSingleton() {
							if actor.GetRelocatable() {
								remoteHost := peer.Host
								remotingPort := peer.RemotingPort

								dependencies, err := r.pid.ActorSystem().getReflection().DependenciesFromProtobuf(actor.GetDependencies()...)
								if err != nil {
									return err
								}

								spawnRequest := &remote.SpawnRequest{
									Name:                actor.GetAddress().GetName(),
									Kind:                actor.GetType(),
									Singleton:           false,
									Relocatable:         true,
									Dependencies:        dependencies,
									PassivationStrategy: passivationStrategyFromProto(actor.GetPassivationStrategy()),
									EnableStashing:      actor.GetEnableStash(),
								}

								if err := r.remoting.RemoteSpawn(egCtx, remoteHost, remotingPort, spawnRequest); err != nil {
									logger.Error(err)
									return NewSpawnError(err)
								}
							}
						}
					}
				}
				return nil
			})
		}

		if len(peerState.GetGrains()) > 0 {
			leaderGrains, peersGrains := r.allocateGrains(len(peers)+1, peerState)
			// Recreate grains on the leader
			if len(leaderGrains) > 0 {
				eg.Go(func() error {
					for _, grain := range leaderGrains {
						if !isReservedName(grain.GetGrainId().GetName()) {
							// reset the grain host and port
							grain.Host = r.pid.ActorSystem().Host()
							grain.Port = int32(r.pid.ActorSystem().Port())
							if err := r.pid.ActorSystem().recreateGrain(egCtx, grain); err != nil {
								return NewSpawnError(err)
							}
						}
					}
					return nil
				})
			}

			// Recreate grains on the peers
			if len(peersGrains) > 0 {
				eg.Go(func() error {
					for i := 1; i < len(peersGrains); i++ {
						grains := peersGrains[i]
						peer := peers[i-1]

						for _, grain := range grains {
							if !isReservedName(grain.GetGrainId().GetName()) {
								remoteHost := peer.Host
								remotingPort := peer.RemotingPort
								remoting := r.pid.ActorSystem().getRemoting()
								remoteClient := remoting.remotingServiceClient(remoteHost, remotingPort)
								// reset the grain host and port
								grain.Host = remoteHost
								grain.Port = int32(remotingPort)
								request := connect.NewRequest(&internalpb.RemoteActivateGrainRequest{
									Grain:        grain,
									Dependencies: grain.GetDependencies(),
								})

								if _, err := remoteClient.RemoteActivateGrain(egCtx, request); err != nil {
									logger.Error(err)
									return NewSpawnError(err)
								}
							}
						}
					}
					return nil
				})
			}
		}

		// only block when there are go routines running
		if len(leaderShares) > 0 || len(peersShares) > 0 || len(peerState.GetGrains()) > 0 {
			if err := eg.Wait(); err != nil {
				logger.Errorf("cluster rebalancing failed: %v", err)
				ctx.Err(err)
			}
		}

		ctx.Tell(ctx.Sender(), &internalpb.RebalanceComplete{
			PeerAddress: net.JoinHostPort(peerState.GetHost(), strconv.Itoa(int(peerState.GetPeersPort()))),
		})

	default:
		ctx.Unhandled()
	}
}

// PostStop is executed when the actor is shutting down.
func (r *rebalancer) PostStop(*Context) error {
	r.remoting.Close()
	r.logger.Infof("%s stopped successfully", r.pid.Name())
	return nil
}

// allocateActors build the list of actors to create on the leader node and the peers in the cluster
func (r *rebalancer) allocateActors(totalPeers int, nodeLeftState *internalpb.PeerState) (leaderShares []*internalpb.Actor, peersShares [][]*internalpb.Actor) {
	actors := nodeLeftState.GetActors()
	actorsCount := len(actors)

	// Collect all actors to be rebalanced
	toRebalances := make([]*internalpb.Actor, 0, actorsCount)
	for _, actorProps := range actors {
		toRebalances = append(toRebalances, actorProps)
	}

	// Separate singleton actors to be assigned to the leader
	leaderShares = collection.Filter(toRebalances, func(actor *internalpb.Actor) bool {
		return actor.GetIsSingleton()
	})

	// Remove singleton actors from the list
	toRebalances = collection.Filter(toRebalances, func(actor *internalpb.Actor) bool {
		return !actor.GetIsSingleton()
	})

	// Distribute remaining actors among peers
	actorsCount = len(toRebalances)
	quotient := actorsCount / totalPeers
	remainder := actorsCount % totalPeers

	// Assign remainder actors to the leader
	leaderShares = append(leaderShares, toRebalances[:remainder]...)

	// Chunk the remaining actors for peers
	chunks := collection.Chunkify(toRebalances[remainder:], quotient)

	// Ensure leader takes the first chunk
	if len(chunks) > 0 {
		leaderShares = append(leaderShares, chunks[0]...)
	}

	return leaderShares, chunks
}

// recreateLocally recreates the actor
func (r *rebalancer) recreateLocally(ctx context.Context, props *internalpb.Actor, enforceSingleton bool) error {
	actor, err := r.pid.ActorSystem().getReflection().NewActor(props.GetType())
	if err != nil {
		return err
	}

	if enforceSingleton && props.GetIsSingleton() {
		// spawn the singleton actor
		return r.pid.ActorSystem().SpawnSingleton(ctx, props.GetAddress().GetName(), actor)
	}

	if !props.GetRelocatable() {
		return nil
	}

	spawnOpts := []SpawnOption{
		WithPassivationStrategy(passivationStrategyFromProto(props.GetPassivationStrategy())),
	}

	if props.GetEnableStash() {
		spawnOpts = append(spawnOpts, WithStashing())
	}

	if len(props.GetDependencies()) > 0 {
		dependencies, err := r.pid.ActorSystem().getReflection().DependenciesFromProtobuf(props.GetDependencies()...)
		if err != nil {
			return err
		}
		spawnOpts = append(spawnOpts, WithDependencies(dependencies...))
	}

	_, err = r.pid.ActorSystem().Spawn(ctx, props.GetAddress().GetName(), actor, spawnOpts...)
	return err
}

// allocateGrains distributes grains among the leader and peers for rebalancing.
//
// It returns two values:
//   - leaderShares: grains to be created on the leader node
//   - peersShares: a slice of grain slices, each assigned to a peer node
func (r *rebalancer) allocateGrains(totalPeers int, nodeLeftState *internalpb.PeerState) (leaderShares []*internalpb.Grain, peersShares [][]*internalpb.Grain) {
	grains := nodeLeftState.GetGrains()
	grainCount := len(grains)

	if grainCount == 0 || totalPeers <= 0 {
		return nil, nil
	}

	// Collect all grains to be rebalanced
	toRebalance := make([]*internalpb.Grain, 0, grainCount)
	for _, grain := range grains {
		toRebalance = append(toRebalance, grain)
	}

	quotient := grainCount / totalPeers
	remainder := grainCount % totalPeers

	// Assign the remainder grains to the leader
	leaderShares = append(leaderShares, toRebalance[:remainder]...)

	// Chunk the remaining grains for peers
	if remainder < grainCount {
		chunks := collection.Chunkify(toRebalance[remainder:], quotient)
		if len(chunks) > 0 {
			// Leader takes the first chunk
			leaderShares = append(leaderShares, chunks[0]...)
			// Remaining chunks go to peers
			peersShares = chunks[1:]
		}
	}

	return leaderShares, peersShares
}
