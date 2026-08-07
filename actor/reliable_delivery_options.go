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
// node. It applies only to point-to-point producers; work-pulling uses
// WithDurableWorkQueue.
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

// WithDurableWorkQueue stores every accepted work-pulling message durably so a
// producer crash or relocation reloads unconfirmed jobs into the pending pool
// and re-dispatches them to current workers. Confirmation is per MessageID
// because workers complete out of order. The queue is also registered as a
// user dependency so relocation can reconstruct it by ID on any eligible node.
func WithDurableWorkQueue(queue DurableWorkQueue) ReliableProducerOption {
	return func(config *reliableProducerConfig) {
		if config == nil {
			return
		}

		config.workQueue = queue

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

// WithRemoteConsumer names the remoting address of the node hosting the
// consumer endpoint, forming a remoting-only flow: the producer's controller
// resolves the consumer's controller by asking that node directly instead of
// the cluster registry. The producer endpoint must be spawned locally on a
// system with remoting enabled and clustering disabled, and the consumer
// endpoint must carry the mirror WithRemoteProducer option. Peer loss is
// recovered by restarting the peer process at this address; the ordinary
// registration resync then reconnects the flow.
func WithRemoteConsumer(host string, port int) ReliableProducerOption {
	return func(config *reliableProducerConfig) {
		if config == nil {
			return
		}

		config.consumerAddress = &reliablePeerAddress{host: host, port: port}
	}
}

// WithRemoteProducer names the remoting address of the node hosting the
// producer endpoint, forming a remoting-only flow: the consumer's controller
// resolves the producer's controller by asking that node directly instead of
// the cluster registry. The consumer endpoint must be spawned locally on a
// system with remoting enabled and clustering disabled, and the producer
// endpoint must carry the mirror WithRemoteConsumer option. Peer loss is
// recovered by restarting the peer process at this address; the ordinary
// registration resync then reconnects the flow.
func WithRemoteProducer(host string, port int) ReliableConsumerOption {
	return func(config *reliableConsumerConfig) {
		if config == nil {
			return
		}

		config.producerAddress = &reliablePeerAddress{host: host, port: port}
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

// AsWorkPullingProducer spawns the actor as the producer endpoint of a
// work-pulling reliable delivery flow. Authorized workers are discovered
// through registration fencing rather than a peer name. The actor system
// creates and owns a work-pulling producer controller next to the endpoint;
// the producer answers the same RequestNext/Produced/Stored/StoredAck
// contract as point-to-point. WithChunking and WithDurableQueue are
// rejected; durability uses WithDurableWorkQueue. Reliable endpoints must be
// long-lived: finite passivation is rejected at spawn.
func AsWorkPullingProducer(opts ...ReliableProducerOption) SpawnOption {
	producer := &reliableProducerConfig{
		workPulling:        true,
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
		config.durableWorkQueue = producer.workQueue
	})
}

// AsWorkPullingWorker spawns the actor as a work-pulling worker endpoint that
// pulls jobs from the named producer. The worker runs the unchanged consumer
// controller and answers Delivery with Confirmed; processing must be
// idempotent because a lost worker requeues unconfirmed work under the same
// MessageID. Reliable endpoints must be long-lived: finite passivation is
// rejected at spawn.
func AsWorkPullingWorker(producerName string, opts ...ReliableConsumerOption) SpawnOption {
	return AsReliableConsumer(producerName, opts...)
}
