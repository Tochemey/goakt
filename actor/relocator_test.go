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
	"context"
	"crypto/tls"
	stdErrors "errors"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/kapetan-io/tackle/autotls"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/address"
	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/pointer"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	mockscluster "github.com/tochemey/goakt/v4/mocks/cluster"
	mocksremote "github.com/tochemey/goakt/v4/mocks/remote"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/supervisor"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

type spawnSingletonSpy struct {
	*actorSystem
	called    bool
	actorName string
	actor     Actor
	config    *clusterSingletonConfig
}

func (s *spawnSingletonSpy) SpawnSingleton(ctx context.Context, name string, actor Actor, opts ...ClusterSingletonOption) error {
	s.called = true
	s.actorName = name
	s.actor = actor
	s.config = newClusterSingletonConfig(opts...)
	return nil
}

func TestRelocatorPeersError(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, system)

	sys := system.(*actorSystem)

	clusterMock := mockscluster.NewCluster(t)
	expectedErr := stdErrors.New("cluster failure")
	clusterMock.EXPECT().Peers(mock.Anything).Return(nil, expectedErr).Once()

	sys.cluster = clusterMock
	sys.relocationEnabled.Store(true)

	actor := &relocator{
		remoting: remote.NewRemoting(),
		pid: &PID{
			actorSystem: system,
		},
	}

	msg := &internalpb.Rebalance{PeerState: new(internalpb.PeerState)}
	receiveCtx := newReceiveContext(ctx, nil, actor.pid, msg)

	actor.Relocate(receiveCtx)

	errRecorded := receiveCtx.getError()
	require.Error(t, errRecorded)

	var internalErr *errors.InternalError
	require.ErrorAs(t, errRecorded, &internalErr)
	require.Contains(t, errRecorded.Error(), expectedErr.Error())
}

func TestRelocatorSpawnRemoteActorActorExistsError(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("relocator-actor-exists-error", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, system)

	sys := system.(*actorSystem)

	clusterMock := mockscluster.NewCluster(t)
	expectedErr := stdErrors.New("cluster ActorExists failure")
	clusterMock.EXPECT().ActorExists(mock.Anything, "relocated-actor").Return(false, expectedErr).Once()

	sys.relocationEnabled.Store(true)
	sys.cluster = clusterMock

	actor := &relocator{
		remoting: remote.NewRemoting(),
		pid: &PID{
			actorSystem: system,
		},
	}

	targetActor := &internalpb.Actor{
		Address: address.New("relocated-actor", "system", "127.0.0.1", 8080).String(),
	}
	targetPeer := &cluster.Peer{
		Host:         "127.0.0.1",
		RemotingPort: 8080,
	}

	err = actor.spawnRemoteActor(ctx, targetActor, targetPeer)
	require.Error(t, err)

	var internalErr *errors.InternalError
	require.ErrorAs(t, err, &internalErr)
	require.Contains(t, err.Error(), expectedErr.Error())
}

func TestRelocatorSpawnRemoteActorRemoveActorError(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, system)

	sys := system.(*actorSystem)

	clusterMock := mockscluster.NewCluster(t)
	clusterMock.EXPECT().ActorExists(mock.Anything, "relocated-actor").Return(true, nil).Once()
	expectedErr := fmt.Errorf("failed to remove actor from cluster")
	clusterMock.EXPECT().RemoveActor(mock.Anything, "relocated-actor").Return(expectedErr).Once()

	sys.cluster = clusterMock

	sys.relocationEnabled.Store(true)
	sys.cluster = clusterMock

	actor := &relocator{
		remoting: remote.NewRemoting(),
		pid: &PID{
			actorSystem: system,
		},
	}

	targetActor := &internalpb.Actor{
		Address: address.New("relocated-actor", "system", "127.0.0.1", 8080).String(),
	}
	targetPeer := &cluster.Peer{
		Host:         "127.0.0.1",
		RemotingPort: 8080,
	}

	err = actor.spawnRemoteActor(ctx, targetActor, targetPeer)
	require.Error(t, err)

	var internalErr *errors.InternalError
	require.ErrorAs(t, err, &internalErr)
	require.Contains(t, err.Error(), expectedErr.Error())
}

func TestRelocatorSpawnRemoteActorInvalidAddress(t *testing.T) {
	ctx := context.Background()

	system := MockReplicationTestSystem(mockscluster.NewCluster(t))

	actor := &relocator{
		remoting: remote.NewRemoting(),
		pid: &PID{
			actorSystem: system,
		},
	}

	targetActor := &internalpb.Actor{
		Address: "invalid-address",
	}
	targetPeer := &cluster.Peer{
		Host:         "127.0.0.1",
		RemotingPort: 8080,
	}

	err := actor.spawnRemoteActor(ctx, targetActor, targetPeer)
	require.Error(t, err)

	var internalErr *errors.InternalError
	require.ErrorAs(t, err, &internalErr)
	assert.ErrorContains(t, err, "address format is invalid")
}

func TestRelocatorSpawnRemoteActorSetsReentrancyConfig(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("relocator-reentrancy", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	sys := system.(*actorSystem)
	clusterMock := mockscluster.NewCluster(t)
	clusterMock.EXPECT().ActorExists(mock.Anything, "relocated-actor").Return(false, nil).Once()
	sys.cluster = clusterMock

	remotingMock := mocksremote.NewRemoting(t)
	remotingMock.EXPECT().RemoteSpawn(mock.Anything, "127.0.0.1", 9000, mock.Anything).
		Run(func(_ context.Context, _ string, _ int, req *remote.SpawnRequest) {
			require.NotNil(t, req.Reentrancy)
			require.Equal(t, reentrancy.StashNonReentrant, req.Reentrancy.Mode())
			require.Equal(t, 5, req.Reentrancy.MaxInFlight())
		}).
		Return(nil).
		Once()

	actor := &relocator{
		remoting: remotingMock,
		pid: &PID{
			actorSystem: system,
		},
		logger: log.DiscardLogger,
	}

	targetActor := &internalpb.Actor{
		Address: address.New("relocated-actor", system.Name(), "127.0.0.1", 8080).String(),
		Type:    "relocated-kind",
		Reentrancy: &internalpb.ReentrancyConfig{
			Mode:        internalpb.ReentrancyMode_REENTRANCY_MODE_STASH_NON_REENTRANT,
			MaxInFlight: 5,
		},
	}
	targetPeer := &cluster.Peer{
		Host:         "127.0.0.1",
		RemotingPort: 9000,
	}

	err = actor.spawnRemoteActor(ctx, targetActor, targetPeer)
	require.NoError(t, err)
}

func TestRelocatorRelocateActorsInvalidAddress(t *testing.T) {
	ctx := context.Background()

	actor := &relocator{}

	var eg errgroup.Group
	actor.relocateActors(ctx, &eg, []*internalpb.Actor{
		{Address: "invalid-address"},
	}, nil, nil)

	err := eg.Wait()
	require.Error(t, err)

	var spawnErr *errors.SpawnError
	require.ErrorAs(t, err, &spawnErr)
	assert.ErrorContains(t, err, "address format is invalid")
}

func TestRelocatorRelocateActorsInvalidAddressForPeerShare(t *testing.T) {
	ctx := context.Background()

	actor := &relocator{}

	var eg errgroup.Group
	actor.relocateActors(ctx, &eg, nil, [][]*internalpb.Actor{
		nil,
		{{Address: "invalid-address"}},
	}, []*cluster.Peer{
		{Host: "127.0.0.1", RemotingPort: 8080},
	})

	err := eg.Wait()
	require.Error(t, err)

	var spawnErr *errors.SpawnError
	require.ErrorAs(t, err, &spawnErr)
	assert.ErrorContains(t, err, "address format is invalid")
}

func TestRelocatorRecreateLocallyUsesSingletonSpec(t *testing.T) {
	ctx := context.Background()
	system := MockSingletonClusterReadyActorSystem(t)
	clusterMock := mockscluster.NewCluster(t)
	system.locker.Lock()
	system.cluster = clusterMock
	system.locker.Unlock()

	system.registry.Register(new(MockActor))

	singletonSpec := &internalpb.SingletonSpec{
		SpawnTimeout: durationpb.New(3 * time.Second),
		WaitInterval: durationpb.New(250 * time.Millisecond),
		MaxRetries:   int32(4),
	}
	props := &internalpb.Actor{
		Address: address.New("singleton", system.Name(), "127.0.0.1", 8080).String(),
		Type:    types.Name(new(MockActor)),
		Singleton: &internalpb.SingletonSpec{
			SpawnTimeout: singletonSpec.SpawnTimeout,
			WaitInterval: singletonSpec.WaitInterval,
			MaxRetries:   singletonSpec.MaxRetries,
		},
		Role: pointer.To("blue"),
	}

	clusterMock.EXPECT().RemoveActor(mock.Anything, "singleton").Return(nil).Once()

	spy := &spawnSingletonSpy{actorSystem: system}
	relocator := &relocator{pid: &PID{actorSystem: spy}}
	err := relocator.recreateLocally(ctx, props, true)
	require.NoError(t, err)

	require.True(t, spy.called)
	require.Equal(t, "singleton", spy.actorName)
	require.Equal(t, props.GetType(), types.Name(spy.actor))
	require.NotNil(t, spy.config)
	require.Equal(t, singletonSpec.SpawnTimeout.AsDuration(), spy.config.spawnTimeout)
	require.Equal(t, singletonSpec.WaitInterval.AsDuration(), spy.config.waitInterval)
	require.Equal(t, int(singletonSpec.MaxRetries), spy.config.numberOfRetries)
	require.NotNil(t, spy.config.Role())
	require.Equal(t, props.GetRole(), *spy.config.Role())
}

func TestRelocation(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor1-%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor2-%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	reentrantName := "Reentrant-Actor"
	pid, err := node2.Spawn(ctx, reentrantName, NewMockActor(),
		WithLongLived(),
		WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant), reentrancy.WithMaxInFlight(3))))
	require.NoError(t, err)
	require.NotNil(t, pid)

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor3-%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	// Verify actors are on node2 before shutdown
	actorName := "Actor2-1"
	node2Address := net.JoinHostPort(node2.Host(), strconv.Itoa(node2.Port()))
	addr, _, err := node1.ActorOf(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, addr)
	require.Equal(t, node2Address, addr.HostPort(), "Actor %s should be on node2 before shutdown", actorName)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Allow time for the cluster to detect node2 leaving and start relocation
	pause.For(2 * time.Second)

	// Wait for cluster rebalancing - verify actor relocation actually occurred.
	// The relocation process:
	// 1. Node2 shutdown: cleanupCluster removes actors from cluster map (synchronous)
	// 2. NodeLeft event is emitted and processed by leader
	// 3. Leader fetches peer state from cluster store and enqueues for relocation
	// 4. Relocator processes: recreateLocally removes actor from cluster map, then spawns locally
	// 5. New actor is registered in cluster map with NEW address (via putActorOnCluster)
	//
	// The test must verify the actor is relocated (has new address), not just that it exists.
	// During relocation, the actor may temporarily not exist (removed but not yet re-added),
	// so we accept either: actor doesn't exist (relocation in progress) OR actor exists with new address (relocation complete).
	// We reject: actor exists with old address (relocation hasn't started or failed).
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil {
			return false
		}
		if !exists {
			// Actor doesn't exist - this is OK during relocation (removed but not yet re-added)
			// We'll keep checking until it's re-added with new address
			return false
		}
		// Actor exists - verify it's on a live node (not node2)
		relocatedAddr, _, err := node1.ActorOf(ctx, actorName)
		if err != nil || relocatedAddr == nil {
			return false
		}
		actorAddr := relocatedAddr.HostPort()
		// Critical check: actor must have a NEW address (not node2's address)
		// If it still has node2's address, relocation hasn't happened yet
		return actorAddr != node2Address
	}, 2*time.Minute, 500*time.Millisecond, "Actor %s should be relocated from node2 (was %s) to a live node", actorName, node2Address)

	sender, err := node1.LocalActor("Actor1-1")
	require.NoError(t, err)
	require.NotNil(t, sender)

	// Actor should now exist, safe to send
	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.NoError(t, err)

	// Wait for reentrant actor to be relocated - verify it's on a live node
	// During relocation, the actor may temporarily not exist (removed but not yet re-added),
	// so we accept either: actor doesn't exist (relocation in progress) OR actor exists with new address (relocation complete).
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, reentrantName)
		if err != nil {
			return false
		}
		if !exists {
			// Actor doesn't exist - this is OK during relocation (removed but not yet re-added)
			// We'll keep checking until it's re-added with new address
			return false
		}
		// Verify actor is actually on a live node (node1 or node3), not on dead node2
		reentrantAddr, _, err := node1.ActorOf(ctx, reentrantName)
		if err != nil || reentrantAddr == nil {
			return false
		}
		actorAddr := reentrantAddr.HostPort()
		// Actor should be on node1 or node3, not on node2 (which is down)
		return actorAddr != node2Address
	}, 2*time.Minute, 500*time.Millisecond, "Reentrant actor %s should be relocated from node2 (was %s) to a live node", reentrantName, node2Address)

	reentrantAddr, pid, err := node1.ActorOf(ctx, reentrantName)
	require.NoError(t, err)
	if pid != nil {
		require.NotNil(t, pid.reentrancy)
		require.Equal(t, reentrancy.StashNonReentrant, pid.reentrancy.mode)
		require.Equal(t, 3, pid.reentrancy.maxInFlight)
	}

	if reentrantAddr.HostPort() == net.JoinHostPort(node1.Host(), strconv.Itoa(node1.Port())) {
		pid, err = node1.LocalActor(reentrantName)
		require.NoError(t, err)
		require.NotNil(t, pid.reentrancy)
		require.Equal(t, reentrancy.StashNonReentrant, pid.reentrancy.mode)
		require.Equal(t, 3, pid.reentrancy.maxInFlight)
	}

	if reentrantAddr.HostPort() == net.JoinHostPort(node3.Host(), strconv.Itoa(node3.Port())) {
		pid, err = node3.LocalActor(reentrantName)
		require.NoError(t, err)
		require.NotNil(t, pid.reentrancy)
		require.Equal(t, reentrancy.StashNonReentrant, pid.reentrancy.mode)
		require.Equal(t, 3, pid.reentrancy.maxInFlight)
	}

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRelocationWithCustomSupervisor(t *testing.T) {
	ctx := context.TODO()
	srv := startNatsServer(t)

	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	customSupervisor := supervisor.NewSupervisor(
		supervisor.WithStrategy(supervisor.OneForAllStrategy),
		supervisor.WithRetry(3, 2*time.Second),
		supervisor.WithDirective(&errors.InternalError{}, supervisor.RestartDirective),
	)

	actorName := "custom-supervised-actor"
	pid, err := node2.Spawn(ctx, actorName, NewMockActor(), WithSupervisor(customSupervisor))
	require.NoError(t, err)
	require.NotNil(t, pid)

	pause.For(time.Second)

	// Verify actor is on node2 before shutdown
	node2Address := net.JoinHostPort(node2.Host(), strconv.Itoa(node2.Port()))
	addr, _, err := node1.ActorOf(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, addr)
	require.Equal(t, node2Address, addr.HostPort(), "Actor %s should be on node2 before shutdown", actorName)

	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Allow time for the cluster to detect node2 leaving and start relocation
	pause.For(2 * time.Second)

	// Wait for relocation - verify actor exists and is on a live node (not node2)
	// The relocation process:
	// 1. Node2 shutdown: cleanupCluster removes actors from cluster map (synchronous)
	// 2. NodeLeft event is emitted and processed by leader
	// 3. Leader fetches peer state from cluster store and enqueues for relocation
	// 4. Relocator processes: recreateLocally removes actor from cluster map, then spawns locally
	// 5. New actor is registered in cluster map with NEW address (via putActorOnCluster)
	//
	// The test must verify the actor is relocated (has new address), not just that it exists.
	// During relocation, the actor may temporarily not exist (removed but not yet re-added),
	// so we accept either: actor doesn't exist (relocation in progress) OR actor exists with new address (relocation complete).
	// We reject: actor exists with old address (relocation hasn't started or failed).
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil {
			return false
		}
		if !exists {
			// Actor doesn't exist - this is OK during relocation (removed but not yet re-added)
			// We'll keep checking until it's re-added with new address
			return false
		}
		// Actor exists - verify it's on a live node (not node2)
		relocatedAddr, _, err := node1.ActorOf(ctx, actorName)
		if err != nil || relocatedAddr == nil {
			return false
		}
		actorAddr := relocatedAddr.HostPort()
		// Critical check: actor must have a NEW address (not node2's address)
		// If it still has node2's address, relocation hasn't happened yet
		return actorAddr != node2Address
	}, 2*time.Minute, 500*time.Millisecond, "Actor %s should be relocated from node2 (was %s) to a live node", actorName, node2Address)

	relocated, err := node1.LocalActor(actorName)
	require.NoError(t, err)
	require.NotNil(t, relocated)
	require.NotNil(t, relocated.supervisor)

	require.Equal(t, supervisor.OneForAllStrategy, relocated.supervisor.Strategy())
	require.EqualValues(t, 3, relocated.supervisor.MaxRetries())
	require.Equal(t, 2*time.Second, relocated.supervisor.Timeout())

	directive, ok := relocated.supervisor.Directive(&errors.InternalError{})
	require.True(t, ok)
	require.Equal(t, supervisor.RestartDirective, directive)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, sd1.Close())
	srv.Shutdown()
}

func TestRelocationWithTLS(t *testing.T) {
	t.Skip("Github actions is not stable with TLS tests")
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// AutoGenerate TLS certs
	serverConf := autotls.Config{
		CaFile:           "../test/data/certs/ca.cert",
		CertFile:         "../test/data/certs/auto.pem",
		KeyFile:          "../test/data/certs/auto.key",
		ClientAuthCaFile: "../test/data/certs/client-auth-ca.pem",
		ClientAuth:       tls.RequireAndVerifyClientCert,
	}
	require.NoError(t, autotls.Setup(&serverConf))

	clientConf := &autotls.Config{
		CertFile:           "../test/data/certs/client-auth.pem",
		KeyFile:            "../test/data/certs/client-auth.key",
		InsecureSkipVerify: true,
	}
	require.NoError(t, autotls.Setup(clientConf))

	serverConfig := serverConf.ServerTLS
	clientConfig := clientConf.ClientTLS
	serverConfig.NextProtos = []string{"h2", "http/1.1"}
	clientConfig.NextProtos = []string{"h2", "http/1.1"}

	// create and start system cluster
	node1, sd1 := testNATs(t, srv.Addr().String(), withTestTLS(serverConfig, clientConfig))
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start system cluster
	node2, sd2 := testNATs(t, srv.Addr().String(), withTestTLS(serverConfig, clientConfig))
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start system cluster
	node3, sd3 := testNATs(t, srv.Addr().String(), withTestTLS(serverConfig, clientConfig))
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node1-Actor-%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node2-Actor-%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node3-Actor-%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for cluster rebalancing
	pause.For(time.Minute)

	sender, err := node1.LocalActor("Node1-Actor-1")
	require.NoError(t, err)
	require.NotNil(t, sender)

	// let us access some of the node2 actors from node 1 and  node 3
	actorName := "Node2-Actor-1"
	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.NoError(t, err)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRelocationWithSingletonActor(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start system cluster
	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start system cluster
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start system cluster
	node3, sd3 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// create a singleton actor
	actorName := "actorName"
	err := node1.SpawnSingleton(ctx, actorName, NewMockActor())
	require.NoError(t, err)

	pause.For(time.Second)

	// Verify singleton is on node1 before shutdown
	node1Address := net.JoinHostPort(node1.Host(), strconv.Itoa(node1.Port()))
	addr, _, err := node2.ActorOf(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, addr)
	require.Equal(t, node1Address, addr.HostPort(), "Singleton %s should be on node1 before shutdown", actorName)

	// take down node1 since it is the first node created in the cluster
	require.NoError(t, node1.Stop(ctx))
	require.NoError(t, sd1.Close())

	// Allow time for the cluster to detect node1 leaving and start relocation
	pause.For(2 * time.Second)

	// Wait for singleton relocation - verify it exists and is on a live node (not node1)
	// The relocation process:
	// 1. Node1 shutdown: cleanupCluster removes actors from cluster map (synchronous)
	// 2. NodeLeft event is emitted and processed by leader
	// 3. Leader fetches peer state from cluster store and enqueues for relocation
	// 4. Relocator processes: recreateLocally removes actor from cluster map, then spawns locally
	// 5. New singleton is registered in cluster map with NEW address (via putActorOnCluster)
	//
	// The test must verify the singleton is relocated (has new address), not just that it exists.
	// During relocation, the singleton may temporarily not exist (removed but not yet re-added),
	// so we accept either: singleton doesn't exist (relocation in progress) OR singleton exists with new address (relocation complete).
	// We reject: singleton exists with old address (relocation hasn't started or failed).
	require.Eventually(t, func() bool {
		exists, err := node2.ActorExists(ctx, actorName)
		if err != nil {
			return false
		}
		if !exists {
			// Singleton doesn't exist - this is OK during relocation (removed but not yet re-added)
			// We'll keep checking until it's re-added with new address
			return false
		}
		// Singleton exists - verify it's on a live node (not node1)
		relocatedAddr, _, err := node2.ActorOf(ctx, actorName)
		if err != nil || relocatedAddr == nil {
			return false
		}
		actorAddr := relocatedAddr.HostPort()
		// Critical check: singleton must have a NEW address (not node1's address)
		// If it still has node1's address, relocation hasn't happened yet
		return actorAddr != node1Address
	}, 2*time.Minute, 500*time.Millisecond, "Singleton %s should be relocated from node1 (was %s) to a live node", actorName, node1Address)

	assert.NoError(t, node2.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd2.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRelocationWithActorRelocationDisabled(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start system cluster
	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start system cluster
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start system cluster
	node3, sd3 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node1-Actor-%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node2-Actor-%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor(), WithRelocationDisabled())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node3-Actor-%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for cluster rebalancing
	pause.For(time.Minute)

	sender, err := node1.LocalActor("Node1-Actor-1")
	require.NoError(t, err)
	require.NotNil(t, sender)

	// let us access some of the node2 actors from node 1 and  node 3
	actorName := "Node2-Actor-1"
	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.Error(t, err)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRelocationWithSystemRelocationDisabled(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String(), withoutTestRelocation())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String(), withoutTestRelocation())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testNATs(t, srv.Addr().String(), withoutTestRelocation())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node1-Actor-%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node2-Actor-%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node3-Actor-%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for cluster rebalancing
	pause.For(time.Second)

	actorName := "Node1-Actor-1"
	sender, err := node1.LocalActor(actorName)
	require.NoError(t, err)
	require.NotNil(t, sender)

	// let us access some of the node2 actors from node 1 and  node 3
	actorName = "Node2-Actor-1"
	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.Error(t, err)
	require.ErrorIs(t, err, errors.ErrActorNotFound)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRelocationWithExtension(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create the state store extension
	stateStoreExtension := NewMockExtension()

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String(), withMockExtension(stateStoreExtension))
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String(), withMockExtension(stateStoreExtension))
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testNATs(t, srv.Addr().String(), withMockExtension(stateStoreExtension))
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 entities on each node
	for j := 1; j <= 4; j++ {
		entityID := fmt.Sprintf("node1-entity-%d", j)
		pid, err := node1.Spawn(ctx, entityID, NewMockEntity(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)

		command := &testpb.CreateAccount{
			AccountBalance: 500.00,
		}
		_, err = Ask(ctx, pid, command, time.Minute)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		entityID := fmt.Sprintf("node2-entity-%d", j)
		pid, err := node2.Spawn(ctx, entityID, NewMockEntity(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)

		command := &testpb.CreateAccount{
			AccountBalance: 600.00,
		}
		_, err = Ask(ctx, pid, command, time.Minute)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		entityID := fmt.Sprintf("node3-entity-%d", j)
		pid, err := node3.Spawn(ctx, entityID, NewMockEntity(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)

		command := &testpb.CreateAccount{
			AccountBalance: 700.00,
		}
		_, err = Ask(ctx, pid, command, time.Minute)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	// Verify entity is on node2 before shutdown
	entityID := "node2-entity-1"
	node2Address := net.JoinHostPort(node2.Host(), strconv.Itoa(node2.Port()))
	addr, _, err := node1.ActorOf(ctx, entityID)
	require.NoError(t, err)
	require.NotNil(t, addr)
	require.Equal(t, node2Address, addr.HostPort(), "Entity %s should be on node2 before shutdown", entityID)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Allow time for the cluster to detect node2 leaving and start relocation
	pause.For(2 * time.Second)

	// Wait for relocation - verify entity exists and is on a live node (not node2)
	// The relocation process:
	// 1. Node2 shutdown: cleanupCluster removes actors from cluster map (synchronous)
	// 2. NodeLeft event is emitted and processed by leader
	// 3. Leader fetches peer state from cluster store and enqueues for relocation
	// 4. Relocator processes: recreateLocally removes actor from cluster map, then spawns locally
	// 5. New actor is registered in cluster map with NEW address (via putActorOnCluster)
	//
	// The test must verify the entity is relocated (has new address), not just that it exists.
	// During relocation, the entity may temporarily not exist (removed but not yet re-added),
	// so we accept either: entity doesn't exist (relocation in progress) OR entity exists with new address (relocation complete).
	// We reject: entity exists with old address (relocation hasn't started or failed).
	//
	// On CI, cluster state updates may take longer, so we use a longer check interval (1s instead of 500ms)
	// to reduce contention and allow cluster state to properly propagate.
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, entityID)
		if err != nil {
			return false
		}
		if !exists {
			// Entity doesn't exist - this is OK during relocation (removed but not yet re-added)
			// We'll keep checking until it's re-added with new address
			return false
		}
		// Entity exists - verify it's on a live node (not node2)
		relocatedAddr, pid, err := node1.ActorOf(ctx, entityID)
		if err != nil || relocatedAddr == nil {
			return false
		}

		// If the entity is local (pid != nil), it's definitely been relocated to this node
		if pid != nil {
			return true
		}

		actorAddr := relocatedAddr.HostPort()
		// Critical check: entity must have a NEW address (not node2's address)
		// If it still has node2's address, relocation hasn't happened yet
		return actorAddr != node2Address
	}, 2*time.Minute, time.Second, "Entity %s should be relocated from node2 (was %s) to a live node", entityID, node2Address)

	sender, err := node1.LocalActor("node1-entity-1")
	require.NoError(t, err)
	require.NotNil(t, sender)

	// let us access some of the node2 actors from node 1
	var response any
	response, err = sender.SendSync(ctx, entityID, new(testpb.GetAccount), time.Minute)
	require.NoError(t, err)
	account, ok := response.(*testpb.Account)
	require.True(t, ok)

	// the balance when creating that entity is 600
	require.EqualValues(t, 600, account.GetAccountBalance())

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRelocationWithDependency(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	dependencyID := "dependency"
	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		entityID := fmt.Sprintf("node1-actor-%d", j)
		// create the dependency
		dependency := NewMockDependency(dependencyID, entityID, "email")
		pid, err := node1.Spawn(ctx, entityID, NewMockActor(), WithDependencies(dependency), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		entityID := fmt.Sprintf("node2-actor-%d", j)
		// create the dependency
		dependency := NewMockDependency(dependencyID, entityID, "email")
		pid, err := node2.Spawn(ctx, entityID, NewMockActor(), WithDependencies(dependency), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	// Verify actor is on node2 before shutdown
	actorName := "node2-actor-1"
	node2Address := net.JoinHostPort(node2.Host(), strconv.Itoa(node2.Port()))
	addr, _, err := node1.ActorOf(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, addr)
	require.Equal(t, node2Address, addr.HostPort(), "Actor %s should be on node2 before shutdown", actorName)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Allow time for the cluster to detect node2 leaving and start relocation
	pause.For(2 * time.Second)

	// Wait for relocation - verify actor exists and is on a live node (not node2)
	// The relocation process:
	// 1. Node2 shutdown: cleanupCluster removes actors from cluster map (synchronous)
	// 2. NodeLeft event is emitted and processed by leader
	// 3. Leader fetches peer state from cluster store and enqueues for relocation
	// 4. Relocator processes: recreateLocally removes actor from cluster map, then spawns locally
	// 5. New actor is registered in cluster map with NEW address (via putActorOnCluster)
	//
	// The test must verify the actor is relocated (has new address), not just that it exists.
	// During relocation, the actor may temporarily not exist (removed but not yet re-added),
	// so we accept either: actor doesn't exist (relocation in progress) OR actor exists with new address (relocation complete).
	// We reject: actor exists with old address (relocation hasn't started or failed).
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil {
			return false
		}
		if !exists {
			// Actor doesn't exist - this is OK during relocation (removed but not yet re-added)
			// We'll keep checking until it's re-added with new address
			return false
		}
		// Actor exists - verify it's on a live node (not node2)
		relocatedAddr, _, err := node1.ActorOf(ctx, actorName)
		if err != nil || relocatedAddr == nil {
			return false
		}
		actorAddr := relocatedAddr.HostPort()
		// Critical check: actor must have a NEW address (not node2's address)
		// If it still has node2's address, relocation hasn't happened yet
		return actorAddr != node2Address
	}, 2*time.Minute, 500*time.Millisecond, "Actor %s should be relocated from node2 (was %s) to a live node", actorName, node2Address)

	sender, err := node1.LocalActor("node1-actor-1")
	require.NoError(t, err)
	require.NotNil(t, sender)

	// we know the actor will be on node 1
	pid, err := node1.LocalActor(actorName)
	require.NoError(t, err)
	require.NotNil(t, pid)
	actual := pid.Dependencies()
	require.NotNil(t, actual)
	require.Len(t, actual, 1)

	dep := pid.Dependency(dependencyID)
	require.NotNil(t, dep)
	mockdep := dep.(*MockDependency)
	require.Equal(t, actorName, mockdep.Username)
	require.Equal(t, "email", mockdep.Email)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, sd1.Close())
	srv.Shutdown()
}

func TestRelocationIssue781(t *testing.T) {
	// reference: https://github.com/Tochemey/goakt/issues/781
	// create a context
	ctx := t.Context()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor-1%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 5; j++ {
		actorName := fmt.Sprintf("Actor-2%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor-3%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	// let stop actor Actor-21 on node2
	actorName := "Actor-21"
	err := node2.Kill(ctx, actorName)
	require.NoError(t, err)

	pause.For(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for cluster rebalancing
	pause.For(time.Minute)

	sender, err := node1.LocalActor("Actor-11")
	require.NoError(t, err)
	require.NotNil(t, sender)

	pause.For(time.Second)

	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.Error(t, err)
	require.ErrorIs(t, err, errors.ErrActorNotFound)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

// nolint
func TestGrainsRelocation(t *testing.T) {
	// create a context
	ctx := t.Context()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	for j := range 4 {
		identity, err := node1.GrainIdentity(ctx, fmt.Sprintf("Grain-1%d", j), func(ctx context.Context) (Grain, error) {
			return NewMockGrain(), nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := new(testpb.TestSend)
		err = node1.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := range 5 {
		identity, err := node2.GrainIdentity(ctx, fmt.Sprintf("Grain-2%d", j), func(ctx context.Context) (Grain, error) {
			return NewMockGrain(), nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := new(testpb.TestSend)
		err = node2.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := range 4 {
		identity, err := node3.GrainIdentity(ctx, fmt.Sprintf("Grain-3%d", j), func(ctx context.Context) (Grain, error) {
			return NewMockGrain(), nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := new(testpb.TestSend)
		err = node3.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for each relocated grain to become reachable on a surviving node.
	// On CI, relocation can take considerably longer than 1 minute, so we
	// poll with require.Eventually instead of a fixed sleep.
	type grainCase struct {
		node ActorSystem
		name string
	}
	grainCases := []grainCase{
		{node3, "Grain-20"},
		{node1, "Grain-21"},
		{node3, "Grain-22"},
		{node1, "Grain-23"},
		{node1, "Grain-24"},
	}
	for _, gc := range grainCases {
		gc := gc
		require.Eventually(t, func() bool {
			identity, err := gc.node.GrainIdentity(ctx, gc.name, func(ctx context.Context) (Grain, error) {
				return NewMockGrain(), nil
			})
			if err != nil {
				return false
			}
			return gc.node.TellGrain(ctx, identity, new(testpb.TestSend)) == nil
		}, 2*time.Minute, time.Second, "grain %s should be accessible after relocation", gc.name)
	}

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

// nolint
func TestPersistenceGrainsRelocation(t *testing.T) {
	// create a context
	ctx := t.Context()
	// start the NATS server
	srv := startNatsServer(t)

	// create the state store extension
	stateStoreExtension := NewMockExtension()

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String(), withMockExtension(stateStoreExtension))
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String(), withMockExtension(stateStoreExtension))
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testNATs(t, srv.Addr().String(), withMockExtension(stateStoreExtension))
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	for j := range 4 {
		identity, err := node1.GrainIdentity(ctx, fmt.Sprintf("Grain-1%d", j), func(ctx context.Context) (Grain, error) {
			return NewMockPersistenceGrain(), nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)

		message := &testpb.CreateAccount{
			AccountBalance: 500.00,
		}
		err = node1.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := range 5 {
		identity, err := node2.GrainIdentity(ctx, fmt.Sprintf("Grain-2%d", j), func(ctx context.Context) (Grain, error) {
			return NewMockPersistenceGrain(), nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := &testpb.CreateAccount{
			AccountBalance: 500.00,
		}
		err = node2.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := range 4 {
		identity, err := node3.GrainIdentity(ctx, fmt.Sprintf("Grain-3%d", j), func(ctx context.Context) (Grain, error) {
			return NewMockPersistenceGrain(), nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := &testpb.CreateAccount{
			AccountBalance: 500.00,
		}
		err = node3.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// For each persistence grain that was on node2, we:
	//   1. Poll with require.Eventually until the grain is reachable and shows the
	//      pre-relocation balance (500). CreditAccount is not idempotent, so we must
	//      not send it until we are sure the grain is live on a surviving node.
	//   2. Then credit once with a generous timeout to account for grain activation
	//      latency on CI.
	type pgCase struct {
		node ActorSystem
		name string
	}
	pgCases := []pgCase{
		{node3, "Grain-20"},
		{node1, "Grain-21"},
		{node3, "Grain-22"},
		{node1, "Grain-23"},
		{node1, "Grain-24"},
	}
	for _, gc := range pgCases {
		gc := gc
		// Step 1: wait until grain is accessible with its pre-relocation balance.
		require.Eventually(t, func() bool {
			identity, err := gc.node.GrainIdentity(ctx, gc.name, func(ctx context.Context) (Grain, error) {
				return NewMockPersistenceGrain(), nil
			})
			if err != nil {
				return false
			}
			resp, err := gc.node.AskGrain(ctx, identity, new(testpb.GetAccount), 5*time.Second)
			if err != nil {
				return false
			}
			account, ok := resp.(*testpb.Account)
			return ok && account.GetAccountBalance() == 500
		}, 2*time.Minute, time.Second, "grain %s should be relocated and readable with balance 500", gc.name)

		// Step 2: credit exactly once and verify the final balance.
		identity, err := gc.node.GrainIdentity(ctx, gc.name, func(ctx context.Context) (Grain, error) {
			return NewMockPersistenceGrain(), nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)
		response, err := gc.node.AskGrain(ctx, identity, &testpb.CreditAccount{Balance: 500.00}, time.Minute)
		require.NoError(t, err)
		require.NotNil(t, response)
		actual := response.(*testpb.Account)
		require.EqualValues(t, 1000.00, actual.GetAccountBalance())
	}

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

// nolint
func TestGrainsWithDependenciesRelocation(t *testing.T) {
	// create a context
	ctx := t.Context()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	dependencyID := "dependency"

	for j := range 4 {
		name := fmt.Sprintf("Grain-1%d", j)
		email := fmt.Sprintf("email1%d", j)
		dependency := NewMockDependency(dependencyID, name, email)
		identity, err := node1.GrainIdentity(ctx, name, func(ctx context.Context) (Grain, error) {
			return NewMockGrain(), nil
		}, WithGrainDependencies(dependency))
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := new(testpb.TestSend)
		err = node1.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := range 5 {
		name := fmt.Sprintf("Grain-2%d", j)
		email := fmt.Sprintf("email2%d", j)
		dependency := NewMockDependency(dependencyID, name, email)
		identity, err := node2.GrainIdentity(ctx, name, func(ctx context.Context) (Grain, error) {
			return NewMockGrain(), nil
		}, WithGrainDependencies(dependency))
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := new(testpb.TestSend)
		err = node2.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := range 4 {
		name := fmt.Sprintf("Grain-3%d", j)
		email := fmt.Sprintf("email3%d", j)
		dependency := NewMockDependency(dependencyID, name, email)
		identity, err := node3.GrainIdentity(ctx, name, func(ctx context.Context) (Grain, error) {
			return NewMockGrain(), nil
		}, WithGrainDependencies(dependency))
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := new(testpb.TestSend)
		err = node3.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for each relocated grain (with dependencies) to become reachable on a
	// surviving node. A fixed sleep is unreliable on CI, so we poll instead.
	type gdCase struct {
		node       ActorSystem
		name       string
		email      string
		dependency string
	}
	gdCases := []gdCase{
		{node3, "Grain-20", "email20", dependencyID},
		{node1, "Grain-21", "email21", dependencyID},
		{node3, "Grain-22", "email22", dependencyID},
		{node1, "Grain-23", "email23", dependencyID},
		{node1, "Grain-24", "email24", dependencyID},
	}
	for _, gc := range gdCases {
		gc := gc
		require.Eventually(t, func() bool {
			identity, err := gc.node.GrainIdentity(ctx, gc.name, func(ctx context.Context) (Grain, error) {
				return NewMockGrain(), nil
			}, WithGrainDependencies(NewMockDependency(gc.dependency, gc.name, gc.email)))
			if err != nil {
				return false
			}
			return gc.node.TellGrain(ctx, identity, new(testpb.TestSend)) == nil
		}, 2*time.Minute, time.Second, "grain %s should be accessible after relocation", gc.name)
	}

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRelocationWithConsulProvider(t *testing.T) {
	t.Skip("Skipping relocation test with Consul provider")
	// create a context
	ctx := t.Context()
	agent := startConsulAgent(t)

	endpoint, err := agent.ApiEndpoint(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, endpoint)

	// wait for the agent to be ready
	pause.For(time.Second)

	// create and start a system cluster
	node1, sd1 := testConsul(t, endpoint)
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testConsul(t, endpoint)
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testConsul(t, endpoint)
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor1%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor2%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor3%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	// Verify actor is on node2 before shutdown
	actorName := "Actor21"
	node2Address := net.JoinHostPort(node2.Host(), strconv.Itoa(node2.Port()))
	addr, _, err := node1.ActorOf(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, addr)
	require.Equal(t, node2Address, addr.HostPort(), "Actor %s should be on node2 before shutdown", actorName)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for relocation - verify actor exists and is on a live node (not node2)
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil || !exists {
			return false
		}
		relocatedAddr, _, err := node1.ActorOf(ctx, actorName)
		if err != nil || relocatedAddr == nil {
			return false
		}
		return relocatedAddr.HostPort() != node2Address
	}, 2*time.Minute, time.Second, "Actor %s should be relocated from node2 to a live node", actorName)

	sender, err := node1.LocalActor("Actor11")
	require.NoError(t, err)
	require.NotNil(t, sender)

	// let us access some of the node2 actors from node 1 and  node 3
	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.NoError(t, err)

	require.NoError(t, node1.Stop(ctx))
	require.NoError(t, node3.Stop(ctx))
	require.NoError(t, sd1.Close())
	require.NoError(t, sd3.Close())
}

func TestRelocationWithEtcdProvider(t *testing.T) {
	t.Skip("Skipping relocation test with Etcd provider")
	// create a context
	ctx := t.Context()
	cluster := startEtcdCluster(t)

	endpoints, err := cluster.ClientEndpoints(ctx)
	require.NoError(t, err)

	// wait for the agent to be ready
	pause.For(time.Second)

	// create and start a system cluster
	node1, sd1 := testEtcd(t, endpoints[0])
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testEtcd(t, endpoints[0])
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testEtcd(t, endpoints[0])
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor1%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor2%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor3%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	// Verify actor is on node2 before shutdown
	actorName := "Actor21"
	node2Address := net.JoinHostPort(node2.Host(), strconv.Itoa(node2.Port()))
	addr, _, err := node1.ActorOf(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, addr)
	require.Equal(t, node2Address, addr.HostPort(), "Actor %s should be on node2 before shutdown", actorName)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Give the cluster time to detect the node failure and start relocation
	pause.For(2 * time.Second)

	// Wait for cluster rebalancing - verify actor relocation actually occurred.
	// The relocation process:
	// 1. Node2 shutdown: cleanupCluster removes actors from cluster map (synchronous)
	// 2. NodeLeft event is emitted and processed by leader
	// 3. Leader fetches peer state from cluster store and enqueues for relocation
	// 4. Relocator processes: recreateLocally removes actor from cluster map, then spawns locally
	// 5. New actor is registered in cluster map with NEW address (via putActorOnCluster)
	//
	// The test must verify the actor is relocated (has new address), not just that it exists.
	// During relocation, the actor may temporarily not exist (removed but not yet re-added),
	// so we accept either: actor doesn't exist (relocation in progress) OR actor exists with new address (relocation complete).
	// We reject: actor exists with old address (relocation hasn't started or failed).
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil {
			return false
		}
		if !exists {
			// Actor doesn't exist - this is OK during relocation (removed but not yet re-added)
			// We'll keep checking until it's re-added with new address
			return false
		}
		// Actor exists - verify it's on a live node (not node2)
		relocatedAddr, _, err := node1.ActorOf(ctx, actorName)
		if err != nil || relocatedAddr == nil {
			return false
		}
		actorAddr := relocatedAddr.HostPort()
		// Critical check: actor must have a NEW address (not node2's address)
		// If it still has node2's address, relocation hasn't happened yet
		return actorAddr != node2Address
	}, 2*time.Minute, time.Second, "Actor %s should be relocated from node2 (was %s) to a live node", actorName, node2Address)

	sender, err := node1.LocalActor("Actor11")
	require.NoError(t, err)
	require.NotNil(t, sender)

	// let us access some of the node2 actors from node 1 and  node 3
	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.NoError(t, err)

	require.NoError(t, node1.Stop(ctx))
	require.NoError(t, node3.Stop(ctx))
	require.NoError(t, sd1.Close())
	require.NoError(t, sd3.Close())
}
