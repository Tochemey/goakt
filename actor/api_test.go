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
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	mocksremote "github.com/tochemey/goakt/v4/mocks/remoteclient"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestAsk(t *testing.T) {
	t.Run("With started actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
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

		actual, ok := reply.(*testpb.Reply)
		require.True(t, ok)
		expected := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, actual))

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With stopped actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// Shutdown the actor after some time
		pause.For(time.Second)
		require.NoError(t, actorRef.Shutdown(ctx))

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := Ask(ctx, actorRef, message, replyTimeout)
		// perform some assertions
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrDead)
		assert.Nil(t, reply)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With remote PID and remoting disabled", func(t *testing.T) {
		ctx := context.TODO()
		addr := address.New("remote-actor", "sys", "127.0.0.1", 9000)
		remotePID := newRemotePID(addr, nil) // nil remoting
		reply, err := Ask(ctx, remotePID, new(testpb.TestReply), time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)
		assert.Nil(t, reply)
	})
	t.Run("With remote PID and remoting enabled (success)", func(t *testing.T) {
		ctx := context.TODO()
		addr := address.New("remote-actor", "sys", "127.0.0.1", 9000)
		remotingMock := mocksremote.NewClient(t)
		expected := &testpb.Reply{Content: "remote response"}
		remotingMock.EXPECT().
			RemoteAsk(
				mock.Anything,
				mock.MatchedBy(func(a *address.Address) bool { return a != nil && a.Equals(address.NoSender()) }),
				mock.MatchedBy(func(a *address.Address) bool { return a != nil && a.Equals(addr) }),
				mock.Anything,
				time.Second,
			).
			Return(expected, nil).Once()
		remotePID := newRemotePID(addr, remotingMock)
		reply, err := Ask(ctx, remotePID, new(testpb.TestReply), time.Second)
		require.NoError(t, err)
		require.NotNil(t, reply)
		assert.True(t, proto.Equal(expected, reply.(*testpb.Reply)))
		remotingMock.AssertExpectations(t)
	})
	t.Run("With remote PID and invalid timeout", func(t *testing.T) {
		ctx := context.TODO()
		addr := address.New("remote-actor", "sys", "127.0.0.1", 9000)
		remotingMock := mocksremote.NewClient(t)
		remotePID := newRemotePID(addr, remotingMock)
		reply, err := Ask(ctx, remotePID, new(testpb.TestReply), 0)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrInvalidTimeout)
		assert.Nil(t, reply)
		remotingMock.AssertNotCalled(t, "RemoteAsk")
	})
	t.Run("With remote PID and RemoteAsk returns error", func(t *testing.T) {
		ctx := context.TODO()
		addr := address.New("remote-actor", "sys", "127.0.0.1", 9000)
		remotingMock := mocksremote.NewClient(t)
		expectedErr := errors.ErrRequestTimeout
		remotingMock.EXPECT().
			RemoteAsk(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, expectedErr).Once()
		remotePID := newRemotePID(addr, remotingMock)
		reply, err := Ask(ctx, remotePID, new(testpb.TestReply), time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, expectedErr)
		assert.Nil(t, reply)
		remotingMock.AssertExpectations(t)
	})
	t.Run("With context canceled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a message to send to the test actor
		message := new(testpb.TestTimeout)
		cancelCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		// send the message to the actor
		reply, err := Ask(cancelCtx, actorRef, message, 2*time.Second)
		// perform some assertions
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRequestTimeout)
		assert.Nil(t, reply)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With request timeout", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a message to send to the test actor
		message := new(testpb.TestTimeout)
		// send the message to the actor
		reply, err := Ask(ctx, actorRef, message, 100*time.Millisecond)
		// perform some assertions
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRequestTimeout)
		assert.Nil(t, reply)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With invalid remote message", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)

		// create a message to send to the test actor
		message := &internalpb.RemoteMessage{}
		// send the message to the actor
		reply, err := Ask(ctx, actorRef, message, replyTimeout)
		// perform some assertions
		require.Error(t, err)
		assert.Nil(t, reply)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With invalid remote sender address", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)

		bytea, err := proto.Marshal(new(testpb.TestSend))
		require.NoError(t, err)

		// create a message to send to the test actor
		message := &internalpb.RemoteMessage{
			Sender:   "invalid-address",
			Receiver: actorRef.Path().String(),
			Message:  bytea,
		}
		// send the message to the actor
		reply, err := Ask(ctx, actorRef, message, replyTimeout)
		// perform some assertions
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrInvalidRemoteMessage)
		assert.Nil(t, reply)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With Batch request happy path", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
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
			actual, ok := reply.(*testpb.Reply)
			require.True(t, ok)
			expected := &testpb.Reply{Content: "received message"}
			assert.True(t, proto.Equal(expected, actual))
		}

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With Batch request with timeout", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a message to send to the test actor
		// send the message to the actor
		replies, err := BatchAsk(ctx, actorRef, replyTimeout, new(testpb.TestTimeout), new(testpb.TestReply))
		// perform some assertions
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrRequestTimeout)
		assert.Empty(t, replies)

		// stop the actor after some time
		// this is due to the actor Waitgroup to gracefully close
		pause.For(time.Second)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With Batch request with dead actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
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
		require.NoError(t, err)
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

func TestTell(t *testing.T) {
	t.Run("With started actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)

		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = Tell(ctx, actorRef, message)
		// perform some assertions
		require.NoError(t, err)

		// stop the actor after some time
		pause.For(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	},
	)
	t.Run("With remote PID and remoting disabled", func(t *testing.T) {
		ctx := context.TODO()
		addr := address.New("remote-actor", "sys", "127.0.0.1", 9000)
		remotePID := newRemotePID(addr, nil) // nil remoting
		err := Tell(ctx, remotePID, new(testpb.TestSend))
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)
	})
	t.Run("With remote PID and remoting enabled (success)", func(t *testing.T) {
		ctx := context.TODO()
		addr := address.New("remote-actor", "sys", "127.0.0.1", 9000)
		remotingMock := mocksremote.NewClient(t)
		remotingMock.EXPECT().
			RemoteTell(
				mock.Anything,
				mock.MatchedBy(func(a *address.Address) bool { return a != nil && a.Equals(address.NoSender()) }),
				mock.MatchedBy(func(a *address.Address) bool { return a != nil && a.Equals(addr) }),
				mock.Anything,
			).
			Return(nil).Once()
		remotePID := newRemotePID(addr, remotingMock)
		err := Tell(ctx, remotePID, new(testpb.TestSend))
		require.NoError(t, err)
		remotingMock.AssertExpectations(t)
	})
	t.Run("With remote PID and RemoteTell returns error", func(t *testing.T) {
		ctx := context.TODO()
		addr := address.New("remote-actor", "sys", "127.0.0.1", 9000)
		remotingMock := mocksremote.NewClient(t)
		expectedErr := errors.ErrRemotingDisabled
		remotingMock.EXPECT().
			RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(expectedErr).Once()
		remotePID := newRemotePID(addr, remotingMock)
		err := Tell(ctx, remotePID, new(testpb.TestSend))
		require.Error(t, err)
		assert.ErrorIs(t, err, expectedErr)
		remotingMock.AssertExpectations(t)
	})
	t.Run("With stopped actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// Shutdown the actor after some time
		pause.For(time.Second)
		require.NoError(t, actorRef.Shutdown(ctx))

		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = Tell(ctx, actorRef, message)
		// perform some assertions
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrDead)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	},
	)
	t.Run("With invalid remote message", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a message to send to the test actor
		message := &internalpb.RemoteMessage{}
		// send the message to the actor
		err = Tell(ctx, actorRef, message)
		require.Error(t, err)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	},
	)
	t.Run(
		"With Batch request", func(t *testing.T) {
			// create the context
			ctx := context.TODO()
			// define the logger to use
			logger := log.DiscardLogger
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

			pause.For(time.Second)

			// create a test actor
			actorName := "test"
			actor := NewMockActor()
			actorRef, err := sys.Spawn(ctx, actorName, actor)
			require.NoError(t, err)
			assert.NotNil(t, actorRef)

			pause.For(time.Second)

			// create a message to send to the test actor
			// send the message to the actor
			err = BatchTell(ctx, actorRef, new(testpb.TestSend), new(testpb.TestSend))
			// perform some assertions
			require.NoError(t, err)
			// wait for processing to be done
			pause.For(500 * time.Millisecond)
			require.EqualValues(t, 2, actorRef.ProcessedCount()-1)

			err = sys.Stop(ctx)
			assert.NoError(t, err)
		},
	)
	t.Run("With Batch request with a dead actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)
		require.NoError(t, actorRef.Shutdown(ctx))

		// create a message to send to the test actor
		// send the message to the actor
		err = BatchTell(ctx, actorRef, new(testpb.TestSend), new(testpb.TestSend))
		// perform some assertions
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrDead)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}
