/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to do so, subject to the following conditions:
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
	"os"
	"strings"
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
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/locker"
	"github.com/tochemey/goakt/v3/internal/memberlist"
	"github.com/tochemey/goakt/v3/internal/size"
	"github.com/tochemey/goakt/v3/log"
	gtls "github.com/tochemey/goakt/v3/tls"
)

const (
	unifiedMapName       = "cluster:data"
	defaultEventsBufSize = 256
)

type recordNamespace string

const (
	namespaceActor     recordNamespace = "actor"
	namespaceGrain     recordNamespace = "grain"
	namespacePeerState recordNamespace = "peer"
	namespaceKind      recordNamespace = "kind"
	namespaceJob       recordNamespace = "job"
)

func composeKey(namespace recordNamespace, id string) string {
	return string(namespace) + ":" + id
}

func hasNamespace(key string, namespace recordNamespace) bool {
	prefix := string(namespace) + ":"
	return strings.HasPrefix(key, prefix)
}

// Cluster captures the behaviour exposed by a cluster engine implementation backed by the unified map.
type Cluster interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	PutActor(ctx context.Context, actor *internalpb.Actor) error
	GetActor(ctx context.Context, actorName string) (*internalpb.Actor, error)
	RemoveActor(ctx context.Context, actorName string) error
	ActorExists(ctx context.Context, actorName string) (bool, error)
	Actors(ctx context.Context, timeout time.Duration) ([]*internalpb.Actor, error)
	PutGrain(ctx context.Context, grain *internalpb.Grain) error
	GetGrain(ctx context.Context, identity string) (*internalpb.Grain, error)
	RemoveGrain(ctx context.Context, identity string) error
	GrainExists(ctx context.Context, identity string) (bool, error)
	Grains(ctx context.Context, timeout time.Duration) ([]*internalpb.Grain, error)
	LookupKind(ctx context.Context, kind string) (string, error)
	PutKind(ctx context.Context, kind string) error
	RemoveKind(ctx context.Context, kind string) error
	GetState(ctx context.Context, peerAddress string) (*internalpb.PeerState, error)
	Events() <-chan *Event
	Peers(ctx context.Context) ([]*Peer, error)
	IsLeader(ctx context.Context) bool
	GetPartition(actorName string) uint64
	IsRunning() bool
	PutJobKey(ctx context.Context, jobID string, metadata []byte) error
	DeleteJobKey(ctx context.Context, jobID string) error
	JobKey(ctx context.Context, jobID string) ([]byte, error)
}

type clusterConfig struct {
	shardCount           uint64
	minimumMembersQuorum uint32
	replicasCount        uint32
	membersWriteQuorum   uint32
	membersReadQuorum    uint32
	tableSize            uint64
	writeTimeout         time.Duration
	readTimeout          time.Duration
	shutdownTimeout      time.Duration
	bootstrapTimeout     time.Duration
	routingTableInterval time.Duration
	logger               log.Logger
	shardHasher          hash.Hasher
	tlsInfo              *gtls.Info
}

func defaultClusterConfig() *clusterConfig {
	return &clusterConfig{
		shardCount:           271,
		minimumMembersQuorum: 1,
		replicasCount:        1,
		membersWriteQuorum:   1,
		membersReadQuorum:    1,
		tableSize:            4 * size.MB,
		writeTimeout:         time.Second,
		readTimeout:          time.Second,
		shutdownTimeout:      3 * time.Minute,
		bootstrapTimeout:     10 * time.Second,
		routingTableInterval: time.Minute,
		logger:               log.New(log.ErrorLevel, os.Stderr),
		shardHasher:          hash.DefaultHasher(),
		tlsInfo:              nil,
	}
}

type ConfigOption func(*clusterConfig)

func WithClusterLogger(logger log.Logger) ConfigOption {
	return func(cfg *clusterConfig) {
		if logger != nil {
			cfg.logger = logger
		}
	}
}

func WithShardHasher(h hash.Hasher) ConfigOption {
	return func(cfg *clusterConfig) {
		if h != nil {
			cfg.shardHasher = h
		}
	}
}

func WithShardCount(count uint64) ConfigOption {
	return func(cfg *clusterConfig) {
		if count > 0 {
			cfg.shardCount = count
		}
	}
}

func WithReplicasCount(count uint32) ConfigOption {
	return func(cfg *clusterConfig) {
		if count > 0 {
			cfg.replicasCount = count
		}
	}
}

func WithMinimumMembersQuorum(quorum uint32) ConfigOption {
	return func(cfg *clusterConfig) {
		if quorum > 0 {
			cfg.minimumMembersQuorum = quorum
		}
	}
}

func WithMembersWriteQuorum(quorum uint32) ConfigOption {
	return func(cfg *clusterConfig) {
		if quorum > 0 {
			cfg.membersWriteQuorum = quorum
		}
	}
}

func WithMembersReadQuorum(quorum uint32) ConfigOption {
	return func(cfg *clusterConfig) {
		if quorum > 0 {
			cfg.membersReadQuorum = quorum
		}
	}
}

func WithDataTableSize(size uint64) ConfigOption {
	return func(cfg *clusterConfig) {
		if size > 0 {
			cfg.tableSize = size
		}
	}
}

func WithClusterWriteTimeout(timeout time.Duration) ConfigOption {
	return func(cfg *clusterConfig) {
		if timeout > 0 {
			cfg.writeTimeout = timeout
		}
	}
}

func WithClusterReadTimeout(timeout time.Duration) ConfigOption {
	return func(cfg *clusterConfig) {
		if timeout > 0 {
			cfg.readTimeout = timeout
		}
	}
}

func WithClusterShutdownTimeout(timeout time.Duration) ConfigOption {
	return func(cfg *clusterConfig) {
		if timeout > 0 {
			cfg.shutdownTimeout = timeout
		}
	}
}

func WithClusterBootstrapTimeout(timeout time.Duration) ConfigOption {
	return func(cfg *clusterConfig) {
		if timeout > 0 {
			cfg.bootstrapTimeout = timeout
		}
	}
}

func WithRoutingTableInterval(interval time.Duration) ConfigOption {
	return func(cfg *clusterConfig) {
		if interval > 0 {
			cfg.routingTableInterval = interval
		}
	}
}

func WithSSL(info *gtls.Info) ConfigOption {
	return func(cfg *clusterConfig) {
		cfg.tlsInfo = info
	}
}

type cluster struct {
	_  locker.NoCopy
	mu sync.RWMutex

	name               string
	discoveryProvider  discovery.Provider
	node               *discovery.Node
	logger             log.Logger
	hasher             hash.Hasher
	partitionsCount    uint64
	minimumPeersQuorum uint32
	replicaCount       uint32
	writeQuorum        uint32
	readQuorum         uint32
	tableSize          uint64
	writeTimeout       time.Duration
	readTimeout        time.Duration
	shutdownTimeout    time.Duration
	bootstrapTimeout   time.Duration
	cacheSyncInterval  time.Duration
	tlsInfo            *gtls.Info

	server  *olric.Olric
	client  olric.Client
	dataMap olric.DMap

	events     chan *Event
	eventsLock *sync.Mutex
	pubSub     *redis.PubSub
	messages   <-chan *redis.Message

	nodeJoinedEventsFilter goset.Set[string]
	nodeLeftEventsFilter   goset.Set[string]

	running *atomic.Bool

	peerState *internalpb.PeerState
}

var _ Cluster = (*cluster)(nil)

func New(name string, disco discovery.Provider, node *discovery.Node, opts ...ConfigOption) (Cluster, error) {
	if name == "" {
		return nil, fmt.Errorf("engine name is required")
	}
	if disco == nil {
		return nil, fmt.Errorf("discovery provider is required")
	}
	if node == nil {
		return nil, fmt.Errorf("discovery node is required")
	}

	config := defaultClusterConfig()
	for _, opt := range opts {
		opt(config)
	}

	engine := &cluster{
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
		cacheSyncInterval:      config.routingTableInterval,
		tlsInfo:                config.tlsInfo,
		events:                 make(chan *Event, defaultEventsBufSize),
		eventsLock:             &sync.Mutex{},
		nodeJoinedEventsFilter: goset.NewSet[string](),
		nodeLeftEventsFilter:   goset.NewSet[string](),
		running:                atomic.NewBool(false),
	}

	return engine, nil
}
func (e *cluster) Start(ctx context.Context) error {
	if e.running.Load() {
		return nil
	}

	e.logger.Infof("starting unified cluster engine (%s)", e.name)

	e.eventsLock.Lock()
	e.events = make(chan *Event, defaultEventsBufSize)
	e.eventsLock.Unlock()
	e.nodeJoinedEventsFilter = goset.NewSet[string]()
	e.nodeLeftEventsFilter = goset.NewSet[string]()

	conf, err := e.buildConfig()
	if err != nil {
		e.logger.Errorf("failed to build engine config: %v", err)
		return err
	}
	conf.Hasher = &hasherWrapper{e.hasher}

	if err := e.setupMemberlistConfig(conf); err != nil {
		return err
	}

	e.configureDiscovery(conf)

	startCtx, cancel := context.WithCancel(ctx)
	conf.Started = func() { defer cancel() }

	cache, err := olric.New(conf)
	if err != nil {
		e.logger.Error(fmt.Errorf("failed to start unified cluster engine: %w", err))
		return err
	}

	e.server = cache
	if err := e.startServer(startCtx, ctx); err != nil {
		e.logger.Error(fmt.Errorf("failed to start unified cluster engine: %w", err))
		return err
	}

	e.client = e.server.NewEmbeddedClient()
	if err := e.createUnifiedMap(); err != nil {
		e.logger.Error(fmt.Errorf("failed to create cluster data map: %w", err))
		se := e.server.Shutdown(ctx)
		return errors.Join(err, se)
	}

	if err := e.createSubscription(ctx); err != nil {
		e.logger.Error(fmt.Errorf("failed to create cluster subscription: %w", err))
		se := e.server.Shutdown(ctx)
		return errors.Join(err, se)
	}

	e.initPeerState()
	if err := e.synchronizeState(ctx); err != nil {
		e.logger.Error(fmt.Errorf("failed to synchronize peer state: %w", err))
		se := e.server.Shutdown(ctx)
		return errors.Join(err, se)
	}

	e.running.Store(true)
	go e.consume()
	e.logger.Infof("unified cluster engine (%s) started", e.name)
	return nil
}

func (e *cluster) Stop(ctx context.Context) error {
	if !e.running.Load() {
		return nil
	}

	ctx, cancelFn := context.WithTimeout(ctx, e.shutdownTimeout)
	defer cancelFn()

	e.logger.Infof("stopping unified cluster engine (%s)", e.name)
	defer e.running.Store(false)

	if e.server != nil {
		if err := e.server.Shutdown(ctx); err != nil {
			e.logger.Errorf("failed to stop unified cluster engine: %v", err)
			return err
		}
	}

	if e.pubSub != nil {
		_ = e.pubSub.Close()
		e.pubSub = nil
	}
	e.messages = nil

	if e.events != nil {
		e.eventsLock.Lock()
		close(e.events)
		e.events = nil
		e.eventsLock.Unlock()
	}

	e.logger.Infof("unified cluster engine (%s) stopped", e.name)
	return nil
}

func (e *cluster) PutActor(ctx context.Context, actor *internalpb.Actor) error {
	if !e.running.Load() {
		return ErrEngineNotRunning
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	address := actor.GetAddress()
	if address == nil {
		return fmt.Errorf("actor address is not set")
	}

	encoded, err := encode(actor)
	if err != nil {
		return err
	}

	key := address.GetName()
	if key == "" {
		return fmt.Errorf("actor name is empty")
	}

	if err := e.putRecord(ctx, namespaceActor, key, encoded); err != nil {
		return err
	}

	actors := e.peerState.GetActors()
	actors[key] = actor
	e.peerState.Actors = actors

	return e.persistPeerState(ctx)
}

func (e *cluster) GetActor(ctx context.Context, actorName string) (*internalpb.Actor, error) {
	if !e.running.Load() {
		return nil, ErrEngineNotRunning
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	value, err := e.getRecord(ctx, namespaceActor, actorName)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			return nil, ErrActorNotFound
		}
		return nil, err
	}

	return decode(value)
}

func (e *cluster) RemoveActor(ctx context.Context, actorName string) error {
	if !e.running.Load() {
		return ErrEngineNotRunning
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.deleteRecord(ctx, namespaceActor, actorName); err != nil {
		return err
	}

	delete(e.peerState.Actors, actorName)
	return e.persistPeerState(ctx)
}

func (e *cluster) ActorExists(ctx context.Context, actorName string) (bool, error) {
	if !e.running.Load() {
		return false, ErrEngineNotRunning
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	_, err := e.getRecord(ctx, namespaceActor, actorName)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (e *cluster) Actors(ctx context.Context, timeout time.Duration) ([]*internalpb.Actor, error) {
	if !e.running.Load() {
		return nil, ErrEngineNotRunning
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	e.mu.RLock()
	defer e.mu.RUnlock()

	scanner, err := e.dataMap.Scan(ctx)
	if err != nil {
		return nil, err
	}
	defer scanner.Close()

	actors := make([]*internalpb.Actor, 0)
	for scanner.Next() {
		key := scanner.Key()
		if !hasNamespace(key, namespaceActor) {
			continue
		}

		resp, err := e.dataMap.Get(ctx, key)
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

func (e *cluster) PutGrain(ctx context.Context, grain *internalpb.Grain) error {
	if !e.running.Load() {
		return ErrEngineNotRunning
	}

	e.mu.Lock()
	defer e.mu.Unlock()

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

	if err := e.putRecord(ctx, namespaceGrain, key, encoded); err != nil {
		return err
	}

	grains := e.peerState.GetGrains()
	grains[key] = grain
	e.peerState.Grains = grains

	return e.persistPeerState(ctx)
}

func (e *cluster) GetGrain(ctx context.Context, identity string) (*internalpb.Grain, error) {
	if !e.running.Load() {
		return nil, ErrEngineNotRunning
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	value, err := e.getRecord(ctx, namespaceGrain, identity)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			return nil, ErrGrainNotFound
		}
		return nil, err
	}
	return decodeGrain(value)
}

func (e *cluster) GrainExists(ctx context.Context, identity string) (bool, error) {
	if !e.running.Load() {
		return false, ErrEngineNotRunning
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	_, err := e.getRecord(ctx, namespaceGrain, identity)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (e *cluster) RemoveGrain(ctx context.Context, identity string) error {
	if !e.running.Load() {
		return ErrEngineNotRunning
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.deleteRecord(ctx, namespaceGrain, identity); err != nil {
		return err
	}

	delete(e.peerState.Grains, identity)
	return e.persistPeerState(ctx)
}

func (e *cluster) Grains(ctx context.Context, timeout time.Duration) ([]*internalpb.Grain, error) {
	if !e.running.Load() {
		return nil, ErrEngineNotRunning
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	e.mu.RLock()
	defer e.mu.RUnlock()

	scanner, err := e.dataMap.Scan(ctx)
	if err != nil {
		return nil, err
	}
	defer scanner.Close()

	grains := make([]*internalpb.Grain, 0)
	for scanner.Next() {
		key := scanner.Key()
		if !hasNamespace(key, namespaceGrain) {
			continue
		}

		resp, err := e.dataMap.Get(ctx, key)
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

func (e *cluster) LookupKind(ctx context.Context, kind string) (string, error) {
	if !e.running.Load() {
		return "", ErrEngineNotRunning
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	value, err := e.getRecord(ctx, namespaceKind, kind)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			return "", nil
		}
		return "", err
	}

	return string(value), nil
}

func (e *cluster) PutKind(ctx context.Context, kind string) error {
	if !e.running.Load() {
		return ErrEngineNotRunning
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	return e.putRecord(ctx, namespaceKind, kind, []byte(kind))
}

func (e *cluster) RemoveKind(ctx context.Context, kind string) error {
	if !e.running.Load() {
		return ErrEngineNotRunning
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.deleteRecord(ctx, namespaceKind, kind); err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			return nil
		}
		return err
	}
	return nil
}

func (e *cluster) GetState(ctx context.Context, peerAddress string) (*internalpb.PeerState, error) {
	if !e.running.Load() {
		return nil, ErrEngineNotRunning
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	value, err := e.getRecord(ctx, namespacePeerState, peerAddress)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			return nil, ErrPeerSyncNotFound
		}
		return nil, err
	}

	peerState := new(internalpb.PeerState)
	if err := proto.Unmarshal(value, peerState); err != nil {
		return nil, err
	}
	return peerState, nil
}

func (e *cluster) Events() <-chan *Event {
	return e.events
}

func (e *cluster) Peers(ctx context.Context) ([]*Peer, error) {
	if !e.running.Load() {
		return nil, ErrEngineNotRunning
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	members, err := e.client.Members(ctx)
	if err != nil {
		e.logger.Errorf("failed to read cluster peers: %v", err)
		return nil, err
	}

	peers := make([]*Peer, 0, len(members))
	for _, member := range members {
		if member.Name == e.node.PeersAddress() {
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

func (e *cluster) IsLeader(ctx context.Context) bool {
	if !e.running.Load() {
		return false
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	members, err := e.client.Members(ctx)
	if err != nil {
		e.logger.Errorf("failed to fetch cluster members: %v", err)
		return false
	}

	for _, member := range members {
		node := new(discovery.Node)
		_ = json.Unmarshal([]byte(member.Meta), node)
		if node.PeersAddress() == e.node.PeersAddress() && member.Coordinator {
			return true
		}
	}
	return false
}

func (e *cluster) GetPartition(actorName string) uint64 {
	if !e.running.Load() {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.readTimeout)
	defer cancel()

	e.mu.RLock()
	defer e.mu.RUnlock()

	resp, err := e.dataMap.Get(ctx, composeKey(namespaceActor, actorName))
	if err != nil {
		return 0
	}
	return resp.Partition()
}

func (e *cluster) IsRunning() bool {
	return e.running.Load()
}

func (e *cluster) buildConfig() (*config.Config, error) {
	logLevel := "INFO"
	switch e.logger.LogLevel() {
	case log.DebugLevel:
		logLevel = "DEBUG"
	case log.ErrorLevel, log.FatalLevel, log.PanicLevel:
		logLevel = "ERROR"
	case log.WarningLevel:
		logLevel = "WARN"
	default:
	}

	jsonbytes, _ := json.Marshal(e.node)
	meta := string(jsonbytes)

	options := storage.NewConfig(nil)
	options.Add("tableSize", e.tableSize)

	cfg := &config.Config{
		BindAddr:          e.node.Host,
		BindPort:          e.node.PeersPort,
		ReadRepair:        true,
		ReplicaCount:      int(e.replicaCount),
		WriteQuorum:       int(e.writeQuorum),
		ReadQuorum:        int(e.readQuorum),
		MemberCountQuorum: int32(e.minimumPeersQuorum),
		Peers:             []string{},
		DMaps: &config.DMaps{
			Engine: &config.Engine{
				Config: options.ToMap(),
			},
		},
		KeepAlivePeriod:            config.DefaultKeepAlivePeriod,
		PartitionCount:             e.partitionsCount,
		BootstrapTimeout:           e.bootstrapTimeout,
		ReplicationMode:            config.SyncReplicationMode,
		RoutingTablePushInterval:   e.cacheSyncInterval,
		JoinRetryInterval:          config.DefaultJoinRetryInterval,
		MaxJoinAttempts:            config.DefaultMaxJoinAttempts,
		LogLevel:                   logLevel,
		LogOutput:                  newLogWriter(e.logger),
		EnableClusterEventsChannel: true,
		Hasher:                     hasher.NewDefaultHasher(),
		TriggerBalancerInterval:    config.DefaultTriggerBalancerInterval,
		MemberMeta:                 meta,
	}

	if e.tlsInfo != nil {
		cfg.TLS = &config.TLS{
			Client: e.tlsInfo.ClientConfig,
			Server: e.tlsInfo.ServerConfig,
		}

		client := &config.Client{TLS: e.tlsInfo.ClientConfig}
		if err := client.Sanitize(); err != nil {
			return nil, fmt.Errorf("failed to sanitize client config: %v", err)
		}
		cfg.Client = client
	}

	if e.logger.LogLevel() == log.DebugLevel {
		cfg.LogVerbosity = config.DefaultLogVerbosity
	}

	return cfg, nil
}

func (e *cluster) setupMemberlistConfig(cfg *config.Config) error {
	mconfig, err := config.NewMemberlistConfig("lan")
	if err != nil {
		e.logger.Errorf("failed to configure memberlist: %v", err)
		return err
	}
	mconfig.BindAddr = e.node.Host
	mconfig.BindPort = e.node.DiscoveryPort
	mconfig.AdvertisePort = e.node.DiscoveryPort
	mconfig.AdvertiseAddr = e.node.Host

	// Kubernetes-specific filtering is necessary because dynamic IP assignment can cause pods in different namespaces to share the same IP address over time.
	// This can lead to unintended cross-namespace communication within the memberlist ring.
	// To prevent this, all nodes are assigned the same label corresponding to the actor system name, enabling proper filtering.
	// As a result, even if a pod receives a gossip message from a reused IP now belonging to a different namespace,
	// the message will be rejected if it lacks the expected label identifying it as part of the correct ring.
	mconfig.Label = fmt.Sprintf("prefix-%s", strings.ToLower(e.name))

	if e.tlsInfo != nil {
		transport, err := memberlist.NewTransport(memberlist.TransportConfig{
			BindAddrs:          []string{e.node.Host},
			BindPort:           e.node.DiscoveryPort,
			PacketDialTimeout:  5 * time.Second,
			PacketWriteTimeout: 5 * time.Second,
			Logger:             e.logger,
			DebugEnabled:       false,
			TLSEnabled:         true,
			TLS:                e.tlsInfo.ClientConfig,
		})
		if err != nil {
			e.logger.Errorf("failed to create memberlist transport: %v", err)
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

func (e *cluster) configureDiscovery(conf *config.Config) {
	discoveryWrapper := &discoveryProvider{
		provider: e.discoveryProvider,
		log:      e.logger.StdLogger(),
	}
	conf.ServiceDiscovery = map[string]any{
		"plugin": discoveryWrapper,
		"id":     e.discoveryProvider.ID(),
	}
}

func (e *cluster) startServer(startCtx, ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		if err := e.server.Start(); err != nil {
			errCh <- errors.Join(err, e.server.Shutdown(ctx))
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

func (e *cluster) createUnifiedMap() error {
	dmap, err := e.client.NewDMap(unifiedMapName)
	if err != nil {
		return err
	}
	e.dataMap = dmap
	return nil
}

func (e *cluster) createSubscription(ctx context.Context) error {
	ps, err := e.client.NewPubSub(olric.ToAddress(e.node.PeersAddress()))
	if err != nil {
		return err
	}
	e.pubSub = ps.Subscribe(ctx, events.ClusterEventsChannel)
	e.messages = e.pubSub.Channel()
	return nil
}

func (e *cluster) initPeerState() {
	e.peerState = &internalpb.PeerState{
		Host:         e.node.Host,
		RemotingPort: int32(e.node.RemotingPort),
		PeersPort:    int32(e.node.PeersPort),
		Actors:       map[string]*internalpb.Actor{},
		Grains:       map[string]*internalpb.Grain{},
	}
}

func (e *cluster) synchronizeState(ctx context.Context) error {
	return e.persistPeerState(ctx)
}

func (e *cluster) persistPeerState(ctx context.Context) error {
	encoded, err := proto.Marshal(e.peerState)
	if err != nil {
		return err
	}
	return e.putRecord(ctx, namespacePeerState, e.node.PeersAddress(), encoded)
}

func (e *cluster) consume() {
	for message := range e.messages {
		payload := message.Payload
		var event map[string]any
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			e.logger.Errorf("failed to unmarshal cluster event: %v", err)
			continue
		}

		kind := event["kind"]
		switch kind {
		case events.KindNodeJoinEvent:
			e.eventsLock.Lock()
			nodeJoined := new(events.NodeJoinEvent)
			if err := json.Unmarshal([]byte(payload), &nodeJoined); err != nil {
				e.logger.Errorf("failed to unmarshal node join event: %v", err)
				e.eventsLock.Unlock()
				continue
			}

			if e.node.PeersAddress() == nodeJoined.NodeJoin {
				e.eventsLock.Unlock()
				continue
			}

			if e.nodeJoinedEventsFilter.Contains(nodeJoined.NodeJoin) {
				e.eventsLock.Unlock()
				continue
			}

			e.nodeJoinedEventsFilter.Add(nodeJoined.NodeJoin)
			timeMilli := nodeJoined.Timestamp / int64(1e6)
			evt := &goaktpb.NodeJoined{
				Address:   nodeJoined.NodeJoin,
				Timestamp: timestamppb.New(time.UnixMilli(timeMilli)),
			}
			payload, _ := anypb.New(evt)
			e.events <- &Event{Payload: payload, Type: NodeJoined}
			e.eventsLock.Unlock()

		case events.KindNodeLeftEvent:
			e.eventsLock.Lock()
			nodeLeft := new(events.NodeLeftEvent)
			if err := json.Unmarshal([]byte(payload), &nodeLeft); err != nil {
				e.logger.Errorf("failed to unmarshal node left event: %v", err)
				e.eventsLock.Unlock()
				continue
			}

			if e.nodeLeftEventsFilter.Contains(nodeLeft.NodeLeft) {
				e.eventsLock.Unlock()
				continue
			}

			e.nodeLeftEventsFilter.Add(nodeLeft.NodeLeft)
			timeMilli := nodeLeft.Timestamp / int64(1e6)
			evt := &goaktpb.NodeLeft{
				Address:   nodeLeft.NodeLeft,
				Timestamp: timestamppb.New(time.UnixMilli(timeMilli)),
			}
			payload, _ := anypb.New(evt)
			e.events <- &Event{Payload: payload, Type: NodeLeft}
			e.eventsLock.Unlock()
		default:
		}
	}
}

func (e *cluster) putRecord(ctx context.Context, namespace recordNamespace, key string, value []byte) error {
	ctx = context.WithoutCancel(ctx)
	ctx, cancel := context.WithTimeout(ctx, e.writeTimeout)
	defer cancel()

	return e.dataMap.Put(ctx, composeKey(namespace, key), value)
}

func (e *cluster) getRecord(ctx context.Context, namespace recordNamespace, key string) ([]byte, error) {
	ctx = context.WithoutCancel(ctx)
	ctx, cancel := context.WithTimeout(ctx, e.readTimeout)
	defer cancel()

	resp, err := e.dataMap.Get(ctx, composeKey(namespace, key))
	if err != nil {
		return nil, err
	}
	return resp.Byte()
}

func (e *cluster) deleteRecord(ctx context.Context, namespace recordNamespace, key string) error {
	ctx = context.WithoutCancel(ctx)
	ctx, cancel := context.WithTimeout(ctx, e.writeTimeout)
	defer cancel()

	_, err := e.dataMap.Delete(ctx, composeKey(namespace, key))
	if errors.Is(err, olric.ErrKeyNotFound) {
		return nil
	}
	return err
}
func (e *cluster) PutJobKey(ctx context.Context, jobID string, metadata []byte) error {
	if !e.running.Load() {
		return ErrEngineNotRunning
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	return e.putRecord(ctx, namespaceJob, jobID, metadata)
}

func (e *cluster) DeleteJobKey(ctx context.Context, jobID string) error {
	if !e.running.Load() {
		return ErrEngineNotRunning
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	return e.deleteRecord(ctx, namespaceJob, jobID)
}

func (e *cluster) JobKey(ctx context.Context, jobID string) ([]byte, error) {
	if !e.running.Load() {
		return nil, ErrEngineNotRunning
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	return e.getRecord(ctx, namespaceJob, jobID)
}
