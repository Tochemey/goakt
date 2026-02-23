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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/olric"
	"github.com/travisjeffery/go-dynaport"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	mockcluster "github.com/tochemey/goakt/v4/mocks/cluster"
	mockremote "github.com/tochemey/goakt/v4/mocks/remote"
	"github.com/tochemey/goakt/v4/remote"
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
		err := cl1.SpawnSingleton(ctx, actorName, actor)
		require.NoError(t, err)

		// attempt to create another singleton actor with the same kind is idempotent
		err = cl2.SpawnSingleton(ctx, "actorName", actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrSingletonAlreadyExists)

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
		err := system.SpawnSingleton(ctx, "singleton", NewMockActor(), WithSingletonSpawnRetries(retries))
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
		err = newActorSystem.SpawnSingleton(ctx, actorName, actor)
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
		err := cl1.SpawnSingleton(ctx, actorName, actor)
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
		err = newActorSystem.SpawnSingleton(ctx, actorName, actor)
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

		err := system.spawnSingletonOnLeader(ctx, cl, "singleton", NewMockActor(),
			singletonSpec.SpawnTimeout,
			singletonSpec.WaitInterval,
			singletonSpec.MaxRetries)
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

		err := system.spawnSingletonOnLeader(ctx, cl, "singleton", NewMockActor(),
			singletonSpec.SpawnTimeout,
			singletonSpec.WaitInterval,
			singletonSpec.MaxRetries)
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
			Return(expectedErr).
			Once()

		err := system.spawnSingletonOnLeader(ctx, cl, name, actor,
			singletonSpec.SpawnTimeout,
			singletonSpec.WaitInterval,
			singletonSpec.MaxRetries)
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
			).Return(nil).Once()

		err := system.SpawnSingleton(ctx, "singleton", NewMockActor(),
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

		err := system.spawnSingletonWithRole(ctx, clusterMock, "singleton", NewMockActor(), "blue",
			singletonSpec.SpawnTimeout,
			singletonSpec.WaitInterval,
			singletonSpec.MaxRetries)
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

		err := system.spawnSingletonWithRole(ctx, clusterMock, "singleton", NewMockActor(), role,
			singletonSpec.SpawnTimeout,
			singletonSpec.WaitInterval,
			singletonSpec.MaxRetries)
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
						*req.Role == role
				}),
			).Return(nil).Once()

		err := system.spawnSingletonWithRole(ctx, clusterMock, "singleton", NewMockActor(), role,
			singletonSpec.SpawnTimeout,
			singletonSpec.WaitInterval,
			singletonSpec.MaxRetries)
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
			LookupKind(mock.Anything, "actor.mockactor::role").
			Return("", nil).
			Once()

		clusterMock.EXPECT().
			PutKind(mock.Anything, "actor.mockactor::role").
			Return(nil)

		clusterMock.EXPECT().
			ActorExists(mock.Anything, "singleton").
			Return(false, nil).
			Once()

		singletonSpec := &remote.SingletonSpec{
			SpawnTimeout: time.Second,
			WaitInterval: time.Millisecond,
			MaxRetries:   2,
		}

		err := system.spawnSingletonWithRole(ctx, clusterMock, "singleton", NewMockActor(), role,
			singletonSpec.SpawnTimeout,
			singletonSpec.WaitInterval,
			singletonSpec.MaxRetries)
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
		err := cl1.SpawnSingleton(ctx, actorName, actor, WithSingletonRole(role))
		require.NoError(t, err)

		// attempt to create another singleton actor with the same actor
		err = cl2.SpawnSingleton(ctx, "actorName", actor)
		require.NoError(t, err)

		err = cl3.SpawnSingleton(ctx, "actor2", actor, WithSingletonRole(role))
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrSingletonAlreadyExists)

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
		clusterMock.EXPECT().LookupKind(mock.Anything, "actor.mockactor").Return("", nil).Times(3)
		clusterMock.EXPECT().PutKind(mock.Anything, "actor.mockactor").Return(nil).Times(3)
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(false, olric.ErrWriteQuorum).Times(3)
		// Kind reservation is released on ActorExists errors.
		clusterMock.EXPECT().RemoveKind(mock.Anything, "actor.mockactor").Return(nil).Times(3)

		err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(3),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.ErrorIs(t, err, gerrors.ErrWriteQuorum)
	})

	t.Run("already exists short-circuits retries", func(t *testing.T) {
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
		clusterMock.EXPECT().LookupKind(mock.Anything, "actor.mockactor").Return("actor.mockactor", nil).Once()

		err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(3),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrSingletonAlreadyExists)
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
		clusterMock.EXPECT().LookupKind(mock.Anything, "actor.mockactor").Return("", context.Canceled).Once()

		err := system.SpawnSingleton(
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
		clusterMock.EXPECT().LookupKind(mock.Anything, "actor.mockactor").Return("", nil).Once()
		clusterMock.EXPECT().PutKind(mock.Anything, "actor.mockactor").Return(nil).Once()
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(true, nil).Once()
		clusterMock.EXPECT().RemoveKind(mock.Anything, "actor.mockactor").Return(nil).Once()
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(&internalpb.Actor{
			Address: "singleton@test@127.0.0.1:0",
			Type:    "actor.other",
			// Singleton is nil: indicates this name is taken by a non-singleton (or unknown) actor.
			Singleton: nil,
		}, nil).Once()

		err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)
	})

	t.Run("fails when registry lookup returns non-retryable error", func(t *testing.T) {
		ctx := context.Background()
		clusterMock := mockcluster.NewCluster(t)
		system := MockSingletonClusterReadyActorSystem(t)

		system.locker.Lock()
		system.cluster = clusterMock
		system.locker.Unlock()

		lookupErr := fmt.Errorf("registry read failed")
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{
				Host:         "127.0.0.1",
				PeersPort:    system.clusterNode.PeersPort,
				RemotingPort: system.clusterNode.RemotingPort,
				Coordinator:  true,
			},
		}, nil).Once()
		clusterMock.EXPECT().LookupKind(mock.Anything, "actor.mockactor").Return("", nil).Once()
		clusterMock.EXPECT().PutKind(mock.Anything, "actor.mockactor").Return(nil).Once()
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(true, nil).Once()
		// Kind reservation is released on name collisions.
		clusterMock.EXPECT().RemoveKind(mock.Anything, "actor.mockactor").Return(nil).Once()
		// Simulate propagation delay: name collision exists, but metadata is temporarily not readable.
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(nil, cluster.ErrActorNotFound).Once()
		clusterMock.EXPECT().LookupKind(mock.Anything, "actor.mockactor").Return("", lookupErr).Once()

		err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.ErrorContains(t, err, lookupErr.Error())
	})

	t.Run("fails when kind reservation write fails", func(t *testing.T) {
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

		putErr := stdErrors.New("put kind failed")
		clusterMock.EXPECT().LookupKind(mock.Anything, "actor.mockactor").Return("", nil).Once()
		clusterMock.EXPECT().PutKind(mock.Anything, "actor.mockactor").Return(putErr).Once()

		err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(1),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.ErrorContains(t, err, putErr.Error())
	})

	t.Run("releases kind reservation when ActorExists returns an error", func(t *testing.T) {
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
		clusterMock.EXPECT().LookupKind(mock.Anything, "actor.mockactor").Return("", nil).Once()
		clusterMock.EXPECT().PutKind(mock.Anything, "actor.mockactor").Return(nil).Once()
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(false, existsErr).Once()
		// checkSpawnPreconditions must release the reserved kind on ActorExists errors.
		clusterMock.EXPECT().RemoveKind(mock.Anything, "actor.mockactor").Return(nil).Once()

		err := system.SpawnSingleton(
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

		clusterMock.EXPECT().LookupKind(mock.Anything, "actor.mockactor").Return("actor.mockactor", nil).Once()
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(nil, cluster.ErrActorNotFound).Once()

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
			Return(gerrors.ErrActorAlreadyExists).
			Once()

		err := system.SpawnSingleton(
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
		clusterMock.EXPECT().LookupKind(mock.Anything, "actor.mockactor").Return("", nil).Once()
		clusterMock.EXPECT().PutKind(mock.Anything, "actor.mockactor").Return(nil).Once()
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(true, nil).Once()
		// Kind reservation is released because the name is taken; the name-based disambiguation
		// then proves the existing actor is the singleton we wanted (idempotent success).
		clusterMock.EXPECT().RemoveKind(mock.Anything, "actor.mockactor").Return(nil).Once()
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(&internalpb.Actor{
			Address:   "singleton@test@127.0.0.1:0",
			Type:      "actor.mockactor",
			Singleton: &internalpb.SingletonSpec{},
		}, nil).Once()

		err := system.SpawnSingleton(
			ctx,
			"singleton",
			NewMockActor(),
			WithSingletonSpawnRetries(2),
			WithSingletonSpawnWaitInterval(time.Millisecond),
			WithSingletonSpawnTimeout(time.Second),
		)
		require.NoError(t, err)
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
		clusterMock.EXPECT().LookupKind(mock.Anything, "actor.mockactor").Return("", nil).Times(2)
		clusterMock.EXPECT().PutKind(mock.Anything, "actor.mockactor").Return(nil).Times(2)
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(true, nil).Times(2)
		clusterMock.EXPECT().RemoveKind(mock.Anything, "actor.mockactor").Return(nil).Times(2)
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(nil, cluster.ErrEngineNotRunning).Once()

		// Attempt 2: name exists, cluster metadata shows it is the intended singleton -> success.
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(&internalpb.Actor{
			Address:   "singleton@test@127.0.0.1:0",
			Type:      "actor.mockactor",
			Singleton: &internalpb.SingletonSpec{},
		}, nil).Once()

		err := system.SpawnSingleton(
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
		clusterMock.EXPECT().LookupKind(mock.Anything, "actor.mockactor").Return("", nil).Once()
		clusterMock.EXPECT().PutKind(mock.Anything, "actor.mockactor").Return(nil).Once()
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(true, nil).Once()
		clusterMock.EXPECT().RemoveKind(mock.Anything, "actor.mockactor").Return(nil).Once()
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(nil, getErr).Once()

		err := system.SpawnSingleton(
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

		clusterMock.EXPECT().LookupKind(mock.Anything, "actor.mockactor").Return("", nil).Once()
		clusterMock.EXPECT().PutKind(mock.Anything, "actor.mockactor").Return(nil).Once()
		clusterMock.EXPECT().ActorExists(mock.Anything, "singleton").Return(true, nil).Once()
		clusterMock.EXPECT().RemoveKind(mock.Anything, "actor.mockactor").Return(nil).Once()
		// Existing actor is a singleton, but not the intended kind/role.
		clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(&internalpb.Actor{
			Address:   "singleton@test@127.0.0.1:0",
			Type:      "actor.other",
			Singleton: &internalpb.SingletonSpec{},
		}, nil).Once()

		err := system.SpawnSingleton(
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

		err := system.SpawnSingleton(
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

		err := system.SpawnSingleton(
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
			Return(nil).
			Once()

		err := system.SpawnSingleton(
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
			Return(nil).
			Once()

		err := system.SpawnSingleton(
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
					Return(tc.err).
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
					Return(nil).
					Once()

				err := system.SpawnSingleton(
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
}
