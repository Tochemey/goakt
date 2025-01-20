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

package cluster

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	goset "github.com/deckarep/golang-set/v2"
	"github.com/redis/go-redis/v9"
	"github.com/tochemey/olric"
	"github.com/tochemey/olric/config"
	"github.com/tochemey/olric/events"
	"github.com/tochemey/olric/hasher"
	"github.com/tochemey/olric/pkg/storage"
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
	"github.com/tochemey/goakt/v2/log"
)

const MB = 1 << 20 // 1 MB = 2^20 bytes

type EventType int

const (
	NodeJoined EventType = iota
	NodeLeft
	actorsMap  = "actors"
	statesMap  = "states"
	jobKeysMap = "jobKeys"
)

func (x EventType) String() string {
	switch x {
	case NodeJoined:
		return "NodeJoined"
	case NodeLeft:
		return "NodeLeft"
	default:
		return fmt.Sprintf("%d", int(x))
	}
}

// Event defines the cluster event
type Event struct {
	Payload *anypb.Any
	Type    EventType
}

// Interface defines the Node interface
type Interface interface {
	// Start starts the cluster engine
	Start(ctx context.Context) error
	// Stop stops the cluster engine
	Stop(ctx context.Context) error
	// PutActor replicates onto the Node the metadata of an actor
	PutActor(ctx context.Context, actor *internalpb.ActorRef) error
	// GetActor fetches an actor from the Node
	GetActor(ctx context.Context, actorName string) (*internalpb.ActorRef, error)
	// GetPartition returns the partition where a given actor is stored
	GetPartition(actorName string) int
	// SetSchedulerJobKey sets a given key to the cluster
	SetSchedulerJobKey(ctx context.Context, key string) error
	// SchedulerJobKeyExists checks the existence of a given key
	SchedulerJobKeyExists(ctx context.Context, key string) (bool, error)
	// UnsetSchedulerJobKey unsets the already set given key in the cluster
	UnsetSchedulerJobKey(ctx context.Context, key string) error
	// RemoveActor removes a given actor from the cluster.
	// An actor is removed from the cluster when this actor has been passivated.
	RemoveActor(ctx context.Context, actorName string) error
	// Events returns a channel where cluster events are published
	Events() <-chan *Event
	// Peers returns a channel containing the list of peers at a given time
	Peers(ctx context.Context) ([]*Peer, error)
	// GetState fetches a given peer state
	GetState(ctx context.Context, peerAddress string) (*internalpb.PeerState, error)
	// IsLeader states whether the given cluster node is a leader or not at a given
	// point in time in the cluster
	IsLeader(ctx context.Context) bool
	// Actors returns all actors in the cluster at any given time
	Actors(ctx context.Context, timeout time.Duration) ([]*internalpb.ActorRef, error)
	// IsRunning returns true when the cluster engine is running
	IsRunning() bool
}

// Engine represents the Engine
type Engine struct {
	*sync.Mutex
	// specifies the total number of partitions
	// the default values is 271
	partitionsCount uint64

	// specifies the minimum number of cluster members
	// the default values is 1
	minimumPeersQuorum uint32
	replicaCount       uint32
	writeQuorum        uint32
	readQuorum         uint32

	// specifies the logger
	logger log.Logger

	// specifies the Node name
	name string

	// specifies the Node server
	server *olric.Olric
	// specifies the Node client
	// this help set and fetch data from the Node
	client olric.Client

	actorsMap   olric.DMap
	statesMap   olric.DMap
	jobKeysMap  olric.DMap
	storageSize uint64

	// specifies the discovery node
	node *discovery.Node

	// specifies the hasher
	hasher hash.Hasher

	// specifies the discovery provider
	discoveryProvider discovery.Provider

	writeTimeout    time.Duration
	readTimeout     time.Duration
	shutdownTimeout time.Duration

	events     chan *Event
	eventsLock *sync.Mutex
	pubSub     *redis.PubSub
	messages   <-chan *redis.Message

	// specifies the node state
	peerState *internalpb.PeerState

	nodeJoinedEventsFilter goset.Set[string]
	nodeLeftEventsFilter   goset.Set[string]

	tlsClientConfig *tls.Config
	tlsServerConfig *tls.Config

	running *atomic.Bool
}

// enforce compilation error
var _ Interface = (*Engine)(nil)

// NewEngine creates an instance of cluster Engine
func NewEngine(name string, disco discovery.Provider, host *discovery.Node, opts ...Option) (*Engine, error) {
	// create an instance of the Node
	engine := &Engine{
		partitionsCount:        271,
		logger:                 log.New(log.ErrorLevel, os.Stderr),
		name:                   name,
		discoveryProvider:      disco,
		writeTimeout:           time.Second,
		readTimeout:            time.Second,
		shutdownTimeout:        3 * time.Minute,
		hasher:                 hash.DefaultHasher(),
		pubSub:                 nil,
		events:                 make(chan *Event, 256),
		eventsLock:             &sync.Mutex{},
		messages:               make(chan *redis.Message, 1),
		minimumPeersQuorum:     1,
		replicaCount:           1,
		writeQuorum:            1,
		readQuorum:             1,
		Mutex:                  new(sync.Mutex),
		nodeJoinedEventsFilter: goset.NewSet[string](),
		nodeLeftEventsFilter:   goset.NewSet[string](),
		storageSize:            10 * MB,
		running:                atomic.NewBool(false),
	}
	// apply the various options
	for _, opt := range opts {
		opt.Apply(engine)
	}

	// perform some quick validations
	if (engine.tlsServerConfig == nil) != (engine.tlsClientConfig == nil) {
		return nil, ErrInvalidTLSConfiguration
	}

	// set the node startNode
	engine.node = host

	return engine, nil
}

// Start starts the Engine.
func (x *Engine) Start(ctx context.Context) error {
	logger := x.logger

	logger.Infof("Starting GoAkt cluster Engine service on node=(%s)....🤔", x.node.PeersAddress())

	conf, err := x.buildConfig()
	if err != nil {
		logger.Errorf("failed to build the cluster Engine configuration.💥: %v", err)
		return err
	}

	conf.Hasher = &hasherWrapper{x.hasher}

	m, err := config.NewMemberlistConfig("lan")
	if err != nil {
		logger.Errorf("failed to configure the cluster Engine members list.💥: %v", err)
		return err
	}

	// sets the bindings
	m.BindAddr = x.node.Host
	m.BindPort = x.node.DiscoveryPort
	m.AdvertisePort = x.node.DiscoveryPort
	m.AdvertiseAddr = x.node.Host
	conf.MemberlistConfig = m

	// set the discovery provider
	discoveryWrapper := &discoveryProvider{
		provider: x.discoveryProvider,
		log:      x.logger.StdLogger(),
	}

	conf.ServiceDiscovery = map[string]any{
		"plugin": discoveryWrapper,
		"id":     x.discoveryProvider.ID(),
	}

	// let us start the Node
	startCtx, cancel := context.WithCancel(ctx)
	// cancel the context the server has started
	conf.Started = func() {
		defer cancel()
	}

	eng, err := olric.New(conf)
	if err != nil {
		logger.Error(fmt.Errorf("failed to start the cluster Engine on node=(%s): %w", x.name, err))
		return err
	}

	// set the server
	x.server = eng
	go func() {
		if err = x.server.Start(); err != nil {
			if e := x.server.Shutdown(ctx); e != nil {
				logger.Panic(e)
			}
			// the expectation is to exit the application
			logger.Error(fmt.Errorf("failed to start the cluster Engine on node=(%s): %w", x.name, err))
		}
	}()

	<-startCtx.Done()
	logger.Info("cluster engine successfully started. 🎉")

	// set the client
	x.client = x.server.NewEmbeddedClient()

	// create the various maps
	x.actorsMap, err = x.client.NewDMap(actorsMap)
	if err != nil {
		logger.Error(fmt.Errorf("failed to start the cluster Engine on node=(%s): %w", x.name, err))
		return x.server.Shutdown(ctx)
	}

	x.statesMap, err = x.client.NewDMap(statesMap)
	if err != nil {
		logger.Error(fmt.Errorf("failed to start the cluster Engine on node=(%s): %w", x.name, err))
		return x.server.Shutdown(ctx)
	}

	x.jobKeysMap, err = x.client.NewDMap(jobKeysMap)
	if err != nil {
		logger.Error(fmt.Errorf("failed to start the cluster Engine on node=(%s): %w", x.name, err))
		return x.server.Shutdown(ctx)
	}

	// set the peer state
	x.peerState = &internalpb.PeerState{
		Host:         x.node.Host,
		RemotingPort: int32(x.node.RemotingPort),
		PeersPort:    int32(x.node.PeersPort),
		Actors:       []*internalpb.ActorRef{},
	}

	if err := x.initializeState(ctx); err != nil {
		logger.Error(fmt.Errorf("failed to start the cluster Engine on node=(%s): %w", x.name, err))
		return x.server.Shutdown(ctx)
	}

	// create a subscriber to consume to cluster events
	ps, err := x.client.NewPubSub(olric.ToAddress(x.node.PeersAddress()))
	if err != nil {
		logger.Error(fmt.Errorf("failed to start the cluster Engine on node=(%s): %w", x.name, err))
		return x.server.Shutdown(ctx)
	}

	x.pubSub = ps.Subscribe(ctx, events.ClusterEventsChannel)
	x.messages = x.pubSub.Channel()
	go x.consume()

	x.running.Store(true)
	logger.Infof("GoAkt cluster Engine=(%s) successfully started.", x.name)
	return nil
}

// Stop stops the Engine gracefully
func (x *Engine) Stop(ctx context.Context) error {
	// check whether it is running
	// if not running just return
	if !x.IsRunning() {
		return nil
	}

	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, x.shutdownTimeout)
	defer cancelFn()

	// set the logger
	logger := x.logger

	// add some logging information
	logger.Infof("Stopping GoAkt cluster Node=(%s)....🤔", x.name)

	// create an errors chain to pipeline the shutdown processes
	chain := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(x.pubSub.Close()).
		AddError(x.actorsMap.Destroy(ctx)).
		AddError(x.statesMap.Destroy(ctx)).
		AddError(x.jobKeysMap.Destroy(ctx)).
		AddError(x.client.Close(ctx)).
		AddError(x.server.Shutdown(ctx))

	// close the events listener
	if err := chain.Error(); err != nil {
		logger.Errorf("failed to stop the cluster Engine on node=(%s): %w", x.node.PeersAddress(), err)
		return err
	}

	// close the events queue
	x.eventsLock.Lock()
	close(x.events)
	x.eventsLock.Unlock()

	logger.Infof("GoAkt cluster Node=(%s) successfully stopped.", x.name)
	return nil
}

// IsRunning returns true when the cluster engine is running
func (x *Engine) IsRunning() bool {
	x.Lock()
	running := x.running.Load()
	x.Unlock()
	return running
}

// IsLeader states whether the given cluster node is a leader or not at a given
// point in time in the cluster
func (x *Engine) IsLeader(ctx context.Context) bool {
	// return false when not running
	if !x.IsRunning() {
		return false
	}

	x.Lock()
	defer x.Unlock()
	client := x.client
	host := x.node

	stats, err := client.Stats(ctx, host.PeersAddress())
	if err != nil {
		x.logger.Errorf("failed to fetch the cluster node=(%s) stats: %v", x.node.PeersAddress(), err)
		return false
	}
	return stats.ClusterCoordinator.String() == stats.Member.String()
}

// Actors returns all actors in the cluster at any given time
func (x *Engine) Actors(ctx context.Context, timeout time.Duration) ([]*internalpb.ActorRef, error) {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return nil, ErrEngineNotRunning
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	x.Lock()
	defer x.Unlock()

	logger := x.logger

	logger.Infof("fetching the actors list from the cluster...")
	scanner, err := x.actorsMap.Scan(ctx)
	if err != nil {
		logger.Errorf("failed to fetch the actors list from the cluster: %v", err)
		return nil, err
	}

	var actors []*internalpb.ActorRef
	for scanner.Next() {
		actorName := scanner.Key()
		resp, _ := x.actorsMap.Get(ctx, actorName)
		bytea, _ := resp.Byte()
		actor, err := decode(bytea)
		if err != nil {
			logger.Errorf("[%s] failed to decode actor=(%s) record: %v", x.node.PeersAddress(), actorName, err)
			return nil, err
		}
		actors = append(actors, actor)
	}
	scanner.Close()
	logger.Infof("actors list successfully fetched from the cluster.:)")
	return actors, nil
}

// PutActor pushes to the cluster the peer sync request
func (x *Engine) PutActor(ctx context.Context, actor *internalpb.ActorRef) error {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return ErrEngineNotRunning
	}

	ctx, cancelFn := context.WithTimeout(ctx, x.writeTimeout)
	defer cancelFn()

	x.Lock()
	defer x.Unlock()

	logger := x.logger

	logger.Infof("synchronization peer (%s)", x.node.PeersAddress())

	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(2)

	eg.Go(func() error {
		encoded, _ := encode(actor)
		if err := x.actorsMap.Put(ctx, actor.GetActorAddress().GetName(), encoded); err != nil {
			return fmt.Errorf("failed to sync actor=(%s) of peer=(%s): %v", actor.GetActorAddress().GetName(), x.node.PeersAddress(), err)
		}

		return nil
	})

	eg.Go(func() error {
		// append to the existing list
		actors := append(x.peerState.GetActors(), actor)
		// remove duplicates
		compacted := slices.CompactFunc(actors, func(actor *internalpb.ActorRef, actor2 *internalpb.ActorRef) bool {
			return proto.Equal(actor, actor2)
		})

		x.peerState.Actors = compacted
		encoded, _ := proto.Marshal(x.peerState)
		if err := x.statesMap.Put(ctx, x.node.PeersAddress(), encoded); err != nil {
			return fmt.Errorf("failed to sync peer=(%s) request: %v", x.node.PeersAddress(), err)
		}

		return nil
	})

	if err := eg.Wait(); err != nil {
		logger.Errorf("failed to synchronize peer=(%s): %v", x.node.PeersAddress(), err)
		return err
	}

	logger.Infof("peer (%s) successfully synchronized in the cluster", x.node.PeersAddress())
	return nil
}

// GetState fetches a given peer state
func (x *Engine) GetState(ctx context.Context, peerAddress string) (*internalpb.PeerState, error) {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return nil, ErrEngineNotRunning
	}

	ctx, cancelFn := context.WithTimeout(ctx, x.readTimeout)
	defer cancelFn()

	x.Lock()
	defer x.Unlock()

	logger := x.logger

	logger.Infof("[%s] retrieving peer (%s) sync record", x.node.PeersAddress(), peerAddress)
	resp, err := x.statesMap.Get(ctx, peerAddress)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("[%s] has not found peer=(%s) sync record", x.node.PeersAddress(), peerAddress)
			return nil, ErrPeerSyncNotFound
		}

		logger.Errorf("[%s] failed to find peer=(%s) sync record: %v", x.node.PeersAddress(), peerAddress, err)
		return nil, err
	}

	bytea, err := resp.Byte()
	if err != nil {
		logger.Errorf("[%s] failed to read peer=(%s) sync record: %v", x.node.PeersAddress(), peerAddress, err)
		return nil, err
	}

	peerState := new(internalpb.PeerState)
	if err := proto.Unmarshal(bytea, peerState); err != nil {
		logger.Errorf("[%s] failed to decode peer=(%s) sync record: %v", x.node.PeersAddress(), peerAddress, err)
		return nil, err
	}

	logger.Infof("[%s] successfully retrieved peer (%s) sync record .🎉", x.node.PeersAddress(), peerAddress)
	return peerState, nil
}

// GetActor fetches an actor from the Node
func (x *Engine) GetActor(ctx context.Context, actorName string) (*internalpb.ActorRef, error) {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return nil, ErrEngineNotRunning
	}

	ctx, cancelFn := context.WithTimeout(ctx, x.readTimeout)
	defer cancelFn()

	x.Lock()
	defer x.Unlock()

	logger := x.logger

	logger.Infof("[%s] retrieving actor (%s) from the cluster", x.node.PeersAddress(), actorName)

	resp, err := x.actorsMap.Get(ctx, actorName)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("[%s] could not find actor=%s the cluster", x.node.PeersAddress(), actorName)
			return nil, ErrActorNotFound
		}
		logger.Errorf("[%s] failed to get actor=(%s) record from the cluster: %v", x.node.PeersAddress(), actorName, err)
		return nil, err
	}

	bytea, err := resp.Byte()
	if err != nil {
		logger.Errorf("[%s] failed to read actor=(%s) record: %v", x.node.PeersAddress(), actorName, err)
		return nil, err
	}

	actor, err := decode(bytea)
	if err != nil {
		logger.Errorf("[%s] failed to decode actor=(%s) record: %v", x.node.PeersAddress(), actorName, err)
		return nil, err
	}

	logger.Infof("[%s] successfully retrieved from the cluster actor (%s)", x.node.PeersAddress(), actor.GetActorAddress().GetName())
	return actor, nil
}

// RemoveActor removes a given actor from the cluster.
// An actor is removed from the cluster when this actor has been passivated.
func (x *Engine) RemoveActor(ctx context.Context, actorName string) error {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return ErrEngineNotRunning
	}

	logger := x.logger
	x.Lock()
	defer x.Unlock()

	logger.Infof("removing actor (%s) from cluster", actorName)

	_, err := x.actorsMap.Delete(ctx, actorName)
	if err != nil {
		logger.Errorf("[%s] failed to remove actor=(%s) record from cluster: %v", x.node.PeersAddress(), actorName, err)
		return err
	}

	logger.Infof("actor (%s) successfully removed from the cluster", actorName)
	return nil
}

// SetSchedulerJobKey sets a given key to the cluster
func (x *Engine) SetSchedulerJobKey(ctx context.Context, key string) error {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return ErrEngineNotRunning
	}

	ctx, cancelFn := context.WithTimeout(ctx, x.writeTimeout)
	defer cancelFn()

	x.Lock()
	defer x.Unlock()

	logger := x.logger

	logger.Infof("replicating key (%s)", key)

	if err := x.jobKeysMap.Put(ctx, key, true); err != nil {
		logger.Errorf("failed to replicate scheduler job key (%s): %v", key, err)
		return err
	}

	logger.Infof("key (%s) successfully replicated", key)
	return nil
}

// SchedulerJobKeyExists checks the existence of a given key
func (x *Engine) SchedulerJobKeyExists(ctx context.Context, key string) (bool, error) {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return false, ErrEngineNotRunning
	}

	ctx, cancelFn := context.WithTimeout(ctx, x.readTimeout)
	defer cancelFn()

	x.Lock()
	defer x.Unlock()

	logger := x.logger

	logger.Infof("checking key (%s) existence in the cluster", key)

	resp, err := x.jobKeysMap.Get(ctx, key)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("key=%s is not found in the cluster", key)
			return false, nil
		}

		logger.Errorf("[%s] failed to check scheduler job key (%s) existence: %v", x.node.PeersAddress(), key, err)
		return false, err
	}
	return resp.Bool()
}

// UnsetSchedulerJobKey unsets the already set given key in the cluster
func (x *Engine) UnsetSchedulerJobKey(ctx context.Context, key string) error {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return ErrEngineNotRunning
	}

	logger := x.logger

	x.Lock()
	defer x.Unlock()

	logger.Infof("unsetting key (%s)", key)

	if _, err := x.jobKeysMap.Delete(ctx, key); err != nil {
		logger.Errorf("failed to unset scheduler job key (%s): %v", key, err)
		return err
	}

	logger.Infof("key (%s) successfully unset", key)
	return nil
}

// GetPartition returns the partition where a given actor is stored
func (x *Engine) GetPartition(actorName string) int {
	// return -1 when the engine is not running
	if !x.IsRunning() {
		return -1
	}

	key := []byte(actorName)
	hkey := x.hasher.HashCode(key)
	partition := int(hkey % x.partitionsCount)
	x.logger.Debugf("partition of actor (%s) is (%d)", actorName, partition)
	return partition
}

// Events returns a channel where cluster events are published
func (x *Engine) Events() <-chan *Event {
	return x.events
}

// Peers returns a channel containing the list of peers at a given time
func (x *Engine) Peers(ctx context.Context) ([]*Peer, error) {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return nil, ErrEngineNotRunning
	}

	x.Lock()
	defer x.Unlock()
	client := x.client

	members, err := client.Members(ctx)
	if err != nil {
		x.logger.Errorf("failed to read cluster peers: %v", err)
		return nil, err
	}

	peers := make([]*Peer, 0, len(members))
	for _, member := range members {
		if member.Name != x.node.PeersAddress() {
			x.logger.Debugf("node=(%s) has found peer=(%s)", x.node.PeersAddress(), member.Name)
			peerHost, port, _ := net.SplitHostPort(member.Name)
			peerPort, _ := strconv.Atoi(port)
			peers = append(peers, &Peer{Host: peerHost, PeersPort: peerPort, Coordinator: member.Coordinator})
		}
	}
	return peers, nil
}

// consume reads to the underlying cluster events
// and emit the event
func (x *Engine) consume() {
	for message := range x.messages {
		payload := message.Payload
		var event map[string]any
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			x.logger.Errorf("failed to unmarshal cluster event: %v", err)
			// TODO: should we continue or not
			continue
		}

		kind := event["kind"]
		switch kind {
		case events.KindNodeJoinEvent:
			x.eventsLock.Lock()
			nodeJoined := new(events.NodeJoinEvent)
			if err := json.Unmarshal([]byte(payload), &nodeJoined); err != nil {
				x.logger.Errorf("failed to unmarshal node join cluster event: %v", err)
				// TODO: should we continue or not
				x.eventsLock.Unlock()
				continue
			}

			if x.node.PeersAddress() == nodeJoined.NodeJoin {
				x.logger.Debug("skipping self")
				x.eventsLock.Unlock()
				continue
			}

			if x.nodeJoinedEventsFilter.Contains(nodeJoined.NodeJoin) {
				x.eventsLock.Unlock()
				continue
			}

			x.nodeJoinedEventsFilter.Add(nodeJoined.NodeJoin)
			timeMilli := nodeJoined.Timestamp / int64(1e6)
			event := &goaktpb.NodeJoined{
				Address:   nodeJoined.NodeJoin,
				Timestamp: timestamppb.New(time.UnixMilli(timeMilli)),
			}

			x.logger.Debugf("%s received (%s):[addr=(%s)] cluster event", x.name, kind, event.GetAddress())
			payload, _ := anypb.New(event)
			x.events <- &Event{payload, NodeJoined}
			x.eventsLock.Unlock()

		case events.KindNodeLeftEvent:
			x.eventsLock.Lock()
			nodeLeft := new(events.NodeLeftEvent)
			if err := json.Unmarshal([]byte(payload), &nodeLeft); err != nil {
				x.logger.Errorf("failed to unmarshal node left cluster event: %v", err)
				// TODO: should we continue or not
				x.eventsLock.Unlock()
				continue
			}

			if x.nodeLeftEventsFilter.Contains(nodeLeft.NodeLeft) {
				x.eventsLock.Unlock()
				continue
			}

			x.nodeLeftEventsFilter.Add(nodeLeft.NodeLeft)
			timeMilli := nodeLeft.Timestamp / int64(1e6)
			event := &goaktpb.NodeLeft{
				Address:   nodeLeft.NodeLeft,
				Timestamp: timestamppb.New(time.UnixMilli(timeMilli)),
			}

			x.logger.Debugf("%s received (%s):[addr=(%s)] cluster event", x.name, kind, event.GetAddress())
			payload, _ := anypb.New(event)
			x.events <- &Event{payload, NodeLeft}
			x.eventsLock.Unlock()
		default:
			// skip
		}
	}
}

// buildConfig builds the Node configuration
func (x *Engine) buildConfig() (*config.Config, error) {
	// define the log level
	logLevel := "INFO"
	switch x.logger.LogLevel() {
	case log.DebugLevel:
		logLevel = "DEBUG"
	case log.ErrorLevel, log.FatalLevel, log.PanicLevel:
		logLevel = "ERROR"
	case log.WarningLevel:
		logLevel = "WARN"
	default:
		// pass
	}

	// set the cluster storage tableSize
	options := storage.NewConfig(nil)
	options.Add("tableSize", x.storageSize)

	// create the config and return it
	conf := &config.Config{
		BindAddr:          x.node.Host,
		BindPort:          x.node.PeersPort,
		ReadRepair:        true,
		ReplicaCount:      int(x.replicaCount),
		WriteQuorum:       int(x.writeQuorum),
		ReadQuorum:        int(x.readQuorum),
		MemberCountQuorum: int32(x.minimumPeersQuorum),
		Peers:             []string{},
		DMaps: &config.DMaps{
			Engine: &config.Engine{
				Config: options.ToMap(),
			},
		},
		KeepAlivePeriod:            config.DefaultKeepAlivePeriod,
		PartitionCount:             x.partitionsCount,
		BootstrapTimeout:           config.DefaultBootstrapTimeout,
		ReplicationMode:            config.SyncReplicationMode,
		RoutingTablePushInterval:   config.DefaultRoutingTablePushInterval,
		JoinRetryInterval:          config.DefaultJoinRetryInterval,
		MaxJoinAttempts:            config.DefaultMaxJoinAttempts,
		LogLevel:                   logLevel,
		LogOutput:                  newLogWriter(x.logger),
		EnableClusterEventsChannel: true,
		Hasher:                     hasher.NewDefaultHasher(),
		TriggerBalancerInterval:    config.DefaultTriggerBalancerInterval,
	}

	// Set TLS configuration accordingly
	if x.tlsServerConfig != nil && x.tlsClientConfig != nil {
		// set the server TLS config
		conf.TlsConfig = x.tlsServerConfig

		// create a client configuration that will be used by the
		// embedded client calls
		client := &config.Client{TLSConfig: x.tlsClientConfig}
		// sanitize client configuration
		if err := client.Sanitize(); err != nil {
			return nil, fmt.Errorf("failed to sanitize client config: %v", err)
		}

		// set the client configuration
		conf.Client = client
	}

	// set verbosity when debug is enabled
	if x.logger.LogLevel() == log.DebugLevel {
		conf.LogVerbosity = config.DefaultLogVerbosity
	}

	return conf, nil
}

// initializeState sets the node state in the cluster after boot
func (x *Engine) initializeState(ctx context.Context) error {
	encoded, _ := proto.Marshal(x.peerState)
	return x.statesMap.Put(ctx, x.node.PeersAddress(), encoded)
}
