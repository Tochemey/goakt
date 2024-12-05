/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package actors

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/slice"
	"github.com/tochemey/goakt/v2/log"
)

// rebalancer is a system actor that helps rebalance cluster
// when the cluster topology changes
type rebalancer struct {
	reflection *reflection
	remoting   *Remoting
	pid        *PID
	logger     log.Logger
}

// enforce compilation error
var _ Actor = (*rebalancer)(nil)

// newRebalancer creates an instance of rebalancer
func newRebalancer(reflection *reflection) *rebalancer {
	return &rebalancer{
		reflection: reflection,
	}
}

// PreStart pre-starts the actor.
func (r *rebalancer) PreStart(context.Context) error {
	return nil
}

// Receive handles messages sent to the rebalancer
func (r *rebalancer) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		r.pid = ctx.Self()
		r.remoting = NewRemoting()
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
			ctx.Err(err)
			return
		}

		leaderShares, peersShares := r.computeRebalancing(len(peers)+1, peerState)
		eg, egCtx := errgroup.WithContext(rctx)
		logger := r.pid.Logger()

		if len(leaderShares) > 0 {
			eg.Go(func() error {
				for _, actor := range leaderShares {
					if isReservedName(actor.GetActorAddress().GetName()) {
						continue
					}

					iactor, err := r.reflection.ActorFrom(actor.GetActorType())
					if err != nil {
						return err
					}

					if _, err = r.pid.ActorSystem().Spawn(egCtx, actor.GetActorAddress().GetName(), iactor); err != nil {
						return err
					}
				}
				return nil
			})
		}

		// defensive programming
		if len(peersShares) == 0 {
			return
		}

		eg.Go(func() error {
			for i := 1; i < len(peersShares); i++ {
				actors := peersShares[i]
				peer := peers[i-1]
				peerState, err := r.pid.ActorSystem().getPeerStateFromCache(peer.PeerAddress())
				if err != nil {
					return err
				}

				for _, actor := range actors {
					// never redistribute system actors
					if isReservedName(actor.GetActorAddress().GetName()) {
						continue
					}

					if err := r.remoting.RemoteSpawn(egCtx,
						peerState.GetHost(),
						int(peerState.GetRemotingPort()),
						actor.GetActorAddress().GetName(),
						actor.GetActorType()); err != nil {
						logger.Error(err)
						return err
					}
				}
			}
			return nil
		})

		if err := eg.Wait(); err != nil {
			logger.Errorf("cluster rebalancing failed: %v", err)
			ctx.Shutdown()
		}
	default:
		ctx.Unhandled()
	}
}

// PostStop is executed when the actor is shutting down.
func (r *rebalancer) PostStop(context.Context) error {
	r.remoting.Close()
	r.logger.Infof("%s stopped successfully", r.pid.Name())
	return nil
}

// computeRebalancing build the list of actors to create on the leader node and the peers in the cluster
func (r *rebalancer) computeRebalancing(totalPeers int, nodeLeftState *internalpb.PeerState) (leaderShares []*internalpb.ActorRef, peersShares [][]*internalpb.ActorRef) {
	var (
		chunks      [][]*internalpb.ActorRef
		actorsCount = len(nodeLeftState.GetActors())
	)

	// distribute actors amongst the peers with the leader taking the heavy load
	switch {
	case actorsCount < totalPeers:
		leaderShares = nodeLeftState.GetActors()
	default:
		quotient := actorsCount / totalPeers
		remainder := actorsCount % totalPeers
		leaderShares = nodeLeftState.GetActors()[:remainder]
		chunks = slice.Chunk[*internalpb.ActorRef](nodeLeftState.GetActors()[remainder:], quotient)
	}

	if len(chunks) > 0 {
		leaderShares = append(leaderShares, chunks[0]...)
	}

	return leaderShares, chunks
}
