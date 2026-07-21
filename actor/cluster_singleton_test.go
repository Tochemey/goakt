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
	stdErrors "errors"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/olric"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	mockcluster "github.com/tochemey/goakt/v4/mocks/cluster"
	mockremote "github.com/tochemey/goakt/v4/mocks/remoteclient"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/supervisor"
)

func TestSingletonActor(t *testing.T) {
	t.Run("With Singleton Actor", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		cl1, sd1 := testNATs(t, srv.Addr().String())
		require.NotNil(t, cl1)
		require.NotNil(t, sd1)

		cl2, sd2 := testNATs(t, srv.Addr().String())
		require.NotNil(t, cl2)
		require.NotNil(t, sd2)

		cl3, sd3 := testNATs(t, srv.Addr().String())
		require.NotNil(t, cl3)
		require.NotNil(t, sd3)

		pause.For(time.Second)

		// create a singleton actor
		actor := NewMockActor()
		actorName := "actorID"
		// create a singleton actor
		_, err := cl1.SpawnSingleton(ctx, actorName, actor)
		require.NoError(t, err)

		// a second singleton of the same kind under a different name also runs
		// (regression for https://github.com/Tochemey/goakt/issues/1272)
		secondName := "actorID2"
		_, err = cl2.SpawnSingleton(ctx, secondName, NewMockActor())
		require.NoError(t, err)

		// both singletons are resolvable cluster-wide by name
		pid1, err := cl3.ActorOf(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		pid2, err := cl3.ActorOf(ctx, secondName)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// respawning an existing singleton under the same name is idempotent
		_, err = cl2.SpawnSingleton(ctx, actorName, NewMockActor())
		require.NoError(t, err)

		// free resources
		require.NoError(t, cl3.Stop(ctx))
		require.NoError(t, sd3.Close())
		require.NoError(t, cl1.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, cl2.Stop(ctx))
		require.NoError(t, sd2.Close())
		// shutdown the nats server gracefully
		srv.Shutdown()
	})
	t.Run("With Singleton Actor returns error when retries overflow int32", func(t *testing.T) {
		if strconv.IntSize == 32 {
			t.Skip("int overflow case not representable on 32-bit platforms")
		}

		ctx := context.Background()
		system := MockSingletonClusterReadyActorSystem(t)
		clusterMock := mockcluster.NewCluster(t)
		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		retries := int(math.MaxInt32) + 1
		_, err := system.SpawnSingleton(ctx, "singleton", NewMockActor(), WithSingletonSpawnRetries(retries))
		require.Error(t, err)
		require.ErrorContains(t, err, "out of range for int32")
	})
	t.Run("With Singleton Actor when cluster is not enabled returns error", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		// create a singleton actor
		actor := NewMockActor()
		actorName := "actorID"
		// create a singleton actor
		_, err = newActorSystem.SpawnSingleton(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrClusterDisabled)

		err = newActorSystem.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With Singleton Actor when creating actor fails returns error", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		cl1, sd1 := testNATs(t, srv.Addr().String())
		require.NotNil(t, cl1)
		require.NotNil(t, sd1)

		// create a singleton actor
		actor := NewMockActor()
		actorName := strings.Repeat("a", 256)
		_, err := cl1.SpawnSingleton(ctx, actorName, actor)
		require.Error(t, err)

		// free resources
		require.NoError(t, cl1.Stop(ctx))
		require.NoError(t, sd1.Close())
		// shutdown the nats server gracefully
		srv.Shutdown()
	})
	t.Run("With Singleton Actor when not started returns error", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		require.False(t, newActorSystem.Running())

		// create a singleton actor
		actor := NewMockActor()
		actorName := "actorID"
		// create a singleton actor
		_, err = newActorSystem.SpawnSingleton(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
	})
	t.Run("Returns error when fetching peers fails", func(t *testing.T) {
		ctx := context.Background()
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)

		system := &actorSystem{remoting: rem}

		singletonSpec := &remote.SingletonSpec{
			SpawnTimeout: time.Second,
			WaitInterval: time.Millisecond,
			MaxRetries:   2,
		}

		expectedErr := fmt.Errorf("cluster unreachable")
		cl.EXPECT().Members(mock.Anything).Return(nil, expectedErr).Once()

		_, err := system.spawnSingletonOnLeader(ctx, cl, "singleton", NewMockActor(),
			singletonSpec.SpawnTimeout,
			singletonSpec.WaitInterval,
			singletonSpec.MaxRetries,
			nil)
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("Returns error when leader is not found", func(t *testing.T) {
		ctx := context.Background()
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)

		system := &actorSystem{remoting: rem}

		singletonSpec := &remote.SingletonSpec{
			SpawnTimeout: time.Second,
			WaitInterval: time.Millisecond,
			MaxRetries:   2,
		}

		cl.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{
				Host:         "127.0.0.1",
				PeersPort:    1001,
				Coordinator:  false,
				RemotingPort: 9090,
			},
			{
				Host:         "127.0.0.2",
				PeersPort:    1002,
				Coordinator:  false,
				RemotingPort: 9091,
			},
		}, nil).Once()

		_, err := system.spawnSingletonOnLeader(ctx, cl, "singleton", NewMockActor(),
			singletonSpec.SpawnTimeout,
			singletonSpec.WaitInterval,
			singletonSpec.MaxRetries,
			nil)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrLeaderNotFound)
	})

	t.Run("Returns error when remote spawn fails", func(t *testing.T) {
		ctx := context.Background()
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)

		system := &actorSystem{remoting: rem}

		leader := &cluster.Peer{
			Host:         "10.0.0.1",
			PeersPort:    1111,
			Coordinator:  true,
			RemotingPort: 2222,
		}

		cl.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			leader,
			{
				Host:         "10.0.0.2",
				PeersPort:    1112,
				Coordinator:  false,
				RemotingPort: 3333,
			},
		}, nil).Once()

		name := "singleton"
		actor := NewMockActor()
		expectedKind := types.Name(actor)
		expectedErr := fmt.Errorf("remote spawn failure")
		singletonSpec := &remote.SingletonSpec{
			SpawnTimeout: 5 * time.Second,
			WaitInterval: 500 * time.Millisecond,
			MaxRetries:   3,
		}

		rem.EXPECT().
			RemoteSpawn(mock.Anything, leader.Host, leader.RemotingPort, mock.MatchedBy(
				func(req *remote.SpawnRequest) bool {
					return req != nil &&
						req.Name == name &&
						req.Kind == expectedKind &&
						req.Singleton.SpawnTimeout == singletonSpec.SpawnTimeout &&
						req.Singleton.WaitInterval == singletonSpec.WaitInterval &&
						req.Singleton.MaxRetries == singletonSpec.MaxRetries
				}),
			).
			Return(nil, expectedErr).
			Once()

		_, err := system.spawnSingletonOnLeader(ctx, cl, name, actor,
			singletonSpec.SpawnTimeout,
			singletonSpec.WaitInterval,
			singletonSpec.MaxRetries,
			nil)
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)
	})
	t.Run("When not coordinator delegates to leader", func(t *testing.T) {
		ctx := context.Background()
		ports := dynaport.Get(3)

		clusterMock := mockcluster.NewCluster(t)
		remotingMock := mockremote.NewClient(t)
		system := MockSingletonClusterReadyActorSystem(t)
		system.remoting = remotingMock

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		leader := &cluster.Peer{
			Host:          "127.0.0.1",
			DiscoveryPort: ports[0],
			PeersPort:     ports[1],
			RemotingPort:  ports[2],
			Coordinator:   true,
		}

		singletonSpec := &remote.SingletonSpec{
			SpawnTimeout: 5 * time.Second,
			WaitInterval: 500 * time.Millisecond,
			MaxRetries:   3,
		}

		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{leader}, nil).Once()
		remotingMock.EXPECT().
			RemoteSpawn(
				mock.Anything,
				leader.Host,
				leader.RemotingPort,
				mock.MatchedBy(func(req *remote.SpawnRequest) bool {
					return req != nil &&
						req.Name == "singleton" &&
						req.Kind == "actor.mockactor" &&
						req.Singleton != nil &&
						req.Singleton.SpawnTimeout == singletonSpec.SpawnTimeout &&
						req.Singleton.WaitInterval == singletonSpec.WaitInterval &&
						req.Singleton.MaxRetries == singletonSpec.MaxRetries &&
						req.Role == nil
				}),
			).Return(new("goakt://test@127.0.0.1:9000/actor"), nil).Once()

		_, err := system.SpawnSingleton(ctx, "singleton", NewMockActor(),
			WithSingletonSpawnRetries(int(singletonSpec.MaxRetries)),
			WithSingletonSpawnWaitInterval(singletonSpec.WaitInterval),
			WithSingletonSpawnTimeout(singletonSpec.SpawnTimeout))
		require.NoError(t, err)
	})
	t.Run("With role and member error returns error", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		remotingMock := mockremote.NewClient(t)
		system := MockSingletonClusterReadyActorSystem(t)
		system.remoting = remotingMock

		singletonSpec := &remote.SingletonSpec{
			SpawnTimeout: time.Second,
			WaitInterval: time.Millisecond,
			MaxRetries:   2,
		}

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		expectedErr := fmt.Errorf("cluster unavailable")
		clusterMock.EXPECT().Members(mock.Anything).Return(nil, expectedErr).Once()

		_, err := system.spawnSingletonWithRole(ctx, clusterMock, "singleton", NewMockActor(), "blue",
			singletonSpec.SpawnTimeout,
			singletonSpec.WaitInterval,
			singletonSpec.MaxRetries,
			nil)
		require.Error(t, err)
		require.ErrorContains(t, err, expectedErr.Error())
	})
	t.Run("With role and no matching peers", func(t *testing.T) {
		ctx := context.Background()
		ports := dynaport.Get(3)

		clusterMock := mockcluster.NewCluster(t)
		remotingMock := mockremote.NewClient(t)
		system := MockSingletonClusterReadyActorSystem(t)
		system.remoting = remotingMock

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		singletonSpec := &remote.SingletonSpec{
			SpawnTimeout: time.Second,
			WaitInterval: time.Millisecond,
			MaxRetries:   2,
		}

		role := "roleA"
		clusterMock.EXPECT().
			Members(mock.Anything).
			Return([]*cluster.Peer{
				{
					Host:          "127.0.0.1",
					DiscoveryPort: ports[0],
					PeersPort:     ports[1],
					RemotingPort:  ports[2],
					Roles:         []string{"search"},
					CreatedAt:     time.Now().UnixNano(),
				},
			}, nil).
			Once()

		_, err := system.spawnSingletonWithRole(ctx, clusterMock, "singleton", NewMockActor(), role,
			singletonSpec.SpawnTimeout,
			singletonSpec.WaitInterval,
			singletonSpec.MaxRetries,
			nil)
		require.Error(t, err)
		require.EqualError(t, err, fmt.Sprintf("no cluster members found with role %s", role))
	})
	t.Run("With role and spawn on remote leader", func(t *testing.T) {
		ctx := context.Background()
		ports := dynaport.Get(3)

		clusterMock := mockcluster.NewCluster(t)
		remotingMock := mockremote.NewClient(t)
		system := MockSingletonClusterReadyActorSystem(t)
		system.remoting = remotingMock

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		t1 := time.Now().UnixNano()
		t2 := time.Now().Add(1 * time.Minute).UnixNano()
		role := "role"

		local := &cluster.Peer{
			Host:         system.clusterNode.Host,
			PeersPort:    system.clusterNode.PeersPort,
			RemotingPort: system.clusterNode.RemotingPort,
			Roles:        []string{role},
			CreatedAt:    t2,
		}
		remoteLeader := &cluster.Peer{
			Host:          "127.0.0.1",
			DiscoveryPort: ports[0],
			PeersPort:     ports[1],
			RemotingPort:  ports[2],
			Roles:         []string{role},
			CreatedAt:     t1,
		}

		clusterMock.EXPECT().
			Members(mock.Anything).
			Return([]*cluster.Peer{local, remoteLeader}, nil).
			Once()

		singletonSpec := &remote.SingletonSpec{
			SpawnTimeout: 5 * time.Second,
			WaitInterval: 500 * time.Millisecond,
			MaxRetries:   3,
		}

		singletonSupervisor := supervisor.NewSupervisor(supervisor.WithAnyErrorDirective(supervisor.RestartDirective))

		remotingMock.EXPECT().
			RemoteSpawn(
				mock.Anything,
				remoteLeader.Host,
				remoteLeader.RemotingPort,
				mock.MatchedBy(func(req *remote.SpawnRequest) bool {
					return req != nil &&
						req.Name == "singleton" &&
						req.Kind == "actor.mockactor" &&
						req.Singleton != nil &&
						req.Singleton.SpawnTimeout == singletonSpec.SpawnTimeout &&
						req.Singleton.WaitInterval == singletonSpec.WaitInterval &&
						req.Singleton.MaxRetries == singletonSpec.MaxRetries &&
						req.Role != nil &&
						*req.Role == role &&
						req.Supervisor == singletonSupervisor
				}),
			).Return(new("goakt://test@127.0.0.1:9000/actor"), nil).Once()

		_, err := system.spawnSingletonWithRole(ctx, clusterMock, "singleton", NewMockActor(), role,
			singletonSpec.SpawnTimeout,
			singletonSpec.WaitInterval,
			singletonSpec.MaxRetries,
			singletonSupervisor)
		require.NoError(t, err)
	})
	t.Run("With role happy path", func(t *testing.T) {
		ctx := context.Background()
		ports := dynaport.Get(3)
		clusterMock := mockcluster.NewCluster(t)
		remotingMock := mockremote.NewClient(t)
		system := MockSingletonClusterReadyActorSystem(t)
		system.remoting = remotingMock

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		t1 := time.Now().UnixNano()
		t2 := time.Now().Add(1 * time.Minute).UnixNano()

		role := "role"
		localPeer := &cluster.Peer{
			Host:         system.clusterNode.Host,
			PeersPort:    system.clusterNode.PeersPort,
			RemotingPort: system.clusterNode.RemotingPort,
			Roles:        []string{role},
			CreatedAt:    t1,
		}
		remotePeer := &cluster.Peer{
			Host:          "127.0.0.1",
			DiscoveryPort: ports[0],
			PeersPort:     ports[1],
			RemotingPort:  ports[2],
			Roles:         []string{role},
			CreatedAt:     t2,
		}

		clusterMock.EXPECT().
			Members(mock.Anything).
			Return([]*cluster.Peer{remotePeer, localPeer}, nil).
			Once()

		clusterMock.EXPECT().
			ActorExists(mock.Anything, "singleton").
			Return(false, nil).
			Once()

		clusterMock.EXPECT().
			PutActor(mock.Anything, mock.Anything).
			Return(nil).
			Once()

		singletonSpec := &remote.SingletonSpec{
			SpawnTimeout: time.Second,
			WaitInterval: time.Millisecond,
			MaxRetries:   2,
		}

		_, err := system.spawnSingletonWithRole(ctx, clusterMock, "singleton", NewMockActor(), role,
			singletonSpec.SpawnTimeout,
			singletonSpec.WaitInterval,
			singletonSpec.MaxRetries,
			nil)
		require.NoError(t, err)
		require.Equal(t, uint64(1), system.actorsCounter.Load())
	})
	t.Run("With role based Singleton Actor e2e", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		roles := []string{"backend", "api", "worker"}

		cl1, sd1 := testNATs(t, srv.Addr().String(), withMockRoles(roles...))
		require.NotNil(t, cl1)
		require.NotNil(t, sd1)

		cl2, sd2 := testNATs(t, srv.Addr().String(), withMockRoles(roles...))
		require.NotNil(t, cl2)
		require.NotNil(t, sd2)

		cl3, sd3 := testNATs(t, srv.Addr().String())
		require.NotNil(t, cl3)
		require.NotNil(t, sd3)

		pause.For(time.Second)

		// create a singleton actor
		actor := NewMockActor()
		actorName := "actorID"
		role := "api"
		// create a singleton actor
		_, err := cl1.SpawnSingleton(ctx, actorName, actor, WithSingletonRole(role))
		require.NoError(t, err)

		// attempt to create another singleton actor with the same actor
		_, err = cl2.SpawnSingleton(ctx, "actorName", actor)
		require.NoError(t, err)

		// a second role-based singleton of the same kind under a different name also runs
		// (regression for https://github.com/Tochemey/goakt/issues/1272)
		_, err = cl3.SpawnSingleton(ctx, "actor2", actor, WithSingletonRole(role))
		require.NoError(t, err)

		pid2, err := cl3.ActorOf(ctx, "actor2")
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// free resources
		require.NoError(t, cl3.Stop(ctx))
		require.NoError(t, sd3.Close())
		require.NoError(t, cl1.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, cl2.Stop(ctx))
		require.NoError(t, sd2.Close())
		// shutdown the nats server gracefully
		srv.Shutdown()
	})
}

// TestSpawnSingletonReturnsPID verifies that retrySpawnSingleton returns a non-nil PID
// when SpawnSingleton succeeds, covering the fix for the bug where the PID was discarded.
func TestSpawnSingletonReturnsPID(t *testing.T) {
	t.Run("returns non-nil remote PID when delegating to leader", func(t *testing.T) {
		ctx := context.Background()
		ports := dynaport.Get(3)

		clusterMock := mockcluster.NewCluster(t)
		remotingMock := mockremote.NewClient(t)
		system := MockSingletonClusterReadyActorSystem(t)
		system.remoting = remotingMock

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		// Leader is a different node (different remoting port) so we delegate via RemoteSpawn
		leader := &cluster.Peer{
			Host:          "127.0.0.1",
			DiscoveryPort: ports[0],
			PeersPort:     ports[1],
			RemotingPort:  ports[2] + 100, // Different port so we're not the leader
			Coordinator:   true,
		}

		expectedAddr := "goakt://test@127.0.0.1:9000/singleton"
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{leader}, nil).Once()
		remotingMock.EXPECT().
			RemoteSpawn(mock.Anything, leader.Host, leader.RemotingPort, mock.MatchedBy(func(req *remote.SpawnRequest) bool {
				return req != nil && req.Name == "singleton" && req.Kind == "actor.mockactor"
			})).
			Return(new(expectedAddr), nil).Once()

		pid, err := system.SpawnSingleton(ctx, "singleton", NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second))
		require.NoError(t, err)
		require.NotNil(t, pid, "retrySpawnSingleton must return non-nil PID on success")
		require.True(t, pid.IsRemote(), "delegated spawn must return remote PID")
		require.Equal(t, expectedAddr, pid.Path().String())
	})

	t.Run("returns non-nil local PID when spawning locally as coordinator", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		// Leader is local node - we spawn locally
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{
				Host:         system.clusterNode.Host,
				PeersPort:    system.clusterNode.PeersPort,
				RemotingPort: system.clusterNode.RemotingPort,
				Coordinator:  true,
			},
		}, nil).Once()
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(false, nil).Once()
		clusterMock.EXPECT().PutActor(mock.Anything, mock.Anything).Return(nil).Once()

		pid, err := system.SpawnSingleton(ctx, "singleton", NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second))
		require.NoError(t, err)
		require.NotNil(t, pid, "retrySpawnSingleton must return non-nil PID on success")
		require.False(t, pid.IsRemote(), "local spawn must return local PID")
		require.Equal(t, "singleton", pid.Name())
	})

	t.Run("returns non-nil remote PID after retry succeeds", func(t *testing.T) {
		ctx := context.Background()
		ports := dynaport.Get(3)

		clusterMock := mockcluster.NewCluster(t)
		remotingMock := mockremote.NewClient(t)
		system := MockSingletonClusterReadyActorSystem(t)
		system.remoting = remotingMock

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		leader := &cluster.Peer{
			Host:          "127.0.0.1",
			DiscoveryPort: ports[0],
			PeersPort:     ports[1],
			RemotingPort:  ports[2] + 100,
			Coordinator:   true,
		}

		expectedAddr := "goakt://test@127.0.0.1:9000/singleton"
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{leader}, nil).Times(2)
		remotingMock.EXPECT().
			RemoteSpawn(mock.Anything, leader.Host, leader.RemotingPort, mock.Anything).
			Return(nil, olric.ErrReadQuorum).Once()
		remotingMock.EXPECT().
			RemoteSpawn(mock.Anything, leader.Host, leader.RemotingPort, mock.Anything).
			Return(new(expectedAddr), nil).Once()

		pid, err := system.SpawnSingleton(ctx, "singleton", NewMockActor(),
			WithSingletonSpawnRetries(3),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second))
		require.NoError(t, err)
		require.NotNil(t, pid, "retrySpawnSingleton must return non-nil PID after retry succeeds")
		require.True(t, pid.IsRemote())
		require.Equal(t, expectedAddr, pid.Path().String())
	})
}

// TestSpawnSingletonRetryBehavior exercises retry and error paths for SpawnSingleton.
//
//nolint:gocyclo
func TestSpawnSingletonRetryBehavior(t *testing.T) {
	t.Run("retries on quorum errors", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{
				Host:         "127.0.0.1",
				PeersPort:    system.clusterNode.PeersPort,
				RemotingPort: system.clusterNode.RemotingPort,
				Coordinator:  true,
			},
		}, nil).Times(3)
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(false, olric.ErrWriteQuorum).Times(3)

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(3),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.ErrorIs(t, err, gerrors.ErrWriteQuorum)
	})

	t.Run("already exists under the same name short-circuits retries", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{
				Host:         "127.0.0.1",
				PeersPort:    system.clusterNode.PeersPort,
				RemotingPort: system.clusterNode.RemotingPort,
				Coordinator:  true,
			},
		}, nil).Once()
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(true, nil).Once()
		// The name is already bound to the singleton we wanted: idempotent success, no retry.
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(&internalpb.Actor{
			Address:   "goakt://test@127.0.0.1:0/singleton",
			Type:      "actor.mockactor",
			Singleton: &internalpb.SingletonSpec{},
		}, nil).Once()

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(3),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.NoError(t, err)
	})

	t.Run("stops when context is canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{
				Host:         "127.0.0.1",
				PeersPort:    system.clusterNode.PeersPort,
				RemotingPort: system.clusterNode.RemotingPort,
				Coordinator:  true,
			},
		}, nil).Once()
		// No ActorExists expectation: an already-canceled context is rejected by
		// runSpawnActivation before the spawn attempt reaches the cluster.

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("returns actor already exists when name is taken", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{
				Host:         "127.0.0.1",
				PeersPort:    system.clusterNode.PeersPort,
				RemotingPort: system.clusterNode.RemotingPort,
				Coordinator:  true,
			},
		}, nil).Once()
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(true, nil).Once()
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(&internalpb.Actor{
			Address: "singleton@test@127.0.0.1:0",
			Type:    "actor.other",
			// Singleton is nil: indicates this name is taken by a non-singleton (or unknown) actor.
			Singleton: nil,
		}, nil).Once()

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)
	})

	t.Run("retries when name conflict metadata is not yet visible", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		// Coordinator is local for both attempts.
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{
				Host:         system.clusterNode.Host,
				PeersPort:    system.clusterNode.PeersPort,
				RemotingPort: system.clusterNode.RemotingPort,
				Coordinator:  true,
			},
		}, nil).Times(2)
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(true, nil).Times(2)

		// Attempt 1: name exists but the registry record is not visible yet -> retry.
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(nil, cluster.ErrActorNotFound).Once()

		// Attempt 2: the record propagated and shows it is the intended singleton -> success.
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(&internalpb.Actor{
			Address:   "goakt://test@127.0.0.1:0/singleton",
			Type:      "actor.mockactor",
			Singleton: &internalpb.SingletonSpec{},
		}, nil).Once()

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.NoError(t, err)
	})

	t.Run("fails when ActorExists returns a non-retryable error", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		// Coordinator is local; spawn will attempt local singleton spawn.
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{
				Host:         system.clusterNode.Host,
				PeersPort:    system.clusterNode.PeersPort,
				RemotingPort: system.clusterNode.RemotingPort,
				Coordinator:  true,
			},
		}, nil).Once()

		existsErr := fmt.Errorf("actor exists lookup failed")
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(false, existsErr).Once()

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(1),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.ErrorContains(t, err, existsErr.Error())
	})

	t.Run("already exists on leader is treated as success", func(t *testing.T) {
		ctx := context.Background()
		ports := dynaport.Get(3)

		clusterMock := mockcluster.NewCluster(t)
		remotingMock := mockremote.NewClient(t)
		system := MockSingletonClusterReadyActorSystem(t)
		system.remoting = remotingMock

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		// The leader created the singleton; the delegating node confirms by name.
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(&internalpb.Actor{
			Address:   "goakt://test@127.0.0.1:0/singleton",
			Type:      "actor.mockactor",
			Singleton: &internalpb.SingletonSpec{},
		}, nil).Once()

		leader := &cluster.Peer{
			Host:         "127.0.0.1",
			RemotingPort: ports[2],
			Coordinator:  true,
		}
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{leader}, nil).Once()

		singletonSpec := &remote.SingletonSpec{
			SpawnTimeout: time.Second,
			WaitInterval: time.Millisecond,
			MaxRetries:   2,
		}

		remotingMock.EXPECT().
			RemoteSpawn(
				mock.Anything,
				leader.Host,
				leader.RemotingPort,
				mock.MatchedBy(func(req *remote.SpawnRequest) bool {
					return req != nil &&
						req.Name == "singleton" &&
						req.Kind == "actor.mockactor" &&
						req.Singleton != nil &&
						req.Singleton.SpawnTimeout == singletonSpec.SpawnTimeout &&
						req.Singleton.WaitInterval == singletonSpec.WaitInterval &&
						req.Singleton.MaxRetries == singletonSpec.MaxRetries &&
						req.Role == nil
				}),
			).
			Return(nil, gerrors.ErrActorAlreadyExists).
			Once()

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.NoError(t, err)
	})

	t.Run("name collision is treated as success when it is the same singleton", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{
				Host:         "127.0.0.1",
				PeersPort:    system.clusterNode.PeersPort,
				RemotingPort: system.clusterNode.RemotingPort,
				Coordinator:  true,
			},
		}, nil).Once()
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(true, nil).Once()
		// The name-based disambiguation proves the existing actor is the singleton we
		// wanted (idempotent success).
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(&internalpb.Actor{
			Address:   "goakt://test@127.0.0.1:0/singleton",
			Type:      "actor.mockactor",
			Singleton: &internalpb.SingletonSpec{},
		}, nil).Once()

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.NoError(t, err)
	})

	t.Run("idempotent success resolves the confirmed address without a second lookup", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		remotingMock := mockremote.NewClient(t)
		system := MockSingletonClusterReadyActorSystem(t)
		system.remoting = remotingMock

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		// Local coordinator: the local spawn reports the name is already taken.
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{
				Host:         system.clusterNode.Host,
				PeersPort:    system.clusterNode.PeersPort,
				RemotingPort: system.clusterNode.RemotingPort,
				Coordinator:  true,
			},
		}, nil).Once()
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(true, nil).Once()

		// The conflict handler confirms the name is bound to our singleton with a single
		// GetActor call. The PID must then be resolved from this record's address WITHOUT a
		// second lookup — the .Once() below fails if the old ActorOf re-resolution returns.
		// This is the regression guard: a creation call must never leak ErrActorNotFound
		// on the idempotent path just because a second read momentarily 404s.
		const confirmedAddr = "goakt://test@127.0.0.1:9000/singleton"
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(&internalpb.Actor{
			Address:   confirmedAddr,
			Type:      "actor.mockactor",
			Singleton: &internalpb.SingletonSpec{},
		}, nil).Once()

		pid, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.NoError(t, err)
		require.NotErrorIs(t, err, gerrors.ErrActorNotFound)
		require.NotNil(t, pid)
		require.True(t, pid.IsRemote())
		require.Equal(t, confirmedAddr, pid.Path().String())
	})

	t.Run("never returns ErrActorNotFound to the caller on the idempotent path", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		// Local coordinator: the local spawn reports the name is already taken.
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{
				Host:         system.clusterNode.Host,
				PeersPort:    system.clusterNode.PeersPort,
				RemotingPort: system.clusterNode.RemotingPort,
				Coordinator:  true,
			},
		}, nil).Once()
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(true, nil).Once()

		// Confirmation succeeds (it is our singleton) but the record carries no address,
		// which forces the by-name fallback resolution...
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(&internalpb.Actor{
			Address:   "",
			Type:      "actor.mockactor",
			Singleton: &internalpb.SingletonSpec{},
		}, nil).Once()
		// ...which momentarily 404s while the registry record propagates.
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(nil, cluster.ErrActorNotFound).Once()

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.Error(t, err)
		// A creation call must never tell the caller the actor it just created was not found.
		require.NotErrorIs(t, err, gerrors.ErrActorNotFound)
	})

	t.Run("retries when GetActor returns retryable error during name conflict", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		// Coordinator is local for both attempts.
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{
				Host:         system.clusterNode.Host,
				PeersPort:    system.clusterNode.PeersPort,
				RemotingPort: system.clusterNode.RemotingPort,
				Coordinator:  true,
			},
		}, nil).Times(2)

		// Attempt 1: name exists, GetActor fails transiently -> retry.
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(true, nil).Times(2)
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(nil, cluster.ErrEngineNotRunning).Once()

		// Attempt 2: name exists, cluster metadata shows it is the intended singleton -> success.
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(&internalpb.Actor{
			Address:   "goakt://test@127.0.0.1:0/singleton",
			Type:      "actor.mockactor",
			Singleton: &internalpb.SingletonSpec{},
		}, nil).Once()

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.NoError(t, err)
	})

	t.Run("fails when GetActor returns non-retryable error during name conflict", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{
				Host:         system.clusterNode.Host,
				PeersPort:    system.clusterNode.PeersPort,
				RemotingPort: system.clusterNode.RemotingPort,
				Coordinator:  true,
			},
		}, nil).Once()

		getErr := fmt.Errorf("get actor failed")
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(true, nil).Once()
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(nil, getErr).Once()

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(1),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.ErrorContains(t, err, getErr.Error())
	})

	t.Run("name conflict fails when existing actor is a different singleton", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{
				Host:         system.clusterNode.Host,
				PeersPort:    system.clusterNode.PeersPort,
				RemotingPort: system.clusterNode.RemotingPort,
				Coordinator:  true,
			},
		}, nil).Once()

		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(true, nil).Once()
		// Existing actor is a singleton, but not the intended kind/role.
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(&internalpb.Actor{
			Address:   "singleton@test@127.0.0.1:0",
			Type:      "actor.other",
			Singleton: &internalpb.SingletonSpec{},
		}, nil).Once()

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(1),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		// This should surface as an "actor already exists" name conflict.
		require.ErrorContains(t, err, gerrors.ErrActorAlreadyExists.Error())
	})

	t.Run("retries when cluster engine is not running", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		clusterMock.EXPECT().Members(mock.Anything).Return(nil, cluster.ErrEngineNotRunning).Times(2)

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.ErrorIs(t, err, cluster.ErrEngineNotRunning)
	})

	t.Run("returns error when no eligible role members exist", func(t *testing.T) {
		ctx := context.Background()
		ports := dynaport.Get(3)

		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		role := "control"
		clusterMock.EXPECT().
			Members(mock.Anything).
			Return([]*cluster.Peer{
				{
					Host:          "127.0.0.1",
					DiscoveryPort: ports[0],
					PeersPort:     ports[1],
					RemotingPort:  ports[2],
					Roles:         []string{"worker"},
					CreatedAt:     time.Now().UnixNano(),
				},
			}, nil).
			Times(2)

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonRole(role),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.Error(t, err)
		require.ErrorContains(t, err, fmt.Sprintf("no cluster members found with role %s", role))
	})

	t.Run("retries until a role member becomes available", func(t *testing.T) {
		ctx := context.Background()
		ports := dynaport.Get(6)

		clusterMock := mockcluster.NewCluster(t)
		remotingMock := mockremote.NewClient(t)
		system := MockSingletonClusterReadyActorSystem(t)
		system.remoting = remotingMock

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		role := "control"
		clusterMock.EXPECT().
			Members(mock.Anything).
			Return([]*cluster.Peer{
				{
					Host:          "127.0.0.1",
					DiscoveryPort: ports[0],
					PeersPort:     ports[1],
					RemotingPort:  ports[2],
					Roles:         []string{"worker"},
					CreatedAt:     time.Now().UnixNano(),
				},
			}, nil).
			Once()

		rolePeer := &cluster.Peer{
			Host:          "127.0.0.2",
			DiscoveryPort: ports[3],
			PeersPort:     ports[4],
			RemotingPort:  ports[5],
			Roles:         []string{role},
			CreatedAt:     time.Now().Add(1 * time.Minute).UnixNano(),
		}

		clusterMock.EXPECT().
			Members(mock.Anything).
			Return([]*cluster.Peer{rolePeer}, nil).
			Once()

		singletonSpec := &remote.SingletonSpec{
			SpawnTimeout: time.Second,
			WaitInterval: time.Millisecond,
			MaxRetries:   2,
		}

		remotingMock.EXPECT().
			RemoteSpawn(
				mock.Anything,
				rolePeer.Host,
				rolePeer.RemotingPort,
				mock.MatchedBy(func(req *remote.SpawnRequest) bool {
					return req != nil &&
						req.Name == "singleton" &&
						req.Kind == "actor.mockactor" &&
						req.Singleton != nil &&
						req.Singleton.SpawnTimeout == singletonSpec.SpawnTimeout &&
						req.Singleton.WaitInterval == singletonSpec.WaitInterval &&
						req.Singleton.MaxRetries == singletonSpec.MaxRetries &&
						req.Role != nil &&
						*req.Role == role
				}),
			).
			Return(new("goakt://test@127.0.0.1:9000/actor"), nil).
			Once()

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonRole(role),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.NoError(t, err)
	})

	t.Run("retries until a leader is elected", func(t *testing.T) {
		ctx := context.Background()
		ports := dynaport.Get(3)

		clusterMock := mockcluster.NewCluster(t)
		remotingMock := mockremote.NewClient(t)
		system := MockSingletonClusterReadyActorSystem(t)
		system.remoting = remotingMock

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		leader := &cluster.Peer{
			Host:         "127.0.0.1",
			RemotingPort: ports[2],
			Coordinator:  true,
		}

		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{}, nil).Once()
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{leader}, nil).Once()

		singletonSpec := &remote.SingletonSpec{
			SpawnTimeout: time.Second,
			WaitInterval: time.Millisecond,
			MaxRetries:   2,
		}

		remotingMock.EXPECT().
			RemoteSpawn(
				mock.Anything,
				leader.Host,
				leader.RemotingPort,
				mock.MatchedBy(func(req *remote.SpawnRequest) bool {
					return req != nil &&
						req.Name == "singleton" &&
						req.Kind == "actor.mockactor" &&
						req.Singleton != nil &&
						req.Singleton.SpawnTimeout == singletonSpec.SpawnTimeout &&
						req.Singleton.WaitInterval == singletonSpec.WaitInterval &&
						req.Singleton.MaxRetries == singletonSpec.MaxRetries &&
						req.Role == nil
				}),
			).
			Return(new("goakt://test@127.0.0.1:9000/actor"), nil).
			Once()

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.NoError(t, err)
	})

	t.Run("retries when the leader is temporarily unavailable", func(t *testing.T) {
		cases := []struct {
			name string
			err  error
		}{
			{
				name: "connection refused",
				err:  fmt.Errorf("dial tcp 127.0.0.1:9090: %w", syscall.ECONNREFUSED),
			},
			{
				name: "deadline exceeded",
				err:  context.DeadlineExceeded,
			},
			// Leader-delegated transient conditions: the leader's failures are serialized to
			// proto codes and de-serialized by the client into these error values (see
			// checkProtoError). Before the fix shouldRetrySpawnSingleton did not recognize
			// them and SpawnSingleton failed terminally during membership churn (issue #1209).
			{
				name: "remote send failure (leader quorum error)",
				err:  gerrors.ErrRemoteSendFailure,
			},
			{
				name: "request timeout (leader deadline exceeded)",
				err:  gerrors.ErrRequestTimeout,
			},
			{
				name: "address not found (stale coordinator view)",
				err:  gerrors.ErrAddressNotFound,
			},
			{
				name: "invalid response (leader shutting down)",
				err:  gerrors.ErrInvalidResponse,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ctx := context.Background()
				ports := dynaport.Get(3)

				clusterMock := mockcluster.NewCluster(t)
				remotingMock := mockremote.NewClient(t)
				system := MockSingletonClusterReadyActorSystem(t)
				system.remoting = remotingMock

				system.locker.Lock()
				system.cluster = clusterMock
				system.locker.Unlock()

				leader := &cluster.Peer{
					Host:         "127.0.0.1",
					RemotingPort: ports[2],
					Coordinator:  true,
				}

				clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{leader}, nil).Times(2)

				singletonSpec := &remote.SingletonSpec{
					SpawnTimeout: time.Second,
					WaitInterval: time.Millisecond,
					MaxRetries:   2,
				}

				remotingMock.EXPECT().
					RemoteSpawn(
						mock.Anything,
						leader.Host,
						leader.RemotingPort,
						mock.MatchedBy(func(req *remote.SpawnRequest) bool {
							return req != nil &&
								req.Name == "singleton" &&
								req.Kind == "actor.mockactor" &&
								req.Singleton != nil &&
								req.Singleton.SpawnTimeout == singletonSpec.SpawnTimeout &&
								req.Singleton.WaitInterval == singletonSpec.WaitInterval &&
								req.Singleton.MaxRetries == singletonSpec.MaxRetries &&
								req.Role == nil
						}),
					).
					Return(nil, tc.err).
					Once()

				remotingMock.EXPECT().
					RemoteSpawn(
						mock.Anything,
						leader.Host,
						leader.RemotingPort,
						mock.MatchedBy(func(req *remote.SpawnRequest) bool {
							return req != nil &&
								req.Name == "singleton" &&
								req.Kind == "actor.mockactor" &&
								req.Singleton != nil &&
								req.Singleton.SpawnTimeout == singletonSpec.SpawnTimeout &&
								req.Singleton.WaitInterval == singletonSpec.WaitInterval &&
								req.Singleton.MaxRetries == singletonSpec.MaxRetries &&
								req.Role == nil
						}),
					).
					Return(new("goakt://test@127.0.0.1:9000/actor"), nil).
					Once()

				_, err := system.SpawnSingleton(
					ctx,
					"singleton",
					NewMockActor(),
					WithSingletonSpawnRetries(2),
					WithSingletonSpawnWaitInterval(time.Millisecond),
					WithSingletonSpawnTimeout(time.Second),
				)
				require.NoError(t, err)
			})
		}
	})

	t.Run("surfaces leader-delegated transient error after the retry budget is exhausted", func(t *testing.T) {
		ctx := context.Background()
		ports := dynaport.Get(3)

		clusterMock := mockcluster.NewCluster(t)
		remotingMock := mockremote.NewClient(t)
		system := MockSingletonClusterReadyActorSystem(t)
		system.remoting = remotingMock

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		leader := &cluster.Peer{
			Host:         "127.0.0.1",
			RemotingPort: ports[2],
			Coordinator:  true,
		}

		// The leader keeps returning a transient remote send failure for every attempt:
		// the retrier must spend its full budget rather than stopping on the first
		// occurrence, then surface the error.
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{leader}, nil).Times(2)
		remotingMock.EXPECT().
			RemoteSpawn(mock.Anything, leader.Host, leader.RemotingPort, mock.Anything).
			Return(nil, gerrors.ErrRemoteSendFailure).
			Times(2)

		_, err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.ErrorIs(t, err, gerrors.ErrRemoteSendFailure)
	})
}

// TestResolveExistingSingleton covers the idempotent-success resolution helper used by
// retrySpawnSingleton: it must prefer a live local PID, otherwise build a remote PID from
// the address confirmed during conflict handling, and only fall back to a by-name lookup
// when no address was captured.
func TestResolveExistingSingleton(t *testing.T) {
	t.Run("prefers a live local PID over the confirmed address", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() {
			require.NoError(t, sys.Stop(ctx))
		})

		actorSys := sys.(*actorSystem)
		const actorName = "local-singleton"
		localPID, err := actorSys.configPID(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NoError(t, actorSys.tree().addNode(actorSys.getUserGuardian(), localPID))

		// A remote address is supplied, but the singleton is hosted locally: the live local
		// PID must win so we never hand back a remote proxy to ourselves.
		got, err := actorSys.resolveExistingSingleton(ctx, actorName, "goakt://test@127.0.0.1:9000/local-singleton")
		require.NoError(t, err)
		require.NotNil(t, got)
		require.False(t, got.IsRemote())
		require.Same(t, localPID, got)
	})

	t.Run("builds a remote PID from the confirmed address when not hosted locally", func(t *testing.T) {
		ctx := context.Background()
		remotingMock := mockremote.NewClient(t)
		system := MockSingletonClusterReadyActorSystem(t)
		system.remoting = remotingMock

		const confirmedAddr = "goakt://test@127.0.0.1:9000/remote-singleton"
		got, err := system.resolveExistingSingleton(ctx, "remote-singleton", confirmedAddr)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.True(t, got.IsRemote())
		require.Equal(t, confirmedAddr, got.Path().String())
	})

	t.Run("falls back to ActorOf when no address was captured", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		remotingMock := mockremote.NewClient(t)
		system := MockSingletonClusterReadyActorSystem(t)
		system.remoting = remotingMock

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		const resolvedAddr = "goakt://test@127.0.0.1:9000/fallback-singleton"
		clusterMock.EXPECT().GetActor(mock.Anything, "fallback-singleton").Return(&internalpb.Actor{
			Address:   resolvedAddr,
			Type:      "actor.mockactor",
			Singleton: &internalpb.SingletonSpec{},
		}, nil).Once()

		got, err := system.resolveExistingSingleton(ctx, "fallback-singleton", "")
		require.NoError(t, err)
		require.NotNil(t, got)
		require.True(t, got.IsRemote())
		require.Equal(t, resolvedAddr, got.Path().String())
	})

	t.Run("never surfaces ErrActorNotFound when the fallback lookup 404s", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		// No confirmed address, and the by-name fallback cannot see the record yet.
		clusterMock.EXPECT().GetActor(mock.Anything, "ghost-singleton").Return(nil, cluster.ErrActorNotFound).Once()

		got, err := system.resolveExistingSingleton(ctx, "ghost-singleton", "")
		require.Error(t, err)
		require.Nil(t, got)
		// The singleton was confirmed to exist upstream, so a creation call must never
		// leak ErrActorNotFound to the caller.
		require.NotErrorIs(t, err, gerrors.ErrActorNotFound)
	})
}

func TestShouldRetrySpawnSingleton(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "leader not found", err: gerrors.ErrLeaderNotFound, want: true},
		{name: "engine not running", err: cluster.ErrEngineNotRunning, want: true},
		{name: "no role members", err: errNoRoleMembers{role: "control"}, want: true},
		{name: "context deadline exceeded", err: context.DeadlineExceeded, want: true},
		{name: "connection refused", err: fmt.Errorf("dial: %w", syscall.ECONNREFUSED), want: true},
		// Leader-delegated transient conditions (issue #1209).
		{name: "remote send failure", err: gerrors.ErrRemoteSendFailure, want: true},
		{name: "wrapped remote send failure", err: fmt.Errorf("spawn: %w", gerrors.ErrRemoteSendFailure), want: true},
		{name: "request timeout", err: gerrors.ErrRequestTimeout, want: true},
		{name: "address not found", err: gerrors.ErrAddressNotFound, want: true},
		{name: "invalid response", err: gerrors.ErrInvalidResponse, want: true},
		// Terminal conditions must not be retried.
		{name: "singleton already exists", err: gerrors.ErrSingletonAlreadyExists, want: false}, //nolint:staticcheck // old-version hosts still emit it
		{name: "type not registered", err: gerrors.ErrTypeNotRegistered, want: false},
		{name: "arbitrary error", err: stdErrors.New("boom"), want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, shouldRetrySpawnSingleton(tc.err))
		})
	}
}

// TestConcurrentSpawnSingletonSingleInstance is an integration test that hammers
// SpawnSingleton for the same name concurrently from every node in a real
// (NATS-backed) cluster. It asserts the singleton converges to a single
// instance: every call succeeds and resolves to the same address, exactly one
// node hosts it locally, and it is resolvable cluster-wide from every node.
func TestConcurrentSpawnSingletonSingleInstance(t *testing.T) {
	ctx := context.TODO()
	srv := startNatsServer(t)

	cl1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, cl1)
	cl2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, cl2)
	cl3, sd3 := testNATs(t, srv.Addr().String())
	require.NotNil(t, cl3)

	// let the cluster settle
	pause.For(time.Second)

	systems := []ActorSystem{cl1, cl2, cl3}
	const perNode = 5
	total := len(systems) * perNode
	name := "concurrent-singleton"

	var (
		wg   sync.WaitGroup
		gate = make(chan struct{})
		pids = make([]*PID, total)
		errs = make([]error, total)
	)

	idx := 0
	for _, sys := range systems {
		for j := 0; j < perNode; j++ {
			i := idx
			s := sys
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-gate
				pids[i], errs[i] = s.SpawnSingleton(ctx, name, NewMockActor())
			}()
			idx++
		}
	}
	close(gate)
	wg.Wait()

	// every concurrent call succeeds and resolves to the same singleton address
	var addr string
	for i := 0; i < total; i++ {
		require.NoErrorf(t, errs[i], "call %d failed", i)
		require.NotNilf(t, pids[i], "call %d returned a nil pid", i)
		if addr == "" {
			addr = pids[i].ID()
			continue
		}
		require.Equalf(t, addr, pids[i].ID(), "call %d resolved to a different address", i)
	}

	// exactly one node hosts the singleton locally (a single live instance)
	localHosts := 0
	for _, sys := range systems {
		if node, ok := sys.tree().nodeByName(name); ok {
			if pid := node.value(); pid != nil && pid.IsRunning() {
				localHosts++
			}
		}
	}
	require.Equal(t, 1, localHosts, "singleton must be hosted on exactly one node")

	// resolvable to the same address from every node
	for _, sys := range systems {
		pid, err := sys.ActorOf(ctx, name)
		require.NoError(t, err)
		require.NotNil(t, pid)
		require.Equal(t, addr, pid.ID())
	}

	require.NoError(t, cl3.Stop(ctx))
	require.NoError(t, sd3.Close())
	require.NoError(t, cl1.Stop(ctx))
	require.NoError(t, sd1.Close())
	require.NoError(t, cl2.Stop(ctx))
	require.NoError(t, sd2.Close())
	srv.Shutdown()
}

// TestSingletonSupervisor verifies the supervision behavior of singleton actors:
// the dedicated default supervisor applies when no supervisor is provided, a
// supervisor set via WithSingletonSupervisor is attached to the singleton, and
// the supervisor survives delegation to the node that ends up hosting the
// singleton.
func TestSingletonSupervisor(t *testing.T) {
	t.Run("With default supervisor when none is provided", func(t *testing.T) {
		ctx := context.Background()
		system := MockSingletonClusterReadyActorSystem(t)
		clusterMock := mockcluster.NewCluster(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(false, nil).Once()
		clusterMock.EXPECT().PutActor(mock.Anything, mock.Anything).Return(nil).Once()

		pid, err := system.spawnSingletonOnLocal(ctx, "singleton", NewMockActor(), nil, time.Second, 100*time.Millisecond, 1, nil)
		require.NoError(t, err)
		require.NotNil(t, pid)
		require.NotNil(t, pid.supervisor)

		require.Equal(t, supervisor.OneForOneStrategy, pid.supervisor.Strategy())

		directive, ok := pid.supervisor.Directive(&gerrors.PanicError{})
		require.True(t, ok)
		require.Equal(t, supervisor.StopDirective, directive)

		directive, ok = pid.supervisor.Directive(&gerrors.InternalError{})
		require.True(t, ok)
		require.Equal(t, supervisor.StopDirective, directive)

		directive, ok = pid.supervisor.Directive(&runtime.PanicNilError{})
		require.True(t, ok)
		require.Equal(t, supervisor.StopDirective, directive)
	})
	t.Run("With custom supervisor on local spawn", func(t *testing.T) {
		ctx := context.Background()
		system := MockSingletonClusterReadyActorSystem(t)
		clusterMock := mockcluster.NewCluster(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(false, nil).Once()
		clusterMock.EXPECT().PutActor(mock.Anything, mock.Anything).Return(nil).Once()

		custom := supervisor.NewSupervisor(supervisor.WithAnyErrorDirective(supervisor.RestartDirective))

		pid, err := system.spawnSingletonOnLocal(ctx, "singleton", NewMockActor(), nil, time.Second, 100*time.Millisecond, 1, custom)
		require.NoError(t, err)
		require.NotNil(t, pid)
		require.Same(t, custom, pid.supervisor)
	})
	t.Run("With custom supervisor surviving delegation to the host node", func(t *testing.T) {
		ctx := context.TODO()
		srv := startNatsServer(t)

		role := "singleton-host"

		cl1, sd1 := testNATs(t, srv.Addr().String(), withMockRoles(role))
		require.NotNil(t, cl1)
		require.NotNil(t, sd1)

		cl2, sd2 := testNATs(t, srv.Addr().String())
		require.NotNil(t, cl2)
		require.NotNil(t, sd2)

		pause.For(time.Second)

		// spawn from the node without the role so the spawn is delegated to the
		// role-bearing host over remoting
		actorName := "supervised-singleton"
		custom := supervisor.NewSupervisor(supervisor.WithAnyErrorDirective(supervisor.RestartDirective))

		_, err := cl2.SpawnSingleton(ctx, actorName, NewMockActor(),
			WithSingletonRole(role),
			WithSingletonSupervisor(custom),
		)
		require.NoError(t, err)

		// the singleton runs on the role-bearing host with the custom supervisor
		host := cl1.(*actorSystem)
		node, ok := host.actors.nodeByName(actorName)
		require.True(t, ok)

		pid := node.value()
		require.NotNil(t, pid)
		require.NotNil(t, pid.supervisor)

		directive, ok := pid.supervisor.AnyErrorDirective()
		require.True(t, ok)
		require.Equal(t, supervisor.RestartDirective, directive)

		require.NoError(t, cl2.Stop(ctx))
		require.NoError(t, sd2.Close())
		require.NoError(t, cl1.Stop(ctx))
		require.NoError(t, sd1.Close())
		srv.Shutdown()
	})
}
