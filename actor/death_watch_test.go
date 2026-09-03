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
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	mockscluster "github.com/tochemey/goakt/v4/mocks/cluster"
	sup "github.com/tochemey/goakt/v4/supervisor"
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
	t.Run("System keeps running when RemoveActor call failed in cluster mode", func(t *testing.T) {
		ctx := context.Background()
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		actorID := "testID"

		// mock the cluster interface
		clmock := mockscluster.NewCluster(t)
		clmock.EXPECT().ActorExists(mock.Anything, actorID).Return(false, nil)
		clmock.EXPECT().PutActor(mock.Anything, mock.Anything).Return(nil).Once()
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

		t.Cleanup(func() {
			// Detach the mocked cluster before stopping the system to avoid
			// background shutdown workflows calling into unexpected mock methods.
			sys.clusterEnabled.Store(false)
			sys.remotingEnabled.Store(false)
			sys.locker.Lock()
			sys.cluster = nil
			sys.locker.Unlock()
			require.NoError(t, actorSys.Stop(ctx))
		})

		cid, err := actorSys.Spawn(ctx, actorID, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, cid)

		pause.For(500 * time.Millisecond)

		// No need to set deathWatch.cluster — handlePostStart already set it
		// from getCluster() which returned clmock.

		require.NoError(t, cid.Shutdown(ctx))

		pause.For(time.Second)

		// The removal failure is a resumable cluster cleanup error (issue #1337):
		// DeathWatch resumes and the node keeps running instead of shutting down.
		pid := actorSys.getDeathWatch()
		require.True(t, pid.IsRunning())
		require.False(t, pid.IsSuspended())
		require.True(t, actorSys.Running())
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

		err = Tell(ctx, pid, NewTerminated(newPath(addr)))
		require.NoError(t, err)

		pause.For(time.Second)
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With Terminated when cluster removal fails returns cluster cleanup error", func(t *testing.T) {
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
		clmock.EXPECT().PutActor(mock.Anything, mock.Anything).Return(nil).Once()

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

		terminated := NewTerminated(cid.Path())
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, terminated)

		err = deathWatchActor.handleTerminated(receiveCtx)
		require.Error(t, err)
		var cleanupErr *clusterCleanupError
		require.ErrorAs(t, err, &cleanupErr)
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

		clmock.EXPECT().RemoveActor(mock.Anything, actorName).Return(nil).Once()

		terminated := NewTerminated(singletonPID.Path())
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, terminated)

		require.NoError(t, deathWatchActor.handleTerminated(receiveCtx))
		require.NoError(t, singletonPID.Shutdown(ctx))
	})

	// Logging path tests: verify all log messages are emitted when logger is enabled.
	t.Run("Logging PostStop logs stopped successfully", func(t *testing.T) {
		ctx := context.Background()
		buf := &safeBuffer{}
		logger := log.NewSlog(log.InfoLevel, buf)
		actorSys, err := NewActorSystem("testSys", WithLogger(logger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		err = actorSys.Start(ctx)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		buf.Reset()
		require.NoError(t, actorSys.Stop(ctx))
		pause.For(500 * time.Millisecond)

		// Flush logger if it implements Flush
		_ = logger.Flush()

		logContent := buf.String()
		require.Contains(t, logContent, "stopped successfully", "PostStop should log when deathWatch stops")
		require.Contains(t, logContent, "GoAktDeathWatch", "PostStop should include deathWatch actor name")
	})

	t.Run("Logging handlePostStart logs started successfully", func(t *testing.T) {
		ctx := context.Background()
		buf := &safeBuffer{}
		logger := log.NewSlog(log.InfoLevel, buf)
		actorSys, err := NewActorSystem("testSys", WithLogger(logger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		err = actorSys.Start(ctx)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		require.NoError(t, actorSys.Stop(ctx))

		_ = logger.Flush()
		logContent := buf.String()
		require.Contains(t, logContent, "started successfully", "handlePostStart should log when deathWatch starts")
		require.Contains(t, logContent, "GoAktDeathWatch", "handlePostStart should include deathWatch actor name")
	})

	t.Run("Logging handleTerminated logs when PID not found", func(t *testing.T) {
		ctx := context.Background()
		buf := &safeBuffer{}
		// handleTerminated diagnostics are per-actor lifecycle, logged at Debug.
		logger := log.NewSlog(log.DebugLevel, buf)
		actorSys, err := NewActorSystem("testSys", WithLogger(logger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		err = actorSys.Start(ctx)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		addr := address.New("nonexistent", actorSys.Name(), actorSys.Host(), actorSys.Port())
		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)

		terminated := NewTerminated(newPath(addr))
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, terminated)

		buf.Reset()
		err = deathWatchActor.handleTerminated(receiveCtx)
		require.NoError(t, err)

		_ = logger.Flush()
		logContent := buf.String()
		require.Contains(t, logContent, "removing dead actor resource from system", "should log when starting to process Terminated")
		require.Contains(t, logContent, "unable to locate dead actor resource", "should log when PID not found in tree")
		require.Contains(t, logContent, "maybe already freed", "should log hint when PID not found")

		require.NoError(t, actorSys.Stop(ctx))
	})

	t.Run("Logging handleTerminated logs when cluster removal fails", func(t *testing.T) {
		ctx := context.Background()
		buf := &safeBuffer{}
		// handleTerminated diagnostics are per-actor lifecycle, logged at Debug.
		logger := log.NewSlog(log.DebugLevel, buf)
		actorSys, err := NewActorSystem("testSys", WithLogger(logger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		clmock := mockscluster.NewCluster(t)
		sys := actorSys.(*actorSystem)
		sys.locker.Lock()
		sys.cluster = clmock
		sys.locker.Unlock()

		err = actorSys.Start(ctx)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)
		sys.clusterEnabled.Store(true)

		t.Cleanup(func() {
			sys.clusterEnabled.Store(false)
			sys.locker.Lock()
			sys.cluster = nil
			sys.locker.Unlock()
			_ = actorSys.Stop(ctx)
		})

		const actorName = "actor-to-free"
		clmock.EXPECT().ActorExists(mock.Anything, actorName).Return(false, nil)
		clmock.EXPECT().PutActor(mock.Anything, mock.Anything).Return(nil).Once()

		cid, err := actorSys.Spawn(ctx, actorName, &noLogActor{})
		require.NoError(t, err)
		require.NotNil(t, cid)
		pause.For(500 * time.Millisecond)

		clusterErr := stdErrors.New("cluster failure")
		clmock.EXPECT().RemoveActor(mock.Anything, actorName).Return(clusterErr)

		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)
		terminated := NewTerminated(cid.Path())
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, terminated)

		buf.Reset()
		err = deathWatchActor.handleTerminated(receiveCtx)
		require.Error(t, err)
		require.NotNil(t, err)

		_ = logger.Flush()
		logContent := buf.String()
		require.Contains(t, logContent, "removing dead actor resource from system", "should log when starting to process Terminated")
		require.Contains(t, logContent, "failed to remove dead actor from cluster", "should log when cluster removal fails")
		require.Contains(t, logContent, "cluster failure", "should include error message in log")

		require.NoError(t, cid.Shutdown(ctx))
	})

	t.Run("Logging handleTerminated logs when actor successfully removed", func(t *testing.T) {
		ctx := context.Background()
		buf := &safeBuffer{}
		// handleTerminated diagnostics are per-actor lifecycle, logged at Debug.
		logger := log.NewSlog(log.DebugLevel, buf)
		actorSys, err := NewActorSystem("testSys", WithLogger(logger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		err = actorSys.Start(ctx)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		const actorName = "actor-to-remove"
		cid, err := actorSys.Spawn(ctx, actorName, &noLogActor{})
		require.NoError(t, err)
		require.NotNil(t, cid)
		pause.For(500 * time.Millisecond)

		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)
		terminated := NewTerminated(cid.Path())
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, terminated)

		buf.Reset()
		err = deathWatchActor.handleTerminated(receiveCtx)
		require.NoError(t, err)

		_ = logger.Flush()
		logContent := buf.String()
		require.Contains(t, logContent, "removing dead actor resource from system", "should log when starting to process Terminated")
		require.Contains(t, logContent, "removed dead actor resource from system", "should log when actor successfully removed")

		require.NoError(t, cid.Shutdown(ctx))
		require.NoError(t, actorSys.Stop(ctx))
	})
}

func TestDeathWatchClusterCleanupFailure(t *testing.T) {
	t.Run("With transient removal failure DeathWatch resumes and keeps processing", func(t *testing.T) {
		ctx := context.Background()
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		firstActor := "first-actor"
		secondActor := "second-actor"

		// mock the cluster interface: the first removal fails the way issue
		// #1337 observed it (a bare "canceled" surfaced while the cluster
		// digests a membership change) and succeeds on the scheduled retry;
		// the second actor's removal succeeds outright, proving DeathWatch
		// resumed and kept processing terminations.
		clmock := mockscluster.NewCluster(t)
		clmock.EXPECT().ActorExists(mock.Anything, firstActor).Return(false, nil)
		clmock.EXPECT().ActorExists(mock.Anything, secondActor).Return(false, nil)
		clmock.EXPECT().PutActor(mock.Anything, mock.Anything).Return(nil).Twice()
		clmock.EXPECT().RemoveActor(mock.Anything, firstActor).Return(stdErrors.New("canceled")).Once()
		clmock.EXPECT().RemoveActor(mock.Anything, firstActor).Return(nil).Once()
		clmock.EXPECT().RemoveActor(mock.Anything, secondActor).Return(nil).Once()

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

		t.Cleanup(func() {
			// Detach the mocked cluster before stopping the system to avoid
			// background shutdown workflows calling into unexpected mock methods.
			sys.clusterEnabled.Store(false)
			sys.remotingEnabled.Store(false)
			sys.locker.Lock()
			sys.cluster = nil
			sys.locker.Unlock()
			require.NoError(t, actorSys.Stop(ctx))
		})

		first, err := actorSys.Spawn(ctx, firstActor, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, first)

		pause.For(500 * time.Millisecond)

		require.NoError(t, first.Shutdown(ctx))

		pause.For(time.Second)

		// the failed cleanup left the node untouched
		deathWatchPID := actorSys.getDeathWatch()
		require.True(t, deathWatchPID.IsRunning())
		require.False(t, deathWatchPID.IsSuspended())
		require.True(t, actorSys.Running())

		// DeathWatch keeps processing: the next termination is cleaned up
		// normally (the mock's Once assertion proves the removal happened)
		second, err := actorSys.Spawn(ctx, secondActor, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, second)

		pause.For(500 * time.Millisecond)

		require.NoError(t, second.Shutdown(ctx))

		pause.For(time.Second)

		require.True(t, deathWatchPID.IsRunning())
		require.False(t, deathWatchPID.IsSuspended())
		require.True(t, actorSys.Running())
	})
	t.Run("With DeathWatch supervisor resuming on ClusterCleanupError only", func(t *testing.T) {
		ctx := context.Background()
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		err = actorSys.Start(ctx)
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		t.Cleanup(func() {
			require.NoError(t, actorSys.Stop(ctx))
		})

		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)

		// the cleanup error resumes DeathWatch
		directive, ok := deathWatchPID.supervisor.Directive(newClusterCleanupError(stdErrors.New("boom")))
		require.True(t, ok)
		require.Equal(t, sup.ResumeDirective, directive)

		// any other error still escalates through the catch-all rule
		_, ok = deathWatchPID.supervisor.Directive(stdErrors.New("boom"))
		require.False(t, ok)
		directive, ok = deathWatchPID.supervisor.AnyErrorDirective()
		require.True(t, ok)
		require.Equal(t, sup.EscalateDirective, directive)
	})
	t.Run("With handleTerminated returns cluster cleanup error on removal failure", func(t *testing.T) {
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
		clmock.EXPECT().PutActor(mock.Anything, mock.Anything).Return(nil).Once()

		cid, err := actorSys.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, cid)

		// allow the spawned actor to register with the tree
		pause.For(500 * time.Millisecond)

		clusterErr := stdErrors.New("canceled")
		clmock.EXPECT().RemoveActor(mock.Anything, actorName).Return(clusterErr).Once()

		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)

		terminated := NewTerminated(cid.Path())
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, terminated)

		err = deathWatchActor.handleTerminated(receiveCtx)
		require.Error(t, err)
		var cleanupErr *clusterCleanupError
		require.ErrorAs(t, err, &cleanupErr)
		require.ErrorIs(t, err, clusterErr)
		require.Contains(t, err.Error(), "cluster cleanup error")
		require.Contains(t, err.Error(), clusterErr.Error())

		require.NoError(t, cid.Shutdown(ctx))
	})
	t.Run("With removal retry rescheduling until success", func(t *testing.T) {
		ctx := context.Background()
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		clmock := mockscluster.NewCluster(t)

		sys := actorSys.(*actorSystem)
		sys.locker.Lock()
		sys.cluster = clmock
		sys.locker.Unlock()

		err = actorSys.Start(ctx)
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		sys.clusterEnabled.Store(true)

		t.Cleanup(func() {
			sys.clusterEnabled.Store(false)
			sys.locker.Lock()
			sys.cluster = nil
			sys.locker.Unlock()
			require.NoError(t, actorSys.Stop(ctx))
		})

		// the retry handler only touches the cluster registry, so no live
		// actor is needed: the first attempt fails and must reschedule itself
		// through the system scheduler; the rescheduled attempt succeeds.
		const actorName = "dead-actor"
		clmock.EXPECT().RemoveActor(mock.Anything, actorName).Return(stdErrors.New("canceled")).Once()
		clmock.EXPECT().RemoveActor(mock.Anything, actorName).Return(nil).Once()

		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)

		retry := &retryDeadActorRemoval{actorName: actorName, attempt: 1}
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, retry)
		deathWatchActor.handleRetryDeadActorRemoval(receiveCtx)

		// attempt 2 is scheduled with a 1s backoff and delivered through the
		// real scheduler and mailbox; the mock's Once assertions prove both
		// attempts ran and the second one succeeded
		pause.For(2 * time.Second)

		require.True(t, deathWatchPID.IsRunning())
		require.False(t, deathWatchPID.IsSuspended())
	})
	t.Run("With removal retry giving up once the budget is exhausted", func(t *testing.T) {
		ctx := context.Background()
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		clmock := mockscluster.NewCluster(t)

		sys := actorSys.(*actorSystem)
		sys.locker.Lock()
		sys.cluster = clmock
		sys.locker.Unlock()

		err = actorSys.Start(ctx)
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		sys.clusterEnabled.Store(true)

		t.Cleanup(func() {
			sys.clusterEnabled.Store(false)
			sys.locker.Lock()
			sys.cluster = nil
			sys.locker.Unlock()
			require.NoError(t, actorSys.Stop(ctx))
		})

		// the final attempt in the budget fails: no further retry may be
		// scheduled, which the strict mock enforces (a rescheduled attempt
		// would surface as an unexpected RemoveActor call below)
		const actorName = "dead-actor"
		clmock.EXPECT().RemoveActor(mock.Anything, actorName).Return(stdErrors.New("canceled")).Once()

		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)

		retry := &retryDeadActorRemoval{actorName: actorName, attempt: deathWatchRemovalMaxRetries}
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, retry)
		deathWatchActor.handleRetryDeadActorRemoval(receiveCtx)

		pause.For(time.Second)

		require.True(t, deathWatchPID.IsRunning())
		require.False(t, deathWatchPID.IsSuspended())
	})
	t.Run("With removal retry not scheduled when the engine is not running", func(t *testing.T) {
		ctx := context.Background()
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		clmock := mockscluster.NewCluster(t)

		sys := actorSys.(*actorSystem)
		sys.locker.Lock()
		sys.cluster = clmock
		sys.locker.Unlock()

		err = actorSys.Start(ctx)
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		sys.clusterEnabled.Store(true)

		t.Cleanup(func() {
			sys.clusterEnabled.Store(false)
			sys.locker.Lock()
			sys.cluster = nil
			sys.locker.Unlock()
			require.NoError(t, actorSys.Stop(ctx))
		})

		const actorName = "actor-to-free"
		clmock.EXPECT().ActorExists(mock.Anything, actorName).Return(false, nil)
		clmock.EXPECT().PutActor(mock.Anything, mock.Anything).Return(nil).Once()

		cid, err := actorSys.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, cid)

		pause.For(500 * time.Millisecond)

		// a stopped engine is the one failure no retry can outlive: DeathWatch
		// must still resume through the cleanup error but never book a retry,
		// which the strict mock enforces (a scheduled retry would surface as
		// an unexpected second RemoveActor call within the wait below)
		clmock.EXPECT().RemoveActor(mock.Anything, actorName).Return(cluster.ErrEngineNotRunning).Once()

		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)

		terminated := NewTerminated(cid.Path())
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, terminated)

		err = deathWatchActor.handleTerminated(receiveCtx)
		require.Error(t, err)
		var cleanupErr *clusterCleanupError
		require.ErrorAs(t, err, &cleanupErr)
		require.ErrorIs(t, err, cluster.ErrEngineNotRunning)

		pause.For(time.Second)

		require.NoError(t, cid.Shutdown(ctx))
	})
	t.Run("With removal retry skipped when the system is stopping", func(t *testing.T) {
		ctx := context.Background()
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		clmock := mockscluster.NewCluster(t)

		sys := actorSys.(*actorSystem)
		sys.locker.Lock()
		sys.cluster = clmock
		sys.locker.Unlock()

		err = actorSys.Start(ctx)
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		sys.clusterEnabled.Store(true)

		t.Cleanup(func() {
			sys.clusterEnabled.Store(false)
			sys.locker.Lock()
			sys.cluster = nil
			sys.locker.Unlock()
			require.NoError(t, actorSys.Stop(ctx))
		})

		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)

		// a stopping system abandons the retry: the shutdown path reconciles
		// the node's registry records itself. The strict mock (no RemoveActor
		// expectation) enforces that the registry is left untouched.
		sys.shuttingDown.Store(true)

		retry := &retryDeadActorRemoval{actorName: "dead-actor", attempt: 1}
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, retry)
		deathWatchActor.handleRetryDeadActorRemoval(receiveCtx)

		// undo the simulated stopping state so the deferred Stop runs normally
		sys.shuttingDown.Store(false)
	})
	t.Run("With removal retry skipped when cluster mode is disabled", func(t *testing.T) {
		ctx := context.Background()
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		// the cluster mock stays attached but cluster mode is never enabled:
		// the strict mock (no expectations) enforces that the retry handler
		// returns before touching the registry
		clmock := mockscluster.NewCluster(t)

		sys := actorSys.(*actorSystem)
		sys.locker.Lock()
		sys.cluster = clmock
		sys.locker.Unlock()

		err = actorSys.Start(ctx)
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		t.Cleanup(func() {
			sys.locker.Lock()
			sys.cluster = nil
			sys.locker.Unlock()
			require.NoError(t, actorSys.Stop(ctx))
		})

		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)

		retry := &retryDeadActorRemoval{actorName: "dead-actor", attempt: 1}
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, retry)
		deathWatchActor.handleRetryDeadActorRemoval(receiveCtx)
	})
	t.Run("With removal retry abandoned when the engine stops mid-retries", func(t *testing.T) {
		ctx := context.Background()
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		clmock := mockscluster.NewCluster(t)

		sys := actorSys.(*actorSystem)
		sys.locker.Lock()
		sys.cluster = clmock
		sys.locker.Unlock()

		err = actorSys.Start(ctx)
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		sys.clusterEnabled.Store(true)

		t.Cleanup(func() {
			sys.clusterEnabled.Store(false)
			sys.locker.Lock()
			sys.cluster = nil
			sys.locker.Unlock()
			require.NoError(t, actorSys.Stop(ctx))
		})

		// the engine reports itself stopped mid-budget: no further retry may
		// be booked, which the strict mock enforces (a rescheduled attempt
		// would surface as an unexpected RemoveActor call within the wait)
		const actorName = "dead-actor"
		clmock.EXPECT().RemoveActor(mock.Anything, actorName).Return(cluster.ErrEngineNotRunning).Once()

		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)

		retry := &retryDeadActorRemoval{actorName: actorName, attempt: 1}
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, retry)
		deathWatchActor.handleRetryDeadActorRemoval(receiveCtx)

		pause.For(1500 * time.Millisecond)

		require.True(t, deathWatchPID.IsRunning())
		require.False(t, deathWatchPID.IsSuspended())
	})
	t.Run("With removal retry succeeding on first attempt", func(t *testing.T) {
		ctx := context.Background()
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		clmock := mockscluster.NewCluster(t)

		sys := actorSys.(*actorSystem)
		sys.locker.Lock()
		sys.cluster = clmock
		sys.locker.Unlock()

		err = actorSys.Start(ctx)
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		sys.clusterEnabled.Store(true)

		t.Cleanup(func() {
			sys.clusterEnabled.Store(false)
			sys.locker.Lock()
			sys.cluster = nil
			sys.locker.Unlock()
			require.NoError(t, actorSys.Stop(ctx))
		})

		// a successful retry books nothing further, which the strict mock
		// enforces during the wait below
		const actorName = "dead-actor"
		clmock.EXPECT().RemoveActor(mock.Anything, actorName).Return(nil).Once()

		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)

		retry := &retryDeadActorRemoval{actorName: actorName, attempt: 1}
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, retry)
		deathWatchActor.handleRetryDeadActorRemoval(receiveCtx)

		pause.For(time.Second)

		require.True(t, deathWatchPID.IsRunning())
		require.False(t, deathWatchPID.IsSuspended())
	})
	t.Run("With removal retry scheduling failure only logged", func(t *testing.T) {
		ctx := context.Background()
		buf := &safeBuffer{}
		logger := log.NewSlog(log.ErrorLevel, buf)
		actorSys, err := NewActorSystem("testSys", WithLogger(logger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		clmock := mockscluster.NewCluster(t)

		sys := actorSys.(*actorSystem)
		sys.locker.Lock()
		sys.cluster = clmock
		sys.locker.Unlock()

		err = actorSys.Start(ctx)
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		sys.clusterEnabled.Store(true)

		t.Cleanup(func() {
			sys.clusterEnabled.Store(false)
			sys.locker.Lock()
			sys.cluster = nil
			sys.locker.Unlock()
			require.NoError(t, actorSys.Stop(ctx))
		})

		// with the scheduler stopped, booking the follow-up retry fails; the
		// failure must only be logged — DeathWatch keeps running and the
		// registry is left to the exhausted-budget report
		sys.scheduler.Stop(ctx)

		const actorName = "dead-actor"
		clmock.EXPECT().RemoveActor(mock.Anything, actorName).Return(stdErrors.New("canceled")).Once()

		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)

		buf.Reset()
		retry := &retryDeadActorRemoval{actorName: actorName, attempt: 1}
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, retry)
		deathWatchActor.handleRetryDeadActorRemoval(receiveCtx)

		_ = logger.Flush()
		logContent := buf.String()
		require.Contains(t, logContent, "failed to schedule removal retry", "scheduling failure should be logged")
		require.True(t, deathWatchPID.IsRunning())
		require.False(t, deathWatchPID.IsSuspended())
	})
	t.Run("With Terminated skipping cluster removal when the system is stopping", func(t *testing.T) {
		ctx := context.Background()
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		clmock := mockscluster.NewCluster(t)

		sys := actorSys.(*actorSystem)
		sys.locker.Lock()
		sys.cluster = clmock
		sys.locker.Unlock()

		err = actorSys.Start(ctx)
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		sys.clusterEnabled.Store(true)

		t.Cleanup(func() {
			sys.clusterEnabled.Store(false)
			sys.locker.Lock()
			sys.cluster = nil
			sys.locker.Unlock()
			require.NoError(t, actorSys.Stop(ctx))
		})

		const actorName = "actor-to-free"
		clmock.EXPECT().ActorExists(mock.Anything, actorName).Return(false, nil)
		clmock.EXPECT().PutActor(mock.Anything, mock.Anything).Return(nil).Once()

		cid, err := actorSys.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, cid)

		pause.For(500 * time.Millisecond)

		// a stopping system never attempts the registry removal at all: the
		// shutdown path reconciles its records wholesale. The strict mock (no
		// RemoveActor expectation) enforces that the registry is untouched.
		sys.shuttingDown.Store(true)

		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)

		terminated := NewTerminated(cid.Path())
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, terminated)

		require.NoError(t, deathWatchActor.handleTerminated(receiveCtx))

		// undo the simulated stopping state so the deferred Stop runs normally
		sys.shuttingDown.Store(false)

		require.NoError(t, cid.Shutdown(ctx))
	})
	t.Run("Logging removal retry warning, success and give-up messages", func(t *testing.T) {
		ctx := context.Background()
		buf := &safeBuffer{}
		logger := log.NewSlog(log.DebugLevel, buf)
		actorSys, err := NewActorSystem("testSys", WithLogger(logger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)

		clmock := mockscluster.NewCluster(t)

		sys := actorSys.(*actorSystem)
		sys.locker.Lock()
		sys.cluster = clmock
		sys.locker.Unlock()

		err = actorSys.Start(ctx)
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		sys.clusterEnabled.Store(true)

		t.Cleanup(func() {
			sys.clusterEnabled.Store(false)
			sys.locker.Lock()
			sys.cluster = nil
			sys.locker.Unlock()
			require.NoError(t, actorSys.Stop(ctx))
		})

		// retriedActor fails its first attempt (warning) and succeeds on the
		// rescheduled one (debug); doomedActor fails its final attempt (error)
		const retriedActor = "retried-actor"
		const doomedActor = "doomed-actor"
		clmock.EXPECT().RemoveActor(mock.Anything, retriedActor).Return(stdErrors.New("canceled")).Once()
		clmock.EXPECT().RemoveActor(mock.Anything, retriedActor).Return(nil).Once()
		clmock.EXPECT().RemoveActor(mock.Anything, doomedActor).Return(stdErrors.New("canceled")).Once()

		deathWatchPID := actorSys.getDeathWatch()
		require.NotNil(t, deathWatchPID)
		deathWatchActor := deathWatchPID.Actor().(*deathWatch)

		buf.Reset()

		retry := &retryDeadActorRemoval{actorName: retriedActor, attempt: 1}
		receiveCtx := newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, retry)
		deathWatchActor.handleRetryDeadActorRemoval(receiveCtx)

		doomed := &retryDeadActorRemoval{actorName: doomedActor, attempt: deathWatchRemovalMaxRetries}
		receiveCtx = newReceiveContext(context.Background(), actorSys.NoSender(), deathWatchPID, doomed)
		deathWatchActor.handleRetryDeadActorRemoval(receiveCtx)

		// the rescheduled attempt for retriedActor fires after a 1s backoff
		pause.For(2 * time.Second)

		_ = logger.Flush()
		logContent := buf.String()
		require.Contains(t, logContent, fmt.Sprintf("removal retry=1/%d failed", deathWatchRemovalMaxRetries), "a failed attempt within the budget should log a warning")
		require.Contains(t, logContent, "removed dead actor resource from cluster on retry=2", "a successful retry should log at debug level")
		require.Contains(t, logContent, fmt.Sprintf("failed to remove dead actor from cluster after %d retries", deathWatchRemovalMaxRetries), "an exhausted budget should log an error")
	})
}

// noLogActor is a minimal actor that never logs. Used in tests that capture log
// output to avoid data races from concurrent writes to a shared buffer.
type noLogActor struct{}

func (n *noLogActor) PreStart(*Context) error { return nil }
func (n *noLogActor) Receive(*ReceiveContext) {}
func (n *noLogActor) PostStop(*Context) error { return nil }
