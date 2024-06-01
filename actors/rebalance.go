/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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
	"net"
	"strconv"

	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/cluster"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/slices"
)

const rebalanceKey = "rebalancing"

// rebalance is used when cluster topology changes particularly
// when a given node has left the cluster
func (x *actorSystem) rebalance(ctx context.Context, event *cluster.Event) error {
	if event.Type != cluster.NodeLeft {
		return nil
	}

	// first check whether rebalancing is not ongoing
	exist, err := x.cluster.KeyExists(ctx, rebalanceKey)
	if err != nil {
		return err
	}

	if exist {
		return nil
	}

	// set the rebalance key
	if err := x.cluster.SetKey(ctx, rebalanceKey); err != nil {
		return err
	}

	defer func() {
		if err := x.cluster.UnsetKey(ctx, rebalanceKey); err != nil {
			// panic when we cannot unset the key
			panic(err)
		}
	}()

	peers, err := x.cluster.Peers(ctx)
	if err != nil {
		x.logger.Errorf("failed to fetch peers: (%v)", err)
		return err
	}

	totalPeers := len(peers) + 1

	// locate the leader node amongst the peers
	var leader *cluster.Peer
	for _, peer := range peers {
		if peer.Leader {
			leader = peer
			break
		}
	}

	// no leader found, then the given node becomes the leader
	if leader == nil {
		leader = &cluster.Peer{
			Host:   x.remotingHost,
			Port:   x.clusterConfig.PeersPort(),
			Leader: true,
		}
	}

	nodeLeft := new(goaktpb.NodeLeft)
	if err := event.Payload.UnmarshalTo(nodeLeft); err != nil {
		return err
	}

	// only the cluster coordinator can perform rebalancing
	if net.JoinHostPort(leader.Host, strconv.Itoa(leader.Port)) != x.cluster.AdvertisedAddress() {
		return nil
	}

	bytea, ok := x.peersCache.Get(nodeLeft.GetAddress())
	if !ok {
		return ErrPeerNotFound
	}

	peerState := new(internalpb.PeerState)
	_ = proto.Unmarshal(bytea, peerState)

	if len(peerState.GetActors()) == 0 {
		return nil
	}

	quotient := len(peerState.GetActors()) / totalPeers
	remainder := len(peerState.GetActors()) % totalPeers

	leaderActors := peerState.GetActors()[:remainder]
	chunks := slices.Chunk[*internalpb.WireActor](peerState.GetActors()[remainder:], quotient)
	leaderActors = append(leaderActors, chunks[0]...)

	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(2)

	eg.Go(func() error {
		for _, leaderActor := range leaderActors {
			actor, err := x.reflection.ActorFrom(leaderActor.GetActorType())
			if err != nil {
				return err
			}

			if _, err = x.Spawn(ctx, leaderActor.GetActorName(), actor); err != nil {
				return err
			}
		}
		return nil
	})

	eg.Go(func() error {
		for i := 1; i < len(chunks); i++ {
			actors := chunks[i]
			peer := peers[i-1]
			bytea, _ := x.peersCache.Get(net.JoinHostPort(peer.Host, strconv.Itoa(peer.Port)))
			state := new(internalpb.PeerState)
			_ = proto.Unmarshal(bytea, state)

			for _, actor := range actors {
				if err := RemoteSpawn(ctx, state.GetHost(), int(state.GetRemotingPort()), actor.GetActorName(), actor.GetActorPath()); err != nil {
					return err
				}
			}
		}
		return nil
	})

	return eg.Wait()
}
