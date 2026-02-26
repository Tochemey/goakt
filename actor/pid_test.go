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
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"go.opentelemetry.io/otel"
	noopmetric "go.opentelemetry.io/otel/metric/noop"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/breaker"
	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/metric"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/log"
	testkit "github.com/tochemey/goakt/v4/mocks/discovery"
	mocksremote "github.com/tochemey/goakt/v4/mocks/remoteclient"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/supervisor"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

const (
	receivingDelay = 1 * time.Second
	replyTimeout   = 100 * time.Millisecond
	passivateAfter = 200 * time.Millisecond
)

func TestReceive(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		host := "127.0.0.1"

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor ref
		pid, err := actorSystem.Spawn(ctx, "name", NewMockActor())
		require.NoError(t, err)
		assert.NotNil(t, pid)

		pause.For(time.Second)

		// let us send 10 messages to the actor
		count := 10
		for range count {
			receiveContext := &ReceiveContext{
				ctx:     ctx,
				message: new(testpb.TestSend),
				sender:  actorSystem.NoSender(),
				self:    pid,
			}

			pid.doReceive(receiveContext)
		}

		pause.For(500 * time.Millisecond)
		assert.Zero(t, pid.ChildrenCount())
		assert.NotZero(t, pid.LatestProcessedDuration())
		assert.EqualValues(t, 10, pid.ProcessedCount()-1) // 1 because of the PostStart message
		metric := pid.Metric(ctx)
		require.NotNil(t, metric)
		assert.NotZero(t, metric.LatestProcessedDuration())
		assert.Zero(t, metric.ChidrenCount())
		assert.EqualValues(t, 10, metric.ProcessedCount())
		assert.NotZero(t, metric.Uptime())
		assert.Zero(t, metric.RestartCount())
		assert.Zero(t, metric.StashSize())
		assert.Zero(t, metric.DeadlettersCount())
		assert.Zero(t, metric.FailureCount())
		assert.Zero(t, metric.ReinstateCount())

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("When mailbox returns an error", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		host := "127.0.0.1"

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// this mailbox will not be able to process messages which will result in a deadletter
		// and the actor will not be started
		mailbox := NewMockErrorMailbox()
		pid, err := actorSystem.Spawn(ctx, "name", NewMockActor(), WithMailbox(mailbox))
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrRequestTimeout)
		require.Nil(t, pid)

		message := new(testpb.TestSend)
		err = Tell(ctx, pid, message)
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrDead)

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

		// unsubscribe the consumer
		err = actorSystem.Unsubscribe(consumer)
		require.NoError(t, err)
		require.NotEmpty(t, items)
		deadletter := items[0]
		actual, ok := deadletter.Message().(*commands.HealthCheckRequest)
		require.True(t, ok)
		require.NotNil(t, actual)

		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
}

func TestMessageOrdering(t *testing.T) {
	ctx := context.Background()
	actorSystem, err := NewActorSystem("fifo", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	require.NoError(t, actorSystem.Start(ctx))
	defer func() { _ = actorSystem.Stop(ctx) }()

	expected := 128
	fifoActor := NewMockFIFO(expected)
	pidName := "fifo-" + uuid.NewString()
	pid, err := actorSystem.Spawn(ctx, pidName, fifoActor)
	require.NoError(t, err)
	defer func() { _ = pid.Shutdown(ctx) }()

	for i := range expected {
		msg := &testpb.TestCount{Value: int32(i)}
		require.NoError(t, Tell(ctx, pid, msg))
	}

	select {
	case <-fifoActor.Done():
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for FIFO actor to receive %d messages", expected)
	}

	require.Len(t, fifoActor.Seen(), expected)
	expectedOrder := make([]int32, expected)
	for i := range expectedOrder {
		expectedOrder[i] = int32(i)
	}
	assert.Equal(t, expectedOrder, fifoActor.Seen())
}

func TestPassivation(t *testing.T) {
	t.Run("With actor shutdown failure", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))
		pause.For(time.Second)

		// create the actor path
		pid, err := actorSystem.Spawn(ctx, "test", &MockPostStop{},
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(passivateAfter)),
		)
		require.NoError(t, err)
		assert.NotNil(t, pid)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Go(func() {
			pause.For(receivingDelay)
		})
		// block until timer is up
		wg.Wait()

		// let us send a message to the actor
		err = Tell(ctx, pid, new(testpb.TestSend))
		assert.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrDead)

		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With Pause/Resume path", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		host := "127.0.0.1"

		// passivation timeout
		timeout := 500 * time.Millisecond

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))
		pause.For(time.Second)

		// create the actor path
		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor(),
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(timeout)))
		require.NoError(t, err)
		assert.NotNil(t, pid)

		pause.For(100 * time.Millisecond)

		// Pause the passivation
		err = Tell(ctx, pid, new(PausePassivation))
		require.NoError(t, err)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Go(func() {
			pause.For(time.Second)
		})
		// block until timer is up
		wg.Wait()
		require.True(t, pid.IsRunning())

		// Resume passivation
		err = Tell(ctx, pid, new(ResumePassivation))
		require.NoError(t, err)

		wg = sync.WaitGroup{}
		wg.Go(func() {
			pause.For(time.Second)
		})
		// block until timer is up
		wg.Wait()
		// let us send a message to the actor
		err = Tell(ctx, pid, new(testpb.TestSend))
		assert.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrDead)

		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With Long lived passivation", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		host := "127.0.0.1"

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor path
		pid, err := actorSystem.Spawn(ctx, "test1", NewMockActor(),
			WithPassivationStrategy(passivation.NewLongLivedStrategy()))
		require.NoError(t, err)
		assert.NotNil(t, pid)

		pid2, err := actorSystem.Spawn(ctx, "test2", NewMockActor(),
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(passivateAfter)))
		require.NoError(t, err)
		assert.NotNil(t, pid)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Go(func() {
			pause.For(receivingDelay)
		})
		// block until timer is up
		wg.Wait()

		require.True(t, pid.IsRunning())

		// let us send a message to the actor
		err = Tell(ctx, pid2, new(testpb.TestSend))
		assert.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrDead)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With Messages Count-based passivation", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		host := "127.0.0.1"

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor path
		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor(),
			WithPassivationStrategy(passivation.NewMessageCountBasedStrategy(2)))
		require.NoError(t, err)
		assert.NotNil(t, pid)

		// create two messages
		for range 2 {
			err = Tell(ctx, pid, new(testpb.TestSend))
			require.NoError(t, err)
		}

		pause.For(time.Second)

		// let us send a message to the actor
		err = Tell(ctx, pid, new(testpb.TestSend))
		assert.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrDead)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("Message-count passivation honors pause", func(t *testing.T) {
		ctx := context.TODO()

		system, err := NewActorSystem("pause-passivation", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, system.Start(ctx))
		t.Cleanup(func() { _ = system.Stop(ctx) })

		pid, err := system.Spawn(ctx, "pause-test", NewMockActor(),
			WithPassivationStrategy(passivation.NewMessageCountBasedStrategy(1)))
		require.NoError(t, err)
		require.NotNil(t, pid)

		require.NoError(t, Tell(ctx, pid, new(testpb.TestSend)))
		require.True(t, pid.IsRunning())

		require.NoError(t, Tell(ctx, pid, new(PausePassivation)))

		require.Eventually(t, func() bool {
			return pid.passivationManager != nil &&
				len(pid.passivationManager.messageTriggers) == 0
		}, time.Second, 5*time.Millisecond)

		require.True(t, pid.IsRunning())
	})
	t.Run("With Time-based passivation", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		host := "127.0.0.1"

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		// create a subscriber
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create the actor path
		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor(),
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(passivateAfter)))
		require.NoError(t, err)
		assert.NotNil(t, pid)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Go(func() {
			pause.For(receivingDelay)
		})
		// block until timer is up
		wg.Wait()

		var passivated []*ActorPassivated
		for event := range consumer.Iterator() {
			payload := event.Payload()
			if actorPassivated, ok := payload.(*ActorPassivated); ok {
				passivated = append(passivated, actorPassivated)
			}
		}

		require.NotEmpty(t, passivated)
		require.Len(t, passivated, 1)
		require.Equal(t, pid.ID(), passivated[0].Address())
		require.NotZero(t, passivated[0].PassivatedAt())

		// let us send a message to the actor
		err = Tell(ctx, pid, new(testpb.TestSend))
		assert.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrDead)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
}
func TestRestart(t *testing.T) {
	t.Run("With restart a stopped actor", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor(),
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(10*time.Second)),
		)

		require.NoError(t, err)
		assert.NotNil(t, pid)

		pause.For(time.Second)

		assert.NotZero(t, pid.Uptime())

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)
		assert.Zero(t, pid.Uptime())

		// let us send a message to the actor
		err = Tell(ctx, pid, new(testpb.TestSend))
		assert.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrDead)

		// restart the actor
		err = pid.Restart(ctx)
		assert.NoError(t, err)
		assert.True(t, pid.IsRunning())
		assert.NotZero(t, pid.Uptime())
		// let us send 10 messages to the actor
		count := 10
		for range count {
			err = Tell(ctx, pid, new(testpb.TestSend))
			assert.NoError(t, err)
		}

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With restart an actor", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor())

		require.NoError(t, err)
		assert.NotNil(t, pid)
		count := 10
		for range count {
			err := Tell(ctx, pid, new(testpb.TestSend))
			assert.NoError(t, err)
		}

		// restart the actor
		err = pid.Restart(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		assert.True(t, pid.IsRunning())

		assert.EqualValues(t, 1, pid.RestartCount())
		// let us send 10 messages to the actor
		for i := 0; i < count; i++ {
			err = Tell(ctx, pid, new(testpb.TestSend))
			assert.NoError(t, err)
		}

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With restart an actor and its descendants", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)

		pause.For(time.Second)

		cid, err := pid.SpawnChild(ctx, "child", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, cid)
		pause.For(500 * time.Millisecond)

		gcid, err := cid.SpawnChild(ctx, "grandchild", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, gcid)
		pause.For(500 * time.Millisecond)

		metric := actorSystem.Metric(ctx)
		require.NotNil(t, metric)
		require.EqualValues(t, 3, metric.ActorsCount())

		require.EqualValues(t, 1, pid.ChildrenCount())

		// let us send 10 messages to the actor
		count := 10
		for range count {
			err := Tell(ctx, pid, new(testpb.TestSend))
			require.NoError(t, err)
		}

		// restart the actor
		err = pid.Restart(ctx)
		require.NoError(t, err)
		require.True(t, pid.IsRunning())

		pause.For(time.Second)

		metric = actorSystem.Metric(ctx)
		require.NotNil(t, metric)
		require.EqualValues(t, 3, metric.ActorsCount())

		require.EqualValues(t, 1, pid.RestartCount())
		require.EqualValues(t, 1, pid.ChildrenCount())
		require.EqualValues(t, 1, cid.RestartCount())
		require.EqualValues(t, 1, gcid.RestartCount())

		// let us send 10 messages to the actor
		for range count {
			err = Tell(ctx, pid, new(testpb.TestSend))
			assert.NoError(t, err)
		}

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With restart an actor and its suspended descendant", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)

		child, err := pid.SpawnChild(ctx, "child", NewMockSupervised())
		require.NoError(t, err)
		require.NotNil(t, child)

		pause.For(time.Second)

		child.supervisor = newBareSupervisor(supervisor.OneForOneStrategy)

		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))
		pause.For(time.Second)

		require.False(t, child.IsRunning())
		require.True(t, child.IsSuspended())

		err = pid.Restart(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		require.True(t, pid.IsRunning())
		require.True(t, child.IsRunning())
		require.False(t, child.IsSuspended())
		require.EqualValues(t, 1, pid.ChildrenCount())
		require.EqualValues(t, 1, child.RestartCount())

		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With restart an actor and its descendant failure", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)

		child, err := pid.SpawnChild(ctx, "child", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, child)
		pause.For(500 * time.Millisecond)

		grandchild, err := child.SpawnChild(ctx, "grandchild", NewMockRestart())
		require.NoError(t, err)
		require.NotNil(t, grandchild)
		pause.For(500 * time.Millisecond)

		err = pid.Restart(ctx)
		require.Error(t, err)
		require.False(t, pid.IsRunning())

		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With restart subtree reattaches existing nodes", func(t *testing.T) {
		ctx := context.TODO()

		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)

		system := sys.(*actorSystem)
		system.noSender = &PID{actorSystem: system, logger: log.DiscardLogger}
		system.noSender.setState(runningState, true)

		rootAddr := system.actorAddress("root")
		root, err := newPID(ctx, rootAddr, NewMockActor(), withActorSystem(system), withCustomLogger(log.DiscardLogger))
		require.NoError(t, err)

		childAddr := address.NewWithParent("child", rootAddr.System(), rootAddr.Host(), rootAddr.Port(), rootAddr)
		child, err := newPID(ctx, childAddr, NewMockActor(), withActorSystem(system), withCustomLogger(log.DiscardLogger))
		require.NoError(t, err)

		t.Cleanup(func() {
			root.stopSupervisionLoop()
			child.stopSupervisionLoop()
		})

		tree := system.tree()
		require.NoError(t, tree.addRootNode(root))
		require.NoError(t, tree.addNode(root, child))

		parentNode, ok := tree.node(root.ID())
		require.True(t, ok)
		delete(parentNode.descendants, child.ID())
		delete(parentNode.watchees, child.ID())

		childNode, ok := tree.node(child.ID())
		require.True(t, ok)
		childNode.parentNode = nil

		child.setState(runningState, false)

		err = restartSubtree(ctx, &restartNode{pid: child}, root, tree, nil, system)
		require.NoError(t, err)

		require.Len(t, root.Children(), 1)
		require.Equal(t, root.ID(), child.Parent().ID())
	})
	t.Run("With restart subtree ignores non-running descendants", func(t *testing.T) {
		ctx := context.TODO()

		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)

		system := sys.(*actorSystem)
		system.noSender = &PID{actorSystem: system, logger: log.DiscardLogger}
		system.noSender.setState(runningState, true)

		rootAddr := system.actorAddress("root")
		root, err := newPID(ctx, rootAddr, NewMockActor(), withActorSystem(system), withCustomLogger(log.DiscardLogger))
		require.NoError(t, err)

		childAddr := address.NewWithParent("child", rootAddr.System(), rootAddr.Host(), rootAddr.Port(), rootAddr)
		child, err := newPID(ctx, childAddr, NewMockActor(), withActorSystem(system), withCustomLogger(log.DiscardLogger))
		require.NoError(t, err)

		t.Cleanup(func() {
			root.stopSupervisionLoop()
			child.stopSupervisionLoop()
		})

		tree := system.tree()
		require.NoError(t, tree.addRootNode(root))
		require.NoError(t, tree.addNode(root, child))

		child.setState(runningState, false)

		require.Len(t, tree.descendants(root), 1)
		subtree := buildRestartSubtree(root, tree)
		require.Empty(t, subtree.children)
	})
	t.Run("With restart an actor with PreStart failure", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx),
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(time.Minute)),
		)

		pause.For(time.Second)

		// create a Ping actor
		actor := NewMockRestart()
		assert.NotNil(t, actor)

		pid, err := actorSystem.Spawn(ctx, "test", actor,
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(time.Minute)),
		)

		require.NoError(t, err)
		assert.NotNil(t, pid)

		// wait awhile for a proper start
		assert.True(t, pid.IsRunning())

		// restart the actor
		err = pid.Restart(ctx)
		assert.Error(t, err)
		assert.False(t, pid.IsRunning())

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("noSender cannot be restarted", func(t *testing.T) {
		pid := &PID{
			fieldsLocker: sync.RWMutex{},
		}
		err := pid.Restart(context.TODO())
		assert.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrUndefinedActor)
	})
	t.Run("With restart failed due to PostStop failure", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx),
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(passivateAfter)),
		)

		pause.For(time.Second)

		// create a Ping actor
		actor := &MockPostStop{}
		assert.NotNil(t, actor)

		pid, err := actorSystem.Spawn(ctx, "test", actor,
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(passivateAfter)),
		)
		require.NoError(t, err)
		assert.NotNil(t, pid)

		require.NoError(t, err)
		assert.NotNil(t, pid)
		// let us send 10 messages to the actor
		count := 10
		for i := 0; i < count; i++ {
			err := Tell(ctx, pid, new(testpb.TestSend))
			assert.NoError(t, err)
		}

		// restart the actor
		err = pid.Restart(ctx)
		assert.Error(t, err)
		assert.False(t, pid.IsRunning())
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With restart an actor and its children failure", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)

		pause.For(time.Second)

		cid, err := pid.SpawnChild(ctx, "child", NewMockRestart())
		require.NoError(t, err)
		require.NotNil(t, cid)
		pause.For(500 * time.Millisecond)

		require.EqualValues(t, 1, pid.ChildrenCount())

		pause.For(time.Second)

		// restart the actor
		err = pid.Restart(ctx)
		require.Error(t, err)
		require.False(t, pid.IsRunning())

		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With restart an actor with PostStart message", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		pid, err := actorSystem.Spawn(ctx, "test", NewMockPostStart())
		require.NoError(t, err)
		require.NotNil(t, pid)

		pause.For(time.Second)

		require.EqualValues(t, 1, postStarCount.Load())
		// restart the actor
		err = pid.Restart(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		require.True(t, pid.IsRunning())

		require.EqualValues(t, 2, postStarCount.Load())

		assert.EqualValues(t, 1, pid.RestartCount())

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
}
func TestSupervisorStrategy(t *testing.T) {
	t.Run("With stop as supervisor directive", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		stopStrategy := supervisor.NewSupervisor(supervisor.WithDirective(&errors.PanicError{}, supervisor.StopDirective))

		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised(), WithSupervisor(stopStrategy))
		require.NoError(t, err)
		require.NotNil(t, child)
		require.Equal(t, parent.ID(), child.Parent().ID())

		require.Len(t, parent.Children(), 1)
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		pause.For(time.Second)
		require.Zero(t, parent.ChildrenCount())

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	// Ensures that when a child panics and receives a Resume directive, the first
	// time-based passivation decision immediately following reinstate is skipped,
	// so the child remains running instead of being stopped due to a race.
	t.Run("With resume + time-based passivation skips immediate stop after reinstate", func(t *testing.T) {
		ctx := context.Background()

		actorSystem, err := NewActorSystem("reinstate-passivation-skip", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))
		t.Cleanup(func() { _ = actorSystem.Stop(ctx) })

		parent, err := actorSystem.Spawn(ctx, "resume-parent", NewMockSupervisor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// Use a short time-based passivation to trigger quickly
		passiveAfter := 200 * time.Millisecond
		resumeStrategy := supervisor.NewSupervisor(supervisor.WithDirective(&errors.PanicError{}, supervisor.ResumeDirective))

		child, err := parent.SpawnChild(
			ctx,
			"resume-child",
			NewMockSupervised(),
			WithSupervisor(resumeStrategy),
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(passiveAfter)),
		)
		require.NoError(t, err)
		require.NotNil(t, child)

		require.Len(t, parent.Children(), 1)

		// Force a panic; supervisor directive is Resume
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// Wait until the child is running (reinstate completed)
		require.Eventually(t, func() bool { return child.IsRunning() }, 3*time.Second, 20*time.Millisecond)

		// Now wait until just beyond the passivation timeout and assert the actor remains running.
		require.Eventually(t, func() bool {
			return child.IsRunning() && len(parent.Children()) == 1
		}, 3*time.Second, 20*time.Millisecond)

		// Cleanup
		require.NoError(t, parent.Shutdown(ctx))
	})
	t.Run("With stop as supervisor directive with ONE_FOR_ALL", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		stopStrategy := supervisor.NewSupervisor(
			supervisor.WithStrategy(supervisor.OneForAllStrategy),
			supervisor.WithDirective(&errors.PanicError{}, supervisor.StopDirective),
		)

		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised(), WithSupervisor(stopStrategy))
		require.NoError(t, err)
		require.NotNil(t, child)
		require.Equal(t, parent.ID(), child.Parent().ID())

		pause.For(500 * time.Millisecond)

		child2, err := parent.SpawnChild(ctx, "SpawnChild2", NewMockSupervised(), WithSupervisor(stopStrategy))
		require.NoError(t, err)
		require.NotNil(t, child2)
		require.Equal(t, parent.ID(), child2.Parent().ID())

		pause.For(500 * time.Millisecond)

		require.Len(t, parent.Children(), 2)
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		pause.For(time.Second)
		require.Zero(t, parent.ChildrenCount())

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With the default supervisor directives: generic panic", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised())
		require.NoError(t, err)
		require.NotNil(t, child)

		pause.For(time.Second)

		require.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		require.False(t, child.IsRunning())
		require.Len(t, parent.Children(), 0)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With the default supervisor directives: panic with error", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised())
		require.NoError(t, err)
		require.NotNil(t, child)

		pause.For(time.Second)

		require.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanicError)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		require.False(t, child.IsRunning())
		require.Len(t, parent.Children(), 0)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With undefined directive suspends actor", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// Directive(4) is beyond the known constants (Stop=0, Resume=1, Restart=2, Escalate=3),
		// so handlePanicking falls through to the default case which suspends the actor.
		fakeStrategy := supervisor.NewSupervisor(supervisor.WithDirective(&errors.PanicError{}, 4))
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised(), WithSupervisor(fakeStrategy))
		require.NoError(t, err)
		require.NotNil(t, child)

		pause.For(time.Second)

		require.Len(t, parent.Children(), 1)
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		pause.For(time.Second)

		require.False(t, child.IsRunning())
		require.True(t, child.IsSuspended())
		require.Len(t, parent.Children(), 0)

		require.Error(t, Tell(ctx, child, new(testpb.TestSend)))

		err = parent.Shutdown(ctx)
		require.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With directive not found suspends actor", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised())
		require.NoError(t, err)
		require.NotNil(t, child)

		pause.For(time.Second)

		// bare supervisor
		child.supervisor = newBareSupervisor(supervisor.OneForOneStrategy)

		require.Len(t, parent.Children(), 1)
		// send a message to the actor which result in panic
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		require.False(t, child.IsRunning())
		require.True(t, child.IsSuspended())
		require.Len(t, parent.Children(), 0)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With stop as supervisor directive with child actor shutdown failure", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())

		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		stopStrategy := supervisor.NewSupervisor(supervisor.WithDirective(&errors.PanicError{}, DefaultSupervisorDirective))
		child, err := parent.SpawnChild(ctx, "SpawnChild", &MockPostStop{}, WithSupervisor(stopStrategy))
		require.NoError(t, err)
		require.NotNil(t, child)

		pause.For(time.Second)

		require.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		require.False(t, child.IsRunning())
		require.Len(t, parent.Children(), 0)

		//stop the actor
		err = parent.Shutdown(ctx)
		require.NoError(t, err)
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With restart as supervisor strategy of a child actor", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		restartStrategy := supervisor.NewSupervisor(supervisor.WithDirective(&errors.PanicError{}, supervisor.RestartDirective))
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised(), WithSupervisor(restartStrategy))
		require.NoError(t, err)
		require.NotNil(t, child)

		pause.For(time.Second)

		require.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		require.True(t, child.IsRunning())

		// TODO: fix the child relationship supervisor mode
		// require.Len(t, parent.Children(), 1)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With restart as supervisor strategy with ONE_FOR_ALL", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		restartStrategy := supervisor.NewSupervisor(
			supervisor.WithStrategy(supervisor.OneForAllStrategy),
			supervisor.WithDirective(&errors.PanicError{}, supervisor.RestartDirective))
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised(), WithSupervisor(restartStrategy))
		require.NoError(t, err)
		require.NotNil(t, child)

		pause.For(time.Second)

		child2, err := parent.SpawnChild(ctx, "SpawnChild2", NewMockSupervised(), WithSupervisor(restartStrategy))
		require.NoError(t, err)
		require.NotNil(t, child2)
		require.Equal(t, parent.ID(), child2.Parent().ID())

		pause.For(500 * time.Millisecond)

		require.Len(t, parent.Children(), 2)
		// send a test panic message to the actor
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		require.Len(t, parent.Children(), 2)
		require.True(t, child.IsRunning())
		require.True(t, child2.IsRunning())

		// TODO: fix the child relationship supervisor mode
		// require.Len(t, parent.Children(), 1)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With no strategy set will default to a Shutdown", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		assert.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		assert.False(t, child.IsRunning())
		assert.Len(t, parent.Children(), 0)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With resume as supervisor strategy", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())
		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		resumeStrategy := supervisor.NewSupervisor(supervisor.WithDirective(&errors.PanicError{}, supervisor.ResumeDirective))
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised(), WithSupervisor(resumeStrategy))
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		assert.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		assert.True(t, child.IsRunning())
		require.Len(t, parent.Children(), 1)

		reply, err := Ask(ctx, child, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		actual, ok := reply.(*testpb.Reply)
		require.True(t, ok)
		assert.True(t, proto.Equal(expected, actual))

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With restart with limit as supervisor strategy", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		restartStrategy := supervisor.NewSupervisor(
			supervisor.WithStrategy(supervisor.OneForOneStrategy),
			supervisor.WithDirective(&errors.PanicError{}, supervisor.RestartDirective),
			supervisor.WithRetry(2, time.Minute),
		)

		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised(), WithSupervisor(restartStrategy))
		require.NoError(t, err)
		require.NotNil(t, child)

		require.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		require.True(t, child.IsRunning())

		require.Len(t, parent.Children(), 1)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With suspended actor restarted", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		subscriber, err := actorSystem.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, subscriber)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised())
		require.NoError(t, err)
		require.NotNil(t, child)

		pause.For(time.Second)

		child.supervisor = newBareSupervisor(supervisor.OneForOneStrategy)

		require.Len(t, parent.Children(), 1)
		// send a message to the actor which result in panic
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		require.False(t, child.IsRunning())
		require.True(t, child.IsSuspended())
		require.Len(t, parent.Children(), 0)

		var suspendedEvent *ActorSuspended
		for message := range subscriber.Iterator() {
			payload := message.Payload()
			// only listening to suspended actors
			event, ok := payload.(*ActorSuspended)
			if ok {
				suspendedEvent = event
				break
			}
		}

		require.NotNil(t, suspendedEvent)
		require.Equal(t, child.ID(), suspendedEvent.Address())

		// unsubscribe the consumer
		err = actorSystem.Unsubscribe(subscriber)
		require.NoError(t, err)

		// restart the actor
		require.NoError(t, child.Restart(ctx))
		// wait for  a while
		pause.For(time.Second)
		require.True(t, child.IsRunning())
		require.False(t, child.IsSuspended())
		require.Len(t, parent.Children(), 1)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With any error directive", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		stopStrategy := supervisor.NewSupervisor(supervisor.WithAnyErrorDirective(supervisor.StopDirective))

		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised(), WithSupervisor(stopStrategy))
		require.NoError(t, err)
		require.NotNil(t, child)
		require.Equal(t, parent.ID(), child.Parent().ID())

		require.Len(t, parent.Children(), 1)
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		pause.For(time.Second)
		require.Zero(t, parent.ChildrenCount())

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With escalation as supervisor strategy", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockEscalation())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		escalationStrategy := supervisor.NewSupervisor(supervisor.WithDirective(&errors.PanicError{}, supervisor.EscalateDirective))
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised(), WithSupervisor(escalationStrategy))
		require.NoError(t, err)
		require.NotNil(t, child)

		require.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		require.False(t, child.IsRunning())
		require.Len(t, parent.Children(), 0)

		//stop the actor
		err = parent.Shutdown(ctx)
		require.NoError(t, err)
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With reinstate", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "supervisor", NewMockReinstate())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		escalationStrategy := supervisor.NewSupervisor(supervisor.WithDirective(&errors.PanicError{}, supervisor.EscalateDirective))
		child, err := parent.SpawnChild(ctx, "reinstate", NewMockSupervised(), WithSupervisor(escalationStrategy))
		require.NoError(t, err)
		require.NotNil(t, child)

		require.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		require.True(t, child.IsRunning())
		require.Len(t, parent.Children(), 1)

		//stop the actor
		err = parent.Shutdown(ctx)
		require.NoError(t, err)
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With restart as supervisor strategy and any error", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		restartStrategy := supervisor.NewSupervisor(supervisor.WithAnyErrorDirective(supervisor.RestartDirective))
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised(), WithSupervisor(restartStrategy))
		require.NoError(t, err)
		require.NotNil(t, child)

		pause.For(time.Second)

		require.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		require.True(t, child.IsRunning())

		// TODO: fix the child relationship supervisor mode
		// require.Len(t, parent.Children(), 1)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With a supervisor strategy and any error without parent/child relationship", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the child actor
		restartStrategy := supervisor.NewSupervisor(supervisor.WithAnyErrorDirective(supervisor.RestartDirective))
		pid, err := actorSystem.Spawn(ctx, "SpawnChild", NewMockSupervised(), WithSupervisor(restartStrategy))
		require.NoError(t, err)
		require.NotNil(t, pid)

		pause.For(time.Second)

		// send a test panic message to the actor
		require.NoError(t, Tell(ctx, pid, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		require.True(t, pid.IsRunning())

		// TODO: fix the child relationship supervisor mode
		// require.Len(t, parent.Children(), 1)

		//stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("When No Parent found actor is suspended", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent := actorSystem.NoSender()

		// create the child actor
		escalationStrategy := supervisor.NewSupervisor(supervisor.WithDirective(&errors.PanicError{}, supervisor.EscalateDirective))
		child, err := parent.SpawnChild(ctx, "noSenderChild", NewMockSupervised(), WithSupervisor(escalationStrategy))
		require.NoError(t, err)
		require.NotNil(t, child)

		// send a test panic message to the actor
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		require.False(t, child.IsRunning())
		require.True(t, child.IsSuspended())

		//stop the actor
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("Suspend does not block when stop already queued", func(t *testing.T) {
		pid := MockSupervisionPID(t)
		pid.supervisionStopSignal <- types.Unit{}

		done := make(chan struct{})
		go func() {
			pid.suspend("test")
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("suspend blocked with pending supervision stop signal")
		}
	})
	t.Run("Stop signal emission is idempotent per cycle", func(t *testing.T) {
		pid := MockSupervisionPID(t)

		pid.stopSupervisionLoop()
		select {
		case <-pid.supervisionStopSignal:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("expected supervision stop signal")
		}

		pid.stopSupervisionLoop()
		select {
		case <-pid.supervisionStopSignal:
			t.Fatal("unexpected extra supervision stop signal")
		default:
		}

		pid.supervisionStopRequested.Store(false)
		pid.stopSupervisionLoop()
		select {
		case <-pid.supervisionStopSignal:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("expected supervision stop signal after reset")
		}
	})
	t.Run("When Parent shuts down child supervision loop does not deadlock", func(t *testing.T) {
		ctx := context.Background()

		actorSystem, err := NewActorSystem("panic-deadlock", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))
		t.Cleanup(func() { _ = actorSystem.Stop(ctx) })

		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)
		t.Cleanup(func() { _ = actorSystem.Unsubscribe(consumer) })

		parent, err := actorSystem.Spawn(ctx, "panic-parent", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// the default supervisor strategy is to stop the child on panic
		child, err := parent.SpawnChild(ctx, "panic-child", NewMockSupervised())
		require.NoError(t, err)
		require.NotNil(t, child)

		pause.For(200 * time.Millisecond)

		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		require.Eventually(t, func() bool {
			for message := range consumer.Iterator() {
				if event, ok := message.Payload().(*ActorSuspended); ok {
					if event.Address() == child.ID() {
						return true
					}
				}
			}
			return false
		}, 5*time.Second, 20*time.Millisecond, "timed out waiting for child suspension event")

		down := make(chan error, 1)
		go func() { down <- parent.Shutdown(ctx) }()

		select {
		case err := <-down:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("parent shutdown blocked after child panic")
		}
	})
}
func TestMessaging(t *testing.T) {
	t.Run("With happy", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		pid1, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		pid2, err := actorSystem.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(time.Second)

		err = pid1.Tell(ctx, pid2, new(testpb.TestSend))
		require.NoError(t, err)

		// send an ask
		reply, err := pid1.Ask(ctx, pid2, new(testpb.TestReply), time.Second)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		actual, ok := reply.(*testpb.Reply)
		require.True(t, ok)
		assert.True(t, proto.Equal(expected, actual))

		// wait a while because exchange is ongoing
		pause.For(time.Second)

		err = Tell(ctx, pid1, new(testpb.TestBye))
		require.NoError(t, err)

		pause.For(time.Second)

		assert.False(t, pid1.IsRunning())
		assert.True(t, pid2.IsRunning())

		err = Tell(ctx, pid2, new(testpb.TestBye))
		assert.NoError(t, err)
		pause.For(time.Second)
		assert.False(t, pid2.IsRunning())
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With Ask invalid timeout", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor path
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// send an ask
		_, err = pid1.Ask(ctx, pid2, new(testpb.TestReply), 0)
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrInvalidTimeout)

		require.NoError(t, pid1.Shutdown(ctx))
		require.NoError(t, pid2.Shutdown(ctx))
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With Ask when not ready", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor path
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(time.Second)

		assert.NoError(t, pid2.Shutdown(ctx))

		// send an ask
		reply, err := pid1.Ask(ctx, pid2, new(testpb.TestReply), 20*time.Second)
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrDead)
		require.Nil(t, reply)

		// wait a while because exchange is ongoing
		pause.For(time.Second)

		err = Tell(ctx, pid1, new(testpb.TestBye))
		require.NoError(t, err)
		pause.For(time.Second)

		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With Tell when not ready", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor path
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(time.Second)

		assert.NoError(t, pid2.Shutdown(ctx))

		// send an ask
		err = pid1.Tell(ctx, pid2, new(testpb.TestReply))
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrDead)

		// wait a while because exchange is ongoing
		pause.For(time.Second)

		err = Tell(ctx, pid1, new(testpb.TestBye))
		require.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With Ask timeout", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor path
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		actor2 := NewMockActor()
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		err = pid1.Tell(ctx, pid2, new(testpb.TestSend))
		require.NoError(t, err)

		// send an ask
		reply, err := pid1.Ask(ctx, pid2, new(testpb.TestTimeout), replyTimeout)
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrRequestTimeout)
		require.Nil(t, reply)

		// wait a while because exchange is ongoing
		pause.For(time.Second)

		err = Tell(ctx, pid1, new(testpb.TestBye))
		require.NoError(t, err)

		pause.For(time.Second)
		assert.False(t, pid1.IsRunning())
		assert.True(t, pid2.IsRunning())

		pause.For(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))
		assert.False(t, pid2.IsRunning())
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With Ask context timed out or canceled", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor path
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		actor2 := NewMockActor()
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		err = pid1.Tell(ctx, pid2, new(testpb.TestSend))
		require.NoError(t, err)

		// send an ask
		// create a context with timeout
		cancelCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		reply, err := pid1.Ask(cancelCtx, pid2, new(testpb.TestTimeout), replyTimeout)
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrRequestTimeout)
		require.Nil(t, reply)

		// wait a while because exchange is ongoing
		pause.For(time.Second)

		err = Tell(ctx, pid1, new(testpb.TestBye))
		require.NoError(t, err)

		pause.For(time.Second)
		assert.False(t, pid1.IsRunning())
		assert.True(t, pid2.IsRunning())

		pause.For(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))
		assert.False(t, pid2.IsRunning())
		assert.NoError(t, actorSystem.Stop(ctx))
	})
}
func TestRemoting(t *testing.T) {
	t.Run("When remoting is enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger one
		actorName1 := "Exchange1"
		actorRef1, err := sys.Spawn(ctx, actorName1, &exchanger{})
		require.NoError(t, err)
		assert.NotNil(t, actorRef1)

		// create an exchanger two
		actorName2 := "Exchange2"
		actorRef2, err := sys.Spawn(ctx, actorName2, &exchanger{})
		require.NoError(t, err)
		assert.NotNil(t, actorRef2)

		// get the address of the exchanger actor one
		remotePID1, err := actorRef2.RemoteLookup(ctx, host, remotingPort, actorName1)
		require.NoError(t, err)
		require.NotNil(t, remotePID1)

		// send the message to exchanger actor one using remote messaging
		reply, err := actorRef2.Ask(ctx, remotePID1, new(testpb.TestReply), replyTimeout)
		// perform some assertions
		require.NoError(t, err)
		require.NotNil(t, reply)

		actual, ok := reply.(*testpb.Reply)
		require.True(t, ok)
		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, actual))

		// send a message to stop the first exchange actor
		err = actorRef2.Tell(ctx, remotePID1, new(testpb.TestRemoteSend))
		require.NoError(t, err)

		// stop the actor after some time
		pause.For(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("When remoting is disabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger one
		actorName1 := "Exchange1"
		actorRef1, err := sys.Spawn(ctx, actorName1, &exchanger{})
		require.NoError(t, err)
		assert.NotNil(t, actorRef1)

		// create an exchanger two
		actorName2 := "Exchange2"
		actorRef2, err := sys.Spawn(ctx, actorName2, &exchanger{})
		require.NoError(t, err)
		assert.NotNil(t, actorRef2)

		// get the address of the exchanger actor one
		remotePID1, err := actorRef2.RemoteLookup(ctx, host, remotingPort, actorName1)
		require.NoError(t, err)
		require.NotNil(t, remotePID1)

		actorRef2.remoting = nil
		// send the message to exchanger actor one using remote messaging
		reply, err := actorRef2.Ask(ctx, remotePID1, new(testpb.TestReply), replyTimeout)
		// perform some assertions
		require.Error(t, err)
		require.Nil(t, reply)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)

		// send a message to stop the first exchange actor
		err = actorRef2.Tell(ctx, remotePID1, new(testpb.TestRemoteSend))
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)

		// stop the actor after some time
		pause.For(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
}
func TestPIDRemotingEnabledGuard(t *testing.T) {
	type actorSystemWrapper struct {
		*actorSystem
	}

	t.Run("Actor system nil", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		pid := &PID{
			logger:   log.DiscardLogger,
			remoting: remotingMock,
			address:  address.New("pid", "sys", "127.0.0.1", 9000),
		}

		err := pid.remoteTell(context.Background(), address.New("target", "sys", "127.0.0.1", 9001), new(testpb.TestSend))
		require.ErrorIs(t, err, errors.ErrRemotingDisabled)
		remotingMock.AssertNotCalled(t, "RemoteTell", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("Non actorSystem implementation", func(t *testing.T) {
		sys, err := NewActorSystem("wrapper-sys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)

		remotingMock := mocksremote.NewClient(t)
		remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

		pid := &PID{
			logger:      log.DiscardLogger,
			actorSystem: &actorSystemWrapper{actorSystem: sys.(*actorSystem)},
			remoting:    remotingMock,
			address:     address.New("pid", "sys", "127.0.0.1", 9000),
		}

		err = pid.remoteTell(context.Background(), address.New("target", "sys", "127.0.0.1", 9001), new(testpb.TestSend))
		require.NoError(t, err)
	})
}

func TestActorHandle(t *testing.T) {
	ctx := context.TODO()
	host := "127.0.0.1"
	ports := dynaport.Get(1)

	actorSystem, err := NewActorSystem("testSys",
		WithRemote(remote.NewConfig(host, ports[0])),
		WithLogger(log.DiscardLogger))

	require.NoError(t, err)
	require.NotNil(t, actorSystem)

	require.NoError(t, actorSystem.Start(ctx))

	pause.For(time.Second)

	// create the actor ref
	pid, err := actorSystem.Spawn(ctx, "Exchanger", &exchanger{})

	require.NoError(t, err)
	assert.NotNil(t, pid)
	actorHandle := pid.Actor()
	assert.IsType(t, &exchanger{}, actorHandle)
	var p interface{} = actorHandle
	_, ok := p.(Actor)
	assert.True(t, ok)
	// stop the actor
	err = pid.Shutdown(ctx)
	assert.NoError(t, err)
	pause.For(time.Second)
	assert.NoError(t, actorSystem.Stop(ctx))
}
func TestPIDActorSystem(t *testing.T) {
	ctx := context.TODO()
	host := "127.0.0.1"
	ports := dynaport.Get(1)

	actorSystem, err := NewActorSystem("testSys",
		WithRemote(remote.NewConfig(host, ports[0])),
		WithLogger(log.DiscardLogger))

	require.NoError(t, err)
	require.NotNil(t, actorSystem)

	require.NoError(t, actorSystem.Start(ctx))

	pause.For(time.Second)

	// create the actor ref
	pid, err := actorSystem.Spawn(ctx, "Exchanger", &exchanger{})

	require.NoError(t, err)
	assert.NotNil(t, pid)
	sys := pid.ActorSystem()
	require.NotNil(t, sys)
	// stop the actor
	err = pid.Shutdown(ctx)
	require.NoError(t, err)
	pause.For(time.Second)
	assert.NoError(t, actorSystem.Stop(ctx))
}
func TestSpawnChild(t *testing.T) {
	t.Run("With restarting child actor", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the parent actor
		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())
		require.NoError(t, err)
		assert.NotNil(t, parent)

		pause.For(time.Second)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		pause.For(time.Second)

		require.Len(t, parent.Children(), 1)
		metric := parent.Metric(ctx)
		require.EqualValues(t, 1, metric.ChidrenCount())

		// stop the child actor
		require.NoError(t, child.Shutdown(ctx))

		pause.For(time.Second)
		require.False(t, child.IsRunning())
		require.Empty(t, parent.Children())

		// create the child actor
		child, err = parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		pause.For(time.Second)

		assert.Len(t, parent.Children(), 1)
		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With empty children when actor is not running or does not exist", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the parent actor
		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())
		require.NoError(t, err)
		assert.NotNil(t, parent)

		pause.For(time.Second)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		pause.For(time.Second)

		require.Len(t, parent.Children(), 1)

		// stop the child actor
		require.NoError(t, child.Shutdown(ctx))

		pause.For(time.Second)
		require.False(t, child.IsRunning())
		require.Empty(t, parent.Children())

		// stop the parent actor
		require.NoError(t, parent.Shutdown(ctx))
		pause.For(time.Second)

		require.Empty(t, parent.Children())
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With starting child actor with a different mailbox", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the parent actor
		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "child", NewMockSupervised(), WithMailbox(NewBoundedMailbox(20)))
		assert.NoError(t, err)
		assert.NotNil(t, child)
		pause.For(time.Second)
		assert.Len(t, parent.Children(), 1)

		assert.NoError(t, Tell(ctx, child, new(testpb.TestSend)))

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With recreating the same child actor will no-op", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the parent actor
		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)

		pause.For(100 * time.Millisecond)
		// create the child actor
		child, err = parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		pause.For(time.Second)

		assert.Len(t, parent.Children(), 1)
		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With parent not ready", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the parent actor
		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())
		require.NoError(t, err)
		assert.NotNil(t, parent)

		pause.For(100 * time.Millisecond)
		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised())
		assert.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrDead)
		assert.Nil(t, child)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With failed init", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the parent actor
		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", &MockPreStart{})
		assert.Error(t, err)
		assert.Nil(t, child)

		assert.Len(t, parent.Children(), 0)
		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With child created event published", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// add a subscriber
		subsriber, err := actorSystem.Subscribe()
		require.NoError(t, err)

		// create the parent actor
		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)

		pause.For(time.Second)

		var events []*ActorChildCreated
		for message := range subsriber.Iterator() {
			// get the event payload
			payload := message.Payload()
			switch msg := payload.(type) {
			case *ActorChildCreated:
				events = append(events, msg)
			}
		}

		require.NotEmpty(t, events)
		require.Len(t, events, 1)

		event := events[0]
		assert.Equal(t, parent.ID(), event.Parent())

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With starting child actor with a custom passivation setting", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the parent actor
		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "child",
			NewMockSupervised(),
			WithMailbox(NewBoundedMailbox(20)),
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(time.Second)))

		require.NoError(t, err)
		require.NotNil(t, child)

		require.Eventually(t, func() bool {
			return len(parent.Children()) == 1
		}, time.Second, 10*time.Millisecond)

		// wait until the child passivates after inactivity
		require.Eventually(t, func() bool {
			return !child.IsRunning()
		}, 3*time.Second, 20*time.Millisecond)

		err = Tell(ctx, child, new(testpb.TestSend))
		assert.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrDead)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With starting child actor with long lived", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the parent actor
		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "child",
			NewMockSupervised(),
			WithMailbox(NewBoundedMailbox(20)))

		pause.For(time.Second)

		assert.NoError(t, err)
		assert.NotNil(t, child)
		pause.For(time.Second)
		assert.Len(t, parent.Children(), 1)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			pause.For(time.Second)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()

		err = Tell(ctx, child, new(testpb.TestSend))
		assert.NoError(t, err)
		assert.True(t, child.IsRunning())

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
}
func TestPoisonPill(t *testing.T) {
	ctx := context.TODO()
	host := "127.0.0.1"
	ports := dynaport.Get(1)

	actorSystem, err := NewActorSystem("testSys",
		WithRemote(remote.NewConfig(host, ports[0])),
		WithLogger(log.DiscardLogger))

	require.NoError(t, err)
	require.NotNil(t, actorSystem)

	require.NoError(t, actorSystem.Start(ctx))

	pause.For(time.Second)

	// create the actor ref
	pid, err := actorSystem.Spawn(ctx, "PoisonPill", NewMockActor())

	require.NoError(t, err)
	assert.NotNil(t, pid)

	assert.True(t, pid.IsRunning())
	// send a poison pill to the actor
	err = Tell(ctx, pid, new(PoisonPill))
	assert.NoError(t, err)

	// wait for the graceful shutdown
	pause.For(time.Second)
	assert.False(t, pid.IsRunning())
	pause.For(time.Second)
	assert.NoError(t, actorSystem.Stop(ctx))
}
func TestRemoteLookup(t *testing.T) {
	t.Run("With actor address not found", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger 1
		actorName1 := "Exchange1"
		actorRef1, err := sys.Spawn(ctx, actorName1, &exchanger{})

		require.NoError(t, err)
		assert.NotNil(t, actorRef1)

		// let us lookup actor two — it does not exist, expect ErrActorNotFound
		actorName2 := "Exchange2"
		remotePID, err := actorRef1.RemoteLookup(ctx, host, remotingPort, actorName2)
		require.Error(t, err)
		require.Nil(t, remotePID)
		require.ErrorIs(t, err, errors.ErrActorNotFound)

		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With remoting server is unreachable", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger))
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger 1
		actorName1 := "Exchange1"
		actorRef1, err := sys.Spawn(ctx, actorName1, &exchanger{})

		require.NoError(t, err)
		assert.NotNil(t, actorRef1)

		// let us lookup actor two
		actorName2 := "Exchange2"
		addr, err := actorRef1.RemoteLookup(ctx, host, remotingPort, actorName2)
		require.Error(t, err)
		require.Nil(t, addr)

		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With remoting is not enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger 1
		actorName1 := "Exchange1"
		actorRef1, err := sys.Spawn(ctx, actorName1, &exchanger{})

		require.NoError(t, err)
		assert.NotNil(t, actorRef1)

		actorRef1.remoting = nil

		// let us lookup actor two
		actorName2 := "Exchange2"
		addr, err := actorRef1.RemoteLookup(ctx, host, remotingPort, actorName2)
		require.Error(t, err)
		require.Nil(t, addr)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)

		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
	})
}
func TestFailedPreStart(t *testing.T) {
	// create the context
	ctx := context.TODO()
	// define the logger to use
	logger := log.DiscardLogger

	// create the actor system
	sys, err := NewActorSystem("test",
		WithLogger(logger),
		WithActorInitMaxRetries(1))
	// assert there are no error
	require.NoError(t, err)

	// start the actor system
	err = sys.Start(ctx)
	assert.NoError(t, err)

	// create an exchanger 1
	actorName1 := "Exchange1"
	pid, err := sys.Spawn(ctx, actorName1, &MockPreStart{})
	require.Error(t, err)
	require.ErrorIs(t, err, errors.ErrInitFailure)
	require.Nil(t, pid)

	t.Cleanup(func() {
		assert.NoError(t, sys.Stop(ctx))
	})
}
func TestFailedPostStop(t *testing.T) {
	ctx := context.TODO()
	host := "127.0.0.1"
	ports := dynaport.Get(1)

	actorSystem, err := NewActorSystem("testSys",
		WithRemote(remote.NewConfig(host, ports[0])),
		WithLogger(log.DiscardLogger))

	require.NoError(t, err)
	require.NotNil(t, actorSystem)

	require.NoError(t, actorSystem.Start(ctx))

	pause.For(time.Second)
	pid, err := actorSystem.Spawn(ctx, "PostStop", &MockPostStop{})

	require.NoError(t, err)
	assert.NotNil(t, pid)

	assert.Error(t, pid.Shutdown(ctx))
	pause.For(time.Second)
	assert.NoError(t, actorSystem.Stop(ctx))
}
func TestShutdown(t *testing.T) {
	t.Run("With Shutdown panic to child stop failure", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "test", NewMockSupervisor())
		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", &MockPostStop{})
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)

		//stop the
		assert.Error(t, parent.Shutdown(ctx))
		pause.For(2 * time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
}
func TestBatchTell(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor path
		actor := NewMockActor()
		pid, err := actorSystem.Spawn(ctx, "test", actor)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// batch tell
		require.NoError(t, pid.BatchTell(ctx, pid, new(testpb.TestSend), new(testpb.TestSend)))
		// wait for the asynchronous processing to complete
		pause.For(100 * time.Millisecond)
		assert.EqualValues(t, 2, pid.ProcessedCount()-1)
		// shutdown the actor when
		// wait a while because exchange is ongoing
		pause.For(time.Second)
		assert.NoError(t, pid.Shutdown(ctx))
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With a Tell behavior", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor path
		actor := NewMockActor()
		pid, err := actorSystem.Spawn(ctx, "test", actor)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// batch tell
		require.NoError(t, pid.BatchTell(ctx, pid, new(testpb.TestSend)))
		// wait for the asynchronous processing to complete
		pause.For(100 * time.Millisecond)
		assert.EqualValues(t, 1, pid.ProcessedCount()-1)
		// shutdown the actor when
		// wait a while because exchange is ongoing
		pause.For(time.Second)
		assert.NoError(t, pid.Shutdown(ctx))
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With a dead actor", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)
		// create the actor path
		actor := NewMockActor()
		pid, err := actorSystem.Spawn(ctx, "test", actor)
		require.NoError(t, err)
		require.NotNil(t, pid)

		pause.For(time.Second)
		assert.NoError(t, pid.Shutdown(ctx))

		pause.For(time.Second)
		// batch tell
		require.Error(t, pid.BatchTell(ctx, pid, new(testpb.TestSend)))
		assert.NoError(t, actorSystem.Stop(ctx))
	})
}
func TestBatchAsk(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)
		// create the actor path
		actor := &exchanger{}

		pid, err := actorSystem.Spawn(ctx, "test", actor)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// batch ask
		responses, err := pid.BatchAsk(ctx, pid, []any{new(testpb.TestReply), new(testpb.TestReply)}, replyTimeout)
		require.NoError(t, err)
		for reply := range responses {
			require.NoError(t, err)
			require.NotNil(t, reply)
			expected := new(testpb.Reply)
			actual, ok := reply.(*testpb.Reply)
			require.True(t, ok)
			assert.True(t, proto.Equal(expected, actual))
		}

		// wait a while because exchange is ongoing
		pause.For(time.Second)
		assert.NoError(t, pid.Shutdown(ctx))
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With a dead actor", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)
		// create the actor path
		actor := &exchanger{}

		pid, err := actorSystem.Spawn(ctx, "test", actor)
		require.NoError(t, err)
		require.NotNil(t, pid)

		pause.For(time.Second)
		assert.NoError(t, pid.Shutdown(ctx))

		pause.For(time.Second)

		// batch ask
		responses, err := pid.BatchAsk(ctx, pid, []any{new(testpb.TestReply), new(testpb.TestReply)}, replyTimeout)
		require.Error(t, err)
		require.Nil(t, responses)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With a timeout", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor path
		actor := NewMockActor()

		pid, err := actorSystem.Spawn(ctx, "test", actor)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// batch ask
		responses, err := pid.BatchAsk(ctx, pid, []any{new(testpb.TestTimeout), new(testpb.TestReply)}, replyTimeout)
		require.Error(t, err)
		require.Empty(t, responses)

		// wait a while because exchange is ongoing
		pause.For(time.Second)
		assert.NoError(t, pid.Shutdown(ctx))
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
}
func TestRemoteReSpawn(t *testing.T) {
	t.Run("With actor not found returns ErrActorNotFound", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))

		actorRef, err := sys.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		// RemoteReSpawn for non-existent actor: addr is nil, returns not found
		remotePID, err := actorRef.RemoteReSpawn(ctx, host, remotingPort, "nonexistent")
		require.Error(t, err)
		require.Nil(t, remotePID)
		assert.ErrorIs(t, err, errors.ErrActorNotFound)

		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})

	t.Run("With actor found returns remote PID", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))

		actorRef, err := sys.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		remotePID, err := actorRef.RemoteReSpawn(ctx, host, remotingPort, "Exchange1")
		require.NoError(t, err)
		require.NotNil(t, remotePID)
		assert.True(t, remotePID.IsRemote())
		assert.Equal(t, "Exchange1", remotePID.Name())

		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})

	t.Run("With remoting not enabled returns ErrRemotingDisabled", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))

		actorRef, err := sys.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		actorRef.remoting = nil

		remotePID, err := actorRef.RemoteReSpawn(ctx, host, remotingPort, "Exchange2")
		require.Error(t, err)
		require.Nil(t, remotePID)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)

		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})

	t.Run("With remoting server unreachable returns error", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		sys, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))

		actorRef, err := sys.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)

		remotePID, err := actorRef.RemoteReSpawn(ctx, host, remotingPort, "Exchange2")
		require.Error(t, err)
		require.Nil(t, remotePID)

		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})
}
func TestRemoteStop(t *testing.T) {
	t.Run("With actor address not found", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger 1
		actorName1 := "Exchange1"
		actorRef1, err := sys.Spawn(ctx, actorName1, &exchanger{})

		require.NoError(t, err)
		assert.NotNil(t, actorRef1)

		// let us lookup actor two
		actorName2 := "Exchange2"
		err = actorRef1.RemoteStop(ctx, host, remotingPort, actorName2)
		require.NoError(t, err)

		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With remoting enabled and actor started", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger 1
		actorName1 := "Exchange1"
		actorRef1, err := sys.Spawn(ctx, actorName1, &exchanger{})
		require.NoError(t, err)
		assert.NotNil(t, actorRef1)

		// create an exchanger 1
		actorName2 := "Exchange2"
		actorRef2, err := sys.Spawn(ctx, actorName2, &exchanger{})
		require.NoError(t, err)
		assert.NotNil(t, actorRef2)

		err = actorRef1.RemoteStop(ctx, host, remotingPort, actorName2)
		require.NoError(t, err)

		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With remoting enabled and actor not started", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger 1
		actorName1 := "Exchange1"
		actorRef1, err := sys.Spawn(ctx, actorName1, &exchanger{})
		require.NoError(t, err)
		assert.NotNil(t, actorRef1)

		actorName2 := "Exchange2"
		err = actorRef1.RemoteStop(ctx, host, remotingPort, actorName2)
		require.NoError(t, err)

		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With remoting is not enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger 1
		actorName1 := "Exchange1"
		actorRef1, err := sys.Spawn(ctx, actorName1, &exchanger{})
		require.NoError(t, err)
		assert.NotNil(t, actorRef1)

		actorRef1.remoting = nil

		actorName2 := "Exchange2"
		err = actorRef1.RemoteStop(ctx, host, remotingPort, actorName2)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)

		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
	})
}
func TestEquals(t *testing.T) {
	ctx := context.TODO()
	logger := log.DiscardLogger
	sys, err := NewActorSystem("test",
		WithLogger(logger))

	require.NoError(t, err)
	err = sys.Start(ctx)
	assert.NoError(t, err)

	pid1, err := sys.Spawn(ctx, "test", NewMockActor())
	require.NoError(t, err)
	assert.NotNil(t, pid1)

	pid2, err := sys.Spawn(ctx, "exchange", &exchanger{})
	require.NoError(t, err)
	assert.NotNil(t, pid2)

	noSender := sys.NoSender()
	assert.False(t, pid1.Equals(noSender))
	assert.False(t, pid2.Equals(noSender))
	assert.False(t, pid1.Equals(pid2))

	err = sys.Stop(ctx)
	assert.NoError(t, err)
}
func TestName(t *testing.T) {
	ctx := context.TODO()
	host := "127.0.0.1"
	ports := dynaport.Get(1)

	actorSystem, err := NewActorSystem("testSys",
		WithRemote(remote.NewConfig(host, ports[0])),
		WithLogger(log.DiscardLogger))

	require.NoError(t, err)
	require.NotNil(t, actorSystem)

	require.NoError(t, actorSystem.Start(ctx))

	pause.For(time.Second)

	// create the actor ref
	pid, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
	require.NoError(t, err)
	assert.NotNil(t, pid)

	expected := "Exchange1"
	actual := pid.Name()
	require.Equal(t, expected, actual)

	// stop the actor
	err = pid.Shutdown(ctx)
	assert.NoError(t, err)
	pause.For(time.Second)
	assert.NoError(t, actorSystem.Stop(ctx))
}
func TestPipeTo(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		askTimeout := time.Minute
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(500 * time.Millisecond)
		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := func() (any, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return new(testpb.TaskComplete), nil
		}

		err = pid1.PipeTo(ctx, pid2, task)
		require.NoError(t, err)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			// Wait for some time and during that period send some messages to the actor
			// send three messages while waiting for the future to completed
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			pause.For(time.Second)
			wg.Done()
		}()
		wg.Wait()

		pause.For(time.Second)

		require.EqualValues(t, 3, pid1.ProcessedCount()-1)
		require.EqualValues(t, 1, pid2.ProcessedCount()-1)

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With dead actor", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// shutdown the actor after one second of liveness
		pause.For(time.Second)
		require.NoError(t, pid2.Shutdown(ctx))

		task := func() (any, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return new(testpb.TaskComplete), nil
		}

		err = pid1.PipeTo(ctx, pid2, task)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrDead)

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With undefined task", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(time.Second)

		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		err = pid1.PipeTo(ctx, pid2, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrUndefinedTask)

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With failed task result in deadletter", func(t *testing.T) {
		askTimeout := time.Minute
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create a deadletter subscriber
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create actor1
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(time.Second)

		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := func() (any, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return nil, assert.AnError
		}

		cancelCtx, cancel := context.WithCancel(ctx)
		err = pid1.PipeTo(cancelCtx, pid2, task)
		require.NoError(t, err)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			// Wait for some time and during that period send some messages to the actor
			// send three messages while waiting for the future to completed
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			pause.For(time.Second)
			wg.Done()
		}()

		cancel()
		wg.Wait()

		pause.For(time.Second)

		require.EqualValues(t, 3, pid1.ProcessedCount()-1)

		// no message piped to the actor
		require.Zero(t, pid2.ProcessedCount()-1)

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

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With explicit timeout", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))
		pause.For(time.Second)

		// create a deadletter subscriber
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create actor1
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(500 * time.Millisecond)
		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := func() (any, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return new(testpb.TaskComplete), nil
		}

		err = pid1.PipeTo(ctx, pid2, task, WithTimeout(500*time.Millisecond))
		require.NoError(t, err)
		pause.For(time.Second)

		// no message piped to the actor
		require.Zero(t, pid2.ProcessedCount()-1)

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

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With successful circuit breaker", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))
		pause.For(time.Second)

		// create a deadletter subscriber
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create actor1
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(500 * time.Millisecond)
		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := func() (any, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return new(testpb.TaskComplete), nil
		}

		cb := breaker.NewCircuitBreaker(
			breaker.WithFailureRate(0.5),
			breaker.WithMinRequests(2),
			breaker.WithOpenTimeout(50*time.Millisecond),
			breaker.WithWindow(100*time.Millisecond, 2),
			breaker.WithHalfOpenMaxCalls(1),
		)

		err = pid1.PipeTo(ctx, pid2, task, WithCircuitBreaker(cb))
		require.NoError(t, err)
		pause.For(time.Second)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			// Wait for some time and during that period send some messages to the actor
			// send three messages while waiting for the future to completed
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), time.Minute)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), time.Minute)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), time.Minute)
			pause.For(time.Second)
			wg.Done()
		}()
		wg.Wait()

		pause.For(time.Second)

		require.EqualValues(t, 3, pid1.ProcessedCount()-1)
		require.EqualValues(t, 1, pid2.ProcessedCount()-1)

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With circuit breaker on failed task", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))
		pause.For(time.Second)

		// create a deadletter subscriber
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create actor1
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(500 * time.Millisecond)
		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := func() (any, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return nil, assert.AnError
		}

		cb := breaker.NewCircuitBreaker(
			breaker.WithFailureRate(0.5),
			breaker.WithMinRequests(1),
		)

		err = pid1.PipeTo(ctx, pid2, task, WithCircuitBreaker(cb))
		require.NoError(t, err)
		pause.For(time.Second)

		// no message piped to the actor
		require.Zero(t, pid2.ProcessedCount()-1)

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

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With dead actor after task scheduled", func(t *testing.T) {
		askTimeout := time.Minute
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create a deadletter subscriber
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create actor1
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(500 * time.Millisecond)
		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := func() (any, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return new(testpb.TaskComplete), nil
		}

		err = pid1.PipeTo(ctx, pid2, task)
		require.NoError(t, err)

		require.NoError(t, pid2.Shutdown(ctx))

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			// Wait for some time and during that period send some messages to the actor
			// send three messages while waiting for the future to completed
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			pause.For(time.Second)
			wg.Done()
		}()
		wg.Wait()

		pause.For(time.Second)

		require.EqualValues(t, 3, pid1.ProcessedCount()-1)

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
		msg := items[0].Message()
		require.NotNil(t, msg)
		actual, ok := msg.(*testpb.TaskComplete)
		require.True(t, ok)
		require.True(t, proto.Equal(new(testpb.TaskComplete), actual))

		pause.For(time.Second)
		require.NoError(t, pid1.Shutdown(ctx))
		pause.For(time.Second)
		require.NoError(t, actorSystem.Stop(ctx))
	})
}
func TestSendAsync(t *testing.T) {
	t.Run("With local actor", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		receivingActor := NewMockActor()
		receiver, err := actorSystem.Spawn(ctx, "receiver", receivingActor)
		assert.NoError(t, err)
		assert.NotNil(t, receiver)

		pause.For(time.Second)

		sender, err := actorSystem.Spawn(ctx, "sender", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		pause.For(time.Second)

		err = sender.SendAsync(ctx, receiver.Name(), new(testpb.TestSend))
		require.NoError(t, err)

		t.Cleanup(func() {
			assert.NoError(t, actorSystem.Stop(ctx))
		})
	})
	t.Run("With dead Sender", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger))

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		receivingActor := NewMockActor()
		receiver, err := actorSystem.Spawn(ctx, "receiver", receivingActor)
		assert.NoError(t, err)
		assert.NotNil(t, receiver)

		pause.For(time.Second)

		sender, err := actorSystem.Spawn(ctx, "sender", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		pause.For(time.Second)

		require.NoError(t, actorSystem.Kill(ctx, sender.Name()))

		err = sender.SendAsync(ctx, receiver.Name(), new(testpb.TestSend))
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrDead)

		t.Cleanup(func() {
			assert.NoError(t, actorSystem.Stop(ctx))
		})
	})
	t.Run("With cluster enabled", func(t *testing.T) {
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

		// create an actor on node1
		receiver, err := node1.Spawn(ctx, "receiver", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, receiver)
		pause.For(time.Second)

		sender, err := node2.Spawn(ctx, "sender", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		pause.For(time.Second)

		err = sender.SendAsync(ctx, receiver.Name(), new(testpb.TestSend))
		require.NoError(t, err)

		t.Cleanup(func() {
			assert.NoError(t, node1.Stop(ctx))
			assert.NoError(t, node2.Stop(ctx))
			assert.NoError(t, sd2.Close())
			assert.NoError(t, sd1.Close())
			srv.Shutdown()
		})
	})
	t.Run("With cluster enabled and Actor not found", func(t *testing.T) {
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

		sender, err := node2.Spawn(ctx, "sender", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		pause.For(time.Second)

		err = sender.SendAsync(ctx, "receiver", new(testpb.TestSend))
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrActorNotFound)

		t.Cleanup(func() {
			assert.NoError(t, node1.Stop(ctx))
			assert.NoError(t, node2.Stop(ctx))
			assert.NoError(t, sd2.Close())
			assert.NoError(t, sd1.Close())
			srv.Shutdown()
		})
	})
}
func TestSendSync(t *testing.T) {
	t.Run("With local actor", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		receiver, err := actorSystem.Spawn(ctx, "receiver", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, receiver)

		pause.For(time.Second)

		sender, err := actorSystem.Spawn(ctx, "sender", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		pause.For(time.Second)

		response, err := sender.SendSync(ctx, receiver.Name(), new(testpb.TestReply), replyTimeout)
		require.NoError(t, err)
		require.NotNil(t, response)
		expected := &testpb.Reply{Content: "received message"}

		actual, ok := response.(*testpb.Reply)
		require.True(t, ok)
		assert.True(t, proto.Equal(expected, actual))

		t.Cleanup(func() {
			assert.NoError(t, actorSystem.Stop(ctx))
		})
	})
	t.Run("With dead Sender", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		receiver, err := actorSystem.Spawn(ctx, "receiver", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, receiver)

		pause.For(time.Second)

		sender, err := actorSystem.Spawn(ctx, "sender", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		pause.For(time.Second)
		err = actorSystem.Kill(ctx, sender.Name())
		require.NoError(t, err)

		response, err := sender.SendSync(ctx, receiver.Name(), new(testpb.TestReply), replyTimeout)
		require.Error(t, err)
		require.Nil(t, response)
		assert.ErrorIs(t, err, errors.ErrDead)

		t.Cleanup(func() {
			assert.NoError(t, actorSystem.Stop(ctx))
		})
	})
	t.Run("With cluster enabled", func(t *testing.T) {
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

		// create an actor on node1
		receiver, err := node1.Spawn(ctx, "receiver", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, receiver)
		pause.For(time.Second)

		sender, err := node2.Spawn(ctx, "sender", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		pause.For(time.Second)

		response, err := sender.SendSync(ctx, receiver.Name(), new(testpb.TestReply), replyTimeout)
		require.NoError(t, err)
		require.NotNil(t, response)
		expected := &testpb.Reply{Content: "received message"}
		actual, ok := response.(*testpb.Reply)
		require.True(t, ok)
		assert.True(t, proto.Equal(expected, actual))

		t.Cleanup(func() {
			assert.NoError(t, node1.Stop(ctx))
			assert.NoError(t, node2.Stop(ctx))
			assert.NoError(t, sd2.Close())
			assert.NoError(t, sd1.Close())
			srv.Shutdown()
		})
	})
	t.Run("With cluster enabled and Actor not found", func(t *testing.T) {
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

		sender, err := node2.Spawn(ctx, "sender", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		pause.For(time.Second)

		response, err := sender.SendSync(ctx, "receiver", new(testpb.TestReply), time.Minute)
		require.Nil(t, response)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrActorNotFound)

		t.Cleanup(func() {
			assert.NoError(t, node1.Stop(ctx))
			assert.NoError(t, node2.Stop(ctx))
			assert.NoError(t, sd2.Close())
			assert.NoError(t, sd1.Close())
			srv.Shutdown()
		})
	})
}
func TestStopChild(t *testing.T) {
	t.Run("With Stop failure", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "parent", NewMockSupervisor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", new(MockPostStop))
		require.NoError(t, err)
		require.NotNil(t, child)
		require.Len(t, parent.Children(), 1)
		// stop the child actor
		require.Error(t, parent.Stop(ctx, child))

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
}
func TestNewPID(t *testing.T) {
	t.Run("With actor path not defined", func(t *testing.T) {
		ctx := context.Background()
		pid, err := newPID(
			ctx,
			nil,
			NewMockActor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger))

		require.Error(t, err)
		assert.Nil(t, pid)
	})
	t.Run("With metrics registration error", func(t *testing.T) {
		ctx := context.Background()

		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		addr := address.New("actor", actorSystem.Name(), "127.0.0.1", 0)

		t.Cleanup(func() { otel.SetMeterProvider(noopmetric.NewMeterProvider()) })

		errRegister := assert.AnError
		baseProvider := noopmetric.NewMeterProvider()
		otel.SetMeterProvider(&MockMeterProvider{
			MeterProvider: baseProvider,
			meter: registerCallbackFailingMeter{
				Meter: baseProvider.Meter("test"),
				err:   errRegister,
			},
		})
		metricProvider := metric.NewProvider()

		pid, err := newPID(
			ctx,
			addr,
			NewMockActor(),
			withActorSystem(actorSystem),
			withMetricProvider(metricProvider),
			asSystemActor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		)

		require.Error(t, err)
		require.ErrorIs(t, err, errRegister)
		require.Nil(t, pid)
	})
	t.Run("With metric instrument creation error", func(t *testing.T) {
		ctx := context.Background()

		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		addr := address.New("actor", actorSystem.Name(), "127.0.0.1", 0)

		t.Cleanup(func() { otel.SetMeterProvider(noopmetric.NewMeterProvider()) })

		errInstrument := assert.AnError
		baseProvider := noopmetric.NewMeterProvider()
		otel.SetMeterProvider(&MockMeterProvider{
			MeterProvider: baseProvider,
			meter: instrumentFailingMeter{
				Meter: baseProvider.Meter("test"),
				failures: map[string]error{
					"actor.children.count": errInstrument,
				},
			},
		})
		metricProvider := metric.NewProvider()

		pid, err := newPID(
			ctx,
			addr,
			NewMockActor(),
			withActorSystem(actorSystem),
			withMetricProvider(metricProvider),
			asSystemActor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		)

		require.Error(t, err)
		require.EqualError(t, err, fmt.Sprintf("failed to create childrenCount instrument, %v", errInstrument))
		require.Nil(t, pid)
	})
	t.Run("With RegisterMetrics callback", func(t *testing.T) {
		ctx := context.Background()

		prevProvider := otel.GetMeterProvider()
		meterProvider := newManualMeterProvider()
		otel.SetMeterProvider(meterProvider)
		t.Cleanup(func() { otel.SetMeterProvider(prevProvider) })

		sys, err := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger),
			WithMetrics())
		require.NoError(t, err)
		require.NotNil(t, sys)

		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { require.NoError(t, sys.Stop(ctx)) })

		pid, err := sys.Spawn(ctx, "metrics-actor", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)

		manual, ok := meterProvider.meter.(*manualMeter)
		require.True(t, ok)
		require.NotEmpty(t, manual.callbacks)

		observer := &manualObserver{}
		for _, cb := range manual.callbacks {
			require.NoError(t, cb(ctx, observer))
		}
		require.NotEmpty(t, observer.records)
	})
}

func TestLogger(t *testing.T) {
	buffer := new(bytes.Buffer)

	pid := &PID{
		logger:       log.New(log.InfoLevel, buffer),
		fieldsLocker: sync.RWMutex{},
	}

	pid.getLogger().Info("test debug")
	actual, err := extractMessage(buffer.Bytes())
	require.NoError(t, err)

	expected := "test debug"
	require.Equal(t, expected, actual)

	t.Cleanup(func() {
		// reset the buffer
		buffer.Reset()
	})
}
func TestDeadletterCountMetric(t *testing.T) {
	ctx := context.TODO()
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

	// start the actor system
	err := sys.Start(ctx)
	assert.NoError(t, err)

	// wait for complete start
	pause.For(time.Second)

	// create the black hole actor
	actor := &MockUnhandled{}
	actorName := "actorName"
	actorRef, err := sys.Spawn(ctx, actorName, actor)
	assert.NoError(t, err)
	assert.NotNil(t, actorRef)

	// wait a while
	pause.For(time.Second)

	// every message sent to the actor will result in deadletter
	for range 5 {
		require.NoError(t, Tell(ctx, actorRef, new(testpb.TestSend)))
	}

	pause.For(time.Second)

	metric := actorRef.Metric(ctx)
	require.NotNil(t, metric)

	require.EqualValues(t, 5, metric.DeadlettersCount())

	err = sys.Stop(ctx)
	assert.NoError(t, err)
}
func TestWatch(t *testing.T) {
	ctx := context.TODO()
	host := "127.0.0.1"
	ports := dynaport.Get(1)

	actorSystem, err := NewActorSystem("testSys",
		WithRemote(remote.NewConfig(host, ports[0])),
		WithLogger(log.DiscardLogger))

	require.NoError(t, err)
	require.NotNil(t, actorSystem)

	require.NoError(t, actorSystem.Start(ctx))

	pause.For(time.Second)

	// create the actor ref
	pid, err := actorSystem.Spawn(ctx, "actor", NewMockActor())
	require.NoError(t, err)
	require.NotNil(t, pid)
	require.True(t, pid.IsRunning())

	// create another actor ref
	pid2, err := actorSystem.Spawn(ctx, "actor2", NewMockActor())
	require.NoError(t, err)
	require.NotNil(t, pid2)
	require.True(t, pid2.IsRunning())

	// let pid watch pid2
	pid.Watch(pid2)
	pnode, ok := pid2.ActorSystem().tree().node(pid2.ID())
	require.True(t, ok)

	found := false
	for _, watcher := range pnode.watchers {
		if watcher.Equals(pid) {
			found = true
			break
		}
	}

	require.True(t, found)
	pause.For(time.Second)
	assert.NoError(t, actorSystem.Stop(ctx))
}
func TestUnWatchWithInexistentNode(t *testing.T) {
	ctx := context.TODO()
	host := "127.0.0.1"
	ports := dynaport.Get(1)

	actorSystem, err := NewActorSystem("testSys",
		WithRemote(remote.NewConfig(host, ports[0])),
		WithLogger(log.DiscardLogger))

	require.NoError(t, err)
	require.NotNil(t, actorSystem)

	require.NoError(t, actorSystem.Start(ctx))

	pause.For(time.Second)

	// create the actor ref
	pid, err := actorSystem.Spawn(ctx, "actor", NewMockActor())
	require.NoError(t, err)
	require.NotNil(t, pid)
	require.True(t, pid.IsRunning())

	childPath := address.New("child", "sys", "127.0.0.1", ports[0])
	cid, err := newPID(
		ctx, childPath,
		NewMockSupervisor(),
		withInitMaxRetries(1),
		withActorSystem(actorSystem),
		withCustomLogger(log.DiscardLogger),
	)
	require.NoError(t, err)
	require.NotNil(t, cid)
	// this node does not exist in the actors tree
	pnode, ok := pid.ActorSystem().tree().node(cid.ID())
	require.False(t, ok)
	require.Nil(t, pnode)

	// this call will result in no-op
	cid.UnWatch(pid)

	pause.For(time.Second)
	require.NoError(t, cid.Shutdown(ctx))
	require.NoError(t, actorSystem.Stop(ctx))
}
func TestParentWithInexistenceNodeReturnsNil(t *testing.T) {
	ctx := context.TODO()
	host := "127.0.0.1"
	ports := dynaport.Get(1)

	actorSystem, err := NewActorSystem("testSys",
		WithRemote(remote.NewConfig(host, ports[0])),
		WithLogger(log.DiscardLogger))

	require.NoError(t, err)
	require.NotNil(t, actorSystem)

	require.NoError(t, actorSystem.Start(ctx))

	pause.For(time.Second)

	childPath := address.New("child", "sys", "127.0.0.1", ports[0])
	cid, err := newPID(
		ctx, childPath,
		NewMockSupervisor(),
		withInitMaxRetries(1),
		withActorSystem(actorSystem),
		withCustomLogger(log.DiscardLogger),
	)
	require.NoError(t, err)
	require.NotNil(t, cid)
	parent := cid.Parent()
	require.Nil(t, parent)
	require.NoError(t, cid.Shutdown(ctx))
	require.NoError(t, actorSystem.Stop(ctx))
}
func TestReinstate(t *testing.T) {
	t.Run("When PID is not started", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor ref
		pid, err := actorSystem.Spawn(ctx, "actor", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
		require.True(t, pid.IsRunning())

		// create another actor ref
		pid2, err := actorSystem.Spawn(ctx, "actor2", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid2)
		require.True(t, pid2.IsRunning())

		// let us stop both actors
		require.NoError(t, pid.Shutdown(ctx))
		require.NoError(t, pid2.Shutdown(ctx))

		pause.For(time.Second)

		// let us reinstate the actor pid2
		err = pid.Reinstate(pid2)
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrDead)

		pause.For(time.Second)
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("When actor is not defined", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor ref
		pid, err := actorSystem.Spawn(ctx, "actor", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
		require.True(t, pid.IsRunning())

		pause.For(time.Second)
		noSender := actorSystem.NoSender()
		err = pid.Reinstate(noSender)
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrUndefinedActor)

		pause.For(time.Second)
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("When actor not found", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor ref
		pid, err := actorSystem.Spawn(ctx, "actor", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
		require.True(t, pid.IsRunning())

		// create another actor ref
		pid2, err := actorSystem.Spawn(ctx, "actor2", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid2)
		require.True(t, pid2.IsRunning())

		require.NoError(t, pid2.Shutdown(ctx))

		pause.For(time.Second)

		// let us reinstate the actor pid2
		err = pid.Reinstate(pid2)
		require.Error(t, err)

		pause.For(time.Second)
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("When PID is already running", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor ref
		pid, err := actorSystem.Spawn(ctx, "actor", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
		require.True(t, pid.IsRunning())

		// create another actor ref
		pid2, err := actorSystem.Spawn(ctx, "actor2", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid2)
		require.True(t, pid2.IsRunning())

		// let us reinstate the actor pid2
		err = pid.Reinstate(pid2)
		require.NoError(t, err)

		pause.For(time.Second)
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("When remote caller without remoting returns ErrRemotingDisabled", func(t *testing.T) {
		addr := address.New("remote-actor", "remoteSystem", "10.0.0.1", 8080)
		remotePID := newRemotePID(addr, nil)
		other := newRemotePID(addr, nil)
		err := remotePID.Reinstate(other)
		require.ErrorIs(t, err, errors.ErrRemotingDisabled)
	})
	t.Run("When remote caller with remoting delegates to RemoteReinstate", func(t *testing.T) {
		host, port, name := "10.0.0.1", 8080, "remote-actor"
		addr := address.New(name, "remoteSystem", host, port)
		remotingMock := mocksremote.NewClient(t)
		remotingMock.EXPECT().RemoteReinstate(mock.Anything, host, port, name).Return(nil).Once()

		remoteCaller := newRemotePID(addr, remotingMock)
		remoteTarget := newRemotePID(addr, remotingMock)
		err := remoteCaller.Reinstate(remoteTarget)
		require.NoError(t, err)
		remotingMock.AssertExpectations(t)
	})
	t.Run("When remote caller with nil cid returns ErrUndefinedActor", func(t *testing.T) {
		host, port, name := "10.0.0.1", 8080, "remote-actor"
		addr := address.New(name, "remoteSystem", host, port)
		remotingMock := mocksremote.NewClient(t)
		remoteCaller := newRemotePID(addr, remotingMock)
		err := remoteCaller.Reinstate(nil)
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrUndefinedActor)
	})
}

func TestReinstateAvoidsPassivationRace(t *testing.T) {
	ctx := context.Background()

	actorSystem, err := NewActorSystem("reinstate-passivation-race", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, actorSystem.Start(ctx))
	t.Cleanup(func() { _ = actorSystem.Stop(ctx) })

	postStopCounter := atomic.Int32{}
	passiveAfter := 20 * time.Millisecond

	pid, err := actorSystem.Spawn(
		ctx,
		"reinstate-passivation-race-actor",
		&MockReinstateRaceActor{postStopCount: &postStopCounter},
		WithPassivationStrategy(passivation.NewLongLivedStrategy()),
	)
	require.NoError(t, err)
	require.NotNil(t, pid)
	t.Cleanup(func() { _ = pid.Shutdown(ctx) })
	require.Equal(t, int32(0), postStopCounter.Load(), "actor should not have stopped before test begins")

	// Swap to a short time-based strategy under lock so only the manual passivation
	// loop we trigger below uses it.
	pid.fieldsLocker.Lock()
	pid.passivationStrategy = passivation.NewTimeBasedStrategy(passiveAfter)
	pid.msgCountPassivation.Store(false)
	pid.fieldsLocker.Unlock()

	pid.latestReceiveTimeNano.Store(time.Now().Add(-time.Minute).UnixNano())
	pid.setState(passivationSkipNextState, false)
	pid.setState(passivatingState, false)
	pid.setState(suspendedState, false)
	pid.setState(runningState, true)

	pid.stopLocker.Lock()
	locked := true
	defer func() {
		if locked {
			pid.stopLocker.Unlock()
		}
	}()

	done := make(chan struct{})
	var passivated atomic.Bool
	go func() {
		defer close(done)
		passivated.Store(pid.tryPassivation("test-passivation"))
	}()

	require.Eventually(t, func() bool {
		return pid.isStateSet(passivatingState)
	}, time.Second, 5*time.Millisecond)

	// Simulate a reinstate arriving after the initial skip check but before doStop executes.
	pid.setState(suspendedState, true)
	pid.doReinstate()
	require.True(t, pid.isStateSet(passivationSkipNextState), "reinstate should set skip flag")

	pid.stopLocker.Unlock()
	locked = false

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("passivation attempt did not exit")
	}

	require.Equal(t, int32(0), postStopCounter.Load())
	require.False(t, passivated.Load(), "passivation should be skipped due to reinstate")
	require.True(t, pid.IsRunning())
}

func TestPIDTryPassivationSkipsWhenSystemStopping(t *testing.T) {
	pid := MockPassivationPID(t, "system-stopping", passivation.NewTimeBasedStrategy(time.Second))

	sys := &actorSystem{}
	sys.shuttingDown.Store(true)

	pid.fieldsLocker.Lock()
	pid.actorSystem = sys
	pid.fieldsLocker.Unlock()

	require.False(t, pid.tryPassivation("system stopping"))
}

func TestPIDTryPassivationSkipsWhenSkipFlagSet(t *testing.T) {
	pid := MockPassivationPID(t, "skip-flag", passivation.NewTimeBasedStrategy(time.Second))
	pid.setState(passivationSkipNextState, true)

	require.True(t, pid.isStateSet(passivationSkipNextState))
	require.False(t, pid.tryPassivation("skip"))
	require.False(t, pid.isStateSet(passivationSkipNextState))
}

func TestPIDTryPassivationSkipsWhenStoppingFlagRaised(t *testing.T) {
	pid := MockPassivationPID(t, "stopping-flag", passivation.NewTimeBasedStrategy(time.Second))
	pid.setState(stoppingState, true)

	require.False(t, pid.tryPassivation("already stopping"))
}

func TestPIDDoReceiveDuringShutdown(t *testing.T) {
	t.Run("With user message rejected during shutdown", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)

		require.NoError(t, sys.Start(ctx))
		defer func() { _ = sys.Stop(ctx) }()

		pause.For(time.Second)

		// Create an actor
		pid, err := sys.Spawn(ctx, "test-actor", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)

		pause.For(time.Second)

		// Subscribe to deadletter events
		consumer, err := sys.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)
		defer func() { _ = sys.Unsubscribe(consumer) }()

		// Create a mock actorSystem with shuttingDown set to true
		// Copy all necessary fields from the real system to avoid nil pointer issues
		mockSys := &actorSystem{}
		mockSys.shuttingDown.Store(true)
		mockSys.logger = log.DiscardLogger
		if actualSys, ok := sys.(interface{ getDeadletter() *PID }); ok {
			mockSys.deadletter = actualSys.getDeadletter()
		}
		if actualSys, ok := sys.(interface{ tree() *tree }); ok {
			mockSys.actors = actualSys.tree()
		}
		if actualSys, ok := sys.(interface{ NoSender() *PID }); ok {
			mockSys.noSender = actualSys.NoSender()
		}
		// Copy eventsStream to allow deadletter publishing
		if actualSys, ok := sys.(interface{ EventsStream() eventstream.Stream }); ok {
			mockSys.eventsStream = actualSys.EventsStream()
		}
		// Set the mock system on the PID
		pid.fieldsLocker.Lock()
		originalSys := pid.actorSystem
		pid.actorSystem = mockSys
		// Ensure PID has eventsStream set (it's needed for handleReceivedError)
		if pid.eventsStream == nil && mockSys.eventsStream != nil {
			pid.eventsStream = mockSys.eventsStream
		}
		pid.fieldsLocker.Unlock()

		// Create a user message (non-system message)
		userMessage := new(testpb.TestSend)
		receiveContext := &ReceiveContext{
			ctx:     ctx,
			message: userMessage,
			sender:  sys.NoSender(),
			self:    pid,
		}

		// Attempt to receive user message during shutdown
		pid.doReceive(receiveContext)

		// Restore original system immediately to avoid issues
		pid.fieldsLocker.Lock()
		pid.actorSystem = originalSys
		pid.fieldsLocker.Unlock()

		// Wait for deadletter to be sent
		pause.For(500 * time.Millisecond)

		// Verify message was sent to deadletter
		var deadletterItems []*Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			if deadletter, ok := payload.(*Deadletter); ok {
				deadletterItems = append(deadletterItems, deadletter)
				// Check that the deadletter contains our user message
				actual, ok := deadletter.Message().(*testpb.TestSend)
				require.True(t, ok)
				if ok {
					require.NotNil(t, actual)
					require.Equal(t, errors.ErrSystemShuttingDown.Error(), deadletter.Reason())
					break
				}
			}
		}

		require.NotEmpty(t, deadletterItems, "Expected deadletter to receive rejected message")
	})

	t.Run("With system message allowed during shutdown", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)

		require.NoError(t, sys.Start(ctx))

		pause.For(time.Second)

		// Create an actor
		pid, err := sys.Spawn(ctx, "test-actor", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)

		pause.For(time.Second)

		// Simulate system shutdown by marking the real actor system as shutting down.
		// Using the real system avoids data races: shuttingDown is an atomic.Bool,
		// and all system internals (tree, mailbox, etc.) remain fully valid for
		// background goroutines that process the enqueued messages.
		realSys := sys.(*actorSystem)
		realSys.shuttingDown.Store(true)
		defer func() {
			realSys.shuttingDown.Store(false)
			_ = sys.Stop(ctx)
		}()

		// Test various system messages that should be allowed through during shutdown.
		// PoisonPill is excluded because it triggers actor shutdown as a side effect,
		// which would affect subsequent iterations of this loop.
		systemMessages := []any{
			new(commands.HealthCheckRequest),
			new(commands.Panicking),
			new(PausePassivation),
			new(ResumePassivation),
			new(PostStart),
			new(Terminated),
			new(PanicSignal),
		}

		for _, sysMsg := range systemMessages {
			receiveContext := &ReceiveContext{
				ctx:     ctx,
				message: sysMsg,
				sender:  sys.NoSender(),
				self:    pid,
			}

			// System messages should be allowed through (no panic, no error)
			require.NotPanics(t, func() {
				pid.doReceive(receiveContext)
			}, "System message %T should be allowed during shutdown", sysMsg)
		}
	})

	t.Run("With no shutdown check when system not shutting down", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)

		require.NoError(t, sys.Start(ctx))
		defer func() { _ = sys.Stop(ctx) }()

		pause.For(time.Second)

		// Create an actor
		pid, err := sys.Spawn(ctx, "test-actor", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)

		pause.For(time.Second)

		// Create a user message
		userMessage := new(testpb.TestSend)
		receiveContext := &ReceiveContext{
			ctx:     ctx,
			message: userMessage,
			sender:  sys.NoSender(),
			self:    pid,
		}

		// User message should be accepted normally
		initialCount := pid.ProcessedCount()
		pid.doReceive(receiveContext)

		// Wait for message to be processed
		pause.For(500 * time.Millisecond)

		// Verify message was processed (count increased)
		require.Greater(t, pid.ProcessedCount(), initialCount, "User message should be processed when system is not shutting down")
	})
}

func TestReinstateNamed(t *testing.T) {
	t.Run("When PID is not started", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor ref
		pid, err := actorSystem.Spawn(ctx, "actor", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
		require.True(t, pid.IsRunning())

		// create another actor ref
		pid2, err := actorSystem.Spawn(ctx, "actor2", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid2)
		require.True(t, pid2.IsRunning())

		// let us stop both actors
		require.NoError(t, pid.Shutdown(ctx))
		require.NoError(t, pid2.Shutdown(ctx))

		pause.For(time.Second)

		// let us reinstate the actor pid2
		err = pid.ReinstateNamed(ctx, pid2.Name())
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrDead)

		pause.For(time.Second)
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("When actor not found", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(gossipPort)),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(MockActor)).
					WithPartitionCount(9).
					WithReplicaCount(1).
					WithPeersPort(clusterPort).
					WithMinimumPeersQuorum(1).
					WithDiscoveryPort(gossipPort).
					WithDiscovery(provider)),
		)
		require.NoError(t, err)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the cluster to start
		pause.For(time.Second)

		// create an actor
		ref, err := newActorSystem.Spawn(ctx, "ref0", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, ref)

		pause.For(time.Second)

		ref1, err := newActorSystem.Spawn(ctx, "ref1", NewMockActor(),
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(passivateAfter)))
		assert.NoError(t, err)
		assert.NotNil(t, ref1)

		pause.For(time.Second)

		err = ref.ReinstateNamed(ctx, "ref1")
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrActorNotFound)

		// stop the actor after some time
		pause.For(time.Second)
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("When happy path", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(gossipPort)),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(MockActor)).
					WithPartitionCount(9).
					WithReplicaCount(1).
					WithPeersPort(clusterPort).
					WithMinimumPeersQuorum(1).
					WithDiscoveryPort(gossipPort).
					WithDiscovery(provider)),
		)
		require.NoError(t, err)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the cluster to start
		pause.For(time.Second)

		// create an actor
		ref, err := newActorSystem.Spawn(ctx, "ref0", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, ref)

		pause.For(time.Second)

		ref1, err := newActorSystem.Spawn(ctx, "ref1", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, ref1)

		pause.For(time.Second)

		ref1.suspend("test")
		pause.For(time.Second)

		err = ref.ReinstateNamed(ctx, "ref1")
		require.NoError(t, err)

		pause.For(time.Second)
		// check if the actor is running
		require.True(t, ref1.IsRunning())

		// stop the actor after some time
		pause.For(time.Second)
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("When actor is already running", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(gossipPort)),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(MockActor)).
					WithPartitionCount(9).
					WithReplicaCount(1).
					WithPeersPort(clusterPort).
					WithMinimumPeersQuorum(1).
					WithDiscoveryPort(gossipPort).
					WithDiscovery(provider)),
		)
		require.NoError(t, err)

		provider.EXPECT().ID().Return("test")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the cluster to start
		pause.For(time.Second)

		// create an actor
		ref, err := newActorSystem.Spawn(ctx, "ref0", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, ref)

		pause.For(time.Second)

		ref1, err := newActorSystem.Spawn(ctx, "ref1", NewMockActor())
		assert.NoError(t, err)
		assert.NotNil(t, ref1)

		pause.For(time.Second)

		err = ref.ReinstateNamed(ctx, "ref1")
		require.NoError(t, err)

		pause.For(time.Second)
		// check if the actor is running
		require.True(t, ref1.IsRunning())

		// stop the actor after some time
		pause.For(time.Second)
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
}
func TestPipeToName(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		askTimeout := time.Minute
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

		// create actor1
		pid1, err := node1.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := node3.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(500 * time.Millisecond)
		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := func() (any, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return new(testpb.TaskComplete), nil
		}

		err = pid1.PipeToName(ctx, "Exchange2", task)
		require.NoError(t, err)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			// Wait for some time and during that period send some messages to the actor
			// send three messages while waiting for the future to completed
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			pause.For(time.Second)
			wg.Done()
		}()
		wg.Wait()

		pause.For(time.Second)

		require.EqualValues(t, 3, pid1.ProcessedCount()-1)
		require.EqualValues(t, 1, pid2.ProcessedCount()-1)

		pause.For(time.Second)

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("When actor not found", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// shutdown the actor after one second of liveness
		pause.For(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))

		// wait for the actor to be completely stopped
		pause.For(time.Second)

		task := func() (any, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return new(testpb.TaskComplete), nil
		}

		err = pid1.PipeToName(ctx, "Exchange2", task)
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrActorNotFound)

		pause.For(time.Second)
		require.NoError(t, pid1.Shutdown(ctx))
		pause.For(time.Second)
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With undefined task", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(time.Second)

		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		err = pid1.PipeToName(ctx, "Exchange2", nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrUndefinedTask)

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With failed task result in deadletter", func(t *testing.T) {
		askTimeout := time.Minute
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create a deadletter subscriber
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create actor1
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(time.Second)

		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := func() (any, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return nil, assert.AnError
		}

		cancelCtx, cancel := context.WithCancel(ctx)
		err = pid1.PipeToName(cancelCtx, "Exchange2", task)
		require.NoError(t, err)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			// Wait for some time and during that period send some messages to the actor
			// send three messages while waiting for the future to completed
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			pause.For(time.Second)
			wg.Done()
		}()

		cancel()
		wg.Wait()

		pause.For(time.Second)

		require.EqualValues(t, 3, pid1.ProcessedCount()-1)

		// no message piped to the actor
		require.Zero(t, pid2.ProcessedCount()-1)

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

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With explicit timeout", func(t *testing.T) {
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

		// create a deadletter subscriber
		consumer, err := node1.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create actor1
		pid1, err := node1.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := node2.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(500 * time.Millisecond)
		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := func() (any, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return new(testpb.TaskComplete), nil
		}

		err = pid1.PipeToName(ctx, "Exchange2", task, WithTimeout(500*time.Millisecond))
		require.NoError(t, err)
		pause.For(time.Second)

		// no message piped to the actor
		require.Zero(t, pid2.ProcessedCount()-1)

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

		pause.For(time.Second)
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("With successful circuit breaker", func(t *testing.T) {
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

		// create a deadletter subscriber
		consumer, err := node1.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create actor1
		pid1, err := node1.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := node2.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(500 * time.Millisecond)
		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := func() (any, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return new(testpb.TaskComplete), nil
		}

		cb := breaker.NewCircuitBreaker(
			breaker.WithFailureRate(0.5),
			breaker.WithMinRequests(2),
			breaker.WithOpenTimeout(50*time.Millisecond),
			breaker.WithWindow(100*time.Millisecond, 2),
			breaker.WithHalfOpenMaxCalls(1),
		)

		err = pid1.PipeToName(ctx, "Exchange2", task, WithCircuitBreaker(cb))
		require.NoError(t, err)
		pause.For(time.Second)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			// Wait for some time and during that period send some messages to the actor
			// send three messages while waiting for the future to completed
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), time.Minute)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), time.Minute)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), time.Minute)
			pause.For(time.Second)
			wg.Done()
		}()
		wg.Wait()

		pause.For(time.Second)

		require.EqualValues(t, 3, pid1.ProcessedCount()-1)
		require.EqualValues(t, 1, pid2.ProcessedCount()-1)

		pause.For(time.Second)
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("With circuit breaker on failed task", func(t *testing.T) {
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))
		pause.For(time.Second)

		// create a deadletter subscriber
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create actor1
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(500 * time.Millisecond)
		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := func() (any, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return nil, assert.AnError
		}

		cb := breaker.NewCircuitBreaker(
			breaker.WithFailureRate(0.5),
			breaker.WithMinRequests(1),
		)

		err = pid1.PipeToName(ctx, "Exchange2", task, WithCircuitBreaker(cb))
		require.NoError(t, err)
		pause.For(time.Second)

		// no message piped to the actor
		require.Zero(t, pid2.ProcessedCount()-1)

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

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With SendAsync returning error", func(t *testing.T) {
		askTimeout := time.Minute
		ctx := context.TODO()
		host := "127.0.0.1"
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create a deadletter subscriber
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create actor1
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(500 * time.Millisecond)
		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := func() (any, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return new(testpb.TaskComplete), nil
		}

		err = pid1.PipeToName(ctx, "Exchange2", task)
		require.NoError(t, err)

		require.NoError(t, pid2.Shutdown(ctx))

		var wg sync.WaitGroup
		wg.Go(func() {
			// Wait for some time and during that period send some messages to the actor
			// send three messages while waiting for the future to completed
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			_, _ = Ask(ctx, pid1, new(testpb.TestReply), askTimeout)
			pause.For(time.Second)
		})
		wg.Wait()

		pause.For(time.Second)

		require.EqualValues(t, 3, pid1.ProcessedCount()-1)

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
		msg := items[0].Message()
		require.NotNil(t, msg)

		actual, ok := msg.(*testpb.TaskComplete)
		require.True(t, ok)
		require.True(t, proto.Equal(new(testpb.TaskComplete), actual))

		pause.For(time.Second)
		require.NoError(t, pid1.Shutdown(ctx))
		pause.For(time.Second)
		require.NoError(t, actorSystem.Stop(ctx))
	})
}

func TestToWireActorDependencyError(t *testing.T) {
	pid := &PID{
		actor:        NewMockActor(),
		address:      address.New("actor-to-wire", "testSys", "127.0.0.1", 0),
		fieldsLocker: sync.RWMutex{},
		dependencies: xsync.NewMap[string, extension.Dependency](),
	}

	expectedErr := assert.AnError
	pid.dependencies.Set("failing", &MockFailingDependency{err: expectedErr})

	wire, err := pid.toSerialize()
	require.ErrorIs(t, err, expectedErr)
	require.Nil(t, wire)
}

func TestToWireActorSupervisorSpec(t *testing.T) {
	noSupervisorPID := &PID{
		actor:        NewMockActor(),
		address:      address.New("actor-to-wire-nosupervisor", "testSys", "127.0.0.1", 0),
		fieldsLocker: sync.RWMutex{},
	}

	wire, err := noSupervisorPID.toSerialize()
	require.NoError(t, err)
	require.Nil(t, wire.GetSupervisor())

	withSupervisorPID := &PID{
		actor:        NewMockActor(),
		address:      address.New("actor-to-wire-supervisor", "testSys", "127.0.0.1", 0),
		fieldsLocker: sync.RWMutex{},
		supervisor:   supervisor.NewSupervisor(supervisor.WithStrategy(supervisor.OneForAllStrategy)),
	}

	wire, err = withSupervisorPID.toSerialize()
	require.NoError(t, err)
	require.NotNil(t, wire.GetSupervisor())
	require.Equal(t, internalpb.SupervisorStrategy_SUPERVISOR_STRATEGY_ONE_FOR_ALL, wire.GetSupervisor().GetStrategy())
}

func TestToWireActorIncludesSingletonSpecWhenSingleton(t *testing.T) {
	spec := &singletonSpec{
		SpawnTimeout: time.Second,
		WaitInterval: 500 * time.Millisecond,
		MaxRetries:   3,
	}

	pid := &PID{
		actor:   NewMockActor(),
		address: address.New("singleton", "test-system", "127.0.0.1", 0),
	}
	pid.setState(singletonState, true)
	pid.singletonSpec = spec

	wire, err := pid.toSerialize()
	require.NoError(t, err)

	require.NotNil(t, wire.GetSingleton())
	assert.Equal(t, spec.MaxRetries, wire.GetSingleton().GetMaxRetries())
	assert.Equal(t, spec.SpawnTimeout, wire.GetSingleton().GetSpawnTimeout().AsDuration())
	assert.Equal(t, spec.WaitInterval, wire.GetSingleton().GetWaitInterval().AsDuration())
}

// TestAssertLocal verifies that every public method guarded by assertLocal
// returns errors.ErrNotLocal when invoked on a remote PID.
func TestAssertLocal(t *testing.T) {
	ctx := context.Background()
	// Build a minimal remote PID: address is set and the remoteState flag is set.
	addr := address.New("remote-actor", "remoteSystem", "10.0.0.1", 8080)
	remotePID := newRemotePID(addr, nil)
	require.True(t, remotePID.IsRemote(), "sanity: newRemotePID must be remote")

	t.Run("Child", func(t *testing.T) {
		_, err := remotePID.Child("child")
		require.ErrorIs(t, err, errors.ErrNotLocal)
	})

	t.Run("SpawnChild", func(t *testing.T) {
		_, err := remotePID.SpawnChild(ctx, "child", NewMockActor())
		require.ErrorIs(t, err, errors.ErrNotLocal)
	})

	t.Run("ReinstateNamed", func(t *testing.T) {
		err := remotePID.ReinstateNamed(ctx, "some-actor")
		require.ErrorIs(t, err, errors.ErrNotLocal)
	})

	t.Run("PipeTo", func(t *testing.T) {
		other := newRemotePID(addr, nil)
		err := remotePID.PipeTo(ctx, other, func() (any, error) { return nil, nil })
		require.ErrorIs(t, err, errors.ErrNotLocal)
	})

	t.Run("PipeToName", func(t *testing.T) {
		err := remotePID.PipeToName(ctx, "some-actor", func() (any, error) { return nil, nil })
		require.ErrorIs(t, err, errors.ErrNotLocal)
	})

	t.Run("SendAsync", func(t *testing.T) {
		err := remotePID.SendAsync(ctx, "some-actor", new(testpb.TestSend))
		require.ErrorIs(t, err, errors.ErrNotLocal)
	})

	t.Run("SendSync", func(t *testing.T) {
		_, err := remotePID.SendSync(ctx, "some-actor", new(testpb.TestReply), time.Second)
		require.ErrorIs(t, err, errors.ErrNotLocal)
	})

	t.Run("DiscoverActor", func(t *testing.T) {
		_, err := remotePID.DiscoverActor(ctx, "some-actor", time.Second)
		require.ErrorIs(t, err, errors.ErrNotLocal)
	})
}

// TestRemoteStopRestartShutdownWithoutRemoting verifies that Stop, Restart, and Shutdown
// return ErrRemotingDisabled when invoked on or targeting remote PIDs without remoting configured.
func TestRemoteStopRestartShutdownWithoutRemoting(t *testing.T) {
	ctx := context.Background()
	addr := address.New("remote-actor", "remoteSystem", "10.0.0.1", 8080)
	remotePID := newRemotePID(addr, nil)
	require.True(t, remotePID.IsRemote(), "sanity: newRemotePID must be remote")

	t.Run("Stop remote child without remoting", func(t *testing.T) {
		other := newRemotePID(addr, nil)
		err := remotePID.Stop(ctx, other)
		require.ErrorIs(t, err, errors.ErrRemotingDisabled)
	})

	t.Run("Restart remote PID without remoting", func(t *testing.T) {
		err := remotePID.Restart(ctx)
		require.ErrorIs(t, err, errors.ErrRemotingDisabled)
	})

	t.Run("Shutdown remote PID without remoting", func(t *testing.T) {
		err := remotePID.Shutdown(ctx)
		require.ErrorIs(t, err, errors.ErrRemotingDisabled)
	})
}

// TestRemoteStopRestartShutdownWithRemoting verifies that Stop, Restart, and Shutdown
// delegate to the remoting layer when invoked on or targeting remote PIDs with remoting configured.
func TestRemoteStopRestartShutdownWithRemoting(t *testing.T) {
	ctx := context.Background()
	host, port, name := "10.0.0.1", 8080, "remote-actor"
	addr := address.New(name, "remoteSystem", host, port)

	t.Run("Stop remote child with remoting", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		remotingMock.EXPECT().RemoteStop(ctx, host, port, name).Return(nil).Once()

		remoteChild := newRemotePID(addr, remotingMock)
		localParent := &PID{
			remoting: remotingMock,
			address:  address.New("parent", "sys", "127.0.0.1", 9000),
		}
		localParent.setState(runningState, true)

		err := localParent.Stop(ctx, remoteChild)
		require.NoError(t, err)
		remotingMock.AssertExpectations(t)
	})

	t.Run("Restart remote PID with remoting", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		respAddr := addr.String()
		remotingMock.EXPECT().RemoteReSpawn(ctx, host, port, name).Return(&respAddr, nil).Once()

		remotePID := newRemotePID(addr, remotingMock)
		err := remotePID.Restart(ctx)
		require.NoError(t, err)
		remotingMock.AssertExpectations(t)
	})

	t.Run("Shutdown remote PID with remoting", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		remotingMock.EXPECT().RemoteStop(ctx, host, port, name).Return(nil).Once()

		remotePID := newRemotePID(addr, remotingMock)
		err := remotePID.Shutdown(ctx)
		require.NoError(t, err)
		remotingMock.AssertExpectations(t)
	})
}

// TestBatchTellBatchAskRemote verifies that BatchTell and BatchAsk use RemoteBatchTell
// and RemoteBatchAsk when the target is remote, for efficiency.
func TestBatchTellBatchAskRemote(t *testing.T) {
	ctx := context.Background()
	host, port, name := "10.0.0.1", 8080, "remote-actor"
	addr := address.New(name, "remoteSystem", host, port)

	type actorSystemWrapper struct {
		*actorSystem
	}

	t.Run("BatchTell remote uses RemoteBatchTell", func(t *testing.T) {
		sys, err := NewActorSystem("batch-sys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)

		remotingMock := mocksremote.NewClient(t)
		messages := []any{new(testpb.TestSend), new(testpb.TestSend)}
		remotingMock.EXPECT().RemoteBatchTell(mock.Anything, mock.Anything, mock.Anything, messages).Return(nil).Once()

		sender := &PID{
			actorSystem: &actorSystemWrapper{actorSystem: sys.(*actorSystem)},
			remoting:    remotingMock,
			address:     address.New("sender", "sys", "127.0.0.1", 9000),
		}
		sender.setState(runningState, true)
		remoteTarget := newRemotePID(addr, remotingMock)

		err = sender.BatchTell(ctx, remoteTarget, messages...)
		require.NoError(t, err)
		remotingMock.AssertExpectations(t)
	})

	t.Run("BatchTell returns ErrDead when caller is not running", func(t *testing.T) {
		sys, err := NewActorSystem("batch-sys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))

		caller, err := sys.Spawn(ctx, "caller", NewMockActor())
		require.NoError(t, err)
		target, err := sys.Spawn(ctx, "target", NewMockActor())
		require.NoError(t, err)

		require.NoError(t, caller.Shutdown(ctx))
		pause.For(time.Second)

		err = caller.BatchTell(ctx, target, new(testpb.TestSend))
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrDead)

		require.NoError(t, target.Shutdown(ctx))
		require.NoError(t, sys.Stop(ctx))
	})

	t.Run("BatchAsk returns ErrDead when caller is not running", func(t *testing.T) {
		sys, err := NewActorSystem("batch-sys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))

		caller, err := sys.Spawn(ctx, "caller", NewMockActor())
		require.NoError(t, err)
		target, err := sys.Spawn(ctx, "target", NewMockActor())
		require.NoError(t, err)

		require.NoError(t, caller.Shutdown(ctx))
		pause.For(time.Second)

		ch, errAsk := caller.BatchAsk(ctx, target, []any{new(testpb.TestReply)}, time.Second)
		require.Error(t, errAsk)
		require.ErrorIs(t, errAsk, errors.ErrDead)
		require.Nil(t, ch)

		require.NoError(t, target.Shutdown(ctx))
		require.NoError(t, sys.Stop(ctx))
	})

	t.Run("BatchAsk remote uses RemoteBatchAsk", func(t *testing.T) {
		sys, err := NewActorSystem("batch-sys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)

		remotingMock := mocksremote.NewClient(t)
		messages := []any{new(testpb.TestReply), new(testpb.TestReply)}
		responses := []any{new(testpb.TestReply), new(testpb.TestReply)}
		remotingMock.EXPECT().RemoteBatchAsk(mock.Anything, mock.Anything, mock.Anything, messages, time.Minute).
			Return(responses, nil).Once()

		sender := &PID{
			actorSystem: &actorSystemWrapper{actorSystem: sys.(*actorSystem)},
			remoting:    remotingMock,
			address:     address.New("sender", "sys", "127.0.0.1", 9000),
		}
		sender.setState(runningState, true)
		remoteTarget := newRemotePID(addr, remotingMock)

		ch, errAsk := sender.BatchAsk(ctx, remoteTarget, messages, time.Minute)
		require.NoError(t, errAsk)
		require.NotNil(t, ch)
		var count int
		for range ch {
			count++
		}
		require.Equal(t, 2, count)
		remotingMock.AssertExpectations(t)
	})
}

// TestIsSystemMessage verifies that isSystemMessage correctly classifies each
// known system-message type as true and non-system messages as false.
func TestIsSystemMessage(t *testing.T) {
	systemMessages := []struct {
		name string
		msg  any
	}{
		{"AsyncResponse", new(commands.AsyncResponse)},
		{"AsyncRequest", new(commands.AsyncRequest)},
		{"PoisonPill", new(PoisonPill)},
		{"HealthCheckRequest", new(commands.HealthCheckRequest)},
		{"Panicking", new(commands.Panicking)},
		{"SendDeadletter", new(commands.SendDeadletter)},
		{"PausePassivation", new(PausePassivation)},
		{"ResumePassivation", new(ResumePassivation)},
		{"PostStart", new(PostStart)},
		{"Terminated", new(Terminated)},
		{"PanicSignal", new(PanicSignal)},
	}
	for _, tc := range systemMessages {
		t.Run(tc.name+" is a system message", func(t *testing.T) {
			assert.True(t, isSystemMessage(tc.msg))
		})
	}
	t.Run("user message is not a system message", func(t *testing.T) {
		assert.False(t, isSystemMessage(new(testpb.TestSend)))
	})
	t.Run("nil is not a system message", func(t *testing.T) {
		assert.False(t, isSystemMessage(nil))
	})
}

func newBareSupervisor(strategy supervisor.Strategy) *supervisor.Supervisor {
	supv := supervisor.NewSupervisor()
	supv.Reset()
	supervisor.WithStrategy(strategy)(supv)
	return supv
}
