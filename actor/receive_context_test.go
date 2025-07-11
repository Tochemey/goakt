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
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func TestReceiveContext(t *testing.T) {
	t.Run("With behaviors handling", func(t *testing.T) {
		ctx := context.TODO()
		// create the actor path
		actor := &MockBehavior{}
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		pid, err := actorSystem.Spawn(ctx, "User", actor)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// send Login
		var expected proto.Message
		success, err := Ask(ctx, pid, new(testpb.TestLogin), replyTimeout)
		require.NoError(t, err)
		require.NotNil(t, success)
		expected = &testpb.TestLoginSuccess{}
		require.True(t, proto.Equal(expected, success))

		// ask for readiness
		ready, err := Ask(ctx, pid, new(testpb.TestReadiness), replyTimeout)
		require.NoError(t, err)
		require.NotNil(t, ready)
		expected = &testpb.TestReady{}
		require.True(t, proto.Equal(expected, ready))

		// send a message to create account
		created, err := Ask(ctx, pid, new(testpb.CreateAccount), replyTimeout)
		require.NoError(t, err)
		require.NotNil(t, created)
		expected = &testpb.AccountCreated{}
		require.True(t, proto.Equal(expected, created))

		// credit account
		credited, err := Ask(ctx, pid, new(testpb.CreditAccount), replyTimeout)
		require.NoError(t, err)
		require.NotNil(t, credited)
		expected = &testpb.AccountCredited{}
		require.True(t, proto.Equal(expected, credited))

		// debit account
		debited, err := Ask(ctx, pid, new(testpb.DebitAccount), replyTimeout)
		require.NoError(t, err)
		require.NotNil(t, debited)
		expected = &testpb.AccountDebited{}
		require.True(t, proto.Equal(expected, debited))

		// send bye
		err = Tell(ctx, pid, new(testpb.TestBye))
		require.NoError(t, err)

		pause.For(time.Second)
		assert.False(t, pid.IsRunning())
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With successful Tell command", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		context.Tell(pid2, new(testpb.TestSend))
		require.NoError(t, context.getError())

		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With failed Tell", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// wait a while and shutdown actor2
		pause.For(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))
		context.Tell(pid2, new(testpb.TestSend))
		require.Error(t, context.getError())

		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With successful Ask command", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		reply := context.Ask(pid2, new(testpb.TestReply), time.Minute)
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, reply))

		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With failed Ask", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// wait a while and shutdown actor2
		pause.For(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))

		context.Ask(pid2, new(testpb.TestReply), time.Minute)
		require.Error(t, context.getError())

		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With successful RemoteAsk", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger two
		actorName2 := "Exchange2"
		actorRef2, err := sys.Spawn(ctx, actorName2, &exchanger{})
		require.NoError(t, err)
		assert.NotNil(t, actorRef2)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := sys.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// get the address of the exchanger actor one
		addr1 := context.RemoteLookup(host, remotingPort, actorName2)
		// send the message to t exchanger actor one using remote messaging
		reply := context.RemoteAsk(address.From(addr1), new(testpb.TestReply), time.Minute)
		// perform some assertions
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		pause.For(time.Second)

		t.Cleanup(
			func() {
				assert.NoError(t, pid1.Shutdown(ctx))
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With failed RemoteAsk", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger two
		actorName2 := "Exchange2"

		// create actor1
		actor1 := &exchanger{}
		pid1, err := sys.Spawn(ctx, "Exchange1", actor1)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		context.RemoteAsk(address.From(
			&goaktpb.Address{
				Host: "127.0.0.1",
				Port: int32(remotingPort),
				Name: actorName2,
				Id:   "",
			},
		), new(testpb.TestReply), time.Minute)
		require.Error(t, context.getError())
		pause.For(time.Second)

		t.Cleanup(
			func() {
				assert.NoError(t, pid1.Shutdown(ctx))
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With successful RemoteTell", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(2)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger two
		actorName2 := "Exchange2"
		actorRef2, err := sys.Spawn(ctx, actorName2, &exchanger{})

		require.NoError(t, err)
		assert.NotNil(t, actorRef2)

		// create actor1
		actor1 := &exchanger{}
		sys2, err := NewActorSystem("sys", WithLogger(logger),
			WithRemote(remote.NewConfig(host, nodePorts[1])))

		require.NoError(t, err)
		require.NoError(t, sys2.Start(ctx))

		pause.For(time.Second)

		pid1, err := sys2.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// get the address of the exchanger actor one
		addr1 := context.RemoteLookup(host, remotingPort, actorName2)
		// send the message to t exchanger actor one using remote messaging
		context.RemoteTell(address.From(addr1), new(testpb.TestRemoteSend))
		require.NoError(t, context.getError())
		pause.For(time.Second)

		t.Cleanup(
			func() {
				assert.NoError(t, pid1.Shutdown(ctx))
				assert.NoError(t, sys.Stop(ctx))
				assert.NoError(t, sys2.Stop(ctx))
			},
		)
	})
	t.Run("With failed RemoteTell", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger two
		actorName2 := "Exchange2"

		// create actor1
		actor1 := &exchanger{}
		ports := dynaport.Get(1)
		sys2, err := NewActorSystem("sys", WithLogger(logger),
			WithRemote(remote.NewConfig(host, ports[0])))
		require.NoError(t, err)
		require.NoError(t, sys2.Start(ctx))
		pause.For(time.Second)

		pid1, err := sys2.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// send the message to the exchanger actor one using remote messaging
		context.RemoteTell(address.From(
			&goaktpb.Address{
				Host: "127.0.0.1",
				Port: int32(remotingPort),
				Name: actorName2,
				Id:   "",
			},
		), new(testpb.TestRemoteSend))
		require.Error(t, context.getError())
		pause.For(time.Second)

		t.Cleanup(
			func() {
				assert.NoError(t, pid1.Shutdown(ctx))
				assert.NoError(t, sys.Stop(ctx))
				assert.NoError(t, sys2.Stop(ctx))
			},
		)
	})
	t.Run("With address not found RemoteLookup", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger two
		actorName2 := "Exchange2"

		// create actor1
		actor1 := &exchanger{}
		ports := dynaport.Get(1)
		sys2, err := NewActorSystem("sys", WithLogger(logger),
			WithRemote(remote.NewConfig(host, ports[0])))
		require.NoError(t, err)
		require.NoError(t, sys2.Start(ctx))
		pause.For(time.Second)

		pid1, err := sys2.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		require.Nil(t, context.RemoteLookup(host, remotingPort, actorName2))

		pause.For(time.Second)

		t.Cleanup(
			func() {
				assert.NoError(t, pid1.Shutdown(ctx))
				assert.NoError(t, sys.Stop(ctx))
				assert.NoError(t, sys2.Stop(ctx))
			},
		)
	})
	t.Run("With failed RemoteLookup", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger two
		actorName2 := "Exchange2"

		// create actor1
		actor1 := &exchanger{}
		ports := dynaport.Get(1)

		sys2, err := NewActorSystem("sys", WithLogger(logger),
			WithRemote(remote.NewConfig(host, ports[0])))
		require.NoError(t, err)
		require.NoError(t, sys2.Start(ctx))
		pause.For(time.Second)

		pid1, err := sys2.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}
		context.RemoteLookup(host, remotingPort, actorName2)
		require.Error(t, context.getError())
		pause.For(time.Second)

		t.Cleanup(
			func() {
				assert.NoError(t, pid1.Shutdown(ctx))
				assert.NoError(t, sys.Stop(ctx))
				assert.NoError(t, sys2.Stop(ctx))
			},
		)
	})
	t.Run("With successful Shutdown", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		context.Shutdown()
		require.NoError(t, context.getError())
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With successful SpawnChild", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the parent actor
		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())
		require.NoError(t, err)
		assert.NotNil(t, parent)

		pause.For(time.Second)

		// create an instance of receive receiveCtx
		receiveCtx := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    parent,
		}

		// create the child actor
		name := "monitored"
		child := receiveCtx.Spawn(name, NewMockSupervised())
		assert.NotNil(t, child)
		assert.Len(t, receiveCtx.Children(), 1)

		actual := receiveCtx.Child(name)
		require.NotNil(t, actual)
		assert.Equal(t, child.Address().String(), actual.Address().String())

		t.Cleanup(
			func() {
				receiveCtx.Shutdown()
				assert.NoError(t, actorSystem.Stop(ctx))
			},
		)
	})
	t.Run("With failed SpawnChild", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the parent actor
		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())
		require.NoError(t, err)
		assert.NotNil(t, parent)

		pause.For(time.Second)

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    parent,
		}

		// stop the actor
		context.Shutdown()

		// create the child actor
		context.Spawn("SpawnChild", NewMockSupervised())
		require.Error(t, context.getError())
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With not found Child", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the parent actor
		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())
		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    parent,
		}

		// create the child actor
		name := "monitored"
		child := context.Spawn(name, NewMockSupervised())
		assert.NotNil(t, child)
		assert.Len(t, context.Children(), 1)

		// stop the child
		require.NoError(t, child.Shutdown(ctx))

		context.Child(name)
		require.Error(t, context.getError())
		t.Cleanup(
			func() {
				context.Shutdown()
				assert.NoError(t, actorSystem.Stop(ctx))
			},
		)
	})
	t.Run("With dead parent Child", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the parent actor
		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    parent,
		}

		// create the child actor
		name := "monitored"
		child := context.Spawn(name, NewMockSupervised())
		assert.NotNil(t, child)
		assert.Len(t, context.Children(), 1)

		// stop the parent
		context.Shutdown()

		context.Child(name)
		require.Error(t, context.getError())
		t.Cleanup(
			func() {
				require.NoError(t, child.Shutdown(ctx))
				assert.NoError(t, actorSystem.Stop(ctx))
			},
		)
	})
	t.Run("With successful Stop", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)
		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    parent,
		}

		// create the child actor
		name := "monitored"
		child := context.Spawn(name, NewMockSupervised())
		assert.NotNil(t, child)
		assert.Len(t, context.Children(), 1)

		// stop the child actor
		context.Stop(child)
		require.NoError(t, context.getError())
		pause.For(time.Second)
		assert.Empty(t, context.Children())
		t.Cleanup(
			func() {
				context.Shutdown()
				assert.NoError(t, actorSystem.Stop(ctx))
			},
		)
	})
	t.Run("With child actor Stop freeing up parent link", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    parent,
		}

		// create the child actor
		name := "monitored"
		child := context.Spawn(name, NewMockSupervised())
		assert.NotNil(t, child)
		assert.Len(t, context.Children(), 1)

		// let us stop the child actor
		require.NoError(t, child.Shutdown(ctx))

		pause.For(time.Second)

		assert.Empty(t, context.Children())

		pause.For(time.Second)
		assert.Empty(t, context.Children())
		t.Cleanup(
			func() {
				context.Shutdown()
				assert.NoError(t, actorSystem.Stop(ctx))
			},
		)
	})
	t.Run("With failed Stop: child not defined", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    parent,
		}

		pause.For(time.Second)
		// stop the child actor
		context.Stop(NoSender)
		require.Error(t, context.getError())
		t.Cleanup(
			func() {
				context.Shutdown()
				assert.NoError(t, actorSystem.Stop(ctx))
			},
		)
	})
	t.Run("With failed Stop: parent is dead", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    parent,
		}

		name := "monitored"
		child := context.Spawn(name, NewMockSupervised())
		assert.NotNil(t, child)
		assert.Len(t, context.Children(), 1)

		pause.For(time.Second)

		context.Shutdown()

		pause.For(time.Second)

		// stop the child actor
		context.Stop(child)
		require.Error(t, context.getError())
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With failed Stop: actor not found", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())
		require.NoError(t, err)
		assert.NotNil(t, parent)

		workerPool := actorSystem.getWorkerPool()

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    parent,
		}

		// create the child actor
		childPath := address.New("child", "sys", "127.0.0.1", ports[0])
		child, err := newPID(
			ctx, childPath,
			NewMockSupervisor(),
			withInitMaxRetries(1),
			withActorSystem(actorSystem),
			withWorkerPool(workerPool),
			withCustomLogger(log.DiscardLogger),
		)

		require.NoError(t, err)

		// stop the child actor
		context.Stop(child)
		require.Error(t, context.getError())
		t.Cleanup(
			func() {
				context.Shutdown()
				assert.NoError(t, child.Shutdown(ctx))
				assert.NoError(t, actorSystem.Stop(ctx))
			},
		)
	})
	t.Run("With Stop when child is already stopped", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())

		require.NoError(t, err)
		assert.NotNil(t, parent)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    parent,
		}

		// create the child actor
		name := "monitored"
		child := context.Spawn(name, NewMockSupervised())
		assert.NotNil(t, child)
		assert.Len(t, context.Children(), 1)

		// stop the child
		assert.NoError(t, child.Shutdown(ctx))
		// stop the child actor
		context.Stop(child)
		require.Error(t, context.getError())
		pause.For(time.Second)
		assert.Empty(t, context.Children())
		t.Cleanup(
			func() {
				context.Shutdown()
				assert.NoError(t, actorSystem.Stop(ctx))
			},
		)
	})
	t.Run("With failed Shutdown", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		actor1 := &MockPostStop{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		context.Shutdown()
		require.Error(t, context.getError())
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With successful Forward", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actorA
		actorA := &exchanger{}
		pidA, err := actorSystem.Spawn(ctx, "actorA", actorA)
		require.NoError(t, err)
		require.NotNil(t, pidA)

		// create actorC
		actorC := &exchanger{}
		pidC, err := actorSystem.Spawn(ctx, "actorC", actorC)
		require.NoError(t, err)
		require.NotNil(t, pidC)

		// create actorB
		actorB := &MockForward{
			actorRef: pidC,
		}

		pidB, err := actorSystem.Spawn(ctx, "actorB", actorB)
		require.NoError(t, err)
		require.NotNil(t, pidB)

		// actor A is killing actor C using a forward pattern
		// actorA tell actorB forward actorC
		die := new(testpb.TestBye)
		err = pidA.Tell(ctx, pidB, die)
		require.NoError(t, err)

		// wait for the async call to properly complete
		pause.For(time.Second)
		require.True(t, pidA.IsRunning())
		require.True(t, pidB.IsRunning())
		require.False(t, pidC.IsRunning())

		// let us shutdown the rest
		require.NoError(t, pidA.Shutdown(ctx))
		require.NoError(t, pidB.Shutdown(ctx))
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With Unhandled with no sender", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create a consumer
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		send := new(testpb.TestSend)
		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: send,
			sender:  NoSender,
			self:    pid1,
		}

		// calling unhandled will push the current message to deadletter
		context.Unhandled()

		// wait for messages to be published
		pause.For(time.Second)

		var items []*goaktpb.Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			assert.Equal(t, eventsTopic, message.Topic())
			deadletter, ok := payload.(*goaktpb.Deadletter)
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
		addr := deadletter.GetSender()
		require.True(t, NoSender.Address().Equals(address.From(addr)))

		assert.EqualValues(t, 1, len(consumer.Topics()))

		t.Cleanup(
			func() {
				// shutdown the consumer
				consumer.Shutdown()
				context.Shutdown()
				assert.NoError(t, actorSystem.Stop(ctx))
			},
		)
	})
	t.Run("With Unhandled with a sender", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create a consumer
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		actor2 := &exchanger{}
		// nolint
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)

		send := new(testpb.TestSend)
		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: send,
			sender:  pid2,
			self:    pid1,
		}

		// calling unhandled will push the current message to deadletter
		context.Unhandled()

		// wait for messages to be published
		pause.For(time.Second)

		var items []*goaktpb.Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			deadletter, ok := payload.(*goaktpb.Deadletter)
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
		assert.True(t, proto.Equal(deadletter.GetSender(), pid2.Address().Address))

		assert.EqualValues(t, 1, len(consumer.Topics()))

		require.NoError(t, pid2.Shutdown(ctx))
		// shutdown the consumer
		consumer.Shutdown()
		context.Shutdown()
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With Unhandled with system messages", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create a consumer
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(goaktpb.PostStart),
			sender:  NoSender,
			self:    pid1,
		}

		// calling unhandled will push the current message to deadletter
		context.Unhandled()

		// wait for messages to be published
		pause.For(time.Second)

		var items []*goaktpb.Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			assert.Equal(t, eventsTopic, message.Topic())
			deadletter, ok := payload.(*goaktpb.Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		require.Empty(t, items)

		assert.EqualValues(t, 1, len(consumer.Topics()))

		t.Cleanup(
			func() {
				// shutdown the consumer
				consumer.Shutdown()
				context.Shutdown()
				assert.NoError(t, actorSystem.Stop(ctx))
			},
		)
	})
	t.Run("With successful BatchTell", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		context.BatchTell(pid2, new(testpb.TestSend), new(testpb.TestSend))
		require.NoError(t, context.getError())

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With successful BatchTell as a Tell", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}

		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		context.BatchTell(pid2, new(testpb.TestSend))
		require.NoError(t, context.getError())

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With failed BatchTell", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// wait a while and shutdown actor2
		pause.For(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))

		context.BatchTell(pid2, new(testpb.TestSend), new(testpb.TestSend))
		require.Error(t, context.getError())

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With successful BatchAsk", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		replies := context.BatchAsk(pid2, []proto.Message{new(testpb.TestReply), new(testpb.TestReply)}, time.Minute)
		require.NotNil(t, replies)
		require.Len(t, replies, 2)
		for reply := range replies {
			expected := new(testpb.Reply)
			assert.True(t, proto.Equal(expected, reply))
		}

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With failed BatchAsk", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}

		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// wait a while and shutdown actor2
		pause.For(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))

		context.BatchAsk(pid2, []proto.Message{new(testpb.TestReply), new(testpb.TestReply)}, time.Minute)
		require.Error(t, context.getError())

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With successful RemoteBatchTell", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create test actor
		tester := "test"
		testActor := NewMockActor()
		testerRef, err := sys.Spawn(ctx, tester, testActor)
		require.NoError(t, err)
		require.NotNil(t, testerRef)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:    ctx,
			sender: NoSender,
			self:   testerRef,
		}

		// get the address of the exchanger actor one
		testerAddr := context.RemoteLookup(host, remotingPort, tester)
		// send the message to t exchanger actor one using remote messaging
		messages := []proto.Message{new(testpb.TestSend), new(testpb.TestSend), new(testpb.TestSend)}
		context.RemoteBatchTell(address.From(testerAddr), messages)
		require.NoError(t, context.getError())
		// wait for processing to complete on the actor side
		pause.For(500 * time.Millisecond)
		require.EqualValues(t, 3, testerRef.ProcessedCount()-1)

		pause.For(time.Second)

		t.Cleanup(
			func() {
				assert.NoError(t, testerRef.Shutdown(ctx))
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With successful RemoteBatchAsk", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create test actor
		tester := "test"
		testActor := NewMockActor()
		testerRef, err := sys.Spawn(ctx, tester, testActor)
		require.NoError(t, err)
		require.NotNil(t, testerRef)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:    ctx,
			sender: NoSender,
			self:   testerRef,
		}

		// get the address of the exchanger actor one
		testerAddr := context.RemoteLookup(host, remotingPort, tester)
		// send the message to t exchanger actor one using remote messaging
		messages := []proto.Message{new(testpb.TestReply), new(testpb.TestReply), new(testpb.TestReply)}
		replies := context.RemoteBatchAsk(address.From(testerAddr), messages, time.Minute)
		require.NoError(t, context.getError())
		require.Len(t, replies, 3)
		pause.For(time.Second)

		t.Cleanup(
			func() {
				assert.NoError(t, testerRef.Shutdown(ctx))
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With RemoteBatchAsk when remoting is not enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create test actor
		tester := "test"
		testActor := NewMockActor()
		testerRef, err := sys.Spawn(ctx, tester, testActor)
		require.NoError(t, err)
		require.NotNil(t, testerRef)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:    ctx,
			sender: NoSender,
			self:   testerRef,
		}

		testerRef.remoting = nil
		// get the address of the exchanger actor one
		testerAddr := context.RemoteLookup(host, remotingPort, tester)
		// send the message to t exchanger actor one using remote messaging
		messages := []proto.Message{new(testpb.TestReply), new(testpb.TestReply), new(testpb.TestReply)}
		replies := context.RemoteBatchAsk(address.From(testerAddr), messages, time.Minute)
		err = context.getError()
		require.Error(t, err)
		require.Empty(t, replies)
		assert.ErrorIs(t, err, ErrRemotingDisabled)

		t.Cleanup(
			func() {
				assert.NoError(t, testerRef.Shutdown(ctx))
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With failed RemoteBatchTell", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger two
		actorName2 := "Exchange2"

		// create actor1
		actor1 := &exchanger{}
		pid1, err := sys.Spawn(ctx, "Exchange1", actor1)

		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// send the message to the exchanger actor one using remote messaging
		context.RemoteBatchTell(address.From(
			&goaktpb.Address{
				Host: "127.0.0.1",
				Port: int32(remotingPort),
				Name: actorName2,
				Id:   "",
			},
		), []proto.Message{new(testpb.TestRemoteSend)})
		require.Error(t, context.getError())
		pause.For(time.Second)

		t.Cleanup(
			func() {
				assert.NoError(t, pid1.Shutdown(ctx))
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With RemoteBatchTell when remoting is not enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create test actor
		tester := "test"
		testActor := NewMockActor()
		testerRef, err := sys.Spawn(ctx, tester, testActor)
		require.NoError(t, err)
		require.NotNil(t, testerRef)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:    ctx,
			sender: NoSender,
			self:   testerRef,
		}

		// get the address of the exchanger actor one
		testerAddr := context.RemoteLookup(host, remotingPort, tester)

		testerRef.remoting = nil

		// send the message to t exchanger actor one using remote messaging
		messages := []proto.Message{new(testpb.TestSend), new(testpb.TestSend), new(testpb.TestSend)}
		context.RemoteBatchTell(address.From(testerAddr), messages)
		err = context.getError()
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrRemotingDisabled)

		pause.For(time.Second)

		t.Cleanup(
			func() {
				assert.NoError(t, testerRef.Shutdown(ctx))
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With successful RemoteBatchAsk", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger two
		actorName2 := "Exchange2"

		// create actor1
		actor1 := &exchanger{}
		pid1, err := sys.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		context.RemoteBatchAsk(address.From(
			&goaktpb.Address{
				Host: "127.0.0.1",
				Port: int32(remotingPort),
				Name: actorName2,
				Id:   "",
			},
		), []proto.Message{new(testpb.TestReply)}, time.Minute)
		require.Error(t, context.getError())
		pause.For(time.Second)

		t.Cleanup(
			func() {
				assert.NoError(t, pid1.Shutdown(ctx))
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With no panics RemoteReSpawn when actor not found", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger two
		actorName2 := "Exchange2"

		// create actor1
		actor1 := &exchanger{}
		pid1, err := sys.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		context.RemoteReSpawn(host, remotingPort, actorName2)
		require.NoError(t, context.getError())
		pause.For(time.Second)

		t.Cleanup(
			func() {
				assert.NoError(t, pid1.Shutdown(ctx))
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With failed RemoteReSpawn", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an exchanger 2
		actorName2 := "Exchange2"

		// create actor1
		actor1 := &exchanger{}
		pid1, err := sys.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		context.RemoteReSpawn(host, remotingPort, actorName2)
		require.Error(t, context.getError())
		pause.For(time.Second)

		t.Cleanup(
			func() {
				assert.NoError(t, pid1.Shutdown(ctx))
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With successful PipeTo", func(t *testing.T) {
		askTimeout := time.Minute
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		actor2 := &exchanger{}

		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(time.Second)

		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		// create an instance of receive context
		messageContext := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TaskComplete),
			sender:  NoSender,
			self:    pid1,
		}

		task := func() (proto.Message, error) {
			// simulate a long-running task
			pause.For(500 * time.Millisecond)
			return new(testpb.TaskComplete), nil
		}
		messageContext.PipeTo(pid2, task)

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

		require.EqualValues(t, 3, pid1.ProcessedCount()-1)
		require.EqualValues(t, 1, pid2.ProcessedCount()-1)

		pause.For(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With failed PipeTo: no task defined", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}

		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		actor2 := &exchanger{}

		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(time.Second)

		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		// create an instance of receive context
		messageContext := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TaskComplete),
			sender:  NoSender,
			self:    pid1,
		}

		messageContext.PipeTo(pid2, nil)
		require.Error(t, messageContext.getError())
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With failed PipeTo: task returns an error", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create a deadletter subscriber
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create actor1
		actor1 := &exchanger{}

		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create actor2
		actor2 := &exchanger{}

		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		pause.For(time.Second)

		// zero message received by both actors
		require.Zero(t, pid1.ProcessedCount()-1)
		require.Zero(t, pid2.ProcessedCount()-1)

		task := func() (proto.Message, error) {
			// simulate a long-running task
			pause.For(500 * time.Millisecond)
			return nil, assert.AnError
		}

		// create an instance of receive context
		messageContext := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TaskComplete),
			sender:  NoSender,
			self:    pid1,
		}

		messageContext.PipeTo(pid2, task)

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

		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With successful SendAsync command", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		context.SendAsync(pid2.Name(), new(testpb.TestSend))
		require.NoError(t, context.getError())

		t.Cleanup(
			func() {
				assert.NoError(t, actorSystem.Stop(ctx))
			},
		)
	})
	t.Run("With failed SendAsync", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// wait a while and shutdown actor2
		pause.For(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))
		context.SendAsync(pid2.Name(), new(testpb.TestSend))
		require.Error(t, context.getError())

		t.Cleanup(
			func() {
				assert.NoError(t, actorSystem.Stop(ctx))
			},
		)
	})
	t.Run("With successful SendSync command", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		reply := context.SendSync(pid2.Name(), new(testpb.TestReply), time.Minute)
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, reply))
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With failed SendSync", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// wait a while and shutdown actor2
		pause.For(time.Second)
		assert.NoError(t, pid2.Shutdown(ctx))

		context.SendSync(pid2.Name(), new(testpb.TestReply), time.Minute)
		require.Error(t, context.getError())

		t.Cleanup(
			func() {
				assert.NoError(t, actorSystem.Stop(ctx))
			},
		)
	})
	t.Run("With Stash when stash not set", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor path
		actor := &MockStash{}
		pid, err := actorSystem.Spawn(ctx, "stashQA", actor)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// wait for the actor to properly start
		pause.For(5 * time.Millisecond)

		// create a receiveContext
		receiveContext := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid,
		}

		receiveContext.Stash()
		err = receiveContext.getError()
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrStashBufferNotSet)
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With Unstash when stash not set", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor path
		actor := &MockStash{}

		pid, err := actorSystem.Spawn(ctx, "stashQA", actor)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// wait for the actor to properly start
		pause.For(5 * time.Millisecond)

		// create a receiveContext
		receiveContext := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid,
		}

		receiveContext.Unstash()
		err = receiveContext.getError()
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrStashBufferNotSet)
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With UnstashAll when stash not set", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor path
		actor := &MockStash{}

		pid, err := actorSystem.Spawn(ctx, "stashQA", actor)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// wait for the actor to properly start
		pause.For(5 * time.Millisecond)

		// create a receive context
		receiveContext := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid,
		}

		receiveContext.UnstashAll()
		err = receiveContext.getError()
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrStashBufferNotSet)
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With successful RemoteForward", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(2)
		host := "127.0.0.1"

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		actorSystem2, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[1])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem2)

		require.NoError(t, actorSystem2.Start(ctx))

		// create actorA
		pidA, err := actorSystem2.Spawn(ctx, "ExchangeA", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pidA)

		pidC, err := actorSystem2.Spawn(ctx, "ExchangeC", &MockRemote{})
		require.NoError(t, err)
		require.NotNil(t, pidC)

		// create actorB
		actorB := &MockForward{
			remoteRef: pidC,
		}

		pidB, err := actorSystem2.Spawn(ctx, "ExchangeB", actorB)
		require.NoError(t, err)
		require.NotNil(t, pidB)

		// actor A is killing actor C using a forward pattern
		// actorA tell actorB forward actorC
		err = pidA.Tell(ctx, pidB, new(testpb.TestRemoteForward))
		require.NoError(t, err)

		// wait for the async call to properly complete
		pause.For(time.Second)
		require.True(t, pidA.IsRunning())
		require.True(t, pidB.IsRunning())
		require.False(t, pidC.IsRunning())

		// let us shutdown the rest
		require.NoError(t, pidA.Shutdown(ctx))
		require.NoError(t, pidB.Shutdown(ctx))
	})
	t.Run("With failed RemoteForward", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(2)
		host := "127.0.0.1"

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		actorSystem2, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[1])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem2)

		require.NoError(t, actorSystem2.Start(ctx))

		// create actorA
		pidA, err := actorSystem2.Spawn(ctx, "ExchangeA", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pidA)

		pidC, err := actorSystem2.Spawn(ctx, "ExchangeC", &MockRemote{})
		require.NoError(t, err)
		require.NotNil(t, pidC)

		// create actorB
		actorB := &MockForward{
			remoteRef: pidC,
		}

		require.NoError(t, pidC.Shutdown(ctx))

		pidB, err := actorSystem2.Spawn(ctx, "ExchangeB", actorB)
		require.NoError(t, err)
		require.NotNil(t, pidB)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestRemoteForward),
			sender:  pidA,
			self:    pidB,
		}

		// actor A is killing actor C using a forward pattern
		// actorA tell actorB forward actorC
		context.RemoteForward(pidC.Address())
		require.Error(t, context.getError())

		// let us shutdown the rest
		require.NoError(t, pidA.Shutdown(ctx))
		require.NoError(t, pidB.Shutdown(ctx))

		assert.NoError(t, actorSystem.Stop(ctx))
		assert.NoError(t, actorSystem2.Stop(ctx))
	})
	t.Run("With successful RemoteForward case 2", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(2)
		host := "127.0.0.1"

		actorSystem, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[0])),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		require.NoError(t, actorSystem.Start(ctx))

		actorSystem2, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, ports[1])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NotNil(t, actorSystem2)

		require.NoError(t, actorSystem2.Start(ctx))

		// create actorA
		pidA, err := actorSystem2.Spawn(ctx, "ExchangeA", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pidA)

		pidC, err := actorSystem2.Spawn(ctx, "ExchangeC", &MockRemote{})
		require.NoError(t, err)
		require.NotNil(t, pidC)

		// create actorB
		actorB := &MockForward{
			remoteRef: pidC,
		}

		pidB, err := actorSystem2.Spawn(ctx, "ExchangeB", actorB)
		require.NoError(t, err)
		require.NotNil(t, pidB)

		// actor A is killing actor C using a forward pattern
		// actorA tell actorB forward actorC
		err = pidA.RemoteTell(ctx, pidB.Address(), new(testpb.TestRemoteForward))
		require.NoError(t, err)

		// wait for the async call to properly complete
		pause.For(time.Second)
		require.True(t, pidA.IsRunning())
		require.True(t, pidB.IsRunning())
		require.False(t, pidC.IsRunning())

		// let us shutdown the rest
		require.NoError(t, pidA.Shutdown(ctx))
		require.NoError(t, pidB.Shutdown(ctx))
		assert.NoError(t, actorSystem.Stop(ctx))
		assert.NoError(t, actorSystem2.Stop(ctx))
	})
	t.Run("With successful ForwardTo", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		actorSystem, provider1 := testCluster(t, srv.Addr().String())
		require.NotNil(t, actorSystem)
		require.NotNil(t, provider1)

		// create and start system cluster
		actorSystem2, provider2 := testCluster(t, srv.Addr().String())
		require.NotNil(t, actorSystem2)
		require.NotNil(t, provider2)

		// create actorA
		pidA, err := actorSystem2.Spawn(ctx, "ExchangeA", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pidA)

		// create actorC
		pidC, err := actorSystem2.Spawn(ctx, "ExchangeC", &MockRemote{})
		require.NoError(t, err)
		require.NotNil(t, pidC)

		// create actorB
		actorB := &MockForward{
			remoteRef: pidC,
		}

		pidB, err := actorSystem2.Spawn(ctx, "ExchangeB", actorB)
		require.NoError(t, err)
		require.NotNil(t, pidB)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestRemoteForward),
			sender:  pidA,
			self:    pidB,
		}

		// actor A is killing actor C using a forward pattern
		// actorA tell actorB forward actorC
		context.ForwardTo("ExchangeC")
		require.NoError(t, context.getError())

		// wait for the async call to properly complete
		pause.For(time.Second)

		require.True(t, pidA.IsRunning())
		require.True(t, pidB.IsRunning())
		require.False(t, pidC.IsRunning())

		// let us shutdown the rest
		require.NoError(t, actorSystem.Stop(ctx))
		require.NoError(t, actorSystem2.Stop(ctx))
		srv.Shutdown()
	})
	t.Run("With failed ForwardTo", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		actorSystem, provider1 := testCluster(t, srv.Addr().String())
		require.NotNil(t, actorSystem)
		require.NotNil(t, provider1)

		// create and start system cluster
		actorSystem2, provider2 := testCluster(t, srv.Addr().String())
		require.NotNil(t, actorSystem2)
		require.NotNil(t, provider2)

		// create actorA
		pidA, err := actorSystem2.Spawn(ctx, "ExchangeA", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pidA)

		// create actorC
		pidC, err := actorSystem2.Spawn(ctx, "ExchangeC", &MockRemote{})
		require.NoError(t, err)
		require.NotNil(t, pidC)

		// create actorB
		actorB := &MockForward{
			remoteRef: pidC,
		}

		require.NoError(t, pidC.Shutdown(ctx))

		pidB, err := actorSystem2.Spawn(ctx, "ExchangeB", actorB)
		require.NoError(t, err)
		require.NotNil(t, pidB)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestRemoteForward),
			sender:  pidA,
			self:    pidB,
		}

		// actor A is killing actor C using a forward pattern
		// actorA tell actorB forward actorC
		context.ForwardTo("ExchangeC")
		require.Error(t, context.getError())

		// wait for the async call to properly complete
		pause.For(time.Second)

		// let us shutdown the rest
		require.NoError(t, actorSystem.Stop(ctx))
		require.NoError(t, actorSystem2.Stop(ctx))
		srv.Shutdown()
	})
	t.Run("With Watch/UnWatch", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// watch actor2
		context.Watch(pid2)

		pnode, ok := pid2.ActorSystem().tree().node(pid2.ID())
		require.True(t, ok)
		watchers := pnode.watchers

		found := false
		for _, watcher := range watchers.Values() {
			if watcher.Equals(pid1) {
				found = true
				break
			}
		}

		require.True(t, found)

		// reset the found
		found = false
		// unwatch actor2
		context.UnWatch(pid2)
		for _, watcher := range watchers.Values() {
			if watcher.Equals(pid1) {
				found = true
				break
			}
		}

		require.False(t, found)
		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With extensions", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		actorSystem, err := NewActorSystem("sys",
			WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		extensions := context.Extensions()
		require.NotNil(t, extensions)
		require.Empty(t, extensions)

		extension := context.Extension("test")
		require.Nil(t, extension)

		pause.For(time.Second)
		assert.NoError(t, actorSystem.Stop(ctx))
	})

	t.Run("With successful Reinstate", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// suspend actor2
		pid2.suspend("test")
		require.True(t, pid2.IsSuspended())
		require.False(t, pid2.IsRunning())

		// reinstate actor2
		context.Reinstate(pid2)
		require.NoError(t, context.getError())
		require.True(t, pid2.IsRunning())
		require.False(t, pid2.IsSuspended())

		require.NoError(t, actorSystem.Stop(ctx))
	})

	t.Run("With failed Reinstate", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create actor1
		actor1 := &exchanger{}
		pid1, err := actorSystem.Spawn(ctx, "Exchange1", actor1)
		require.NoError(t, err)
		require.NotNil(t, pid1)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			sender:  NoSender,
			self:    pid1,
		}

		// create actor2
		actor2 := &exchanger{}
		pid2, err := actorSystem.Spawn(ctx, "Exchange2", actor2)
		require.NoError(t, err)
		require.NotNil(t, pid2)

		// stop actor2
		err = pid2.Shutdown(ctx)
		require.NoError(t, err)

		pause.For(time.Second)
		require.False(t, pid2.IsRunning())

		// reinstate actor2
		context.Reinstate(pid2)
		require.Error(t, context.getError())

		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With successful ReinstateNamed", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		actorSystem, provider1 := testCluster(t, srv.Addr().String())
		require.NotNil(t, actorSystem)
		require.NotNil(t, provider1)

		// create and start system cluster
		actorSystem2, provider2 := testCluster(t, srv.Addr().String())
		require.NotNil(t, actorSystem2)
		require.NotNil(t, provider2)

		// create actorA
		pidA, err := actorSystem.Spawn(ctx, "ExchangeA", &exchanger{})
		require.NoError(t, err)
		require.NotNil(t, pidA)

		pause.For(time.Second)

		// create actorB
		pidB, err := actorSystem2.Spawn(ctx, "ExchangeC", &MockRemote{})
		require.NoError(t, err)
		require.NotNil(t, pidB)

		pause.For(time.Second)

		// create an instance of receive context
		context := &ReceiveContext{
			ctx:     ctx,
			message: new(testpb.TestSend),
			self:    pidA,
		}

		pidB.suspend("test")
		require.True(t, pidB.IsSuspended())
		require.False(t, pidB.IsRunning())

		context.ReinstateNamed("ExchangeC")

		// wait for the async call to properly complete
		pause.For(time.Second)

		require.NoError(t, context.getError())
		require.True(t, pidB.IsRunning())
		require.False(t, pidB.IsSuspended())

		// let us shutdown the rest
		require.NoError(t, actorSystem.Stop(ctx))
		require.NoError(t, actorSystem2.Stop(ctx))
		srv.Shutdown()
	})
}
