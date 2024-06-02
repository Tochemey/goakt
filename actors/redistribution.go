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

// redistribute is used when cluster topology changes particularly
// when a given node has left the cluster
func (x *actorSystem) redistribute(ctx context.Context, event *cluster.Event) error {
	if event.Type != cluster.NodeLeft {
		return nil
	}

	// only the leader can perform redistribution
	if !x.cluster.IsLeader(ctx) {
		return nil
	}

	peers, err := x.cluster.Peers(ctx)
	if err != nil {
		x.logger.Errorf("failed to fetch peers: (%v)", err)
		return err
	}

	totalPeers := len(peers) + 1

	nodeLeft := new(goaktpb.NodeLeft)
	if err := event.Payload.UnmarshalTo(nodeLeft); err != nil {
		return err
	}

	bytea, ok := x.peersCache.Get(nodeLeft.GetAddress())
	if !ok {
		return ErrPeerNotFound
	}

	peerState := new(internalpb.PeerState)
	_ = proto.Unmarshal(bytea, peerState)

	actorsCount := len(peerState.GetActors())

	if actorsCount == 0 {
		return nil
	}

	var (
		leaderActors []*internalpb.WireActor
		chunks       [][]*internalpb.WireActor
	)

	if actorsCount < totalPeers {
		leaderActors = peerState.GetActors()
	} else {
		quotient := actorsCount / totalPeers
		remainder := actorsCount % totalPeers
		leaderActors = peerState.GetActors()[:remainder]
		chunks = slices.Chunk[*internalpb.WireActor](peerState.GetActors()[remainder:], quotient)
	}

	if len(chunks) > 0 {
		leaderActors = append(leaderActors, chunks[0]...)
	}

	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(2)

	eg.Go(func() error {
		for _, wireActor := range leaderActors {
			actor, err := x.reflection.ActorFrom(wireActor.GetActorType())
			if err != nil {
				return err
			}

			if _, err = x.Spawn(ctx, wireActor.GetActorName(), actor); err != nil {
				return err
			}
		}
		return nil
	})

	eg.Go(func() error {
		// defensive programming
		if len(chunks) == 0 {
			return nil
		}

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
