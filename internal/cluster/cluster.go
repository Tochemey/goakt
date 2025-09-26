/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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
	"errors"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	goset "github.com/deckarep/golang-set/v2"
	"github.com/redis/go-redis/v9"
	"github.com/tochemey/olric"
	oconfig "github.com/tochemey/olric/config"
	"github.com/tochemey/olric/events"
	"github.com/tochemey/olric/hasher"
	"github.com/tochemey/olric/pkg/storage"
	"go.uber.org/atomic"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/hash"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/locker"
	"github.com/tochemey/goakt/v3/internal/memberlist"
	"github.com/tochemey/goakt/v3/log"
	gtls "github.com/tochemey/goakt/v3/tls"
)

const (
	dMapName              = "goakt.dmap"
	defaultEventsBufSize  = 256
	namespaceSeparator    = "::"
	peerStatesActorsTopic = "goakt.cluster.peerstates.actors"
	peerStatesGrainsTopic = "goakt.cluster.peerstates.grains"
)

type recordNamespace string

const (
	namespaceActors recordNamespace = "actors"
	namespaceGrains recordNamespace = "grains"
	namespaceKinds  recordNamespace = "kinds"
	namespaceJobs   recordNamespace = "jobs"
)

func composeKey(namespace recordNamespace, id string) string {
	return fmt.Sprintf("%s%s%s", string(namespace), namespaceSeparator, id)
}

func hasNamespace(key string, namespace recordNamespace) bool {
	prefix := string(namespace) + namespaceSeparator
	return strings.HasPrefix(key, prefix)
}

// Cluster captures the behaviour exposed by a cluster engine implementation backed by the unified map.
type Cluster interface {
	// Start boots the cluster engine and joins the underlying memberlist.
	Start(ctx context.Context) error
	// Stop gracefully shuts down the cluster engine and frees resources.
	Stop(ctx context.Context) error
	// PutActor stores the provided actor metadata within the cluster state.
	PutActor(ctx context.Context, actor *internalpb.Actor) error
	// GetActor retrieves actor metadata by name.
	GetActor(ctx context.Context, actorName string) (*internalpb.Actor, error)
	// RemoveActor deletes an actor entry from the cluster store.
	RemoveActor(ctx context.Context, actorName string) error
	// ActorExists checks whether the specified actor is registered.
	ActorExists(ctx context.Context, actorName string) (bool, error)
	// Actors enumerates all actors tracked by the cluster.
	Actors(ctx context.Context, timeout time.Duration) ([]*internalpb.Actor, error)
	// PutGrain records grain metadata in the cluster state.
	PutGrain(ctx context.Context, grain *internalpb.Grain) error
	// GetGrain fetches grain metadata for the provided identity.
	GetGrain(ctx context.Context, identity string) (*internalpb.Grain, error)
	// RemoveGrain deletes grain metadata from the store.
	RemoveGrain(ctx context.Context, identity string) error
	// GrainExists verifies whether a grain entry is present.
	GrainExists(ctx context.Context, identity string) (bool, error)
	// Grains enumerates all grains tracked by the cluster.
	Grains(ctx context.Context, timeout time.Duration) ([]*internalpb.Grain, error)
	// LookupKind reads the value registered for the provided actor kind.
	LookupKind(ctx context.Context, kind string) (string, error)
	// PutKind registers an actor kind mapping.
	PutKind(ctx context.Context, kind string) error
	// RemoveKind deletes an actor kind mapping.
	RemoveKind(ctx context.Context, kind string) error
	// GetState returns the peer state for the specified address.
	GetState(ctx context.Context, peerAddress string) (*internalpb.PeerState, error)
	// DeleteState removes any stored state for the given peer address.
	DeleteState(ctx context.Context, peerAddress string) error
	// Events exposes the event stream describing membership changes.
	Events() <-chan *Event
	// Peers lists known cluster members excluding the local node.
	Peers(ctx context.Context) ([]*Peer, error)
	// IsLeader reports whether the local node acts as the cluster coordinator.
	IsLeader(ctx context.Context) bool
	// GetPartition determines the partition assigned to the actor name.
	GetPartition(actorName string) uint64
	// IsRunning reports whether the cluster engine is currently running.
	IsRunning() bool
	// PutJobKey stores job metadata.
	PutJobKey(ctx context.Context, jobID string, metadata []byte) error
	// DeleteJobKey removes job metadata.
	DeleteJobKey(ctx context.Context, jobID string) error
	// JobKey retrieves stored job metadata.
	JobKey(ctx context.Context, jobID string) ([]byte, error)
	// PublishState shares local peer state with other members during shutdown
	PublishState(ctx context.Context, actors []*internalpb.Actor, grains []*internalpb.Grain) error
}

// cluster implements the Cluster interface backed by an Olric unified
// map and discovery provider. It embeds synchronization primitives, runtime
// configuration and caches required to manage the cluster state.
type cluster struct {
	_  locker.NoCopy
	mu sync.RWMutex

	name                 string
	discoveryProvider    discovery.Provider
	node                 *discovery.Node
	logger               log.Logger
	hasher               hash.Hasher
	partitionsCount      uint64
	minimumPeersQuorum   uint32
	replicaCount         uint32
	writeQuorum          uint32
	readQuorum           uint32
	tableSize            uint64
	writeTimeout         time.Duration
	readTimeout          time.Duration
	shutdownTimeout      time.Duration
	bootstrapTimeout     time.Duration
	routingTableInterval time.Duration
	tlsInfo              *gtls.Info

	server *olric.Olric
	client olric.Client
	dmap   olric.DMap

	events         chan *Event
	eventsLock     *sync.Mutex
	peersStateLock *sync.Mutex
	subscriber     *redis.PubSub
	publisher      *olric.PubSub
	messages       <-chan *redis.Message

	nodeJoinedEventsFilter goset.Set[string]
	nodeLeftEventsFilter   goset.Set[string]

	running *atomic.Bool
	store   Store
}

var _ Cluster = (*cluster)(nil)

// New constructs a cluster implementation using the provided discovery
// provider, node information and optional configuration overrides.
func New(name string, disco discovery.Provider, node *discovery.Node, opts ...ConfigOption) Cluster {
	config := defaultConfig()
	for _, opt := range opts {
		opt(config)
	}

	return &cluster{
		name:                   name,
		discoveryProvider:      disco,
		node:                   node,
		logger:                 config.logger,
		hasher:                 config.shardHasher,
		partitionsCount:        config.shardCount,
		minimumPeersQuorum:     config.minimumMembersQuorum,
		replicaCount:           config.replicasCount,
		writeQuorum:            config.membersWriteQuorum,
		readQuorum:             config.membersReadQuorum,
		tableSize:              config.tableSize,
		writeTimeout:           config.writeTimeout,
		readTimeout:            config.readTimeout,
		shutdownTimeout:        config.shutdownTimeout,
		bootstrapTimeout:       config.bootstrapTimeout,
		routingTableInterval:   config.routingTableInterval,
		tlsInfo:                config.tlsInfo,
		events:                 make(chan *Event, defaultEventsBufSize),
		eventsLock:             &sync.Mutex{},
		peersStateLock:         &sync.Mutex{},
		nodeJoinedEventsFilter: goset.NewSet[string](),
		nodeLeftEventsFilter:   goset.NewSet[string](),
		running:                atomic.NewBool(false),
		store:                  NewMemoryStore(), // TODO: switch to high performance store
	}
}

// Start initializes the cluster engine, configures the underlying Olric
// instance, and begins consuming cluster events.
func (x *cluster) Start(ctx context.Context) error {
	if x.running.Load() {
		return nil
	}

	x.logger.Infof("starting cluster engine (%s)", x.name)
	conf, err := x.buildConfig()
	if err != nil {
		x.logger.Errorf("failed to build engine config: %v", err)
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
		x.logger.Error(fmt.Errorf("failed to start cluster engine: %w", err))
		return err
	}

	x.server = cache
	if err := x.startServer(startCtx, ctx); err != nil {
		x.logger.Error(fmt.Errorf("failed to start cluster engine: %w", err))
		return err
	}

	x.client = x.server.NewEmbeddedClient()
	if err := x.createDMap(); err != nil {
		x.logger.Error(fmt.Errorf("failed to create cluster data map: %w", err))
		se := x.server.Shutdown(ctx)
		return errors.Join(err, se)
	}

	if err := x.createSubscription(ctx); err != nil {
		x.logger.Error(fmt.Errorf("failed to create cluster subscription: %w", err))
		se := x.server.Shutdown(ctx)
		return errors.Join(err, se)
	}

	x.running.Store(true)
	go x.consume()
	x.logger.Infof("cluster engine (%s) started", x.name)
	return nil
}

// Stop gracefully tears down the cluster engine and releases allocated runtime
// resources. It is safe to call multiple times.
func (x *cluster) Stop(ctx context.Context) error {
	if !x.running.Load() {
		return nil
	}

	ctx, cancelFn := context.WithTimeout(ctx, x.shutdownTimeout)
	defer cancelFn()

	x.logger.Infof("stopping cluster engine (%s)", x.name)
	defer x.running.Store(false)

	if err := x.server.Shutdown(ctx); err != nil {
		x.logger.Errorf("failed to stop cluster engine: %v", err)
		return err
	}

	x.eventsLock.Lock()
	close(x.events)
	x.events = nil
	x.eventsLock.Unlock()

	x.logger.Infof("cluster engine (%s) stopped", x.name)
	return nil
}

// PutActor persists the supplied actor metadata into the cluster state and
// updates the local peer cache.
func (x *cluster) PutActor(ctx context.Context, actor *internalpb.Actor) error {
	if !x.running.Load() {
		return ErrEngineNotRunning
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	address := actor.GetAddress()
	key := address.GetName()

	encoded, err := encode(actor)
	if err != nil {
		return err
	}

	return x.putRecord(ctx, namespaceActors, key, encoded)
}

// GetActor fetches actor metadata by name from the unified map.
func (x *cluster) GetActor(ctx context.Context, actorName string) (*internalpb.Actor, error) {
	if !x.running.Load() {
		return nil, ErrEngineNotRunning
	}

	x.mu.RLock()
	defer x.mu.RUnlock()

	value, err := x.getRecord(ctx, namespaceActors, actorName)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			return nil, ErrActorNotFound
		}
		return nil, err
	}

	return decode(value)
}

// RemoveActor deletes an actor entry from the unified map and peer cache.
func (x *cluster) RemoveActor(ctx context.Context, actorName string) error {
	if !x.running.Load() {
		return ErrEngineNotRunning
	}

	x.mu.RLock()
	defer x.mu.RUnlock()

	return x.deleteRecord(ctx, namespaceActors, actorName)
}

// ActorExists reports whether an actor with the given name exists in the
// cluster.
func (x *cluster) ActorExists(ctx context.Context, actorName string) (bool, error) {
	if !x.running.Load() {
		return false, ErrEngineNotRunning
	}

	x.mu.RLock()
	defer x.mu.RUnlock()

	_, err := x.getRecord(ctx, namespaceActors, actorName)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Actors scans the unified map and returns all registered actors.
func (x *cluster) Actors(ctx context.Context, timeout time.Duration) ([]*internalpb.Actor, error) {
	if !x.running.Load() {
		return nil, ErrEngineNotRunning
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	x.mu.RLock()
	defer x.mu.RUnlock()

	scanner, err := x.dmap.Scan(ctx)
	if err != nil {
		return nil, err
	}
	defer scanner.Close()

	actors := make([]*internalpb.Actor, 0)
	for scanner.Next() {
		key := scanner.Key()
		if !hasNamespace(key, namespaceActors) {
			continue
		}

		resp, err := x.dmap.Get(ctx, key)
		if err != nil {
			if errors.Is(err, olric.ErrKeyNotFound) {
				continue
			}
			return nil, err
		}

		value, err := resp.Byte()
		if err != nil {
			return nil, err
		}

		actor, err := decode(value)
		if err != nil {
			return nil, err
		}
		actors = append(actors, actor)
	}

	return actors, nil
}

// PutGrain stores the provided grain metadata and refreshes the peer state.
func (x *cluster) PutGrain(ctx context.Context, grain *internalpb.Grain) error {
	if !x.running.Load() {
		return ErrEngineNotRunning
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	grainID := grain.GetGrainId()
	if grainID == nil {
		return fmt.Errorf("grain id is not set")
	}

	encoded, err := encodeGrain(grain)
	if err != nil {
		return err
	}

	key := grainID.GetValue()
	if key == "" {
		return fmt.Errorf("grain id value is empty")
	}

	return x.putRecord(ctx, namespaceGrains, key, encoded)
}

// GetGrain loads a grain by identity from the unified map.
func (x *cluster) GetGrain(ctx context.Context, identity string) (*internalpb.Grain, error) {
	if !x.running.Load() {
		return nil, ErrEngineNotRunning
	}

	x.mu.RLock()
	defer x.mu.RUnlock()

	value, err := x.getRecord(ctx, namespaceGrains, identity)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			return nil, ErrGrainNotFound
		}
		return nil, err
	}
	return decodeGrain(value)
}

// GrainExists reports whether grain metadata is present for the given
// identity.
func (x *cluster) GrainExists(ctx context.Context, identity string) (bool, error) {
	if !x.running.Load() {
		return false, ErrEngineNotRunning
	}

	x.mu.RLock()
	defer x.mu.RUnlock()

	_, err := x.getRecord(ctx, namespaceGrains, identity)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RemoveGrain deletes grain metadata from the unified map and local cache.
func (x *cluster) RemoveGrain(ctx context.Context, identity string) error {
	if !x.running.Load() {
		return ErrEngineNotRunning
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	return x.deleteRecord(ctx, namespaceGrains, identity)
}

// Grains scans the map and returns all registered grains.
func (x *cluster) Grains(ctx context.Context, timeout time.Duration) ([]*internalpb.Grain, error) {
	if !x.running.Load() {
		return nil, ErrEngineNotRunning
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	x.mu.RLock()
	defer x.mu.RUnlock()

	scanner, err := x.dmap.Scan(ctx)
	if err != nil {
		return nil, err
	}
	defer scanner.Close()

	grains := make([]*internalpb.Grain, 0)
	for scanner.Next() {
		key := scanner.Key()
		if !hasNamespace(key, namespaceGrains) {
			continue
		}

		resp, err := x.dmap.Get(ctx, key)
		if err != nil {
			if errors.Is(err, olric.ErrKeyNotFound) {
				continue
			}
			return nil, err
		}

		value, err := resp.Byte()
		if err != nil {
			return nil, err
		}

		grain, err := decodeGrain(value)
		if err != nil {
			return nil, err
		}
		grains = append(grains, grain)
	}

	return grains, nil
}

// LookupKind fetches the value registered for the provided actor kind.
func (x *cluster) LookupKind(ctx context.Context, kind string) (string, error) {
	if !x.running.Load() {
		return "", ErrEngineNotRunning
	}

	x.mu.RLock()
	defer x.mu.RUnlock()

	value, err := x.getRecord(ctx, namespaceKinds, kind)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			return "", nil
		}
		return "", err
	}

	return string(value), nil
}

// PutKind stores the provided actor kind mapping in the cluster state.
func (x *cluster) PutKind(ctx context.Context, kind string) error {
	if !x.running.Load() {
		return ErrEngineNotRunning
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	return x.putRecord(ctx, namespaceKinds, kind, []byte(kind))
}

// RemoveKind deletes an actor kind mapping from the cluster state.
func (x *cluster) RemoveKind(ctx context.Context, kind string) error {
	if !x.running.Load() {
		return ErrEngineNotRunning
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	if err := x.deleteRecord(ctx, namespaceKinds, kind); err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			return nil
		}
		return err
	}
	return nil
}

// GetState returns the peer state tracked for the requested address.
func (x *cluster) GetState(ctx context.Context, peerAddress string) (*internalpb.PeerState, error) {
	if !x.running.Load() {
		return nil, ErrEngineNotRunning
	}

	x.mu.RLock()
	defer x.mu.RUnlock()

	peerState, ok := x.store.GetPeerState(ctx, peerAddress)
	if !ok {
		return nil, ErrPeerSyncNotFound
	}

	return peerState, nil
}

// DeleteState removes any stored state for the given peer address.
func (x *cluster) DeleteState(ctx context.Context, peerAddress string) error {
	if !x.running.Load() {
		return ErrEngineNotRunning
	}

	x.mu.RLock()
	defer x.mu.RUnlock()

	return x.store.DeletePeerState(ctx, peerAddress)
}

// Events returns the stream of cluster membership events consumed from the
// underlying pub-sub channel.
func (x *cluster) Events() <-chan *Event {
	return x.events
}

// Peers lists known members of the cluster excluding the local node.
func (x *cluster) Peers(ctx context.Context) ([]*Peer, error) {
	if !x.running.Load() {
		return nil, ErrEngineNotRunning
	}

	x.mu.RLock()
	defer x.mu.RUnlock()

	members, err := x.client.Members(ctx)
	if err != nil {
		x.logger.Errorf("failed to read cluster peers: %v", err)
		return nil, err
	}

	peers := make([]*Peer, 0, len(members))
	for _, member := range members {
		if member.Name == x.node.PeersAddress() {
			continue
		}
		node := new(discovery.Node)
		_ = json.Unmarshal([]byte(member.Meta), node)
		peers = append(peers, &Peer{
			Host:         node.Host,
			PeersPort:    node.PeersPort,
			Coordinator:  member.Coordinator,
			RemotingPort: node.RemotingPort,
		})
	}
	return peers, nil
}

// IsLeader reports whether the local node is the cluster coordinator.
func (x *cluster) IsLeader(ctx context.Context) bool {
	if !x.running.Load() {
		return false
	}

	x.mu.RLock()
	defer x.mu.RUnlock()

	members, err := x.client.Members(ctx)
	if err != nil {
		x.logger.Errorf("failed to fetch cluster members: %v", err)
		return false
	}

	for _, member := range members {
		node := new(discovery.Node)
		_ = json.Unmarshal([]byte(member.Meta), node)
		if node.PeersAddress() == x.node.PeersAddress() && member.Coordinator {
			return true
		}
	}
	return false
}

// GetPartition returns the partition identifier used to distribute the actor
// key in the unified map.
func (x *cluster) GetPartition(actorName string) uint64 {
	if !x.running.Load() {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), x.readTimeout)
	defer cancel()

	x.mu.RLock()
	defer x.mu.RUnlock()

	resp, err := x.dmap.Get(ctx, composeKey(namespaceActors, actorName))
	if err != nil {
		return 0
	}
	return resp.Partition()
}

// IsRunning exposes whether the cluster engine has been started.
func (x *cluster) IsRunning() bool {
	return x.running.Load()
}

// PutJobKey stores job metadata associated with the provided identifier.
func (x *cluster) PutJobKey(ctx context.Context, jobID string, metadata []byte) error {
	if !x.running.Load() {
		return ErrEngineNotRunning
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	return x.putRecord(ctx, namespaceJobs, jobID, metadata)
}

// DeleteJobKey removes a job metadata entry from the cluster state.
func (x *cluster) DeleteJobKey(ctx context.Context, jobID string) error {
	if !x.running.Load() {
		return ErrEngineNotRunning
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	return x.deleteRecord(ctx, namespaceJobs, jobID)
}

// JobKey retrieves the metadata stored for the given job identifier.
func (x *cluster) JobKey(ctx context.Context, jobID string) ([]byte, error) {
	if !x.running.Load() {
		return nil, ErrEngineNotRunning
	}

	x.mu.RLock()
	defer x.mu.RUnlock()

	return x.getRecord(ctx, namespaceJobs, jobID)
}

// PublishState shares local peer state with other members during shutdown
func (x *cluster) PublishState(ctx context.Context, actors []*internalpb.Actor, grains []*internalpb.Grain) error {
	if !x.running.Load() {
		return ErrEngineNotRunning
	}

	total := len(actors) + len(grains)
	if total == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, x.writeTimeout)
	defer cancel()

	// For small batches, process sequentially to avoid goroutine overhead
	if total <= 8 {
		return x.publishStateSequential(ctx, actors, grains)
	}

	// Use bounded parallelism for larger batches
	return x.publishStateConcurrent(ctx, actors, grains)
}

// publishStateSequential handles small batches without goroutine overhead
func (x *cluster) publishStateSequential(ctx context.Context, actors []*internalpb.Actor, grains []*internalpb.Grain) error {
	for _, actor := range actors {
		if err := x.executePublishJob(ctx, publishJob{actor: actor}); err != nil {
			return err
		}
	}

	for _, grain := range grains {
		if err := x.executePublishJob(ctx, publishJob{grain: grain}); err != nil {
			return err
		}
	}

	return nil
}

// executePublishJob handles the common publish logic with context awareness.
func (x *cluster) executePublishJob(ctx context.Context, job publishJob) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	var err error
	switch {
	case job.actor != nil:
		err = x.publishStateActor(ctx, job.actor)
	case job.grain != nil:
		err = x.publishStateGrain(ctx, job.grain)
	default:
		return nil
	}

	if err != nil {
		x.logger.Errorf("failed to publish state: %v", err)
	}

	return err
}

// publishStateConcurrent handles larger batches with bounded parallelism
func (x *cluster) publishStateConcurrent(ctx context.Context, actors []*internalpb.Actor, grains []*internalpb.Grain) error {
	total := len(actors) + len(grains)
	if total == 0 {
		return nil
	}

	// Cap concurrency to avoid saturating the runtime while still keeping throughput
	perBatchWorkers := min(max(total/4, 1), 8)
	workerLimit := min(max(runtime.NumCPU(), 2), perBatchWorkers)

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(workerLimit)

	for _, actor := range actors {
		actor := actor
		eg.Go(func() error {
			return x.executePublishJob(egCtx, publishJob{actor: actor})
		})
	}

	for _, grain := range grains {
		grain := grain
		eg.Go(func() error {
			return x.executePublishJob(egCtx, publishJob{grain: grain})
		})
	}

	return eg.Wait()
}

// publishJob represents a single publish task
type publishJob struct {
	actor *internalpb.Actor
	grain *internalpb.Grain
}

// buildConfig creates the Olric configuration tailored to the current
// cluster settings.
func (x *cluster) buildConfig() (*oconfig.Config, error) {
	logLevel := "INFO"
	switch x.logger.LogLevel() {
	case log.DebugLevel:
		logLevel = "DEBUG"
	case log.ErrorLevel, log.FatalLevel, log.PanicLevel:
		logLevel = "ERROR"
	case log.WarningLevel:
		logLevel = "WARN"
	default:
	}

	jsonbytes, _ := json.Marshal(x.node)
	meta := string(jsonbytes)

	options := storage.NewConfig(nil)
	options.Add("tableSize", x.tableSize)

	cfg := &oconfig.Config{
		BindAddr:          x.node.Host,
		BindPort:          x.node.PeersPort,
		ReadRepair:        true,
		ReplicaCount:      int(x.replicaCount),
		WriteQuorum:       int(x.writeQuorum),
		ReadQuorum:        int(x.readQuorum),
		MemberCountQuorum: int32(x.minimumPeersQuorum),
		Peers:             []string{},
		DMaps: &oconfig.DMaps{
			Engine: &oconfig.Engine{
				Config: options.ToMap(),
			},
		},
		KeepAlivePeriod:            oconfig.DefaultKeepAlivePeriod,
		PartitionCount:             x.partitionsCount,
		BootstrapTimeout:           x.bootstrapTimeout,
		ReplicationMode:            oconfig.SyncReplicationMode,
		RoutingTablePushInterval:   x.routingTableInterval,
		JoinRetryInterval:          oconfig.DefaultJoinRetryInterval,
		MaxJoinAttempts:            oconfig.DefaultMaxJoinAttempts,
		LogLevel:                   logLevel,
		LogOutput:                  newLogWriter(x.logger),
		EnableClusterEventsChannel: true,
		Hasher:                     hasher.NewDefaultHasher(),
		TriggerBalancerInterval:    oconfig.DefaultTriggerBalancerInterval,
		MemberMeta:                 meta,
	}

	if x.tlsInfo != nil {
		cfg.TLS = &oconfig.TLS{
			Client: x.tlsInfo.ClientConfig,
			Server: x.tlsInfo.ServerConfig,
		}

		client := &oconfig.Client{TLS: x.tlsInfo.ClientConfig}
		if err := client.Sanitize(); err != nil {
			return nil, fmt.Errorf("failed to sanitize client config: %v", err)
		}
		cfg.Client = client
	}

	if x.logger.LogLevel() == log.DebugLevel {
		cfg.LogVerbosity = oconfig.DefaultLogVerbosity
	}

	return cfg, nil
}

// setupMemberlistConfig applies memberlist specific configuration to the
// provided Olric config instance.
func (x *cluster) setupMemberlistConfig(cfg *oconfig.Config) error {
	mconfig, err := oconfig.NewMemberlistConfig("lan")
	if err != nil {
		x.logger.Errorf("failed to configure memberlist: %v", err)
		return err
	}
	mconfig.BindAddr = x.node.Host
	mconfig.BindPort = x.node.DiscoveryPort
	mconfig.AdvertisePort = x.node.DiscoveryPort
	mconfig.AdvertiseAddr = x.node.Host

	// Kubernetes-specific filtering is necessary because dynamic IP assignment can cause pods in different namespaces to share the same IP address over time.
	// This can lead to unintended cross-namespace communication within the memberlist ring.
	// To prevent this, all nodes are assigned the same label corresponding to the actor system name, enabling proper filtering.
	// As a result, even if a pod receives a gossip message from a reused IP now belonging to a different namespace,
	// the message will be rejected if it lacks the expected label identifying it as part of the correct ring.
	mconfig.Label = fmt.Sprintf("prefix-%s", strings.ToLower(x.name))

	if x.tlsInfo != nil {
		transport, err := memberlist.NewTransport(memberlist.TransportConfig{
			BindAddrs:          []string{x.node.Host},
			BindPort:           x.node.DiscoveryPort,
			PacketDialTimeout:  5 * time.Second,
			PacketWriteTimeout: 5 * time.Second,
			Logger:             x.logger,
			DebugEnabled:       false,
			TLSEnabled:         true,
			TLS:                x.tlsInfo.ClientConfig,
		})
		if err != nil {
			x.logger.Errorf("failed to create memberlist transport: %v", err)
			return err
		}

		mconfig.Transport = transport
		mconfig.UDPBufferSize = 10 * 1024 * 1024
		mconfig.ProbeInterval = 5 * time.Second
		mconfig.ProbeTimeout = 2 * time.Second
		mconfig.DisableTcpPings = true
	}
	cfg.MemberlistConfig = mconfig
	return nil
}

// configureDiscovery injects the discovery provider wrapper into the Olric
// configuration.
func (x *cluster) configureDiscovery(conf *oconfig.Config) {
	discoveryWrapper := &discoveryProvider{
		provider: x.discoveryProvider,
		log:      x.logger.StdLogger(),
	}
	conf.ServiceDiscovery = map[string]any{
		"plugin": discoveryWrapper,
		"id":     x.discoveryProvider.ID(),
	}
}

// startServer launches the embedded Olric server and waits for it to become
// ready or return an error.
func (x *cluster) startServer(startCtx, ctx context.Context) error {
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
	case err := <-errCh:
		if err != nil {
			return err
		}
	}
	return nil
}

// createDMap provisions the unified map used to store cluster records.
func (x *cluster) createDMap() error {
	dmap, err := x.client.NewDMap(dMapName)
	if err != nil {
		return err
	}
	x.dmap = dmap
	return nil
}

// createSubscription attaches to the cluster events channel and prepares the
// consumer goroutine.
func (x *cluster) createSubscription(ctx context.Context) error {
	ps, err := x.client.NewPubSub(olric.ToAddress(x.node.PeersAddress()))
	if err != nil {
		return err
	}
	x.publisher = ps
	x.subscriber = ps.Subscribe(ctx, events.ClusterEventsChannel, peerStatesActorsTopic, peerStatesGrainsTopic)
	x.messages = x.subscriber.Channel()
	return nil
}

func (x *cluster) publishStateActor(ctx context.Context, actor *internalpb.Actor) error {
	stateActor := &internalpb.StateActor{
		Actor:        actor,
		Host:         x.node.Host,
		RemotingPort: int32(x.node.RemotingPort),
		PeersPort:    int32(x.node.PeersPort),
	}

	bytea, _ := protojson.Marshal(stateActor)
	_, err := x.publisher.Publish(ctx, peerStatesActorsTopic, string(bytea))
	return err
}

func (x *cluster) publishStateGrain(ctx context.Context, grain *internalpb.Grain) error {
	stateActor := &internalpb.StateGrain{
		Grain:        grain,
		Host:         x.node.Host,
		RemotingPort: int32(x.node.RemotingPort),
		PeersPort:    int32(x.node.PeersPort),
	}

	bytea, _ := protojson.Marshal(stateActor)
	_, err := x.publisher.Publish(ctx, peerStatesGrainsTopic, string(bytea))
	return err
}

// consume listens for cluster membership and peer-state events and delegates handling.
func (x *cluster) consume() {
	for message := range x.messages {
		switch message.Channel {
		case peerStatesActorsTopic:
			if err := x.handleActorStateMsg(message.Payload); err != nil {
				x.logger.Errorf("actor state handling error: %v", err)
			}
		case peerStatesGrainsTopic:
			if err := x.handleGrainStateMsg(message.Payload); err != nil {
				x.logger.Errorf("grain state handling error: %v", err)
			}
		case events.ClusterEventsChannel:
			if err := x.handleClusterEventMsg(message.Payload); err != nil {
				x.logger.Errorf("cluster event handling error: %v", err)
			}
		default:
			// ignore
		}
	}
}

// handleActorStateMsg updates peer actor state from a pub-sub payload.
func (x *cluster) handleActorStateMsg(payload string) error {
	x.peersStateLock.Lock()
	defer x.peersStateLock.Unlock()

	ctx := context.Background()

	state := new(internalpb.StateActor)
	if err := protojson.Unmarshal([]byte(payload), state); err != nil {
		return fmt.Errorf("unmarshal state actor: %w", err)
	}

	peerAddress := net.JoinHostPort(state.GetHost(), strconv.Itoa(int(state.GetPeersPort())))
	// ignore self
	if x.node.PeersAddress() == peerAddress {
		return nil
	}

	x.logger.Infof("(%s) processing peer=(%s)'s state", x.node.PeersAddress(), peerAddress)
	peerState := x.getOrInitPeerState(ctx, state.GetHost(), state.GetRemotingPort(), state.GetPeersPort())
	actors := peerState.GetActors()
	if actors == nil {
		actors = map[string]*internalpb.Actor{}
	}

	key := state.GetActor().GetAddress().GetName()
	actors[key] = state.GetActor()
	peerState.Actors = actors

	x.logger.Debugf("(%s) persisting peer=(%s)'s state: [Actors count=(%d), Grains count=(%d)]", x.node.PeersAddress(), peerAddress, len(peerState.GetActors()), len(peerState.GetGrains()))
	if err := x.store.PersistPeerState(ctx, peerState); err != nil {
		return fmt.Errorf("persist peer state (actors) [%s]: %w", peerAddress, err)
	}

	x.logger.Infof("(%s) processed peer=(%s) successfully ", x.node.PeersAddress(), peerAddress)
	return nil
}

// handleGrainStateMsg updates peer grain state from a pub-sub payload.
func (x *cluster) handleGrainStateMsg(payload string) error {
	x.peersStateLock.Lock()
	defer x.peersStateLock.Unlock()

	ctx := context.Background()
	state := new(internalpb.StateGrain)
	if err := protojson.Unmarshal([]byte(payload), state); err != nil {
		return fmt.Errorf("unmarshal state grain: %w", err)
	}

	peerAddress := net.JoinHostPort(state.GetHost(), strconv.Itoa(int(state.GetPeersPort())))
	// ignore self
	if x.node.PeersAddress() == peerAddress {
		return nil
	}

	x.logger.Infof("(%s) processing peer=(%s)'s state", x.node.PeersAddress(), peerAddress)
	peerState := x.getOrInitPeerState(ctx, state.GetHost(), state.GetRemotingPort(), state.GetPeersPort())
	grains := peerState.GetGrains()
	if grains == nil {
		grains = map[string]*internalpb.Grain{}
	}

	key := state.GetGrain().GetGrainId().GetValue()
	grains[key] = state.GetGrain()
	peerState.Grains = grains

	x.logger.Debugf("(%s) persisting peer=(%s)'s state: [Actors count=(%d), Grains count=(%d)]", x.node.PeersAddress(), peerAddress, len(peerState.GetActors()), len(peerState.GetGrains()))
	if err := x.store.PersistPeerState(ctx, peerState); err != nil {
		return fmt.Errorf("persist peer state (grains) [%s]: %w", peerAddress, err)
	}

	x.logger.Infof("(%s) processed peer=(%s) successfully ", x.node.PeersAddress(), peerAddress)
	return nil
}

// handleClusterEventMsg decodes and dispatches cluster topology events with de-duplication.
func (x *cluster) handleClusterEventMsg(payload string) error {
	var envelope map[string]any
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return fmt.Errorf("unmarshal cluster event envelope: %w", err)
	}

	switch envelope["kind"] {
	case events.KindNodeJoinEvent:
		var ev events.NodeJoinEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return fmt.Errorf("unmarshal node join: %w", err)
		}
		x.processNodeJoin(ev)
	case events.KindNodeLeftEvent:
		var ev events.NodeLeftEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return fmt.Errorf("unmarshal node left: %w", err)
		}
		x.processNodeLeft(ev)
	default:
		// unknown or unhandled kind
	}
	return nil
}

// processNodeJoin applies de-dup and forwards a NodeJoined event.
func (x *cluster) processNodeJoin(ev events.NodeJoinEvent) {
	x.eventsLock.Lock()
	defer x.eventsLock.Unlock()

	// ignore self
	if x.node.PeersAddress() == ev.NodeJoin {
		return
	}
	// de-dup
	if x.nodeJoinedEventsFilter.Contains(ev.NodeJoin) {
		return
	}
	x.nodeJoinedEventsFilter.Add(ev.NodeJoin)

	timeMilli := ev.Timestamp / int64(1e6)
	evt := &goaktpb.NodeJoined{
		Address:   ev.NodeJoin,
		Timestamp: timestamppb.New(time.UnixMilli(timeMilli)),
	}
	payload, _ := anypb.New(evt)

	x.sendEventLocked(&Event{Payload: payload, Type: NodeJoined})
}

// processNodeLeft applies de-dup and forwards a NodeLeft event.
func (x *cluster) processNodeLeft(ev events.NodeLeftEvent) {
	x.eventsLock.Lock()
	defer x.eventsLock.Unlock()

	x.nodeJoinedEventsFilter.Remove(ev.NodeLeft)
	// de-dup
	if x.nodeLeftEventsFilter.Contains(ev.NodeLeft) {
		return
	}
	x.nodeLeftEventsFilter.Add(ev.NodeLeft)

	timeMilli := ev.Timestamp / int64(1e6)
	evt := &goaktpb.NodeLeft{
		Address:   ev.NodeLeft,
		Timestamp: timestamppb.New(time.UnixMilli(timeMilli)),
	}
	payload, _ := anypb.New(evt)

	x.sendEventLocked(&Event{Payload: payload, Type: NodeLeft})
}

// getOrInitPeerState fetches an existing peer state or initializes a new one.
func (x *cluster) getOrInitPeerState(ctx context.Context, host string, remotingPort, peersPort int32) *internalpb.PeerState {
	address := net.JoinHostPort(host, strconv.Itoa(int(peersPort)))
	if ps, ok := x.store.GetPeerState(ctx, address); ok {
		return ps
	}
	return &internalpb.PeerState{
		Host:         host,
		RemotingPort: remotingPort,
		PeersPort:    peersPort,
	}
}

// sendEventLocked pushes an event if the channel is active.
func (x *cluster) sendEventLocked(e *Event) {
	if x.events == nil {
		return
	}
	x.events <- e
}

// putRecord writes a namespaced record to the unified map applying timeouts.
func (x *cluster) putRecord(ctx context.Context, namespace recordNamespace, key string, value []byte) error {
	ctx = context.WithoutCancel(ctx)
	ctx, cancel := context.WithTimeout(ctx, x.writeTimeout)
	defer cancel()

	return x.dmap.Put(ctx, composeKey(namespace, key), value)
}

// getRecord fetches a namespaced record from the unified map.
func (x *cluster) getRecord(ctx context.Context, namespace recordNamespace, key string) ([]byte, error) {
	ctx = context.WithoutCancel(ctx)
	ctx, cancel := context.WithTimeout(ctx, x.readTimeout)
	defer cancel()

	resp, err := x.dmap.Get(ctx, composeKey(namespace, key))
	if err != nil {
		return nil, err
	}
	return resp.Byte()
}

// deleteRecord removes a namespaced record from the unified map, tolerating
// missing entries.
func (x *cluster) deleteRecord(ctx context.Context, namespace recordNamespace, key string) error {
	ctx = context.WithoutCancel(ctx)
	ctx, cancel := context.WithTimeout(ctx, x.writeTimeout)
	defer cancel()

	_, err := x.dmap.Delete(ctx, composeKey(namespace, key))
	return err
}
