/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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
	"context"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
	messagespb "github.com/tochemey/goakt/pb/messages/v1"
	"github.com/tochemey/goakt/telemetry"
	testpb "github.com/tochemey/goakt/test/data/pb/v1"
	"github.com/travisjeffery/go-dynaport"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

const (
	receivingDelay   = 1 * time.Second
	receivingTimeout = 100 * time.Millisecond
	passivateAfter   = 200 * time.Millisecond
)

func TestActorReceive(t *testing.T) {
	ctx := context.TODO()

	// create the actor path
	actorPath := NewPath("Test", NewAddress("sys", "host", 1))

	// create the actor ref
	pid, err := newPID(
		ctx,
		actorPath,
		NewTester(),
		withInitMaxRetries(1),
		withCustomLogger(log.DefaultLogger),
		withSendReplyTimeout(receivingTimeout))

	require.NoError(t, err)
	assert.NotNil(t, pid)
	// let us send 10 public to the actor
	count := 10
	for i := 0; i < count; i++ {
		receiveContext := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		pid.doReceive(receiveContext)
	}

	// stop the actor
	err = pid.Shutdown(ctx)
	assert.NoError(t, err)
}
func TestActorWithPassivation(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withPassivationAfter(passivateAfter),
			withSendReplyTimeout(receivingTimeout),
		}

		// create the actor path
		actorPath := NewPath("Test", NewAddress("sys", "host", 1))
		pid, err := newPID(ctx, actorPath, NewTester(), opts...)
		require.NoError(t, err)
		assert.NotNil(t, pid)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			time.Sleep(receivingDelay)
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
			withSendReplyTimeout(receivingTimeout),
		}

		// create the actor path
		actorPath := NewPath("Test", NewAddress("sys", "host", 1))
		pid, err := newPID(ctx, actorPath, &PostStopBreaker{}, opts...)
		require.NoError(t, err)
		assert.NotNil(t, pid)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			time.Sleep(receivingDelay)
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
func TestActorWithReply(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		actorPath := NewPath("Test", NewAddress("sys", "host", 1))
		pid, err := newPID(ctx, actorPath, NewTester(), opts...)

		require.NoError(t, err)
		assert.NotNil(t, pid)

		actual, err := Ask(ctx, pid, new(testpb.TestReply), receivingTimeout)
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
		actorPath := NewPath("Test", NewAddress("sys", "host", 1))
		pid, err := newPID(ctx, actorPath, NewTester(), opts...)

		require.NoError(t, err)
		assert.NotNil(t, pid)

		actual, err := Ask(ctx, pid, new(testpb.TestSend), receivingTimeout)
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
		actorPath := NewPath("Test", NewAddress("sys", "host", 1))
		pid, err := newPID(ctx, actorPath, NewTester(), opts...)

		require.NoError(t, err)
		assert.NotNil(t, pid)

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)

		actual, err := Ask(ctx, pid, new(testpb.TestReply), receivingTimeout)
		assert.Error(t, err)
		assert.EqualError(t, err, ErrDead.Error())
		assert.Nil(t, actual)
	})
}
func TestActorRestart(t *testing.T) {
	t.Run("With restart a stopped actor", func(t *testing.T) {
		ctx := context.TODO()

		// create a Ping actor
		actor := NewTester()
		assert.NotNil(t, actor)

		// create the actor path
		actorPath := NewPath("Test", NewAddress("sys", "host", 1))
		// create the actor ref
		pid, err := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withPassivationAfter(10*time.Second),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, pid)

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)

		time.Sleep(time.Second)

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
		actor := NewTester()
		assert.NotNil(t, actor)
		// create the actor path
		actorPath := NewPath("Test", NewAddress("sys", "host", 1))

		// create the actor ref
		pid, err := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

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
		actor := NewRestartBreaker()
		assert.NotNil(t, actor)
		// create the actor path
		actorPath := NewPath("Test", NewAddress("sys", "host", 1))

		// create the actor ref
		pid, err := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withPassivationAfter(time.Minute),
			withSendReplyTimeout(receivingTimeout))

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
		pid := &pid{
			tracer: trace.NewNoopTracerProvider().Tracer(""),
		}
		err := pid.Restart(context.TODO())
		assert.Error(t, err)
		assert.EqualError(t, err, ErrUndefinedActor.Error())
	})
	t.Run("With restart failed due to PostStop failure", func(t *testing.T) {
		ctx := context.TODO()

		// create a Ping actor
		actor := &PostStopBreaker{}
		assert.NotNil(t, actor)
		// create the actor path
		actorPath := NewPath("Test", NewAddress("sys", "host", 1))

		// create the actor ref
		pid, err := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withPassivationAfter(passivateAfter),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

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
func TestActorWithSupervisorStrategy(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMonitored())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)
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
	t.Run("With stop as default strategy", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx,
			actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withPassivationDisabled(),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMonitored())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		time.Sleep(time.Second)

		assert.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		assert.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		time.Sleep(time.Second)

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
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx,
			actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withPassivationDisabled(),
			withSupervisorStrategy(-1), // this is a rogue strategy which will default to a Stop
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", &PostStopBreaker{})
		assert.NoError(t, err)
		assert.NotNil(t, child)

		time.Sleep(time.Second)

		assert.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		assert.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		time.Sleep(time.Second)

		// assert the actor state
		assert.False(t, child.IsRunning())
		assert.Len(t, parent.Children(), 0)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With stop as default strategy with child actor shutdown failure", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx,
			actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSupervisorStrategy(DefaultSupervisoryStrategy),
			withPassivationDisabled(),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", &PostStopBreaker{})
		assert.NoError(t, err)
		assert.NotNil(t, child)

		time.Sleep(time.Second)

		assert.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		assert.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		time.Sleep(time.Second)

		// assert the actor state
		assert.False(t, child.IsRunning())
		assert.Len(t, parent.Children(), 0)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With restart as default strategy", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()

		logger := log.New(log.DebugLevel, os.Stdout)
		// create the actor path
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))
		// create the parent actor
		parent, err := newPID(ctx,
			actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(logger),
			withPassivationDisabled(),
			withSupervisorStrategy(RestartDirective),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMonitored())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		assert.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		time.Sleep(time.Second)

		// assert the actor state
		assert.True(t, child.IsRunning())
		require.Len(t, parent.Children(), 1)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With no strategy set will default to a Shutdown", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx,
			actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withPassivationDisabled(),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// this is for the sake of the test
		parent.supervisorStrategy = StrategyDirective(-1)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMonitored())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)
		// send a test panic message to the actor
		assert.NoError(t, Tell(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		time.Sleep(time.Second)

		// assert the actor state
		assert.False(t, child.IsRunning())
		assert.Len(t, parent.Children(), 0)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
}
func TestActorToActor(t *testing.T) {
	t.Run("With happy", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		actor2 := &Exchanger{}
		actorPath2 := NewPath("Exchange2", NewAddress("sys", "host", 1))
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
		time.Sleep(time.Second)

		err = Tell(ctx, pid1, new(testpb.TestBye))
		require.NoError(t, err)

		time.Sleep(time.Second)
		assert.False(t, pid1.IsRunning())
		assert.True(t, pid2.IsRunning())

		err = Tell(ctx, pid2, new(testpb.TestBye))
		time.Sleep(time.Second)
		assert.False(t, pid2.IsRunning())
	})
	t.Run("With Ask when not ready", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		actor2 := &Exchanger{}
		actorPath2 := NewPath("Exchange2", NewAddress("sys", "host", 1))
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		time.Sleep(time.Second)

		assert.NoError(t, pid2.Shutdown(ctx))

		// send an ask
		reply, err := pid1.Ask(ctx, pid2, new(testpb.TestReply))
		require.Error(t, err)
		require.EqualError(t, err, ErrDead.Error())
		require.Nil(t, reply)

		// wait a while because exchange is ongoing
		time.Sleep(time.Second)

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

		// create the actor path
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		actor2 := &Exchanger{}
		actorPath2 := NewPath("Exchange2", NewAddress("sys", "host", 1))
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		time.Sleep(time.Second)

		assert.NoError(t, pid2.Shutdown(ctx))

		// send an ask
		err = pid1.Tell(ctx, pid2, new(testpb.TestReply))
		require.Error(t, err)
		require.EqualError(t, err, ErrDead.Error())

		// wait a while because exchange is ongoing
		time.Sleep(time.Second)

		err = Tell(ctx, pid1, new(testpb.TestBye))
		require.NoError(t, err)
	})
	t.Run("With Ask timeout", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout),
		}

		// create the actor path
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		actor2 := NewTester()
		actorPath2 := NewPath("Exchange2", NewAddress("sys", "host", 1))
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
		time.Sleep(time.Second)

		err = Tell(ctx, pid1, new(testpb.TestBye))
		require.NoError(t, err)

		time.Sleep(time.Second)
		assert.False(t, pid1.IsRunning())
		assert.True(t, pid2.IsRunning())

		time.Sleep(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))
		assert.False(t, pid2.IsRunning())
	})
}
func TestActorRemoting(t *testing.T) {
	// create the context
	ctx := context.TODO()
	// define the logger to use
	logger := log.New(log.DebugLevel, os.Stdout)
	// generate the remoting port
	nodePorts := dynaport.Get(1)
	remotingPort := nodePorts[0]
	host := "localhost"

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
	actorRef1, err := sys.Spawn(ctx, actorName1, &Exchanger{})
	require.NoError(t, err)
	assert.NotNil(t, actorRef1)

	// create an exchanger two
	actorName2 := "Exchange2"
	actorRef2, err := sys.Spawn(ctx, actorName2, &Exchanger{})
	require.NoError(t, err)
	assert.NotNil(t, actorRef2)

	// get the address of the exchanger actor one
	addr1, err := actorRef2.RemoteLookup(ctx, host, remotingPort, actorName1)
	require.NoError(t, err)

	// send the message to t exchanger actor one using remote messaging
	reply, err := actorRef2.RemoteAsk(ctx, addr1, new(testpb.TestReply))
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
	err = actorRef2.RemoteTell(ctx, addr1, new(testpb.TestRemoteSend))
	require.NoError(t, err)

	// stop the actor after some time
	time.Sleep(time.Second)

	t.Cleanup(func() {
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}
func TestActorHandle(t *testing.T) {
	ctx := context.TODO()
	// create the actor path
	actorPath := NewPath("Test", NewAddress("sys", "host", 1))

	// create the actor ref
	pid, err := newPID(
		ctx,
		actorPath,
		&Exchanger{},
		withInitMaxRetries(1),
		withCustomLogger(log.DefaultLogger),
		withSendReplyTimeout(receivingTimeout))

	require.NoError(t, err)
	assert.NotNil(t, pid)
	actorHandle := pid.ActorHandle()
	assert.IsType(t, &Exchanger{}, actorHandle)
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
	actorPath := NewPath("Test", NewAddress("sys", "host", 1))

	// create the actor ref
	pid, err := newPID(
		ctx,
		actorPath,
		&Exchanger{},
		withInitMaxRetries(1),
		withCustomLogger(log.DefaultLogger),
		withSendReplyTimeout(receivingTimeout))
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
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMonitored())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)

		// stop the child actor
		assert.NoError(t, child.Shutdown(ctx))

		time.Sleep(100 * time.Millisecond)
		// create the child actor
		child, err = parent.SpawnChild(ctx, "SpawnChild", NewMonitored())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		time.Sleep(time.Second)

		assert.Len(t, parent.Children(), 1)
		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With restarting child actor when not shutdown", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMonitored())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)

		time.Sleep(100 * time.Millisecond)
		// create the child actor
		child, err = parent.SpawnChild(ctx, "SpawnChild", NewMonitored())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		time.Sleep(time.Second)

		assert.Len(t, parent.Children(), 1)
		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With parent not ready", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		time.Sleep(100 * time.Millisecond)
		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", NewMonitored())
		assert.Error(t, err)
		assert.EqualError(t, err, ErrDead.Error())
		assert.Nil(t, child)
	})
	t.Run("With failed init", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", &PreStartBreaker{})
		assert.Error(t, err)
		assert.Nil(t, child)

		assert.Len(t, parent.Children(), 0)
		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
}
func TestPoisonPill(t *testing.T) {
	ctx := context.TODO()

	// create the actor path
	actorPath := NewPath("Test", NewAddress("sys", "host", 1))

	// create the actor ref
	pid, err := newPID(
		ctx,
		actorPath,
		NewTester(),
		withInitMaxRetries(1),
		withCustomLogger(log.DefaultLogger),
		withSendReplyTimeout(receivingTimeout))

	require.NoError(t, err)
	assert.NotNil(t, pid)

	assert.True(t, pid.IsRunning())
	// send a poison pill to the actor
	err = Tell(ctx, pid, new(messagespb.PoisonPill))
	assert.NoError(t, err)

	// wait for the graceful shutdown
	time.Sleep(time.Second)
	assert.False(t, pid.IsRunning())
}
func TestRemoteLookup(t *testing.T) {
	t.Run("With actor address not found", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "localhost"

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
		actorRef1, err := sys.Spawn(ctx, actorName1, &Exchanger{})

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
		logger := log.New(log.DebugLevel, os.Stdout)
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "localhost"

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
		actorRef1, err := sys.Spawn(ctx, actorName1, &Exchanger{})

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
	logger := log.New(log.DebugLevel, os.Stdout)

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
	pid, err := sys.Spawn(ctx, actorName1, &PreStartBreaker{})
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
	actorPath := NewPath("Test", NewAddress("sys", "host", 1))

	// create the actor ref
	pid, err := newPID(
		ctx,
		actorPath,
		&PostStopBreaker{},
		withInitMaxRetries(1),
		withCustomLogger(log.DefaultLogger),
		withSendReplyTimeout(receivingTimeout))

	require.NoError(t, err)
	assert.NotNil(t, pid)

	assert.Error(t, pid.Shutdown(ctx))
}
func TestShutdown(t *testing.T) {
	t.Run("With Shutdown panic to child stop failure", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create the actor path
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "SpawnChild", &PostStopBreaker{})
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(), 1)

		//stop the
		assert.Panics(t, func() {
			err = parent.Shutdown(ctx)
			assert.Nil(t, err)
		})
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
		actor := NewTester()
		actorPath := NewPath("Tester", NewAddress("sys", "host", 1))
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// batch tell
		require.NoError(t, pid.BatchTell(ctx, pid, new(testpb.TestSend), new(testpb.TestSend)))
		// wait for the asynchronous processing to complete
		time.Sleep(100 * time.Millisecond)
		assert.EqualValues(t, 2, actor.counter.Load())
		// shutdown the actor when
		// wait a while because exchange is ongoing
		time.Sleep(time.Second)
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
		actor := NewTester()
		actorPath := NewPath("Tester", NewAddress("sys", "host", 1))
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// batch tell
		require.NoError(t, pid.BatchTell(ctx, pid, new(testpb.TestSend)))
		// wait for the asynchronous processing to complete
		time.Sleep(100 * time.Millisecond)
		assert.EqualValues(t, 1, actor.counter.Load())
		// shutdown the actor when
		// wait a while because exchange is ongoing
		time.Sleep(time.Second)
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
		actor := NewTester()
		actorPath := NewPath("Tester", NewAddress("sys", "host", 1))
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		time.Sleep(time.Second)
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
		actor := &Exchanger{}
		actorPath := NewPath("Tester", NewAddress("sys", "host", 1))
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
		time.Sleep(time.Second)
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
		actor := &Exchanger{}
		actorPath := NewPath("Tester", NewAddress("sys", "host", 1))
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		time.Sleep(time.Second)
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
			withSendReplyTimeout(receivingTimeout),
		}

		// create the actor path
		actor := NewTester()
		actorPath := NewPath("Tester", NewAddress("sys", "host", 1))
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// batch ask
		responses, err := pid.BatchAsk(ctx, pid, new(testpb.TestTimeout), new(testpb.TestReply))
		require.Error(t, err)
		require.Empty(t, responses)

		// wait a while because exchange is ongoing
		time.Sleep(time.Second)
		assert.NoError(t, pid.Shutdown(ctx))
	})
}
func TestRegisterMetrics(t *testing.T) {
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	// create an instance of telemetry
	tel := telemetry.New(telemetry.WithMeterProvider(mp))

	ctx := context.TODO()

	// create the actor path
	actorPath := NewPath("Test", NewAddress("sys", "host", 1))

	// create the actor ref
	pid, err := newPID(
		ctx,
		actorPath,
		NewTester(),
		withInitMaxRetries(1),
		withCustomLogger(log.DefaultLogger),
		withTelemetry(tel),
		withMetric(),
		withSendReplyTimeout(receivingTimeout))

	require.NoError(t, err)
	assert.NotNil(t, pid)

	// let us send 10 public to the actor
	count := 10
	for i := 0; i < count; i++ {
		receiveContext := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		pid.doReceive(receiveContext)
	}

	// assert some metrics

	// Should collect 3 metrics
	got := &metricdata.ResourceMetrics{}
	err = r.Collect(ctx, got)
	require.NoError(t, err)
	assert.Len(t, got.ScopeMetrics, 1)
	assert.Len(t, got.ScopeMetrics[0].Metrics, 3)

	expected := []string{
		"actor_child_count",
		"actor_stash_count",
		"actor_restart_count",
	}
	// sort the array
	sort.Strings(expected)
	// get the metric names
	actual := make([]string, len(got.ScopeMetrics[0].Metrics))
	for i, metric := range got.ScopeMetrics[0].Metrics {
		actual[i] = metric.Name
	}
	sort.Strings(actual)

	assert.ElementsMatch(t, expected, actual)

	// stop the actor
	err = pid.Shutdown(ctx)
	assert.NoError(t, err)
}
