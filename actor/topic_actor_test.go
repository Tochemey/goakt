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
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func TestTopicActor(t *testing.T) {
	t.Run("With Subscribe/Unsubscribe", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		cl1, sd1 := testNATs(t, srv.Addr().String(), withTestPubSub())
		require.NotNil(t, cl1)
		require.NotNil(t, sd1)

		// create and start system cluster
		cl2, sd2 := testNATs(t, srv.Addr().String(), withTestPubSub())
		require.NotNil(t, cl2)
		require.NotNil(t, sd2)

		// create and start system cluster
		cl3, sd3 := testNATs(t, srv.Addr().String(), withTestPubSub())
		require.NotNil(t, cl3)
		require.NotNil(t, sd3)

		// create an actor on each node
		actor1, err := cl1.Spawn(ctx, "actor1", NewMockSubscriber())
		require.NoError(t, err)
		require.NotNil(t, actor1)

		actor2, err := cl2.Spawn(ctx, "actor2", NewMockSubscriber())
		require.NoError(t, err)
		require.NotNil(t, actor2)

		actor3, err := cl3.Spawn(ctx, "actor3", NewMockSubscriber())
		require.NoError(t, err)
		require.NotNil(t, actor3)

		topic := "test-topic"
		// subscribe to the topic
		err = actor1.Tell(ctx, cl1.TopicActor(), &goaktpb.Subscribe{Topic: topic})
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor1.Metric(ctx).ProcessedCount())

		err = actor2.Tell(ctx, cl2.TopicActor(), &goaktpb.Subscribe{Topic: topic})
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor2.Metric(ctx).ProcessedCount())

		err = actor3.Tell(ctx, cl3.TopicActor(), &goaktpb.Subscribe{Topic: topic})
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor3.Metric(ctx).ProcessedCount())

		// subscribe to the topic
		err = actor1.Tell(ctx, cl1.TopicActor(), &goaktpb.Unsubscribe{Topic: topic})
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 2, actor1.Metric(ctx).ProcessedCount())

		require.NoError(t, cl1.Stop(ctx))
		require.NoError(t, cl2.Stop(ctx))
		require.NoError(t, cl3.Stop(ctx))

		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("With happy path Publish", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		cl1, sd1 := testNATs(t, srv.Addr().String(), withTestPubSub())
		require.NotNil(t, cl1)
		require.NotNil(t, sd1)

		// create and start system cluster
		cl2, sd2 := testNATs(t, srv.Addr().String(), withTestPubSub())
		require.NotNil(t, cl2)
		require.NotNil(t, sd2)

		// create and start system cluster
		cl3, sd3 := testNATs(t, srv.Addr().String(), withTestPubSub())
		require.NotNil(t, cl3)
		require.NotNil(t, sd3)

		// create an actor on each node
		actor1, err := cl1.Spawn(ctx, "actor1", NewMockSubscriber())
		require.NoError(t, err)
		require.NotNil(t, actor1)

		actor2, err := cl2.Spawn(ctx, "actor2", NewMockSubscriber())
		require.NoError(t, err)
		require.NotNil(t, actor2)

		actor3, err := cl3.Spawn(ctx, "actor3", NewMockSubscriber())
		require.NoError(t, err)
		require.NotNil(t, actor3)

		topic := "test-topic"
		// subscribe to the topic
		err = actor1.Tell(ctx, cl1.TopicActor(), &goaktpb.Subscribe{Topic: topic})
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor1.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor1.Actor().(*MockSubscriber).counter.Load())

		err = actor2.Tell(ctx, cl2.TopicActor(), &goaktpb.Subscribe{Topic: topic})
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor2.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor2.Actor().(*MockSubscriber).counter.Load())

		err = actor3.Tell(ctx, cl3.TopicActor(), &goaktpb.Subscribe{Topic: topic})
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor3.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor3.Actor().(*MockSubscriber).counter.Load())

		actual := new(testpb.TestCount)
		transformed, _ := anypb.New(actual)

		// publish a message
		publisher, err := cl1.Spawn(ctx, "publisher", NewMockSubscriber())
		require.NoError(t, err)
		require.NotNil(t, publisher)

		pause.For(time.Second)

		message := &goaktpb.Publish{
			Id:      "messsage1",
			Topic:   topic,
			Message: transformed,
		}
		err = publisher.Tell(ctx, cl1.TopicActor(), message)
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 2, actor1.Actor().(*MockSubscriber).counter.Load())
		require.EqualValues(t, 2, actor2.Actor().(*MockSubscriber).counter.Load())
		require.EqualValues(t, 2, actor3.Actor().(*MockSubscriber).counter.Load())

		require.NoError(t, cl1.Stop(ctx))
		require.NoError(t, cl2.Stop(ctx))
		require.NoError(t, cl3.Stop(ctx))

		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("With unhandled", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		cl1, sd1 := testNATs(t, srv.Addr().String(), withTestPubSub())
		require.NotNil(t, cl1)
		require.NotNil(t, sd1)

		// create a deadletter subscriber
		consumer, err := cl1.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// publish a message
		publisher, err := cl1.Spawn(ctx, "publisher", NewMockSubscriber())
		require.NoError(t, err)
		require.NotNil(t, publisher)

		pause.For(time.Second)

		// this message will result in an unhandled message
		err = publisher.Tell(ctx, cl1.TopicActor(), new(testpb.TestCount))
		require.NoError(t, err)

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

		consumer.Shutdown()
		require.NoError(t, cl1.Stop(ctx))
		require.NoError(t, sd1.Close())
		srv.Shutdown()
	})

	t.Run("Without clustering", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithPubSub())

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		// wait for the actor system to be ready
		pause.For(time.Second)

		// start bunch of actors
		actor1, err := actorSystem.Spawn(ctx, "actor1", NewMockSubscriber(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, actor1)

		pause.For(500 * time.Millisecond)

		actor2, err := actorSystem.Spawn(ctx, "actor2", NewMockSubscriber(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, actor2)

		pause.For(500 * time.Millisecond)

		actor3, err := actorSystem.Spawn(ctx, "actor3", NewMockSubscriber(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, actor3)

		pause.For(500 * time.Millisecond)

		topic := "test-topic"

		// let the various actors subscribe to the topic
		err = actor1.Tell(ctx, actorSystem.TopicActor(), &goaktpb.Subscribe{Topic: topic})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor1.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor1.Actor().(*MockSubscriber).counter.Load())

		err = actor2.Tell(ctx, actorSystem.TopicActor(), &goaktpb.Subscribe{Topic: topic})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor2.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor2.Actor().(*MockSubscriber).counter.Load())

		err = actor3.Tell(ctx, actorSystem.TopicActor(), &goaktpb.Subscribe{Topic: topic})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor3.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor3.Actor().(*MockSubscriber).counter.Load())

		// create a publisher that will publish messages to the topic
		publisher, err := actorSystem.Spawn(ctx, "publisher", NewMockSubscriber())
		require.NoError(t, err)
		require.NotNil(t, publisher)

		pause.For(time.Second)

		// publish a message
		actual := new(testpb.TestCount)
		transformed, _ := anypb.New(actual)
		message := &goaktpb.Publish{
			Id:      "messsage1",
			Topic:   topic,
			Message: transformed,
		}
		err = publisher.Tell(ctx, actorSystem.TopicActor(), message)
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 2, actor1.Actor().(*MockSubscriber).counter.Load())
		require.EqualValues(t, 2, actor2.Actor().(*MockSubscriber).counter.Load())
		require.EqualValues(t, 2, actor3.Actor().(*MockSubscriber).counter.Load())

		require.NoError(t, actorSystem.Stop(ctx))
	})

	t.Run("Without a sender", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithPubSub())

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		// wait for the actor system to be ready
		pause.For(time.Second)

		// start bunch of actors
		actor1, err := actorSystem.Spawn(ctx, "actor1", NewMockSubscriber(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, actor1)

		pause.For(500 * time.Millisecond)

		actor2, err := actorSystem.Spawn(ctx, "actor2", NewMockSubscriber(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, actor2)

		pause.For(500 * time.Millisecond)

		actor3, err := actorSystem.Spawn(ctx, "actor3", NewMockSubscriber(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, actor3)

		pause.For(500 * time.Millisecond)

		topic := "test-topic"

		// let the various actors subscribe to the topic
		err = actor1.Tell(ctx, actorSystem.TopicActor(), &goaktpb.Subscribe{Topic: topic})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor1.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor1.Actor().(*MockSubscriber).counter.Load())

		err = actor2.Tell(ctx, actorSystem.TopicActor(), &goaktpb.Subscribe{Topic: topic})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor2.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor2.Actor().(*MockSubscriber).counter.Load())

		err = actor3.Tell(ctx, actorSystem.TopicActor(), &goaktpb.Subscribe{Topic: topic})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor3.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor3.Actor().(*MockSubscriber).counter.Load())

		// publish a message without a sender
		actual := new(testpb.TestCount)
		transformed, _ := anypb.New(actual)
		message := &goaktpb.Publish{
			Id:      "messsage1",
			Topic:   topic,
			Message: transformed,
		}
		err = Tell(ctx, actorSystem.TopicActor(), message) // no sender
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 2, actor1.Actor().(*MockSubscriber).counter.Load())
		require.EqualValues(t, 2, actor2.Actor().(*MockSubscriber).counter.Load())
		require.EqualValues(t, 2, actor3.Actor().(*MockSubscriber).counter.Load())

		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With handle Terminated message", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithPubSub())

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		// wait for the actor system to be ready
		pause.For(time.Second)

		// start bunch of actors
		actor1, err := actorSystem.Spawn(ctx, "actor1", NewMockSubscriber(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, actor1)

		pause.For(500 * time.Millisecond)

		actor2, err := actorSystem.Spawn(ctx, "actor2", NewMockSubscriber(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, actor2)

		pause.For(500 * time.Millisecond)

		actor3, err := actorSystem.Spawn(ctx, "actor3", NewMockSubscriber(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, actor3)

		pause.For(500 * time.Millisecond)

		topic := "test-topic"

		// let the various actors subscribe to the topic
		err = actor1.Tell(ctx, actorSystem.TopicActor(), &goaktpb.Subscribe{Topic: topic})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor1.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor1.Actor().(*MockSubscriber).counter.Load())

		err = actor2.Tell(ctx, actorSystem.TopicActor(), &goaktpb.Subscribe{Topic: topic})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor2.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor2.Actor().(*MockSubscriber).counter.Load())

		err = actor3.Tell(ctx, actorSystem.TopicActor(), &goaktpb.Subscribe{Topic: topic})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor3.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor3.Actor().(*MockSubscriber).counter.Load())

		// create a publisher that will publish messages to the topic
		publisher, err := actorSystem.Spawn(ctx, "publisher", NewMockSubscriber())
		require.NoError(t, err)
		require.NotNil(t, publisher)

		pause.For(time.Second)

		// publish a message
		actual := new(testpb.TestCount)
		transformed, _ := anypb.New(actual)
		message := &goaktpb.Publish{
			Id:      "messsage1",
			Topic:   topic,
			Message: transformed,
		}
		err = publisher.Tell(ctx, actorSystem.TopicActor(), message)
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 2, actor1.Actor().(*MockSubscriber).counter.Load())
		require.EqualValues(t, 2, actor2.Actor().(*MockSubscriber).counter.Load())
		require.EqualValues(t, 2, actor3.Actor().(*MockSubscriber).counter.Load())

		// stop actor1
		require.NoError(t, actor1.Shutdown(ctx))
		pause.For(time.Second)

		// for the sake of the test
		topicActor := actorSystem.TopicActor().Actor().(*topicActor)
		subscribers, ok := topicActor.topics.Get(topic)
		require.True(t, ok)
		require.EqualValues(t, subscribers.Len(), 2)

		require.NoError(t, actorSystem.Stop(ctx))
	})
}
