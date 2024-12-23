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

package members

import (
	"context"
	"errors"
	"fmt"
	"net"
	nethttp "net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/flowchartsman/retry"
	"github.com/hashicorp/memberlist"
	"go.uber.org/atomic"
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

type Server struct {
	delegate *delegate

	meta            *internalpb.PeerMeta
	memberConfig    *memberlist.Config
	memberlist      *memberlist.Memberlist
	started         *atomic.Bool
	shutdownTimeout time.Duration

	httpServer        *nethttp.Server
	httpClient        *nethttp.Client
	mu                *sync.RWMutex
	maxJoinAttempts   int
	joinRetryInterval time.Duration
	joinTimeout       time.Duration
	provider          discovery.Provider
	logger            log.Logger

	node                *discovery.Node
	replicationInterval time.Duration
	stopReplicationLoop chan types.Unit

	stopEventsListener chan types.Unit
	eventsLock         *sync.Mutex
	peersLock          *sync.Mutex
	eventsChan         chan *Event
}

// NewServer creates an instance of Server
func NewServer(name string, provider discovery.Provider, node *discovery.Node, opts ...Option) (*Server, error) {
	bindIP, err := tcp.GetBindIP(node.PeersAddress())
	if err != nil {
		return nil, fmt.Errorf("failed to resolve TCP discoveryAddress: %w", err)
	}

	node.Host = bindIP
	meta := &internalpb.PeerMeta{
		Name:          name,
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

	server := &Server{
		meta:                meta,
		mu:                  &sync.RWMutex{},
		node:                node,
		logger:              log.DiscardLogger,
		provider:            provider,
		maxJoinAttempts:     5,
		joinRetryInterval:   time.Second,
		shutdownTimeout:     3 * time.Second,
		joinTimeout:         time.Minute,
		replicationInterval: time.Second,
		eventsLock:          &sync.Mutex{},
		peersLock:           &sync.Mutex{},
		eventsChan:          make(chan *Event, 1),
		httpClient:          http.NewClient(),
		stopEventsListener:  make(chan types.Unit),
		stopReplicationLoop: make(chan types.Unit),
		started:             atomic.NewBool(false),
	}

	// apply the various options
	for _, opt := range opts {
		opt.Apply(server)
	}

	// set the server delegate
	server.delegate = newDelegate(meta, peerState, server.logger)

	// create the memberlist config
	mconfig := memberlist.DefaultLANConfig()
	mconfig.BindAddr = node.Host
	mconfig.BindPort = int(node.DiscoveryPort)
	mconfig.AdvertisePort = mconfig.BindPort
	mconfig.LogOutput = newLogWriter(server.logger)
	mconfig.Name = name
	mconfig.Delegate = server.delegate
	mconfig.PushPullInterval = server.replicationInterval

	// set the server memberlist config
	server.memberConfig = mconfig

	return server, nil
}

var (
	// enforce compilation error
	_ IServer                                 = (*Server)(nil)
	_ internalpbconnect.MembersServiceHandler = (*Server)(nil)
)

// Start starts the cluster node
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	go s.eventsListener(eventsCh)

	s.logger.Infof("%s successfully started", s.node.DiscoveryAddress())
	return nil
}

// Stop stops gracefully the members service
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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
	close(s.stopReplicationLoop)
	s.httpClient.CloseIdleConnections()

	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(s.memberlist.Leave(s.shutdownTimeout)).
		AddError(s.provider.Deregister()).
		AddError(s.provider.Close()).
		AddError(s.memberlist.Shutdown()).
		AddError(s.httpServer.Shutdown(ctx)).
		Error(); err != nil {
		s.logger.Error(fmt.Errorf("%s failed to stop: %w", s.node.DiscoveryAddress(), err))
		return err
	}
	s.logger.Infof("%s successfully stopped", s.node.DiscoveryAddress())
	return nil
}

// Peers returns the list of peers
func (s *Server) Peers() ([]*Member, error) {
	s.peersLock.Lock()
	mnodes := s.memberlist.Members()
	s.peersLock.Unlock()
	members := make([]*Member, 0, len(mnodes))
	for _, mnode := range mnodes {
		member, err := memberFromMeta(mnode.Meta)
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
func (s *Server) Whoami() *Member {
	s.mu.Lock()
	meta := s.meta
	s.mu.Unlock()
	return &Member{
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
func (s *Server) Leader() (*Member, error) {
	peers, err := s.Peers()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	meta := s.meta
	s.mu.Unlock()

	peers = append(peers, &Member{
		Name:          meta.GetName(),
		Host:          meta.GetHost(),
		Port:          meta.GetPort(),
		DiscoveryPort: meta.GetDiscoveryPort(),
		RemotingPort:  meta.GetRemotingPort(),
		CreatedAt:     meta.GetCreationTime().AsTime(),
	})

	slices.SortStableFunc(peers, func(a, b *Member) int {
		if a.CreatedAt.Unix() < b.CreatedAt.Unix() {
			return -1
		}
		return 1
	})

	return peers[0], nil
}

// Address returns the node host:port address
func (s *Server) Address() string {
	s.mu.RLock()
	addr := s.node.PeersAddress()
	s.mu.RUnlock()
	return addr
}

// PutActor sets the given actor on the localState of the node and let the replicator
// pushes to the rest of the cluster
func (s *Server) PutActor(actor *internalpb.ActorRef) error {
	name := actor.GetActorAddress().GetName()
	kind := actor.GetActorType()

	s.logger.Infof("%s replicating actor=[%s/%s] in the cluster", s.node.PeersAddress(), name, kind)
	s.mu.RLock()
	// first attempt to get the actor
	actual, _, err := s.delegate.GetActor(name, kind)
	switch {
	case errors.Is(err, ErrActorNotFound):
		s.mu.RUnlock()
	default:
		if actual != nil && !proto.Equal(actual, new(internalpb.ActorRef)) {
			s.logger.Warnf("%s cannot duplicate actor=[%s/%s] in the cluster", s.node.PeersAddress(), name, kind)
			s.mu.RUnlock()
			return ErrActorAlreadyExists
		}
		s.mu.RUnlock()
	}

	s.mu.Lock()
	s.delegate.PutActor(actor)
	s.logger.Infof("%s successfully replicated actor=[%s/%s] in the cluster", s.node.PeersAddress(), name, kind)
	s.mu.Unlock()
	return nil
}

// GetActor fetches an actor from the cluster
func (s *Server) GetActor(actorName, actorKind string) (*internalpb.ActorRef, error) {
	s.mu.RLock()
	s.logger.Infof("[%s] retrieving actor (%s) from the cluster", s.node.PeersAddress(), actorName)
	actor, _, err := s.delegate.GetActor(actorName, actorKind)
	if err != nil {
		s.logger.Warnf("[%s] could not find actor=%s the cluster", s.node.PeersAddress(), actorName)
		s.mu.RUnlock()
		return nil, err
	}
	s.logger.Infof("[%s] successfully retrieved from the cluster actor (%s)", s.node.PeersAddress(), actor.GetActorAddress().GetName())
	s.mu.RUnlock()
	return actor, nil
}

// SetSchedulerJobKey sets a given key to the cluster
func (s *Server) SetSchedulerJobKey(key string) error {
	s.logger.Infof("%s replicating key=%s in the cluster", s.node.PeersAddress(), key)
	s.mu.RLock()
	// first attempt to get the actor
	actual, _, err := s.delegate.GetJobKey(key)
	switch {
	case errors.Is(err, ErrActorNotFound):
		s.mu.RUnlock()
	default:
		if actual != nil {
			s.logger.Warnf("%s cannot duplicate key=%s in the cluster", s.node.PeersAddress(), key)
			s.mu.RUnlock()
			return ErrKeyAlreadyExists
		}
		s.mu.RUnlock()
	}

	s.mu.Lock()
	s.delegate.PutJobKey(key)
	s.logger.Infof("%s successfully replicated key=%s in the cluster", s.node.PeersAddress(), key)
	s.mu.Unlock()
	return nil
}

// SchedulerJobKeyExists checks the existence of a given key
func (s *Server) SchedulerJobKeyExists(key string) bool {
	s.mu.RLock()
	s.logger.Infof("checking key (%s) existence in the cluster", key)
	_, _, err := s.delegate.GetJobKey(key)
	if errors.Is(err, ErrKeyNotFound) {
		s.logger.Errorf("[%s] failed to check scheduler job key (%s) existence", s.node.PeersAddress(), key)
		s.mu.RUnlock()
		return false
	}
	s.mu.RUnlock()
	return true
}

// UnsetSchedulerJobKey unsets the already set given key in the cluster
func (s *Server) UnsetSchedulerJobKey(ctx context.Context, key string) error {
	s.logger.Infof("%s unsetting key (%s) from cluster", s.node.PeersAddress(), key)
	s.mu.RLock()
	_, owner, err := s.delegate.GetJobKey(key)
	if errors.Is(err, ErrKeyNotFound) {
		s.logger.Warnf("[%s] could not find key=%s the cluster", s.node.PeersAddress(), key)
		s.mu.RUnlock()
		return ErrKeyNotFound
	}

	s.mu.RUnlock()
	s.mu.Lock()

	if owner == s.node.PeersAddress() {
		if err := s.delegate.DeleteJobKey(key); errors.Is(err, ErrKeyNotFound) {
			s.logger.Warnf("[%s] could not find key=%s the cluster", s.node.PeersAddress(), key)
			s.mu.Unlock()
			return ErrKeyNotFound
		}
		s.logger.Infof("%s key (%s) successfully unset", s.node.PeersAddress(), key)
		s.mu.Unlock()
		return nil
	}

	request := connect.NewRequest(&internalpb.RemoveKeyRequest{
		PeerAddress: owner,
		Key:         key,
	})

	s.logger.Infof("%s requesting %s to unset key (%s) from cluster", s.node.PeersAddress(), owner, key)
	service := internalpbconnect.NewMembersServiceClient(s.httpClient, http.HostAndPortURL(owner))
	if _, err := service.RemoveKey(ctx, request); err != nil {
		s.logger.Errorf("[%s] failed to remove key (%s) the cluster via %s: %v", s.node.PeersAddress(), key, owner, err)
		s.mu.Unlock()
		return err
	}

	s.logger.Infof("%s key (%s) successfully unset from cluster via %s", s.node.PeersAddress(), key, owner)
	s.mu.Unlock()
	return nil
}

// DeleteActor removes a given actor from the cluster.
// An actor is removed from the cluster when this actor has been passivated.
func (s *Server) DeleteActor(ctx context.Context, actorName, actorKind string) error {
	s.logger.Infof("%s removing actor (%s) from cluster", s.node.PeersAddress(), actorName)
	s.mu.RLock()
	// first attempt to get the actor
	actor, owner, err := s.delegate.GetActor(actorName, actorKind)
	if err != nil {
		s.logger.Warnf("[%s] could not find actor=%s the cluster", s.node.PeersAddress(), actorName)
		s.mu.RUnlock()
		return err
	}

	s.mu.RUnlock()
	s.mu.Lock()

	if owner == s.node.PeersAddress() {
		s.delegate.DeleteActor(actorName, actorKind)
		s.logger.Infof("%s successfully removed actor (%s) from the cluster", s.node.PeersAddress(), actorName)
		s.mu.Unlock()
		return nil
	}

	s.logger.Infof("%s requesting %s to remove actor (%s) from cluster", s.node.PeersAddress(), owner, actorName)
	request := connect.NewRequest(&internalpb.RemoveActorRequest{
		PeerAddress: owner,
		ActorName:   actor.GetActorAddress().GetName(),
		ActorKind:   actor.GetActorType(),
	})

	service := internalpbconnect.NewMembersServiceClient(s.httpClient, http.HostAndPortURL(owner))
	_, err = service.RemoveActor(ctx, request)
	if err != nil {
		s.logger.Errorf("[%s] failed to remove actor=(%s) record from cluster via %s: %v", s.node.PeersAddress(), actorName, owner, err)
		s.mu.Lock()
		return err
	}

	s.logger.Infof("%s successfully removed actor (%s) from the cluster via %s", s.node.PeersAddress(), actorName, owner)
	s.mu.Unlock()
	return nil
}

// GetState fetches a given peer state from the cluster
func (s *Server) GetState(peerAddress string) (*internalpb.PeerState, error) {
	s.mu.RLock()
	peerState, err := s.delegate.GetState(peerAddress)
	if errors.Is(err, ErrPeerSyncNotFound) {
		s.mu.RUnlock()
		s.logger.Warnf("[%s] has not found peer=(%s) sync record", s.node.PeersAddress(), peerAddress)
		return nil, ErrPeerSyncNotFound
	}

	s.mu.RUnlock()
	s.logger.Infof("[%s] successfully retrieved peer (%s) sync record", s.node.PeersAddress(), peerAddress)
	return peerState, nil
}

// IsLeader states whether the given cluster node is a leader or not at a given
// point in time in the cluster
func (s *Server) IsLeader() (bool, error) {
	me := s.Whoami()
	leader, err := s.Leader()
	if err != nil {
		return false, err
	}
	return me.PeerAddress() == leader.PeerAddress(), nil
}

// Events returns a channel where cluster events are published
func (s *Server) Events() <-chan *Event {
	s.eventsLock.Lock()
	ch := s.eventsChan
	s.eventsLock.Unlock()
	return ch
}

// RemoveActor handles remove actors from the cluster peers
func (s *Server) RemoveActor(ctx context.Context, request *connect.Request[internalpb.RemoveActorRequest]) (*connect.Response[internalpb.RemoveActorResponse], error) {
	peerAddress := request.Msg.GetPeerAddress()
	actorName := request.Msg.GetActorName()
	actorKind := request.Msg.GetActorKind()
	if s.node.PeersAddress() != peerAddress {
		err := fmt.Errorf("peer address (%s) is not the owner of the actor=[%s/%s]", peerAddress, actorName, actorKind)
		s.logger.Error(err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	s.logger.Infof("%s deleting actor=[%s/%s] from the cluster", s.node.PeersAddress(), actorName, actorKind)
	if err := s.DeleteActor(ctx, actorName, actorKind); err != nil {
		s.logger.Error(fmt.Errorf("failed to delete actor=[%s/%s] record from cluster: %w", actorName, actorKind, err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	s.logger.Infof("%s successfully deleted actor=[%s/%s] from the cluster", s.node.PeersAddress(), actorName, actorKind)
	return connect.NewResponse(new(internalpb.RemoveActorResponse)), nil
}

// RemoveKey handles remove key from the cluster peers
func (s *Server) RemoveKey(ctx context.Context, request *connect.Request[internalpb.RemoveKeyRequest]) (*connect.Response[internalpb.RemoveKeyResponse], error) {
	peerAddress := request.Msg.GetPeerAddress()
	key := request.Msg.GetKey()
	if s.node.PeersAddress() != peerAddress {
		err := fmt.Errorf("peer address (%s) is not the owner of the key=(%s)", peerAddress, key)
		s.logger.Error(err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	s.logger.Infof("%s deleting key=(%s) from the cluster", s.node.PeersAddress(), key)
	if err := s.UnsetSchedulerJobKey(ctx, key); err != nil {
		s.logger.Error(fmt.Errorf("failed to delete key=(%s) record from cluster: %w", key, err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	s.logger.Infof("%s successfully deleted key=(%s) from the cluster", peerAddress, key)
	return connect.NewResponse(new(internalpb.RemoveKeyResponse)), nil
}

// join attempts to join an existing cluster if node peers is provided
func (s *Server) join(ctx context.Context) error {
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
			s.logger.Error(fmt.Errorf("failed to join cluster: %w", err))
			return err
		}
		s.logger.Infof("%s successfully joined cluster: [%s]", s.node.DiscoveryAddress(), strings.Join(peers, ","))
	}
	return nil
}

// eventsListener listens to cluster events to handle them
func (s *Server) eventsListener(eventsCh chan memberlist.NodeEvent) {
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
			member, err := memberFromMeta(event.Node.Meta)
			if err != nil {
				s.logger.Errorf("failed to marshal node meta from cluster event: %v", err)
				continue
			}

			// send the event to the event channels
			s.eventsLock.Lock()
			s.logger.Debugf("%s received (%s):[addr=(%s)] cluster event", s.node.PeersAddress(), eventType, addr)
			s.eventsChan <- &Event{
				Member: member,
				Time:   time.Now().UTC(),
				Type:   eventType,
			}
			s.eventsLock.Unlock()
		case <-s.stopEventsListener:
			// finish listening to cluster events
			close(s.eventsChan)
			return
		}
	}
}

// serve start the underlying http server
func (s *Server) serve(ctx context.Context) error {
	pattern, handler := internalpbconnect.NewMembersServiceHandler(s)
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
