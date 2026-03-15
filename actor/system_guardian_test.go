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
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	testkit "github.com/tochemey/goakt/v4/mocks/discovery"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestSystemGuardian(t *testing.T) {
	t.Run("Stop actorsystem when a system actor is terminated", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		err = actorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the system to start properly
		pause.For(500 * time.Millisecond)
		require.True(t, actorSystem.Running())

		// let us terminate the user guardian for the sake of the test
		deathWatch := actorSystem.getDeathWatch()
		require.NotNil(t, deathWatch)
		require.True(t, deathWatch.IsRunning())

		message, _ := anypb.New(new(testpb.TestSend))
		err = deathWatch.Tell(ctx, actorSystem.getRootGuardian(), NewPanicSignal(message, "test panic signal", time.Now().UTC()))

		require.NoError(t, err)

		// wait for the system to stop properly
		pause.For(time.Second)

		require.False(t, actorSystem.Running())
	})
	t.Run("Stop actorsystem when PanicSignal sent to system guardian", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		err = actorSystem.Start(ctx)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)
		require.True(t, actorSystem.Running())

		systemGuardian := actorSystem.getSystemGuardian()
		deathWatch := actorSystem.getDeathWatch()
		require.NotNil(t, systemGuardian)
		require.NotNil(t, deathWatch)
		message, _ := anypb.New(new(testpb.TestSend))
		err = deathWatch.Tell(ctx, systemGuardian, NewPanicSignal(message, "test panic", time.Now().UTC()))
		require.NoError(t, err)

		pause.For(time.Second)
		require.False(t, actorSystem.Running())
	})
	t.Run("With PostStart using enabled logger", func(t *testing.T) {
		ctx := context.Background()
		logger := log.NewZap(log.DebugLevel, io.Discard)
		actorSystem, err := NewActorSystem("testSys", WithLogger(logger))
		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))
		pause.For(500 * time.Millisecond)
		require.True(t, actorSystem.Running())
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With RebalanceComplete message", func(t *testing.T) {
		ctx := context.Background()
		nodePorts := dynaport.Get(3)
		host := "127.0.0.1"
		addrs := []string{net.JoinHostPort(host, strconv.Itoa(nodePorts[0]))}
		provider := new(testkit.Provider)
		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		actorSystem, err := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger),
			WithRemote(remote.NewConfig(host, nodePorts[2])),
			WithCluster(NewClusterConfig().
				WithKinds(new(MockActor)).
				WithPartitionCount(9).
				WithReplicaCount(1).
				WithPeersPort(nodePorts[1]).
				WithMinimumPeersQuorum(1).
				WithDiscoveryPort(nodePorts[0]).
				WithDiscovery(provider)),
		)
		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))
		pause.For(2 * time.Second)

		systemGuardian := actorSystem.getSystemGuardian()
		require.NotNil(t, systemGuardian)
		err = systemGuardian.Tell(ctx, systemGuardian, &internalpb.RebalanceComplete{PeerAddress: "127.0.0.1:99999"})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)
		require.NoError(t, actorSystem.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With unhandled message result in deadletter", func(t *testing.T) {
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

		systemGuardian := actorSystem.getSystemGuardian()
		// send an unhandled message to the system guardian
		err = Tell(ctx, systemGuardian, new(anypb.Any))
		require.NoError(t, err)

		pause.For(time.Second)

		var items []*Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		require.Len(t, items, 1)
		consumer.Shutdown()
		require.NoError(t, actorSystem.Stop(ctx))
	})
}
