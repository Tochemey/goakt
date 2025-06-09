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

	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/hash"
	"github.com/tochemey/goakt/v3/internal/errorschain"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/memberlist"
	"github.com/tochemey/goakt/v3/internal/size"
	"github.com/tochemey/goakt/v3/log"
)

type EventType int

const (
	NodeJoined EventType = iota
	NodeLeft
	actorsMap     = "actors"
	statesMap     = "states"
	jobKeysMap    = "jobKeys"
	actorKindsMap = "actorKinds"
	grainsMap     = "grains"
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
	// PutGrain replicates a Grain into the cluster
	PutGrain(ctx context.Context, grain *internalpb.Grain) error
	// GetGrain returns a Grain from the cluster
	GetGrain(ctx context.Context, grainID string) (*internalpb.Grain, error)
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

	actorsMap     olric.DMap
	statesMap     olric.DMap
	jobKeysMap    olric.DMap
	actorKindsMap olric.DMap
	grainsMap     olric.DMap
	tableSize     uint64

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

	transport, err := memberlist.NewTransport(memberlist.TransportConfig{
		BindAddrs:          []string{x.node.Host},
		BindPort:           x.node.DiscoveryPort,
		PacketDialTimeout:  5 * time.Second,
		PacketWriteTimeout: 5 * time.Second,
		Logger:             x.logger,
		DebugEnabled:       false,
		TLSEnabled:         x.serverTLS != nil,
		TLS:                x.serverTLS,
	})
	if err != nil {
		x.logger.Errorf("Failed to create memberlist TCP transport: %v", err)
		return err
	}

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
	m.Transport = transport
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
	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddErrorFn(func() error { x.actorsMap, err = x.client.NewDMap(actorsMap); return err }).
		AddErrorFn(func() error { x.statesMap, err = x.client.NewDMap(statesMap); return err }).
		AddErrorFn(func() error { x.jobKeysMap, err = x.client.NewDMap(jobKeysMap); return err }).
		AddErrorFn(func() error { x.actorKindsMap, err = x.client.NewDMap(actorKindsMap); return err }).
		AddErrorFn(func() error { x.grainsMap, err = x.client.NewDMap(grainsMap); return err }).
		Error(); err != nil {
		logger.Error(fmt.Errorf("failed to start the cluster Engine on node=(%s): %w", x.name, err))
		return x.server.Shutdown(ctx)
	}

	// set the peer state
	x.peerState = &internalpb.PeerState{
		Host:         x.node.Host,
		RemotingPort: int32(x.node.RemotingPort),
		PeersPort:    int32(x.node.PeersPort),
		Actors:       map[string]*internalpb.Actor{},
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

	x.running.Store(false)
	// create an errors chain to pipeline the shutdown processes
	chain := errorschain.
		New(errorschain.ReturnFirst()).
		AddErrorFn(func() error { return x.pubSub.Close() }).
		AddErrorFn(func() error { return x.actorsMap.Destroy(ctx) }).
		AddErrorFn(func() error { return x.statesMap.Destroy(ctx) }).
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

	logger.Infof("(%s) synchronization...", x.node.PeersAddress())

	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(2)

	eg.Go(func() error {
		encoded, _ := encode(actor)
		key := actor.GetAddress().GetName()
		kind := actor.GetType()
		if actor.GetIsSingleton() {
			if err := errorschain.
				New(errorschain.ReturnFirst()).
				AddErrorFn(func() error { return x.actorsMap.Put(ctx, key, encoded) }).
				AddErrorFn(func() error { return x.actorKindsMap.Put(ctx, kind, kind) }).
				Error(); err != nil {
				return fmt.Errorf("(%s) failed to sync actor=(%s): %v", x.node.PeersAddress(), actor.GetAddress().GetName(), err)
			}
			return nil
		}

		if err := x.actorsMap.Put(ctx, key, encoded); err != nil {
			return fmt.Errorf("(%s) failed to sync actor=(%s): %v", x.node.PeersAddress(), actor.GetAddress().GetName(), err)
		}
		return nil
	})

	eg.Go(func() error {
		actors := x.peerState.GetActors()
		actorName := actor.GetAddress().GetName()
		actors[actorName] = &internalpb.Actor{
			Address:        actor.GetAddress(),
			Type:           actor.GetType(),
			IsSingleton:    actor.GetIsSingleton(),
			Relocatable:    actor.GetRelocatable(),
			PassivateAfter: actor.PassivateAfter,
			Dependencies:   actor.GetDependencies(),
			EnableStash:    actor.GetEnableStash(),
		}
		x.peerState.Actors = actors

		bytea, _ := proto.Marshal(x.peerState)
		if err := x.statesMap.Put(ctx, x.node.PeersAddress(), bytea); err != nil {
			return fmt.Errorf("(%s) failed to sync state: %v", x.node.PeersAddress(), err)
		}
		return nil
	})

	if err := eg.Wait(); err != nil {
		logger.Errorf("(%s) synchronization failed: %v", x.node.PeersAddress(), err)
		return err
	}

	logger.Infof("(%s) successfully synchronized in the cluster", x.node.PeersAddress())
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

		logger.Errorf("(%s) failed to find peer=(%s) sync record: %v", x.node.PeersAddress(), peerAddress, err)
		return nil, err
	}

	bytea, err := resp.Byte()
	if err != nil {
		logger.Errorf("(%s) failed to read peer=(%s) sync record: %v", x.node.PeersAddress(), peerAddress, err)
		return nil, err
	}

	peerState := new(internalpb.PeerState)
	if err := proto.Unmarshal(bytea, peerState); err != nil {
		logger.Errorf("(%s) failed to decode peer=(%s) sync record: %v", x.node.PeersAddress(), peerAddress, err)
		return nil, err
	}

	logger.Infof("(%s) successfully retrieved peer (%s) sync record .🎉", x.node.PeersAddress(), peerAddress)
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

	logger.Infof("(%s) retrieving actor (%s) from the cluster", x.node.PeersAddress(), actorName)

	resp, err := x.actorsMap.Get(ctx, actorName)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("(%s) could not find actor=%s the cluster", x.node.PeersAddress(), actorName)
			return nil, ErrActorNotFound
		}
		logger.Errorf("(%s) failed to get actor=(%s) record from the cluster: %v", x.node.PeersAddress(), actorName, err)
		return nil, err
	}

	bytea, err := resp.Byte()
	if err != nil {
		logger.Errorf("(%s) failed to read actor=(%s) record: %v", x.node.PeersAddress(), actorName, err)
		return nil, err
	}

	actor, err := decode(bytea)
	if err != nil {
		logger.Errorf("(%s) failed to decode actor=(%s) record: %v", x.node.PeersAddress(), actorName, err)
		return nil, err
	}

	logger.Infof("(%s) successfully retrieved from the cluster actor (%s)", x.node.PeersAddress(), actor.GetAddress().GetName())
	return actor, nil
}

// PutGrain replicates a Grain into the cluster
func (x *Engine) PutGrain(ctx context.Context, grain *internalpb.Grain) error {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return ErrEngineNotRunning
	}

	ctx, cancelFn := context.WithTimeout(ctx, x.writeTimeout)
	defer cancelFn()

	x.Lock()
	defer x.Unlock()

	logger := x.logger

	logger.Infof("(%s) synchronization...", x.node.PeersAddress())

	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(2)

	kind := grain.GetGrainId().GetKind()
	grainID := grain.GetGrainId().GetValue()

	eg.Go(func() error {
		encoded, _ := encodeGrain(grain)
		if err := errorschain.
			New(errorschain.ReturnFirst()).
			AddErrorFn(func() error { return x.grainsMap.Put(ctx, grainID, encoded) }).
			AddErrorFn(func() error { return x.actorKindsMap.Put(ctx, kind, kind) }).
			Error(); err != nil {
			return fmt.Errorf("(%s) failed to sync grain=(%s): %v", x.node.PeersAddress(), grainID, err)
		}

		return nil
	})

	eg.Go(func() error {
		grains := x.peerState.GetGrains()
		grains[grainID] = &internalpb.Grain{
			GrainId:        grain.GetGrainId(),
			PassivateAfter: grain.GetPassivateAfter(),
			Dependencies:   grain.GetDependencies(),
		}
		x.peerState.Grains = grains

		bytea, _ := proto.Marshal(x.peerState)
		if err := x.statesMap.Put(ctx, x.node.PeersAddress(), bytea); err != nil {
			return fmt.Errorf("(%s) failed to sync state: %v", x.node.PeersAddress(), err)
		}
		return nil
	})

	if err := eg.Wait(); err != nil {
		logger.Errorf("(%s) synchronization failed: %v", x.node.PeersAddress(), err)
		return err
	}

	logger.Infof("(%s) successfully synchronized in the cluster", x.node.PeersAddress())
	return nil
}

// GetGrain returns a Grain from the cluster
func (x *Engine) GetGrain(ctx context.Context, grainID string) (*internalpb.Grain, error) {
	// return an error when the engine is not running
	if !x.IsRunning() {
		return nil, ErrEngineNotRunning
	}

	ctx, cancelFn := context.WithTimeout(ctx, x.readTimeout)
	defer cancelFn()

	x.Lock()
	defer x.Unlock()

	logger := x.logger

	logger.Infof("(%s) retrieving grain (%s) from the cluster", x.node.PeersAddress(), grainID)

	resp, err := x.grainsMap.Get(ctx, grainID)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("(%s) could not find grain=%s the cluster", x.node.PeersAddress(), grainID)
			return nil, ErrGrainNotFound
		}
		logger.Errorf("(%s) failed to get grain=(%s) record from the cluster: %v", x.node.PeersAddress(), grainID, err)
		return nil, err
	}

	bytea, err := resp.Byte()
	if err != nil {
		logger.Errorf("(%s) failed to read grain=(%s) record: %v", x.node.PeersAddress(), grainID, err)
		return nil, err
	}

	grain, err := decodeGrain(bytea)
	if err != nil {
		logger.Errorf("(%s) failed to decode grain=(%s) record: %v", x.node.PeersAddress(), grainID, err)
		return nil, err
	}

	logger.Infof("(%s) successfully retrieved from the cluster grain (%s)", x.node.PeersAddress(), grain.GetGrainId().GetValue())
	return grain, nil
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
		logger.Errorf("(%s) failed to remove actor=(%s) record from cluster: %v", x.node.PeersAddress(), actorName, err)
		return err
	}

	logger.Infof("actor (%s) successfully removed from the cluster", actorName)
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

	_, err := x.actorKindsMap.Delete(ctx, kind)
	if err != nil {
		logger.Errorf("(%s) failed to remove actor kind=(%s) record from cluster: %v", x.node.PeersAddress(), kind, err)
		return err
	}

	logger.Infof("actor kind (%s) successfully removed from the cluster", kind)
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

	resp, err := x.actorKindsMap.Get(ctx, kind)
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

// initializeState sets the node state in the cluster after boot
func (x *Engine) initializeState(ctx context.Context) error {
	encoded, _ := proto.Marshal(x.peerState)
	return x.statesMap.Put(ctx, x.node.PeersAddress(), encoded)
}
