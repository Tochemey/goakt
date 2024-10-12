/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

package actors

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/address"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/eventstream"
	"github.com/tochemey/goakt/v2/internal/lib"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/test/data/testpb"
	testspb "github.com/tochemey/goakt/v2/test/data/testpb"
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
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the actor ref
		pid, err := newPID(
			ctx,
			actorPath,
			newTestActor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, pid)
		// let us send 10 public to the actor
		count := 10
		for i := 0; i < count; i++ {
			receiveContext := &ReceiveContext{
				ctx:     ctx,
				message: new(testpb.TestSend),
				sender:  NoSender,
				self:    pid,
			}

			pid.doReceive(receiveContext)
		}

		lib.Pause(500 * time.Millisecond)
		assert.Zero(t, pid.ChildrenCount())
		assert.NotZero(t, pid.LatestProcessedDuration())
		assert.EqualValues(t, 10, pid.ProcessedCount()-1) // 1 because of the PostStart message
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
}
func TestPassivation(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withPassivationAfter(passivateAfter),
			withAskTimeout(replyTimeout),
		}

		ports := dynaport.Get(1)
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])

		pid, err := newPID(ctx, actorPath, newTestActor(), opts...)
		require.NoError(t, err)
		assert.NotNil(t, pid)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			lib.Pause(receivingDelay)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()
		// let us send a message to the actor
		err = Tell(ctx, pid, new(testpb.TestSend))
		assert.Error(t, err)
		assert.EqualError(t, err, ErrDead.Error())
	})
	t.Run("With actor shutdown failure", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withPassivationAfter(passivateAfter),
			withAskTimeout(replyTimeout),
		}

		ports := dynaport.Get(1)
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])

		pid, err := newPID(ctx, actorPath, &testPostStop{}, opts...)
		require.NoError(t, err)
		assert.NotNil(t, pid)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			lib.Pause(receivingDelay)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()
		// let us send a message to the actor
		err = Tell(ctx, pid, new(testpb.TestSend))
		assert.Error(t, err)
		assert.EqualError(t, err, ErrDead.Error())
	})
}
func TestReply(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		ports := dynaport.Get(1)
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])

		pid, err := newPID(ctx, actorPath, newTestActor(), opts...)

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
	})
	t.Run("With timeout", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		ports := dynaport.Get(1)
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])
		pid, err := newPID(ctx, actorPath, newTestActor(), opts...)

		require.NoError(t, err)
		assert.NotNil(t, pid)

		actual, err := Ask(ctx, pid, new(testpb.TestSend), replyTimeout)
		assert.Error(t, err)
		assert.EqualError(t, err, ErrRequestTimeout.Error())
		assert.Nil(t, actual)
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With actor not ready", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		ports := dynaport.Get(1)
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])
		pid, err := newPID(ctx, actorPath, newTestActor(), opts...)

		require.NoError(t, err)
		assert.NotNil(t, pid)

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)

		actual, err := Ask(ctx, pid, new(testpb.TestReply), replyTimeout)
		assert.Error(t, err)
		assert.EqualError(t, err, ErrDead.Error())
		assert.Nil(t, actual)
	})
}
func TestRestart(t *testing.T) {
	t.Run("With restart a stopped actor", func(t *testing.T) {
		ctx := context.TODO()

		// create a Ping actor
		actor := newTestActor()
		assert.NotNil(t, actor)

		// create the actor path
		ports := dynaport.Get(1)
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])
		// create the actor ref
		pid, err := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withPassivationAfter(10*time.Second),
			withCustomLogger(log.DiscardLogger),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, pid)

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		// let us send a message to the actor
		err = Tell(ctx, pid, new(testpb.TestSend))
		assert.Error(t, err)
		assert.EqualError(t, err, ErrDead.Error())

		// restart the actor
		err = pid.Restart(ctx)
		assert.NoError(t, err)
		assert.True(t, pid.IsRunning())
		// let us send 10 public to the actor
		count := 10
		for i := 0; i < count; i++ {
			err = Tell(ctx, pid, new(testpb.TestSend))
			assert.NoError(t, err)
		}

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With restart an actor", func(t *testing.T) {
		ctx := context.TODO()

		// create a Ping actor
		actor := newTestActor()
		assert.NotNil(t, actor)
		// create the actor path
		ports := dynaport.Get(1)
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the actor ref
		pid, err := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, pid)
		// let us send 10 public to the actor
		count := 10
		for i := 0; i < count; i++ {
			err := Tell(ctx, pid, new(testpb.TestSend))
			assert.NoError(t, err)
		}

		// restart the actor
		err = pid.Restart(ctx)
		assert.NoError(t, err)
		assert.True(t, pid.IsRunning())

		assert.EqualValues(t, 1, pid.RestartCount())
		// let us send 10 public to the actor
		for i := 0; i < count; i++ {
			err = Tell(ctx, pid, new(testpb.TestSend))
			assert.NoError(t, err)
		}

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With restart an actor with PreStart failure", func(t *testing.T) {
		ctx := context.TODO()

		// create a Ping actor
		actor := newTestRestart()
		assert.NotNil(t, actor)
		// create the actor path
		ports := dynaport.Get(1)
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the actor ref
		pid, err := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withPassivationAfter(time.Minute),
			withAskTimeout(replyTimeout))

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
	})
	t.Run("noSender cannot be restarted", func(t *testing.T) {
		pid := &PID{
			fieldsLocker: &sync.RWMutex{},
		}
		err := pid.Restart(context.TODO())
		assert.Error(t, err)
		assert.EqualError(t, err, ErrUndefinedActor.Error())
	})
	t.Run("With restart failed due to PostStop failure", func(t *testing.T) {
		ctx := context.TODO()

		// create a Ping actor
		actor := &testPostStop{}
		assert.NotNil(t, actor)
		// create the actor path
		ports := dynaport.Get(1)
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the actor ref
		pid, err := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withPassivationAfter(passivateAfter),
			withCustomLogger(log.DiscardLogger),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, pid)
		// let us send 10 public to the actor
		count := 10
		for i := 0; i < count; i++ {
			err := Tell(ctx, pid, new(testpb.TestSend))
			assert.NoError(t, err)
		}

		// restart the actor
		err = pid.Restart(ctx)
		assert.Error(t, err)
		assert.False(t, pid.IsRunning())
	})
}
func TestSupervisorStrategy(t *testing.T) {
	t.Run("With stop as supervisor directive", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		ports := dynaport.Get(1)
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSupervisorDirective(NewStopDirective()),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", newTestSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)
		assert.Len(t, child.Parents(), 1)
		// let us send 10 public to the actors
		count := 10
		for i := 0; i < count; i++ {
			assert.NoError(t, Tell(ctx, parent, new(testpb.TestSend)))
			assert.NoError(t, Tell(ctx, child, new(testpb.TestSend)))
		}

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With the default supervisor directive", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		ports := dynaport.Get(1)
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the parent actor
		parent, err := newPID(ctx,
			actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withPassivationDisabled(),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", newTestSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		lib.Pause(time.Second)

		assert.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		assert.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		lib.Pause(time.Second)

		// assert the actor state
		assert.False(t, child.IsRunning())
		assert.Len(t, parent.Children(), 0)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With default strategy with child actor shutdown failure", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		ports := dynaport.Get(1)
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the parent actor
		parent, err := newPID(ctx,
			actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withPassivationDisabled(),
			withSupervisorDirective(new(unhandledSupervisorDirective)), // only for test to handle default case
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", &testPostStop{})
		assert.NoError(t, err)
		assert.NotNil(t, child)

		lib.Pause(time.Second)

		assert.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		assert.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		lib.Pause(time.Second)

		// assert the actor state
		assert.False(t, child.IsRunning())
		assert.Len(t, parent.Children(), 0)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With stop as supervisor directive with child actor shutdown failure", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		ports := dynaport.Get(1)
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the parent actor
		parent, err := newPID(ctx,
			actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSupervisorDirective(DefaultSupervisoryStrategy),
			withPassivationDisabled(),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", &testPostStop{})
		assert.NoError(t, err)
		assert.NotNil(t, child)

		lib.Pause(time.Second)

		assert.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		assert.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		lib.Pause(time.Second)

		// assert the actor state
		assert.False(t, child.IsRunning())
		assert.Len(t, parent.Children(), 0)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With restart as supervisor strategy", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()

		logger := log.DiscardLogger
		// create the actor path
		ports := dynaport.Get(1)
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])
		// create the parent actor
		parent, err := newPID(ctx,
			actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(logger),
			withPassivationDisabled(),
			withSupervisorDirective(NewRestartDirective()),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", newTestSupervised())
		require.NoError(t, err)
		require.NotNil(t, child)

		lib.Pause(time.Second)

		require.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		lib.Pause(time.Second)

		// assert the actor state
		require.True(t, child.IsRunning())

		// TODO: fix the child relationship supervisor mode
		// require.Len(t, parent.Children(), 1)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With no strategy set will default to a Shutdown", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		ports := dynaport.Get(1)
		// create the actor path
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the parent actor
		parent, err := newPID(ctx,
			actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withPassivationDisabled(),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", newTestSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		assert.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		lib.Pause(time.Second)

		// assert the actor state
		assert.False(t, child.IsRunning())
		assert.Len(t, parent.Children(), 0)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With resume as supervisor strategy", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()

		logger := log.DiscardLogger
		// create the actor path
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])
		// create the parent actor
		parent, err := newPID(ctx,
			actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(logger),
			withPassivationDisabled(),
			withSupervisorDirective(NewResumeDirective()),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", newTestSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		assert.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		lib.Pause(time.Second)

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
	})
	t.Run("With restart with limit as supervisor strategy", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()

		logger := log.DiscardLogger
		// create the actor path
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the directive
		restart := NewRestartDirective()
		restart.WithLimit(2, time.Minute)

		// create the parent actor
		parent, err := newPID(ctx,
			actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(logger),
			withPassivationDisabled(),
			withSupervisorDirective(restart),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", newTestSupervised())
		require.NoError(t, err)
		require.NotNil(t, child)

		require.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		require.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		lib.Pause(time.Second)

		// assert the actor state
		require.True(t, child.IsRunning())

		// TODO: fix the child relationship supervisor mode
		// require.Len(t, parent.Children(), 1)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
}
func TestMessaging(t *testing.T) {
	t.Run("With happy", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}
		ports := dynaport.Get(1)
		// create the actor path
		actor1 := &exchanger{}
		actorPath1 := address.New("Exchange1", "sys", "host", ports[0])
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		actor2 := &exchanger{}
		actorPath2 := address.New("Exchange2", "sys", "host", ports[0])
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		err = pid1.Tell(ctx, pid2, new(testpb.TestSend))
		require.NoError(t, err)

		// send an ask
		reply, err := pid1.Ask(ctx, pid2, new(testpb.TestReply))
		require.NoError(t, err)
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, reply))

		// wait a while because exchange is ongoing
		lib.Pause(time.Second)

		err = Tell(ctx, pid1, new(testpb.TestBye))
		require.NoError(t, err)

		lib.Pause(time.Second)
		assert.False(t, pid1.IsRunning())
		assert.True(t, pid2.IsRunning())

		err = Tell(ctx, pid2, new(testpb.TestBye))
		lib.Pause(time.Second)
		assert.False(t, pid2.IsRunning())
	})
	t.Run("With Ask when not ready", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}
		ports := dynaport.Get(1)
		// create the actor path
		actor1 := &exchanger{}
		actorPath1 := address.New("Exchange1", "sys", "host", ports[0])
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		actor2 := &exchanger{}
		actorPath2 := address.New("Exchange2", "sys", "host", ports[0])
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		lib.Pause(time.Second)

		assert.NoError(t, pid2.Shutdown(ctx))

		// send an ask
		reply, err := pid1.Ask(ctx, pid2, new(testpb.TestReply))
		require.Error(t, err)
		require.EqualError(t, err, ErrDead.Error())
		require.Nil(t, reply)

		// wait a while because exchange is ongoing
		lib.Pause(time.Second)

		err = Tell(ctx, pid1, new(testpb.TestBye))
		require.NoError(t, err)
	})
	t.Run("With Tell when not ready", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}
		ports := dynaport.Get(1)

		// create the actor path
		actor1 := &exchanger{}
		actorPath1 := address.New("Exchange1", "sys", "host", ports[0])
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		actor2 := &exchanger{}
		actorPath2 := address.New("Exchange2", "sys", "host", ports[0])
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		lib.Pause(time.Second)

		assert.NoError(t, pid2.Shutdown(ctx))

		// send an ask
		err = pid1.Tell(ctx, pid2, new(testpb.TestReply))
		require.Error(t, err)
		require.EqualError(t, err, ErrDead.Error())

		// wait a while because exchange is ongoing
		lib.Pause(time.Second)

		err = Tell(ctx, pid1, new(testpb.TestBye))
		require.NoError(t, err)
	})
	t.Run("With Ask timeout", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withAskTimeout(replyTimeout),
		}
		ports := dynaport.Get(1)

		// create the actor path
		actor1 := &exchanger{}
		actorPath1 := address.New("Exchange1", "sys", "host", ports[0])
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		actor2 := newTestActor()
		actorPath2 := address.New("Exchange2", "sys", "host", ports[0])
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		err = pid1.Tell(ctx, pid2, new(testpb.TestSend))
		require.NoError(t, err)

		// send an ask
		reply, err := pid1.Ask(ctx, pid2, new(testpb.TestTimeout))
		require.Error(t, err)
		require.EqualError(t, err, ErrRequestTimeout.Error())
		require.Nil(t, reply)

		// wait a while because exchange is ongoing
		lib.Pause(time.Second)

		err = Tell(ctx, pid1, new(testpb.TestBye))
		require.NoError(t, err)

		lib.Pause(time.Second)
		assert.False(t, pid1.IsRunning())
		assert.True(t, pid2.IsRunning())

		lib.Pause(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))
		assert.False(t, pid2.IsRunning())
	})
}
func TestRemoting(t *testing.T) {
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
		WithPassivationDisabled(),
		WithRemoting(host, int32(remotingPort)),
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
	reply, err := actorRef2.RemoteAsk(ctx, address.From(addr1), new(testpb.TestReply))
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
	lib.Pause(time.Second)

	t.Cleanup(func() {
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}
func TestActorHandle(t *testing.T) {
	ctx := context.TODO()
	// create the actor path
	ports := dynaport.Get(1)
	actorPath := address.New("Test", "sys", "host", ports[0])

	// create the actor ref
	pid, err := newPID(
		ctx,
		actorPath,
		&exchanger{},
		withInitMaxRetries(1),
		withCustomLogger(log.DiscardLogger),
		withAskTimeout(replyTimeout))

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
}
func TestPIDActorSystem(t *testing.T) {
	ctx := context.TODO()
	// create the actor path
	ports := dynaport.Get(1)
	actorPath := address.New("Test", "sys", "host", ports[0])

	// create the actor ref
	pid, err := newPID(
		ctx,
		actorPath,
		&exchanger{},
		withInitMaxRetries(1),
		withCustomLogger(log.DiscardLogger),
		withAskTimeout(replyTimeout))
	require.NoError(t, err)
	assert.NotNil(t, pid)
	sys := pid.ActorSystem()
	assert.Nil(t, sys)
	// stop the actor
	err = pid.Shutdown(ctx)
	assert.NoError(t, err)
}
func TestSpawnChild(t *testing.T) {
	t.Run("With restarting child actor", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", newTestSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)

		// stop the child actor
		assert.NoError(t, child.Shutdown(ctx))

		lib.Pause(100 * time.Millisecond)
		// create the child actor
		child, err = parent.SpawnChild(ctx, "SpawnChild", newTestSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		lib.Pause(time.Second)

		assert.Len(t, parent.Children(), 1)
		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With starting child actor with a different mailbox", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "child", newTestSupervised(), WithMailbox(NewBoundedMailbox(20)))
		assert.NoError(t, err)
		assert.NotNil(t, child)
		lib.Pause(time.Second)
		assert.Len(t, parent.Children(), 1)

		assert.NoError(t, Tell(ctx, child, new(testpb.TestSend)))

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With restarting child actor when not shutdown", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", newTestSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)

		lib.Pause(100 * time.Millisecond)
		// create the child actor
		child, err = parent.SpawnChild(ctx, "SpawnChild", newTestSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		lib.Pause(time.Second)

		assert.Len(t, parent.Children(), 1)
		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With parent not ready", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		lib.Pause(100 * time.Millisecond)
		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", newTestSupervised())
		assert.Error(t, err)
		assert.EqualError(t, err, ErrDead.Error())
		assert.Nil(t, child)
	})
	t.Run("With failed init", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", &testPreStart{})
		assert.Error(t, err)
		assert.Nil(t, child)

		assert.Len(t, parent.Children(), 0)
		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With child created event published", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])

		eventsStream := eventstream.New()

		// add a subscriber
		subsriber := eventsStream.AddSubscriber()
		eventsStream.Subscribe(subsriber, eventsTopic)

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withEventsStream(eventsStream),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", newTestSupervised())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)

		lib.Pause(time.Second)

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
	})
}
func TestPoisonPill(t *testing.T) {
	ctx := context.TODO()

	// create the actor path
	ports := dynaport.Get(1)
	actorPath := address.New("Test", "sys", "host", ports[0])

	// create the actor ref
	pid, err := newPID(
		ctx,
		actorPath,
		newTestActor(),
		withInitMaxRetries(1),
		withCustomLogger(log.DiscardLogger),
		withAskTimeout(replyTimeout))

	require.NoError(t, err)
	assert.NotNil(t, pid)

	assert.True(t, pid.IsRunning())
	// send a poison pill to the actor
	err = Tell(ctx, pid, new(goaktpb.PoisonPill))
	assert.NoError(t, err)

	// wait for the graceful shutdown
	lib.Pause(time.Second)
	assert.False(t, pid.IsRunning())
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
			WithPassivationDisabled(),
			WithRemoting(host, int32(remotingPort)),
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
	t.Run("With remoting not enabled", func(t *testing.T) {
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
			WithPassivationDisabled())
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
}
func TestFailedPreStart(t *testing.T) {
	// create the context
	ctx := context.TODO()
	// define the logger to use
	logger := log.DiscardLogger

	// create the actor system
	sys, err := NewActorSystem("test",
		WithLogger(logger),
		WithActorInitMaxRetries(1),
		WithPassivationDisabled())
	// assert there are no error
	require.NoError(t, err)

	// start the actor system
	err = sys.Start(ctx)
	assert.NoError(t, err)

	// create an exchanger 1
	actorName1 := "Exchange1"
	pid, err := sys.Spawn(ctx, actorName1, &testPreStart{})
	require.Error(t, err)
	require.EqualError(t, err, "failed to initialize: failed")
	require.Nil(t, pid)

	t.Cleanup(func() {
		assert.NoError(t, sys.Stop(ctx))
	})
}
func TestFailedPostStop(t *testing.T) {
	ctx := context.TODO()

	// create the actor path
	ports := dynaport.Get(1)
	actorPath := address.New("Test", "sys", "host", ports[0])

	// create the actor ref
	pid, err := newPID(
		ctx,
		actorPath,
		&testPostStop{},
		withInitMaxRetries(1),
		withCustomLogger(log.DiscardLogger),
		withAskTimeout(replyTimeout))

	require.NoError(t, err)
	assert.NotNil(t, pid)

	assert.Error(t, pid.Shutdown(ctx))
}
func TestShutdown(t *testing.T) {
	t.Run("With Shutdown panic to child stop failure", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", &testPostStop{})
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)

		//stop the
		assert.Error(t, parent.Shutdown(ctx))
	})
}
func TestBatchTell(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		actor := newTestActor()
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// batch tell
		require.NoError(t, pid.BatchTell(ctx, pid, new(testpb.TestSend), new(testpb.TestSend)))
		// wait for the asynchronous processing to complete
		lib.Pause(100 * time.Millisecond)
		assert.EqualValues(t, 2, pid.ProcessedCount()-1)
		// shutdown the actor when
		// wait a while because exchange is ongoing
		lib.Pause(time.Second)
		assert.NoError(t, pid.Shutdown(ctx))
	})
	t.Run("With a Tell behavior", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		actor := newTestActor()
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// batch tell
		require.NoError(t, pid.BatchTell(ctx, pid, new(testpb.TestSend)))
		// wait for the asynchronous processing to complete
		lib.Pause(100 * time.Millisecond)
		assert.EqualValues(t, 1, pid.ProcessedCount()-1)
		// shutdown the actor when
		// wait a while because exchange is ongoing
		lib.Pause(time.Second)
		assert.NoError(t, pid.Shutdown(ctx))
	})
	t.Run("With a dead actor", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		actor := newTestActor()
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		lib.Pause(time.Second)
		assert.NoError(t, pid.Shutdown(ctx))

		// batch tell
		require.Error(t, pid.BatchTell(ctx, pid, new(testpb.TestSend)))
	})
}
func TestBatchAsk(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		actor := &exchanger{}
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// batch ask
		responses, err := pid.BatchAsk(ctx, pid, new(testpb.TestReply), new(testpb.TestReply))
		require.NoError(t, err)
		for reply := range responses {
			require.NoError(t, err)
			require.NotNil(t, reply)
			expected := new(testpb.Reply)
			assert.True(t, proto.Equal(expected, reply))
		}

		// wait a while because exchange is ongoing
		lib.Pause(time.Second)
		assert.NoError(t, pid.Shutdown(ctx))
	})
	t.Run("With a dead actor", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		actor := &exchanger{}
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		lib.Pause(time.Second)
		assert.NoError(t, pid.Shutdown(ctx))

		// batch ask
		responses, err := pid.BatchAsk(ctx, pid, new(testpb.TestReply), new(testpb.TestReply))
		require.Error(t, err)
		require.Nil(t, responses)
	})
	t.Run("With a timeout", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withAskTimeout(replyTimeout),
		}

		// create the actor path
		actor := newTestActor()
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// batch ask
		responses, err := pid.BatchAsk(ctx, pid, new(testpb.TestTimeout), new(testpb.TestReply))
		require.Error(t, err)
		require.Empty(t, responses)

		// wait a while because exchange is ongoing
		lib.Pause(time.Second)
		assert.NoError(t, pid.Shutdown(ctx))
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
			WithPassivationDisabled(),
			WithRemoting(host, int32(remotingPort)),
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
	t.Run("With remoting not enabled", func(t *testing.T) {
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
			WithPassivationDisabled())
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
			WithPassivationDisabled(),
			WithRemoting(host, int32(remotingPort)),
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
			WithPassivationDisabled(),
			WithRemoting(host, int32(remotingPort)),
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
			WithPassivationDisabled(),
			WithRemoting(host, int32(remotingPort)),
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
}
func TestID(t *testing.T) {
	ctx := context.TODO()
	// create the actor path
	ports := dynaport.Get(1)
	actorPath := address.New("Test", "sys", "host", ports[0])

	// create the actor ref
	pid, err := newPID(
		ctx,
		actorPath,
		&exchanger{},
		withInitMaxRetries(1),
		withCustomLogger(log.DiscardLogger),
		withAskTimeout(replyTimeout))
	require.NoError(t, err)
	assert.NotNil(t, pid)
	sys := pid.ActorSystem()
	assert.Nil(t, sys)

	expected := actorPath.String()
	actual := pid.ID()

	require.Equal(t, expected, actual)

	// stop the actor
	err = pid.Shutdown(ctx)
	assert.NoError(t, err)
}
func TestEquals(t *testing.T) {
	ctx := context.TODO()
	logger := log.DiscardLogger
	sys, err := NewActorSystem("test",
		WithLogger(logger),
		WithPassivationDisabled())

	require.NoError(t, err)
	err = sys.Start(ctx)
	assert.NoError(t, err)

	pid1, err := sys.Spawn(ctx, "test", newTestActor())
	require.NoError(t, err)
	assert.NotNil(t, pid1)

	pid2, err := sys.Spawn(ctx, "exchange", &exchanger{})
	require.NoError(t, err)
	assert.NotNil(t, pid2)

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
			WithPassivationDisabled(),
			WithRemoting(host, int32(remotingPort)),
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
		err = pid.RemoteSpawn(ctx, host, remotingPort, actorName, "actors.exchanger")
		require.NoError(t, err)

		// re-fetching the address of the actor should return not nil address after start
		addr, err = pid.RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.NotNil(t, addr)

		// send the message to exchanger actor one using remote messaging
		reply, err := pid.RemoteAsk(ctx, address.From(addr), new(testpb.TestReply))

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
			WithPassivationDisabled(),
			WithRemoting(host, int32(remotingPort)),
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
		assert.EqualError(t, err, ErrTypeNotRegistered.Error())

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
			WithPassivationDisabled(),
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
}
func TestName(t *testing.T) {
	ctx := context.TODO()
	// create the actor path
	ports := dynaport.Get(1)
	actorPath := address.New("Test", "sys", "host", ports[0])

	// create the actor ref
	pid, err := newPID(
		ctx,
		actorPath,
		&exchanger{},
		withInitMaxRetries(1),
		withCustomLogger(log.DiscardLogger),
		withAskTimeout(replyTimeout))
	require.NoError(t, err)
	assert.NotNil(t, pid)
	sys := pid.ActorSystem()
	assert.Nil(t, sys)

	expected := actorPath.Name()
	actual := pid.Name()

	require.Equal(t, expected, actual)

	// stop the actor
	err = pid.Shutdown(ctx)
	assert.NoError(t, err)
}
func TestPipeTo(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		askTimeout := time.Minute
		ctx := context.TODO()

		opts := []pidOption{
			withInitMaxRetries(1),
			withAskTimeout(askTimeout),
			withPassivationDisabled(),
			withCustomLogger(log.DiscardLogger),
		}
		ports := dynaport.Get(1)

		// create actor1
		actor1 := &exchanger{}
		actorPath1 := address.New("Exchange1", "sys", "host", ports[0])
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		actor2 := &exchanger{}
		actorPath2 := address.New("Exchange2", "sys", "host", ports[0])
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		lib.Pause(500 * time.Millisecond)
		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := make(chan proto.Message)
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
			lib.Pause(time.Second)
			wg.Done()
		}()

		// now we complete the Task
		task <- new(testspb.TaskComplete)
		wg.Wait()

		lib.Pause(time.Second)

		require.EqualValues(t, 3, pid1.ProcessedCount()-1)
		require.EqualValues(t, 1, pid2.ProcessedCount()-1)

		lib.Pause(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
	})
	t.Run("With is a dead actor: case 1", func(t *testing.T) {
		askTimeout := time.Minute
		ctx := context.TODO()

		opts := []pidOption{
			withInitMaxRetries(1),
			withAskTimeout(askTimeout),
			withPassivationDisabled(),
			withCustomLogger(log.DiscardLogger),
		}
		ports := dynaport.Get(1)

		// create actor1
		actor1 := &exchanger{}
		actorPath1 := address.New("Exchange1", "sys", "host", ports[0])
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		actor2 := &exchanger{}
		actorPath2 := address.New("Exchange2", "sys", "host", ports[0])
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// shutdown the actor after one second of liveness
		lib.Pause(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))

		task := make(chan proto.Message)
		err = pid1.PipeTo(ctx, pid2, task)
		require.Error(t, err)
		assert.EqualError(t, err, ErrDead.Error())

		lib.Pause(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
	})
	t.Run("With is a dead actor: case 2", func(t *testing.T) {
		askTimeout := time.Minute
		ctx := context.TODO()

		opts := []pidOption{
			withInitMaxRetries(1),
			withAskTimeout(askTimeout),
			withPassivationDisabled(),
			withCustomLogger(log.DiscardLogger),
		}
		ports := dynaport.Get(1)

		// create actor1
		actor1 := &exchanger{}
		actorPath1 := address.New("Exchange1", "sys", "host", ports[0])
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		actor2 := &exchanger{}
		actorPath2 := address.New("Exchange2", "sys", "host", ports[0])
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		lib.Pause(time.Second)

		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := make(chan proto.Message)
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
			lib.Pause(time.Second)
			wg.Done()
		}()

		// now we complete the Task
		task <- new(testspb.TaskComplete)
		_ = Tell(ctx, pid2, new(testpb.TestBye))

		wg.Wait()

		lib.Pause(2 * time.Second)

		require.EqualValues(t, 3, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount())

		lib.Pause(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
	})
	t.Run("With undefined task", func(t *testing.T) {
		askTimeout := time.Minute
		ctx := context.TODO()

		opts := []pidOption{
			withInitMaxRetries(1),
			withAskTimeout(askTimeout),
			withPassivationDisabled(),
			withCustomLogger(log.DiscardLogger),
		}
		ports := dynaport.Get(1)

		// create actor1
		actor1 := &exchanger{}
		actorPath1 := address.New("Exchange1", "sys", "host", ports[0])
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		actor2 := &exchanger{}
		actorPath2 := address.New("Exchange2", "sys", "host", ports[0])
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		lib.Pause(time.Second)

		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		err = pid1.PipeTo(ctx, pid2, nil)
		require.Error(t, err)
		assert.EqualError(t, err, ErrUndefinedTask.Error())

		lib.Pause(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
	})
	t.Run("With failed task", func(t *testing.T) {
		askTimeout := time.Minute
		ctx := context.TODO()

		opts := []pidOption{
			withInitMaxRetries(1),
			withAskTimeout(askTimeout),
			withPassivationDisabled(),
			withCustomLogger(log.DiscardLogger),
		}
		ports := dynaport.Get(1)

		// create actor1
		actor1 := &exchanger{}
		actorPath1 := address.New("Exchange1", "sys", "host", ports[0])
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		actor2 := &exchanger{}
		actorPath2 := address.New("Exchange2", "sys", "host", ports[0])
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		lib.Pause(time.Second)

		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := make(chan proto.Message)

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
			lib.Pause(time.Second)
			wg.Done()
		}()

		cancel()
		wg.Wait()

		lib.Pause(time.Second)

		require.EqualValues(t, 3, pid1.ProcessedCount()-1)

		// no message piped to the actor
		require.Zero(t, pid2.ProcessedCount()-1)

		lib.Pause(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
	})
}
func TestSendAsync(t *testing.T) {
	t.Run("With local actor", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		receivingActor := newTestActor()
		receiver, err := actorSystem.Spawn(ctx, "receiver", receivingActor)
		assert.NoError(t, err)
		assert.NotNil(t, receiver)

		lib.Pause(time.Second)

		sender, err := actorSystem.Spawn(ctx, "sender", newTestActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		lib.Pause(time.Second)

		err = sender.SendAsync(ctx, receiver.Name(), new(testpb.TestSend))
		require.NoError(t, err)

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

		lib.Pause(time.Second)

		receivingActor := newTestActor()
		receiver, err := actorSystem.Spawn(ctx, "receiver", receivingActor)
		assert.NoError(t, err)
		assert.NotNil(t, receiver)

		lib.Pause(time.Second)

		sender, err := actorSystem.Spawn(ctx, "sender", newTestActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		lib.Pause(time.Second)

		require.NoError(t, actorSystem.Kill(ctx, sender.Name()))

		err = sender.SendAsync(ctx, receiver.Name(), new(testpb.TestSend))
		require.Error(t, err)
		assert.EqualError(t, err, ErrDead.Error())

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
		node1, sd1 := startClusterSystem(t, "Node1", srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := startClusterSystem(t, "Node2", srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		// create an actor on node1
		receiver, err := node1.Spawn(ctx, "receiver", newTestActor())
		assert.NoError(t, err)
		assert.NotNil(t, receiver)
		lib.Pause(time.Second)

		sender, err := node2.Spawn(ctx, "sender", newTestActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		lib.Pause(time.Second)

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
		node1, sd1 := startClusterSystem(t, "Node1", srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := startClusterSystem(t, "Node2", srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		sender, err := node2.Spawn(ctx, "sender", newTestActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		lib.Pause(time.Second)

		err = sender.SendAsync(ctx, "receiver", new(testpb.TestSend))
		require.Error(t, err)
		assert.EqualError(t, err, ErrActorNotFound("receiver").Error())

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

		lib.Pause(time.Second)

		receiver, err := actorSystem.Spawn(ctx, "receiver", newTestActor())
		assert.NoError(t, err)
		assert.NotNil(t, receiver)

		lib.Pause(time.Second)

		sender, err := actorSystem.Spawn(ctx, "sender", newTestActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		lib.Pause(time.Second)

		response, err := sender.SendSync(ctx, receiver.Name(), new(testpb.TestReply))
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

		lib.Pause(time.Second)

		receiver, err := actorSystem.Spawn(ctx, "receiver", newTestActor())
		assert.NoError(t, err)
		assert.NotNil(t, receiver)

		lib.Pause(time.Second)

		sender, err := actorSystem.Spawn(ctx, "sender", newTestActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		lib.Pause(time.Second)
		err = actorSystem.Kill(ctx, sender.Name())
		require.NoError(t, err)

		response, err := sender.SendSync(ctx, receiver.Name(), new(testpb.TestReply))
		require.Error(t, err)
		require.Nil(t, response)
		assert.EqualError(t, err, ErrDead.Error())

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
		node1, sd1 := startClusterSystem(t, "Node1", srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := startClusterSystem(t, "Node2", srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		// create an actor on node1
		receiver, err := node1.Spawn(ctx, "receiver", newTestActor())
		assert.NoError(t, err)
		assert.NotNil(t, receiver)
		lib.Pause(time.Second)

		sender, err := node2.Spawn(ctx, "sender", newTestActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		lib.Pause(time.Second)

		response, err := sender.SendSync(ctx, receiver.Name(), new(testpb.TestReply))
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
		node1, sd1 := startClusterSystem(t, "Node1", srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := startClusterSystem(t, "Node2", srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		sender, err := node2.Spawn(ctx, "sender", newTestActor())
		assert.NoError(t, err)
		assert.NotNil(t, sender)

		lib.Pause(time.Second)

		response, err := sender.SendSync(ctx, "receiver", new(testpb.TestReply))
		require.Nil(t, response)
		require.Error(t, err)
		assert.EqualError(t, err, ErrActorNotFound("receiver").Error())

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
		// create a test context
		ctx := context.TODO()
		// create the actor path
		ports := dynaport.Get(1)
		actorPath := address.New("Test", "sys", "host", ports[0])

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			newTestSupervisor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withAskTimeout(replyTimeout))

		require.NoError(t, err)
		require.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", new(testPostStop))
		require.NoError(t, err)
		require.NotNil(t, child)
		require.Len(t, parent.Children(), 1)
		// stop the child actor
		require.Error(t, parent.Stop(ctx, child))

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
}
func TestNewPID(t *testing.T) {
	t.Run("With actor path not defined", func(t *testing.T) {
		ctx := context.Background()
		pid, err := newPID(
			ctx,
			nil,
			newTestActor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withAskTimeout(replyTimeout))

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
