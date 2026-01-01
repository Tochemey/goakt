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
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/discovery"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	mockscluster "github.com/tochemey/goakt/v3/mocks/cluster"
	mocksdiscovery "github.com/tochemey/goakt/v3/mocks/discovery"
	"github.com/tochemey/goakt/v3/remote"
)

func TestDeathWatch(t *testing.T) {
	t.Run("With unhandled message", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		err = actorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the system to start properly
		pause.For(500 * time.Millisecond)

		// create a deadletter subscriber
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		pid := actorSystem.getDeathWatch()
		// send an unhandled message to the system guardian
		err = Tell(ctx, pid, new(anypb.Any))
		require.NoError(t, err)

		pause.For(time.Second)

		var items []*goaktpb.Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*goaktpb.Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		require.Len(t, items, 1)
		consumer.Shutdown()
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("System stops when RemoveActor call failed in cluster mode", func(t *testing.T) {
		ctx := context.Background()
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		actorID := "testID"

		// mock the cluster interface
		clmock := mockscluster.NewCluster(t)
		clmock.EXPECT().ActorExists(mock.Anything, actorID).Return(false, nil)
		clmock.EXPECT().IsLeader(mock.Anything).Return(false)
		clmock.EXPECT().Stop(mock.Anything).Return(nil)

		err = actorSys.Start(ctx)
		require.NoError(t, err)

		// wait for the system to start properly
		pause.For(500 * time.Millisecond)
		actorSys.(*actorSystem).cluster = clmock
		actorSys.(*actorSystem).clusterEnabled.Store(true)

		cid, err := actorSys.Spawn(ctx, actorID, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, cid)

		pause.For(500 * time.Millisecond)

		pid := actorSys.getDeathWatch()
		pid.Actor().(*deathWatch).cluster = clmock

		require.NoError(t, cid.Shutdown(ctx))

		pause.For(time.Second)

		require.False(t, pid.IsRunning())
		require.False(t, actorSys.Running())
	})
	t.Run("With Terminated when PID not found return no error", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		actorID := "testID"

		err = actorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the system to start properly
		pause.For(500 * time.Millisecond)
		pid := actorSystem.getDeathWatch()

		addr := address.New(actorID, actorSystem.Name(), actorSystem.Host(), actorSystem.Port())

		err = Tell(ctx, pid, &goaktpb.Terminated{Address: addr.String()})
		require.NoError(t, err)

		pause.For(time.Second)
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With Terminated when cluster removal fails returns internal error", func(t *testing.T) {
		ctx := context.Background()

		ports := dynaport.Get(3)

		discoveryPort := ports[0]
		peersPort := ports[1]
		remotingPort := ports[2]

		host := "127.0.0.1"

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(discoveryPort)),
		}

		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		clmock := mockscluster.NewCluster(t)
		provider := mocksdiscovery.NewProvider(t)
		provider.EXPECT().ID().Return("test")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		sys := actorSys.(*actorSystem)
		sys.cluster = clmock
		sys.clusterEnabled.Store(true)
		sys.remotingEnabled.Store(true)
		sys.remoteConfig = remote.NewConfig(host, remotingPort)
		sys.clusterNode = &discovery.Node{Host: host, PeersPort: peersPort, DiscoveryPort: discoveryPort}

		clConfig := NewClusterConfig()
		clConfig.discoveryPort = 9001
		clConfig.discovery = provider

		sys.clusterConfig = clConfig

		err = actorSys.Start(ctx)
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		t.Cleanup(func() {
			require.NoError(t, actorSys.Stop(ctx))
		})

		const actorName = "actor-to-free"
		cid, err := actorSys.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, cid)

		// allow the spawned actor to register with the tree
		pause.For(500 * time.Millisecond)

		clusterErr := stdErrors.New("cluster failure")
		clmock.EXPECT().RemoveActor(mock.Anything, actorName).Return(clusterErr)

		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)
		deathWatchActor.cluster = clmock
		deathWatchActor.actorSystem = actorSys
		deathWatchActor.pid = deathWatchPID
		deathWatchActor.logger = log.DiscardLogger
		deathWatchActor.tree = sys.tree()

		terminated := &goaktpb.Terminated{Address: cid.ID()}
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, terminated)

		err = deathWatchActor.handleTerminated(receiveCtx)
		require.Error(t, err)
		var internalErr *gerrors.InternalError
		require.ErrorAs(t, err, &internalErr)
		require.Contains(t, err.Error(), clusterErr.Error())

		require.NoError(t, cid.Shutdown(ctx))
	})
}
