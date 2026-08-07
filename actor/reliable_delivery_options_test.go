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
		assert.Equal(t, DefaultReliableProducerRetryInterval, producer.retryInterval)
		assert.Equal(t, DefaultReliableQueueRetryAttempts, producer.queueRetry.maxAttempts)
		assert.Equal(t, DefaultReliableQueueRetryBackoff, producer.queueRetry.initialBackoff)
		assert.Empty(t, producer.durableQueueID)
		assert.Nil(t, config.durableQueue)
		assert.False(t, producer.deliveryConfirmation)
	})

	t.Run("With all options", func(t *testing.T) {
		queue := &mockDurableQueue{}
		config := newSpawnConfig(AsReliableProducer("orders-consumer",
			WithReliableDurableQueue(queue),
			WithReliableQueueRetry(5, 250*time.Millisecond),
			WithReliableRetryInterval(time.Second),
			WithReliableDeliveryConfirmation(),
		))
		require.NoError(t, config.Validate())

		producer := config.reliableDelivery.producer
		assert.Equal(t, queue.ID(), producer.durableQueueID)
		assert.Equal(t, 5, producer.queueRetry.maxAttempts)
		assert.Equal(t, 250*time.Millisecond, producer.queueRetry.initialBackoff)
		assert.Equal(t, time.Second, producer.retryInterval)
		assert.Same(t, queue, config.durableQueue)
		assert.True(t, producer.deliveryConfirmation)
	})

	t.Run("With chunking", func(t *testing.T) {
		config := newSpawnConfig(AsReliableProducer("orders-consumer", WithReliableChunking(64*1024)))
		require.NoError(t, config.Validate())
		assert.EqualValues(t, 64*1024, config.reliableDelivery.producer.maxChunkBytes)
	})

	t.Run("With a chunk size below the minimum", func(t *testing.T) {
		config := newSpawnConfig(AsReliableProducer("orders-consumer", WithReliableChunking(MinReliableChunkSize-1)))
		assert.ErrorContains(t, config.Validate(), "chunk size must be in")
	})

	t.Run("With a chunk size above the maximum", func(t *testing.T) {
		config := newSpawnConfig(AsReliableProducer("orders-consumer", WithReliableChunking(MaxReliableChunkSize+1)))
		assert.ErrorContains(t, config.Validate(), "chunk size must be in")
	})

	t.Run("With chunking and a durable queue", func(t *testing.T) {
		config := newSpawnConfig(AsReliableProducer("orders-consumer", WithReliableChunking(MinReliableChunkSize), WithReliableDurableQueue(&mockDurableQueue{})))
		require.NoError(t, config.Validate())
		assert.EqualValues(t, MinReliableChunkSize, config.reliableDelivery.producer.maxChunkBytes)
		assert.NotNil(t, config.durableQueue)
	})

	t.Run("With nil durable queue", func(t *testing.T) {
		config := newSpawnConfig(AsReliableProducer("orders-consumer", WithReliableDurableQueue(nil)))
		require.NoError(t, config.Validate())
		assert.Empty(t, config.reliableDelivery.producer.durableQueueID)
		assert.Nil(t, config.durableQueue)
	})

	t.Run("With invalid settings", func(t *testing.T) {
		config := newSpawnConfig(AsReliableProducer(""))
		require.Error(t, config.Validate())

		config = newSpawnConfig(AsReliableProducer("orders-consumer", WithReliableQueueRetry(0, time.Second)))
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
		assert.Equal(t, DefaultReliableFlowControlWindow, consumer.flowControlWindow)
		assert.Equal(t, DefaultReliableResendInterval, consumer.resendInterval)
		assert.Nil(t, config.durableQueue)
	})

	t.Run("With all options", func(t *testing.T) {
		config := newSpawnConfig(AsReliableConsumer("orders-producer",
			WithReliableFlowControlWindow(100),
			WithReliableResendInterval(500*time.Millisecond),
		))
		require.NoError(t, config.Validate())

		consumer := config.reliableDelivery.consumer
		assert.Equal(t, 100, consumer.flowControlWindow)
		assert.Equal(t, 500*time.Millisecond, consumer.resendInterval)
	})

	t.Run("With invalid settings", func(t *testing.T) {
		config := newSpawnConfig(AsReliableConsumer(""))
		require.Error(t, config.Validate())

		config = newSpawnConfig(AsReliableConsumer("orders-producer", WithReliableFlowControlWindow(0)))
		require.Error(t, config.Validate())

		config = newSpawnConfig(AsReliableConsumer("orders-producer", WithReliableFlowControlWindow(MaxReliableFlowControlWindow+1)))
		require.Error(t, config.Validate())
	})
}

func TestReliableOptionsTolerateNilConfig(t *testing.T) {
	// the option closures run against caller-provided state, so a nil
	// configuration must be a no-op rather than a panic
	producerOptions := []ReliableProducerOption{
		WithReliableDurableQueue(&mockDurableQueue{}),
		WithReliableDurableWorkQueue(&mockDurableWorkQueue{}),
		WithReliableQueueRetry(3, 100*time.Millisecond),
		WithReliableRetryInterval(time.Second),
		WithReliableDeliveryConfirmation(),
		WithReliableChunking(MinReliableChunkSize),
		WithReliableRemoteConsumer("127.0.0.1", 9000),
	}

	for _, option := range producerOptions {
		assert.NotPanics(t, func() { option(nil) })
	}

	consumerOptions := []ReliableConsumerOption{
		WithReliableRemoteProducer("127.0.0.1", 9000),
		WithReliableFlowControlWindow(10),
		WithReliableResendInterval(time.Second),
	}

	for _, option := range consumerOptions {
		assert.NotPanics(t, func() { option(nil) })
	}
}
