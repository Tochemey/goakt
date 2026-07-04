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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestSubscribeTopic(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithPubSub())
		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		var mu sync.Mutex
		var received []proto.Message

		topic := "test-topic"
		sub, err := actorSystem.SubscribeTopic(topic, func(_ context.Context, message proto.Message) {
			mu.Lock()
			received = append(received, message)
			mu.Unlock()
		})
		require.NoError(t, err)
		require.NotNil(t, sub)
		require.Equal(t, topic, sub.Topic())

		pause.For(500 * time.Millisecond)

		publisher, err := actorSystem.Spawn(ctx, "publisher", NewMockSubscriber())
		require.NoError(t, err)

		message := NewPublish("message1", topic, new(testpb.TestCount))
		require.NoError(t, publisher.Tell(ctx, actorSystem.TopicActor(), message))

		pause.For(500 * time.Millisecond)

		mu.Lock()
		require.Len(t, received, 1)
		assert.True(t, proto.Equal(new(testpb.TestCount), received[0]))
		mu.Unlock()

		require.NoError(t, sub.Close())
		require.NoError(t, actorSystem.Stop(ctx))
	})

	t.Run("With Unsubscribe stopping delivery", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithPubSub())
		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		var counter atomic.Int64
		topic := "test-topic"
		sub, err := actorSystem.SubscribeTopic(topic, func(context.Context, proto.Message) {
			counter.Inc()
		})
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		publisher, err := actorSystem.Spawn(ctx, "publisher", NewMockSubscriber())
		require.NoError(t, err)

		require.NoError(t, publisher.Tell(ctx, actorSystem.TopicActor(), NewPublish("message1", topic, new(testpb.TestCount))))
		pause.For(500 * time.Millisecond)
		require.EqualValues(t, 1, counter.Load())

		// unsubscribe: the bridge actor is shut down and the topic actor's death
		// watch removes it from the topic's subscriber registry
		require.NoError(t, sub.Unsubscribe())
		// calling it again is a no-op and must not error
		require.NoError(t, sub.Unsubscribe())
		pause.For(500 * time.Millisecond)

		topicActor := actorSystem.TopicActor().Actor().(*topicActor)
		subscribers, ok := topicActor.topics.Get(topic)
		require.True(t, ok)
		require.Zero(t, subscribers.Len())

		require.NoError(t, publisher.Tell(ctx, actorSystem.TopicActor(), NewPublish("message2", topic, new(testpb.TestCount))))
		pause.For(500 * time.Millisecond)
		require.EqualValues(t, 1, counter.Load())

		require.NoError(t, actorSystem.Stop(ctx))
	})

	t.Run("With a panicking handler resumed and not killed", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithPubSub())
		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		var counter atomic.Int64
		topic := "test-topic"
		sub, err := actorSystem.SubscribeTopic(topic, func(context.Context, proto.Message) {
			counter.Inc()
			panic("boom")
		})
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		publisher, err := actorSystem.Spawn(ctx, "publisher", NewMockSubscriber())
		require.NoError(t, err)

		require.NoError(t, publisher.Tell(ctx, actorSystem.TopicActor(), NewPublish("message1", topic, new(testpb.TestCount))))
		pause.For(500 * time.Millisecond)
		require.EqualValues(t, 1, counter.Load())

		// the bridge actor must still be alive and subscribed after the panic
		require.NoError(t, publisher.Tell(ctx, actorSystem.TopicActor(), NewPublish("message2", topic, new(testpb.TestCount))))
		pause.For(500 * time.Millisecond)
		require.EqualValues(t, 2, counter.Load())

		require.NoError(t, sub.Close())
		require.NoError(t, actorSystem.Stop(ctx))
	})

	t.Run("With a non-proto message dropped without killing the bridge", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithPubSub())
		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		var counter atomic.Int64
		topic := "test-topic"
		sub, err := actorSystem.SubscribeTopic(topic, func(context.Context, proto.Message) {
			counter.Inc()
		})
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		publisher, err := actorSystem.Spawn(ctx, "publisher", NewMockSubscriber())
		require.NoError(t, err)

		// a locally published non-proto payload cannot be forwarded to the handler;
		// it must be dropped without affecting the bridge
		require.NoError(t, publisher.Tell(ctx, actorSystem.TopicActor(), NewPublish("message1", topic, "not-a-proto-message")))
		pause.For(500 * time.Millisecond)
		require.Zero(t, counter.Load())

		require.NoError(t, publisher.Tell(ctx, actorSystem.TopicActor(), NewPublish("message2", topic, new(testpb.TestCount))))
		pause.For(500 * time.Millisecond)
		require.EqualValues(t, 1, counter.Load())

		require.NoError(t, sub.Close())
		require.NoError(t, actorSystem.Stop(ctx))
	})

	t.Run("With PubSub disabled", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		sub, err := actorSystem.SubscribeTopic("test-topic", func(context.Context, proto.Message) {})
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrPubSubDisabled)
		require.Nil(t, sub)

		require.NoError(t, actorSystem.Stop(ctx))
	})

	t.Run("With a nil handler", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithPubSub())
		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		sub, err := actorSystem.SubscribeTopic("test-topic", nil)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrSubscribeHandlerRequired)
		require.Nil(t, sub)

		require.NoError(t, actorSystem.Stop(ctx))
	})

	t.Run("With the actor system not started", func(t *testing.T) {
		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithPubSub())
		require.NoError(t, err)

		sub, err := actorSystem.SubscribeTopic("test-topic", func(context.Context, proto.Message) {})
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
		require.Nil(t, sub)
	})
}
