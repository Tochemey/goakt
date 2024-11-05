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

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/flowchartsman/retry"
	"github.com/hashicorp/memberlist"
	"github.com/reugn/go-quartz/logger"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/errorschain"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/log"
)

var NoExpiry = time.Time{}

const (
	// ActorsGroup defines the actors group
	ActorsGroup = "Actors"
	// JobsGroups defines the scheduler job group
	// This is necessary when doing remote scheduling in cluster mode
	JobsGroups = "Jobs"
	// PeerStatesGroup defines the peers state group
	PeerStatesGroup = "PeersStates"
)

// Node defines the cluster Node
type Node struct {
	mconfig  *memberlist.Config
	mlist    *memberlist.Memberlist
	delegate *nodeDelegate
	started  *atomic.Bool
	// specifies the minimum number of cluster members
	// the default values is 1
	minimumPeersQuorum uint
	maxJoinAttempts    int

	// specifies the Node name
	name string

	// specifies the logger
	logger log.Logger

	// specifies the underlying node
	node *discovery.Node

	// specifies the discovery provider
	provider discovery.Provider

	shutdownTimeout      time.Duration
	maxJoinTimeout       time.Duration
	maxJoinRetryInterval time.Duration
	syncInterval         time.Duration

	events                chan *Event
	eventsLock            *sync.Mutex
	lock                  *sync.Mutex
	stopEventsListenerSig chan types.Unit

	actorsGroupLock     *sync.RWMutex
	peerStatesGroupLock *sync.RWMutex
	jobsGroupLock       *sync.RWMutex

	// specifies the node state
	peerState *internalpb.PeerState
}

// enforce compilation error
var _ Interface = (*Node)(nil)

// NewNode creates an instance of Node
func NewNode(name string, disco discovery.Provider, host *discovery.Node, opts ...NodeOption) *Node {
	// create an instance of Node
	n := &Node{
		logger:                log.DefaultLogger,
		name:                  name,
		provider:              disco,
		shutdownTimeout:       3 * time.Second,
		events:                make(chan *Event, 20),
		minimumPeersQuorum:    1,
		maxJoinTimeout:        time.Second,
		maxJoinRetryInterval:  time.Second,
		syncInterval:          time.Minute,
		maxJoinAttempts:       10,
		lock:                  new(sync.Mutex),
		eventsLock:            new(sync.Mutex),
		actorsGroupLock:       new(sync.RWMutex),
		jobsGroupLock:         new(sync.RWMutex),
		peerStatesGroupLock:   new(sync.RWMutex),
		stopEventsListenerSig: make(chan types.Unit, 1),
		started:               atomic.NewBool(false),
	}

	for _, opt := range opts {
		opt.Apply(n)
	}

	// set the host startNode
	n.node = host

	return n
}

// Start starts the Engine.
func (n *Node) Start(ctx context.Context) error {
	logger := n.logger

	logger.Infof("Starting GoAkt cluster Engine service on host=(%s)....🤔", n.node.PeersAddress())

	// create the memberlist configuration
	n.mconfig = memberlist.DefaultLANConfig()
	n.mconfig.BindAddr = n.node.Host
	n.mconfig.BindPort = n.node.DiscoveryPort
	n.mconfig.AdvertisePort = n.node.DiscoveryPort
	n.mconfig.LogOutput = newLogWriter(n.logger)
	n.mconfig.Name = n.node.DiscoveryAddress()
	n.mconfig.PushPullInterval = n.syncInterval

	// set the peer state
	n.peerState = &internalpb.PeerState{
		Host:         n.Host(),
		RemotingPort: int32(n.node.RemotingPort),
		PeersPort:    int32(n.node.PeersPort),
		Actors:       []*internalpb.ActorRef{},
	}

	// get the nodeDelegate
	delegate, err := n.newNodeDelegate()
	if err != nil {
		logger.Error(fmt.Errorf("failed to create the discovery engine nodeDelegate: %w", err))
		return err
	}

	n.delegate = delegate
	// set the nodeDelegate
	n.mconfig.Delegate = n.delegate

	// start process
	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(n.provider.Initialize()).
		AddError(n.provider.Register()).
		AddError(n.joinCluster(ctx)).
		Error(); err != nil {
		return err
	}

	// create enough buffer to house the cluster events
	// TODO: revisit this number
	eventsCh := make(chan memberlist.NodeEvent, 256)
	n.mconfig.Events = &memberlist.ChannelEventDelegate{
		Ch: eventsCh,
	}

	n.started.Store(true)
	// start listening to events
	go n.eventsListener(eventsCh)

	logger.Infof("GoAkt cluster Engine=(%s) successfully started.", n.name)
	return nil
}

// Stop stops the Engine gracefully
func (n *Node) Stop(ctx context.Context) error {
	if !n.started.Load() {
		return nil
	}

	// set the cacheLogger
	logger := n.logger

	// add some logging information
	logger.Infof("Stopping GoAkt cluster Node=(%s)....🤔", n.name)

	n.started.Store(false)

	// create a cancellation context
	ctx, cancelFn := context.WithTimeout(ctx, n.shutdownTimeout)
	defer cancelFn()

	// stop the events loop
	close(n.stopEventsListenerSig)

	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(n.mlist.Leave(n.shutdownTimeout)).
		AddError(n.provider.Deregister()).
		AddError(n.provider.Close()).
		AddError(n.mlist.Shutdown()).
		Error(); err != nil {
		logger.Error(fmt.Errorf("failed to shutdown the cluster engine=(%s): %w", n.name, err))
		return err
	}

	return nil
}

// Host returns the Node Host
func (n *Node) Host() string {
	n.lock.Lock()
	host := n.node.Host
	n.lock.Unlock()
	return host
}

// RemotingPort returns the Node remoting port
func (n *Node) RemotingPort() int {
	n.lock.Lock()
	port := n.node.RemotingPort
	n.lock.Unlock()
	return port
}

// PutActor pushes to the cluster the peer sync request
func (n *Node) PutActor(_ context.Context, actor *internalpb.ActorRef) error {
	logger := n.logger
	logger.Infof("synchronization peer (%s)", n.node.PeersAddress())
	n.actorsGroupLock.Lock()
	n.delegate.PutActor(actor)
	n.actorsGroupLock.Unlock()
	logger.Infof("peer (%s) successfully synchronized in the cluster", n.node.PeersAddress())
	return nil
}

// GetActor fetches an actor from the Node
func (n *Node) GetActor(_ context.Context, actorName string) (*internalpb.ActorRef, error) {
	logger := n.logger
	n.actorsGroupLock.Lock()
	logger.Infof("[%s] retrieving actor (%s) from the cluster", n.node.PeersAddress(), actorName)
	actor, err := n.delegate.GetActor(actorName)
	if err != nil {
		logger.Error(fmt.Errorf("failed to get actor=(%s) from the cluster: %w", actorName, err))
		n.actorsGroupLock.Unlock()
		return nil, err
	}
	n.actorsGroupLock.Unlock()
	return actor, nil
}

// GetPartition returns the partition where a given actor is stored
func (n *Node) GetPartition(string) int {
	//TODO implement me
	return 0
}

// SetJobKey sets a given key to the cluster
func (n *Node) SetJobKey(_ context.Context, key string) error {
	logger := n.logger
	logger.Infof("replicating Job key (%s)", key)
	n.jobsGroupLock.Lock()
	n.delegate.PutJobKey(key)
	n.jobsGroupLock.Unlock()
	logger.Infof("Job key (%s) successfully replicated", key)
	return nil
}

// JobKeyExists checks the existence of a given key
func (n *Node) JobKeyExists(_ context.Context, key string) (bool, error) {
	logger := n.logger
	logger.Infof("checking Job key (%s) existence in the cluster", key)
	n.jobsGroupLock.Lock()
	exist := n.delegate.JobKeyExists(key)
	n.jobsGroupLock.Unlock()
	return exist, nil
}

// UnsetJobKey unsets the already set given key in the cluster
func (n *Node) UnsetJobKey(_ context.Context, key string) error {
	logger := n.logger
	logger.Infof("unsetting Job key (%s)", key)
	n.jobsGroupLock.Lock()
	n.delegate.DeleteJobKey(key)
	n.jobsGroupLock.Unlock()
	logger.Infof("Job key (%s) successfully unset", key)
	return nil
}

// RemoveActor removes a given actor from the cluster.
// An actor is removed from the cluster when this actor has been passivated.
func (n *Node) RemoveActor(_ context.Context, actorName string) error {
	logger := n.logger
	logger.Infof("removing actor (%s) from cluster", actorName)
	n.actorsGroupLock.Lock()
	n.delegate.DeleteActor(actorName)
	n.actorsGroupLock.Unlock()
	logger.Infof("actor (%s) successfully removed from the cluster", actorName)
	return nil
}

// GetState fetches a given peer state
func (n *Node) GetState(_ context.Context, peerAddress string) (*internalpb.PeerState, error) {
	logger := n.logger
	logger.Infof("[%s] retrieving peer (%s) sync record", n.node.PeersAddress(), peerAddress)
	n.peerStatesGroupLock.Lock()
	peerState, err := n.delegate.GetPeerState(peerAddress)
	if err != nil {
		logger.Errorf("[%s] failed to decode peer=(%s) sync record: %v", n.node.PeersAddress(), peerAddress, err)
		n.peerStatesGroupLock.Unlock()
		return nil, err
	}

	n.peerStatesGroupLock.Unlock()
	logger.Infof("[%s] successfully retrieved peer (%s) sync record .🎉", n.node.PeersAddress(), peerAddress)
	return peerState, nil
}

// Events returns a channel where cluster events are published
func (n *Node) Events() <-chan *Event {
	return n.events
}

// AdvertisedAddress returns the cluster node cluster address that is known by the
// peers in the cluster
func (n *Node) AdvertisedAddress() string {
	n.lock.Lock()
	address := n.node.PeersAddress()
	n.lock.Unlock()
	return address
}

// Peers returns a channel containing the list of peers at a given time
func (n *Node) Peers(context.Context) ([]*Peer, error) {
	n.lock.Lock()
	mnodes := n.mlist.Members()
	n.lock.Unlock()
	nodes := make([]*discovery.Node, 0, len(mnodes))
	for _, mnode := range mnodes {
		node := new(discovery.Node)
		if err := json.Unmarshal(mnode.Meta, &node); err != nil {
			return nil, err
		}

		if node != nil && node.DiscoveryAddress() != n.node.DiscoveryAddress() {
			nodes = append(nodes, node)
		}
	}

	// no peers found
	if len(nodes) == 0 {
		return nil, nil
	}

	sort.Slice(nodes, func(i int, j int) bool {
		return nodes[i].Birthdate < nodes[j].Birthdate
	})

	coordinator := nodes[0]
	peers := make([]*Peer, len(nodes))
	for i := 0; i < len(nodes); i++ {
		member := nodes[i]
		peer := &Peer{
			Host:        member.Host,
			Port:        member.PeersPort,
			Coordinator: member.DiscoveryAddress() == coordinator.DiscoveryAddress(),
		}
		peers[i] = peer
	}
	return peers, nil
}

// IsLeader states whether the given cluster node is a leader or not at a given
// point in time in the cluster
func (n *Node) IsLeader(context.Context) bool {
	n.lock.Lock()
	mnodes := n.mlist.Members()
	n.lock.Unlock()

	logger := n.logger

	nodes := make([]*discovery.Node, 0, len(mnodes))
	for _, mnode := range mnodes {
		node := new(discovery.Node)
		if err := json.Unmarshal(mnode.Meta, &node); err != nil {
			logger.Error(fmt.Errorf("failed to fetch cluster members: %w", err))
			return false
		}
		nodes = append(nodes, node)
	}

	sort.Slice(nodes, func(i int, j int) bool {
		return nodes[i].Birthdate < nodes[j].Birthdate
	})

	coordinator := nodes[0]
	return coordinator.PeersAddress() == n.node.PeersAddress()
}

// eventsListener listens to cluster events
func (n *Node) eventsListener(eventsChan chan memberlist.NodeEvent) {
	for {
		select {
		case <-n.stopEventsListenerSig:
			// finish listening to cluster events
			close(n.events)
			return
		case event := <-eventsChan:
			if event.Node == nil {
				continue
			}

			var node *discovery.Node
			if err := json.Unmarshal(event.Node.Meta, &node); err != nil {
				logger.Error(fmt.Errorf("failed to unpack GoAkt cluster node:(%s) meta: %w", event.Node.Address(), err))
				continue
			}

			if node.DiscoveryAddress() == n.node.DiscoveryAddress() {
				continue
			}

			var (
				xevent proto.Message
				xtype  EventType
			)
			switch event.Event {
			case memberlist.NodeJoin:
				xevent = &goaktpb.NodeJoined{
					Address:   node.PeersAddress(),
					Timestamp: timestamppb.New(time.Now().UTC()),
				}
				xtype = NodeJoined
			case memberlist.NodeLeave:
				xevent = &goaktpb.NodeLeft{
					Address:   node.PeersAddress(),
					Timestamp: timestamppb.New(time.Now().UTC()),
				}
				xtype = NodeLeft
			case memberlist.NodeUpdate:
				// TODO: need to handle that later
				continue
			}

			payload, _ := anypb.New(xevent)
			n.events <- &Event{payload, xtype}
		}
	}
}

// joinCluster attempts to join an existing cluster if peers are provided
func (n *Node) joinCluster(ctx context.Context) error {
	logger := n.logger
	var err error
	n.mlist, err = memberlist.Create(n.mconfig)
	if err != nil {
		logger.Error(fmt.Errorf("failed to create the GoAkt cluster members list: %w", err))
		return err
	}

	discoveryCtx, discoveryCancel := context.WithTimeout(ctx, n.maxJoinTimeout)
	defer discoveryCancel()

	var peers []string
	retrier := retry.NewRetrier(n.maxJoinAttempts, n.maxJoinRetryInterval, n.maxJoinRetryInterval)
	if err := retrier.RunContext(discoveryCtx, func(ctx context.Context) error { // nolint
		peers, err = n.provider.DiscoverPeers()
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		logger.Error(fmt.Errorf("failed to discover GoAkt cluster nodes: %w", err))
		return err
	}

	if len(peers) > 0 {
		// check whether the cluster quorum is met to operate
		if n.minimumPeersQuorum > uint(len(peers)) {
			return ErrClusterQuorum
		}

		// attempt to join
		joinCtx, joinCancel := context.WithTimeout(ctx, n.maxJoinTimeout)
		defer joinCancel()
		joinRetrier := retry.NewRetrier(n.maxJoinAttempts, n.maxJoinRetryInterval, n.maxJoinRetryInterval)
		if err := joinRetrier.RunContext(joinCtx, func(ctx context.Context) error { // nolint
			if _, err := n.mlist.Join(peers); err != nil {
				return err
			}
			return nil
		}); err != nil {
			logger.Error(fmt.Errorf("failed to join existing GoAkt cluster: :%w", err))
			return err
		}
	}

	return nil
}
