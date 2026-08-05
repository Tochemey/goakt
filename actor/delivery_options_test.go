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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/passivation"
)

func TestAsReliableProducer(t *testing.T) {
	t.Run("With defaults", func(t *testing.T) {
		config := newSpawnConfig(AsReliableProducer("orders-consumer"))
		require.NoError(t, config.Validate())

		producer := config.reliableDelivery.producer
		require.NotNil(t, producer)
		assert.Equal(t, "orders-consumer", producer.consumerName)
		assert.Equal(t, DefaultLocalRetryInterval, producer.localRetryInterval)
		assert.Equal(t, DefaultQueueRetryAttempts, producer.queueRetry.maxAttempts)
		assert.Equal(t, DefaultQueueRetryBackoff, producer.queueRetry.initialBackoff)
		assert.Empty(t, producer.durableQueueID)
		assert.Nil(t, config.durableQueue)
	})

	t.Run("With all options", func(t *testing.T) {
		queue := &mockDurableQueue{}
		config := newSpawnConfig(AsReliableProducer("orders-consumer",
			WithDurableQueue(queue),
			WithQueueRetry(5, 250*time.Millisecond),
			WithLocalRetryInterval(time.Second),
		))
		require.NoError(t, config.Validate())

		producer := config.reliableDelivery.producer
		assert.Equal(t, queue.ID(), producer.durableQueueID)
		assert.Equal(t, 5, producer.queueRetry.maxAttempts)
		assert.Equal(t, 250*time.Millisecond, producer.queueRetry.initialBackoff)
		assert.Equal(t, time.Second, producer.localRetryInterval)
		assert.Same(t, queue, config.durableQueue)
	})

	t.Run("With nil durable queue", func(t *testing.T) {
		config := newSpawnConfig(AsReliableProducer("orders-consumer", WithDurableQueue(nil)))
		require.NoError(t, config.Validate())
		assert.Empty(t, config.reliableDelivery.producer.durableQueueID)
		assert.Nil(t, config.durableQueue)
	})

	t.Run("With invalid settings", func(t *testing.T) {
		config := newSpawnConfig(AsReliableProducer(""))
		require.Error(t, config.Validate())

		config = newSpawnConfig(AsReliableProducer("orders-consumer", WithQueueRetry(0, time.Second)))
		require.Error(t, config.Validate())
	})

	t.Run("With finite passivation", func(t *testing.T) {
		config := newSpawnConfig(
			AsReliableProducer("orders-consumer"),
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(time.Minute)),
		)
		require.Error(t, config.Validate())
	})
}

func TestAsReliableConsumer(t *testing.T) {
	t.Run("With defaults", func(t *testing.T) {
		config := newSpawnConfig(AsReliableConsumer("orders-producer"))
		require.NoError(t, config.Validate())

		consumer := config.reliableDelivery.consumer
		require.NotNil(t, consumer)
		assert.Equal(t, "orders-producer", consumer.producerName)
		assert.Equal(t, DefaultFlowControlWindow, consumer.flowControlWindow)
		assert.Equal(t, DefaultResendInterval, consumer.resendInterval)
		assert.Nil(t, config.durableQueue)
	})

	t.Run("With all options", func(t *testing.T) {
		config := newSpawnConfig(AsReliableConsumer("orders-producer",
			WithFlowControlWindow(100),
			WithResendInterval(500*time.Millisecond),
		))
		require.NoError(t, config.Validate())

		consumer := config.reliableDelivery.consumer
		assert.Equal(t, 100, consumer.flowControlWindow)
		assert.Equal(t, 500*time.Millisecond, consumer.resendInterval)
	})

	t.Run("With invalid settings", func(t *testing.T) {
		config := newSpawnConfig(AsReliableConsumer(""))
		require.Error(t, config.Validate())

		config = newSpawnConfig(AsReliableConsumer("orders-producer", WithFlowControlWindow(0)))
		require.Error(t, config.Validate())

		config = newSpawnConfig(AsReliableConsumer("orders-producer", WithFlowControlWindow(MaxFlowControlWindow+1)))
		require.Error(t, config.Validate())
	})
}
