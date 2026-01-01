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

package eventstream

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStream(t *testing.T) {
	t.Run("Message accessors", func(t *testing.T) {
		msg := NewMessage("topic", "payload")
		require.Equal(t, "topic", msg.Topic())
		require.Equal(t, "payload", msg.Payload())
	})

	t.Run("With Subscriber lifecycle", func(t *testing.T) {
		sub := newSubscriber()
		require.NotEmpty(t, sub.ID())
		require.True(t, sub.Active())

		// empty iterator should close immediately
		for range sub.Iterator() {
			require.Fail(t, "iterator should be empty on new subscriber")
		}

		sub.subscribe("a")
		sub.subscribe("b")
		topics := sub.Topics()
		require.Len(t, topics, 2)

		sub.signal(NewMessage("a", "one"))
		sub.signal(NewMessage("b", "two"))

		var seen []*Message
		for msg := range sub.Iterator() {
			seen = append(seen, msg)
		}
		require.Len(t, seen, 2)

		sub.unsubscribe("a")
		topics = sub.Topics()
		require.Len(t, topics, 1)

		sub.Shutdown()
		require.False(t, sub.Active())

		// signals after shutdown should be ignored
		sub.signal(NewMessage("b", "three"))
		for range sub.Iterator() {
			require.Fail(t, "iterator should be empty after shutdown")
		}

		// enqueue nil while active to cover nil dequeue branch before shutdown
		activeSub := newSubscriber()
		activeSub.messages.Enqueue(nil)
		for range activeSub.Iterator() {
			require.Fail(t, "iterator should break on nil dequeue while active")
		}

		// cover nil dequeue path: manually drop a nil into the queue
		sub.messages.Enqueue(nil)
		for msg := range sub.Iterator() {
			require.Nil(t, msg)
		}
	})

	t.Run("With Subscription", func(t *testing.T) {
		broker := New()

		// add consumer
		cons := broker.AddSubscriber()
		require.NotNil(t, cons)
		broker.Subscribe(cons, "t1")
		broker.Subscribe(cons, "t2")

		require.EqualValues(t, 1, broker.SubscribersCount("t1"))
		require.EqualValues(t, 1, broker.SubscribersCount("t2"))

		// remove the consumer
		broker.RemoveSubscriber(cons)
		assert.Zero(t, broker.SubscribersCount("t1"))
		assert.Zero(t, broker.SubscribersCount("t2"))

		broker.Subscribe(cons, "t3")
		assert.Zero(t, broker.SubscribersCount("t3"))

		broker.Close()
	})
	t.Run("With Unsubscription", func(t *testing.T) {
		broker := New()

		// add consumer
		cons := broker.AddSubscriber()
		require.NotNil(t, cons)
		broker.Subscribe(cons, "t1")
		broker.Subscribe(cons, "t2")

		sub2 := broker.AddSubscriber()
		require.NotNil(t, sub2)
		broker.Subscribe(sub2, "t1")

		require.EqualValues(t, 2, broker.SubscribersCount("t1"))
		require.EqualValues(t, 1, broker.SubscribersCount("t2"))

		sub2.Shutdown()

		// Unsubscribe the consumer
		broker.Unsubscribe(cons, "t1")
		broker.Unsubscribe(sub2, "t1")
		require.Zero(t, broker.SubscribersCount("t1"))
		require.EqualValues(t, 1, broker.SubscribersCount("t2"))

		broker.Subscribe(cons, "t3")
		require.EqualValues(t, 1, broker.SubscribersCount("t3"))

		// remove the consumer
		broker.RemoveSubscriber(cons)
		broker.Subscribe(cons, "t4")
		assert.Zero(t, broker.SubscribersCount("t4"))

		broker.Close()
	})

	t.Run("Publish skips inactive subscribers and topics with no subscribers", func(t *testing.T) {
		broker := New()

		// publish without subscribers should be a no-op
		broker.Publish("unused", "ignored")

		active := broker.AddSubscriber()
		inactive := broker.AddSubscriber()
		broker.Subscribe(active, "t1")
		broker.Subscribe(inactive, "t1")
		inactive.Shutdown()

		broker.Publish("t1", "hi")
		time.Sleep(50 * time.Millisecond)

		var activeMsgs []*Message
		for msg := range active.Iterator() {
			activeMsgs = append(activeMsgs, msg)
		}
		assert.Len(t, activeMsgs, 1)

		for range inactive.Iterator() {
			require.Fail(t, "inactive subscriber should receive no messages")
		}

		broker.Close()
	})

	t.Run("Close shuts down all subscribers", func(t *testing.T) {
		broker := New()
		sub1 := broker.AddSubscriber()
		sub2 := broker.AddSubscriber()
		broker.Subscribe(sub1, "t1")
		broker.Subscribe(sub2, "t1")

		broker.Close()

		require.False(t, sub1.Active())
		require.False(t, sub2.Active())
	})

	t.Run("With Publication", func(t *testing.T) {
		broker := New()

		// add consumer
		cons := broker.AddSubscriber()
		require.NotNil(t, cons)
		broker.Subscribe(cons, "t1")
		broker.Subscribe(cons, "t2")

		sub2 := broker.AddSubscriber()
		require.NotNil(t, sub2)
		broker.Subscribe(sub2, "t1")

		require.EqualValues(t, 2, broker.SubscribersCount("t1"))
		require.EqualValues(t, 1, broker.SubscribersCount("t2"))

		sub2.Shutdown()

		broker.Publish("t1", "hi")
		broker.Publish("t2", "hello")

		time.Sleep(time.Second)

		var messages []*Message
		for message := range cons.Iterator() {
			require.NotNil(t, message)
			require.NotNil(t, message.Topic())
			require.NotNil(t, message.Payload())
			messages = append(messages, message)
		}

		assert.Len(t, messages, 2)
		assert.Len(t, cons.Topics(), 2)

		broker.Close()
	})
	t.Run("With Broadcast", func(t *testing.T) {
		broker := New()

		// add consumer
		cons := broker.AddSubscriber()
		require.NotNil(t, cons)
		broker.Subscribe(cons, "t1")
		broker.Subscribe(cons, "t2")

		sub2 := broker.AddSubscriber()
		require.NotNil(t, sub2)
		broker.Subscribe(sub2, "t1")

		require.EqualValues(t, 2, broker.SubscribersCount("t1"))
		require.EqualValues(t, 1, broker.SubscribersCount("t2"))

		sub2.Shutdown()

		broker.Broadcast("hi", []string{"t1", "t2"})

		time.Sleep(time.Second)

		var messages []*Message
		for message := range cons.Iterator() {
			messages = append(messages, message)
		}

		assert.Len(t, messages, 2)
		assert.Len(t, cons.Topics(), 2)

		broker.Close()
	})
}
