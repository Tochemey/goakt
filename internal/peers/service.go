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

package peers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	goset "github.com/deckarep/golang-set/v2"
	"github.com/flowchartsman/retry"
	"github.com/hashicorp/memberlist"
	"go.uber.org/atomic"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/internal/errorschain"
	"github.com/tochemey/goakt/v2/internal/http"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/v2/internal/tcp"
	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/log"
)

// IService defines the peers service interface
type IService interface {
	// Start starts the peers service engine
	Start(ctx context.Context) error
	// Stop stops the peers service engine
	Stop(ctx context.Context) error
	// GetPeerState fetches a given peer state
	GetPeerState(peerAddress string) (*internalpb.PeerState, error)
	// RemovePeerState removes the state of a given peer
	RemovePeerState(ctx context.Context, peerAddress string) error
	// PutActor sets the given actor on the localState of the node and let the replicator
	// pushes to the rest of the cluster
	PutActor(ctx context.Context, actor *internalpb.ActorRef) error
	// GetActor fetches an actor from the cluster
	GetActor(actorName string) (*internalpb.ActorRef, error)
	// RemoveActor removes a given actor from the cluster.
	// An actor is removed from the cluster when this actor has been passivated.
	RemoveActor(ctx context.Context, actorName string) error
	// Peers returns a channel containing the list of peers at a given time
	Peers() ([]*Peer, error)
	// Whoami returns the node member data
	Whoami() *Peer
	// Leader returns the eldest member of the cluster
	// Leadership here is based upon node time creation
	Leader() (*Peer, error)
	// IsLeader states whether the given cluster node is a leader or not at a given
	// point in time in the cluster
	IsLeader() (bool, error)
	// Events returns a channel where cluster events are published
	Events() <-chan *Event
	// Actors returns the list of actors in the cluster
	Actors() []*internalpb.ActorRef
	// Address returns the node host:port address
	Address() string
	// PutJobKey broadcasts the given job key to the list the node's peers
	PutJobKey(ctx context.Context, jobKey string) error
	// GetJobKey fetches a given job key from the cluster
	GetJobKey(jobKey string) (*string, error)
	// RemoveJobKey removes a given job key from the cluster
	RemoveJobKey(ctx context.Context, jobKey string) error
}

type Service struct {
	delegate *delegate

	peerMeta        *internalpb.PeerMeta
	memberConfig    *memberlist.Config
	memberlist      *memberlist.Memberlist
	started         *atomic.Bool
	shutdownTimeout time.Duration

	httpServer   *nethttp.Server
	httpClient   *nethttp.Client
	localOpsLock *sync.RWMutex

	maxJoinAttempts   int
	joinRetryInterval time.Duration
	joinTimeout       time.Duration
	provider          discovery.Provider
	logger            log.Logger

	node *discovery.Node

	stopEventsListener chan types.Unit
	eventsLock         *sync.Mutex
	peersLock          *sync.Mutex
	eventsQueue        chan *Event
	joinedQueue        chan *Peer

	broadcastRetryInterval time.Duration
	broadcastTimeout       time.Duration
	broadcastMaxRetries    int

	localState *internalpb.PeerState
	peersCache *peersCache

	localJobKeys    goset.Set[string]
	peersJobKeys    map[string][]string
	peerJobKeysLock *sync.RWMutex
}

var (
	// enforce compilation error
	_ internalpbconnect.PeersServiceHandler = (*Service)(nil)
	_ IService                              = (*Service)(nil)
)

// NewService creates an instance of Service
func NewService(provider discovery.Provider, node *discovery.Node, opts ...Option) (*Service, error) {
	bindIP, err := tcp.GetBindIP(node.PeersAddress())
	if err != nil {
		return nil, fmt.Errorf("failed to resolve TCP discoveryAddress: %w", err)
	}

	node.Host = bindIP
	meta := &internalpb.PeerMeta{
		Name:          node.PeersAddress(),
		Host:          node.Host,
		Port:          uint32(node.PeersPort),
		DiscoveryPort: uint32(node.DiscoveryPort),
		RemotingPort:  uint32(node.RemotingPort),
		CreationTime:  timestamppb.New(time.Now().UTC()),
	}

	peerState := &internalpb.PeerState{
		Host:         node.Host,
		RemotingPort: int32(node.RemotingPort),
		PeersPort:    int32(node.PeersPort),
		Actors:       make([]*internalpb.ActorRef, 0),
	}

	server := &Service{
		peerMeta:               meta,
		localState:             peerState,
		peersCache:             newPeersCache(),
		localOpsLock:           &sync.RWMutex{},
		node:                   node,
		logger:                 log.DiscardLogger,
		provider:               provider,
		joinRetryInterval:      100 * time.Millisecond,
		joinTimeout:            time.Second,
		shutdownTimeout:        3 * time.Second,
		eventsLock:             &sync.Mutex{},
		peersLock:              &sync.Mutex{},
		eventsQueue:            make(chan *Event, 1),
		joinedQueue:            make(chan *Peer, 1),
		broadcastRetryInterval: 200 * time.Millisecond,
		broadcastTimeout:       time.Second,
		httpClient:             http.NewClient(),
		stopEventsListener:     make(chan types.Unit),
		started:                atomic.NewBool(false),
		localJobKeys:           goset.NewSet[string](),
		peersJobKeys:           make(map[string][]string),
		peerJobKeysLock:        &sync.RWMutex{},
	}

	// apply the various options
	for _, opt := range opts {
		opt.Apply(server)
	}

	// set the server delegate
	server.maxJoinAttempts = maxRetries(server.joinTimeout, server.joinRetryInterval)
	server.broadcastMaxRetries = maxRetries(server.broadcastTimeout, server.broadcastRetryInterval)
	server.delegate = newDelegate(meta)

	// create the memberlist config
	mconfig := memberlist.DefaultLANConfig()
	mconfig.BindAddr = node.Host
	mconfig.BindPort = int(node.DiscoveryPort)
	mconfig.AdvertisePort = mconfig.BindPort
	mconfig.LogOutput = io.Discard
	mconfig.Name = node.PeersAddress()
	mconfig.Delegate = server.delegate
	mconfig.PushPullInterval = 0

	// set the server memberlist config
	server.memberConfig = mconfig

	return server, nil
}

// Start starts the cluster node
func (s *Service) Start(ctx context.Context) error {
	s.localOpsLock.Lock()
	defer s.localOpsLock.Unlock()

	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(s.provider.Initialize()).
		AddError(s.provider.Register()).
		AddError(s.join(ctx)).
		AddError(s.serve(ctx)).
		Error(); err != nil {
		return err
	}

	// create enough buffer to house the cluster events
	eventsCh := make(chan memberlist.NodeEvent, 256)
	s.memberConfig.Events = &memberlist.ChannelEventDelegate{
		Ch: eventsCh,
	}

	s.started.Store(true)

	go s.handleJoinedPeers()
	go s.eventsListener(eventsCh)

	s.logger.Infof("%s successfully started", s.node.String())
	return nil
}

// Stop stops gracefully the members service
func (s *Service) Stop(ctx context.Context) error {
	s.localOpsLock.Lock()
	defer s.localOpsLock.Unlock()

	// no-op when the node has not started
	if !s.started.Load() {
		return nil
	}

	// no matter the outcome the node is officially off
	s.started.Store(false)
	ctx, cancel := context.WithTimeout(ctx, s.shutdownTimeout)
	defer cancel()

	// stop the events and replication loop
	close(s.stopEventsListener)
	close(s.joinedQueue)
	s.httpClient.CloseIdleConnections()
	s.peersCache.reset()

	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(s.memberlist.Leave(s.shutdownTimeout)).
		AddError(s.provider.Deregister()).
		AddError(s.provider.Close()).
		AddError(s.memberlist.Shutdown()).
		AddError(s.httpServer.Shutdown(ctx)).
		Error(); err != nil {
		s.logger.Error(fmt.Errorf("%s failed to stop: %w", s.node.String(), err))
		return err
	}
	s.logger.Infof("%s successfully stopped", s.node.String())
	return nil
}

// Address returns the node host:port address
func (s *Service) Address() string {
	if !s.started.Load() {
		return ""
	}

	s.localOpsLock.RLock()
	addr := s.node.PeersAddress()
	s.localOpsLock.RUnlock()
	return addr
}

// PutPeerState handles push peers state request from remote peers
func (s *Service) PutPeerState(_ context.Context, request *connect.Request[internalpb.PutPeerStateRequest]) (*connect.Response[internalpb.PutPeerStateResponse], error) {
	s.peersLock.Lock()
	defer s.peersLock.Unlock()

	peerState := request.Msg.GetPeerState()
	peerAddress := request.Msg.GetPeerAddress()

	s.logger.Infof("%s handling peer=(%s) state", s.node.String(), peerAddress)
	s.peersCache.set(peerAddress, peerState)
	s.logger.Infof("%s successfully handled peer=(%s) state", s.node.String(), peerAddress)
	return connect.NewResponse(&internalpb.PutPeerStateResponse{Ack: true}), nil
}

// DeletePeerState handles the deletion of peer state
func (s *Service) DeletePeerState(_ context.Context, request *connect.Request[internalpb.DeletePeerStateRequest]) (*connect.Response[internalpb.DeletePeerStateResponse], error) {
	s.peersLock.Lock()
	defer s.peersLock.Unlock()

	peerAddress := request.Msg.GetPeerAddress()
	s.logger.Infof("%s handling peer=(%s) state deletion", s.node.String(), peerAddress)
	s.peersCache.remove(peerAddress)
	s.logger.Infof("%s successfully deleted peer=(%s) state", s.node.String(), peerAddress)
	return connect.NewResponse(new(internalpb.DeletePeerStateResponse)), nil
}

// PutJobKeys handles the put job key request
func (s *Service) PutJobKeys(_ context.Context, request *connect.Request[internalpb.PutJobKeysRequest]) (*connect.Response[internalpb.PutJobKeysResponse], error) {
	s.peersLock.Lock()

	peerAddress := request.Msg.GetPeerAddress()
	jobKeys := request.Msg.GetJobKeys()

	s.logger.Infof("%s handling peer=(%s) job keys", s.node.String(), peerAddress)

	s.peerJobKeysLock.Lock()
	s.peersJobKeys[peerAddress] = jobKeys
	s.peerJobKeysLock.Unlock()

	s.peersLock.Unlock()

	s.logger.Infof("%s successfully handled peer=(%s) job keys", s.node.String(), peerAddress)
	return connect.NewResponse(new(internalpb.PutJobKeysResponse)), nil
}

// DeleteJobKey handles the delete job key request
func (s *Service) DeleteJobKey(_ context.Context, request *connect.Request[internalpb.DeleteJobKeyRequest]) (*connect.Response[internalpb.DeleteJobKeyResponse], error) {
	s.peersLock.Lock()

	peerAddress := request.Msg.GetPeerAddress()
	jobKey := request.Msg.GetJobKey()

	s.logger.Infof("%s handling peer=(%s) jobkey=(%s) deletion", s.node.String(), peerAddress, jobKey)

	s.peerJobKeysLock.Lock()
	jobKeys := s.peersJobKeys[peerAddress]
	index := slices.Index(jobKeys, jobKey)
	if index >= 0 {
		jobKeys = append(jobKeys[:index], jobKeys[index+1:]...)
		s.peersJobKeys[peerAddress] = jobKeys
	}
	s.peerJobKeysLock.Unlock()

	s.peersLock.Unlock()

	s.logger.Infof("%s successfully delet peer=(%s) jobkey=(%s)", s.node.String(), jobKey)
	return connect.NewResponse(new(internalpb.DeleteJobKeyResponse)), nil
}

// DeleteActor handles the delete actor request from a remote peer
func (s *Service) DeleteActor(_ context.Context, request *connect.Request[internalpb.DeleteActorRequest]) (*connect.Response[internalpb.DeleteActorResponse], error) {
	s.peersLock.Lock()
	defer s.peersLock.Unlock()

	peerAddress := request.Msg.GetPeerAddress()
	actorRef := request.Msg.GetActorRef()
	name := actorRef.GetActorAddress().GetName()
	kind := actorRef.GetActorType()

	s.logger.Infof("%s handling peer=(%s) actor=(%s/%s) deletion", s.node.String(), peerAddress, name, kind)
	peerState, ok := s.peersCache.get(peerAddress)
	if !ok {
		s.logger.Warnf("[%s] has not found peer=(%s) sync record", s.node.String(), peerAddress)
		return nil, connect.NewError(connect.CodeNotFound, ErrPeerSyncNotFound)
	}

	index := -1
	for i, actor := range peerState.GetActors() {
		if proto.Equal(actorRef, actor) {
			index = i
			break
		}
	}

	if index >= 0 {
		peerState.Actors = append(peerState.Actors[:index], peerState.Actors[index+1:]...)
	}
	s.logger.Infof("%s successfully delete peer=(%s) actor=(%s/%s)", s.node.String(), peerAddress, name, kind)
	return connect.NewResponse(new(internalpb.DeleteActorResponse)), nil
}

// Actors returns the list of actors in the cluster
func (s *Service) Actors() []*internalpb.ActorRef {
	if !s.started.Load() {
		return nil
	}

	s.localOpsLock.RLock()
	tracked := make(map[string]types.Unit)
	var actors []*internalpb.ActorRef
	for _, actor := range s.localState.GetActors() {
		if _, ok := tracked[actor.GetActorAddress().GetName()]; !ok {
			actors = append(actors, actor)
			tracked[actor.GetActorAddress().GetName()] = types.Unit{}
		}
	}

	for _, localState := range s.peersCache.peerStates() {
		for _, actor := range localState.GetActors() {
			if _, ok := tracked[actor.GetActorAddress().GetName()]; !ok {
				actors = append(actors, actor)
				tracked[actor.GetActorAddress().GetName()] = types.Unit{}
			}
		}
	}
	s.localOpsLock.RUnlock()
	return actors
}

// GetPeerState fetches a given peer state from the cluster
func (s *Service) GetPeerState(peerAddress string) (*internalpb.PeerState, error) {
	if !s.started.Load() {
		return nil, ErrPeersServiceNotStarted
	}

	s.localOpsLock.RLock()
	defer s.localOpsLock.RUnlock()

	peerState, ok := s.peersCache.get(peerAddress)
	if !ok {
		s.logger.Warnf("(%s) has not found peer=(%s) sync record", s.node.String(), peerAddress)
		return nil, ErrPeerSyncNotFound
	}

	s.logger.Infof("(%s) successfully retrieved peer (%s) sync record", s.node.String(), peerAddress)
	return peerState, nil
}

// RemovePeerState removes a given peer from the node peers peersCache
// This action is only performed by the Leader node is the cluster
func (s *Service) RemovePeerState(ctx context.Context, peerAddress string) error {
	if !s.started.Load() {
		return ErrPeersServiceNotStarted
	}

	// clear its peersCache first
	s.peersCache.remove(peerAddress)

	// notify the rest of the cluster to clear their peersCache from this vilain
	s.logger.Infof("%s removing peer=(%s) from cluster", s.node.String(), peerAddress)
	peers, err := s.Peers()
	if err != nil {
		s.logger.Error(fmt.Errorf("%s failed to get peers: %w", s.node.String(), err))
		return err
	}

	s.localOpsLock.Lock()
	defer s.localOpsLock.Unlock()

	if len(peers) > 0 {
		eg, ctx := errgroup.WithContext(ctx)
		for _, peer := range peers {
			peer := peer
			eg.Go(func() error {
				client := internalpbconnect.NewPeersServiceClient(s.httpClient, http.URL(peer.Host, int(peer.Port)))
				_, err = client.DeletePeerState(ctx, connect.NewRequest(&internalpb.DeletePeerStateRequest{
					PeerAddress: peerAddress,
				}))

				if err != nil {
					s.logger.Error(fmt.Errorf("%s failed to remove peer=%s state from its peersCache: %w", peer.String(), peerAddress, err))
				}
				s.logger.Infof("%s successfully remove peer=%s state from its peersCache", peer.String(), peerAddress)
				return nil
			})
		}

		return eg.Wait()
	}
	return nil
}

// PutActor broadcasts the given actor to the list of the node's peers
func (s *Service) PutActor(ctx context.Context, actor *internalpb.ActorRef) error {
	if !s.started.Load() {
		return ErrPeersServiceNotStarted
	}

	name := actor.GetActorAddress().GetName()
	kind := actor.GetActorType()

	s.logger.Infof("%s replicating actor=[%s/%s] in the cluster", s.node.String(), name, kind)
	peers, err := s.Peers()
	if err != nil {
		s.logger.Error(fmt.Errorf("%s failed to get peers: %w", s.node.String(), err))
		return err
	}

	s.localOpsLock.Lock()
	defer s.localOpsLock.Unlock()

	// add to the local state actor
	s.localState.Actors = append(s.localState.GetActors(), actor)

	if len(peers) > 0 {
		eg, ctx := errgroup.WithContext(ctx)
		for _, peer := range peers {
			peer := peer
			eg.Go(func() error {
				s.logger.Infof("%s pushing peer state to peer=%s", s.node.String(), peer.PeerAddress())

				if err := s.broadcastState(ctx, peer); err != nil {
					s.logger.Error(fmt.Errorf("%s failed to push peer state to peer=%s: %w", s.node.String(), peer.PeerAddress(), err))
					return err
				}

				s.logger.Infof("%s successfully pushed peer state to peer=%s", s.node.String(), peer.PeerAddress())
				return nil
			})
		}
		return eg.Wait()
	}
	return nil
}

// GetActor fetches an actor from the cluster
func (s *Service) GetActor(actorName string) (*internalpb.ActorRef, error) {
	if !s.started.Load() {
		return nil, ErrPeersServiceNotStarted
	}

	s.localOpsLock.RLock()
	s.logger.Infof("(%s) retrieving actor (%s) from the cluster", s.node.String(), actorName)
	actors := s.localState.GetActors()
	for _, actor := range actors {
		name := actor.GetActorAddress().GetName()
		if actorName == name {
			s.localOpsLock.RUnlock()
			s.logger.Infof("(%s) successfully retrieved from the cluster actor (%s)", s.node.String(), actor.GetActorAddress().GetName())
			return actor, nil
		}
	}

	for _, localState := range s.peersCache.peerStates() {
		actors := localState.GetActors()
		for _, actor := range actors {
			name := actor.GetActorAddress().GetName()
			if actorName == name {
				s.localOpsLock.RUnlock()
				s.logger.Infof("(%s) successfully retrieved from the cluster actor (%s)", s.node.String(), actor.GetActorAddress().GetName())
				return actor, nil
			}
		}
	}

	s.logger.Warnf("(%s) could not find actor=%s the cluster", s.node.String(), actorName)
	s.localOpsLock.RUnlock()
	return nil, ErrActorNotFound
}

// RemoveActor removes a given actor from the cluster.
// An actor is removed from the cluster when this actor has been passivated.
func (s *Service) RemoveActor(ctx context.Context, actorName string) error {
	if !s.started.Load() {
		return ErrPeersServiceNotStarted
	}

	s.localOpsLock.Lock()
	defer s.localOpsLock.Unlock()

	index := -1
	for i, actor := range s.localState.GetActors() {
		name := actor.GetActorAddress().GetName()
		if actorName == name {
			index = i
			break
		}
	}

	if index >= 0 {
		// first remove the given actor from the local state
		s.localState.Actors = append(s.localState.GetActors()[:index], s.localState.GetActors()[index+1:]...)

		// secondly replicate the state to the rest of the cluster
		s.logger.Infof("%s replicating state in the cluster", s.node.String())
		peers, err := s.Peers()
		if err != nil {
			s.logger.Error(fmt.Errorf("%s failed to get peers: %w", s.node.String(), err))
			return err
		}

		if len(peers) > 0 {
			eg, ctx := errgroup.WithContext(ctx)
			for _, peer := range peers {
				peer := peer
				eg.Go(func() error {
					s.logger.Infof("%s pushing peer state to peer=%s", s.node.String(), peer.PeerAddress())

					if err := s.broadcastState(ctx, peer); err != nil {
						s.logger.Error(fmt.Errorf("%s failed to push peer state to peer=%s: %w", s.node.String(), peer.PeerAddress(), err))
						return err
					}

					s.logger.Infof("%s successfully pushed peer state to peer=%s", s.node.String(), peer.PeerAddress())
					return nil
				})
			}
			return eg.Wait()
		}
	}
	return ErrActorNotFound
}

// PutJobKey broadcasts the given job key to the list the node's peers
func (s *Service) PutJobKey(ctx context.Context, jobKey string) error {
	if !s.started.Load() {
		return ErrPeersServiceNotStarted
	}

	s.logger.Infof("%s replicating jobkey=(%s) in the cluster", s.node.String(), jobKey)
	peers, err := s.Peers()
	if err != nil {
		s.logger.Error(fmt.Errorf("%s failed to get peers: %w", s.node.String(), err))
		return err
	}

	s.localOpsLock.Lock()
	defer s.localOpsLock.Unlock()

	// add to the local cache
	s.localJobKeys.Add(jobKey)

	if len(peers) > 0 {
		eg, ctx := errgroup.WithContext(ctx)
		for _, peer := range peers {
			peer := peer
			eg.Go(func() error {
				s.logger.Infof("%s pushing peer job keys to peer=%s", s.node.String(), peer.PeerAddress())

				if err := s.broadcastJobKeys(ctx, peer); err != nil {
					s.logger.Error(fmt.Errorf("%s failed to push peer job keys to peer=%s: %w", s.node.String(), peer.PeerAddress(), err))
					return err
				}

				s.logger.Infof("%s successfully pushed peer job keys to peer=%s", s.node.String(), peer.PeerAddress())
				return nil
			})
		}
		return eg.Wait()
	}
	return nil
}

// GetJobKey fetches a given job key from the cluster
func (s *Service) GetJobKey(jobKey string) (*string, error) {
	if !s.started.Load() {
		return nil, ErrPeersServiceNotStarted
	}

	s.localOpsLock.RLock()
	s.logger.Infof("[%s] retrieving jobkey=(%s) from the cluster", s.node.String(), jobKey)
	jobKeys := s.localJobKeys
	if jobKeys.Contains(jobKey) {
		s.logger.Infof("[%s] successfully retrieved from the cluster jobkey=(%s)", s.node.String(), jobKey)
		s.localOpsLock.RUnlock()
		return &jobKey, nil
	}

	s.peerJobKeysLock.RLock()
	for _, keys := range s.peersJobKeys {
		if slices.Contains(keys, jobKey) {
			s.logger.Infof("[%s] successfully retrieved from the cluster jobkey=(%s)", s.node.String(), jobKey)
			s.peerJobKeysLock.RUnlock()
			s.localOpsLock.RUnlock()
			return &jobKey, nil
		}
	}

	s.logger.Warnf("[%s] could not find jobkey=%s the cluster", s.node.String(), jobKey)
	s.peerJobKeysLock.RUnlock()
	s.localOpsLock.RUnlock()
	return nil, ErrKeyNotFound
}

// RemoveJobKey removes a given job key from the cluster
func (s *Service) RemoveJobKey(ctx context.Context, jobKey string) error {
	if !s.started.Load() {
		return ErrPeersServiceNotStarted
	}

	s.localOpsLock.Lock()
	defer s.localOpsLock.Unlock()

	if s.localJobKeys.Contains(jobKey) {
		// first remove the given actor from the local state
		s.localJobKeys.Remove(jobKey)
		// secondly replicate the state to the rest of the cluster
		s.logger.Infof("%s replicating job keys in the cluster", s.node.String())
		peers, err := s.Peers()
		if err != nil {
			s.logger.Error(fmt.Errorf("%s failed to get peers: %w", s.node.String(), err))
			return err
		}

		if len(peers) > 0 {
			eg, ctx := errgroup.WithContext(ctx)
			for _, peer := range peers {
				peer := peer
				eg.Go(func() error {
					s.logger.Infof("%s pushing peer job keys to peer=%s", s.node.String(), peer.PeerAddress())

					if err := s.broadcastJobKeys(ctx, peer); err != nil {
						s.logger.Error(fmt.Errorf("%s failed to push peer job keys to peer=%s: %w", s.node.String(), peer.PeerAddress(), err))
						return err
					}

					s.logger.Infof("%s successfully pushed peer job keys to peer=%s", s.node.String(), peer.PeerAddress())
					return nil
				})
			}
			return eg.Wait()
		}
	}

	return ErrKeyNotFound
}

// Peers returns the list of peers
func (s *Service) Peers() ([]*Peer, error) {
	if !s.started.Load() {
		return nil, ErrPeersServiceNotStarted
	}

	s.peersLock.Lock()
	mnodes := s.memberlist.Members()
	s.peersLock.Unlock()
	members := make([]*Peer, 0, len(mnodes))
	for _, mnode := range mnodes {
		member, err := peerFromMeta(mnode.Meta)
		if err != nil {
			return nil, err
		}
		if member != nil && member.DiscoveryAddress() != s.node.DiscoveryAddress() {
			members = append(members, member)
		}
	}
	return members, nil
}

// Whoami returns the node member data
func (s *Service) Whoami() *Peer {
	if !s.started.Load() {
		return nil
	}

	s.localOpsLock.Lock()
	meta := s.peerMeta
	s.localOpsLock.Unlock()
	return &Peer{
		Name:          meta.GetName(),
		Host:          meta.GetHost(),
		Port:          meta.GetPort(),
		DiscoveryPort: meta.GetDiscoveryPort(),
		RemotingPort:  meta.GetRemotingPort(),
		CreatedAt:     meta.GetCreationTime().AsTime(),
	}
}

// Leader returns the eldest member of the cluster
// Leadership here is based upon node time creation
func (s *Service) Leader() (*Peer, error) {
	if !s.started.Load() {
		return nil, ErrPeersServiceNotStarted
	}

	peers, err := s.Peers()
	if err != nil {
		return nil, err
	}
	s.localOpsLock.Lock()
	meta := s.peerMeta
	s.localOpsLock.Unlock()

	peers = append(peers, &Peer{
		Name:          meta.GetName(),
		Host:          meta.GetHost(),
		Port:          meta.GetPort(),
		DiscoveryPort: meta.GetDiscoveryPort(),
		RemotingPort:  meta.GetRemotingPort(),
		CreatedAt:     meta.GetCreationTime().AsTime(),
	})

	slices.SortStableFunc(peers, func(a, b *Peer) int {
		if a.CreatedAt.Unix() < b.CreatedAt.Unix() {
			return -1
		}
		return 1
	})

	return peers[0], nil
}

// IsLeader states whether the given cluster node is a leader or not at a given
// point in time in the cluster
func (s *Service) IsLeader() (bool, error) {
	if !s.started.Load() {
		return false, ErrPeersServiceNotStarted
	}

	me := s.Whoami()
	leader, err := s.Leader()
	if err != nil {
		return false, err
	}
	return me.PeerAddress() == leader.PeerAddress(), nil
}

// Events returns a channel where cluster events are published
func (s *Service) Events() <-chan *Event {
	s.eventsLock.Lock()
	ch := s.eventsQueue
	s.eventsLock.Unlock()
	return ch
}

// join attempts to join an existing cluster if node peers is provided
func (s *Service) join(ctx context.Context) error {
	mlist, err := memberlist.Create(s.memberConfig)
	if err != nil {
		s.logger.Error(fmt.Errorf("failed to create memberlist: %w", err))
		return err
	}

	ctx2, cancel := context.WithTimeout(ctx, s.joinTimeout)
	var peers []string
	retrier := retry.NewRetrier(s.maxJoinAttempts, s.joinRetryInterval, s.joinRetryInterval)
	if err := retrier.RunContext(ctx2, func(ctx context.Context) error { // nolint
		peers, err = s.provider.DiscoverPeers()
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		cancel()
		return err
	}

	cancel()

	// set the mlist
	s.memberlist = mlist
	if len(peers) > 0 {
		if _, err := s.memberlist.Join(peers); err != nil {
			s.logger.Error(fmt.Errorf("%s failed to join cluster: %w", s.node.String(), err))
			return err
		}
		s.logger.Infof("%s successfully joined cluster: [%s]",
			s.node.String(),
			strings.Join(peers, ","))
	}
	return nil
}

// handleJoinedPeers handles peers that join the existing cluster
func (s *Service) handleJoinedPeers() {
	for peer := range s.joinedQueue {
		s.logger.Infof("%s pushing peer state to joined peer=%s", s.node.String(), peer.String())

		if err := errorschain.New(errorschain.ReturnFirst()).
			AddError(s.broadcastState(context.Background(), peer)).
			AddError(s.broadcastJobKeys(context.Background(), peer)).
			Error(); err != nil {
			s.logger.Error(fmt.Errorf("%s failed to push peer state to joined peer=%s: %w", s.node.String(), peer.String(), err))
			continue
		}

		s.logger.Infof("%s successfully to push peer state to joined peer=%s", s.node.String(), peer.String())
	}
}

// eventsListener listens to cluster events to handle them
func (s *Service) eventsListener(eventsCh chan memberlist.NodeEvent) {
	for {
		select {
		case event := <-eventsCh:
			var addr string
			// skip this node
			if event.Node != nil {
				addr = net.JoinHostPort(event.Node.Addr.String(), strconv.Itoa(int(event.Node.Port)))
				if addr == s.node.DiscoveryAddress() {
					continue
				}
			}

			var eventType EventType
			switch event.Event {
			case memberlist.NodeJoin:
				eventType = NodeJoined
			case memberlist.NodeLeave:
				eventType = NodeLeft
			case memberlist.NodeUpdate:
				// TODO: maybe handle this
				continue
			}

			// parse the node meta information, log an eventual error during parsing and skip the event
			peer, err := peerFromMeta(event.Node.Meta)
			if err != nil {
				s.logger.Errorf("failed to marshal node meta from cluster event: %v", err)
				continue
			}

			// routine check
			if addr != peer.DiscoveryAddress() {
				s.logger.Warnf("event peer=%s parse does not match addr=[%s]", peer.String(), addr)
				continue
			}

			// send the event to the event channels
			s.eventsLock.Lock()
			s.logger.Debugf("%s received (%s) from %s cluster event",
				s.node.String(),
				eventType,
				peer.String())

			s.eventsQueue <- &Event{
				Peer: peer,
				Time: time.Now().UTC(),
				Type: eventType,
			}

			// push peer to queue of th peers that has joined the cluster
			if eventType == NodeJoined {
				s.joinedQueue <- peer
			}

			s.eventsLock.Unlock()
		case <-s.stopEventsListener:
			// finish listening to cluster events
			close(s.eventsQueue)
			return
		}
	}
}

// serve start the underlying http server
func (s *Service) serve(ctx context.Context) error {
	pattern, handler := internalpbconnect.NewPeersServiceHandler(s)
	mux := nethttp.NewServeMux()
	mux.Handle(pattern, handler)
	server := http.NewServer(ctx, s.node.Host, s.node.PeersPort, mux)
	s.httpServer = server

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil {
			if !errors.Is(err, nethttp.ErrServerClosed) {
				// just panic
				s.logger.Panic(fmt.Errorf("failed to start members service: %w", err))
			}
		}
	}()
	return nil
}

// broadcastState broadcasts the local state to the given peer
func (s *Service) broadcastState(ctx context.Context, peer *Peer) error {
	ctx, cancel := context.WithTimeout(ctx, s.broadcastTimeout)
	defer cancel()
	retrier := retry.NewRetrier(s.broadcastMaxRetries, s.broadcastRetryInterval, s.broadcastRetryInterval)

	return retrier.RunContext(ctx, func(ctx context.Context) error {
		client := internalpbconnect.NewPeersServiceClient(s.httpClient, http.URL(peer.Host, int(peer.Port)))
		_, err := client.PutPeerState(ctx, connect.NewRequest(&internalpb.PutPeerStateRequest{
			PeerAddress: s.node.PeersAddress(),
			PeerState:   s.localState,
		}))
		return err
	})
}

// broadcastJobKeys broadcasts the local job keys to the given peer
func (s *Service) broadcastJobKeys(ctx context.Context, peer *Peer) error {
	ctx, cancel := context.WithTimeout(ctx, s.broadcastTimeout)
	defer cancel()
	retrier := retry.NewRetrier(s.broadcastMaxRetries, s.broadcastRetryInterval, s.broadcastRetryInterval)
	return retrier.RunContext(ctx, func(ctx context.Context) error {
		client := internalpbconnect.NewPeersServiceClient(s.httpClient, http.URL(peer.Host, int(peer.Port)))
		_, err := client.PutJobKeys(ctx, connect.NewRequest(&internalpb.PutJobKeysRequest{
			PeerAddress: s.node.PeersAddress(),
			JobKeys:     s.localJobKeys.ToSlice(),
		}))
		return err
	})
}

func maxRetries(timeout time.Duration, retryInterval time.Duration) int {
	return int(timeout / retryInterval)
}
