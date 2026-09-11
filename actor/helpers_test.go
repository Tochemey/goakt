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

package actor

import (
	"bytes"
	"context"
	cryptotls "crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	syncatomic "sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/google/uuid"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	consulcontainer "github.com/testcontainers/testcontainers-go/modules/consul"
	etcdContainer "github.com/testcontainers/testcontainers-go/modules/etcd"
	"github.com/tochemey/goakt/v4/crdt"
	"github.com/tochemey/goakt/v4/datacenter"
	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/discovery/consul"
	"github.com/tochemey/goakt/v4/discovery/etcd"
	"github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/discovery/selfmanaged"
	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/datacentercontroller"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/log"
	mockcluster "github.com/tochemey/goakt/v4/mocks/cluster"
	testkit "github.com/tochemey/goakt/v4/mocks/discovery"
	mocksremote "github.com/tochemey/goakt/v4/mocks/remoteclient"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/supervisor"
	"github.com/tochemey/goakt/v4/test/data/testpb"
	"github.com/tochemey/goakt/v4/tls"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/types/known/anypb"
)

// everySecondCron fires on every second, the smallest granularity the Quartz
// cron format supports.
const everySecondCron = "* * * ? * *"

// benchMailboxDepth is the batch size for the mailbox throughput benchmarks: a
// batch of messages is enqueued and then fully drained before the next batch.
const benchMailboxDepth = 128

// pidMaxSizeBytes is the allocation size class the PID must stay in, since every actor pays for a
// class step. Raise it only with a BenchmarkActorMemoryFootprint measurement that justifies it.
const pidMaxSizeBytes = 416

// Timing budgets shared by the actor messaging and passivation tests.
const (
	receivingDelay = 1 * time.Second
	replyTimeout   = 100 * time.Millisecond
	passivateAfter = 200 * time.Millisecond
)

// Timing budgets shared by the reentrancy tests.
const (
	reentrancyReplyTimeout = time.Second
	reentrancyShortWait    = 50 * time.Millisecond
	reentrancyProcessWait  = 120 * time.Millisecond
	reentrancyDelay        = 200 * time.Millisecond
	reentrancyDispatchWait = 20 * time.Millisecond
)

// reliableDeliveryBenchmarkWindow is the largest supported demand window.
const reliableDeliveryBenchmarkWindow = 10_000

// Fixed system name and PID addresses used by the remote registry tests.
const (
	testRegistrySystemName = "TestSys"
	testRegistryPidA       = "goakt://TestSys@local-host:0/a"
	testRegistryPidB       = "goakt://TestSys@local-host:0/b"
)

// activationCount counts OnActivate calls of MockActivationCountingGrain across a test.
var activationCount syncatomic.Int32

// benchMailboxPriorities is a small spread of priorities so the priority
// mailboxes actually reorder their heap during the benchmark.
var benchMailboxPriorities = []*testpb.TestMessage{
	testpb.TestMessage_builder{Priority: 3}.Build(), testpb.TestMessage_builder{Priority: 1}.Build(), testpb.TestMessage_builder{Priority: 4}.Build(), testpb.TestMessage_builder{Priority: 1}.Build(),
	testpb.TestMessage_builder{Priority: 5}.Build(), testpb.TestMessage_builder{Priority: 9}.Build(), testpb.TestMessage_builder{Priority: 2}.Build(), testpb.TestMessage_builder{Priority: 6}.Build(),
}

// sharedQueueStates backs MockSharedDurableQueue instances across the in-process
// cluster nodes, modeling an external durable store every node can reach.
// Instances resolve their backing state by queue ID, so an instance
// reconstructed on another node observes the same sequences, confirmations,
// and writer epoch.
var (
	sharedQueueStatesMu sync.Mutex
	sharedQueueStates   = map[string]*MockDurableQueue{}
)

// sharedWorkQueueStates backs MockSharedDurableWorkQueue instances across the
// in-process cluster nodes, modeling an external durable work store.
var (
	sharedWorkQueueStatesMu sync.Mutex
	sharedWorkQueueStates   = map[string]*MockDurableWorkQueue{}
)

// nullConn is a no-op Connection implementation used where the handler ignores conn.
var nullConn dynaport.Connection

// errSupervisionTest is the failure a supervised test actor returns to trigger its supervision directive.
var errSupervisionTest = errors.New("supervision test failure")

// sharedRemotingForTests gives the remote PIDs built by makeReceiveContextFromAddress a non-nil remoting client.
var (
	sharedRemotingForTests = remoteclient.NewClient()
)

// newReSpawnClusterSystem returns a cluster-enabled actor system together with the mock cluster and
// mock remoting client it is wired to. The actors tree is empty, so ReSpawn takes the ActorOf path.
func newReSpawnClusterSystem(t *testing.T) (*mockcluster.Cluster, *mocksremote.Client, *actorSystem) {
	t.Helper()
	clusterMock := mockcluster.NewCluster(t)
	remotingMock := mocksremote.NewClient(t)
	system := newReplicationSystem(clusterMock)
	system.locker.Lock()
	system.actors = newTree()
	system.remoting = remotingMock
	system.locker.Unlock()
	return clusterMock, remotingMock, system
}

// newReplicationSystem returns an actor system wired to clusterMock and flagged as started, with
// clustering enabled and relocation disabled.
func newReplicationSystem(clusterMock *mockcluster.Cluster) *actorSystem {
	topic := &PID{}
	topic.setState(runningState, false)
	noSender := &PID{}
	noSender.setState(runningState, true)

	sys := &actorSystem{
		name:                  "test-replication",
		logger:                log.DiscardLogger,
		actors:                newTree(),
		grains:                xsync.NewMap[string, *grainPID](),
		remoteConfig:          remote.NewConfig("127.0.0.1", 8080),
		remoteHostPort:        net.JoinHostPort("127.0.0.1", "8080"),
		remoteSenderAddresses: xsync.NewMap[string, *address.Address](),
		clusterNode:           &discovery.Node{Host: "127.0.0.1", PeersPort: 9000},
		cluster:               clusterMock,
		topicActor:            topic,
		noSender:              noSender,
		dispatcher:            newDispatcher(dispatcherWorkerCount(), dispatcherThroughput),
	}
	sys.relocationJobs = make(map[string]*internalpb.PeerState)
	sys.peerRemotingPorts = xsync.NewMap[string, int]()
	sys.recentDepartures = xsync.NewTTLMap[string, types.Unit](correlatedDepartureWindow)
	sys.dispatcher.start()

	sys.started.Store(true)
	sys.starting.Store(false)
	sys.shuttingDown.Store(false)
	sys.startedAt.Store(time.Now().Unix())
	sys.actorsCounter.Store(0)
	sys.deadlettersCounter.Store(0)

	sys.clusterEnabled.Store(true)
	sys.relocationEnabled.Store(false) // callers toggle when needed
	sys.noSender.actorSystem = sys
	sys.topicActor.actorSystem = sys

	return sys
}

// newClusterReadySystem returns an actor system flagged as started and cluster-enabled, backed by the
// given remoting client, cluster and node.
func newClusterReadySystem(rem remoteclient.Client, cl cluster.Cluster, node *discovery.Node, opts ...remote.Option) *actorSystem {
	sys := &actorSystem{
		logger:      log.DiscardLogger,
		cluster:     cl,
		remoting:    rem,
		clusterNode: node,
		remoteConfig: remote.NewConfig(
			node.Host,
			node.RemotingPort,
			opts...,
		),
		dispatcher: newDispatcher(dispatcherWorkerCount(), dispatcherThroughput),
	}
	sys.dispatcher.start()

	sys.started.Store(true)
	sys.clusterEnabled.Store(true)
	sys.shuttingDown.Store(false)
	sys.grains = xsync.NewMap[string, *grainPID]()
	sys.remoteSenderAddresses = xsync.NewMap[string, *address.Address]()
	sys.registry = types.NewRegistry()
	sys.reflection = newReflection(sys.registry)

	return sys
}

// newSingletonClusterSystem returns a single actor system flagged as started, with clustering and
// remoting enabled on dynamically allocated ports.
func newSingletonClusterSystem(t *testing.T) *actorSystem {
	t.Helper()
	ports := dynaport.Get(3)

	sys, err := NewActorSystem("spawn-test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	clusterNode := &discovery.Node{
		Name:          "spawn-test-node",
		Host:          "127.0.0.1",
		DiscoveryPort: ports[0],
		PeersPort:     ports[1],
		RemotingPort:  ports[2],
	}

	actorSys := sys.(*actorSystem)
	actorSys.started.Store(true)
	actorSys.clusterEnabled.Store(true)
	actorSys.remotingEnabled.Store(true)
	actorSys.clusterNode = clusterNode
	actorSys.dispatcher.start()

	return actorSys
}

// newClusterGrainSystem returns a cluster-ready actor system with grain registered, the mock cluster
// backing it, and the identity of grain under name.
func newClusterGrainSystem(t *testing.T, grain Grain, name string) (*actorSystem, *mockcluster.Cluster, *GrainIdentity) {
	t.Helper()

	clusterMock := mockcluster.NewCluster(t)
	remotingMock := mocksremote.NewClient(t)
	node := &discovery.Node{
		Host:          "127.0.0.1",
		PeersPort:     14000,
		RemotingPort:  15000,
		DiscoveryPort: 0,
	}
	sys := newClusterReadySystem(remotingMock, clusterMock, node)
	sys.registry.Register(grain)

	return sys, clusterMock, newGrainIdentity(grain, name)
}

// startDatacenterSystem returns an actor system whose datacenter controller is already started
// against a fake control plane driven by listActive. The controller is stopped through t.Cleanup.
func startDatacenterSystem(t *testing.T, listActive func(_ context.Context) ([]datacenter.DataCenterRecord, error), remoting *mocksremote.Client) *actorSystem {
	t.Helper()
	dcConfig := datacenter.NewConfig()
	dcConfig.ControlPlane = &MockControlPlane{listActive: listActive}
	dcConfig.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}
	endpoints := []string{"127.0.0.1:8080"}
	// A generous staleness window keeps slow CI runners off the stale-cache path.
	dcConfig.MaxCacheStaleness = 5 * time.Second
	dcConfig.CacheRefreshInterval = 500 * time.Millisecond

	controller, err := datacentercontroller.NewController(dcConfig, endpoints)
	require.NoError(t, err)
	startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err = controller.Start(startCtx)
	cancel()
	require.NoError(t, err)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		_ = controller.Stop(stopCtx)
		stopCancel()
	})

	// controller.Start refreshes the cache synchronously, so no pause is needed here.
	clusterMock := mockcluster.NewCluster(t)
	sys := newReplicationSystem(clusterMock)
	sys.remoting = remoting
	sys.remotingEnabled.Store(true)
	sys.clusterConfig = NewClusterConfig().WithDataCenter(dcConfig)
	sys.dataCenterController = controller

	return sys
}

// newPIDAt returns an unstarted PID for name addressed at the given port within system.
func newPIDAt(system ActorSystem, name string, port int) *PID {
	addr := address.New(name, system.Name(), "host", port)
	return &PID{
		address:     addr,
		path:        newPath(addr),
		actorSystem: system,
	}
}

// newPassivationPID returns an unstarted PID for name carrying the given passivation strategy.
func newPassivationPID(t *testing.T, name string, strategy passivation.Strategy) *PID {
	t.Helper()
	addr := address.New(name, "test-system", "127.0.0.1", 0)
	pid := &PID{
		address:             addr,
		path:                newPath(addr),
		passivationStrategy: strategy,
	}
	return pid
}

// seedInactiveGrainPID registers a grain PID for id in sys without activating it and returns it.
// A nil config falls back to newGrainConfig.
func seedInactiveGrainPID(sys *actorSystem, id *GrainIdentity, grain Grain, config *grainConfig) *grainPID {
	if config == nil {
		config = newGrainConfig()
	}
	pid := newGrainPID(id, grain, sys, config)
	sys.grains.Set(id.String(), pid)
	return pid
}

// testClusterConfig holds the knobs a testClusterOption applies before the fixture builds a node.
type testClusterConfig struct {
	tlsEnabled        bool
	serverTLS         *cryptotls.Config
	clientTLS         *cryptotls.Config
	pubsubEnabled     bool
	relocationEnabled bool
	crdtEnabled       bool
	crdtOpts          []crdt.Option
	extension         extension.Extension
	dependency        extension.Dependency // injected into the system after it is built
	compression       remote.Compression
	roles             []string
	contextPropagator remote.ContextPropagator
	extraGrains       []Grain            // grain kinds registered on top of the fixture defaults
	extraKinds        []Actor            // actor kinds registered on top of the fixture defaults
	replicaCount      uint32             // cluster registry replication factor
	writeQuorum       uint32             // replicas that must acknowledge a registry write
	readQuorum        uint32             // replicas that must answer a registry read
	bootstrapTimeout  time.Duration      // window allowed for the initial partition sync
	protocolPin       remote.ProtocolPin // remoting wire protocol every node is pinned to
}

// testClusterOption mutates a testClusterConfig before the fixture builds a node.
type testClusterOption func(*testClusterConfig)

// withTestTLS enables TLS on the node with the given server and client configurations.
func withTestTLS(serverTLS, clientTLS *cryptotls.Config) testClusterOption {
	return func(tc *testClusterConfig) {
		tc.tlsEnabled = true
		tc.serverTLS = serverTLS
		tc.clientTLS = clientTLS
	}
}

// withTestPubSub enables the pub/sub subsystem on the node.
func withTestPubSub() testClusterOption {
	return func(tc *testClusterConfig) {
		tc.pubsubEnabled = true
	}
}

// withoutTestRelocation disables actor relocation on the node.
func withoutTestRelocation() testClusterOption {
	return func(tc *testClusterConfig) {
		tc.relocationEnabled = false
	}
}

// withTestReplication sets the cluster registry replication factor and the write and read quorums.
// A replicaCount above 1 keeps promotable backups on a departure, and a matching writeQuorum makes
// the promotion deterministic by requiring the backup before a write returns.
func withTestReplication(replicaCount, writeQuorum, readQuorum uint32) testClusterOption {
	return func(tc *testClusterConfig) {
		tc.replicaCount = replicaCount
		tc.writeQuorum = writeQuorum
		tc.readQuorum = readQuorum
	}
}

// withTestBootstrapTimeout overrides the short default bootstrap timeout used by the fixture.
// A cluster with replicaCount above 1 needs a longer window to complete its initial partition sync.
func withTestBootstrapTimeout(timeout time.Duration) testClusterOption {
	return func(tc *testClusterConfig) {
		tc.bootstrapTimeout = timeout
	}
}

// withTestProtocolPin pins the remoting wire protocol of every node built by the fixture, so a test
// can exercise a cluster that speaks only duplex or only legacy remoting.
func withTestProtocolPin(pin remote.ProtocolPin) testClusterOption {
	return func(tc *testClusterConfig) {
		tc.protocolPin = pin
	}
}

// withTestContextPropagator sets the remoting context propagator used by the node.
func withTestContextPropagator(propagator remote.ContextPropagator) testClusterOption {
	return func(tc *testClusterConfig) {
		tc.contextPropagator = propagator
	}
}

// withTestExtraGrains registers additional grain kinds cluster-wide on top of the fixture defaults.
func withTestExtraGrains(grains ...Grain) testClusterOption {
	return func(tc *testClusterConfig) {
		tc.extraGrains = append(tc.extraGrains, grains...)
	}
}

// withTestExtraKinds registers additional actor kinds cluster-wide so tests can relocate or
// remote-spawn their own actor types.
func withTestExtraKinds(kinds ...Actor) testClusterOption {
	return func(tc *testClusterConfig) {
		tc.extraKinds = append(tc.extraKinds, kinds...)
	}
}

// withTestExtension registers ext as an actor system extension on the node.
func withTestExtension(ext extension.Extension) testClusterOption {
	return func(tc *testClusterConfig) {
		tc.extension = ext
	}
}

// withTestCompression sets the remoting compression used by the node.
func withTestCompression(c remote.Compression) testClusterOption {
	return func(tc *testClusterConfig) {
		tc.compression = c
	}
}

// withTestRoles assigns the cluster roles carried by the node.
func withTestRoles(roles ...string) testClusterOption {
	return func(tc *testClusterConfig) {
		tc.roles = roles
	}
}

// withTestCRDT enables the CRDT subsystem on the node with the given options.
func withTestCRDT(opts ...crdt.Option) testClusterOption {
	return func(tc *testClusterConfig) {
		tc.crdtEnabled = true
		tc.crdtOpts = opts
	}
}

// providerFactory builds a discovery provider bound to host and discoveryPort.
type providerFactory func(t *testing.T, host string, discoveryPort int) discovery.Provider

// createNATsProvider returns a factory for NATS discovery providers pointed at the server at serverAddr.
func createNATsProvider(serverAddr string) providerFactory {
	return func(_ *testing.T, host string, discoveryPort int) discovery.Provider {
		natsSubject := "some-subject"
		config := nats.Config{
			NatsServer:    fmt.Sprintf("nats://%s", serverAddr),
			NatsSubject:   natsSubject,
			Host:          host,
			DiscoveryPort: discoveryPort,
		}
		return nats.NewDiscovery(&config, nats.WithLogger(log.DiscardLogger))
	}
}

// createConsulProvider returns a factory for Consul discovery providers pointed at the agent at agentEndpoint.
func createConsulProvider(agentEndpoint string) providerFactory {
	return func(t *testing.T, host string, discoveryPort int) discovery.Provider {
		config := &consul.Config{
			Address:         agentEndpoint,
			Timeout:         10 * time.Second,
			ActorSystemName: "accountsSystem",
			Host:            host,
			DiscoveryPort:   discoveryPort,
			Context:         t.Context(),
			HealthCheck: &consul.HealthCheck{
				Interval: time.Second, // short interval for faster failure detection in tests
				Timeout:  time.Second,
			},
			QueryOptions: &consul.QueryOptions{
				OnlyPassing: false,
				AllowStale:  false,
				WaitTime:    5 * time.Second,
			},
		}
		return consul.NewDiscovery(config)
	}
}

// createEtcdProvider returns a factory for etcd discovery providers pointed at the server at serverAddr.
func createEtcdProvider(serverAddr string) providerFactory {
	return func(t *testing.T, host string, discoveryPort int) discovery.Provider {
		config := &etcd.Config{
			Endpoints:       []string{serverAddr},
			Timeout:         10 * time.Second,
			ActorSystemName: "accountsSystem",
			Host:            host,
			DiscoveryPort:   discoveryPort,
			Context:         t.Context(),
			TTL:             60,
			DialTimeout:     5 * time.Second,
		}
		return etcd.NewDiscovery(config)
	}
}

// createSelfManagedProvider returns a factory for self-managed discovery providers broadcasting on broadcastPort.
func createSelfManagedProvider(broadcastPort int) providerFactory {
	return func(_ *testing.T, host string, discoveryPort int) discovery.Provider {
		config := &selfmanaged.Config{
			ClusterName:       "accountsSystem",
			SelfAddress:       fmt.Sprintf("%s:%d", host, discoveryPort),
			BroadcastPort:     broadcastPort,
			BroadcastInterval: 100 * time.Millisecond,
			BroadcastAddress:  net.IPv4(127, 0, 0, 1),
		}
		return selfmanaged.NewDiscovery(config)
	}
}

// startClusterSystem builds a clustered actor system from factory and starts it, returning the
// system and the discovery provider it uses.
func startClusterSystem(t *testing.T, factory providerFactory, opts ...testClusterOption) (ActorSystem, discovery.Provider) {
	system, provider := newClusterSystem(t, factory, opts...)
	require.NoError(t, system.Start(context.TODO()))
	return system, provider
}

// newClusterSystem builds, but does not start, a clustered actor system on dynamically allocated
// ports. It is split out of startClusterSystem so callers can start several nodes concurrently,
// which lets replicaCount > 1 members sync from each other instead of each waiting out the
// empty-partition escape.
func newClusterSystem(t *testing.T, factory providerFactory, opts ...testClusterOption) (ActorSystem, discovery.Provider) {
	logger := log.DiscardLogger

	ports := dynaport.Get(3)
	discoveryPort := ports[0]
	remotingPort := ports[1]
	peersPort := ports[2]

	host := "127.0.0.1"
	actorSystemName := "accountsSystem"

	provider := factory(t, host, discoveryPort)

	cfg := &testClusterConfig{relocationEnabled: true, replicaCount: 1, writeQuorum: 1, readQuorum: 1, bootstrapTimeout: time.Second}
	for _, opt := range opts {
		opt(cfg)
	}

	clusterConfig := NewClusterConfig().
		WithKinds(append([]Actor{
			new(MockActor),
			new(MockPersistentActor),
			new(MockExchanger),
			new(MockPingActor),
		}, cfg.extraKinds...)...).
		WithGrains(append([]Grain{new(MockGrain)}, cfg.extraGrains...)...).
		WithPartitionCount(7).
		WithReplicaCount(cfg.replicaCount).
		WithWriteQuorum(cfg.writeQuorum).
		WithReadQuorum(cfg.readQuorum).
		WithPeersPort(peersPort).
		WithMinimumPeersQuorum(1).
		WithDiscoveryPort(discoveryPort).
		WithBootstrapTimeout(cfg.bootstrapTimeout).
		WithClusterStateSyncInterval(300 * time.Millisecond).
		WithClusterBalancerInterval(100 * time.Millisecond).
		WithRoles(cfg.roles...).
		WithDiscovery(provider)

	if cfg.crdtEnabled {
		clusterConfig = clusterConfig.WithCRDT(cfg.crdtOpts...)
	}

	options := []Option{
		WithLogger(logger),
		WithShutdownTimeout(3 * time.Minute),
		WithCluster(clusterConfig),
	}

	if cfg.pubsubEnabled {
		options = append(options, WithPubSub())
	}

	if !cfg.relocationEnabled {
		options = append(options, WithoutRelocation())
	}

	if cfg.extension != nil {
		options = append(options, WithExtensions(cfg.extension))
	}

	remoteOpts := []remote.Option{remote.WithCompression(cfg.compression), remote.WithProtocolPin(cfg.protocolPin)}
	if cfg.contextPropagator != nil {
		remoteOpts = append(remoteOpts, remote.WithContextPropagator(cfg.contextPropagator))
	}

	if cfg.tlsEnabled {
		remoteOpts = append(remoteOpts, remote.WithTLS(&tls.Info{
			ClientConfig: cfg.clientTLS,
			ServerConfig: cfg.serverTLS,
		}))
	}
	options = append(options, WithRemote(remote.NewConfig(host, remotingPort, remoteOpts...)))

	system, err := NewActorSystem(actorSystemName, options...)
	require.NotNil(t, system)
	require.NoError(t, err)

	if cfg.dependency != nil {
		require.NoError(t, system.Inject(cfg.dependency))
	}

	return system, provider
}

// startNATsSystems builds count NATS-backed nodes and starts them concurrently, returning only once
// every node has finished bootstrapping. Each Start runs in its own goroutine, so failures travel
// through an error slice asserted on the test goroutine instead of a require call inside a goroutine.
func startNATsSystems(t *testing.T, serverAddr string, count int, opts ...testClusterOption) ([]ActorSystem, []discovery.Provider) {
	systems := make([]ActorSystem, count)
	providers := make([]discovery.Provider, count)
	for i := range count {
		systems[i], providers[i] = newClusterSystem(t, createNATsProvider(serverAddr), opts...)
	}

	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = systems[i].Start(context.TODO())
		}(i)
	}
	wg.Wait()

	for i := range count {
		require.NoError(t, errs[i], "node %d failed to start", i)
		require.NotNil(t, systems[i])
		require.NotNil(t, providers[i])
	}
	return systems, providers
}

// startNATsSystem starts one cluster node discovered through the NATS server at serverAddr.
func startNATsSystem(t *testing.T, serverAddr string, opts ...testClusterOption) (ActorSystem, discovery.Provider) {
	return startClusterSystem(t, createNATsProvider(serverAddr), opts...)
}

// startSelfManagedSystem starts one cluster node using self-managed discovery on broadcastPort.
func startSelfManagedSystem(t *testing.T, broadcastPort int, opts ...testClusterOption) (ActorSystem, discovery.Provider) {
	return startClusterSystem(t, createSelfManagedProvider(broadcastPort), opts...)
}

// startConsulSystem starts one cluster node discovered through the Consul agent at agentEndpoint.
func startConsulSystem(t *testing.T, agentEndpoint string, opts ...testClusterOption) (ActorSystem, discovery.Provider) {
	return startClusterSystem(t, createConsulProvider(agentEndpoint), opts...)
}

// startEtcdSystem starts one cluster node discovered through the etcd server at serverAddr.
func startEtcdSystem(t *testing.T, serverAddr string, opts ...testClusterOption) (ActorSystem, discovery.Provider) {
	return startClusterSystem(t, createEtcdProvider(serverAddr), opts...)
}

// startNatsServer starts an in-process NATS server on a random port and returns it once it accepts connections.
func startNatsServer(t *testing.T) *natsserver.Server {
	t.Helper()
	serv, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1",
		Port: -1,
	})

	require.NoError(t, err)

	ready := make(chan bool)
	go func() {
		ready <- true
		serv.Start()
	}()
	<-ready

	if !serv.ReadyForConnections(2 * time.Second) {
		t.Fatalf("nats-io server failed to start")
	}

	return serv
}

// startConsulAgent starts a Consul container and returns it with a channel closed once the agent reports a
// leader. The container is terminated through t.Cleanup.
func startConsulAgent(t *testing.T) (*consulcontainer.ConsulContainer, <-chan struct{}) {
	t.Helper()
	container, err := consulcontainer.Run(t.Context(), "hashicorp/consul:1.15")
	require.NoError(t, err)
	t.Cleanup(func() {
		err := container.Terminate(context.Background())
		require.NoError(t, err)
	})
	ready := make(chan struct{})
	go func() {
		ctx := context.Background()
		for {
			endpoint, err := container.ApiEndpoint(ctx)
			if err != nil {
				pause.For(100 * time.Millisecond)
				continue
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+endpoint+"/v1/status/leader", nil)
			if err != nil {
				pause.For(100 * time.Millisecond)
				continue
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				pause.For(100 * time.Millisecond)
				continue
			}
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				close(ready)
				return
			}
			pause.For(100 * time.Millisecond)
		}
	}()
	return container, ready
}

// startEtcdCluster starts a three-node etcd container and returns it with a channel closed once a write
// succeeds. The container is terminated through t.Cleanup.
func startEtcdCluster(t *testing.T) (*etcdContainer.EtcdContainer, <-chan struct{}) {
	t.Helper()
	container, err := etcdContainer.Run(
		t.Context(),
		"gcr.io/etcd-development/etcd:v3.5.14",
		etcdContainer.WithNodes("etcd-1", "etcd-2", "etcd-3"),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := testcontainers.TerminateContainer(container)
		require.NoError(t, err)
	})
	ready := make(chan struct{})
	go func() {
		ctx := context.Background()
		for {
			endpoints, err := container.ClientEndpoints(ctx)
			if err != nil {
				pause.For(100 * time.Millisecond)
				continue
			}
			cli, err := clientv3.New(clientv3.Config{
				Endpoints:   endpoints,
				DialTimeout: 5 * time.Second,
			})
			if err != nil {
				pause.For(100 * time.Millisecond)
				continue
			}
			_, err = cli.Put(ctx, "goakt/ready", "1")
			_ = cli.Close()
			if err != nil {
				pause.For(100 * time.Millisecond)
				continue
			}
			close(ready)
			return
		}
	}()
	return container, ready
}

// safeBuffer is a bytes.Buffer guarded by a mutex so concurrent log writers can share it.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p to the buffer and is safe for concurrent use.
func (x *safeBuffer) Write(p []byte) (n int, err error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.buf.Write(p)
}

// String returns the content buffered so far.
func (x *safeBuffer) String() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.buf.String()
}

// Reset discards the buffered content.
func (x *safeBuffer) Reset() {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.buf.Reset()
}

// extractMessage returns the msg field of a JSON-encoded log line, or the empty string when absent.
func extractMessage(data []byte) (string, error) {
	c := make(map[string]json.RawMessage)

	if err := json.Unmarshal(data, &c); err != nil {
		return "", err
	}

	for k, v := range c {
		if k == "msg" {
			return strconv.Unquote(string(v))
		}
	}

	return "", nil
}

// actorNamesFromRecords returns the distinct actor.name attribute values
// present in the records.
func actorNamesFromRecords(records []attrObserveRecord) []string {
	seen := make(map[string]bool)
	names := make([]string, 0, len(records))

	for _, record := range records {
		if value, ok := record.attrs.Value(attribute.Key("actor.name")); ok && !seen[value.AsString()] {
			seen[value.AsString()] = true
			names = append(names, value.AsString())
		}
	}

	return names
}

// groupRecordsByActor indexes records by actor.name and instrument name.
func groupRecordsByActor(records []attrObserveRecord) map[string]map[string]int64 {
	result := make(map[string]map[string]int64)

	for _, record := range records {
		value, ok := record.attrs.Value(attribute.Key("actor.name"))
		if !ok {
			continue
		}

		name := value.AsString()
		if result[name] == nil {
			result[name] = make(map[string]int64)
		}

		result[name][record.instrument] = record.value
	}

	return result
}

// remoteTestCtxKey is a custom type for context keys in remote context propagation tests (avoids SA1029).
type remoteTestCtxKey struct{}

// grainTestCtxKey is a custom type for context keys in grain tests (avoids SA1029 empty struct key).
type grainTestCtxKey struct{}

// grainTimerFixture bundles everything the grain timer end-to-end tests interact with.
type grainTimerFixture struct {
	system   ActorSystem
	identity *GrainIdentity
	grain    *MockTimerProbeGrain
	pid      *grainPID
	// props is the scheduling API surface OnActivate and OnDeactivate receive.
	props *GrainProps
}

// consumerControllerHarness wires a consumer controller under test to a recording producer
// controller stand-in and a mock consumer endpoint.
type consumerControllerHarness struct {
	ctx                       context.Context
	system                    *actorSystem
	producerControllerStandIn *PID // recorder registered under the peer producer controller name
	consumer                  *PID
	consumerController        *PID // the controller under test
}

// newConsumerControllerHarness starts a cluster-disabled system with a
// producer endpoint, its recording controller stand-in, a mock consumer, and
// the consumer controller under test.
func newConsumerControllerHarness(t *testing.T, window int, resendInterval time.Duration, autoConfirm bool) *consumerControllerHarness {
	t.Helper()

	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "producer", NewMockActor())
	require.NoError(t, err)

	spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "producer", producer.incarnationID())
	require.NoError(t, err)

	producerControllerName := reliableCompanionName(ReliableControllerRoleProducer, producer.incarnationID())
	producerControllerStandIn, err := system.Spawn(ctx, producerControllerName, &MockDeliveryRecorder{}, asSystem(), asReliableCompanion(spec))
	require.NoError(t, err)

	consumer, err := system.Spawn(ctx, "consumer", &MockReliableConsumer{autoConfirm: autoConfirm})
	require.NoError(t, err)

	controller := newConsumerController(consumer, consumerSettings("producer", window, resendInterval))
	consumerController, err := system.Spawn(ctx, "consumer-controller", controller)
	require.NoError(t, err)

	return &consumerControllerHarness{
		ctx:                       ctx,
		system:                    system,
		producerControllerStandIn: producerControllerStandIn,
		consumer:                  consumer,
		consumerController:        consumerController,
	}
}

// producerControllerRecorded asks the producer controller stand-in for a message snapshot.
func (x *consumerControllerHarness) producerControllerRecorded() []any {
	response, err := Ask(x.ctx, x.producerControllerStandIn, &getRecorded{}, time.Second)
	if err != nil {
		return nil
	}

	snapshot, _ := response.([]any)
	return snapshot
}

// deliveries asks the consumer mock for its delivery snapshot.
func (x *consumerControllerHarness) deliveries() []*Delivery {
	response, err := Ask(x.ctx, x.consumer, &getDeliveries{}, time.Second)
	if err != nil {
		return nil
	}

	snapshot, _ := response.([]*Delivery)
	return snapshot
}

// latestRegistration waits for and returns the latest RegisterConsumer
// received by the producer controller stand-in.
func (x *consumerControllerHarness) latestRegistration(t *testing.T) *commands.RegisterConsumer {
	t.Helper()

	var latest *commands.RegisterConsumer

	require.Eventually(t, func() bool {
		for _, message := range x.producerControllerRecorded() {
			if register, ok := message.(*commands.RegisterConsumer); ok {
				latest = register
			}
		}
		return latest != nil
	}, 3*time.Second, 10*time.Millisecond)

	return latest
}

// fromProducerController sends a protocol message to the consumer controller with the
// producer controller stand-in as sender.
func (x *consumerControllerHarness) fromProducerController(t *testing.T, message any) {
	t.Helper()
	require.NoError(t, Tell(x.ctx, x.producerControllerStandIn, &deliveryForward{to: x.consumerController, message: message}))
}

// adopt completes a registration handshake for the given session and returns
// the nonce of the acknowledged registration. It re-acks the latest
// registration on every poll because a silent controller keeps re-registering
// with fresh nonces.
func (x *consumerControllerHarness) adopt(t *testing.T, sessionID string, nextSeq int64) string {
	t.Helper()

	var nonce string

	require.Eventually(t, func() bool {
		var register *commands.RegisterConsumer

		for _, message := range x.producerControllerRecorded() {
			if candidate, ok := message.(*commands.RegisterConsumer); ok {
				register = candidate
			}
		}

		if register == nil {
			return false
		}

		nonce = register.Nonce()
		ack, err := commands.NewRegistrationAck(sessionID, nextSeq, nonce)
		if err != nil {
			return false
		}

		if Tell(x.ctx, x.producerControllerStandIn, &deliveryForward{to: x.consumerController, message: ack}) != nil {
			return false
		}

		for _, request := range x.requests() {
			if request.SessionID() == sessionID {
				return true
			}
		}

		return false
	}, 5*time.Second, 20*time.Millisecond)

	return nonce
}

// sequenced builds a SequencedMessage carrying an encoded test payload.
func (x *consumerControllerHarness) sequenced(t *testing.T, sessionID string, seq int64) *commands.SequencedMessage {
	t.Helper()

	payload := testpb.Reply_builder{Content: fmt.Sprintf("message-%d", seq)}.Build()
	frame, err := x.system.getRemoting().Serializer(payload).Serialize(payload)
	require.NoError(t, err)

	message, err := commands.NewSequencedMessage(sessionID, fmt.Sprintf("id-%d", seq), seq, frame)
	require.NoError(t, err)
	return message
}

// chunk builds a chunked SequencedMessage carrying one part of a frame.
func (x *consumerControllerHarness) chunk(t *testing.T, sessionID, messageID string, seq int64, part []byte, first, last bool) *commands.SequencedMessage {
	t.Helper()

	message, err := commands.NewChunkedSequencedMessage(sessionID, messageID, seq, part, first, last)
	require.NoError(t, err)
	return message
}

// encodeReply serializes one test payload the way the producer controller
// would before splitting it into chunks.
func (x *consumerControllerHarness) encodeReply(t *testing.T, content string) []byte {
	t.Helper()

	payload := testpb.Reply_builder{Content: content}.Build()
	frame, err := x.system.getRemoting().Serializer(payload).Serialize(payload)
	require.NoError(t, err)
	return frame
}

// requests returns the Requests recorded by the producer controller stand-in.
func (x *consumerControllerHarness) requests() []*commands.Request {
	var requests []*commands.Request

	for _, message := range x.producerControllerRecorded() {
		if request, ok := message.(*commands.Request); ok {
			requests = append(requests, request)
		}
	}

	return requests
}

// acks returns the Acks recorded by the producer controller stand-in.
func (x *consumerControllerHarness) acks() []*commands.Ack {
	var acks []*commands.Ack

	for _, message := range x.producerControllerRecorded() {
		if ack, ok := message.(*commands.Ack); ok {
			acks = append(acks, ack)
		}
	}

	return acks
}

// producerControllerHarness wires a producer controller under test to a recording producer
// endpoint and a recording consumer controller stand-in.
type producerControllerHarness struct {
	ctx                       context.Context
	system                    *actorSystem
	producer                  *PID
	consumerControllerStandIn *PID            // recorder registered under the peer consumer controller name
	producerController        *PID            // the controller under test
	usedTokens                map[string]bool // credits already answered, touched only by the test goroutine
}

// newProducerControllerHarness starts a cluster-disabled system with the
// producer endpoint, the consumer endpoint plus its registered controller
// stand-in, and the producer controller under test.
func newProducerControllerHarness(t *testing.T, queue DurableProducerQueue) *producerControllerHarness {
	t.Helper()
	return newProducerControllerHarnessWith(t, queue, false)
}

// newProducerControllerHarnessWith builds the harness with the endpoint's
// delivery-confirmation setting, which the spawn options would otherwise carry.
func newProducerControllerHarnessWith(t *testing.T, queue DurableProducerQueue, deliveryConfirmation bool) *producerControllerHarness {
	t.Helper()
	return newProducerControllerHarnessFor(t, queue, deliveryConfirmation, 0)
}

// newProducerControllerHarnessChunked builds the harness with chunking enabled
// at the given size on a volatile flow.
func newProducerControllerHarnessChunked(t *testing.T, maxChunkBytes uint32) *producerControllerHarness {
	t.Helper()
	return newProducerControllerHarnessFor(t, nil, false, maxChunkBytes)
}

// newProducerControllerHarnessFor builds the harness from the endpoint
// settings the spawn options would otherwise carry.
func newProducerControllerHarnessFor(t *testing.T, queue DurableProducerQueue, deliveryConfirmation bool, maxChunkBytes uint32) *producerControllerHarness {
	t.Helper()

	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "producer", &MockDeliveryRecorder{})
	require.NoError(t, err)

	consumer, err := system.Spawn(ctx, "consumer", NewMockActor())
	require.NoError(t, err)

	spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "consumer", consumer.incarnationID())
	require.NoError(t, err)

	consumerControllerName := reliableCompanionName(ReliableControllerRoleConsumer, consumer.incarnationID())
	consumerControllerStandIn, err := system.Spawn(ctx, consumerControllerName, &MockDeliveryRecorder{}, asSystem(), asReliableCompanion(spec))
	require.NoError(t, err)

	config := testProducerConfig("consumer", 2, 20*time.Millisecond, 150*time.Millisecond)
	config.deliveryConfirmation = deliveryConfirmation
	config.maxChunkBytes = maxChunkBytes

	producerController, err := system.Spawn(ctx, "producer-controller", newProducerController(producer, config, queue))
	require.NoError(t, err)

	return &producerControllerHarness{ctx: ctx, system: system, producer: producer, consumerControllerStandIn: consumerControllerStandIn, producerController: producerController, usedTokens: map[string]bool{}}
}

// recordedOf asks a recorder double for its message snapshot.
func (x *producerControllerHarness) recordedOf(pid *PID) []any {
	response, err := Ask(x.ctx, pid, &getRecorded{}, time.Second)
	if err != nil {
		return nil
	}

	snapshot, _ := response.([]any)
	return snapshot
}

// fromConsumerController sends a message to the producer controller from the consumer
// controller stand-in.
func (x *producerControllerHarness) fromConsumerController(t *testing.T, message any) {
	t.Helper()
	require.NoError(t, Tell(x.ctx, x.consumerControllerStandIn, &deliveryForward{to: x.producerController, message: message}))
}

// fromProducer sends a message to the producer controller from the producer.
func (x *producerControllerHarness) fromProducer(t *testing.T, message any) {
	t.Helper()
	require.NoError(t, Tell(x.ctx, x.producer, &deliveryForward{to: x.producerController, message: message}))
}

// register performs the registration handshake and returns the session ID.
func (x *producerControllerHarness) register(t *testing.T) string {
	t.Helper()

	registerConsumer, err := commands.NewRegisterConsumer(uuid.NewString())
	require.NoError(t, err)
	x.fromConsumerController(t, registerConsumer)

	var sessionID string

	require.Eventually(t, func() bool {
		for _, message := range x.recordedOf(x.consumerControllerStandIn) {
			if ack, ok := message.(*commands.RegistrationAck); ok && ack.Nonce() == registerConsumer.Nonce() {
				sessionID = ack.SessionID()
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)

	return sessionID
}

// nonceOf extracts the nonce of the latest acknowledged registration.
func (x *producerControllerHarness) nonceOf(t *testing.T) string {
	t.Helper()

	var nonce string

	for _, message := range x.recordedOf(x.consumerControllerStandIn) {
		if ack, ok := message.(*commands.RegistrationAck); ok {
			nonce = ack.Nonce()
		}
	}

	require.NotEmpty(t, nonce)
	return nonce
}

// latestRequestNext waits for the latest credit granted to the producer.
func (x *producerControllerHarness) latestRequestNext(t *testing.T) *RequestNext {
	t.Helper()

	var latest *RequestNext

	require.Eventually(t, func() bool {
		for _, message := range x.recordedOf(x.producer) {
			if request, ok := message.(*RequestNext); ok {
				latest = request
			}
		}
		return latest != nil
	}, 3*time.Second, 10*time.Millisecond)

	return latest
}

// latestStored waits for the latest storage acknowledgement to the producer.
func (x *producerControllerHarness) latestStored(t *testing.T) *Stored {
	t.Helper()

	var latest *Stored

	require.Eventually(t, func() bool {
		for _, message := range x.recordedOf(x.producer) {
			if stored, ok := message.(*Stored); ok {
				latest = stored
			}
		}
		return latest != nil
	}, 3*time.Second, 10*time.Millisecond)

	return latest
}

// deliveryConfirmations returns the confirmation notices the producer received.
func (x *producerControllerHarness) deliveryConfirmations() []*DeliveryConfirmed {
	var notices []*DeliveryConfirmed

	for _, message := range x.recordedOf(x.producer) {
		if notice, ok := message.(*DeliveryConfirmed); ok {
			notices = append(notices, notice)
		}
	}

	return notices
}

// sequencedEmissions returns the sequenced messages the stand-in received.
func (x *producerControllerHarness) sequencedEmissions() []*commands.SequencedMessage {
	var emissions []*commands.SequencedMessage

	for _, message := range x.recordedOf(x.consumerControllerStandIn) {
		if sequenced, ok := message.(*commands.SequencedMessage); ok {
			emissions = append(emissions, sequenced)
		}
	}

	return emissions
}

// produceOne drives one full producer handshake for messageID, waiting for a
// fresh credit and the storage acknowledgement of exactly this message.
func (x *producerControllerHarness) produceOne(t *testing.T, messageID string) {
	t.Helper()
	x.produceOneWith(t, messageID, testpb.Reply_builder{Content: messageID}.Build())
}

// produceOneWith drives one full producer handshake handing over payload.
func (x *producerControllerHarness) produceOneWith(t *testing.T, messageID string, payload *testpb.Reply) {
	t.Helper()

	request := x.freshRequestNext(t)
	produced, err := NewProduced(request, messageID, payload)
	require.NoError(t, err)
	x.fromProducer(t, produced)

	var stored *Stored

	require.Eventually(t, func() bool {
		for _, message := range x.recordedOf(x.producer) {
			if candidate, ok := message.(*Stored); ok && candidate.MessageID() == messageID {
				stored = candidate
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)

	ack, err := NewStoredAck(stored)
	require.NoError(t, err)
	x.fromProducer(t, ack)
}

// produceAgainWith drives a full resubmission handshake for a messageID the
// producer already completed once, waiting for a Stored beyond the ones
// already recorded, and returns that fresh acknowledgement.
func (x *producerControllerHarness) produceAgainWith(t *testing.T, messageID string, payload *testpb.Reply) *Stored {
	t.Helper()

	before := 0

	for _, message := range x.recordedOf(x.producer) {
		if stored, ok := message.(*Stored); ok && stored.MessageID() == messageID {
			before++
		}
	}

	request := x.freshRequestNext(t)
	produced, err := NewProduced(request, messageID, payload)
	require.NoError(t, err)
	x.fromProducer(t, produced)

	var stored *Stored

	require.Eventually(t, func() bool {
		count := 0

		for _, message := range x.recordedOf(x.producer) {
			if candidate, ok := message.(*Stored); ok && candidate.MessageID() == messageID {
				count++
				stored = candidate
			}
		}

		return count > before
	}, 3*time.Second, 10*time.Millisecond)

	ack, err := NewStoredAck(stored)
	require.NoError(t, err)
	x.fromProducer(t, ack)
	return stored
}

// freshRequestNext waits for a credit the test has not answered yet and marks
// it used.
func (x *producerControllerHarness) freshRequestNext(t *testing.T) *RequestNext {
	t.Helper()

	var request *RequestNext

	require.Eventually(t, func() bool {
		for _, message := range x.recordedOf(x.producer) {
			if candidate, ok := message.(*RequestNext); ok && !x.usedTokens[candidate.Token()] {
				request = candidate
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)

	x.usedTokens[request.Token()] = true
	return request
}

// crdtCluster is a test helper that manages a 3-node cluster with CRDT enabled.
type crdtCluster struct {
	nodes [3]ActorSystem
	sds   [3]discovery.Provider   // the discovery provider backing each node
	repls [3]*PID                 // the replicator actor running on each node
	srv   interface{ Shutdown() } // the NATS server every node discovers through
}

// shutdown stops all nodes and closes discovery providers.
func (x *crdtCluster) shutdown(t *testing.T) {
	t.Helper()
	ctx := context.TODO()

	for i := range 3 {
		require.NoError(t, x.nodes[i].Stop(ctx))
	}

	for i := range 3 {
		require.NoError(t, x.sds[i].Close())
	}

	x.srv.Shutdown()
}

// actorReplyTarget returns an async reply target addressed at a fixed remote actor.
func actorReplyTarget() *commands.AsyncReplyTo {
	return &commands.AsyncReplyTo{Kind: commands.ReplyToActor, Actor: address.New("actor", "sys", "127.0.0.1", 9000)}
}

// deadlettersFor collects dead-letter payloads currently queued on a stream
// consumer and returns only those whose Receiver.Name matches the given
// name. The consumer's Iterator yields the full event-stream replay, so
// filtering lets a test focus on the failure it triggered.
func deadlettersFor(consumer eventstream.Subscriber, receiverName string) []*Deadletter {
	var out []*Deadletter
	for message := range consumer.Iterator() {
		dl, ok := message.Payload().(*Deadletter)
		if !ok {
			continue
		}
		if dl.Receiver().Name() == receiverName {
			out = append(out, dl)
		}
	}
	return out
}

// newEmbeddedMailbox builds a standalone embedded mailbox seeded exactly as
// newPID seeds a default actor's: one shared sentinel node on both ends of an
// otherwise blank PID, reinterpreted through (*embeddedMailbox).
func newEmbeddedMailbox() *embeddedMailbox {
	pid := &PID{}
	sentinel := new(ReceiveContext)
	pid.mailboxHead = unsafe.Pointer(sentinel)
	pid.mailboxTail = unsafe.Pointer(sentinel)
	return (*embeddedMailbox)(pid)
}

// embeddedMailboxMessage builds a context carrying message for the mailbox tests.
func embeddedMailboxMessage(message any) *ReceiveContext {
	return &ReceiveContext{message: message}
}

// embeddedMailboxHead atomically reads the mailbox's current head, the node the
// release protocol keeps as the sentinel.
func embeddedMailboxHead(mailbox *embeddedMailbox) *ReceiveContext {
	return (*ReceiveContext)(syncatomic.LoadPointer(&mailbox.mailboxHead))
}

// newEmbeddedGrainMailbox builds a standalone embedded grain mailbox seeded
// exactly as attachMailbox seeds an unbounded grain's: one shared sentinel node
// on both ends of an otherwise blank process, reinterpreted through
// (*embeddedGrainMailbox).
func newEmbeddedGrainMailbox() *embeddedGrainMailbox {
	pid := &grainPID{}
	pid.attachMailbox(0)
	return (*embeddedGrainMailbox)(pid)
}

// embeddedGrainMailboxMessage builds a context carrying message for the grain
// mailbox tests.
func embeddedGrainMailboxMessage(message any) *GrainContext {
	return &GrainContext{message: message}
}

// embeddedGrainMailboxHead reads the mailbox's current head, the node the
// release protocol keeps as the sentinel.
func embeddedGrainMailboxHead(mailbox *embeddedGrainMailbox) *GrainContext {
	return mailbox.mailboxHead.Load()
}

// startTestActorSystem returns a started actor system named name and stops it through t.Cleanup.
func startTestActorSystem(t *testing.T, name string) ActorSystem {
	t.Helper()

	sys, err := NewActorSystem(name, WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(t.Context()))

	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = sys.Stop(stopCtx)
	})

	return sys
}

// newRequestTestSystem starts a system for the grain request and reply tests.
// The logger discards but stays enabled so the debug and error paths execute.
func newRequestTestSystem(t *testing.T) *actorSystem {
	t.Helper()
	ctx := context.Background()

	system, err := NewActorSystem("testSys", WithLogger(log.NewSlog(log.DebugLevel, io.Discard)))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))

	t.Cleanup(func() {
		_ = system.Stop(context.Background())
	})

	return system.(*actorSystem)
}

// activateReentrantGrain activates grain under name and equips its pid with
// reentrancy state the way the config plumbing will.
func activateReentrantGrain(t *testing.T, system *actorSystem, grain Grain, name string) *GrainIdentity {
	t.Helper()

	identity, err := system.GrainIdentity(context.Background(), name, func(context.Context) (Grain, error) {
		return grain, nil
	})
	require.NoError(t, err)

	pid, ok := system.grains.Get(identity.String())
	require.True(t, ok)

	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
	pid.attachResponseQueue()
	return identity
}

// newActivationTestSystem returns a cluster-ready actor system with its cluster and remoting mocks and the
// identity of grain. The grain kind is registered on the system only when register is true.
func newActivationTestSystem(t *testing.T, grain Grain, name string, register bool) (*actorSystem, *mockcluster.Cluster, *mocksremote.Client, *GrainIdentity) {
	t.Helper()

	cl := mockcluster.NewCluster(t)
	rem := mocksremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14000, RemotingPort: 15000}
	sys := newClusterReadySystem(rem, cl, node)
	if register {
		sys.registry.Register(grain)
	}

	return sys, cl, rem, newGrainIdentity(grain, name)
}

// benchmarkNodeLocalGrainSystem starts a single-node clustered system with an
// activated grain and returns it for node-local Tell/Ask benchmarks.
func benchmarkNodeLocalGrainSystem(b *testing.B) (ActorSystem, *GrainIdentity) {
	b.Helper()
	ctx := context.TODO()
	nodePorts := dynaport.Get(3)
	host := "127.0.0.1"
	addrs := []string{net.JoinHostPort(host, strconv.Itoa(nodePorts[0]))}

	provider := new(testkit.Provider)
	provider.EXPECT().ID().Return("bench").Maybe()
	provider.EXPECT().Initialize().Return(nil).Maybe()
	provider.EXPECT().Register().Return(nil).Maybe()
	provider.EXPECT().Deregister().Return(nil).Maybe()
	provider.EXPECT().DiscoverPeers().Return(addrs, nil).Maybe()
	provider.EXPECT().Close().Return(nil).Maybe()

	system, err := NewActorSystem(
		"bench",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig(host, nodePorts[2])),
		WithCluster(
			NewClusterConfig().
				WithGrains(new(MockGrain)).
				WithPartitionCount(9).
				WithReplicaCount(1).
				WithPeersPort(nodePorts[1]).
				WithMinimumPeersQuorum(1).
				WithDiscoveryPort(nodePorts[0]).
				WithDiscovery(provider)),
	)
	require.NoError(b, err)
	require.NoError(b, system.Start(ctx))
	pause.For(time.Second)

	identity, err := system.GrainIdentity(ctx, "bench-grain", func(context.Context) (Grain, error) {
		return NewMockGrain(), nil
	})
	require.NoError(b, err)

	b.Cleanup(func() { _ = system.Stop(ctx) })
	return system, identity
}

// startEnvelopeGrainFixture starts a system, activates the given grain and
// equips its pid with reentrancy state the way the config plumbing will, so
// asks against it take the envelope path.
func startEnvelopeGrainFixture(t *testing.T, grain Grain, name string) (*actorSystem, *grainPID, *GrainIdentity) {
	t.Helper()
	ctx := context.Background()

	system, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))

	t.Cleanup(func() {
		_ = system.Stop(context.Background())
	})

	identity, err := system.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	})
	require.NoError(t, err)

	sys := system.(*actorSystem)
	pid, ok := sys.grains.Get(identity.String())
	require.True(t, ok)

	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
	pid.attachResponseQueue()

	return sys, pid, identity
}

// startReentrantGrainFixture starts a system, activates a recording grain and
// equips its pid with reentrancy state the way the config plumbing will. The
// logger discards but stays enabled so the debug and warning paths execute.
func startReentrantGrainFixture(t *testing.T, mode reentrancy.Mode) (*actorSystem, *grainPID, *MockReentrantRecordingGrain, *GrainIdentity) {
	t.Helper()
	ctx := context.Background()

	system, err := NewActorSystem("testSys", WithLogger(log.NewSlog(log.DebugLevel, io.Discard)))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))

	t.Cleanup(func() {
		_ = system.Stop(context.Background())
	})

	grain := &MockReentrantRecordingGrain{}
	identity, err := system.GrainIdentity(ctx, "reentrantGrain", func(context.Context) (Grain, error) {
		return grain, nil
	})
	require.NoError(t, err)

	sys := system.(*actorSystem)
	pid, ok := sys.grains.Get(identity.String())
	require.True(t, ok)

	pid.reentrancy.Store(newReentrancyState(mode, 0))
	pid.attachResponseQueue()

	return sys, pid, grain, identity
}

// registerGrainRequestState mirrors the admission bookkeeping the public
// request API will perform on-turn: Step 5 owns completion and teardown, so
// these tests seed in-flight states directly.
func registerGrainRequestState(pid *grainPID, correlationID string, mode reentrancy.Mode, callback func(any, error)) *requestState {
	state := newRequestState(correlationID, mode, pid)
	if callback != nil {
		state.setCallback(callback)
	}

	pid.reentrancy.Load().requestStates.Set(correlationID, state)
	pid.reentrancy.Load().inFlightCount.Inc()

	if mode == reentrancy.StashNonReentrant {
		pid.reentrancy.Load().blockingCount.Inc()
	}
	return state
}

// passivationEntryState reports whether the manager tracks the grain and
// whether its entry is paused.
func passivationEntryState(system *actorSystem, pid *grainPID) (exists, paused bool) {
	manager := system.passivationManager()
	manager.mu.Lock()
	defer manager.mu.Unlock()

	entry, ok := manager.entries[pid.passivationID()]
	if !ok {
		return false, false
	}
	return true, entry.paused
}

// newTestGrainTimers returns a registry whose deliveries land on the returned
// channel, recorded by a mock sink in place of a grain process.
func newTestGrainTimers() (*grainTimers, chan *grainTimerEntry) {
	sink := NewMockTimerSink()
	return newGrainTimers(sink), sink.ticks
}

// entryOf fetches the live entry registered under reference.
func entryOf(t *testing.T, timers *grainTimers, reference string) *grainTimerEntry {
	t.Helper()
	timers.mu.Lock()
	defer timers.mu.Unlock()

	entry, ok := timers.entries[reference]
	require.True(t, ok)
	return entry
}

// entriesLen reports the number of registered entries.
func entriesLen(timers *grainTimers) int {
	timers.mu.Lock()
	defer timers.mu.Unlock()
	return len(timers.entries)
}

// expectDelivery waits for one delivery.
func expectDelivery(t *testing.T, deliveries chan *grainTimerEntry) *grainTimerEntry {
	t.Helper()
	select {
	case entry := <-deliveries:
		return entry
	case <-time.After(2 * time.Second):
		t.Fatal("expected a timer tick delivery")
		return nil
	}
}

// expectNoDelivery asserts that nothing is delivered within the given window.
func expectNoDelivery(t *testing.T, deliveries chan *grainTimerEntry, window time.Duration) {
	t.Helper()
	select {
	case entry := <-deliveries:
		t.Fatalf("unexpected timer tick delivery: reference=%s", entry.reference)
	case <-time.After(window):
	}
}

// expectGrainMessage waits for the grain to receive one message.
func expectGrainMessage(t *testing.T, grain *MockTimerProbeGrain) any {
	t.Helper()
	select {
	case message := <-grain.received:
		return message
	case <-time.After(2 * time.Second):
		t.Fatal("expected the grain to receive a message")
		return nil
	}
}

// expectNoGrainMessage asserts the grain receives nothing within the given window.
func expectNoGrainMessage(t *testing.T, grain *MockTimerProbeGrain, window time.Duration) {
	t.Helper()
	select {
	case message := <-grain.received:
		t.Fatalf("unexpected message received by the grain: %v", message)
	case <-time.After(window):
	}
}

// spawnTimerProbeGrain starts a standalone actor system and activates a probe
// grain with the given options.
func spawnTimerProbeGrain(t *testing.T, opts ...GrainOption) *grainTimerFixture {
	t.Helper()
	ctx := context.Background()

	sys, err := NewActorSystem("grainTimersSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(context.Background()) })

	grain := NewMockTimerProbeGrain()
	identity, err := sys.GrainIdentity(ctx, "timer-probe", func(context.Context) (Grain, error) {
		return grain, nil
	}, opts...)
	require.NoError(t, err)

	pid, ok := sys.(*actorSystem).grains.Get(identity.String())
	require.True(t, ok)
	require.True(t, pid.isActive())

	return &grainTimerFixture{
		system:   sys,
		identity: identity,
		grain:    grain,
		pid:      pid,
		props:    newGrainProps(identity, sys, nil, pid),
	}
}

// newTestGrainPID builds a minimal grainPID whose activate/deactivate can be
// driven directly, with a single fast activation attempt.
func newTestGrainPID(grain Grain, name string) *grainPID {
	config := newGrainConfig()
	config.initMaxRetries = 1
	config.initTimeout = 100 * time.Millisecond

	pid := &grainPID{
		grain:       grain,
		identity:    newGrainIdentity(grain, name),
		actorSystem: &actorSystem{logger: log.DiscardLogger},
		config:      config,
	}
	pid.attachMailbox(config.capacity)

	return pid
}

// entryTimerStarted reports whether the entry registered under reference has its
// fire trigger armed.
func entryTimerStarted(timers *grainTimers, reference string) bool {
	timers.mu.Lock()
	defer timers.mu.Unlock()

	entry, ok := timers.entries[reference]
	return ok && entry.timer != nil
}

// recordValue returns the value the scrape observed for the named instrument,
// and whether the instrument was observed at all.
func recordValue(records []attrObserveRecord, instrument string) (int64, bool) {
	for _, record := range records {
		if record.instrument == instrument {
			return record.value, true
		}
	}

	return 0, false
}

// mailboxSizesByActor returns the actor.mailbox.size observations of a default
// mode scrape, indexed by the actor they name.
func mailboxSizesByActor(records []attrObserveRecord) map[string]int64 {
	sizes := make(map[string]int64)

	for _, record := range records {
		if record.instrument != "actor.mailbox.size" {
			continue
		}

		if name, ok := record.attrs.Value(attribute.Key("actor.name")); ok {
			sizes[name.AsString()] = record.value
		}
	}

	return sizes
}

// scrapeOnce invokes every registered metrics callback once and returns the
// observer that captured the resulting observations.
func scrapeOnce(t *testing.T, ctx context.Context, provider *MockRecordingMeterProvider) *MockAttrObserver {
	t.Helper()

	observer := &MockAttrObserver{}
	for _, callback := range provider.meter.callbacks {
		require.NoError(t, callback(ctx, observer))
	}

	return observer
}

// deadlettersByMessageType returns the actor.deadletters.count observations
// made for the named actor, indexed by their message.type attribute.
func deadlettersByMessageType(records []attrObserveRecord, actorName string) map[string]int64 {
	counts := make(map[string]int64)

	for _, record := range records {
		if record.instrument != "actor.deadletters.count" {
			continue
		}

		name, ok := record.attrs.Value(attribute.Key("actor.name"))
		if !ok || name.AsString() != actorName {
			continue
		}

		if messageType, ok := record.attrs.Value(attribute.Key("message.type")); ok {
			counts[messageType.AsString()] = record.value
		}
	}

	return counts
}

// aggregatedRecords returns the observations a low cardinality scrape made for
// the given actor kind, excluding the dead-letter observations broken down by
// message type, whose values would otherwise overwrite one another.
func aggregatedRecords(records []attrObserveRecord, kind string) []attrObserveRecord {
	out := make([]attrObserveRecord, 0, len(records))

	for _, record := range records {
		value, ok := record.attrs.Value(attribute.Key("actor.kind"))
		if !ok || value.AsString() != kind {
			continue
		}

		if _, typed := record.attrs.Value(attribute.Key("message.type")); typed {
			continue
		}

		out = append(out, record)
	}

	return out
}

// aggregatedCountsByKind indexes a kind's aggregated observations by instrument
// name. In the low cardinality mode the per-actor instruments carry the same
// actor.system and actor.kind attribute set as the lifecycle counters, so both
// land in the same index under their own instrument names.
func aggregatedCountsByKind(records []attrObserveRecord, kind string) map[string]int64 {
	counts := make(map[string]int64)
	for _, record := range aggregatedRecords(records, kind) {
		counts[record.instrument] = record.value
	}

	return counts
}

// aggregatedAttributes returns the attribute set carried by a kind's aggregated
// observations.
func aggregatedAttributes(records []attrObserveRecord, kind string) attribute.Set {
	for _, record := range aggregatedRecords(records, kind) {
		return record.attrs
	}

	return *attribute.EmptySet()
}

// deadlettersByKindAndMessageType returns the actor.deadletters.count
// observations made for the given actor kind, indexed by their message.type
// attribute.
func deadlettersByKindAndMessageType(records []attrObserveRecord, kind string) map[string]int64 {
	counts := make(map[string]int64)

	for _, record := range records {
		if record.instrument != "actor.deadletters.count" {
			continue
		}

		value, ok := record.attrs.Value(attribute.Key("actor.kind"))
		if !ok || value.AsString() != kind {
			continue
		}

		if messageType, ok := record.attrs.Value(attribute.Key("message.type")); ok {
			counts[messageType.AsString()] = record.value
		}
	}

	return counts
}

// lifecycleRecords returns the observations carrying the given actor.kind and
// no actor.name, which is exactly the system-level per-kind lifecycle series.
func lifecycleRecords(records []attrObserveRecord, kind string) []attrObserveRecord {
	out := make([]attrObserveRecord, 0, len(records))

	for _, record := range records {
		value, ok := record.attrs.Value(attribute.Key("actor.kind"))
		if !ok || value.AsString() != kind {
			continue
		}

		if _, named := record.attrs.Value(attribute.Key("actor.name")); named {
			continue
		}

		out = append(out, record)
	}

	return out
}

// lifecycleCountsByKind indexes the per-kind lifecycle observations by
// instrument name.
func lifecycleCountsByKind(records []attrObserveRecord, kind string) map[string]int64 {
	counts := make(map[string]int64)
	for _, record := range lifecycleRecords(records, kind) {
		counts[record.instrument] = record.value
	}

	return counts
}

// lifecycleAttributes returns the attribute set carried by the per-kind
// lifecycle observations of the given kind.
func lifecycleAttributes(records []attrObserveRecord, kind string) attribute.Set {
	for _, record := range lifecycleRecords(records, kind) {
		return record.attrs
	}

	return *attribute.EmptySet()
}

// benchmarkMailboxThroughput measures single-consumer enqueue and dequeue cost.
// Each context is drawn from the pool, exactly as the Tell path does, and a
// batch is enqueued before it is drained. The mailbox recycles each drained
// context back to the pool, so the steady state stays allocation free while
// memory stays flat regardless of b.N. Drawing fresh contexts also respects the
// priority intake, which links messages through the intrusive
// ReceiveContext.next field and cannot hold the same node twice.
func benchmarkMailboxThroughput(b *testing.B, mb Mailbox) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i += benchMailboxDepth {
		n := benchMailboxDepth
		if remaining := b.N - i; remaining < n {
			n = remaining
		}

		for j := range n {
			ctx := getContext(0)
			ctx.message = benchMailboxPriorities[j%len(benchMailboxPriorities)]
			_ = mb.Enqueue(ctx)
		}

		for range n {
			mb.Dequeue()
		}
	}
	b.StopTimer()

	opsPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(opsPerSec, "ops/sec")
}

// newSystemWithoutDatacenter creates a mock actor system without a datacenter controller.
func newSystemWithoutDatacenter(t *testing.T, remoting *mocksremote.Client) *actorSystem {
	t.Helper()
	sys := &actorSystem{
		logger:   log.DiscardLogger,
		remoting: remoting,
	}
	sys.started.Store(true)
	sys.remotingEnabled.Store(true)
	return sys
}

// newBareSupervisor returns a supervisor whose default directives are cleared and that carries only strategy.
func newBareSupervisor(strategy supervisor.Strategy) *supervisor.Supervisor {
	supv := supervisor.NewSupervisor()
	supv.Reset()
	supervisor.WithStrategy(strategy)(supv)
	return supv
}

// waitParked blocks until the ready queue reports at least n parked workers and fails the test otherwise.
func waitParked(t *testing.T, rq *readyQueue, n int) {
	t.Helper()
	assert.Eventually(t, func() bool {
		return rq.parkedCount() >= n
	}, 2*time.Second, 1*time.Millisecond, "expected at least %d parked workers", n)
}

// reportScenarioError forwards a non-nil err to errCh, dropping it when the channel is already full.
func reportScenarioError(errCh chan<- error, err error) {
	if err == nil {
		return
	}
	select {
	case errCh <- err:
	default:
	}
}

// newRunningPIDWithReentrancy returns a PID marked running with the given reentrancy settings and its own
// dispatcher, which is stopped through t.Cleanup.
func newRunningPIDWithReentrancy(t *testing.T, mode reentrancy.Mode, maxInFlight int) *PID {
	t.Helper()
	d := newDispatcher(dispatcherWorkerCount(), dispatcherThroughput)
	d.start()
	t.Cleanup(d.signalStop)

	pid := &PID{
		mailbox:    NewUnboundedMailbox(),
		dispatcher: d,
	}
	pid.reentrancy.Store(newReentrancyState(mode, maxInFlight))
	pid.setState(runningState, true)
	return pid
}

// newReentrancySystem starts a minimal actor system for reentrancy tests.
func newReentrancySystem(t *testing.T) (ActorSystem, context.Context) {
	t.Helper()
	ctx := context.Background()
	sys, err := NewActorSystem("reentrancy-"+uuid.NewString(), WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })
	return sys, ctx
}

// spawnReentrancyActor creates a test actor with a custom Receive handler.
func spawnReentrancyActor(t *testing.T, sys ActorSystem, ctx context.Context, name string, receive func(*ReceiveContext), opts ...SpawnOption) *PID {
	t.Helper()
	pid, err := sys.Spawn(ctx, name, &MockReentrancyActor{receive: receive}, opts...)
	require.NoError(t, err)
	require.NotNil(t, pid)
	return pid
}

// responderWithDelay replies after a delay or remains silent for timeout tests.
func responderWithDelay(delay time.Duration, corrCh chan string) func(*ReceiveContext) {
	return func(ctx *ReceiveContext) {
		switch msg := ctx.Message().(type) {
		case *testpb.TestWait:
			if corrCh != nil {
				select {
				case corrCh <- ctx.CorrelationID():
				default:
				}
			}
			wait := delay
			if msg.GetDuration() > 0 {
				wait = time.Duration(msg.GetDuration()) * time.Millisecond
			}
			if wait > 0 {
				pause.For(wait)
			}
			ctx.Response(testpb.Reply_builder{Content: "ok"}.Build())
		case *testpb.TestTimeout:
			// intentionally no response
		default:
			ctx.Response(testpb.Reply_builder{Content: "ok"}.Build())
		}
	}
}

// waitForError blocks until errCh yields an error matching expected, failing the test when the timeout elapses.
func waitForError(t *testing.T, errCh <-chan error, expected error, timeout time.Duration) {
	t.Helper()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, expected)
	case <-time.After(timeout):
		t.Fatalf("expected error: %v", expected)
	}
}

// waitForReply blocks until replyCh yields a reply, failing the test on an error or when the timeout elapses.
func waitForReply(t *testing.T, replyCh <-chan any, errCh <-chan error, timeout time.Duration) {
	t.Helper()
	select {
	case <-replyCh:
		return
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(timeout):
		t.Fatal("expected async reply")
	}
}

// waitForSignal blocks until sigCh fires, failing the test with message when the timeout elapses.
func waitForSignal(t *testing.T, sigCh <-chan struct{}, timeout time.Duration, message string) {
	t.Helper()
	select {
	case <-sigCh:
		return
	case <-time.After(timeout):
		t.Fatal(message)
	}
}

// assertNoSignal fails the test with message when sigCh fires, and on any error errCh yields before the timeout.
func assertNoSignal(t *testing.T, sigCh <-chan struct{}, errCh <-chan error, timeout time.Duration, message string) {
	t.Helper()
	select {
	case <-sigCh:
		t.Fatal(message)
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(timeout):
		return
	}
}

// waitForProcessedBeforeReply blocks until processedCh fires, failing the test when a reply, an error or the
// timeout arrives first.
func waitForProcessedBeforeReply(t *testing.T, processedCh <-chan struct{}, replyCh <-chan any, errCh <-chan error, timeout time.Duration) {
	t.Helper()
	select {
	case <-processedCh:
		return
	case resp := <-replyCh:
		t.Fatalf("reply arrived before other message processed: %T", resp)
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(timeout):
		t.Fatal("expected other message to be processed while awaiting response")
	}
}

// waitForCorrelationID returns the first non-empty correlation id read from corrCh, failing the test on timeout.
func waitForCorrelationID(t *testing.T, corrCh <-chan string, timeout time.Duration) string {
	t.Helper()
	select {
	case id := <-corrCh:
		require.NotEmpty(t, id)
		return id
	case <-time.After(timeout):
		t.Fatal("expected correlation id to be set")
	}
	return ""
}

// newReliableClusterFixture starts a three-node NATS-backed cluster for
// reliable-delivery tests and stops every node when the test finishes. Three
// nodes place the endpoint pair on two members with one uninvolved member, so
// registry records regularly live on partitions owned by nodes that host
// neither endpoint.
func newReliableClusterFixture(t *testing.T) (context.Context, []*actorSystem) {
	t.Helper()

	ctx := context.TODO()
	server := startNatsServer(t)
	built, providers := startNATsSystems(t, server.Addr().String(), 3)

	systems := make([]*actorSystem, len(built))

	for i, system := range built {
		systems[i] = system.(*actorSystem)
	}

	// let membership settle before tests place actors
	pause.For(time.Second)

	t.Cleanup(func() {
		for i, system := range built {
			assert.NoError(t, system.Stop(context.WithoutCancel(ctx)))
			assert.NoError(t, providers[i].Close())
		}

		server.Shutdown()
	})

	return ctx, systems
}

// newCompanionTestSystem starts a cluster-disabled actor system for
// companion-resolution tests and stops it when the test finishes.
func newCompanionTestSystem(t *testing.T) (context.Context, *actorSystem) {
	t.Helper()

	ctx := context.TODO()
	system, err := NewActorSystem("companionTest", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))

	t.Cleanup(func() {
		require.NoError(t, system.Stop(context.WithoutCancel(ctx)))
	})

	return ctx, system.(*actorSystem)
}

// awaitDeliveries polls the consumer mock until it has recorded at least
// count deliveries and returns them collapsed to their first occurrence per
// sequence, since a slow confirmation legitimately allows a redelivery.
func awaitDeliveries(t *testing.T, ctx context.Context, consumer *PID, count int) []*Delivery {
	t.Helper()

	var distinct []*Delivery

	require.Eventually(t, func() bool {
		response, err := Ask(ctx, consumer, &getDeliveries{}, time.Second)
		if err != nil {
			return false
		}

		recorded, _ := response.([]*Delivery)
		seen := make(map[int64]bool, len(recorded))
		distinct = distinct[:0]

		for _, delivery := range recorded {
			if seen[delivery.Seq()] {
				continue
			}

			seen[delivery.Seq()] = true
			distinct = append(distinct, delivery)
		}

		return len(distinct) >= count
	}, 20*time.Second, 20*time.Millisecond)

	return distinct
}

// containsAllOperations reports whether every wanted operation appears in order.
func containsAllOperations(operations []string, wanted ...string) bool {
	index := 0
	for _, operation := range operations {
		if index < len(wanted) && operation == wanted[index] {
			index++
		}
	}
	return index == len(wanted)
}

// producerDeliveryConfig builds a complete producer endpoint configuration.
func producerDeliveryConfig(consumerName string) *reliableDeliveryConfig {
	return &reliableDeliveryConfig{
		producer: &reliableProducerConfig{
			consumerName:  consumerName,
			retryInterval: DefaultReliableProducerRetryInterval,
			queueRetry: &reliableQueueRetryConfig{
				maxAttempts:    DefaultReliableQueueRetryAttempts,
				initialBackoff: DefaultReliableQueueRetryBackoff,
			},
		},
	}
}

// consumerDeliveryConfig builds a complete consumer endpoint configuration.
func consumerDeliveryConfig(producerName string) *reliableDeliveryConfig {
	return &reliableDeliveryConfig{
		consumer: &reliableConsumerConfig{
			producerName:      producerName,
			flowControlWindow: 50,
			resendInterval:    DefaultReliableResendInterval,
		},
	}
}

// consumerSettings builds the consumer-side configuration the controller
// constructor consumes, keeping test call sites compact.
func consumerSettings(producerName string, window int, resendInterval time.Duration) *reliableConsumerConfig {
	return &reliableConsumerConfig{
		producerName:      producerName,
		flowControlWindow: window,
		resendInterval:    resendInterval,
	}
}

// mustChunkStoreRequest builds a StoreRequest or fails the test.
func mustChunkStoreRequest(t *testing.T, messageID string, seq int64, payload ReliablePayload) StoreRequest {
	t.Helper()

	request, err := NewStoreRequest(messageID, seq, payload)
	require.NoError(t, err)
	return request
}

// durableQueuePayload creates a serialized payload for queue tests.
func durableQueuePayload(t *testing.T, data string) ReliablePayload {
	t.Helper()

	payload, err := NewReliablePayload([]byte(data))
	require.NoError(t, err)
	return payload
}

// durableQueueMessage creates an unconfirmed message for queue tests.
func durableQueueMessage(t *testing.T, messageID string, seq int64, data string) UnconfirmedMessage {
	t.Helper()

	message, err := NewUnconfirmedMessage(messageID, seq, durableQueuePayload(t, data))
	require.NoError(t, err)
	return message
}

// testProducerConfig builds the producer settings a directly constructed
// controller needs, so a test states only the values it cares about.
func testProducerConfig(consumerName string, retryAttempts int, retryBackoff, localRetryInterval time.Duration) *reliableProducerConfig {
	return &reliableProducerConfig{
		consumerName:  consumerName,
		retryInterval: localRetryInterval,
		queueRetry: &reliableQueueRetryConfig{
			maxAttempts:    retryAttempts,
			initialBackoff: retryBackoff,
		},
	}
}

// awaitFailure polls the event stream until a terminal failure arrives; the
// subscriber iterator drains a snapshot per call.
func awaitFailure(t *testing.T, subscriber eventstream.Subscriber) *ReliableDeliveryFailed {
	t.Helper()

	var failure *ReliableDeliveryFailed

	require.Eventually(t, func() bool {
		for message := range subscriber.Iterator() {
			if candidate, ok := message.Payload().(*ReliableDeliveryFailed); ok {
				failure = candidate
				return true
			}
		}
		return false
	}, 3*time.Second, 50*time.Millisecond)

	return failure
}

// reliableProtocolPIDs creates distinct local PIDs for authorization tests.
func reliableProtocolPIDs() (endpoint, controller, other *PID) {
	return reliableProtocolPID("endpoint", 9000),
		reliableProtocolPID("controller", 9001),
		reliableProtocolPID("other", 9002)
}

// reliableProtocolPID creates a local PID without starting an actor system.
func reliableProtocolPID(name string, port int) *PID {
	addr := address.New(name, "reliable-protocol", "127.0.0.1", port)
	return &PID{
		address: addr,
		path:    newPath(addr),
	}
}

// newReliableRelocationFixture starts a three-node NATS-backed cluster whose
// nodes register the reliable endpoint kinds, and returns a stopNode function
// that gracefully stops one node mid-test; the cleanup stops the remainder.
// The registry runs with a backup replica: with replicaCount 1 the partitions
// primaried on the departed node are lost with it, and a lost peer-endpoint
// record would wedge companion resolution instead of exercising relocation.
func newReliableRelocationFixture(t *testing.T) (context.Context, []*actorSystem, func(index int)) {
	t.Helper()

	ctx := context.TODO()
	server := startNatsServer(t)
	built, providers := startNATsSystems(t, server.Addr().String(), 3,
		withTestExtraKinds(&MockReliableProducer{}, &MockReliableRelocationConsumer{}),
		withTestReplication(2, 1, 1),
		withTestBootstrapTimeout(20*time.Second))

	systems := make([]*actorSystem, len(built))

	for i, system := range built {
		systems[i] = system.(*actorSystem)
	}

	pause.For(time.Second)

	stopped := make([]bool, len(built))
	stopNode := func(index int) {
		t.Helper()
		require.NoError(t, built[index].Stop(context.WithoutCancel(ctx)))
		stopped[index] = true
	}

	t.Cleanup(func() {
		for i, system := range built {
			if !stopped[i] {
				assert.NoError(t, system.Stop(context.WithoutCancel(ctx)))
			}

			assert.NoError(t, providers[i].Close())
		}

		server.Shutdown()
	})

	return ctx, systems, stopNode
}

// awaitLocalEndpoint waits for name to be respawned on one of the given nodes
// and returns its local PID.
func awaitLocalEndpoint(t *testing.T, nodes []*actorSystem, name string) *PID {
	t.Helper()

	var relocated *PID

	require.Eventually(t, func() bool {
		for _, node := range nodes {
			if pidNode, ok := node.actors.nodeByName(name); ok {
				if pid := pidNode.value(); pid != nil && pid.IsRunning() {
					relocated = pid
					return true
				}
			}
		}

		return false
	}, 30*time.Second, 100*time.Millisecond, "endpoint %s must be respawned on a survivor", name)

	return relocated
}

// mustStoreRequest builds a store request for fencing assertions.
func mustStoreRequest(t *testing.T, messageID string, seq int64) StoreRequest {
	t.Helper()

	reliablePayload, err := NewReliablePayload([]byte(messageID))
	require.NoError(t, err)

	request, err := NewStoreRequest(messageID, seq, reliablePayload)
	require.NoError(t, err)
	return request
}

// newRemotingOnlySystem starts a cluster-disabled, remoting-enabled actor
// system bound to the given local port and stops it when the test finishes.
func newRemotingOnlySystem(t *testing.T, ctx context.Context, name string, port int) *actorSystem {
	system, err := NewActorSystem(name,
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig("127.0.0.1", port)))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))

	t.Cleanup(func() {
		assert.NoError(t, system.Stop(context.WithoutCancel(ctx)))
	})

	return system.(*actorSystem)
}

// workPullingProducerConfig builds a complete work-pulling producer configuration.
func workPullingProducerConfig() *reliableDeliveryConfig {
	return &reliableDeliveryConfig{
		producer: &reliableProducerConfig{
			workPulling:   true,
			retryInterval: DefaultReliableProducerRetryInterval,
			queueRetry: &reliableQueueRetryConfig{
				maxAttempts:    DefaultReliableQueueRetryAttempts,
				initialBackoff: DefaultReliableQueueRetryBackoff,
			},
		},
	}
}

// awaitDeliveriesSnapshot returns the consumer's current delivery list without
// waiting for a count.
func awaitDeliveriesSnapshot(t *testing.T, ctx context.Context, consumer *PID) []*Delivery {
	t.Helper()

	response, err := Ask(ctx, consumer, &getDeliveries{}, time.Second)
	if err != nil {
		return nil
	}

	recorded, _ := response.([]*Delivery)
	return recorded
}

// distinctDeliveries collapses redeliveries to the first occurrence per MessageID.
func distinctDeliveries(recorded []*Delivery) []*Delivery {
	seen := make(map[string]bool, len(recorded))
	distinct := make([]*Delivery, 0, len(recorded))

	for _, delivery := range recorded {
		if seen[delivery.MessageID()] {
			continue
		}

		seen[delivery.MessageID()] = true
		distinct = append(distinct, delivery)
	}

	return distinct
}

// recordedMessages asks a recorder double for its message snapshot.
func recordedMessages(t *testing.T, ctx context.Context, pid *PID) []any {
	t.Helper()

	response, err := Ask(ctx, pid, &getRecorded{}, time.Second)
	if err != nil {
		return nil
	}

	snapshot, _ := response.([]any)
	return snapshot
}

// testWorkPullingConfig builds the producer settings a directly constructed
// work-pulling controller needs, so a test states only the values it cares about.
func testWorkPullingConfig(retryAttempts int, retryBackoff, localRetryInterval time.Duration) *reliableProducerConfig {
	return &reliableProducerConfig{
		workPulling:   true,
		retryInterval: localRetryInterval,
		queueRetry: &reliableQueueRetryConfig{
			maxAttempts:    retryAttempts,
			initialBackoff: retryBackoff,
		},
	}
}

// remotePIDAt returns a remote PID addressed at host and port with no remoting client attached.
func remotePIDAt(host string, port int) *PID {
	return newRemotePID(address.New("target", "test", host, port), nil)
}

// collectRelocationFailedEvents drains the consumer and returns the
// RelocationFailed events it received.
func collectRelocationFailedEvents(consumer eventstream.Subscriber) []*RelocationFailed {
	var events []*RelocationFailed
	for message := range consumer.Iterator() {
		if event, ok := message.Payload().(*RelocationFailed); ok {
			events = append(events, event)
		}
	}
	return events
}

// testReassignByRoleSpreadsLoad asserts that role-less actors are spread evenly across the survivors.
func testReassignByRoleSpreadsLoad(t *testing.T) {
	actors := make([]*internalpb.Actor, 6)
	for i := range actors {
		actors[i] = internalpb.Actor_builder{Address: address.New(fmt.Sprintf("actor-%d", i), "test", "127.0.0.9", 7000).String()}.Build()
	}
	requests := []*internalpb.RelocateBatchRequest{internalpb.RelocateBatchRequest_builder{DepartedNode: "127.0.0.9:7000", Actors: actors}.Build()}

	survivors := []*cluster.Peer{
		{Host: "10.0.0.1", RemotingPort: 1},
		{Host: "10.0.0.2", RemotingPort: 2},
		{Host: "10.0.0.3", RemotingPort: 3},
	}

	failures := &relocationFailures{}
	actorShares, leaderActors, _ := reassignByRole(requests, survivors, nil, failures)

	// role-less actors must spread evenly instead of piling onto survivors[0]
	require.Len(t, actorShares, 3)
	for i := range actorShares {
		assert.Len(t, actorShares[i], 2, "survivor %d must receive an even share", i)
	}

	assert.Empty(t, leaderActors)
	assert.Empty(t, failures.items())
}

// testReassignByRoleDistribution asserts that every actor lands on a survivor advertising its role and that an
// actor whose role no survivor advertises is recorded as a failure.
func testReassignByRoleDistribution(t *testing.T) {
	gpu := "gpu"
	blue := "blue"
	missing := "missing"
	requests := []*internalpb.RelocateBatchRequest{
		internalpb.RelocateBatchRequest_builder{
			DepartedNode: "127.0.0.9:7000",
			Actors: []*internalpb.Actor{
				internalpb.Actor_builder{Address: address.New("no-role", "test", "127.0.0.9", 7000).String()}.Build(),
				internalpb.Actor_builder{Address: address.New("gpu-actor", "test", "127.0.0.9", 7000).String(), Role: &gpu}.Build(),
				internalpb.Actor_builder{Address: address.New("blue-actor", "test", "127.0.0.9", 7000).String(), Role: &blue}.Build(),
			},
			Grains: []*internalpb.Grain{internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: "grain-1"}.Build()}.Build()},
		}.Build(),
		internalpb.RelocateBatchRequest_builder{
			DepartedNode: "127.0.0.9:7000",
			Actors: []*internalpb.Actor{
				internalpb.Actor_builder{Address: address.New("unplaceable", "test", "127.0.0.9", 7000).String(), Role: &missing}.Build(),
			},
		}.Build(),
	}

	// survivor 0 advertises no role (hosts role-less), survivor 1 advertises gpu,
	// survivor 2 advertises blue; none advertises "missing".
	survivors := []*cluster.Peer{
		{Host: "10.0.0.1", RemotingPort: 1},
		{Host: "10.0.0.2", RemotingPort: 2, Roles: []string{"gpu"}},
		{Host: "10.0.0.3", RemotingPort: 3, Roles: []string{"blue"}},
	}

	failures := &relocationFailures{}
	actorShares, leaderActors, grains := reassignByRole(requests, survivors, nil, failures)

	require.Len(t, actorShares, 3)
	// role-less actor lands on the first survivor
	require.Len(t, actorShares[0], 1)
	assert.Contains(t, actorShares[0][0].GetAddress(), "no-role")
	// gpu actor lands on the gpu survivor
	require.Len(t, actorShares[1], 1)
	assert.Contains(t, actorShares[1][0].GetAddress(), "gpu-actor")
	// blue actor lands on the blue survivor
	require.Len(t, actorShares[2], 1)
	assert.Contains(t, actorShares[2][0].GetAddress(), "blue-actor")

	// a role-less leader takes nothing while survivors can host every actor
	assert.Empty(t, leaderActors)

	// grains are flattened out for the caller to place
	require.Len(t, grains, 1)

	// the actor requiring a role no survivor advertises is recorded once
	items := failures.items()
	require.Len(t, items, 1)
	assert.False(t, items[0].GetGrain())
	assert.Contains(t, items[0].GetId(), "unplaceable")
	assert.Contains(t, items[0].GetMessage(), "missing")
}

// testReassignByRoleLeaderFallback asserts that the leader recovers the actors whose role no survivor advertises,
// leaving only the truly unplaceable ones as failures.
func testReassignByRoleLeaderFallback(t *testing.T) {
	gpu := "gpu"
	missing := "missing"
	requests := []*internalpb.RelocateBatchRequest{
		internalpb.RelocateBatchRequest_builder{
			DepartedNode: "127.0.0.9:7000",
			Actors: []*internalpb.Actor{
				internalpb.Actor_builder{Address: address.New("no-role", "test", "127.0.0.9", 7000).String()}.Build(),
				internalpb.Actor_builder{Address: address.New("gpu-actor", "test", "127.0.0.9", 7000).String(), Role: &gpu}.Build(),
				internalpb.Actor_builder{Address: address.New("unplaceable", "test", "127.0.0.9", 7000).String(), Role: &missing}.Build(),
			},
		}.Build(),
	}

	// no survivor advertises gpu, but the leader does: the gpu actor must be
	// recovered locally instead of being reported as unplaceable
	survivors := []*cluster.Peer{{Host: "10.0.0.1", RemotingPort: 1}}

	failures := &relocationFailures{}
	actorShares, leaderActors, grains := reassignByRole(requests, survivors, []string{"gpu"}, failures)

	require.Len(t, actorShares, 1)
	require.Len(t, actorShares[0], 1)
	assert.Contains(t, actorShares[0][0].GetAddress(), "no-role")

	require.Len(t, leaderActors, 1)
	assert.Contains(t, leaderActors[0].GetAddress(), "gpu-actor")

	assert.Empty(t, grains)

	// only the actor no surviving node (leader included) can host is a failure
	items := failures.items()
	require.Len(t, items, 1)
	assert.Contains(t, items[0].GetId(), "unplaceable")

	// with no survivors at all, every leader-eligible actor goes local
	failures = &relocationFailures{}
	actorShares, leaderActors, _ = reassignByRole(requests, nil, []string{"gpu"}, failures)
	assert.Empty(t, actorShares)
	require.Len(t, leaderActors, 2)
	require.Len(t, failures.items(), 1)
}

// mustName returns the name parsed out of the actor's address and fails the test when it cannot be parsed.
func mustName(t *testing.T, actor *internalpb.Actor) string {
	t.Helper()
	addr, err := address.Parse(actor.GetAddress())
	require.NoError(t, err)
	return addr.Name()
}

// grainIDs flattens grain identities for containment assertions.
func grainIDs(grains []*internalpb.Grain) []string {
	ids := make([]string, 0, len(grains))
	for _, grain := range grains {
		ids = append(ids, grain.GetGrainId().GetValue())
	}
	return ids
}

// registryLen counts live entries; test-only, single-threaded.
func registryLen(x *remoteHoldRegistry) int {
	count := 0
	head := (*remoteHoldNode)(syncatomic.LoadPointer(&x.head))

	for current := (*remoteHoldNode)(syncatomic.LoadPointer(&head.next)); current != nil; {
		count++
		current = (*remoteHoldNode)(syncatomic.LoadPointer(&current.next))
	}

	return count
}

// newRemoteServerTestSystem builds the minimal actorSystem needed to unit-test
// the proto TCP handler methods in remote_server.go.
// It does NOT start the actor system; handlers are called directly. A real
// remoting client is attached so the handler's upfront payload decode
// (which needs access to the serializer registry) works against an
// environment that mirrors production wiring.
func newRemoteServerTestSystem(host string, port int) *actorSystem {
	sys := &actorSystem{
		actors:                newTree(),
		logger:                log.DiscardLogger,
		remoteConfig:          remote.NewConfig(host, port),
		name:                  "testSys",
		grains:                xsync.NewMap[string, *grainPID](),
		askTimeout:            DefaultAskTimeout,
		remoting:              remoteclient.NewClient(),
		remoteWatches:         newRemoteWatchRegistry(),
		remoteHostPort:        fmt.Sprintf("%s:%d", host, port),
		remoteSenderAddresses: xsync.NewMap[string, *address.Address](),
	}
	sys.remotingEnabled.Store(true)
	return sys
}

// requireProtoError asserts that the returned proto message is an *internalpb.Error
// with the expected code.
func requireProtoError(t *testing.T, msg any, code internalpb.Code) {
	t.Helper()
	protoErr, ok := msg.(*internalpb.Error)
	require.True(t, ok, "expected *internalpb.Error, got %T", msg)
	assert.Equal(t, code, protoErr.GetCode())
}

// newRemoteServerTestSystemWithStoppedActor creates a minimal actorSystem with a stopped
// actor in the tree. Used to test handlers that return CODE_NOT_FOUND when pid is not running.
func newRemoteServerTestSystemWithStoppedActor(t *testing.T, host string, port int, name string) *actorSystem {
	t.Helper()
	sys := newRemoteServerTestSystem(host, port)
	sys.noSender = newPIDAt(sys, "nosender", 0)
	sys.actors.noSender = sys.noSender
	addr := address.New(name, sys.Name(), host, port)
	stoppedPID := &PID{
		address:     addr,
		path:        newPath(addr),
		actorSystem: sys,
	}
	require.NoError(t, sys.actors.addRootNode(stoppedPID))
	return sys
}

// newRemoteServerTestSystemWithZombieNode creates a minimal actorSystem with a "zombie" node
// in the tree: a node that exists in the map but has nil pid. This simulates the race where
// deleteNode has cleared the pid but a handler still holds a reference to the node.
// Used to verify handlers return CODE_NOT_FOUND instead of panicking.
func newRemoteServerTestSystemWithZombieNode(t *testing.T, host string, port int, name string) *actorSystem {
	t.Helper()
	sys := newRemoteServerTestSystem(host, port)
	sys.noSender = newPIDAt(sys, "nosender", 0)
	sys.actors.noSender = sys.noSender
	addr := address.New(name, sys.Name(), host, port)
	addrStr := addr.String()
	n := newPidNode(nil)
	n.id = addrStr
	n.name = name
	sys.actors.mu.Lock()
	sys.actors.pids[addrStr] = n
	sys.actors.names[name] = n
	sys.actors.mu.Unlock()
	return sys
}

// newTestAddress returns an address for name under the registry test system at host and port.
func newTestAddress(t *testing.T, name, host string, port int) *address.Address {
	t.Helper()
	return address.New(name, testRegistrySystemName, host, port)
}

// spawnTestReplicator registers the CRDT config extension on the actor system
// and spawns a Replicator actor. This mirrors what spawnReplicator does in production.
func spawnTestReplicator(t *testing.T, sys ActorSystem) *PID {
	t.Helper()
	ctx := context.TODO()
	config := crdt.NewConfig()
	impl := sys.(*actorSystem)
	impl.extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{config: config})
	repl, err := sys.Spawn(ctx, "replicator", newReplicatorActor(), WithLongLived())
	require.NoError(t, err)
	require.NotNil(t, repl)
	pause.For(500 * time.Millisecond)
	return repl
}

// newTestReplicator creates a replicatorActor with config set directly for unit tests
// that don't go through the actor system.
func newTestReplicator() *replicatorActor {
	r := newReplicatorActor()
	r.config = crdt.NewConfig()
	r.store = make(map[string]crdt.ReplicatedData)
	r.keyTypes = make(map[string]crdt.DataType)
	r.subscriptions = make(map[string]types.Unit)
	r.watchers = make(map[string][]*PID)
	r.tombstones = make(map[string]*tombstone)
	r.versions = make(map[string]uint64)
	return r
}

// setupCRDTCluster creates and starts a 3-node CRDT-enabled cluster.
func setupCRDTCluster(t *testing.T) *crdtCluster {
	t.Helper()
	srv := startNatsServer(t)
	c := &crdtCluster{srv: srv}
	for i := range 3 {
		node, sd := startNATsSystem(t, srv.Addr().String(), withTestCRDT())
		require.NotNil(t, node)
		c.nodes[i] = node
		c.sds[i] = sd
	}
	pause.For(3 * time.Second)
	for i := range 3 {
		c.repls[i] = c.nodes[i].Replicator()
		require.NotNil(t, c.repls[i], "replicator should be running on node %d", i+1)
	}
	return c
}

// getPNCounter reads a PNCounter from a node's replicator.
func getPNCounter(t *testing.T, repl *PID, key crdt.Key) *crdt.PNCounter {
	t.Helper()
	ctx := context.TODO()
	resp, err := Ask(ctx, repl, &crdt.Get{Key: key}, time.Second)
	require.NoError(t, err)
	data := resp.(*crdt.GetResponse).Data
	if data == nil {
		return nil
	}
	return data.(*crdt.PNCounter)
}

// getORSet reads an ORSet from a node's replicator.
func getORSet(t *testing.T, repl *PID, key crdt.Key) *crdt.ORSet {
	t.Helper()
	ctx := context.TODO()
	resp, err := Ask(ctx, repl, &crdt.Get{Key: key}, time.Second)
	require.NoError(t, err)
	data := resp.(*crdt.GetResponse).Data
	if data == nil {
		return nil
	}
	return data.(*crdt.ORSet)
}

// spawnTestReplicatorWithDC registers the CRDT config extension with DC identity
// and spawns a Replicator actor configured for cross-DC replication.
func spawnTestReplicatorWithDC(t *testing.T, sys ActorSystem, dcName, dcRegion, dcZone string) *PID {
	t.Helper()
	ctx := context.TODO()
	config := crdt.NewConfig(crdt.WithDataCenterReplication())
	impl := sys.(*actorSystem)
	impl.extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{
		config: config,
		dc: datacenter.DataCenter{
			Name:   dcName,
			Region: dcRegion,
			Zone:   dcZone,
		},
	})
	repl, err := sys.Spawn(ctx, "replicator", newReplicatorActor(), WithLongLived())
	require.NoError(t, err)
	require.NotNil(t, repl)
	pause.For(500 * time.Millisecond)
	return repl
}

// spawnReplicatorWithDCController creates a replicator with DC capabilities,
// injecting cluster mock, remoting mock, and a real DC controller backed by
// a mock control plane. Returns the system, PID, replicator actor reference,
// and the two mocks so callers can set expectations and inspect counters.
func spawnReplicatorWithDCController(
	t *testing.T,
	listActive func(context.Context) ([]datacenter.DataCenterRecord, error),
	dcCfgOverride *datacenter.Config,
) (ActorSystem, *PID, *replicatorActor, *mockcluster.Cluster, *mocksremote.Client) {
	t.Helper()
	ctx := context.TODO()

	// WithPubSub ensures the TopicActor is created during Start so
	// publishDelta can buffer deltas for cross-DC forwarding.
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithPubSub())
	err := sys.Start(ctx)
	require.NoError(t, err)
	pause.For(time.Second)

	impl := sys.(*actorSystem)

	dcConfig := dcCfgOverride
	if dcConfig == nil {
		dcConfig = datacenter.NewConfig()
		dcConfig.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}
		dcConfig.MaxCacheStaleness = 5 * time.Second
		dcConfig.CacheRefreshInterval = 500 * time.Millisecond
	}
	dcConfig.ControlPlane = &MockControlPlane{listActive: listActive}

	controller, err := datacentercontroller.NewController(dcConfig, []string{"127.0.0.1:8080"})
	require.NoError(t, err)
	startCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	err = controller.Start(startCtx)
	cancel()
	require.NoError(t, err)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		_ = controller.Stop(stopCtx)
		stopCancel()
	})

	clusterMock := mockcluster.NewCluster(t)
	remotingMock := mocksremote.NewClient(t)

	// ActorExists and PutActor are called during Spawn when clusterEnabled is
	// true: the spawned replicator is synchronously published to the cluster
	clusterMock.EXPECT().ActorExists(mock.Anything, mock.Anything).Return(false, nil).Maybe()
	clusterMock.EXPECT().PutActor(mock.Anything, mock.Anything).Return(nil).Maybe()
	// Close is called during actor system shutdown
	remotingMock.EXPECT().Close().Maybe()

	impl.locker.Lock()
	impl.cluster = clusterMock
	impl.remoting = remotingMock
	impl.locker.Unlock()
	impl.clusterEnabled.Store(true)
	impl.remotingEnabled.Store(true)
	impl.dataCenterController = controller
	impl.clusterConfig = NewClusterConfig().WithDataCenter(dcConfig)

	config := crdt.NewConfig(
		crdt.WithDataCenterReplication(),
		crdt.WithDataCenterReplicationInterval(time.Hour),
		crdt.WithDataCenterSendTimeout(2*time.Second),
	)
	impl.extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{
		config: config,
		dc:     datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"},
	})

	replActor := newReplicatorActor()
	repl, err := sys.Spawn(ctx, "replicator", replActor, WithLongLived())
	require.NoError(t, err)
	require.NotNil(t, repl)
	pause.For(500 * time.Millisecond)

	// Disable cluster flag to prevent shutdown from trying to access
	// cluster-only fields (clusterNode, etc.) that aren't set up here.
	impl.clusterEnabled.Store(false)

	return sys, repl, replActor, clusterMock, remotingMock
}

// remoteRecords returns a ListActive function that produces the given records.
func remoteRecords(records []datacenter.DataCenterRecord) func(context.Context) ([]datacenter.DataCenterRecord, error) {
	return func(context.Context) ([]datacenter.DataCenterRecord, error) {
		return records, nil
	}
}

// spawnBenchReplicator creates a replicator for benchmarks.
func spawnBenchReplicator(b *testing.B) (ActorSystem, *PID) {
	b.Helper()
	ctx := context.TODO()
	sys, err := NewActorSystem("benchSys", WithLogger(log.DiscardLogger))
	require.NoError(b, err)
	require.NoError(b, sys.Start(ctx))

	config := crdt.NewConfig()
	impl := sys.(*actorSystem)
	impl.extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{config: config})
	repl, err := sys.Spawn(ctx, "replicator", newReplicatorActor(), WithLongLived())
	require.NoError(b, err)
	require.NotNil(b, repl)
	pause.For(500 * time.Millisecond)
	return sys, repl
}

// waitForRouteeCount blocks until the router reports exactly expected routees and fails the test otherwise.
func waitForRouteeCount(t *testing.T, ctx context.Context, router *PID, expected int) {
	t.Helper()
	require.Eventually(t, func() bool {
		response, err := Ask(ctx, router, new(GetRoutees), time.Second)
		if err != nil || response == nil {
			return false
		}
		routeesResponse, ok := response.(*Routees)
		if !ok || routeesResponse == nil {
			return false
		}
		return len(routeesResponse.Names()) == expected
	}, 5*time.Second, 100*time.Millisecond, "expected %d routees", expected)
}

// spawnConcurrently runs spawn from n goroutines released simultaneously and
// returns their results.
func spawnConcurrently(t *testing.T, n int, spawn func() (*PID, error)) ([]*PID, []error) {
	t.Helper()
	var wg sync.WaitGroup
	gate := make(chan struct{})
	pids := make([]*PID, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			pids[i], errs[i] = spawn()
		}(i)
	}
	close(gate)
	wg.Wait()
	return pids, errs
}

// requireSamePID asserts every spawn succeeded, is running, and returned the
// same PID.
func requireSamePID(t *testing.T, pids []*PID, errs []error) {
	t.Helper()
	for i, pid := range pids {
		require.NoErrorf(t, errs[i], "call %d failed", i)
		require.NotNilf(t, pid, "call %d returned a nil pid", i)
		require.Truef(t, pid.IsRunning(), "call %d returned a non-running pid", i)
		require.Truef(t, pids[0].Equals(pid), "call %d returned a different pid", i)
	}
}

// assertConcurrentStashedAsks fires repeated rounds of concurrent Asks
// against an actor that stashes each command and replies only after
// Unstash or UnstashAll. Dropped replies need context-pool churn to
// surface: a recycled receive context whose late-reply guard was left
// tripped silently fails Response and the caller times out.
func assertConcurrentStashedAsks(t *testing.T, name string, actor Actor) {
	t.Helper()

	ctx := context.TODO()

	system, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, system)

	require.NoError(t, system.Start(ctx))

	pause.For(time.Second)

	pid, err := system.Spawn(ctx, name, actor, WithStashing(), WithLongLived())
	require.NoError(t, err)
	require.NotNil(t, pid)

	pause.For(time.Second)

	const concurrency = 5
	const rounds = 40

	for range rounds {
		var wg sync.WaitGroup

		errs := make([]error, concurrency)
		responses := make([]any, concurrency)

		wg.Add(concurrency)

		for i := range concurrency {
			go func(idx int) {
				defer wg.Done()
				responses[idx], errs[idx] = Ask(ctx, pid, testpb.TestCount_builder{Value: int32(idx)}.Build(), 5*time.Second)
			}(i)
		}

		wg.Wait()

		for i := range concurrency {
			require.NoError(t, errs[i])
			reply, ok := responses[i].(*testpb.TestCount)
			require.True(t, ok)
			require.EqualValues(t, i, reply.GetValue())
		}
	}

	require.NoError(t, system.Stop(ctx))
}

// systemQueueMessage builds a context carrying message for the queue tests.
func systemQueueMessage(message any) *ReceiveContext {
	return &ReceiveContext{message: message}
}

// popMessages pops until the queue reports nothing and returns the messages
// in the order they came out.
func popMessages(queue *systemQueue) []any {
	var out []any
	for ctx := queue.pop(); ctx != nil; ctx = queue.pop() {
		out = append(out, ctx.Message())
	}

	return out
}

// withSenderLoadOrStoreStub swaps the sender load-or-store hook for stub and restores it through t.Cleanup.
func withSenderLoadOrStoreStub(t *testing.T, stub func(*sync.Map, string, any) (any, bool)) {
	t.Helper()
	original := senderLoadOrStoreFn.Load()
	senderLoadOrStoreFn.Store(&stub)
	t.Cleanup(func() {
		senderLoadOrStoreFn.Store(original)
	})
}

// makeReceiveContext returns a receive context sent by a remote PID named sender and carrying no message.
func makeReceiveContext(sender string) *ReceiveContext {
	return makeReceiveContextWithPayload(sender, "")
}

// makeReceiveContextWithPayload returns a receive context sent by a remote PID named sender, carrying payload.
func makeReceiveContextWithPayload(sender, payload string) *ReceiveContext {
	addr := address.New(sender, "test-system", "localhost", 0)
	return makeReceiveContextFromAddress(addr, payload)
}

// makeReceiveContextFromAddress returns a receive context sent from addr, carrying payload when it is not empty.
func makeReceiveContextFromAddress(addr *address.Address, payload string) *ReceiveContext {
	ctx := &ReceiveContext{sender: newRemotePID(addr, sharedRemotingForTests)}
	if payload != "" {
		ctx.message = &anypb.Any{TypeUrl: "test/payload", Value: []byte(payload)}
	}
	return ctx
}

// highestPriority orders messages so that a larger Priority value is served
// first.
func highestPriority(msg1, msg2 any) bool {
	p1 := msg1.(*testpb.TestMessage)
	p2 := msg2.(*testpb.TestMessage)
	return p1.GetPriority() > p2.GetPriority()
}
