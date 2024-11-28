/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/lib"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/test/data/testpb"
)

func TestAsk(t *testing.T) {
	t.Run(
		"With started actor", func(t *testing.T) {
			// create the context
			ctx := context.TODO()
			// define the logger to use
			logger := log.DiscardLogger
			// create the actor system
			sys, err := NewActorSystem(
				"test",
				WithLogger(logger),
				WithPassivationDisabled(),
			)
			// assert there are no error
			require.NoError(t, err)

			// start the actor system
			err = sys.Start(ctx)
			assert.NoError(t, err)

			lib.Pause(time.Second)

			// create a test actor
			actorName := "test"
			actor := newActor()
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
		},
	)
	t.Run(
		"With stopped actor", func(t *testing.T) {
			// create the context
			ctx := context.TODO()
			// define the logger to use
			logger := log.DiscardLogger
			// create the actor system
			sys, err := NewActorSystem(
				"test",
				WithLogger(logger),
				WithPassivationDisabled(),
			)
			// assert there are no error
			require.NoError(t, err)

			// start the actor system
			err = sys.Start(ctx)
			assert.NoError(t, err)

			lib.Pause(time.Second)

			// create a test actor
			actorName := "test"
			actor := newActor()
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
		},
	)
	t.Run(
		"With request timeout", func(t *testing.T) {
			// create the context
			ctx := context.TODO()
			// define the logger to use
			logger := log.DiscardLogger
			// create the actor system
			sys, err := NewActorSystem(
				"test",
				WithLogger(logger),
				WithPassivationDisabled(),
			)
			// assert there are no error
			require.NoError(t, err)

			// start the actor system
			err = sys.Start(ctx)
			assert.NoError(t, err)

			lib.Pause(time.Second)

			// create a test actor
			actorName := "test"
			actor := newActor()
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
		},
	)
	t.Run(
		"With invalid remote message", func(t *testing.T) {
			// create the context
			ctx := context.TODO()
			// define the logger to use
			logger := log.DiscardLogger
			// create the actor system
			sys, err := NewActorSystem(
				"test",
				WithLogger(logger),
				WithPassivationDisabled(),
			)
			// assert there are no error
			require.NoError(t, err)

			// start the actor system
			err = sys.Start(ctx)
			assert.NoError(t, err)

			lib.Pause(time.Second)

			// create a test actor
			actorName := "test"
			actor := newActor()
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
		},
	)
	t.Run(
		"With Batch request happy path", func(t *testing.T) {
			// create the context
			ctx := context.TODO()
			// define the logger to use
			logger := log.DiscardLogger
			// create the actor system
			sys, err := NewActorSystem(
				"test",
				WithLogger(logger),
				WithPassivationDisabled(),
			)
			// assert there are no error
			require.NoError(t, err)

			// start the actor system
			err = sys.Start(ctx)
			assert.NoError(t, err)

			lib.Pause(time.Second)

			// create a test actor
			actorName := "test"
			actor := newActor()
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
		},
	)
	t.Run(
		"With Batch request with timeout", func(t *testing.T) {
			// create the context
			ctx := context.TODO()
			// define the logger to use
			logger := log.DiscardLogger
			// create the actor system
			sys, err := NewActorSystem(
				"test",
				WithLogger(logger),
				WithPassivationDisabled(),
			)
			// assert there are no error
			require.NoError(t, err)

			// start the actor system
			err = sys.Start(ctx)
			assert.NoError(t, err)

			lib.Pause(time.Second)

			// create a test actor
			actorName := "test"
			actor := newActor()
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
		},
	)
	t.Run(
		"With Batch request with dead actor", func(t *testing.T) {
			// create the context
			ctx := context.TODO()
			// define the logger to use
			logger := log.DiscardLogger
			// create the actor system
			sys, err := NewActorSystem(
				"test",
				WithLogger(logger),
				WithPassivationDisabled(),
			)
			// assert there are no error
			require.NoError(t, err)

			// start the actor system
			err = sys.Start(ctx)
			assert.NoError(t, err)

			lib.Pause(time.Second)

			// create a test actor
			actorName := "test"
			actor := newActor()
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
		},
	)
}

func TestTell(t *testing.T) {
	t.Run(
		"With started actor", func(t *testing.T) {
			// create the context
			ctx := context.TODO()
			// define the logger to use
			logger := log.DiscardLogger
			// create the actor system
			sys, err := NewActorSystem(
				"test",
				WithLogger(logger),
				WithPassivationDisabled(),
			)
			// assert there are no error
			require.NoError(t, err)

			// start the actor system
			err = sys.Start(ctx)
			assert.NoError(t, err)

			lib.Pause(time.Second)

			// create a test actor
			actorName := "test"
			actor := newActor()
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
		},
	)
	t.Run(
		"With stopped actor", func(t *testing.T) {
			// create the context
			ctx := context.TODO()
			// define the logger to use
			logger := log.DiscardLogger
			// create the actor system
			sys, err := NewActorSystem(
				"test",
				WithLogger(logger),
				WithPassivationDisabled(),
			)
			// assert there are no error
			require.NoError(t, err)

			// start the actor system
			err = sys.Start(ctx)
			assert.NoError(t, err)

			lib.Pause(time.Second)

			// create a test actor
			actorName := "test"
			actor := newActor()
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
		},
	)
	t.Run(
		"With invalid remote message", func(t *testing.T) {
			// create the context
			ctx := context.TODO()
			// define the logger to use
			logger := log.DiscardLogger
			// create the actor system
			sys, err := NewActorSystem(
				"test",
				WithLogger(logger),
				WithPassivationDisabled(),
			)
			// assert there are no error
			require.NoError(t, err)

			// start the actor system
			err = sys.Start(ctx)
			assert.NoError(t, err)

			lib.Pause(time.Second)

			// create a test actor
			actorName := "test"
			actor := newActor()
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
				WithPassivationDisabled(),
			)
			// assert there are no error
			require.NoError(t, err)

			// start the actor system
			err = sys.Start(ctx)
			assert.NoError(t, err)

			lib.Pause(time.Second)

			// create a test actor
			actorName := "test"
			actor := newActor()
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
		},
	)
	t.Run(
		"With Batch request with a dead actor", func(t *testing.T) {
			// create the context
			ctx := context.TODO()
			// define the logger to use
			logger := log.DiscardLogger
			// create the actor system
			sys, err := NewActorSystem(
				"test",
				WithLogger(logger),
				WithPassivationDisabled(),
			)
			// assert there are no error
			require.NoError(t, err)

			// start the actor system
			err = sys.Start(ctx)
			assert.NoError(t, err)

			lib.Pause(time.Second)

			// create a test actor
			actorName := "test"
			actor := newActor()
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
		},
	)
}
