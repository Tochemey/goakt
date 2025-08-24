/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
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

package actor

import (
	"bytes"
	"context"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/breaker"
	"github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/collection"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	testkit "github.com/tochemey/goakt/v3/mocks/discovery"
	"github.com/tochemey/goakt/v3/passivation"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
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

		var items []*goaktpb.Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*goaktpb.Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		// unsubscribe the consumer
		err = actorSystem.Unsubscribe(consumer)
		require.NoError(t, err)
		require.NotEmpty(t, items)
		deadletter := items[0]
		require.True(t, deadletter.Message.MessageIs(&internalpb.ReadinessProbe{}))

		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
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
		wg.Add(1)
		go func() {
			pause.For(receivingDelay)
			wg.Done()
		}()
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
		err = Tell(ctx, pid, new(goaktpb.PausePassivation))
		require.NoError(t, err)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			pause.For(time.Second)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()
		require.True(t, pid.IsRunning())

		// Resume passivation
		err = Tell(ctx, pid, new(goaktpb.ResumePassivation))
		require.NoError(t, err)

		wg = sync.WaitGroup{}
		wg.Add(1)
		go func() {
			pause.For(time.Second)
			wg.Done()
		}()
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
		wg.Add(1)
		go func() {
			pause.For(receivingDelay)
			wg.Done()
		}()
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

		pause.For(time.Second)

		// create the actor path
		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor(),
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(passivateAfter)))
		require.NoError(t, err)
		assert.NotNil(t, pid)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			pause.For(receivingDelay)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()
		// let us send a message to the actor
		err = Tell(ctx, pid, new(testpb.TestSend))
		assert.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrDead)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
}
func TestReply(t *testing.T) {
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

		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		assert.NotNil(t, pid)

		actual, err := Ask(ctx, pid, new(testpb.TestReply), replyTimeout)
		assert.NoError(t, err)
		assert.NotNil(t, actual)
		expected := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, actual))
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With context canceled", func(t *testing.T) {
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
		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		assert.NotNil(t, pid)

		cancelCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()

		actual, err := Ask(cancelCtx, pid, new(testpb.TestSend), replyTimeout)
		assert.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRequestTimeout)
		assert.Nil(t, actual)
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With timeout", func(t *testing.T) {
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
		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		assert.NotNil(t, pid)

		actual, err := Ask(ctx, pid, new(testpb.TestSend), replyTimeout)
		assert.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRequestTimeout)
		assert.Nil(t, actual)
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With actor not ready", func(t *testing.T) {
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
		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		assert.NotNil(t, pid)

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)

		actual, err := Ask(ctx, pid, new(testpb.TestReply), replyTimeout)
		assert.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrDead)
		assert.Nil(t, actual)
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
		for i := 0; i < count; i++ {
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
		for i := 0; i < count; i++ {
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
	t.Run("With restart an actor and its children", func(t *testing.T) {
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

		require.EqualValues(t, 1, pid.RestartCount())
		require.EqualValues(t, 1, pid.ChildrenCount())

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
			fieldsLocker: &sync.RWMutex{},
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
		stopStrategy := NewSupervisor(WithDirective(&errors.PanicError{}, StopDirective))

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
		stopStrategy := NewSupervisor(
			WithStrategy(OneForAllStrategy),
			WithDirective(&errors.PanicError{}, StopDirective),
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

		// create the child actor
		fakeStrategy := NewSupervisor(WithDirective(&errors.PanicError{}, 4)) // undefined directive
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMockSupervised(), WithSupervisor(fakeStrategy))
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
		require.True(t, child.IsSuspended())
		require.Len(t, parent.Children(), 0)

		// trying sending a message to the actor will return an error
		require.Error(t, Tell(ctx, child, new(testpb.TestSend)))

		//stop the actor
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
		child.supervisor = &Supervisor{
			Mutex:      sync.Mutex{},
			strategy:   OneForOneStrategy,
			maxRetries: 0,
			timeout:    0,
			directives: collection.NewMap[string, Directive](),
		}

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
		stopStrategy := NewSupervisor(WithDirective(&errors.PanicError{}, DefaultSupervisorDirective))
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
	t.Run("With restart as supervisor strategy", func(t *testing.T) {
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
		restartStrategy := NewSupervisor(WithDirective(&errors.PanicError{}, RestartDirective))
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
		restartStrategy := NewSupervisor(
			WithStrategy(OneForAllStrategy),
			WithDirective(&errors.PanicError{}, RestartDirective))
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
		resumeStrategy := NewSupervisor(WithDirective(&errors.PanicError{}, ResumeDirective))
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
		assert.True(t, proto.Equal(expected, reply))

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

		restartStrategy := NewSupervisor(
			WithStrategy(OneForOneStrategy),
			WithDirective(&errors.PanicError{}, RestartDirective),
			WithRetry(2, time.Minute),
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

		child.supervisor = &Supervisor{
			Mutex:      sync.Mutex{},
			strategy:   OneForOneStrategy,
			maxRetries: 0,
			timeout:    0,
			directives: collection.NewMap[string, Directive](),
		}

		require.Len(t, parent.Children(), 1)
		// send a message to the actor which result in panic
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		pause.For(time.Second)

		// assert the actor state
		require.False(t, child.IsRunning())
		require.True(t, child.IsSuspended())
		require.Len(t, parent.Children(), 0)

		var suspendedEvent *goaktpb.ActorSuspended
		for message := range subscriber.Iterator() {
			payload := message.Payload()
			// only listening to suspended actors
			event, ok := payload.(*goaktpb.ActorSuspended)
			if ok {
				suspendedEvent = event
				break
			}
		}

		require.NotNil(t, suspendedEvent)
		require.False(t, proto.Equal(suspendedEvent, new(goaktpb.ActorSuspended)))
		require.Equal(t, child.Name(), suspendedEvent.GetAddress().GetName())

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
		stopStrategy := NewSupervisor(WithAnyErrorDirective(StopDirective))

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
		escalationStrategy := NewSupervisor(WithDirective(&errors.PanicError{}, EscalateDirective))
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
		escalationStrategy := NewSupervisor(WithDirective(&errors.PanicError{}, EscalateDirective))
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
		restartStrategy := NewSupervisor(WithAnyErrorDirective(RestartDirective))
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
		restartStrategy := NewSupervisor(WithAnyErrorDirective(RestartDirective))
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
		assert.True(t, proto.Equal(expected, reply))

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
		addr1, err := actorRef2.RemoteLookup(ctx, host, remotingPort, actorName1)
		require.NoError(t, err)

		// send the message to exchanger actor one using remote messaging
		reply, err := actorRef2.RemoteAsk(ctx, address.From(addr1), new(testpb.TestReply), replyTimeout)
		// perform some assertions
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = reply.UnmarshalTo(actual)
		require.NoError(t, err)

		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, actual))

		// send a message to stop the first exchange actor
		err = actorRef2.RemoteTell(ctx, address.From(addr1), new(testpb.TestRemoteSend))
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
		addr1, err := actorRef2.RemoteLookup(ctx, host, remotingPort, actorName1)
		require.NoError(t, err)

		actorRef2.remoting = nil
		// send the message to exchanger actor one using remote messaging
		reply, err := actorRef2.RemoteAsk(ctx, address.From(addr1), new(testpb.TestReply), replyTimeout)
		// perform some assertions
		require.Error(t, err)
		require.Nil(t, reply)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)

		// send a message to stop the first exchange actor
		err = actorRef2.RemoteTell(ctx, address.From(addr1), new(testpb.TestRemoteSend))
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
	t.Run("With restarting child actor when not shutdown", func(t *testing.T) {
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

		var events []*goaktpb.ActorChildCreated
		for message := range subsriber.Iterator() {
			// get the event payload
			payload := message.Payload()
			switch msg := payload.(type) {
			case *goaktpb.ActorChildCreated:
				events = append(events, msg)
			}
		}

		require.NotEmpty(t, events)
		require.Len(t, events, 1)

		event := events[0]
		assert.True(t, proto.Equal(parent.Address().Address, event.GetParent()))

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
			WithPassivateAfter(2*time.Second))

		require.NoError(t, err)
		require.NotNil(t, child)

		pause.For(time.Second)
		assert.Len(t, parent.Children(), 1)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			pause.For(2 * time.Second)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()

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
	err = Tell(ctx, pid, new(goaktpb.PoisonPill))
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

		// let us lookup actor two
		actorName2 := "Exchange2"
		addr, err := actorRef1.RemoteLookup(ctx, host, remotingPort, actorName2)
		require.NoError(t, err)
		require.Nil(t, addr)

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
		responses, err := pid.BatchAsk(ctx, pid, []proto.Message{new(testpb.TestReply), new(testpb.TestReply)}, replyTimeout)
		require.NoError(t, err)
		for reply := range responses {
			require.NoError(t, err)
			require.NotNil(t, reply)
			expected := new(testpb.Reply)
			assert.True(t, proto.Equal(expected, reply))
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
		responses, err := pid.BatchAsk(ctx, pid, []proto.Message{new(testpb.TestReply), new(testpb.TestReply)}, replyTimeout)
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
		responses, err := pid.BatchAsk(ctx, pid, []proto.Message{new(testpb.TestTimeout), new(testpb.TestReply)}, replyTimeout)
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

		actorName2 := "Exchange2"
		err = actorRef1.RemoteReSpawn(ctx, host, remotingPort, actorName2)
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
			WithRemote(remote.NewConfig(host, remotingPort)))
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

		// for the sake of the test we set the remoting field of actorRef1
		actorRef1.remoting = nil

		actorName2 := "Exchange2"
		err = actorRef1.RemoteReSpawn(ctx, host, remotingPort, actorName2)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)

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

		actorName2 := "Exchange2"
		err = actorRef1.RemoteReSpawn(ctx, host, remotingPort, actorName2)
		require.Error(t, err)

		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
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
func TestRemoteSpawn(t *testing.T) {
	t.Run("When remoting is enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
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

		// create an actor
		pid, err := sys.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		assert.NotNil(t, pid)

		// create an actor implementation and register it
		actor := &exchanger{}
		actorName := uuid.NewString()

		// fetching the address of the that actor should return nil address
		addr, err := pid.RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.Nil(t, addr)

		// register the actor
		err = sys.Register(ctx, actor)
		require.NoError(t, err)

		// spawn the remote actor
		err = pid.RemoteSpawn(ctx, host, remotingPort, actorName, "actor.exchanger")
		require.NoError(t, err)

		// re-fetching the address of the actor should return not nil address after start
		addr, err = pid.RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.NotNil(t, addr)

		// send the message to exchanger actor one using remote messaging
		reply, err := pid.RemoteAsk(ctx, address.From(addr), new(testpb.TestReply), replyTimeout)

		require.NoError(t, err)
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = reply.UnmarshalTo(actual)
		require.NoError(t, err)

		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, actual))

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("When actor not registered", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
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

		// create an actor
		pid, err := sys.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		assert.NotNil(t, pid)

		actorName := uuid.NewString()
		// fetching the address of the that actor should return nil address
		addr, err := pid.RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.Nil(t, addr)

		// for the sake of the test
		require.NoError(t, sys.Deregister(ctx, &exchanger{}))

		// spawn the remote actor
		err = pid.RemoteSpawn(ctx, host, remotingPort, actorName, "exchanger")
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrTypeNotRegistered)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("When remote server unreachable", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an actor
		pid, err := sys.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		assert.NotNil(t, pid)

		// create an actor implementation and register it
		actorName := uuid.NewString()

		// spawn the remote actor
		err = pid.RemoteSpawn(ctx, host, remotingPort, actorName, "exchanger")
		require.Error(t, err)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("When remoting is not enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
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

		// create an actor
		pid, err := sys.Spawn(ctx, "Exchange1", &exchanger{})
		require.NoError(t, err)
		assert.NotNil(t, pid)

		// create an actor implementation and register it
		actor := &exchanger{}
		actorName := uuid.NewString()

		// fetching the address of the that actor should return nil address
		addr, err := pid.RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.Nil(t, addr)

		// register the actor
		err = sys.Register(ctx, actor)
		require.NoError(t, err)

		// disable remoting on pid
		pid.remoting = nil

		// spawn the remote actor
		err = pid.RemoteSpawn(ctx, host, remotingPort, actorName, "actor.exchanger")
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
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

		task := func() (proto.Message, error) {
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
	t.Run("With is a dead actor", func(t *testing.T) {
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

		task := func() (proto.Message, error) {
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

		task := func() (proto.Message, error) {
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

		task := func() (proto.Message, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return new(testpb.TaskComplete), nil
		}

		err = pid1.PipeTo(ctx, pid2, task, WithPipeToTimeout(500*time.Millisecond))
		require.NoError(t, err)
		pause.For(time.Second)

		// no message piped to the actor
		require.Zero(t, pid2.ProcessedCount()-1)

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

		task := func() (proto.Message, error) {
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

		err = pid1.PipeTo(ctx, pid2, task, WithPipeToCircuitBreaker(cb))
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

		task := func() (proto.Message, error) {
			// simulate a long-running task
			pause.For(time.Second)
			return nil, assert.AnError
		}

		cb := breaker.NewCircuitBreaker(
			breaker.WithFailureRate(0.5),
			breaker.WithMinRequests(1),
		)

		err = pid1.PipeTo(ctx, pid2, task, WithPipeToCircuitBreaker(cb))
		require.NoError(t, err)
		pause.For(time.Second)

		// no message piped to the actor
		require.Zero(t, pid2.ProcessedCount()-1)

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

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
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
		assert.True(t, proto.Equal(expected, response))

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
		assert.True(t, proto.Equal(expected, response))

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
}
func TestLogger(t *testing.T) {
	buffer := new(bytes.Buffer)

	pid := &PID{
		logger:       log.New(log.InfoLevel, buffer),
		fieldsLocker: &sync.RWMutex{},
	}

	pid.Logger().Info("test debug")
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
	watchers := pnode.watchers

	found := false
	for _, watcher := range watchers.Values() {
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

	workerPool := actorSystem.getWorkerPool()

	childPath := address.New("child", "sys", "127.0.0.1", ports[0])
	cid, err := newPID(
		ctx, childPath,
		NewMockSupervisor(),
		withInitMaxRetries(1),
		withActorSystem(actorSystem),
		withWorkerPool(workerPool),
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
	workerPool := actorSystem.getWorkerPool()

	childPath := address.New("child", "sys", "127.0.0.1", ports[0])
	cid, err := newPID(
		ctx, childPath,
		NewMockSupervisor(),
		withInitMaxRetries(1),
		withActorSystem(actorSystem),
		withWorkerPool(workerPool),
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
