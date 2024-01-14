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
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/eventstream"
	"github.com/tochemey/goakt/log"
	addresspb "github.com/tochemey/goakt/pb/address/v1"
	eventspb "github.com/tochemey/goakt/pb/events/v1"
	testpb "github.com/tochemey/goakt/test/data/pb/v1"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestReceiveContext(t *testing.T) {
	t.Run("With Actor behaviors", func(t *testing.T) {
		ctx := context.TODO()
		// create the actor options
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		actor := &UserActor{}
		actorPath := NewPath("User", NewAddress("sys", "host", 1))
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// send Login
		var expected proto.Message
		success, err := Ask(ctx, pid, new(testpb.TestLogin), receivingTimeout)
		require.NoError(t, err)
		require.NotNil(t, success)
		expected = &testpb.TestLoginSuccess{}
		require.True(t, proto.Equal(expected, success))

		// ask for readiness
		ready, err := Ask(ctx, pid, new(testpb.TestReadiness), receivingTimeout)
		require.NoError(t, err)
		require.NotNil(t, ready)
		expected = &testpb.TestReady{}
		require.True(t, proto.Equal(expected, ready))

		// send a message to create account
		created, err := Ask(ctx, pid, new(testpb.CreateAccount), receivingTimeout)
		require.NoError(t, err)
		require.NotNil(t, created)
		expected = &testpb.AccountCreated{}
		require.True(t, proto.Equal(expected, created))

		// credit account
		credited, err := Ask(ctx, pid, new(testpb.CreditAccount), receivingTimeout)
		require.NoError(t, err)
		require.NotNil(t, credited)
		expected = &testpb.AccountCredited{}
		require.True(t, proto.Equal(expected, credited))

		// debit account
		debited, err := Ask(ctx, pid, new(testpb.DebitAccount), receivingTimeout)
		require.NoError(t, err)
		require.NotNil(t, debited)
		expected = &testpb.AccountDebited{}
		require.True(t, proto.Equal(expected, debited))

		// send bye
		err = Tell(ctx, pid, new(testpb.TestBye))
		require.NoError(t, err)

		time.Sleep(time.Second)
		assert.False(t, pid.IsRunning())
	})
	t.Run("With happy path Tell", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create actor2
		actor2 := &Exchanger{}
		actorPath2 := NewPath("Exchange2", NewAddress("sys", "host", 1))
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		op := func() {
			context.Tell(pid2, new(testpb.TestSend))
		}
		assert.NotPanics(t, op)

		time.Sleep(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
	})
	t.Run("With panic Tell", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create actor2
		actor2 := &Exchanger{}
		actorPath2 := NewPath("Exchange2", NewAddress("sys", "host", 1))
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// wait a while and shutdown actor2
		time.Sleep(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))

		op := func() {
			context.Tell(pid2, new(testpb.TestSend))
		}
		assert.Panics(t, op)

		time.Sleep(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
	})
	t.Run("With happy path Ask", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create actor2
		actor2 := &Exchanger{}
		actorPath2 := NewPath("Exchange2", NewAddress("sys", "host", 1))
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		reply := context.Ask(pid2, new(testpb.TestReply))
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, reply))

		time.Sleep(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
	})
	t.Run("With panic Ask", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create actor2
		actor2 := &Exchanger{}
		actorPath2 := NewPath("Exchange2", NewAddress("sys", "host", 1))
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// wait a while and shutdown actor2
		time.Sleep(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))

		op := func() {
			context.Ask(pid2, new(testpb.TestReply))
		}
		assert.Panics(t, op)

		time.Sleep(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
	})
	t.Run("With happy path RemoteAsk", func(t *testing.T) {
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

		// create an exchanger two
		actorName2 := "Exchange2"
		actorRef2, err := sys.Spawn(ctx, actorName2, &Exchanger{})
		require.NoError(t, err)
		assert.NotNil(t, actorRef2)

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx,
			actorPath1,
			actor1,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// get the address of the exchanger actor one
		addr1 := context.RemoteLookup(host, remotingPort, actorName2)
		// send the message to t exchanger actor one using remote messaging
		reply := context.RemoteAsk(addr1, new(testpb.TestReply))
		// perform some assertions
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		time.Sleep(time.Second)

		t.Cleanup(func() {
			assert.NoError(t, pid1.Shutdown(ctx))
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With panic RemoteAsk", func(t *testing.T) {
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

		// create an exchanger two
		actorName2 := "Exchange2"

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx,
			actorPath1,
			actor1,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		op := func() {
			context.RemoteAsk(&addresspb.Address{
				Host: "localhost",
				Port: int32(remotingPort),
				Name: actorName2,
				Id:   "",
			}, new(testpb.TestReply))
		}

		assert.Panics(t, op)
		time.Sleep(time.Second)

		t.Cleanup(func() {
			assert.NoError(t, pid1.Shutdown(ctx))
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With happy path RemoteTell", func(t *testing.T) {
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

		// create an exchanger two
		actorName2 := "Exchange2"
		actorRef2, err := sys.Spawn(ctx, actorName2, &Exchanger{})

		require.NoError(t, err)
		assert.NotNil(t, actorRef2)

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx,
			actorPath1,
			actor1,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// get the address of the exchanger actor one
		addr1 := context.RemoteLookup(host, remotingPort, actorName2)
		// send the message to t exchanger actor one using remote messaging
		assert.NotPanics(t, func() {
			context.RemoteTell(addr1, new(testpb.TestRemoteSend))
		})

		time.Sleep(time.Second)

		t.Cleanup(func() {
			assert.NoError(t, pid1.Shutdown(ctx))
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With panic RemoteTell", func(t *testing.T) {
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

		// create an exchanger two
		actorName2 := "Exchange2"

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx,
			actorPath1,
			actor1,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// send the message to the exchanger actor one using remote messaging
		assert.Panics(t, func() {
			context.RemoteTell(&addresspb.Address{
				Host: "localhost",
				Port: int32(remotingPort),
				Name: actorName2,
				Id:   "",
			}, new(testpb.TestRemoteSend))
		})

		time.Sleep(time.Second)

		t.Cleanup(func() {
			assert.NoError(t, pid1.Shutdown(ctx))
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With address not found RemoteLookup", func(t *testing.T) {
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

		// create an exchanger two
		actorName2 := "Exchange2"

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx,
			actorPath1,
			actor1,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		require.Nil(t, context.RemoteLookup(host, remotingPort, actorName2))

		time.Sleep(time.Second)

		t.Cleanup(func() {
			assert.NoError(t, pid1.Shutdown(ctx))
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With panic RemoteLookup", func(t *testing.T) {
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
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger two
		actorName2 := "Exchange2"

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx,
			actorPath1,
			actor1,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		assert.Panics(t, func() {
			context.RemoteLookup(host, remotingPort, actorName2)
		})

		time.Sleep(time.Second)

		t.Cleanup(func() {
			assert.NoError(t, pid1.Shutdown(ctx))
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With happy path Shutdown", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		assert.NotPanics(t, func() {
			context.Shutdown()
		})
	})
	t.Run("With happy path SpawnChild", func(t *testing.T) {
		ctx := context.TODO()
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      parent,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create the child actor
		name := "monitored"
		child := context.Spawn(name, NewMonitored())
		assert.NotNil(t, child)
		assert.Len(t, context.Children(), 1)

		actual := context.Child(name)
		require.NotNil(t, actual)
		assert.Equal(t, child.ActorPath().String(), actual.ActorPath().String())

		t.Cleanup(func() {
			context.Shutdown()
		})
	})
	t.Run("With panic SpawnChild", func(t *testing.T) {
		ctx := context.TODO()
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      parent,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// stop the actor
		context.Shutdown()

		// create the child actor
		assert.Panics(t, func() {
			context.Spawn("SpawnChild", NewMonitored())
		})
	})
	t.Run("With not found Child", func(t *testing.T) {
		ctx := context.TODO()
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      parent,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create the child actor
		name := "monitored"
		child := context.Spawn(name, NewMonitored())
		assert.NotNil(t, child)
		assert.Len(t, context.Children(), 1)

		// stop the child
		require.NoError(t, child.Shutdown(ctx))

		assert.Panics(t, func() {
			context.Child(name)
		})

		t.Cleanup(func() {
			context.Shutdown()
		})
	})
	t.Run("With dead parent Child", func(t *testing.T) {
		ctx := context.TODO()
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      parent,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create the child actor
		name := "monitored"
		child := context.Spawn(name, NewMonitored())
		assert.NotNil(t, child)
		assert.Len(t, context.Children(), 1)

		// stop the parent
		context.Shutdown()

		assert.Panics(t, func() {
			context.Child(name)
		})

		t.Cleanup(func() {
			require.NoError(t, child.Shutdown(ctx))
		})
	})
	t.Run("With happy path Stop", func(t *testing.T) {
		ctx := context.TODO()
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      parent,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create the child actor
		name := "monitored"
		child := context.Spawn(name, NewMonitored())
		assert.NotNil(t, child)
		assert.Len(t, context.Children(), 1)

		// stop the child actor
		assert.NotPanics(t, func() {
			context.Stop(child)
		})

		time.Sleep(time.Second)
		assert.Empty(t, context.Children())
		t.Cleanup(func() {
			context.Shutdown()
		})
	})
	t.Run("With child actor Stop freeing up parent link", func(t *testing.T) {
		ctx := context.TODO()
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      parent,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create the child actor
		name := "monitored"
		child := context.Spawn(name, NewMonitored())
		assert.NotNil(t, child)
		assert.Len(t, context.Children(), 1)

		// let us stop the child actor
		require.NoError(t, child.Shutdown(ctx))

		time.Sleep(time.Second)

		assert.Empty(t, context.Children())

		time.Sleep(time.Second)
		assert.Empty(t, context.Children())
		t.Cleanup(func() {
			context.Shutdown()
		})
	})
	t.Run("With panic Stop: child not defined", func(t *testing.T) {
		ctx := context.TODO()
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      parent,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		time.Sleep(time.Second)
		// stop the child actor
		assert.Panics(t, func() {
			context.Stop(NoSender)
		})

		t.Cleanup(func() {
			context.Shutdown()
		})
	})
	t.Run("With panic Stop: parent is dead", func(t *testing.T) {
		ctx := context.TODO()
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      parent,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		name := "monitored"
		child := context.Spawn(name, NewMonitored())
		assert.NotNil(t, child)
		assert.Len(t, context.Children(), 1)

		time.Sleep(time.Second)

		context.Shutdown()

		time.Sleep(time.Second)

		// stop the child actor
		assert.Panics(t, func() {
			context.Stop(child)
		})
	})
	t.Run("With panic Stop: actor not found", func(t *testing.T) {
		ctx := context.TODO()
		actorPath := NewPath("parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      parent,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create the child actor
		childPath := NewPath("child", NewAddress("sys", "host", 1))
		child, err := newPID(ctx, childPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)

		// stop the child actor
		assert.Panics(t, func() {
			context.Stop(child)
		})

		t.Cleanup(func() {
			context.Shutdown()
			assert.NoError(t, child.Shutdown(ctx))
		})
	})
	t.Run("With Stop when child is already stopped", func(t *testing.T) {
		ctx := context.TODO()
		actorPath := NewPath("Parent", NewAddress("sys", "host", 1))

		// create the parent actor
		parent, err := newPID(ctx, actorPath,
			NewMonitor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(receivingTimeout))

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      parent,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create the child actor
		name := "monitored"
		child := context.Spawn(name, NewMonitored())
		assert.NotNil(t, child)
		assert.Len(t, context.Children(), 1)

		// stop the child
		assert.NoError(t, child.Shutdown(ctx))
		// stop the child actor
		assert.Panics(t, func() {
			context.Stop(child)
		})

		time.Sleep(time.Second)
		assert.Empty(t, context.Children())
		t.Cleanup(func() {
			context.Shutdown()
		})
	})
	t.Run("With panic Shutdown", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create actor1
		actor1 := &PostStopBreaker{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		assert.Panics(t, func() {
			context.Shutdown()
		})
	})
	t.Run("With happy Forward", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create actorA
		actorA := &Exchanger{}
		actorPath1 := NewPath("actorA", NewAddress("sys", "host", 1))
		pidA, err := newPID(ctx, actorPath1, actorA, opts...)
		require.NoError(t, err)
		require.NotNil(t, pidA)

		// create actorC
		actorC := &Exchanger{}
		actorPath3 := NewPath("actorC", NewAddress("sys", "host", 1))
		pidC, err := newPID(ctx, actorPath3, actorC, opts...)
		require.NoError(t, err)
		require.NotNil(t, pidC)

		// create actorB
		actorB := &Forwarder{
			actorRef: pidC,
		}
		actorPath2 := NewPath("actorB", NewAddress("sys", "host", 1))
		pidB, err := newPID(ctx, actorPath2, actorB, opts...)
		require.NoError(t, err)
		require.NotNil(t, pidB)

		// actor A is killing actor C using a forward pattern
		// actorA tell actorB forward actorC
		die := new(testpb.TestBye)
		err = pidA.Tell(ctx, pidB, die)
		require.NoError(t, err)

		// wait for the async call to properly complete
		time.Sleep(time.Second)
		require.True(t, pidA.IsRunning())
		require.True(t, pidB.IsRunning())
		require.False(t, pidC.IsRunning())

		// let us shutdown the rest
		require.NoError(t, pidA.Shutdown(ctx))
		require.NoError(t, pidB.Shutdown(ctx))
	})
	t.Run("With Unhandled with no sender", func(t *testing.T) {
		ctx := context.TODO()
		// create the deadletter stream
		eventsStream := eventstream.New()

		// create a consumer
		consumer := eventsStream.AddSubscriber()
		eventsStream.Subscribe(consumer, eventsTopic)

		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withEventsStream(eventsStream),
		}

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		send := new(testpb.TestSend)
		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        send,
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// calling unhandled will push the current message to deadletters
		context.Unhandled()

		// wait for messages to be published
		time.Sleep(time.Second)

		var items []*eventspb.Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			assert.Equal(t, eventsTopic, message.Topic())
			deadletter, ok := payload.(*eventspb.Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		require.Len(t, items, 1)
		deadletter := items[0]
		msg := deadletter.GetMessage()
		actual := new(testpb.TestSend)
		require.NoError(t, msg.UnmarshalTo(actual))
		require.True(t, proto.Equal(send, actual))
		require.Equal(t, deadletter.GetReason(), ErrUnhandled.Error())
		assert.Nil(t, deadletter.GetSender())

		assert.EqualValues(t, 1, len(consumer.Topics()))

		t.Cleanup(func() {
			// shutdown the consumer
			consumer.Shutdown()
			context.Shutdown()
		})
	})
	t.Run("With Unhandled with a sender", func(t *testing.T) {
		ctx := context.TODO()
		// create the deadletter stream
		eventsStream := eventstream.New()

		// create a consumer
		consumer := eventsStream.AddSubscriber()
		eventsStream.Subscribe(consumer, eventsTopic)

		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withEventsStream(eventsStream),
		}

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		actor2 := &Exchanger{}
		actorPath2 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)

		send := new(testpb.TestSend)
		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        send,
			sender:         pid2,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// calling unhandled will push the current message to deadletters
		context.Unhandled()

		// wait for messages to be published
		time.Sleep(time.Second)

		var items []*eventspb.Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			deadletter, ok := payload.(*eventspb.Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		require.Len(t, items, 1)
		deadletter := items[0]
		msg := deadletter.GetMessage()
		actual := new(testpb.TestSend)
		require.NoError(t, msg.UnmarshalTo(actual))
		require.True(t, proto.Equal(send, actual))
		require.Equal(t, deadletter.GetReason(), ErrUnhandled.Error())
		assert.True(t, proto.Equal(deadletter.GetSender(), actorPath2.RemoteAddress()))

		assert.EqualValues(t, 1, len(consumer.Topics()))

		t.Cleanup(func() {
			require.NoError(t, pid2.Shutdown(ctx))
			// shutdown the consumer
			consumer.Shutdown()
			context.Shutdown()
		})
	})
	t.Run("With happy BatchTell", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create actor2
		actor2 := &Exchanger{}
		actorPath2 := NewPath("Exchange2", NewAddress("sys", "host", 1))
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		op := func() {
			context.BatchTell(pid2, new(testpb.TestSend), new(testpb.TestSend))
		}
		assert.NotPanics(t, op)

		time.Sleep(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
	})
	t.Run("With happy BatchTell as a Tell", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create actor2
		actor2 := &Exchanger{}
		actorPath2 := NewPath("Exchange2", NewAddress("sys", "host", 1))
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		op := func() {
			context.BatchTell(pid2, new(testpb.TestSend))
		}
		assert.NotPanics(t, op)

		time.Sleep(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
	})
	t.Run("With panic BatchTell", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create actor2
		actor2 := &Exchanger{}
		actorPath2 := NewPath("Exchange2", NewAddress("sys", "host", 1))
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// wait a while and shutdown actor2
		time.Sleep(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))

		op := func() {
			context.BatchTell(pid2, new(testpb.TestSend), new(testpb.TestSend))
		}
		assert.Panics(t, op)

		time.Sleep(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
	})
	t.Run("With happy BatchAsk", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create actor2
		actor2 := &Exchanger{}
		actorPath2 := NewPath("Exchange2", NewAddress("sys", "host", 1))
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		replies := context.BatchAsk(pid2, new(testpb.TestReply), new(testpb.TestReply))
		require.NotNil(t, replies)
		require.Len(t, replies, 2)
		for reply := range replies {
			expected := new(testpb.Reply)
			assert.True(t, proto.Equal(expected, reply))
		}

		time.Sleep(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
	})
	t.Run("With panic BatchAsk", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx, actorPath1, actor1, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// create actor2
		actor2 := &Exchanger{}
		actorPath2 := NewPath("Exchange2", NewAddress("sys", "host", 1))
		pid2, err := newPID(ctx, actorPath2, actor2, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// wait a while and shutdown actor2
		time.Sleep(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))

		op := func() {
			context.BatchAsk(pid2, new(testpb.TestReply), new(testpb.TestReply))
		}
		assert.Panics(t, op)

		time.Sleep(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
	})
	t.Run("With happy RemoteBatchTell", func(t *testing.T) {
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

		// create test actor
		tester := "test"
		testActor := NewTester()
		testerRef, err := sys.Spawn(ctx, tester, testActor)
		require.NoError(t, err)
		require.NotNil(t, testerRef)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			sender:         NoSender,
			recipient:      testerRef,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// get the address of the exchanger actor one
		testerAddr := context.RemoteLookup(host, remotingPort, tester)
		// send the message to t exchanger actor one using remote messaging
		assert.NotPanics(t, func() {
			context.RemoteBatchTell(testerAddr, new(testpb.TestSend), new(testpb.TestSend), new(testpb.TestSend))
		})

		// wait for processing to complete on the actor side
		time.Sleep(500 * time.Millisecond)
		require.EqualValues(t, 3, testActor.counter.Load())

		time.Sleep(time.Second)

		t.Cleanup(func() {
			assert.NoError(t, testerRef.Shutdown(ctx))
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With happy RemoteBatchAsk", func(t *testing.T) {
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

		// create test actor
		tester := "test"
		testActor := NewTester()
		testerRef, err := sys.Spawn(ctx, tester, testActor)
		require.NoError(t, err)
		require.NotNil(t, testerRef)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			sender:         NoSender,
			recipient:      testerRef,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// get the address of the exchanger actor one
		testerAddr := context.RemoteLookup(host, remotingPort, tester)
		// send the message to t exchanger actor one using remote messaging
		var replies []*anypb.Any
		assert.NotPanics(t, func() {
			replies = context.RemoteBatchAsk(testerAddr, new(testpb.TestReply), new(testpb.TestReply), new(testpb.TestReply))
		})
		require.Len(t, replies, 3)
		time.Sleep(time.Second)

		t.Cleanup(func() {
			assert.NoError(t, testerRef.Shutdown(ctx))
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With panic RemoteBatchTell", func(t *testing.T) {
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

		// create an exchanger two
		actorName2 := "Exchange2"

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx,
			actorPath1,
			actor1,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		// send the message to the exchanger actor one using remote messaging
		assert.Panics(t, func() {
			context.RemoteBatchTell(&addresspb.Address{
				Host: "localhost",
				Port: int32(remotingPort),
				Name: actorName2,
				Id:   "",
			}, new(testpb.TestRemoteSend))
		})

		time.Sleep(time.Second)

		t.Cleanup(func() {
			assert.NoError(t, pid1.Shutdown(ctx))
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With panic RemoteBatchAsk", func(t *testing.T) {
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

		// create an exchanger two
		actorName2 := "Exchange2"

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1, err := newPID(ctx,
			actorPath1,
			actor1,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid1,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		op := func() {
			context.RemoteBatchAsk(&addresspb.Address{
				Host: "localhost",
				Port: int32(remotingPort),
				Name: actorName2,
				Id:   "",
			}, new(testpb.TestReply))
		}

		assert.Panics(t, op)
		time.Sleep(time.Second)

		t.Cleanup(func() {
			assert.NoError(t, pid1.Shutdown(ctx))
			assert.NoError(t, sys.Stop(ctx))
		})
	})
}
