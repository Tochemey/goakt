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
	"net"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	goset "github.com/deckarep/golang-set/v2"
	"github.com/flowchartsman/retry"
	groupcache "github.com/groupcache/groupcache-go/v3"
	"github.com/groupcache/groupcache-go/v3/transport"
	"github.com/groupcache/groupcache-go/v3/transport/peer"
	"github.com/hashicorp/memberlist"
	"github.com/reugn/go-quartz/logger"
	"go.uber.org/atomic"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/hash"
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
	mconfig *memberlist.Config
	mlist   *memberlist.Memberlist
	started *atomic.Bool
	// specifies the minimum number of cluster members
	// the default values is 1
	minimumPeersQuorum uint
	replicaCount       uint
	maxJoinAttempts    int

	// specifies the Node name
	name string

	// specifies the logger
	logger log.Logger

	// specifies the underlying node
	node *discovery.Node

	// specifies the hasher
	hasher hash.Hasher

	// specifies the discovery provider
	provider discovery.Provider

	writeTimeout         time.Duration
	readTimeout          time.Duration
	shutdownTimeout      time.Duration
	maxJoinTimeout       time.Duration
	maxJoinRetryInterval time.Duration

	events                chan *Event
	eventsLock            *sync.Mutex
	lock                  *sync.Mutex
	stopEventsListenerSig chan types.Unit

	actorsGroup         groupcache.Group
	actorsGroupLock     *sync.RWMutex
	peerStatesGroup     groupcache.Group
	peerStatesGroupLock *sync.RWMutex
	jobsGroup           groupcache.Group
	jobsGroupLock       *sync.RWMutex

	daemon *groupcache.Daemon

	// specifies the node state
	peerState *internalpb.PeerState

	// specifies the keys state
	jobsCache *sync.Map
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
		writeTimeout:          time.Second,
		readTimeout:           time.Second,
		shutdownTimeout:       3 * time.Second,
		hasher:                hash.DefaultHasher(),
		events:                make(chan *Event, 20),
		minimumPeersQuorum:    1,
		replicaCount:          1,
		maxJoinTimeout:        time.Second,
		maxJoinRetryInterval:  time.Second,
		maxJoinAttempts:       10,
		lock:                  new(sync.Mutex),
		eventsLock:            new(sync.Mutex),
		actorsGroupLock:       new(sync.RWMutex),
		jobsGroupLock:         new(sync.RWMutex),
		peerStatesGroupLock:   new(sync.RWMutex),
		stopEventsListenerSig: make(chan types.Unit, 1),
		jobsCache:             new(sync.Map),
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

	// get the nodeDelegate
	delegate, err := n.newNodeDelegate()
	if err != nil {
		logger.Error(fmt.Errorf("failed to create the discovery engine nodeDelegate: %w", err))
		return err
	}

	// set the nodeDelegate
	n.mconfig.Delegate = delegate

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

	// start the group cache engine
	n.daemon, err = groupcache.ListenAndServe(ctx, n.node.PeersAddress(), groupcache.Options{
		HashFn: func(data []byte) uint64 {
			return n.hasher.HashCode(data)
		},
		Replicas:  int(n.replicaCount),
		Logger:    newCacheLogger(n.logger),
		Transport: transport.NewHttpTransport(transport.HttpTransportOptions{}),
	})
	if err != nil {
		logger.Error(fmt.Errorf("failed to start the GoAkt cluster Engine=(%s) :%w", n.name, err))
		return err
	}

	// start the various groups
	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(n.setupActorsGroup()).
		AddError(n.setupJobsGroup()).
		AddError(n.setupPeersStageGroup()).
		Error(); err != nil {
		logger.Error(fmt.Errorf("failed to create GoAkt cluster actors groups: %w", err))
		return err
	}

	// set the peer state
	n.peerState = &internalpb.PeerState{
		Host:         n.Host(),
		RemotingPort: int32(n.node.RemotingPort),
		PeersPort:    int32(n.node.PeersPort),
		Actors:       []*internalpb.ActorRef{},
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
		AddError(n.daemon.Shutdown(ctx)).
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
func (n *Node) PutActor(ctx context.Context, actor *internalpb.ActorRef) error {
	ctx, cancelFn := context.WithTimeout(ctx, n.writeTimeout)
	defer cancelFn()

	logger := n.logger

	n.lock.Lock()
	defer n.lock.Unlock()

	logger.Infof("synchronization peer (%s)", n.node.PeersAddress())

	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(2)

	eg.Go(func() error {
		encoded, _ := encode(actor)
		if err := n.actorsGroup.Set(ctx, actor.GetActorAddress().GetName(), encoded, NoExpiry, true); err != nil {
			return fmt.Errorf("failed to sync actor=(%s) of peer=(%s): %w", actor.GetActorAddress().GetName(), n.node.PeersAddress(), err)
		}
		return nil
	})

	eg.Go(func() error {
		actors := append(n.peerState.GetActors(), actor)

		compacted := slices.CompactFunc(actors, func(actor *internalpb.ActorRef, actor2 *internalpb.ActorRef) bool {
			return proto.Equal(actor, actor2)
		})

		n.peerState.Actors = compacted
		encoded, _ := proto.Marshal(n.peerState)
		if err := n.peerStatesGroup.Set(ctx, n.node.PeersAddress(), encoded, NoExpiry, true); err != nil {
			return fmt.Errorf("failed to sync peer=(%s) request: %w", n.node.PeersAddress(), err)
		}

		return nil
	})

	if err := eg.Wait(); err != nil {
		logger.Errorf("failed to synchronize peer=(%s): %w", n.node.PeersAddress(), err)
		return err
	}

	logger.Infof("peer (%s) successfully synchronized in the cluster", n.node.PeersAddress())
	return nil
}

// GetActor fetches an actor from the Node
func (n *Node) GetActor(ctx context.Context, actorName string) (*internalpb.ActorRef, error) {
	ctx, cancelFn := context.WithTimeout(ctx, n.readTimeout)
	defer cancelFn()

	logger := n.logger

	n.lock.Lock()
	logger.Infof("[%s] retrieving actor (%s) from the cluster", n.node.PeersAddress(), actorName)
	actor := new(internalpb.ActorRef)
	if err := n.actorsGroup.Get(ctx, actorName, transport.ProtoSink(actor)); err != nil {
		logger.Error(fmt.Errorf("failed to get actor=(%s) from the cluster: %w", actorName, err))
		n.lock.Unlock()
		return nil, err
	}

	if proto.Equal(actor, new(internalpb.ActorRef)) {
		n.lock.Unlock()
		return nil, ErrActorNotFound
	}

	n.lock.Unlock()
	return actor, nil
}

// GetPartition returns the partition where a given actor is stored
// nolint
func (n *Node) GetPartition(actorName string) int {
	//TODO implement me
	return 0
}

// SetJobKey sets a given key to the cluster
func (n *Node) SetJobKey(ctx context.Context, key string) error {
	ctx, cancelFn := context.WithTimeout(ctx, n.writeTimeout)
	defer cancelFn()

	logger := n.logger

	n.jobsGroupLock.Lock()
	n.jobsCache.Store(key, key)
	n.jobsGroupLock.Unlock()

	n.lock.Lock()
	logger.Infof("replicating key (%s)", key)
	if err := n.jobsGroup.Set(ctx, key, []byte(key), NoExpiry, true); err != nil {
		logger.Error(fmt.Errorf("failed to sync job=(%s) key: %w", key, err))
		n.lock.Lock()
		return err
	}

	n.lock.Unlock()
	logger.Infof("Job key (%s) successfully replicated", key)
	return nil
}

// JobKeyExists checks the existence of a given key
func (n *Node) JobKeyExists(ctx context.Context, key string) (bool, error) {
	ctx, cancelFn := context.WithTimeout(ctx, n.readTimeout)
	defer cancelFn()

	logger := n.logger

	n.lock.Lock()
	logger.Infof("checking Job key (%s) existence in the cluster", key)
	var actual string
	if err := n.jobsGroup.Get(ctx, key, transport.StringSink(&actual)); err != nil {
		logger.Error(fmt.Errorf("failed to get job=(%s) key: %w", key, err))
		n.lock.Unlock()
		return false, err
	}

	n.lock.Unlock()
	return actual != "", nil
}

// UnsetJobKey unsets the already set given key in the cluster
func (n *Node) UnsetJobKey(ctx context.Context, key string) error {
	logger := n.logger

	n.lock.Lock()
	logger.Infof("unsetting Job key (%s)", key)
	if err := n.jobsGroup.Remove(ctx, key); err != nil {
		logger.Error(fmt.Errorf("failed to remove job=(%s) key: %w", key, err))
		n.lock.Unlock()
		return err
	}

	n.lock.Unlock()
	logger.Infof("Job key (%s) successfully unset", key)
	return nil
}

// RemoveActor removes a given actor from the cluster.
// An actor is removed from the cluster when this actor has been passivated.
func (n *Node) RemoveActor(ctx context.Context, actorName string) error {
	logger := n.logger

	n.lock.Lock()
	logger.Infof("removing actor (%s) from cluster", actorName)
	if err := n.actorsGroup.Remove(ctx, actorName); err != nil {
		logger.Error(fmt.Errorf("failed to remove actor=(%s) from the cluster: %w", actorName, err))
		n.lock.Unlock()
		return err
	}
	n.lock.Unlock()
	logger.Infof("actor (%s) successfully removed from the cluster", actorName)
	return nil
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
// nolint
func (n *Node) Peers(ctx context.Context) ([]*Peer, error) {
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

// GetState fetches a given peer state
func (n *Node) GetState(ctx context.Context, peerAddress string) (*internalpb.PeerState, error) {
	ctx, cancelFn := context.WithTimeout(ctx, n.readTimeout)
	defer cancelFn()

	logger := n.logger

	n.lock.Lock()
	defer n.lock.Unlock()

	logger.Infof("[%s] retrieving peer (%s) sync record", n.node.PeersAddress(), peerAddress)
	peerState := new(internalpb.PeerState)
	if err := n.peerStatesGroup.Get(ctx, peerAddress, transport.ProtoSink(peerState)); err != nil {
		logger.Errorf("[%s] failed to decode peer=(%s) sync record: %v", n.node.PeersAddress(), peerAddress, err)
		return nil, err
	}

	if proto.Equal(peerState, new(internalpb.PeerState)) {
		return nil, ErrPeerSyncNotFound
	}

	logger.Infof("[%s] successfully retrieved peer (%s) sync record .🎉", n.node.PeersAddress(), peerAddress)
	return peerState, nil
}

// IsLeader states whether the given cluster node is a leader or not at a given
// point in time in the cluster
// nolint
func (n *Node) IsLeader(ctx context.Context) bool {
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
			var node *discovery.Node
			if err := json.Unmarshal(event.Node.Meta, &node); err != nil {
				logger.Error(fmt.Errorf("failed to unpack GoAkt cluster node:(%s) meta: %w", event.Node.Address(), err))
				continue
			}

			if node.DiscoveryAddress() == n.node.DiscoveryAddress() {
				continue
			}

			var xevent proto.Message
			var xtype EventType
			ctx := context.Background()
			// we need to add the new peers
			currentPeers, _ := n.Peers(ctx)
			peersSet := goset.NewSet[peer.Info]()
			peersSet.Add(peer.Info{
				Address: n.node.PeersAddress(),
				IsSelf:  true,
			})

			for _, xpeer := range currentPeers {
				addr := net.JoinHostPort(xpeer.Host, strconv.Itoa(xpeer.Port))
				peersSet.Add(peer.Info{
					Address: addr,
					IsSelf:  false,
				})
			}

			switch event.Event {
			case memberlist.NodeJoin:
				xevent = &goaktpb.NodeJoined{
					Address:   node.PeersAddress(),
					Timestamp: timestamppb.New(time.Now().UTC()),
				}
				xtype = NodeJoined

				// add the joined node to the peers list
				// and set it to the daemon
				n.eventsLock.Lock()
				peersSet.Add(peer.Info{
					Address: node.PeersAddress(),
					IsSelf:  false,
				})
				_ = n.daemon.SetPeers(ctx, peersSet.ToSlice())
				n.eventsLock.Unlock()

			case memberlist.NodeLeave:
				xevent = &goaktpb.NodeLeft{
					Address:   node.PeersAddress(),
					Timestamp: timestamppb.New(time.Now().UTC()),
				}
				xtype = NodeLeft

				// remove the left node from the peers list
				// and set it to the daemon
				n.eventsLock.Lock()
				peersSet.Remove(peer.Info{
					Address: node.PeersAddress(),
					IsSelf:  false,
				})
				_ = n.daemon.SetPeers(ctx, peersSet.ToSlice())
				n.eventsLock.Unlock()

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

// setupActorsGroup sets the actors group
// nolint
func (n *Node) setupActorsGroup() error {
	var err error
	// create a new group cache with a max cache size of 3MB
	// todo: revisit the cache size
	n.actorsGroup, err = n.daemon.NewGroup(ActorsGroup, 3000000, groupcache.GetterFunc(func(ctx context.Context, key string, dest transport.Sink) error {
		n.actorsGroupLock.Lock()
		defer n.actorsGroupLock.Unlock()

		var actor *internalpb.ActorRef
		for _, ref := range n.peerState.Actors {
			if ref.GetActorAddress().GetName() == key {
				actor = ref
			}
		}

		if actor == nil || proto.Equal(actor, new(internalpb.ActorRef)) {
			return ErrActorNotFound
		}
		encoded, _ := encode(actor)
		return dest.SetBytes(encoded, NoExpiry)
	}))
	return err
}

// setupJobsGroup sets the jobs group
// nolint
func (n *Node) setupJobsGroup() error {
	var err error
	n.jobsGroup, err = n.daemon.NewGroup(JobsGroups, 3000000, groupcache.GetterFunc(func(ctx context.Context, key string, dest transport.Sink) error {
		n.jobsGroupLock.Lock()
		defer n.jobsGroupLock.Unlock()

		if value, ok := n.jobsCache.Load(key); ok {
			return dest.SetString(value.(string), NoExpiry)
		}

		return ErrKeyNotFound
	}))
	return err
}

// setupPeersStageGroup sets the peers stage group
// nolint
func (n *Node) setupPeersStageGroup() error {
	var err error
	n.peerStatesGroup, err = n.daemon.NewGroup(PeerStatesGroup, 3000000, groupcache.GetterFunc(func(ctx context.Context, key string, dest transport.Sink) error {
		n.peerStatesGroupLock.Lock()
		defer n.peerStatesGroupLock.Unlock()

		if key != n.node.PeersAddress() {
			return ErrPeerSyncNotFound
		}
		return dest.SetProto(n.peerState, NoExpiry)
	}))
	return err
}
