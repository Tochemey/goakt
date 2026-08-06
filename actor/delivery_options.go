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
	"time"
)

// ReliableProducerOption configures the producer side of a reliable-delivery
// flow when spawning the producer endpoint with AsReliableProducer.
type ReliableProducerOption func(config *reliableProducerConfig)

// ReliableConsumerOption configures the consumer side of a reliable-delivery
// flow when spawning the consumer endpoint with AsReliableConsumer.
type ReliableConsumerOption func(config *reliableConsumerConfig)

// WithDurableQueue stores every produced message durably so a producer crash
// or relocation redelivers unconfirmed messages. The queue is also registered
// as a user dependency so relocation can reconstruct it by ID on any eligible
// node.
func WithDurableQueue(queue DurableProducerQueue) ReliableProducerOption {
	return func(config *reliableProducerConfig) {
		if config == nil {
			return
		}

		config.queue = queue

		if queue != nil {
			config.durableQueueID = queue.ID()
		}
	}
}

// WithQueueRetry bounds the retry policy applied to every durable queue
// operation before the producer controller raises a reliability error.
func WithQueueRetry(maxAttempts int, initialBackoff time.Duration) ReliableProducerOption {
	return func(config *reliableProducerConfig) {
		if config == nil {
			return
		}

		config.queueRetry = &reliableQueueRetryConfig{
			maxAttempts:    maxAttempts,
			initialBackoff: initialBackoff,
		}
	}
}

// WithLocalRetryInterval sets the cadence at which the producer controller
// retries an unanswered RequestNext or Stored toward the producer endpoint.
func WithLocalRetryInterval(interval time.Duration) ReliableProducerOption {
	return func(config *reliableProducerConfig) {
		if config == nil {
			return
		}

		config.localRetryInterval = interval
	}
}

// WithDeliveryConfirmation tells the producer controller to send the producer
// endpoint a DeliveryConfirmed for every message the consumer confirms, so a
// producer can report completion to whoever submitted the work. The
// notification carries no protocol obligation: it is best effort within one
// controller incarnation, it repeats when a message is redelivered and
// confirmed again, and the producer must handle it idempotently by MessageID.
func WithDeliveryConfirmation() ReliableProducerOption {
	return func(config *reliableProducerConfig) {
		if config == nil {
			return
		}

		config.deliveryConfirmation = true
	}
}

// WithChunking splits every produced payload larger than maxChunkBytes into
// parts that each consume one sequence number and are reassembled by the
// consumer controller before Delivery, so one large message can never exceed
// the remoting frame cap. The size must be in [MinChunkSize, MaxChunkSize],
// and a message must fit in the consumer's flow-control window worth of
// chunks. Combined with a durable queue, the producer controller stores the
// chunks through StoreChunked so a crash mid-message cannot mix encodings.
func WithChunking(maxChunkBytes uint32) ReliableProducerOption {
	return func(config *reliableProducerConfig) {
		if config == nil {
			return
		}

		config.maxChunkBytes = maxChunkBytes
	}
}

// WithFlowControlWindow sets the demand granted per consumer request and the
// consumer controller's receive buffer capacity. It must be in
// [1, MaxFlowControlWindow].
func WithFlowControlWindow(window int) ReliableConsumerOption {
	return func(config *reliableConsumerConfig) {
		if config == nil {
			return
		}

		config.flowControlWindow = window
	}
}

// WithResendInterval sets the cadence at which the consumer controller
// re-registers, retries the unconfirmed delivery, and requests gap resends.
func WithResendInterval(interval time.Duration) ReliableConsumerOption {
	return func(config *reliableConsumerConfig) {
		if config == nil {
			return
		}

		config.resendInterval = interval
	}
}

// AsReliableProducer spawns the actor as the producer endpoint of a reliable
// delivery flow toward the named consumer endpoint. The actor system creates
// and owns a producer controller next to the endpoint; the producer answers
// its RequestNext and Stored messages while every other message type stays
// free for business use. Reliability begins at the producer's handoff to the
// controller, not at its inbox, and reliable endpoints must be long-lived:
// finite passivation is rejected at spawn.
func AsReliableProducer(consumerName string, opts ...ReliableProducerOption) SpawnOption {
	producer := &reliableProducerConfig{
		consumerName:       consumerName,
		localRetryInterval: DefaultLocalRetryInterval,
		queueRetry: &reliableQueueRetryConfig{
			maxAttempts:    DefaultQueueRetryAttempts,
			initialBackoff: DefaultQueueRetryBackoff,
		},
	}

	for _, opt := range opts {
		opt(producer)
	}

	return spawnOption(func(config *spawnConfig) {
		config.reliableDelivery = &reliableDeliveryConfig{producer: producer}
		config.durableQueue = producer.queue
	})
}

// AsReliableConsumer spawns the actor as the consumer endpoint of a reliable
// delivery flow from the named producer endpoint. The actor system creates
// and owns a consumer controller next to the endpoint; the consumer handles
// Delivery and replies Confirmed after processing, and processing must be
// idempotent because loss or restart permits redelivery. Reliable endpoints
// must be long-lived: finite passivation is rejected at spawn.
func AsReliableConsumer(producerName string, opts ...ReliableConsumerOption) SpawnOption {
	consumer := &reliableConsumerConfig{
		producerName:      producerName,
		flowControlWindow: DefaultFlowControlWindow,
		resendInterval:    DefaultResendInterval,
	}

	for _, opt := range opts {
		opt(consumer)
	}

	return spawnOption(func(config *spawnConfig) {
		config.reliableDelivery = &reliableDeliveryConfig{consumer: consumer}
	})
}
