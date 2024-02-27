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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/goaktpb"
	internalpb "github.com/tochemey/goakt/internal/internalpb"
	"github.com/tochemey/goakt/log"
	testpb "github.com/tochemey/goakt/test/data/testpb"
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
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

		err = sys.Stop(ctx)
	})
	t.Run("With request timeout", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithReplyTimeout(receivingTimeout),
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a message to send to the test actor
		message := new(testpb.TestTimeout)
		// send the message to the actor
		reply, err := Ask(ctx, actorRef, message, receivingTimeout)
		// perform some assertions
		require.Error(t, err)
		assert.EqualError(t, err, ErrRequestTimeout.Error())
		assert.Nil(t, reply)

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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
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

		err = sys.Stop(ctx)
	})
	t.Run("With Batch request happy path", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a message to send to the test actor
		// send the message to the actor
		replies, err := BatchAsk(ctx, actorRef, receivingTimeout, new(testpb.TestReply), new(testpb.TestReply))
		// perform some assertions
		require.NoError(t, err)
		assert.NotNil(t, replies)
		assert.NotEmpty(t, replies)
		assert.Len(t, replies, 2)

		for reply := range replies {
			expected := &testpb.Reply{Content: "received message"}
			assert.True(t, proto.Equal(expected, reply))
		}

		err = sys.Stop(ctx)
	})
	t.Run("With Batch request with timeout", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithReplyTimeout(receivingTimeout),
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a message to send to the test actor
		// send the message to the actor
		replies, err := BatchAsk(ctx, actorRef, receivingTimeout, new(testpb.TestTimeout), new(testpb.TestReply))
		// perform some assertions
		require.Error(t, err)
		require.EqualError(t, err, ErrRequestTimeout.Error())
		assert.Empty(t, replies)

		// stop the actor after some time
		// this is due to the actor Waitgroup to gracefully close
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
	})
	t.Run("With Batch request with dead actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithReplyTimeout(receivingTimeout),
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// stop the actor
		require.NoError(t, actorRef.Shutdown(ctx))

		// create a message to send to the test actor
		// send the message to the actor
		replies, err := BatchAsk(ctx, actorRef, receivingTimeout, new(testpb.TestTimeout), new(testpb.TestReply))
		// perform some assertions
		require.Error(t, err)
		assert.Empty(t, replies)

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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		time.Sleep(time.Second)

		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = Tell(ctx, actorRef, message)
		// perform some assertions
		require.NoError(t, err)

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a message to send to the test actor
		message := &internalpb.RemoteMessage{
			Message: &anypb.Any{},
		}
		// send the message to the actor
		err = Tell(ctx, actorRef, message)
		require.Error(t, err)

		err = sys.Stop(ctx)
	})
	t.Run("With Batch request", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		time.Sleep(time.Second)

		// create a message to send to the test actor
		// send the message to the actor
		err = BatchTell(ctx, actorRef, new(testpb.TestSend), new(testpb.TestSend))
		// perform some assertions
		require.NoError(t, err)
		// wait for processing to be done
		time.Sleep(500 * time.Millisecond)
		require.EqualValues(t, 2, actor.counter.Load())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch request with a dead actor", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		time.Sleep(time.Second)
		require.NoError(t, actorRef.Shutdown(ctx))

		// create a message to send to the test actor
		// send the message to the actor
		err = BatchTell(ctx, actorRef, new(testpb.TestSend), new(testpb.TestSend))
		// perform some assertions
		require.Error(t, err)
		require.EqualError(t, err, ErrDead.Error())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
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

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
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
		assert.NoError(t, err)
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a wrong address
		addr := &goaktpb.Address{
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
		assert.NoError(t, err)
	})
	t.Run("With remoting disabled", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = RemoteTell(ctx, addr, message)
		// perform some assertions
		require.Error(t, err)
		require.EqualError(t, err, "failed_precondition: remoting is not enabled")

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch request", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		// create a message to send to the test actor
		messages := make([]proto.Message, 10)
		// send the message to the actor
		for i := 0; i < 10; i++ {
			messages[i] = new(testpb.TestSend)
		}

		err = RemoteBatchTell(ctx, addr, messages...)
		require.NoError(t, err)

		// wait for processing to complete on the actor side
		time.Sleep(500 * time.Millisecond)
		require.EqualValues(t, 10, actor.counter.Load())

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch service failure", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a wrong address
		addr := &goaktpb.Address{
			Host: host,
			Port: 2222,
			Name: "",
			Id:   "",
		}
		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = RemoteBatchTell(ctx, addr, message)
		// perform some assertions
		require.Error(t, err)

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch when remoting is disabled", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = RemoteBatchTell(ctx, addr, message)
		// perform some assertions
		require.Error(t, err)
		require.EqualError(t, err, "failed_precondition: remoting is not enabled")

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With actor not found", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// stop the actor when wait for cleanup to take place
		require.NoError(t, actorRef.Shutdown(ctx))
		time.Sleep(time.Second)

		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = RemoteTell(ctx, addr, message)
		// perform some assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch actor not found", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// stop the actor when wait for cleanup to take place
		require.NoError(t, actorRef.Shutdown(ctx))
		time.Sleep(time.Second)

		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = RemoteBatchTell(ctx, addr, message)
		// perform some assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
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
		assert.NoError(t, err)
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
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
		assert.NoError(t, err)
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr := &goaktpb.Address{
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
		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With remoting disabled", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := RemoteAsk(ctx, addr, message)
		// perform some assertions
		require.Error(t, err)
		require.EqualError(t, err, "failed_precondition: remoting is not enabled")
		require.Nil(t, reply)

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch request", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		replies, err := RemoteBatchAsk(ctx, addr, message)
		// perform some assertions
		require.NoError(t, err)
		require.Len(t, replies, 1)
		require.NotNil(t, replies[0])
		require.True(t, replies[0].MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = replies[0].UnmarshalTo(actual)
		require.NoError(t, err)

		expected := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, actual))

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch service failure", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr := &goaktpb.Address{
			Host: host,
			Port: 2222,
			Name: "",
			Id:   "",
		}

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := RemoteBatchAsk(ctx, addr, message)
		// perform some assertions
		require.Error(t, err)
		require.Nil(t, reply)

		// stop the actor after some time
		time.Sleep(time.Second)
		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With Batch when remoting is disabled", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := RemoteBatchAsk(ctx, addr, message)
		// perform some assertions
		require.Error(t, err)
		require.EqualError(t, err, "failed_precondition: remoting is not enabled")
		require.Nil(t, reply)

		// stop the actor after some time
		time.Sleep(time.Second)
		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With actor not found", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// stop the actor when wait for cleanup to take place
		require.NoError(t, actorRef.Shutdown(ctx))
		time.Sleep(time.Second)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := RemoteAsk(ctx, addr, message)
		// perform some assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
		require.Nil(t, reply)

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch actor not found", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// stop the actor when wait for cleanup to take place
		require.NoError(t, actorRef.Shutdown(ctx))
		time.Sleep(time.Second)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := RemoteBatchAsk(ctx, addr, message)
		// perform some assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
		require.Nil(t, reply)

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestAPIRemoteLookup(t *testing.T) {
	t.Run("When remoting is not enabled", func(t *testing.T) {
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

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		// create a test actor
		actorName := "test"
		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.Error(t, err)
		require.EqualError(t, err, "failed_precondition: remoting is not enabled")
		require.Nil(t, addr)

		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
	})
}

func TestAPIRemoteReSpawn(t *testing.T) {
	t.Run("When remoting is not enabled", func(t *testing.T) {
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

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		// create a test actor
		actorName := "test"
		// get the address of the actor
		err = RemoteReSpawn(ctx, host, remotingPort, actorName)
		require.Error(t, err)
		require.EqualError(t, err, "failed_precondition: remoting is not enabled")

		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("When remoting is enabled", func(t *testing.T) {
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
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// assert the actor restart count
		pid := actorRef.(*pid)
		assert.Zero(t, pid.restartCount.Load())

		// get the address of the actor
		err = RemoteReSpawn(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		assert.EqualValues(t, 1, pid.restartCount.Load())

		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}
