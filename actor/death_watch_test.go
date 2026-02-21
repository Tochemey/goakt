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
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v3/address"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/log"
	mockscluster "github.com/tochemey/goakt/v3/mocks/cluster"
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
	t.Run("System stops when RemoveActor call failed in cluster mode", func(t *testing.T) {
		ctx := context.Background()
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		actorID := "testID"

		// mock the cluster interface
		clmock := mockscluster.NewCluster(t)
		clmock.EXPECT().ActorExists(mock.Anything, actorID).Return(false, nil)
		clmock.EXPECT().RemoveActor(mock.Anything, actorID).Return(stdErrors.New("removal failed"))

		// Set the cluster mock BEFORE Start so that handlePostStart (which runs
		// asynchronously during Start) picks it up via getCluster() without racing.
		// Leave clusterEnabled false so setupCluster is skipped during Start.
		sys := actorSys.(*actorSystem)
		sys.locker.Lock()
		sys.cluster = clmock
		sys.locker.Unlock()

		err = actorSys.Start(ctx)
		require.NoError(t, err)

		// wait for the system to start properly
		pause.For(500 * time.Millisecond)

		// Now enable cluster flags — after Start and handlePostStart have completed.
		sys.clusterEnabled.Store(true)
		sys.remotingEnabled.Store(true)
		sys.relocationEnabled.Store(false)

		cid, err := actorSys.Spawn(ctx, actorID, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, cid)

		pause.For(500 * time.Millisecond)

		// No need to set deathWatch.cluster — handlePostStart already set it
		// from getCluster() which returned clmock.

		require.NoError(t, cid.Shutdown(ctx))

		pause.For(time.Second)

		pid := actorSys.getDeathWatch()
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

		err = Tell(ctx, pid, NewTerminated(addr.String()))
		require.NoError(t, err)

		pause.For(time.Second)
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With Terminated when cluster removal fails returns internal error", func(t *testing.T) {
		ctx := context.Background()

		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		clmock := mockscluster.NewCluster(t)

		// Set the cluster mock BEFORE Start so handlePostStart picks it up
		// via getCluster() without racing. Leave clusterEnabled false so
		// setupCluster is skipped during Start.
		sys := actorSys.(*actorSystem)
		sys.locker.Lock()
		sys.cluster = clmock
		sys.locker.Unlock()

		err = actorSys.Start(ctx)
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		// Enable cluster flags after Start and handlePostStart have completed.
		sys.clusterEnabled.Store(true)

		t.Cleanup(func() {
			sys.clusterEnabled.Store(false)
			sys.locker.Lock()
			sys.cluster = nil
			sys.locker.Unlock()
			require.NoError(t, actorSys.Stop(ctx))
		})

		const actorName = "actor-to-free"
		// Spawn checks ActorExists on the cluster when InCluster() is true.
		clmock.EXPECT().ActorExists(mock.Anything, actorName).Return(false, nil)

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

		terminated := NewTerminated(cid.ID())
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, terminated)

		err = deathWatchActor.handleTerminated(receiveCtx)
		require.Error(t, err)
		var internalErr *gerrors.InternalError
		require.ErrorAs(t, err, &internalErr)
		require.Contains(t, err.Error(), clusterErr.Error())

		require.NoError(t, cid.Shutdown(ctx))
	})

	t.Run("With Terminated removes singleton kind entry", func(t *testing.T) {
		ctx := context.Background()

		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		sys := actorSys.(*actorSystem)

		// Mock cluster removal for both actor name and singleton kind.
		clmock := mockscluster.NewCluster(t)

		// Set the cluster mock BEFORE Start so that handlePostStart (which runs
		// asynchronously during Start) picks it up via getCluster() without racing.
		// Leave clusterEnabled false so setupCluster is skipped during Start.
		sys.locker.Lock()
		sys.cluster = clmock
		sys.locker.Unlock()

		err = actorSys.Start(ctx)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// Now enable cluster flag — after Start and handlePostStart have completed.
		sys.clusterEnabled.Store(true)

		t.Cleanup(func() {
			// Detach the mocked cluster before stopping the system to avoid background
			// shutdown workflows (preShutdown) calling into unexpected mock methods.
			sys.clusterEnabled.Store(false)
			sys.locker.Lock()
			sys.cluster = nil
			sys.locker.Unlock()
			require.NoError(t, actorSys.Stop(ctx))
		})

		// Create a singleton actor PID and register it in the tree so deathWatch can find it.
		const (
			actorName = "singleton-to-free"
			role      = "blue"
		)
		actor := NewMockActor()
		singletonPID, err := sys.configPID(ctx, actorName, actor,
			WithLongLived(),
			withSingleton(&singletonSpec{}),
			WithRole(role),
		)
		require.NoError(t, err)
		require.NotNil(t, singletonPID)

		// Register under the user guardian (any existing parent works).
		require.NoError(t, sys.tree().addNode(sys.getUserGuardian(), singletonPID))

		// No need to manually wire deathWatch fields — handlePostStart already set
		// them during Start (cluster, actorSystem, pid, logger, tree).
		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)

		expectedKind := kindRole(registry.Name(actor), role)
		clmock.EXPECT().RemoveActor(mock.Anything, actorName).Return(nil).Once()
		clmock.EXPECT().RemoveKind(mock.Anything, expectedKind).Return(nil).Once()

		terminated := NewTerminated(singletonPID.ID())
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, terminated)

		require.NoError(t, deathWatchActor.handleTerminated(receiveCtx))
		require.NoError(t, singletonPID.Shutdown(ctx))
	})
}
