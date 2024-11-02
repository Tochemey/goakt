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
	"sync/atomic"
	"time"

	goset "github.com/deckarep/golang-set/v2"
	"github.com/flowchartsman/retry"
	groupcache "github.com/groupcache/groupcache-go/v3"
	"github.com/groupcache/groupcache-go/v3/transport"
	"github.com/groupcache/groupcache-go/v3/transport/peer"
	"github.com/hashicorp/memberlist"
	"github.com/reugn/go-quartz/logger"
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

type Group struct {
	mconfig *memberlist.Config
	mlist   *memberlist.Memberlist
	started *atomic.Bool
	// specifies the minimum number of cluster members
	// the default values is 1
	minimumPeersQuorum uint
	replicaCount       uint
	maxJoinAttempts    int

	// specifies the Group name
	name string

	// specifies the logger
	logger log.Logger

	// specifies the Group node
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
var _ Interface = (*Group)(nil)

// NewGroup creates an instance of Group
func NewGroup(name string, disco discovery.Provider, host *discovery.Node, opts ...Option) *Group {
	// create an instance of Group
	group := &Group{
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
	}
	// apply the various options
	//for _, opt := range opts {
	//	opt.Apply(group)
	//}

	// set the host startNode
	host.Birthdate = time.Now().UnixNano()
	group.node = host

	return group
}

// Start starts the Engine.
func (g *Group) Start(ctx context.Context) error {
	logger := g.logger

	logger.Infof("Starting GoAkt cluster Engine service on host=(%s)....🤔", g.node.PeersAddress())

	// create the memberlist configuration
	g.mconfig = memberlist.DefaultLANConfig()
	g.mconfig.BindAddr = g.node.Host
	g.mconfig.BindPort = g.node.DiscoveryPort
	g.mconfig.AdvertisePort = g.node.DiscoveryPort
	g.mconfig.LogOutput = newLogWriter(g.logger)
	g.mconfig.Name = g.node.DiscoveryAddress()

	// get the delegate
	delegate, err := g.newGroupDelegate()
	if err != nil {
		logger.Error(fmt.Errorf("failed to create the discovery engine delegate: %w", err))
		return err
	}

	// set the delegate
	g.mconfig.Delegate = delegate

	// start process
	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(g.provider.Initialize()).
		AddError(g.provider.Register()).
		AddError(g.joinCluster(ctx)).
		Error(); err != nil {
		return err
	}

	// create enough buffer to house the cluster events
	// TODO: revisit this number
	eventsCh := make(chan memberlist.NodeEvent, 256)
	g.mconfig.Events = &memberlist.ChannelEventDelegate{
		Ch: eventsCh,
	}

	// start the group cache engine
	g.daemon, err = groupcache.ListenAndServe(ctx, g.node.PeersAddress(), groupcache.Options{
		HashFn: func(data []byte) uint64 {
			return g.hasher.HashCode(data)
		},
		Replicas:  int(g.replicaCount),
		Logger:    newGroupLogger(g.logger),
		Transport: transport.NewHttpTransport(transport.HttpTransportOptions{}),
	})
	if err != nil {
		logger.Error(fmt.Errorf("failed to start the GoAkt cluster Engine=(%s) :%w", g.name, err))
		return err
	}

	// start the various groups
	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(g.setupActorsGroup()).
		AddError(g.setupJobsGroup()).
		AddError(g.setupPeersStageGroup()).
		Error(); err != nil {
		logger.Error(fmt.Errorf("failed to create GoAkt cluster actors groups: %w", err))
		return err
	}

	g.started.Store(true)
	// start listening to events
	go g.eventsListener(eventsCh)

	logger.Infof("GoAkt cluster Engine=(%s) successfully started.", g.name)
	return nil
}

// Stop stops the Engine gracefully
func (g *Group) Stop(ctx context.Context) error {
	if !g.started.Load() {
		return nil
	}

	// set the logger
	logger := g.logger

	// add some logging information
	logger.Infof("Stopping GoAkt cluster Node=(%s)....🤔", g.name)

	g.started.Store(false)

	// create a cancellation context
	ctx, cancelFn := context.WithTimeout(ctx, g.shutdownTimeout)
	defer cancelFn()

	// stop the events loop
	close(g.stopEventsListenerSig)

	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(g.mlist.Leave(g.shutdownTimeout)).
		AddError(g.provider.Deregister()).
		AddError(g.provider.Close()).
		AddError(g.mlist.Shutdown()).
		AddError(g.daemon.Shutdown(ctx)).
		Error(); err != nil {
		logger.Error(fmt.Errorf("failed to shutdown the cluster engine=(%s): %w", g.name, err))
		return err
	}

	return nil
}

// Host returns the Node Host
func (g *Group) Host() string {
	g.lock.Lock()
	host := g.node.Host
	g.lock.Unlock()
	return host
}

// RemotingPort returns the Node remoting port
func (g *Group) RemotingPort() int {
	g.lock.Lock()
	port := g.node.RemotingPort
	g.lock.Unlock()
	return port
}

// PutActor pushes to the cluster the peer sync request
func (g *Group) PutActor(ctx context.Context, actor *internalpb.ActorRef) error {
	ctx, cancelFn := context.WithTimeout(ctx, g.writeTimeout)
	defer cancelFn()

	logger := g.logger

	g.lock.Lock()
	defer g.lock.Unlock()

	logger.Infof("synchronization peer (%s)", g.node.PeersAddress())

	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(2)

	eg.Go(func() error {
		encoded, _ := encode(actor)
		if err := g.actorsGroup.Set(ctx, actor.GetActorAddress().GetName(), encoded, NoExpiry, true); err != nil {
			return fmt.Errorf("failed to sync actor=(%s) of peer=(%s): %w", actor.GetActorAddress().GetName(), g.node.PeersAddress(), err)
		}
		return nil
	})

	eg.Go(func() error {
		actors := append(g.peerState.GetActors(), actor)

		compacted := slices.CompactFunc(actors, func(actor *internalpb.ActorRef, actor2 *internalpb.ActorRef) bool {
			return proto.Equal(actor, actor2)
		})

		g.peerState.Actors = compacted
		encoded, _ := proto.Marshal(g.peerState)
		if err := g.peerStatesGroup.Set(ctx, g.node.PeersAddress(), encoded, NoExpiry, true); err != nil {
			return fmt.Errorf("failed to sync peer=(%s) request: %w", g.node.PeersAddress(), err)
		}

		return nil
	})

	if err := eg.Wait(); err != nil {
		logger.Errorf("failed to synchronize peer=(%s): %w", g.node.PeersAddress(), err)
		return err
	}

	logger.Infof("peer (%s) successfully synchronized in the cluster", g.node.PeersAddress())
	return nil
}

// GetActor fetches an actor from the Node
func (g *Group) GetActor(ctx context.Context, actorName string) (*internalpb.ActorRef, error) {
	ctx, cancelFn := context.WithTimeout(ctx, g.readTimeout)
	defer cancelFn()

	logger := g.logger

	g.lock.Lock()
	logger.Infof("[%s] retrieving actor (%s) from the cluster", g.node.PeersAddress(), actorName)
	actor := new(internalpb.ActorRef)
	if err := g.actorsGroup.Get(ctx, actorName, transport.ProtoSink(actor)); err != nil {
		logger.Error(fmt.Errorf("failed to get actor=(%s) from the cluster: %w", actorName, err))
		g.lock.Unlock()
		return nil, err
	}

	if proto.Equal(actor, new(internalpb.ActorRef)) {
		g.lock.Unlock()
		return nil, ErrActorNotFound
	}

	g.lock.Unlock()
	return actor, nil
}

func (g *Group) GetPartition(actorName string) int {
	//TODO implement me
	return 0
}

// SetJobKey sets a given key to the cluster
func (g *Group) SetJobKey(ctx context.Context, key string) error {
	ctx, cancelFn := context.WithTimeout(ctx, g.writeTimeout)
	defer cancelFn()

	logger := g.logger

	g.jobsGroupLock.Lock()
	g.jobsCache.Store(key, key)
	g.jobsGroupLock.Unlock()

	g.lock.Lock()
	logger.Infof("replicating key (%s)", key)
	if err := g.jobsGroup.Set(ctx, key, []byte(key), NoExpiry, true); err != nil {
		logger.Error(fmt.Errorf("failed to sync job=(%s) key: %w", key, err))
		g.lock.Lock()
		return err
	}

	g.lock.Unlock()
	logger.Infof("Job key (%s) successfully replicated", key)
	return nil
}

// JobKeyExists checks the existence of a given key
func (g *Group) JobKeyExists(ctx context.Context, key string) (bool, error) {
	ctx, cancelFn := context.WithTimeout(ctx, g.readTimeout)
	defer cancelFn()

	logger := g.logger

	g.lock.Lock()
	logger.Infof("checking Job key (%s) existence in the cluster", key)
	var actual string
	if err := g.jobsGroup.Get(ctx, key, transport.StringSink(&actual)); err != nil {
		logger.Error(fmt.Errorf("failed to get job=(%s) key: %w", key, err))
		g.lock.Unlock()
		return false, err
	}

	g.lock.Unlock()
	return actual != "", nil
}

// UnsetJobKey unsets the already set given key in the cluster
func (g *Group) UnsetJobKey(ctx context.Context, key string) error {
	logger := g.logger

	g.lock.Lock()
	logger.Infof("unsetting Job key (%s)", key)
	if err := g.jobsGroup.Remove(ctx, key); err != nil {
		logger.Error(fmt.Errorf("failed to remove job=(%s) key: %w", key, err))
		g.lock.Unlock()
		return err
	}

	g.lock.Unlock()
	logger.Infof("Job key (%s) successfully unset", key)
	return nil
}

// RemoveActor removes a given actor from the cluster.
// An actor is removed from the cluster when this actor has been passivated.
func (g *Group) RemoveActor(ctx context.Context, actorName string) error {
	logger := g.logger

	g.lock.Lock()
	logger.Infof("removing actor (%s) from cluster", actorName)
	if err := g.actorsGroup.Remove(ctx, actorName); err != nil {
		logger.Error(fmt.Errorf("failed to remove actor=(%s) from the cluster: %w", actorName, err))
		g.lock.Unlock()
		return err
	}
	g.lock.Unlock()
	logger.Infof("actor (%s) successfully removed from the cluster", actorName)
	return nil
}

// Events returns a channel where cluster events are published
func (g *Group) Events() <-chan *Event {
	return g.events
}

// AdvertisedAddress returns the cluster node cluster address that is known by the
// peers in the cluster
func (g *Group) AdvertisedAddress() string {
	g.lock.Lock()
	address := g.node.PeersAddress()
	g.lock.Unlock()
	return address
}

// Peers returns a channel containing the list of peers at a given time
func (g *Group) Peers(ctx context.Context) ([]*Peer, error) {
	g.lock.Lock()
	mnodes := g.mlist.Members()
	g.lock.Unlock()
	nodes := make([]*discovery.Node, 0, len(mnodes))
	for _, mnode := range mnodes {
		node := new(discovery.Node)
		if err := json.Unmarshal(mnode.Meta, &node); err != nil {
			return nil, err
		}

		if node != nil && node.DiscoveryAddress() != g.node.DiscoveryAddress() {
			nodes = append(nodes, node)
		}
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
func (g *Group) GetState(ctx context.Context, peerAddress string) (*internalpb.PeerState, error) {
	ctx, cancelFn := context.WithTimeout(ctx, g.readTimeout)
	defer cancelFn()

	logger := g.logger

	g.lock.Lock()
	defer g.lock.Unlock()

	logger.Infof("[%s] retrieving peer (%s) sync record", g.node.PeersAddress(), peerAddress)
	peerState := new(internalpb.PeerState)
	if err := g.peerStatesGroup.Get(ctx, peerAddress, transport.ProtoSink(peerState)); err != nil {
		logger.Errorf("[%s] failed to decode peer=(%s) sync record: %v", g.node.PeersAddress(), peerAddress, err)
		return nil, err
	}

	if proto.Equal(peerState, new(internalpb.PeerState)) {
		return nil, ErrPeerSyncNotFound
	}

	logger.Infof("[%s] successfully retrieved peer (%s) sync record .🎉", g.node.PeersAddress(), peerAddress)
	return peerState, nil
}

// IsLeader states whether the given cluster node is a leader or not at a given
// point in time in the cluster
func (g *Group) IsLeader(ctx context.Context) bool {
	g.lock.Lock()
	mnodes := g.mlist.Members()
	g.lock.Unlock()

	logger := g.logger

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
	return coordinator.PeersAddress() == g.node.PeersAddress()
}

// eventsListener listens to cluster events
func (g *Group) eventsListener(eventsChan chan memberlist.NodeEvent) {
	for {
		select {
		case <-g.stopEventsListenerSig:
			// finish listening to cluster events
			close(g.events)
			return
		case event := <-eventsChan:
			var node *discovery.Node
			if err := json.Unmarshal(event.Node.Meta, &node); err != nil {
				logger.Error(fmt.Errorf("failed to unpack GoAkt cluster node:(%s) meta: %w", event.Node.Address(), err))
				continue
			}

			if node.DiscoveryAddress() == g.node.DiscoveryAddress() {
				continue
			}

			var xevent proto.Message
			var xtype EventType
			ctx := context.Background()
			// we need to add the new peers
			currentPeers, _ := g.Peers(ctx)
			peersSet := goset.NewSet[peer.Info]()
			peersSet.Add(peer.Info{
				Address: g.node.PeersAddress(),
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
				g.eventsLock.Lock()
				peersSet.Add(peer.Info{
					Address: node.PeersAddress(),
					IsSelf:  false,
				})
				_ = g.daemon.SetPeers(ctx, peersSet.ToSlice())
				g.eventsLock.Unlock()

			case memberlist.NodeLeave:
				xevent = &goaktpb.NodeLeft{
					Address:   node.PeersAddress(),
					Timestamp: timestamppb.New(time.Now().UTC()),
				}
				xtype = NodeLeft

				// remove the left node from the peers list
				// and set it to the daemon
				g.eventsLock.Lock()
				peersSet.Remove(peer.Info{
					Address: node.PeersAddress(),
					IsSelf:  false,
				})
				_ = g.daemon.SetPeers(ctx, peersSet.ToSlice())
				g.eventsLock.Unlock()

			case memberlist.NodeUpdate:
				// TODO: need to handle that later
				continue
			}

			payload, _ := anypb.New(xevent)
			g.events <- &Event{payload, xtype}
		}
	}
}

// joinCluster attempts to join an existing cluster if peers are provided
func (g *Group) joinCluster(ctx context.Context) error {
	logger := g.logger
	var err error
	g.mlist, err = memberlist.Create(g.mconfig)
	if err != nil {
		logger.Error(fmt.Errorf("failed to create the GoAkt cluster members list: %w", err))
		return err
	}

	discoveryCtx, discoveryCancel := context.WithTimeout(ctx, g.maxJoinTimeout)
	defer discoveryCancel()

	var peers []string
	retrier := retry.NewRetrier(g.maxJoinAttempts, g.maxJoinRetryInterval, g.maxJoinRetryInterval)
	if err := retrier.RunContext(discoveryCtx, func(ctx context.Context) error {
		peers, err = g.provider.DiscoverPeers()
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
		if g.minimumPeersQuorum < uint(len(peers)) {
			return ErrClusterQuorum
		}

		// attempt to join
		joinCtx, joinCancel := context.WithTimeout(ctx, g.maxJoinTimeout)
		defer joinCancel()
		joinRetrier := retry.NewRetrier(g.maxJoinAttempts, g.maxJoinRetryInterval, g.maxJoinRetryInterval)
		if err := joinRetrier.RunContext(joinCtx, func(ctx context.Context) error {
			if _, err := g.mlist.Join(peers); err != nil {
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
func (g *Group) setupActorsGroup() error {
	var err error
	// create a new group cache with a max cache size of 3MB
	// todo: revisit the cache size
	g.actorsGroup, err = g.daemon.NewGroup(ActorsGroup, 3000000, groupcache.GetterFunc(func(ctx context.Context, key string, dest transport.Sink) error {
		g.actorsGroupLock.Lock()
		defer g.actorsGroupLock.Unlock()

		var actor *internalpb.ActorRef
		for _, ref := range g.peerState.Actors {
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
func (g *Group) setupJobsGroup() error {
	var err error
	g.jobsGroup, err = g.daemon.NewGroup(JobsGroups, 3000000, groupcache.GetterFunc(func(ctx context.Context, key string, dest transport.Sink) error {
		g.jobsGroupLock.Lock()
		defer g.jobsGroupLock.Unlock()

		if value, ok := g.jobsCache.Load(key); ok {
			return dest.SetString(value.(string), NoExpiry)
		}

		return ErrKeyNotFound
	}))
	return err
}

// setupPeersStageGroup sets the peers stage group
func (g *Group) setupPeersStageGroup() error {
	var err error
	g.peerStatesGroup, err = g.daemon.NewGroup(PeerStatesGroup, 3000000, groupcache.GetterFunc(func(ctx context.Context, key string, dest transport.Sink) error {
		g.peerStatesGroupLock.Lock()
		defer g.peerStatesGroupLock.Unlock()

		if key != g.node.PeersAddress() {
			return ErrPeerSyncNotFound
		}
		return dest.SetProto(g.peerState, NoExpiry)
	}))
	return err
}
