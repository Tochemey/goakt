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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/util"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func TestDeadletter(t *testing.T) {
	t.Run("With GetDeadlettersCount", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for complete start
		util.Pause(time.Second)

		// create a deadletter subscriber
		consumer, err := sys.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create the black hole actor
		actor := &MockUnhandled{}
		actorRef, err := sys.Spawn(ctx, "actor", actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait a while
		util.Pause(time.Second)

		// every message sent to the actor will result in deadletter
		for i := 0; i < 5; i++ {
			require.NoError(t, Tell(ctx, actorRef, new(testpb.TestSend)))
		}

		util.Pause(time.Second)

		var items []*goaktpb.Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*goaktpb.Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		require.Len(t, items, 5)
		reply, err := Ask(ctx, sys.getDeadletter(), &internalpb.GetDeadlettersCount{}, 500*time.Millisecond)
		require.NoError(t, err)
		require.NotNil(t, reply)
		response, ok := reply.(*internalpb.DeadlettersCount)
		require.True(t, ok)
		require.EqualValues(t, 5, response.GetTotalCount())

		// unsubscribe the consumer
		err = sys.Unsubscribe(consumer)
		require.NoError(t, err)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With GetDeadlettersCount for a specific actor", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for complete start
		util.Pause(time.Second)

		// create the black hole actor
		actor := &MockUnhandled{}
		actorName := "actorName"
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait a while
		util.Pause(time.Second)

		// every message sent to the actor will result in deadletter
		for i := 0; i < 5; i++ {
			require.NoError(t, Tell(ctx, actorRef, new(testpb.TestSend)))
		}

		util.Pause(time.Second)

		actorID := actorRef.ID()
		reply, err := Ask(ctx, sys.getDeadletter(), &internalpb.GetDeadlettersCount{
			ActorId: &actorID,
		}, 500*time.Millisecond)
		require.NoError(t, err)
		require.NotNil(t, reply)
		response, ok := reply.(*internalpb.DeadlettersCount)
		require.True(t, ok)
		require.EqualValues(t, 5, response.GetTotalCount())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With GetDeadletters", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for complete start
		util.Pause(time.Second)

		// create a deadletter subscriber
		consumer, err := sys.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create the black hole actor
		actor := &MockUnhandled{}
		actorRef, err := sys.Spawn(ctx, "actor", actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait a while
		util.Pause(time.Second)

		// every message sent to the actor will result in deadletter
		for range 5 {
			require.NoError(t, Tell(ctx, actorRef, new(testpb.TestSend)))
		}

		util.Pause(time.Second)

		var items []*goaktpb.Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*goaktpb.Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		require.Len(t, items, 5)
		// let us empty the item list
		items = []*goaktpb.Deadletter{}

		err = Tell(ctx, sys.getDeadletter(), &internalpb.GetDeadletters{})
		require.NoError(t, err)

		util.Pause(time.Second)

		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*goaktpb.Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}
		require.Len(t, items, 1)

		// unsubscribe the consumer
		err = sys.Unsubscribe(consumer)
		require.NoError(t, err)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}
