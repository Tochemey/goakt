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
	"os"
	"runtime"
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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/hash"
	"github.com/tochemey/goakt/v3/internal/errorschain"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/memberlist"
	"github.com/tochemey/goakt/v3/internal/size"
	"github.com/tochemey/goakt/v3/log"
)

// SyncState defines the state of the cluster node during synchronization
type SyncState int32

const (
	BUSY SyncState = iota
	IDLE
)

type EventType int

const (
	NodeJoined EventType = iota
	NodeLeft
	actorsMap  = "actors"
	statesMap  = "states"
	jobKeysMap = "jobKeys"
	kindsMap   = "actorKinds"
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
	PutActor(ctx context.Context, actor *internalpb.Actor) error
	// GetActor fetches an actor from the Node
	GetActor(ctx context.Context, actorName string) (*internalpb.Actor, error)
	// GetPartition returns the partition where a given actor is stored
	GetPartition(actorName string) int
	// LookupKind checks the existence of a given actor kind in the cluster
	// This function is mainly used when creating a singleton actor
	LookupKind(ctx context.Context, kind string) (string, error)
	// RemoveActor removes a given actor from the cluster.
	// An actor is removed from the cluster when this actor has been passivated.
	RemoveActor(ctx context.Context, actorName string) error
	// RemoveKind removes a given actor from the cluster
	// when the node where the kind is created shuts down
	RemoveKind(ctx context.Context, kind string) error
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
	Actors(ctx context.Context, timeout time.Duration) ([]*internalpb.Actor, error)
	// IsRunning returns true when the cluster engine is running
	IsRunning() bool
	// ActorExists checks whether an actor exists in the cluster
	ActorExists(ctx context.Context, actorName string) (bool, error)
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

	actorsMap  olric.DMap
	statesMap  olric.DMap
	jobKeysMap olric.DMap
	kindsMap   olric.DMap
	tableSize  uint64

	// specifies the discovery node
	node *discovery.Node

	// specifies the hasher
	hasher hash.Hasher

	// specifies the discovery provider
	discoveryProvider discovery.Provider

	writeTimeout      time.Duration
	readTimeout       time.Duration
	shutdownTimeout   time.Duration
	bootstrapTimeout  time.Duration
	cacheSyncInterval time.Duration

	events     chan *Event
	eventsLock *sync.Mutex
	pubSub     *redis.PubSub
	messages   <-chan *redis.Message

	// specifies the node state
	peerState      *internalpb.PeerState
	peerStateQueue *binaryQueue
	syncState      *atomic.Int32
	goScheduler    *goScheduler

	nodeJoinedEventsFilter goset.Set[string]
	nodeLeftEventsFilter   goset.Set[string]

	clientTLS *tls.Config
	serverTLS *tls.Config

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
		bootstrapTimeout:       10 * time.Second,
		cacheSyncInterval:      time.Minute,
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
		tableSize:              20 * size.MB,
		running:                atomic.NewBool(false),
		peerStateQueue:         newBinaryQueue(),
		syncState:              atomic.NewInt32(int32(IDLE)),
		goScheduler:            newGoScheduler(300),
	}
	// apply the various options
	for _, opt := range opts {
		opt.Apply(engine)
	}

	// perform some quick validations
	if (engine.serverTLS == nil) != (engine.clientTLS == nil) {
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

	if err := x.setupMemberlistConfig(conf); err != nil {
		return err
	}

	x.configureDiscovery(conf)

	startCtx, cancel := context.WithCancel(ctx)
	conf.Started = func() { defer cancel() }

	cache, err := olric.New(conf)
	if err != nil {
		logger.Error(fmt.Errorf("failed to start the cluster Engine on node=(%s): %w", x.name, err))
		return err
	}

	x.server = cache
	if err := x.startServer(startCtx, ctx); err != nil {
		logger.Error(fmt.Errorf("failed to start the cluster Engine on node=(%s): %w", x.name, err))
		return err
	}

	x.client = x.server.NewEmbeddedClient()
	if err := x.createMaps(); err != nil {
		logger.Error(fmt.Errorf("failed to start the cluster Engine on node=(%s): %w", x.name, err))
		se := x.server.Shutdown(ctx)
		return errors.Join(err, se)
	}

	if err := x.createSubscription(ctx); err != nil {
		logger.Error(fmt.Errorf("failed to start the cluster Engine on node=(%s): %w", x.name, err))
		se := x.server.Shutdown(ctx)
		return errors.Join(err, se)
	}

	x.initPeerState()
	x.running.Store(true)
	x.synchronizeState()

	go x.consume()
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

	x.running.Store(false)
	// create an errors chain to pipeline the shutdown processes
	chain := errorschain.
		New(errorschain.ReturnFirst()).
		AddErrorFn(func() error { return x.pubSub.Close() }).
		AddErrorFn(func() error { return x.actorsMap.Destroy(ctx) }).
		AddErrorFn(func() error { return x.statesMap.Destroy(ctx) }).
		AddErrorFn(func() error { return x.kindsMap.Destroy(ctx) }).
		AddErrorFn(func() error { return x.jobKeysMap.Destroy(ctx) }).
		AddErrorFn(func() error { return x.client.Close(ctx) }).
		AddErrorFn(func() error { return x.server.Shutdown(ctx) })

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
	members, err := x.client.Members(ctx)
	if err != nil {
		x.logger.Errorf("failed to fetch the cluster members=(%s): %v", x.node.PeersAddress(), err)
		return false
	}

	for _, member := range members {
		node := new(discovery.Node)
		// unmarshal the member meta information
		_ = json.Unmarshal([]byte(member.Meta), node)
		if node.PeersAddress() == x.node.PeersAddress() && member.Coordinator {
			return true
		}
	}
	return false
}

// Actors returns all actors in the cluster at any given time
func (x *Engine) Actors(ctx context.Context, timeout time.Duration) ([]*internalpb.Actor, error) {
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

	var actors []*internalpb.Actor
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
func (x *Engine) PutActor(ctx context.Context, actor *internalpb.Actor) error {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return ErrEngineNotRunning
	}

	ctx, cancelFn := context.WithTimeout(ctx, x.writeTimeout)
	defer cancelFn()

	x.Lock()
	defer x.Unlock()

	logger := x.logger
	logger.Infof("node=(%s) synchronizing Actor (%s)...", x.node.PeersAddress(), actor.GetAddress().GetName())

	encoded, _ := encode(actor)
	key := actor.GetAddress().GetName()
	kind := actor.GetType()

	// keep the actor kind in the cluster for singleton actors
	if actor.GetIsSingleton() {
		if err := x.kindsMap.Put(ctx, kind, kind); err != nil {
			return fmt.Errorf("node=(%s) failed to sync actor=(%s): %v", x.node.PeersAddress(), actor.GetAddress().GetName(), err)
		}
	}

	// put the actor into the actors map
	if err := x.actorsMap.Put(ctx, key, encoded); err != nil {
		return fmt.Errorf("node=(%s) failed to sync actor=(%s): %v", x.node.PeersAddress(), actor.GetAddress().GetName(), err)
	}

	actors := x.peerState.GetActors()
	actorName := actor.GetAddress().GetName()
	actors[actorName] = actor

	x.peerState.Actors = actors
	x.synchronizeState()

	logger.Infof("node=(%s) successfully synchronized Actor (%s) in the cluster", x.node.PeersAddress(), key)
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

	logger.Infof("node=(%s) retrieving peer (%s) sync record", x.node.PeersAddress(), peerAddress)
	resp, err := x.statesMap.Get(ctx, peerAddress)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("[%s] has not found peer=(%s) sync record", x.node.PeersAddress(), peerAddress)
			return nil, ErrPeerSyncNotFound
		}

		logger.Errorf("node=(%s) failed to find peer=(%s) sync record: %v", x.node.PeersAddress(), peerAddress, err)
		return nil, err
	}

	bytea, err := resp.Byte()
	if err != nil {
		logger.Errorf("node=(%s) failed to read peer=(%s) sync record: %v", x.node.PeersAddress(), peerAddress, err)
		return nil, err
	}

	peerState := new(internalpb.PeerState)
	if err := proto.Unmarshal(bytea, peerState); err != nil {
		logger.Errorf("node=(%s) failed to decode peer=(%s) sync record: %v", x.node.PeersAddress(), peerAddress, err)
		return nil, err
	}

	logger.Infof("node=(%s) successfully retrieved peer (%s) sync record .🎉", x.node.PeersAddress(), peerAddress)
	return peerState, nil
}

// GetActor fetches an actor from the Node
func (x *Engine) GetActor(ctx context.Context, actorName string) (*internalpb.Actor, error) {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return nil, ErrEngineNotRunning
	}

	ctx, cancelFn := context.WithTimeout(ctx, x.readTimeout)
	defer cancelFn()

	x.Lock()
	defer x.Unlock()

	logger := x.logger

	logger.Infof("node=(%s) retrieving actor (%s) from the cluster", x.node.PeersAddress(), actorName)

	resp, err := x.actorsMap.Get(ctx, actorName)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("node=(%s) could not find actor=%s the cluster", x.node.PeersAddress(), actorName)
			return nil, ErrActorNotFound
		}
		logger.Errorf("node=(%s) failed to get actor=(%s) record from the cluster: %v", x.node.PeersAddress(), actorName, err)
		return nil, err
	}

	bytea, err := resp.Byte()
	if err != nil {
		logger.Errorf("node=(%s) failed to read actor=(%s) record: %v", x.node.PeersAddress(), actorName, err)
		return nil, err
	}

	actor, err := decode(bytea)
	if err != nil {
		logger.Errorf("node=(%s) failed to decode actor=(%s) record: %v", x.node.PeersAddress(), actorName, err)
		return nil, err
	}

	logger.Infof("node=(%s) successfully retrieved from the cluster actor (%s)", x.node.PeersAddress(), actor.GetAddress().GetName())
	return actor, nil
}

// ActorExists checks whether an actor exists in the cluster
func (x *Engine) ActorExists(ctx context.Context, actorName string) (bool, error) {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return false, ErrEngineNotRunning
	}

	ctx, cancelFn := context.WithTimeout(ctx, x.readTimeout)
	defer cancelFn()

	x.Lock()
	defer x.Unlock()

	// check whether the actor exists in the actors map
	if _, err := x.actorsMap.Get(ctx, actorName); err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
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

	logger.Infof("node=(%s) removing actor (%s) from cluster", x.node.PeersAddress(), actorName)

	// remove the actor from the peer state
	actors := x.peerState.GetActors()
	delete(actors, actorName)
	x.peerState.Actors = actors
	x.synchronizeState()

	_, err := x.actorsMap.Delete(ctx, actorName)
	if err != nil {
		logger.Errorf("node=(%s) failed to remove actor=(%s) record from cluster: %v", x.node.PeersAddress(), actorName, err)
		return err
	}

	logger.Infof("node=(%s) successfully removed actor (%s) from the cluster", x.node.PeersAddress(), actorName)
	return nil
}

// RemoveKind removes a given actor from the cluster
// when the node where the kind is created shuts down
func (x *Engine) RemoveKind(ctx context.Context, kind string) error {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return ErrEngineNotRunning
	}

	logger := x.logger
	x.Lock()
	defer x.Unlock()

	logger.Infof("removing actor kind (%s) from cluster", kind)

	_, err := x.kindsMap.Delete(ctx, kind)
	if err != nil {
		logger.Errorf("node=(%s) failed to remove actor kind=(%s) record from cluster: %v", x.node.PeersAddress(), kind, err)
		return err
	}

	logger.Infof("node=(%s) successfully removed actor kind (%s) from the cluster", x.node.PeersAddress(), kind)
	return nil
}

// LookupKind checks the existence of a given actor kind in the cluster
// This function is mainly used when creating a singleton actor
func (x *Engine) LookupKind(ctx context.Context, kind string) (string, error) {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return "", ErrEngineNotRunning
	}

	ctx, cancelFn := context.WithTimeout(ctx, x.readTimeout)
	defer cancelFn()

	x.Lock()
	defer x.Unlock()

	logger := x.logger

	logger.Infof("checking actor kind (%s) existence in the cluster", kind)

	resp, err := x.kindsMap.Get(ctx, kind)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("actor kind=%s is not found in the cluster", kind)
			return "", nil
		}

		logger.Errorf("[%s] failed to check actor kind (%s) existence: %v", x.node.PeersAddress(), kind, err)
		return "", err
	}
	return resp.String()
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
			node := new(discovery.Node)
			// unmarshal the member meta information
			_ = json.Unmarshal([]byte(member.Meta), node)
			peers = append(peers, &Peer{
				Host:         node.Host,
				PeersPort:    node.PeersPort,
				Coordinator:  member.Coordinator,
				RemotingPort: node.RemotingPort,
			})
		}
	}
	return peers, nil
}

// synchronizeState enqueues the current peer state for synchronization with the cluster.
//
// If the engine is running, the peer state is marshaled and added to the peer state queue.
// Triggers the peer state synchronization loop if not already running.
func (x *Engine) synchronizeState() {
	if x.running.Load() {
		bytea, _ := proto.Marshal(x.peerState)
		x.peerStateQueue.enqueue(bytea)
		if x.syncState.CompareAndSwap(int32(IDLE), int32(BUSY)) {
			x.goScheduler.Schedule(x.peerStateSyncLoop)
		}
	}
}

// peerStateSyncLoop is the background loop that pushes peer state updates to the cluster.
//
// It processes the peer state queue, writing updates to the distributed states map.
// The loop yields periodically based on the configured scheduler throughput.
func (x *Engine) peerStateSyncLoop() {
	counter, throughput := 0, x.goScheduler.Throughput()
	logger := x.logger
	for {
		if counter > throughput {
			counter = 0
			runtime.Gosched()
		}

		counter++
		if bytea := x.peerStateQueue.dequeue(); len(bytea) > 0 {
			ctx := context.Background()
			logger.Infof("node=(%s) begins state synchronization...", x.node.PeersAddress())
			if err := x.statesMap.Put(ctx, x.node.PeersAddress(), bytea); err != nil {
				// TODO: should we retry or not?
				logger.Errorf("node=(%s) failed to sync state: %v", x.node.PeersAddress(), err)
			} else {
				logger.Infof("node=(%s) state successfully synchronized in the cluster", x.node.PeersAddress())
			}
		}

		if !x.syncState.CompareAndSwap(int32(BUSY), int32(IDLE)) {
			return
		}

		if !x.peerStateQueue.isEmpty() && x.syncState.CompareAndSwap(int32(IDLE), int32(BUSY)) {
			continue
		}
		return
	}
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
			// synchronize the state after a node join event
			x.synchronizeState()

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
			// synchronize the state after a node left event
			x.synchronizeState()
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

	// serialize the node information as json and pass it as node meta
	jsonbytes, _ := json.Marshal(x.node)
	meta := string(jsonbytes)

	// set the cluster storage tableSize
	options := storage.NewConfig(nil)
	options.Add("tableSize", x.tableSize)

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
		BootstrapTimeout:           x.bootstrapTimeout,
		ReplicationMode:            config.SyncReplicationMode,
		RoutingTablePushInterval:   x.cacheSyncInterval,
		JoinRetryInterval:          config.DefaultJoinRetryInterval,
		MaxJoinAttempts:            config.DefaultMaxJoinAttempts,
		LogLevel:                   logLevel,
		LogOutput:                  newLogWriter(x.logger),
		EnableClusterEventsChannel: true,
		Hasher:                     hasher.NewDefaultHasher(),
		TriggerBalancerInterval:    config.DefaultTriggerBalancerInterval,
		MemberMeta:                 meta,
	}

	// Set TLS configuration accordingly
	if x.serverTLS != nil && x.clientTLS != nil {
		// set the server TLS info
		conf.TLS = &config.TLS{
			Client: x.clientTLS,
			Server: x.serverTLS,
		}

		// create a client configuration that will be used by the
		// embedded client calls
		client := &config.Client{TLS: x.clientTLS}
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

func (x *Engine) setupMemberlistConfig(conf *config.Config) error {
	m, err := config.NewMemberlistConfig("lan")
	if err != nil {
		x.logger.Errorf("failed to configure the cluster Engine members list.💥: %v", err)
		return err
	}
	m.BindAddr = x.node.Host
	m.BindPort = x.node.DiscoveryPort
	m.AdvertisePort = x.node.DiscoveryPort
	m.AdvertiseAddr = x.node.Host

	if x.serverTLS != nil {
		transport, err := memberlist.NewTransport(memberlist.TransportConfig{
			BindAddrs:          []string{x.node.Host},
			BindPort:           x.node.DiscoveryPort,
			PacketDialTimeout:  5 * time.Second,
			PacketWriteTimeout: 5 * time.Second,
			Logger:             x.logger,
			DebugEnabled:       false,
			TLSEnabled:         true,
			TLS:                x.serverTLS,
		})
		if err != nil {
			x.logger.Errorf("Failed to create memberlist TCP transport: %v", err)
			return err
		}
		m.Transport = transport
	}
	conf.MemberlistConfig = m
	return nil
}

func (x *Engine) configureDiscovery(conf *config.Config) {
	discoveryWrapper := &discoveryProvider{
		provider: x.discoveryProvider,
		log:      x.logger.StdLogger(),
	}
	conf.ServiceDiscovery = map[string]any{
		"plugin": discoveryWrapper,
		"id":     x.discoveryProvider.ID(),
	}
}

func (x *Engine) startServer(startCtx, ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		if err := x.server.Start(); err != nil {
			errCh <- errors.Join(err, x.server.Shutdown(ctx))
			return
		}
		errCh <- nil
	}()

	select {
	case <-startCtx.Done():
		// started successfully
	case err := <-errCh:
		if err != nil {
			return err
		}
	}
	return nil
}

func (x *Engine) createMaps() error {
	var err error
	return errorschain.
		New(errorschain.ReturnFirst()).
		AddErrorFn(func() error { x.actorsMap, err = x.client.NewDMap(actorsMap); return err }).
		AddErrorFn(func() error { x.statesMap, err = x.client.NewDMap(statesMap); return err }).
		AddErrorFn(func() error { x.jobKeysMap, err = x.client.NewDMap(jobKeysMap); return err }).
		AddErrorFn(func() error { x.kindsMap, err = x.client.NewDMap(kindsMap); return err }).
		Error()
}

func (x *Engine) createSubscription(ctx context.Context) error {
	ps, err := x.client.NewPubSub(olric.ToAddress(x.node.PeersAddress()))
	if err != nil {
		return err
	}
	x.pubSub = ps.Subscribe(ctx, events.ClusterEventsChannel)
	x.messages = x.pubSub.Channel()
	return nil
}

func (x *Engine) initPeerState() {
	x.peerState = &internalpb.PeerState{
		Host:         x.node.Host,
		RemotingPort: int32(x.node.RemotingPort),
		PeersPort:    int32(x.node.PeersPort),
		Actors:       map[string]*internalpb.Actor{},
	}
}
