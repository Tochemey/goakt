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
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
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
		err = actor1.Tell(ctx, cl1.TopicActor(), NewSubscribe(topic))
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor1.Metric(ctx).ProcessedCount())

		err = actor2.Tell(ctx, cl2.TopicActor(), NewSubscribe(topic))
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor2.Metric(ctx).ProcessedCount())

		err = actor3.Tell(ctx, cl3.TopicActor(), NewSubscribe(topic))
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor3.Metric(ctx).ProcessedCount())

		// subscribe to the topic
		err = actor1.Tell(ctx, cl1.TopicActor(), NewUnsubscribe(topic))
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
		err = actor1.Tell(ctx, cl1.TopicActor(), NewSubscribe(topic))
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor1.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor1.Actor().(*MockSubscriber).counter.Load())

		err = actor2.Tell(ctx, cl2.TopicActor(), NewSubscribe(topic))
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor2.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor2.Actor().(*MockSubscriber).counter.Load())

		err = actor3.Tell(ctx, cl3.TopicActor(), NewSubscribe(topic))
		require.NoError(t, err)

		pause.For(time.Second)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor3.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor3.Actor().(*MockSubscriber).counter.Load())

		// publish a message
		publisher, err := cl1.Spawn(ctx, "publisher", NewMockSubscriber())
		require.NoError(t, err)
		require.NotNil(t, publisher)

		pause.For(time.Second)

		message := NewPublish("messsage1", topic, new(testpb.TestCount))
		err = publisher.Tell(ctx, cl1.TopicActor(), message)
		require.NoError(t, err)

		pause.For(time.Second)

		// let's re-publish the same message, this should be ignored
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

		var items []*Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*Deadletter)
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
		err = actor1.Tell(ctx, actorSystem.TopicActor(), NewSubscribe(topic))
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor1.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor1.Actor().(*MockSubscriber).counter.Load())

		err = actor2.Tell(ctx, actorSystem.TopicActor(), NewSubscribe(topic))
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor2.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor2.Actor().(*MockSubscriber).counter.Load())

		err = actor3.Tell(ctx, actorSystem.TopicActor(), NewSubscribe(topic))
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
		message := NewPublish("messsage1", topic, new(testpb.TestCount))
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
		err = actor1.Tell(ctx, actorSystem.TopicActor(), NewSubscribe(topic))
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor1.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor1.Actor().(*MockSubscriber).counter.Load())

		err = actor2.Tell(ctx, actorSystem.TopicActor(), NewSubscribe(topic))
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor2.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor2.Actor().(*MockSubscriber).counter.Load())

		err = actor3.Tell(ctx, actorSystem.TopicActor(), NewSubscribe(topic))
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor3.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor3.Actor().(*MockSubscriber).counter.Load())

		// publish a message without a sender
		message := NewPublish("messsage1", topic, new(testpb.TestCount))
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
		err = actor1.Tell(ctx, actorSystem.TopicActor(), NewSubscribe(topic))
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor1.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor1.Actor().(*MockSubscriber).counter.Load())

		err = actor2.Tell(ctx, actorSystem.TopicActor(), NewSubscribe(topic))
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// make sure we receive the subscribe ack message
		require.EqualValues(t, 1, actor2.Metric(ctx).ProcessedCount())
		require.EqualValues(t, 1, actor2.Actor().(*MockSubscriber).counter.Load())

		err = actor3.Tell(ctx, actorSystem.TopicActor(), NewSubscribe(topic))
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
		message := NewPublish("messsage1", topic, new(testpb.TestCount))
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

		// the same cleanup must be observable through the public API, not
		// just the internal subscriber map
		count, err := actorSystem.TopicSubscriberCount(ctx, topic, time.Second)
		require.NoError(t, err)
		require.EqualValues(t, 2, count)

		require.NoError(t, actorSystem.Stop(ctx))
	})

	t.Run("With message retention bounding the dedup state and allowing redelivery", func(t *testing.T) {
		ctx := context.Background()
		retention := 500 * time.Millisecond
		actorSystem, _ := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger),
			WithPubSub(),
			WithMessageRetention(retention),
		)

		// start the actor system
		err := actorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the actor system to be ready
		pause.For(time.Second)

		subscriber, err := actorSystem.Spawn(ctx, "subscriber", NewMockSubscriber(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, subscriber)

		pause.For(500 * time.Millisecond)

		topic := "test-topic"
		err = subscriber.Tell(ctx, actorSystem.TopicActor(), NewSubscribe(topic))
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// the subscribe ack increments the counter to 1
		require.EqualValues(t, 1, subscriber.Actor().(*MockSubscriber).counter.Load())

		publisher, err := actorSystem.Spawn(ctx, "publisher", NewMockSubscriber())
		require.NoError(t, err)
		require.NotNil(t, publisher)

		pause.For(time.Second)

		message := NewPublish("message1", topic, new(testpb.TestCount))

		// the first publish is delivered
		require.NoError(t, publisher.Tell(ctx, actorSystem.TopicActor(), message))
		pause.For(200 * time.Millisecond)
		require.EqualValues(t, 2, subscriber.Actor().(*MockSubscriber).counter.Load())

		// an immediate re-publish within the retention window is deduplicated
		require.NoError(t, publisher.Tell(ctx, actorSystem.TopicActor(), message))
		pause.For(200 * time.Millisecond)
		require.EqualValues(t, 2, subscriber.Actor().(*MockSubscriber).counter.Load())

		// once the retention window elapses the dedup entry expires, so the same
		// message is delivered again instead of being suppressed forever
		pause.For(retention + 500*time.Millisecond)
		require.NoError(t, publisher.Tell(ctx, actorSystem.TopicActor(), message))
		pause.For(300 * time.Millisecond)
		require.EqualValues(t, 3, subscriber.Actor().(*MockSubscriber).counter.Load())

		// the dedup state stays bounded: expired entries do not accumulate
		topicActor := actorSystem.TopicActor().Actor().(*topicActor)
		require.LessOrEqual(t, topicActor.processed.Len(), 1)

		require.NoError(t, actorSystem.Stop(ctx))
	})

	t.Run("With attempt to shutdown when system is not shutting down", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithPubSub())

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		// wait for the actor system to be ready
		pause.For(time.Second)

		// assert the topic actor is running
		require.True(t, actorSystem.TopicActor().IsRunning())

		// attempt to shutdown the topic actor
		err = actorSystem.TopicActor().Shutdown(ctx)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrShutdownForbidden)

		require.NoError(t, actorSystem.Stop(ctx))
	})

	t.Run("With TopicSubscribers, TopicSubscriberCount and Topics without clustering", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithPubSub())

		// start the actor system
		err := actorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the actor system to be ready
		pause.For(time.Second)

		// no subscriber yet: the topic is unknown
		count, err := actorSystem.TopicSubscriberCount(ctx, "test-topic", time.Second)
		require.NoError(t, err)
		require.Zero(t, count)

		topics, err := actorSystem.Topics(ctx, time.Second)
		require.NoError(t, err)
		require.Empty(t, topics)

		actor1, err := actorSystem.Spawn(ctx, "actor1", NewMockSubscriber(), WithLongLived())
		require.NoError(t, err)

		actor2, err := actorSystem.Spawn(ctx, "actor2", NewMockSubscriber(), WithLongLived())
		require.NoError(t, err)

		topic := "test-topic"
		require.NoError(t, actor1.Tell(ctx, actorSystem.TopicActor(), NewSubscribe(topic)))
		pause.For(500 * time.Millisecond)
		require.NoError(t, actor2.Tell(ctx, actorSystem.TopicActor(), NewSubscribe(topic)))
		pause.For(500 * time.Millisecond)

		count, err = actorSystem.TopicSubscriberCount(ctx, topic, time.Second)
		require.NoError(t, err)
		require.EqualValues(t, 2, count)

		subscribers, err := actorSystem.TopicSubscribers(ctx, topic, time.Second)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{actor1.ID(), actor2.ID()}, subscribers)

		topics, err = actorSystem.Topics(ctx, time.Second)
		require.NoError(t, err)
		require.Equal(t, []string{topic}, topics)

		// unsubscribe one actor and check the counts are updated
		require.NoError(t, actor1.Tell(ctx, actorSystem.TopicActor(), NewUnsubscribe(topic)))
		pause.For(500 * time.Millisecond)

		count, err = actorSystem.TopicSubscriberCount(ctx, topic, time.Second)
		require.NoError(t, err)
		require.EqualValues(t, 1, count)

		subscribers, err = actorSystem.TopicSubscribers(ctx, topic, time.Second)
		require.NoError(t, err)
		require.Equal(t, []string{actor2.ID()}, subscribers)

		// a topic nobody ever subscribed to reports zero subscribers, not an error
		count, err = actorSystem.TopicSubscriberCount(ctx, "unknown-topic", time.Second)
		require.NoError(t, err)
		require.Zero(t, count)

		require.NoError(t, actorSystem.Stop(ctx))
	})

	t.Run("With TopicSubscribers when the topic actor is not started", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		require.NoError(t, actorSystem.Start(ctx))
		pause.For(time.Second)

		require.Nil(t, actorSystem.TopicActor())

		_, err := actorSystem.TopicSubscribers(ctx, "test-topic", time.Second)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrTopicActorNotStarted)

		_, err = actorSystem.TopicSubscriberCount(ctx, "test-topic", time.Second)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrTopicActorNotStarted)

		_, err = actorSystem.Topics(ctx, time.Second)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrTopicActorNotStarted)

		require.NoError(t, actorSystem.Stop(ctx))
	})
}
