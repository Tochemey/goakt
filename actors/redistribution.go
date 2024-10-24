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
	nethttp "net/http"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/cluster"
	"github.com/tochemey/goakt/v2/internal/http"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/v2/internal/slice"
	"github.com/tochemey/goakt/v2/secureconn"
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
		x.logger.Errorf("failed to unmarshal payload: (%v)", err)
		return err
	}

	x.peersCacheMu.RLock()
	bytea, ok := x.peersCache[nodeLeft.GetAddress()]
	x.peersCacheMu.RUnlock()
	if !ok {
		x.logger.Errorf("peer %s not found", nodeLeft.GetAddress())
		return ErrPeerNotFound
	}

	peerState := new(internalpb.PeerState)
	_ = proto.Unmarshal(bytea, peerState)

	actorsCount := len(peerState.GetActors())

	if actorsCount == 0 {
		return nil
	}

	var (
		leaderActors []*internalpb.ActorRef
		chunks       [][]*internalpb.ActorRef
	)

	x.logger.Debugf("total actors: %d on node left[%s]", actorsCount, nodeLeft.GetAddress())

	if actorsCount < totalPeers {
		leaderActors = peerState.GetActors()
	} else {
		quotient := actorsCount / totalPeers
		remainder := actorsCount % totalPeers
		leaderActors = peerState.GetActors()[:remainder]
		chunks = slice.Chunk[*internalpb.ActorRef](peerState.GetActors()[remainder:], quotient)
	}

	if len(chunks) > 0 {
		leaderActors = append(leaderActors, chunks[0]...)
	}

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(
		func() error {
			for _, actor := range leaderActors {
				// never redistribute system actors
				if isSystemName(actor.GetActorAddress().GetName()) {
					continue
				}

				x.logger.Debugf(
					"re-creating actor=[(%s) of type (%s)]",
					actor.GetActorAddress().GetName(),
					actor.GetActorType(),
				)
				iactor, err := x.reflection.ActorFrom(actor.GetActorType())
				if err != nil {
					x.logger.Errorf(
						"failed to create actor=[(%s) of type (%s)]: %v",
						actor.GetActorAddress().GetName(),
						actor.GetActorType(),
						err,
					)
					return err
				}

				if _, err = x.Spawn(ctx, actor.GetActorAddress().GetName(), iactor); err != nil {
					x.logger.Errorf(
						"failed to spawn actor=[(%s) of type (%s)]: %v",
						actor.GetActorAddress().GetName(),
						actor.GetActorType(),
						err,
					)
					return err
				}

				x.logger.Debugf(
					"actor=[(%s) of type (%s)] successfully re-created",
					actor.GetActorAddress().GetName(),
					actor.GetActorType(),
				)
			}
			return nil
		},
	)

	eg.Go(
		func() error {
			// defensive programming
			if len(chunks) == 0 {
				return nil
			}

			for i := 1; i < len(chunks); i++ {
				actors := chunks[i]
				peer := peers[i-1]

				x.peersCacheMu.RLock()
				bytea := x.peersCache[net.JoinHostPort(peer.Host, strconv.Itoa(peer.Port))]
				x.peersCacheMu.RUnlock()

				peerState := new(internalpb.PeerState)
				_ = proto.Unmarshal(bytea, peerState)

				for _, actor := range actors {
					// never redistribute system actors
					if isSystemName(actor.GetActorAddress().GetName()) {
						continue
					}

					x.logger.Debugf(
						"re-creating actor=[(%s) of type (%s)]",
						actor.GetActorAddress().GetName(),
						actor.GetActorType(),
					)

					httpClient, err := x.getHTTPClient(peerState.GetCertificate())
					if err != nil {
						x.logger.Error(err)
						return err
					}

					if err := x.remoteSpawn(
						ctx,
						peerState.GetHost(),
						int(peerState.GetRemotingPort()),
						actor.GetActorAddress().GetName(),
						actor.GetActorType(),
						httpClient,
					); err != nil {
						x.logger.Error(err)
						return err
					}

					// gracefully close the connection
					httpClient.CloseIdleConnections()

					x.logger.Debugf(
						"actor=[(%s) of type (%s)] successfully re-created",
						actor.GetActorAddress().GetName(),
						actor.GetActorType(),
					)
				}
			}
			return nil
		},
	)

	return eg.Wait()
}

// getHTTPClient returns a http client
func (x *actorSystem) getHTTPClient(certificate *internalpb.Certificate) (*nethttp.Client, error) {
	if certificate == nil || proto.Equal(certificate, new(internalpb.Certificate)) {
		return http.NewClient(), nil
	}
	secureConn, err := secureconn.NewSecureConnFromPEMBlocks(
		certificate.GetRootCasPemBlock(),
		certificate.GetKeyPemBlock(),
		certificate.GetCertPemBlock(),
	)
	if err != nil {
		return nil, err
	}
	return http.NewTLSClient(secureConn.SecureClient()), nil
}

// remoteSpawn creates an actor on a remote node. The given actor needs to be registered on the remote node using the Register method of ActorSystem
func (x *actorSystem) remoteSpawn(
	ctx context.Context,
	host string,
	port int,
	name,
	actorType string,
	httpClient *nethttp.Client,
) error {
	request := connect.NewRequest(
		&internalpb.RemoteSpawnRequest{
			Host:      host,
			Port:      int32(port),
			ActorName: name,
			ActorType: actorType,
		},
	)

	remoteClient := internalpbconnect.NewRemotingServiceClient(
		httpClient,
		http.URL(host, port),
	)

	if _, err := remoteClient.RemoteSpawn(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeFailedPrecondition {
			connectErr := err.(*connect.Error)
			e := connectErr.Unwrap()
			if strings.Contains(e.Error(), ErrTypeNotRegistered.Error()) {
				return ErrTypeNotRegistered
			}
		}
		return err
	}
	return nil
}
