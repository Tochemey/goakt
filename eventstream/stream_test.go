package eventstream

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBroker(t *testing.T) {
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

		t.Cleanup(func() {
			broker.Shutdown()
		})
	})
	t.Run("With Unsubscription", func(t *testing.T) {
		broker := New()

		// add consumer
		cons := broker.AddSubscriber()
		require.NotNil(t, cons)
		broker.Subscribe(cons, "t1")
		broker.Subscribe(cons, "t2")

		require.EqualValues(t, 1, broker.SubscribersCount("t1"))
		require.EqualValues(t, 1, broker.SubscribersCount("t2"))

		// unsubscribe the consumer
		broker.Unsubscribe(cons, "t1")
		assert.Zero(t, broker.SubscribersCount("t1"))
		require.EqualValues(t, 1, broker.SubscribersCount("t2"))

		broker.Subscribe(cons, "t3")
		require.EqualValues(t, 1, broker.SubscribersCount("t3"))

		// remove the consumer
		broker.RemoveSubscriber(cons)
		broker.Subscribe(cons, "t4")
		assert.Zero(t, broker.SubscribersCount("t4"))

		t.Cleanup(func() {
			broker.Shutdown()
		})
	})
	t.Run("With Publication", func(t *testing.T) {
		broker := New()

		// add consumer
		cons := broker.AddSubscriber()
		require.NotNil(t, cons)
		broker.Subscribe(cons, "t1")
		broker.Subscribe(cons, "t2")

		require.EqualValues(t, 1, broker.SubscribersCount("t1"))
		require.EqualValues(t, 1, broker.SubscribersCount("t2"))

		broker.Publish("t1", "hi")
		broker.Publish("t2", "hello")

		time.Sleep(time.Second)

		var messages []*Message
		for message := range cons.Iterator() {
			messages = append(messages, message)
		}

		assert.Len(t, messages, 2)
		assert.Len(t, cons.Topics(), 2)

		t.Cleanup(func() {
			broker.Shutdown()
		})
	})
	t.Run("With Broadcast", func(t *testing.T) {
		broker := New()

		// add consumer
		cons := broker.AddSubscriber()
		require.NotNil(t, cons)
		broker.Subscribe(cons, "t1")
		broker.Subscribe(cons, "t2")

		require.EqualValues(t, 1, broker.SubscribersCount("t1"))
		require.EqualValues(t, 1, broker.SubscribersCount("t2"))

		broker.Broadcast("hi", []string{"t1", "t2"})

		time.Sleep(time.Second)

		var messages []*Message
		for message := range cons.Iterator() {
			messages = append(messages, message)
		}

		assert.Len(t, messages, 2)
		assert.Len(t, cons.Topics(), 2)

		t.Cleanup(func() {
			broker.Shutdown()
		})
	})
}
