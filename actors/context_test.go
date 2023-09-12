package actors

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
	addresspb "github.com/tochemey/goakt/pb/address/v1"
	testpb "github.com/tochemey/goakt/test/data/pb/v1"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"
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
		pid := newPID(ctx, actorPath, actor, opts...)
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
		pid1 := newPID(ctx, actorPath1, actor1, opts...)
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
		pid2 := newPID(ctx, actorPath2, actor2, opts...)
		require.NotNil(t, pid2)

		op := func() {
			context.Tell(pid2, new(testpb.TestSend))
		}
		assert.NotPanics(t, op)

		time.Sleep(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
	})
	t.Run("With panic attack Tell", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1 := newPID(ctx, actorPath1, actor1, opts...)
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
		pid2 := newPID(ctx, actorPath2, actor2, opts...)
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
		pid1 := newPID(ctx, actorPath1, actor1, opts...)
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
		pid2 := newPID(ctx, actorPath2, actor2, opts...)
		require.NotNil(t, pid2)

		reply := context.Ask(pid2, new(testpb.TestReply))
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, reply))

		time.Sleep(time.Second)
		assert.NoError(t, pid1.Shutdown(ctx))
		assert.NoError(t, pid2.Shutdown(ctx))
	})
	t.Run("With panic attack Ask", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1 := newPID(ctx, actorPath1, actor1, opts...)
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
		pid2 := newPID(ctx, actorPath2, actor2, opts...)
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
		actorRef2 := sys.Spawn(ctx, actorName2, &Exchanger{})
		assert.NotNil(t, actorRef2)

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1 := newPID(ctx,
			actorPath1,
			actor1,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger))
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
	t.Run("With panic attack RemoteAsk", func(t *testing.T) {
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
		pid1 := newPID(ctx,
			actorPath1,
			actor1,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger))
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
		actorRef2 := sys.Spawn(ctx, actorName2, &Exchanger{})
		assert.NotNil(t, actorRef2)

		// create actor1
		actor1 := &Exchanger{}
		actorPath1 := NewPath("Exchange1", NewAddress("sys", "host", 1))
		pid1 := newPID(ctx,
			actorPath1,
			actor1,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger))
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
	t.Run("With panic attack RemoteTell", func(t *testing.T) {
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
		pid1 := newPID(ctx,
			actorPath1,
			actor1,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger))
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

		// send the message to t exchanger actor one using remote messaging
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
	t.Run("With panic attack RemoteLookup", func(t *testing.T) {
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
		pid1 := newPID(ctx,
			actorPath1,
			actor1,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger))
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

		// send the message to t exchanger actor one using remote messaging
		assert.Panics(t, func() {
			context.RemoteLookup(host, remotingPort, actorName2)
		})

		time.Sleep(time.Second)

		t.Cleanup(func() {
			assert.NoError(t, pid1.Shutdown(ctx))
			assert.NoError(t, sys.Stop(ctx))
		})
	})
}
