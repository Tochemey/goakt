package actors

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	internalpb "github.com/tochemey/goakt/internal/v1"
	"github.com/tochemey/goakt/log"
	addresspb "github.com/tochemey/goakt/pb/address/v1"
	testpb "github.com/tochemey/goakt/test/data/pb/v1"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestAsk(t *testing.T) {
	t.Run("With running actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		time.Sleep(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewTester()
		actorRef := sys.Spawn(ctx, actorName, actor)
		assert.NotNil(t, actorRef)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := Ask(ctx, actorRef, message, receivingTimeout)
		// perform some assertions
		require.NoError(t, err)
		assert.NotNil(t, reply)
		expected := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, reply))

		// stop the actor after some time
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		err = sys.Stop(ctx)
	})
	t.Run("With stopped actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		time.Sleep(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewTester()
		actorRef := sys.Spawn(ctx, actorName, actor)
		assert.NotNil(t, actorRef)

		// Shutdown the actor after some time
		time.Sleep(time.Second)
		require.NoError(t, actorRef.Shutdown(ctx))

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := Ask(ctx, actorRef, message, receivingTimeout)
		// perform some assertions
		require.Error(t, err)
		assert.EqualError(t, err, ErrDead.Error())
		assert.Nil(t, reply)

		// stop the actor after some time
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		err = sys.Stop(ctx)
	})
	t.Run("With invalid remote message", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		time.Sleep(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewTester()
		actorRef := sys.Spawn(ctx, actorName, actor)
		assert.NotNil(t, actorRef)

		time.Sleep(time.Second)

		// create a message to send to the test actor
		message := &internalpb.RemoteMessage{
			Message: &anypb.Any{},
		}
		// send the message to the actor
		reply, err := Ask(ctx, actorRef, message, receivingTimeout)
		// perform some assertions
		require.Error(t, err)
		assert.Nil(t, reply)

		// stop the actor after some time
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		err = sys.Stop(ctx)
	})
}

func TestTell(t *testing.T) {
	t.Run("With running actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		time.Sleep(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewTester()
		actorRef := sys.Spawn(ctx, actorName, actor)
		assert.NotNil(t, actorRef)

		time.Sleep(time.Second)

		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = Tell(ctx, actorRef, message)
		// perform some assertions
		require.NoError(t, err)
		count := 1
		assert.EqualValues(t, count, actorRef.ReceivedCount(ctx))

		// stop the actor after some time
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		err = sys.Stop(ctx)
	})
	t.Run("With stopped actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		time.Sleep(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewTester()
		actorRef := sys.Spawn(ctx, actorName, actor)
		assert.NotNil(t, actorRef)

		// Shutdown the actor after some time
		time.Sleep(time.Second)
		require.NoError(t, actorRef.Shutdown(ctx))

		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = Tell(ctx, actorRef, message)
		// perform some assertions
		require.Error(t, err)
		assert.EqualError(t, err, ErrDead.Error())

		// stop the actor after some time
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		err = sys.Stop(ctx)
	})
	t.Run("With invalid remote message", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		time.Sleep(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewTester()
		actorRef := sys.Spawn(ctx, actorName, actor)
		assert.NotNil(t, actorRef)

		// create a message to send to the test actor
		message := &internalpb.RemoteMessage{
			Message: &anypb.Any{},
		}
		// send the message to the actor
		err = Tell(ctx, actorRef, message)
		require.Error(t, err)

		// stop the actor after some time
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		err = sys.Stop(ctx)
	})
}

func TestRemoteTell(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

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

		time.Sleep(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewTester()
		actorRef := sys.Spawn(ctx, actorName, actor)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		for i := 0; i < 10; i++ {
			err = RemoteTell(ctx, addr, message)
			// perform some assertions
			require.NoError(t, err)
		}

		count := 10
		assert.EqualValues(t, count, actorRef.ReceivedCount(ctx))

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
	})
	t.Run("With invalid message", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

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

		time.Sleep(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewTester()
		actorRef := sys.Spawn(ctx, actorName, actor)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		err = RemoteTell(ctx, addr, nil)
		// perform some assertions
		require.Error(t, err)

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
	})
	t.Run("With remote service failure", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

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

		time.Sleep(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewTester()
		actorRef := sys.Spawn(ctx, actorName, actor)
		assert.NotNil(t, actorRef)

		// create a wrong address
		addr := &addresspb.Address{
			Host: host,
			Port: 2222,
			Name: "",
			Id:   "",
		}
		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = RemoteTell(ctx, addr, message)
		// perform some assertions
		require.Error(t, err)

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
	})
}

func TestRemoteAsk(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

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

		time.Sleep(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewTester()
		actorRef := sys.Spawn(ctx, actorName, actor)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := RemoteAsk(ctx, addr, message)
		// perform some assertions
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = reply.UnmarshalTo(actual)
		require.NoError(t, err)

		expected := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, actual))

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
	})
	t.Run("With invalid message", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

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

		time.Sleep(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewTester()
		actorRef := sys.Spawn(ctx, actorName, actor)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// send the message to the actor
		reply, err := RemoteAsk(ctx, addr, nil)
		// perform some assertions
		require.Error(t, err)
		require.Nil(t, reply)

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
	})
	t.Run("With remote service failure", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

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

		time.Sleep(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewTester()
		actorRef := sys.Spawn(ctx, actorName, actor)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr := &addresspb.Address{
			Host: host,
			Port: 2222,
			Name: "",
			Id:   "",
		}

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := RemoteAsk(ctx, addr, message)
		// perform some assertions
		require.Error(t, err)
		require.Nil(t, reply)

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
	})
}
