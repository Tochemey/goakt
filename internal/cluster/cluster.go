// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/hash"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/locker"
	"github.com/tochemey/goakt/v4/internal/memberlist"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	gtls "github.com/tochemey/goakt/v4/tls"
)

const (
	dMapName                = "goakt.dmap"
	defaultEventsBufSize    = 256
	namespaceSeparator      = "::"
	ActorsRoundRobinKey     = "actors_rr_index"
	GrainsRoundRobinKey     = "grains_rr_index"
	rebalanceReasonNodeLeft = "node-left"
	rebalanceReasonNodeJoin = "node-join"
	// actorScanConcurrency bounds how many actor records are fetched in parallel
	// when scanning the registry (Actors, CountActorsByHost). The registry scan
	// otherwise issues one sequential network Get per actor key, which times out
	// the scan budget on large registries; fanning the Gets out keeps the scan
	// within budget while capping the burst of concurrent reads.
	actorScanConcurrency = 16
)

type recordNamespace string

const (
	namespaceActors recordNamespace = "actors"
	namespaceGrains recordNamespace = "grains"
	namespaceKinds  recordNamespace = "kinds"
	namespaceJobs   recordNamespace = "jobs"
	// namespaceScheduleFire stores the short-lived fire claims used to arbitrate which node
	// delivers a given tick of a cluster-wide cron schedule (see actor.ScheduleWithCron).
	namespaceScheduleFire recordNamespace = "schedule-fire"
)

// scheduleFireClaimValue is the placeholder payload for a schedule-fire claim entry; only the
// key's existence matters for arbitration.
var scheduleFireClaimValue = []byte{1}

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
	// ActorsByHost streams the registry and returns only the actors owned by the
	// given host:port. It filters during the scan so it never materializes the
	// whole registry to recover a single node's records.
	ActorsByHost(ctx context.Context, host string, port int, timeout time.Duration) ([]*internalpb.Actor, error)
	// CountActorsByHost returns the number of registered actors per owning node,
	// keyed by the actor address's raw "host:port". It streams the registry and
	// discards each record after counting, so its peak memory is proportional to
	// the number of distinct nodes rather than the number of actors.
	CountActorsByHost(ctx context.Context, timeout time.Duration) (map[string]int, error)
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
	// GrainsByHost streams the registry and returns only the grains owned by the
	// given host:port, filtering during the scan rather than building the full
	// grain set first.
	GrainsByHost(ctx context.Context, host string, port int, timeout time.Duration) ([]*internalpb.Grain, error)
	// LookupKind reads the value registered for the provided actor kind.
	LookupKind(ctx context.Context, kind string) (string, error)
	// PutKind registers an actor kind mapping.
	PutKind(ctx context.Context, kind string) error
	// RemoveKind deletes an actor kind mapping.
	RemoveKind(ctx context.Context, kind string) error
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
	// ClaimScheduleFire atomically claims the exclusive right to deliver one trigger tick of
	// a cluster-wide cron schedule. It returns nil for the winning caller and
	// ErrScheduleFireClaimed for every other caller racing for the same key; see the
	// builtin implementation for the key and ttl contract.
	ClaimScheduleFire(ctx context.Context, key string, ttl time.Duration) error
	// PutJobKey stores job metadata.
	PutJobKey(ctx context.Context, jobID string, metadata []byte) error
	// DeleteJobKey removes job metadata.
	DeleteJobKey(ctx context.Context, jobID string) error
	// JobKey retrieves stored job metadata.
	JobKey(ctx context.Context, jobID string) ([]byte, error)
	// Members lists all cluster members including the local node.
	Members(ctx context.Context) ([]*Peer, error)
	// NextRoundRobinValue returns the next value in a round-robin sequence for the given key.
	// The key here is either actors or grains. When the node that owns the key goes down,
	// the sequence may be reset.
	NextRoundRobinValue(ctx context.Context, key string) (int, error)
}

// cluster implements the Cluster interface backed by an Olric unified
// map and discovery provider. It embeds synchronization primitives, runtime
// configuration and caches required to manage the cluster state.
type cluster struct {
	_  locker.NoCopy
	mu sync.RWMutex

	name                    string
	discoveryProvider       discovery.Provider
	node                    *discovery.Node
	logger                  log.Logger
	hasher                  hash.Hasher
	partitionsCount         uint64
	minimumPeersQuorum      uint32
	replicaCount            uint32
	writeQuorum             uint32
	readQuorum              uint32
	tableSize               uint64
	writeTimeout            time.Duration
	readTimeout             time.Duration
	shutdownTimeout         time.Duration
	bootstrapTimeout        time.Duration
	routingTableInterval    time.Duration
	triggerBalancerInterval time.Duration
	tlsInfo                 *gtls.Info

	server *olric.Olric
	client olric.Client
	dmap   olric.DMap

	events     chan *Event
	eventsLock sync.Mutex
	subscriber *redis.PubSub
	messages   <-chan *redis.Message

	consumeCtx    context.Context
	consumeCancel context.CancelFunc
	consumeWg     sync.WaitGroup

	nodeJoinedEventsFilter   goset.Set[string]
	nodeLeftEventsFilter     goset.Set[string]
	nodeJoinTimestamps       map[string]int64
	nodeLeftTimestamps       map[string]int64
	rebalanceJoinNodeEpochs  map[string]uint64
	rebalanceLeftNodeEpochs  map[string]uint64
	rebalanceJoinLatestEpoch uint64
	rebalanceLeftLatestEpoch uint64
	rebalanceStartSeen       map[uint64]struct{}
	rebalanceCompleteSeen    map[uint64]struct{}

	// lastCoordinatorAddr caches the coordinator's peers address to detect
	// leadership changes. It is seeded (non-empty) in Start before the consume
	// goroutine launches and thereafter touched only while holding eventsLock, so
	// it never races. Only changes away from this baseline emit LeaderChanged.
	lastCoordinatorAddr string

	running *atomic.Bool
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
		name:                    name,
		discoveryProvider:       disco,
		node:                    node,
		logger:                  config.logger,
		hasher:                  config.shardHasher,
		partitionsCount:         config.shardCount,
		minimumPeersQuorum:      config.minimumMembersQuorum,
		replicaCount:            config.replicasCount,
		writeQuorum:             config.membersWriteQuorum,
		readQuorum:              config.membersReadQuorum,
		tableSize:               config.tableSize,
		writeTimeout:            config.writeTimeout,
		readTimeout:             config.readTimeout,
		shutdownTimeout:         config.shutdownTimeout,
		bootstrapTimeout:        config.bootstrapTimeout,
		routingTableInterval:    config.routingTableInterval,
		triggerBalancerInterval: config.triggerBalancerInterval,
		tlsInfo:                 config.tlsInfo,
		events:                  make(chan *Event, defaultEventsBufSize),
		nodeJoinedEventsFilter:  goset.NewSet[string](),
		nodeLeftEventsFilter:    goset.NewSet[string](),
		nodeJoinTimestamps:      make(map[string]int64),
		nodeLeftTimestamps:      make(map[string]int64),
		rebalanceJoinNodeEpochs: make(map[string]uint64),
		rebalanceLeftNodeEpochs: make(map[string]uint64),
		rebalanceStartSeen:      make(map[uint64]struct{}),
		rebalanceCompleteSeen:   make(map[uint64]struct{}),
		running:                 atomic.NewBool(false),
	}
}

// Start initializes the cluster engine, configures the underlying Olric
// instance, and begins consuming cluster events.
func (x *cluster) Start(ctx context.Context) error {
	if x.running.Load() {
		return nil
	}

	conf, err := x.buildConfig()
	if err != nil {
		x.logger.Errorf("failed to build engine config: %v (hint: check Olric bind addresses, discovery config)", err)
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
		x.logger.Error(fmt.Errorf("failed to start cluster engine: %w (hint: check discovery peers, network connectivity, firewall)", err))
		return err
	}

	x.server = cache
	if err := x.startServer(startCtx, ctx); err != nil {
		x.logger.Error(fmt.Errorf("failed to start cluster engine: %w (hint: check discovery peers, network connectivity, firewall)", err))
		return err
	}

	x.client = x.server.NewEmbeddedClient()
	if err := x.createDMap(); err != nil {
		x.logger.Error(fmt.Errorf("failed to create cluster data map: %w (hint: check cluster config, permissions)", err))
		se := x.server.Shutdown(ctx)
		return errors.Join(err, se)
	}

	if err := x.createSubscription(ctx); err != nil {
		x.logger.Error(fmt.Errorf("failed to create cluster subscription: %w (hint: check cluster config, permissions)", err))
		se := x.server.Shutdown(ctx)
		return errors.Join(err, se)
	}

	x.running.Store(true)

	// Seed the leadership baseline silently. The engine has synced membership by
	// the time it reaches this point, so a running cluster always has a visible
	// coordinator (at minimum the local node). This runs before the consume
	// goroutine launches, so lastCoordinatorAddr is not written concurrently, and
	// only later changes are emitted as LeaderChanged.
	x.lastCoordinatorAddr = x.coordinatorAddress(ctx)

	x.consumeCtx, x.consumeCancel = context.WithCancel(ctx)
	x.consumeWg.Go(func() {
		x.consume()
	})
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

	defer x.running.Store(false)

	// Cancel consume context first to signal consume() to stop
	if x.consumeCancel != nil {
		x.consumeCancel()
	}

	// Wait for consume to finish with timeout
	done := make(chan types.Unit)
	go func() {
		x.consumeWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		x.logger.Debugf("consume goroutine stopped")
	case <-ctx.Done():
		x.logger.Warnf("timeout waiting for consume to finish")
	}

	// Now safe to close events channel
	x.eventsLock.Lock()
	if x.events != nil {
		close(x.events)
		x.events = nil
	}
	x.eventsLock.Unlock()

	if err := x.server.Shutdown(ctx); err != nil {
		x.logger.Errorf("failed to stop cluster engine: %v (hint: check for lingering connections or blocked shutdown)", err)
		return err
	}

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

	// no need to check for nil address as it is validated during actor creation
	addr, _ := address.Parse(actor.GetAddress())
	key := addr.Name()

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
	return collectScan(ctx, x, timeout, x.scanActors, nil)
}

// CountActorsByHost scans the unified map and tallies registered actors per
// owning node, keyed by the actor address's raw "host:port".
//
// Unlike Actors it never materializes the full actor set: each record is
// decoded only to read its address and is then discarded, so peak memory is
// proportional to the number of distinct hosts (plus the bounded set of records
// in flight), not the number of actors. This is the load signal the leader uses
// to seed relocation placement, so it must stay cheap even for large registries.
func (x *cluster) CountActorsByHost(ctx context.Context, timeout time.Duration) (map[string]int, error) {
	if !x.running.Load() {
		return nil, ErrEngineNotRunning
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	x.mu.RLock()
	defer x.mu.RUnlock()

	var (
		mu     sync.Mutex
		counts = make(map[string]int)
	)
	if err := x.scanActors(ctx, func(actor *internalpb.Actor) {
		hostPort, ok := address.HostPortOf(actor.GetAddress())
		if !ok {
			return
		}
		mu.Lock()
		counts[hostPort]++
		mu.Unlock()
	}); err != nil {
		return nil, err
	}

	return counts, nil
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

// PutGrainIfAbsent stores the grain metadata only if the entry does not already exist.
// It returns ErrGrainAlreadyExists when another node has already claimed the grain.
func PutGrainIfAbsent(ctx context.Context, cl Cluster, grain *internalpb.Grain) error {
	if cl == nil {
		return errors.New("cluster is nil")
	}

	grainID := grain.GetGrainId()
	if grainID == nil {
		return fmt.Errorf("grain id is not set")
	}

	key := grainID.GetValue()
	if key == "" {
		return fmt.Errorf("grain id value is empty")
	}

	if c, ok := cl.(*cluster); ok {
		return c.putGrainIfAbsent(ctx, grain)
	}

	exists, err := cl.GrainExists(ctx, key)
	if err != nil {
		return err
	}

	if exists {
		return ErrGrainAlreadyExists
	}

	return cl.PutGrain(ctx, grain)
}

// PutKindIfAbsent registers the actor kind only if it does not already exist.
// It returns ErrKindAlreadyExists when another node has already registered the kind.
func PutKindIfAbsent(ctx context.Context, cl Cluster, kind string) error {
	if cl == nil {
		return errors.New("cluster is nil")
	}

	if strings.TrimSpace(kind) == "" {
		return fmt.Errorf("kind is empty")
	}

	// Fast path for the built-in implementation: use an atomic NX write.
	if c, ok := cl.(*cluster); ok {
		return c.putKindIfAbsent(ctx, kind)
	}

	// Best-effort fallback for other Cluster implementations.
	// Note: still subject to races since the interface doesn't expose an NX/transactional API.
	value, err := cl.LookupKind(ctx, kind)
	if err != nil {
		return err
	}

	if value == kind {
		return ErrKindAlreadyExists
	}

	return cl.PutKind(ctx, kind)
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
	return collectScan(ctx, x, timeout, x.scanGrains, nil)
}

// ActorsByHost streams the registry and returns only the actors owned by the
// given host:port (an actor address's raw endpoint). It never materializes the
// full actor set, so recovering a single crashed node's records after a
// departure does not spike memory proportional to the whole cluster registry.
func (x *cluster) ActorsByHost(ctx context.Context, host string, port int, timeout time.Duration) ([]*internalpb.Actor, error) {
	target := address.FormatHostPort(host, port)
	return collectScan(ctx, x, timeout, x.scanActors, func(actor *internalpb.Actor) bool {
		hostPort, ok := address.HostPortOf(actor.GetAddress())
		return ok && hostPort == target
	})
}

// GrainsByHost streams the registry and returns only the grains owned by the
// given host:port. Like ActorsByHost it discards non-matching records during
// the scan instead of building the full grain set first.
func (x *cluster) GrainsByHost(ctx context.Context, host string, port int, timeout time.Duration) ([]*internalpb.Grain, error) {
	port32 := int32(port) // nolint
	return collectScan(ctx, x, timeout, x.scanGrains, func(grain *internalpb.Grain) bool {
		return grain.GetHost() == host && grain.GetPort() == port32
	})
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

// Events returns the stream of cluster membership events consumed from the
// underlying pub-sub channel.
func (x *cluster) Events() <-chan *Event {
	return x.events
}

// Peers lists known members of the cluster excluding the local node.
func (x *cluster) Peers(ctx context.Context) ([]*Peer, error) {
	members, err := x.Members(ctx)
	if err != nil {
		x.logger.Errorf("failed to read cluster peers: %v (hint: check cluster connectivity)", err)
		return nil, err
	}

	peers := make([]*Peer, 0, len(members))
	for _, member := range members {
		if member.PeerAddress() == x.node.PeersAddress() {
			continue
		}
		peers = append(peers, member)
	}

	return peers, nil
}

// Members lists all cluster members including the local node.
func (x *cluster) Members(ctx context.Context) ([]*Peer, error) {
	if !x.running.Load() {
		return nil, ErrEngineNotRunning
	}

	x.mu.RLock()
	defer x.mu.RUnlock()

	members, err := x.client.Members(ctx)
	if err != nil {
		return nil, err
	}

	peers := make([]*Peer, 0, len(members))
	for _, member := range members {
		node := new(discovery.Node)
		_ = json.Unmarshal([]byte(member.Meta), node)
		roles := goset.NewSet(node.Roles...)
		peers = append(peers, &Peer{
			Host:          node.Host,
			DiscoveryPort: node.DiscoveryPort,
			PeersPort:     node.PeersPort,
			Coordinator:   member.Coordinator,
			RemotingPort:  node.RemotingPort,
			Roles:         roles.ToSlice(),
			CreatedAt:     member.Birthdate,
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
		x.logger.Errorf("failed to fetch cluster members: %v (hint: check cluster connectivity)", err)
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

// coordinatorAddress returns the peers address of the current cluster
// coordinator as reported by the engine, or empty when the membership cannot be
// read. A running cluster always has a coordinator, so this only returns empty
// on a transient fetch failure.
func (x *cluster) coordinatorAddress(ctx context.Context) string {
	x.mu.RLock()
	defer x.mu.RUnlock()

	if x.client == nil {
		return ""
	}

	members, err := x.client.Members(ctx)
	if err != nil {
		x.logger.Errorf("failed to fetch cluster members: %v (hint: check cluster connectivity)", err)
		return ""
	}

	for _, member := range members {
		if !member.Coordinator {
			continue
		}

		node := new(discovery.Node)
		_ = json.Unmarshal([]byte(member.Meta), node)
		return node.PeersAddress()
	}

	return ""
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

// NextRoundRobinValue returns the next value in a round-robin sequence for the given key.
// The key here is either actors or grains.
func (x *cluster) NextRoundRobinValue(ctx context.Context, key string) (int, error) {
	if !x.running.Load() {
		return -1, ErrEngineNotRunning
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	ctx = context.WithoutCancel(ctx)
	ctx, cancel := context.WithTimeout(ctx, x.readTimeout)
	defer cancel()

	var composedKey string
	switch key {
	case ActorsRoundRobinKey:
		composedKey = composeKey(namespaceActors, key)
	case GrainsRoundRobinKey:
		composedKey = composeKey(namespaceGrains, key)
	default:
		return -1, fmt.Errorf("invalid round-robin key: %s", key)
	}

	next, err := x.dmap.Incr(ctx, composedKey, 1)
	if err != nil {
		return -1, err
	}

	return next, nil
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

// ClaimScheduleFire attempts to claim the exclusive right to deliver one trigger tick of a
// cluster-wide cron schedule, using the same atomic put-if-absent (NX+EX) primitive that
// guarantees single grain activation across the cluster.
//
// key uniquely identifies the (schedule reference, trigger tick) pair being arbitrated: callers
// are expected to derive it from a value that is identical across every node racing for the same
// tick (e.g. the trigger's deterministic next-fire timestamp), so a fresh key is used per tick.
//
// Because the key is per tick, a caller whose claim arrives after the winner's entry has
// expired would win again and deliver a duplicate: callers must therefore never claim a tick
// older than ttl (the actor scheduler skips such stale ticks before calling this). Claim
// entries are reclaimed by their TTL alone; there is no explicit delete, so a claim outlives
// cancellations and node shutdowns by design.
//
// Returns nil for the caller that wins the race for key (it must proceed to deliver), and
// ErrScheduleFireClaimed for every other caller racing for the same key (it must skip delivery
// for that tick silently).
func (x *cluster) ClaimScheduleFire(ctx context.Context, key string, ttl time.Duration) error {
	if !x.running.Load() {
		return ErrEngineNotRunning
	}

	if key == "" {
		return errors.New("schedule fire key is empty")
	}

	// Unlike the registration-time write paths, this runs on every cron tick, so it takes the
	// read lock only (same as RemoveActor): atomicity comes from the server-side NX+EX write,
	// not from the local mutex, and holding the write lock across a network round trip per
	// tick would stall actor placement and lookups behind schedule arbitration.
	x.mu.RLock()
	defer x.mu.RUnlock()

	err := x.putRecordIfAbsent(ctx, namespaceScheduleFire, key, scheduleFireClaimValue, olric.EX(ttl))
	if err != nil {
		if errors.Is(err, olric.ErrKeyFound) {
			return ErrScheduleFireClaimed
		}

		return err
	}

	return nil
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

	config := &oconfig.Config{
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
		TriggerBalancerInterval:    x.triggerBalancerInterval, // keep rebalance completion timely for stable event emission
		MemberMeta:                 meta,
		EnableProactiveSyncOnJoin:  true,
	}

	// by default, disable redis-client logging
	config.Client = &oconfig.Client{
		DisableRedisLogging: true,
	}

	if x.logger.LogLevel() == log.DebugLevel {
		config.LogVerbosity = oconfig.DefaultLogVerbosity
		config.Client.DisableRedisLogging = false
	}

	if x.tlsInfo != nil {
		config.TLS = &oconfig.TLS{
			Client: x.tlsInfo.ClientConfig,
			Server: x.tlsInfo.ServerConfig,
		}

		config.Client.TLS = x.tlsInfo.ClientConfig
	}

	return config, config.Client.Sanitize()
}

// setupMemberlistConfig applies memberlist specific configuration to the
// provided Olric config instance.
func (x *cluster) setupMemberlistConfig(cfg *oconfig.Config) error {
	mconfig, err := oconfig.NewMemberlistConfig(oconfig.MemberlistEnvLAN)
	if err != nil {
		x.logger.Errorf("failed to configure memberlist: %v (hint: check bind address, discovery port)", err)
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
			TLSEnabled:         true,
			TLS:                x.tlsInfo.ClientConfig,
		})
		if err != nil {
			x.logger.Errorf("failed to create memberlist transport: %v (hint: check TLS config, bind port)", err)
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

	ctx, cancel := context.WithTimeout(ctx, x.bootstrapTimeout)
	defer cancel()
	return x.server.WaitForInitialSync(ctx)
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
	x.subscriber = ps.Subscribe(ctx, events.ClusterEventsChannel)
	x.messages = x.subscriber.Channel()
	return nil
}

// consume listens for cluster membership and peer-state events and delegates handling.
func (x *cluster) consume() {
	for {
		select {
		case message, ok := <-x.messages:
			if !ok {
				x.logger.Debugf("messages channel closed, exiting consume")
				return
			}
			switch message.Channel {
			case events.ClusterEventsChannel:
				if err := x.handleClusterEvent(message.Payload); err != nil {
					x.logger.Errorf("cluster event handling error: %v (hint: check event payload format)", err)
				}
			default:
				// ignore
			}
		case <-x.consumeCtx.Done():
			x.logger.Debugf("consume context cancelled, exiting")
			return
		}
	}
}

// handleClusterEvent decodes and dispatches cluster topology events with de-duplication.
func (x *cluster) handleClusterEvent(payload string) error {
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
		x.trackNodeJoinEvent(ev)
	case events.KindNodeLeftEvent:
		var ev events.NodeLeftEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return fmt.Errorf("unmarshal node left: %w", err)
		}
		x.trackNodeLeftEvent(ev)
	case events.KindRebalanceStartEvent:
		var ev events.RebalanceStartEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return fmt.Errorf("unmarshal rebalance start: %w", err)
		}
		x.processRebalanceStart(ev)
	case events.KindRebalanceCompleteEvent:
		var ev events.RebalanceCompleteEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return fmt.Errorf("unmarshal rebalance complete: %w", err)
		}
		x.processRebalanceComplete(ev)
	default:
		// unknown or unhandled kind
	}
	return nil
}

// trackNodeJoinEvent records node-join metadata and defers NodeJoined emission until
// the matching rebalance epoch completes so cluster events reflect a stable topology.
func (x *cluster) trackNodeJoinEvent(ev events.NodeJoinEvent) {
	x.eventsLock.Lock()
	defer x.eventsLock.Unlock()

	// ignore self
	if x.node.PeersAddress() == ev.NodeJoin {
		return
	}
	if x.nodeJoinedEventsFilter.Contains(ev.NodeJoin) {
		return
	}
	if _, exists := x.nodeJoinTimestamps[ev.NodeJoin]; exists {
		return
	}
	x.nodeJoinTimestamps[ev.NodeJoin] = ev.Timestamp

	if x.rebalanceJoinLatestEpoch != 0 {
		x.rebalanceJoinNodeEpochs[ev.NodeJoin] = x.rebalanceJoinLatestEpoch
		if _, complete := x.rebalanceCompleteSeen[x.rebalanceJoinLatestEpoch]; complete {
			x.emitPendingJoinForEpochLocked(x.rebalanceJoinLatestEpoch)
		}
	}
}

// trackNodeLeftEvent records node-left metadata and defers NodeLeft emission until
// the matching rebalance epoch completes, keeping relocation aligned with routing
// table convergence while preserving the original node-left timestamp.
func (x *cluster) trackNodeLeftEvent(ev events.NodeLeftEvent) {
	x.eventsLock.Lock()
	defer x.eventsLock.Unlock()

	x.nodeJoinedEventsFilter.Remove(ev.NodeLeft)
	if x.nodeLeftEventsFilter.Contains(ev.NodeLeft) {
		return
	}
	if _, exists := x.nodeLeftTimestamps[ev.NodeLeft]; exists {
		return
	}
	x.nodeLeftTimestamps[ev.NodeLeft] = ev.Timestamp

	if x.rebalanceLeftLatestEpoch != 0 {
		x.rebalanceLeftNodeEpochs[ev.NodeLeft] = x.rebalanceLeftLatestEpoch
		if _, complete := x.rebalanceCompleteSeen[x.rebalanceLeftLatestEpoch]; complete {
			x.emitPendingLeftForEpochLocked(x.rebalanceLeftLatestEpoch)
		}
	}
}

// processRebalanceStart records rebalance epochs tied to join/leave triggers.
func (x *cluster) processRebalanceStart(ev events.RebalanceStartEvent) {
	if ev.Reason != rebalanceReasonNodeLeft && ev.Reason != rebalanceReasonNodeJoin {
		return
	}
	if ev.Reason == rebalanceReasonNodeJoin && ev.Node == x.node.PeersAddress() {
		return
	}

	x.eventsLock.Lock()
	defer x.eventsLock.Unlock()

	if _, seen := x.rebalanceStartSeen[ev.Epoch]; seen {
		return
	}
	x.rebalanceStartSeen[ev.Epoch] = struct{}{}

	switch ev.Reason {
	case rebalanceReasonNodeLeft:
		x.rebalanceLeftLatestEpoch = ev.Epoch
		x.assignLeftEpochLocked(ev.Epoch)
		if _, complete := x.rebalanceCompleteSeen[ev.Epoch]; complete {
			x.emitPendingLeftForEpochLocked(ev.Epoch)
		}
	case rebalanceReasonNodeJoin:
		x.rebalanceJoinLatestEpoch = ev.Epoch
		x.assignJoinEpochLocked(ev.Epoch)
		if _, complete := x.rebalanceCompleteSeen[ev.Epoch]; complete {
			x.emitPendingJoinForEpochLocked(ev.Epoch)
		}
	}
}

// processRebalanceComplete emits pending NodeLeft and NodeJoined events when the rebalance epoch completes.
func (x *cluster) processRebalanceComplete(ev events.RebalanceCompleteEvent) {
	x.eventsLock.Lock()
	defer x.eventsLock.Unlock()

	if _, seen := x.rebalanceCompleteSeen[ev.Epoch]; seen {
		return
	}
	x.rebalanceCompleteSeen[ev.Epoch] = struct{}{}

	x.emitPendingLeftForEpochLocked(ev.Epoch)
	x.emitPendingJoinForEpochLocked(ev.Epoch)

	// Reconcile leadership once the membership has settled for this epoch. This
	// no-ops when the coordinator is unchanged, so join-only rebalances cost a
	// single local membership read.
	x.detectLeaderChangeLocked()
}

// assignJoinEpochLocked maps pending joins to the latest rebalance epoch since newer epochs
// supersede earlier ones and act as the stable barrier for event emission.
func (x *cluster) assignJoinEpochLocked(epoch uint64) {
	for node := range x.nodeJoinTimestamps {
		x.rebalanceJoinNodeEpochs[node] = epoch
	}
}

// assignLeftEpochLocked maps pending leaves to the latest rebalance epoch to avoid
// emitting on superseded epochs.
func (x *cluster) assignLeftEpochLocked(epoch uint64) {
	for node := range x.nodeLeftTimestamps {
		x.rebalanceLeftNodeEpochs[node] = epoch
	}
}

func (x *cluster) emitPendingJoinForEpochLocked(epoch uint64) {
	for node, nodeEpoch := range x.rebalanceJoinNodeEpochs {
		if nodeEpoch != epoch {
			continue
		}
		timestamp, ok := x.nodeJoinTimestamps[node]
		if !ok {
			delete(x.rebalanceJoinNodeEpochs, node)
			continue
		}
		x.emitNodeJoinedLocked(node, timestamp)
		delete(x.nodeJoinTimestamps, node)
		delete(x.rebalanceJoinNodeEpochs, node)
	}
}

func (x *cluster) emitPendingLeftForEpochLocked(epoch uint64) {
	for node, nodeEpoch := range x.rebalanceLeftNodeEpochs {
		if nodeEpoch != epoch {
			continue
		}
		timestamp, ok := x.nodeLeftTimestamps[node]
		if !ok {
			delete(x.rebalanceLeftNodeEpochs, node)
			continue
		}
		x.emitNodeLeftLocked(node, timestamp)
		delete(x.nodeLeftTimestamps, node)
		delete(x.rebalanceLeftNodeEpochs, node)
	}
}

// detectLeaderChangeLocked emits a LeaderChanged event when the cluster
// coordinator differs from the last observed one. A transient membership-fetch
// failure returns an empty address, which is ignored so the baseline is
// preserved. It must be called while holding eventsLock.
func (x *cluster) detectLeaderChangeLocked() {
	ctx, cancel := context.WithTimeout(context.Background(), x.readTimeout)
	defer cancel()

	coordinator := x.coordinatorAddress(ctx)
	if coordinator == "" || coordinator == x.lastCoordinatorAddr {
		return
	}

	x.lastCoordinatorAddr = coordinator
	x.sendEventLocked(&Event{
		Payload: &LeaderChangedEvent{
			Address:   coordinator,
			Timestamp: time.Now().UTC(),
		},
		Type: LeaderChanged,
	})
}

func (x *cluster) emitNodeLeftLocked(node string, timestamp int64) {
	x.nodeJoinedEventsFilter.Remove(node)
	if x.nodeLeftEventsFilter.Contains(node) {
		return
	}
	x.nodeLeftEventsFilter.Add(node)

	timeMilli := timestamp / int64(time.Millisecond)
	evt := &NodeLeftEvent{
		Address:   node,
		Timestamp: time.UnixMilli(timeMilli),
	}

	x.sendEventLocked(&Event{Payload: evt, Type: NodeLeft})
}

func (x *cluster) emitNodeJoinedLocked(node string, timestamp int64) {
	if x.nodeJoinedEventsFilter.Contains(node) {
		return
	}
	x.nodeJoinedEventsFilter.Add(node)

	timeMilli := timestamp / int64(time.Millisecond)
	evt := &NodeJoinedEvent{
		Address:   node,
		Timestamp: time.UnixMilli(timeMilli),
	}

	x.sendEventLocked(&Event{Payload: evt, Type: NodeJoined})
}

// sendEventLocked pushes an event if the channel is active, using non-blocking send.
func (x *cluster) sendEventLocked(e *Event) {
	if x.events == nil {
		return
	}
	select {
	case x.events <- e:
		// Successfully sent
	default:
		// Channel full - log warning but don't block
		x.logger.Warnf("cluster event channel full, dropping event type=%s", e.Type)
	}
}

// putRecord writes a namespaced record to the unified map applying timeouts.
func (x *cluster) putRecord(ctx context.Context, namespace recordNamespace, key string, value []byte) error {
	ctx = context.WithoutCancel(ctx)
	ctx, cancel := context.WithTimeout(ctx, x.writeTimeout)
	defer cancel()

	return x.dmap.Put(ctx, composeKey(namespace, key), value)
}

func (x *cluster) putGrainIfAbsent(ctx context.Context, grain *internalpb.Grain) error {
	if !x.running.Load() {
		return ErrEngineNotRunning
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	key := grain.GetGrainId().GetValue()
	if key == "" {
		return fmt.Errorf("grain id value is empty")
	}

	encoded, err := encodeGrain(grain)
	if err != nil {
		return err
	}

	if err := x.putRecordIfAbsent(ctx, namespaceGrains, key, encoded); err != nil {
		if errors.Is(err, olric.ErrKeyFound) {
			return ErrGrainAlreadyExists
		}
		return err
	}

	return nil
}

func (x *cluster) putRecordIfAbsent(ctx context.Context, namespace recordNamespace, key string, value []byte, options ...olric.PutOption) error {
	ctx = context.WithoutCancel(ctx)
	ctx, cancel := context.WithTimeout(ctx, x.writeTimeout)
	defer cancel()

	return x.dmap.Put(ctx, composeKey(namespace, key), value, append([]olric.PutOption{olric.NX()}, options...)...)
}

func (x *cluster) putKindIfAbsent(ctx context.Context, kind string) error {
	if !x.running.Load() {
		return ErrEngineNotRunning
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	if err := x.putRecordIfAbsent(ctx, namespaceKinds, kind, []byte(kind)); err != nil {
		if errors.Is(err, olric.ErrKeyFound) {
			return ErrKindAlreadyExists
		}
		return err
	}
	return nil
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

// scanActors streams every registered actor record and invokes visit for each,
// decoded exactly once. It is the shared scan used by Actors, ActorsByHost and
// CountActorsByHost so all agree on which keys are actor metadata. See
// scanNamespace for the concurrency contract on visit.
//
// Callers must hold x.mu (read lock) for the duration of the scan.
func (x *cluster) scanActors(ctx context.Context, visit func(*internalpb.Actor)) error {
	return scanNamespace(ctx, x, namespaceActors, ActorsRoundRobinKey, decode, visit)
}

// scanGrains streams every registered grain record and invokes visit for each,
// decoded exactly once. It is the grain counterpart of scanActors; see
// scanNamespace for the concurrency contract on visit.
//
// Callers must hold x.mu (read lock) for the duration of the scan.
func (x *cluster) scanGrains(ctx context.Context, visit func(*internalpb.Grain)) error {
	return scanNamespace(ctx, x, namespaceGrains, GrainsRoundRobinKey, decodeGrain, visit)
}

// scanNamespace streams every record in the given namespace and invokes visit
// for each, decoded exactly once. It is the single scan protocol behind the
// actor and grain registry scans, so both agree on which keys are record
// metadata: foreign namespaces and the namespace's round-robin counter entry
// (rrName), which is not record metadata, are skipped.
//
// The registry can hold thousands of entries, so the per-key Gets are fanned out
// with bounded concurrency (actorScanConcurrency) instead of issued one at a
// time: a sequential scan otherwise blows the caller's timeout budget on large
// registries. Keys are collected up front so the scanner is released before the
// Gets run. visit may be called from multiple goroutines concurrently, so
// callers must synchronize any shared state they mutate; the callback order is
// unspecified.
//
// Callers must hold x.mu (read lock) for the duration of the scan.
func scanNamespace[T any](ctx context.Context, x *cluster, namespace recordNamespace, rrName string, decodeRecord func([]byte) (T, error), visit func(T)) error {
	scanner, err := x.dmap.Scan(ctx)
	if err != nil {
		return err
	}

	rrKey := composeKey(namespace, rrName)
	keys := make([]string, 0)

	for scanner.Next() {
		key := scanner.Key()
		if !hasNamespace(key, namespace) || key == rrKey {
			continue
		}
		keys = append(keys, key)
	}

	scanner.Close()

	eg, egctx := errgroup.WithContext(ctx)
	eg.SetLimit(actorScanConcurrency)

	for _, key := range keys {
		eg.Go(func() error {
			resp, err := x.dmap.Get(egctx, key)
			if err != nil {
				if errors.Is(err, olric.ErrKeyNotFound) {
					// the entry was removed between the scan and this Get; skip it
					return nil
				}
				return err
			}

			value, err := resp.Byte()
			if err != nil {
				return err
			}

			record, err := decodeRecord(value)
			if err != nil {
				return err
			}

			visit(record)
			return nil
		})
	}

	return eg.Wait()
}

// collectScan runs scan under the cluster's shared read guards (engine-running
// check, caller timeout, membership read lock) and collects every record the
// keep filter accepts; a nil keep collects everything. It is the shared shell
// of the slice-returning registry scans (Actors, ActorsByHost, Grains,
// GrainsByHost) so the guard-and-collect protocol lives in one place.
func collectScan[T any](ctx context.Context, x *cluster, timeout time.Duration, scan func(context.Context, func(T)) error, keep func(T) bool) ([]T, error) {
	if !x.running.Load() {
		return nil, ErrEngineNotRunning
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	x.mu.RLock()
	defer x.mu.RUnlock()

	var (
		mu  sync.Mutex
		out = make([]T, 0)
	)

	if err := scan(ctx, func(record T) {
		if keep != nil && !keep(record) {
			return
		}

		mu.Lock()
		out = append(out, record)
		mu.Unlock()
	}); err != nil {
		return nil, err
	}

	return out, nil
}
