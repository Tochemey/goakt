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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v2/address"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/lib"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/test/data/testpb"
)

func TestAsk(t *testing.T) {
	t.Run("With running actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := Ask(ctx, actorRef, message, replyTimeout)
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
		logger := log.DiscardLogger
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// Shutdown the actor after some time
		lib.Pause(time.Second)
		require.NoError(t, actorRef.Shutdown(ctx))

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := Ask(ctx, actorRef, message, replyTimeout)
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
		logger := log.DiscardLogger
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithReplyTimeout(replyTimeout),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a message to send to the test actor
		message := new(testpb.TestTimeout)
		// send the message to the actor
		reply, err := Ask(ctx, actorRef, message, replyTimeout)
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
		logger := log.DiscardLogger
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		lib.Pause(time.Second)

		// create a message to send to the test actor
		message := &internalpb.RemoteMessage{
			Message: &anypb.Any{},
		}
		// send the message to the actor
		reply, err := Ask(ctx, actorRef, message, replyTimeout)
		// perform some assertions
		require.Error(t, err)
		assert.Nil(t, reply)

		err = sys.Stop(ctx)
	})
	t.Run("With Batch request happy path", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a message to send to the test actor
		// send the message to the actor
		replies, err := BatchAsk(ctx, actorRef, replyTimeout, new(testpb.TestReply), new(testpb.TestReply))
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
		logger := log.DiscardLogger
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithReplyTimeout(replyTimeout),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a message to send to the test actor
		// send the message to the actor
		replies, err := BatchAsk(ctx, actorRef, replyTimeout, new(testpb.TestTimeout), new(testpb.TestReply))
		// perform some assertions
		require.Error(t, err)
		require.EqualError(t, err, ErrRequestTimeout.Error())
		assert.Empty(t, replies)

		// stop the actor after some time
		// this is due to the actor Waitgroup to gracefully close
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
	})
	t.Run("With Batch request with dead actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithReplyTimeout(replyTimeout),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// stop the actor
		require.NoError(t, actorRef.Shutdown(ctx))

		// create a message to send to the test actor
		// send the message to the actor
		replies, err := BatchAsk(ctx, actorRef, replyTimeout, new(testpb.TestTimeout), new(testpb.TestReply))
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
		logger := log.DiscardLogger
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		lib.Pause(time.Second)

		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = Tell(ctx, actorRef, message)
		// perform some assertions
		require.NoError(t, err)

		// stop the actor after some time
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With stopped actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// Shutdown the actor after some time
		lib.Pause(time.Second)
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
		logger := log.DiscardLogger
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
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
		logger := log.DiscardLogger
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		lib.Pause(time.Second)

		// create a message to send to the test actor
		// send the message to the actor
		err = BatchTell(ctx, actorRef, new(testpb.TestSend), new(testpb.TestSend))
		// perform some assertions
		require.NoError(t, err)
		// wait for processing to be done
		lib.Pause(500 * time.Millisecond)
		require.EqualValues(t, 2, actorRef.ProcessedCount()-1)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch request with a dead actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		lib.Pause(time.Second)
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
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
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
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With invalid message", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
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
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With remote service failure", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
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
		err = RemoteTell(ctx, address.From(addr), message)
		// perform some assertions
		require.Error(t, err)

		// stop the actor after some time
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With remoting disabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
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
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch request", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
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
		lib.Pause(500 * time.Millisecond)
		require.EqualValues(t, 10, actorRef.ProcessedCount()-1)

		// stop the actor after some time
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch service failure", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
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
		err = RemoteBatchTell(ctx, address.From(addr), message)
		// perform some assertions
		require.Error(t, err)

		// stop the actor after some time
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch when remoting is disabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
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
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With actor not found", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// stop the actor when wait for cleanup to take place
		require.NoError(t, actorRef.Shutdown(ctx))
		lib.Pause(time.Second)

		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = RemoteTell(ctx, addr, message)
		// perform some assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")

		// stop the actor after some time
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch actor not found", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// stop the actor when wait for cleanup to take place
		require.NoError(t, actorRef.Shutdown(ctx))
		lib.Pause(time.Second)

		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = RemoteBatchTell(ctx, addr, message)
		// perform some assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")

		// stop the actor after some time
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestRemoteAsk(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := RemoteAsk(ctx, addr, message, time.Minute)
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
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With invalid message", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// send the message to the actor
		reply, err := RemoteAsk(ctx, addr, nil, time.Minute)
		// perform some assertions
		require.Error(t, err)
		require.Nil(t, reply)

		// stop the actor after some time
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With remote service failure", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
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
		reply, err := RemoteAsk(ctx, address.From(addr), message, time.Minute)
		// perform some assertions
		require.Error(t, err)
		require.Nil(t, reply)

		// stop the actor after some time
		lib.Pause(time.Second)
		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With remoting disabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
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
		reply, err := RemoteAsk(ctx, addr, message, time.Minute)
		// perform some assertions
		require.Error(t, err)
		require.EqualError(t, err, "failed_precondition: remoting is not enabled")
		require.Nil(t, reply)

		// stop the actor after some time
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch request", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
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
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch service failure", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
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
		reply, err := RemoteBatchAsk(ctx, address.From(addr), message)
		// perform some assertions
		require.Error(t, err)
		require.Nil(t, reply)

		// stop the actor after some time
		lib.Pause(time.Second)
		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With Batch when remoting is disabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
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
		lib.Pause(time.Second)
		t.Cleanup(func() {
			assert.NoError(t, sys.Stop(ctx))
		})
	})
	t.Run("With actor not found", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled(),
			WithJanitorInterval(30*time.Millisecond),
			WithRemoting(host, int32(remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// stop the actor when wait for cleanup to take place
		require.NoError(t, actorRef.Shutdown(ctx))
		lib.Pause(time.Second)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := RemoteAsk(ctx, addr, message, time.Minute)
		// perform some assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
		require.Nil(t, reply)

		// stop the actor after some time
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch actor not found", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled(),
			WithJanitorInterval(30*time.Millisecond),
			WithRemoting(host, int32(remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// stop the actor when wait for cleanup to take place
		require.NoError(t, actorRef.Shutdown(ctx))
		lib.Pause(time.Second)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := RemoteBatchAsk(ctx, addr, message)
		// perform some assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
		require.Nil(t, reply)

		// stop the actor after some time
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestAPIRemoteLookup(t *testing.T) {
	t.Run("When remoting is not enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

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
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

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
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// assert the actor restart count
		pid := actorRef
		assert.Zero(t, pid.restartCount.Load())

		// get the address of the actor
		err = RemoteReSpawn(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		assert.EqualValues(t, 1, pid.restartCount.Load())

		// stop the actor after some time
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestAPIRemoteStop(t *testing.T) {
	t.Run("When remoting is not enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		// create a test actor
		actorName := "test"
		// get the address of the actor
		err = RemoteStop(ctx, host, remotingPort, actorName)
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
		logger := log.DiscardLogger
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

		lib.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// assert the actor restart count
		pid := actorRef
		assert.Zero(t, pid.restartCount.Load())

		// get the address of the actor
		err = RemoteStop(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		assert.Empty(t, sys.Actors())

		// stop the actor after some time
		lib.Pause(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestAPIRemoteSpawn(t *testing.T) {
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

		// create an actor implementation and register it
		actor := &exchanger{}
		actorName := uuid.NewString()

		// fetching the address of the that actor should return nil address
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.Nil(t, addr)

		// register the actor
		err = sys.Register(ctx, actor)
		require.NoError(t, err)

		// spawn the remote actor
		err = RemoteSpawn(ctx, host, remotingPort, actorName, "actors.exchanger")
		require.NoError(t, err)

		// re-fetching the address of the actor should return not nil address after start
		addr, err = RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.NotNil(t, addr)

		// send the message to exchanger actor one using remote messaging
		reply, err := RemoteAsk(ctx, addr, new(testpb.TestReply), time.Minute)

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

		// create an actor implementation and register it
		actorName := uuid.NewString()

		// fetching the address of the that actor should return nil address
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.Nil(t, addr)

		// spawn the remote actor
		err = RemoteSpawn(ctx, host, remotingPort, actorName, "actors.exchanger")
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

		// create an actor implementation and register it
		actorName := uuid.NewString()

		// spawn the remote actor
		err = RemoteSpawn(ctx, host, remotingPort, actorName, "actors.exchanger")
		require.Error(t, err)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
}
