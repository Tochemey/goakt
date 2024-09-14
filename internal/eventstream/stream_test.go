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

package eventstream

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v2/internal/lib"
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

		require.Empty(t, broker.Subscribers())
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

		lib.Pause(time.Second)

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

		lib.Pause(time.Second)

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
