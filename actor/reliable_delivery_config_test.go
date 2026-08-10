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
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"google.golang.org/protobuf/proto"
)

// producerDeliveryConfig builds a complete producer endpoint configuration.
func producerDeliveryConfig(consumerName string) *reliableDeliveryConfig {
	return &reliableDeliveryConfig{
		producer: &reliableProducerConfig{
			consumerName:  consumerName,
			retryInterval: DefaultReliableProducerRetryInterval,
			queueRetry: &reliableQueueRetryConfig{
				maxAttempts:    DefaultReliableQueueRetryAttempts,
				initialBackoff: DefaultReliableQueueRetryBackoff,
			},
		},
	}
}

// consumerDeliveryConfig builds a complete consumer endpoint configuration.
func consumerDeliveryConfig(producerName string) *reliableDeliveryConfig {
	return &reliableDeliveryConfig{
		consumer: &reliableConsumerConfig{
			producerName:      producerName,
			flowControlWindow: 50,
			resendInterval:    DefaultReliableResendInterval,
		},
	}
}

func TestReliableDeliveryConfigValidate(t *testing.T) {
	tests := map[string]struct {
		config  *reliableDeliveryConfig
		invalid bool
	}{
		"valid producer": {
			config: producerDeliveryConfig("consumer"),
		},
		"valid work-pulling producer": {
			config: workPullingProducerConfig(),
		},
		"valid producer with options": {
			config: &reliableDeliveryConfig{
				producer: &reliableProducerConfig{
					consumerName:   "consumer",
					durableQueueID: "ordersQueue",
					queueRetry: &reliableQueueRetryConfig{
						maxAttempts:    3,
						initialBackoff: 100 * time.Millisecond,
					},
					retryInterval: 500 * time.Millisecond,
				},
			},
		},
		"valid consumer": {
			config: consumerDeliveryConfig("producer"),
		},
		"missing endpoint side": {
			config:  &reliableDeliveryConfig{},
			invalid: true,
		},
		"both endpoint sides": {
			config: &reliableDeliveryConfig{
				producer: &reliableProducerConfig{consumerName: "consumer"},
				consumer: &reliableConsumerConfig{producerName: "producer", flowControlWindow: 50},
			},
			invalid: true,
		},
		"producer with blank consumer name": {
			config:  producerDeliveryConfig("  "),
			invalid: true,
		},
		"work-pulling producer with consumer name": {
			config: func() *reliableDeliveryConfig {
				config := workPullingProducerConfig()
				config.producer.consumerName = "worker"
				return config
			}(),
			invalid: true,
		},
		"work-pulling producer with chunking": {
			config: func() *reliableDeliveryConfig {
				config := workPullingProducerConfig()
				config.producer.maxChunkBytes = MinReliableChunkSize
				return config
			}(),
			invalid: true,
		},
		"point-to-point producer with durable work queue": {
			config: func() *reliableDeliveryConfig {
				config := producerDeliveryConfig("consumer")
				config.producer.workQueue = &mockDurableWorkQueue{}
				return config
			}(),
			invalid: true,
		},
		"producer with reserved consumer name": {
			config:  producerDeliveryConfig("GoAktConsumer"),
			invalid: true,
		},
		"producer with negative local retry interval": {
			config: func() *reliableDeliveryConfig {
				config := producerDeliveryConfig("consumer")
				config.producer.retryInterval = -time.Second
				return config
			}(),
			invalid: true,
		},
		"producer with missing queue retry policy": {
			config: func() *reliableDeliveryConfig {
				config := producerDeliveryConfig("consumer")
				config.producer.queueRetry = nil
				return config
			}(),
			invalid: true,
		},
		"producer with zero queue retry attempts": {
			config: func() *reliableDeliveryConfig {
				config := producerDeliveryConfig("consumer")
				config.producer.queueRetry.maxAttempts = 0
				return config
			}(),
			invalid: true,
		},
		"producer with negative queue retry backoff": {
			config: func() *reliableDeliveryConfig {
				config := producerDeliveryConfig("consumer")
				config.producer.queueRetry.initialBackoff = -time.Millisecond
				return config
			}(),
			invalid: true,
		},
		"producer with invalid durable queue ID": {
			config: func() *reliableDeliveryConfig {
				config := producerDeliveryConfig("consumer")
				config.producer.durableQueueID = "bad id!"
				return config
			}(),
			invalid: true,
		},
		"consumer with blank producer name": {
			config:  consumerDeliveryConfig(""),
			invalid: true,
		},
		"consumer with zero flow control window": {
			config: &reliableDeliveryConfig{
				consumer: &reliableConsumerConfig{
					producerName: "producer",
				},
			},
			invalid: true,
		},
		"consumer with oversized flow control window": {
			config: &reliableDeliveryConfig{
				consumer: &reliableConsumerConfig{
					producerName:      "producer",
					flowControlWindow: MaxReliableFlowControlWindow + 1,
				},
			},
			invalid: true,
		},
		"consumer with negative resend interval": {
			config: &reliableDeliveryConfig{
				consumer: &reliableConsumerConfig{
					producerName:      "producer",
					flowControlWindow: 50,
					resendInterval:    -time.Second,
				},
			},
			invalid: true,
		},
		"consumer with zero resend interval": {
			config: &reliableDeliveryConfig{
				consumer: &reliableConsumerConfig{
					producerName:      "producer",
					flowControlWindow: 50,
				},
			},
			invalid: true,
		},
		"producer with remote consumer address": {
			config: func() *reliableDeliveryConfig {
				config := producerDeliveryConfig("consumer")
				config.producer.consumerAddress = &reliablePeerAddress{host: "127.0.0.1", port: 2280}
				return config
			}(),
		},
		"producer with blank remote consumer host": {
			config: func() *reliableDeliveryConfig {
				config := producerDeliveryConfig("consumer")
				config.producer.consumerAddress = &reliablePeerAddress{host: "  ", port: 2280}
				return config
			}(),
			invalid: true,
		},
		"producer with invalid remote consumer port": {
			config: func() *reliableDeliveryConfig {
				config := producerDeliveryConfig("consumer")
				config.producer.consumerAddress = &reliablePeerAddress{host: "127.0.0.1", port: 0}
				return config
			}(),
			invalid: true,
		},
		"producer with out-of-range remote consumer port": {
			config: func() *reliableDeliveryConfig {
				config := producerDeliveryConfig("consumer")
				config.producer.consumerAddress = &reliablePeerAddress{host: "127.0.0.1", port: 70000}
				return config
			}(),
			invalid: true,
		},
		"work-pulling producer with remote consumer address": {
			config: func() *reliableDeliveryConfig {
				config := workPullingProducerConfig()
				config.producer.consumerAddress = &reliablePeerAddress{host: "127.0.0.1", port: 2280}
				return config
			}(),
			invalid: true,
		},
		"consumer with remote producer address": {
			config: func() *reliableDeliveryConfig {
				config := consumerDeliveryConfig("producer")
				config.consumer.producerAddress = &reliablePeerAddress{host: "127.0.0.1", port: 2280}
				return config
			}(),
		},
		"consumer with blank remote producer host": {
			config: func() *reliableDeliveryConfig {
				config := consumerDeliveryConfig("producer")
				config.consumer.producerAddress = &reliablePeerAddress{host: "", port: 2280}
				return config
			}(),
			invalid: true,
		},
		"consumer with invalid remote producer port": {
			config: func() *reliableDeliveryConfig {
				config := consumerDeliveryConfig("producer")
				config.consumer.producerAddress = &reliablePeerAddress{host: "127.0.0.1", port: -1}
				return config
			}(),
			invalid: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := test.config.Validate()
			if test.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestReliableDeliveryConfigWireRoundTrip(t *testing.T) {
	t.Run("With producer settings", func(t *testing.T) {
		config := &reliableDeliveryConfig{
			producer: &reliableProducerConfig{
				consumerName:   "consumer",
				durableQueueID: "ordersQueue",
				queueRetry: &reliableQueueRetryConfig{
					maxAttempts:    3,
					initialBackoff: 100 * time.Millisecond,
				},
				retryInterval:        500 * time.Millisecond,
				deliveryConfirmation: true,
				maxChunkBytes:        64 * 1024,
			},
		}

		restored, err := reliableDeliveryConfigFromProto(config.toProto())
		require.NoError(t, err)
		assert.Equal(t, config, restored)
	})

	t.Run("With minimal producer settings", func(t *testing.T) {
		config := producerDeliveryConfig("consumer")

		restored, err := reliableDeliveryConfigFromProto(config.toProto())
		require.NoError(t, err)
		assert.Equal(t, config, restored)
	})

	t.Run("With consumer settings", func(t *testing.T) {
		config := &reliableDeliveryConfig{
			consumer: &reliableConsumerConfig{
				producerName:      "producer",
				flowControlWindow: 50,
				resendInterval:    2 * time.Second,
			},
		}

		restored, err := reliableDeliveryConfigFromProto(config.toProto())
		require.NoError(t, err)
		assert.Equal(t, config, restored)
	})

	t.Run("With no configuration", func(t *testing.T) {
		assert.Nil(t, (*reliableDeliveryConfig)(nil).toProto())
		assert.Nil(t, (&reliableDeliveryConfig{}).toProto())

		restored, err := reliableDeliveryConfigFromProto(nil)
		require.NoError(t, err)
		assert.Nil(t, restored)
	})

	t.Run("With missing endpoint side", func(t *testing.T) {
		restored, err := reliableDeliveryConfigFromProto(&internalpb.ReliableDeliveryConfig{})
		require.Error(t, err)
		assert.Nil(t, restored)
	})

	t.Run("With malformed producer durations", func(t *testing.T) {
		malformed := &durationpb.Duration{Seconds: 315576000001}

		producer := internalpb.ReliableProducerConfig_builder{
			ConsumerName:       "consumer",
			LocalRetryInterval: malformed,
		}.Build()
		_, err := reliableDeliveryConfigFromProto(internalpb.ReliableDeliveryConfig_builder{
			Producer: proto.ValueOrDefault(producer),
		}.Build())
		require.Error(t, err)

		producer = internalpb.ReliableProducerConfig_builder{
			ConsumerName: "consumer",
			QueueRetry: internalpb.QueueRetryConfig_builder{
				MaxAttempts:    3,
				InitialBackoff: malformed,
			}.Build(),
		}.Build()
		_, err = reliableDeliveryConfigFromProto(internalpb.ReliableDeliveryConfig_builder{
			Producer: proto.ValueOrDefault(producer),
		}.Build())
		require.Error(t, err)
	})

	t.Run("With malformed consumer duration", func(t *testing.T) {
		consumer := internalpb.ReliableConsumerConfig_builder{
			ProducerName:      "producer",
			FlowControlWindow: 50,
			ResendInterval:    &durationpb.Duration{Seconds: 315576000001},
		}.Build()
		_, err := reliableDeliveryConfigFromProto(internalpb.ReliableDeliveryConfig_builder{
			Consumer: proto.ValueOrDefault(consumer),
		}.Build())
		require.Error(t, err)
	})
}

func TestReliableDeliveryConfigClone(t *testing.T) {
	t.Run("With producer settings", func(t *testing.T) {
		config := &reliableDeliveryConfig{
			producer: &reliableProducerConfig{
				consumerName: "consumer",
				queueRetry: &reliableQueueRetryConfig{
					maxAttempts:    3,
					initialBackoff: 100 * time.Millisecond,
				},
				consumerAddress: &reliablePeerAddress{host: "127.0.0.1", port: 2280},
			},
		}

		cloned := config.clone()
		require.Equal(t, config, cloned)

		config.producer.consumerName = "changed"
		config.producer.queueRetry.maxAttempts = 9
		config.producer.consumerAddress.port = 9999
		assert.Equal(t, "consumer", cloned.producer.consumerName)
		assert.Equal(t, 3, cloned.producer.queueRetry.maxAttempts)
		assert.Equal(t, 2280, cloned.producer.consumerAddress.port)
	})

	t.Run("With consumer settings", func(t *testing.T) {
		config := consumerDeliveryConfig("producer")
		config.consumer.producerAddress = &reliablePeerAddress{host: "127.0.0.1", port: 2280}

		cloned := config.clone()
		require.Equal(t, config, cloned)

		config.consumer.producerName = "changed"
		config.consumer.producerAddress.host = "changed"
		assert.Equal(t, "producer", cloned.consumer.producerName)
		assert.Equal(t, "127.0.0.1", cloned.consumer.producerAddress.host)
	})

	t.Run("With no configuration", func(t *testing.T) {
		assert.Nil(t, (*reliableDeliveryConfig)(nil).clone())
	})
}

func TestReliableDeliveryConfigToRemoteSpec(t *testing.T) {
	t.Run("With a producer configuration", func(t *testing.T) {
		config := producerDeliveryConfig("consumer")
		config.producer.durableQueueID = "ordersQueue"
		config.producer.deliveryConfirmation = true
		config.producer.maxChunkBytes = 64 * 1024

		spec := config.toRemoteSpec()
		require.NotNil(t, spec)
		require.NotNil(t, spec.Producer)
		assert.Nil(t, spec.Consumer)
		assert.Equal(t, "consumer", spec.Producer.ConsumerName)
		assert.Equal(t, "ordersQueue", spec.Producer.DurableQueueID)
		assert.Equal(t, DefaultReliableQueueRetryAttempts, spec.Producer.QueueRetryMaxAttempts)
		assert.Equal(t, DefaultReliableQueueRetryBackoff, spec.Producer.QueueRetryInitialBackoff)
		assert.Equal(t, DefaultReliableProducerRetryInterval, spec.Producer.LocalRetryInterval)
		assert.True(t, spec.Producer.DeliveryConfirmation)
		assert.EqualValues(t, 64*1024, spec.Producer.MaxChunkBytes)
	})

	t.Run("With a consumer configuration", func(t *testing.T) {
		spec := consumerDeliveryConfig("producer").toRemoteSpec()
		require.NotNil(t, spec)
		require.NotNil(t, spec.Consumer)
		assert.Nil(t, spec.Producer)
		assert.Equal(t, "producer", spec.Consumer.ProducerName)
		assert.Equal(t, 50, spec.Consumer.FlowControlWindow)
		assert.Equal(t, DefaultReliableResendInterval, spec.Consumer.ResendInterval)
	})

	t.Run("With no configuration", func(t *testing.T) {
		assert.Nil(t, (*reliableDeliveryConfig)(nil).toRemoteSpec())
	})

	t.Run("With empty configuration", func(t *testing.T) {
		assert.Nil(t, (&reliableDeliveryConfig{}).toRemoteSpec())
	})
}

func TestReliableSpawnOptionFromWire(t *testing.T) {
	t.Run("With a producer and its durable queue", func(t *testing.T) {
		queue := &mockDurableQueue{}
		wire := producerDeliveryConfig("consumer")
		wire.producer.durableQueueID = queue.ID()
		wire.producer.deliveryConfirmation = true

		option, err := reliableSpawnOptionFromWire(wire.toProto(), []extension.Dependency{queue})
		require.NoError(t, err)

		config := newSpawnConfig(option)
		require.NotNil(t, config.reliableDelivery)
		assert.Equal(t, "consumer", config.reliableDelivery.producer.consumerName)
		assert.Same(t, queue, config.durableQueue)
		require.NoError(t, config.Validate())

		// relocation and remote placement rebuild the endpoint with the same
		// notification setting
		assert.True(t, config.reliableDelivery.producer.deliveryConfirmation)
	})

	t.Run("With a volatile producer", func(t *testing.T) {
		option, err := reliableSpawnOptionFromWire(producerDeliveryConfig("consumer").toProto(), nil)
		require.NoError(t, err)

		config := newSpawnConfig(option)
		require.NotNil(t, config.reliableDelivery)
		assert.Nil(t, config.durableQueue)
	})

	t.Run("With a work-pulling producer and its durable work queue", func(t *testing.T) {
		queue := &mockDurableWorkQueue{}
		wire := workPullingProducerConfig()
		wire.producer.durableQueueID = queue.ID()

		option, err := reliableSpawnOptionFromWire(wire.toProto(), []extension.Dependency{queue})
		require.NoError(t, err)

		config := newSpawnConfig(option)
		require.NotNil(t, config.reliableDelivery)
		assert.True(t, config.reliableDelivery.producer.workPulling)
		assert.Same(t, queue, config.durableWorkQueue)
		assert.Nil(t, config.durableQueue)
		require.NoError(t, config.Validate())
	})

	t.Run("With a consumer", func(t *testing.T) {
		option, err := reliableSpawnOptionFromWire(consumerDeliveryConfig("producer").toProto(), nil)
		require.NoError(t, err)

		config := newSpawnConfig(option)
		require.NotNil(t, config.reliableDelivery)
		assert.Equal(t, "producer", config.reliableDelivery.consumer.producerName)
		assert.Nil(t, config.durableQueue)
	})

	t.Run("With a structurally invalid configuration", func(t *testing.T) {
		option, err := reliableSpawnOptionFromWire(&internalpb.ReliableDeliveryConfig{}, nil)
		require.Error(t, err)
		assert.Nil(t, option)
	})

	t.Run("With no configuration", func(t *testing.T) {
		option, err := reliableSpawnOptionFromWire(nil, nil)
		require.ErrorContains(t, err, "endpoint configuration is required")
		assert.Nil(t, option)
	})

	t.Run("With the queue dependency missing", func(t *testing.T) {
		wire := producerDeliveryConfig("consumer")
		wire.producer.durableQueueID = "ordersQueue"

		option, err := reliableSpawnOptionFromWire(wire.toProto(), nil)
		require.ErrorContains(t, err, "missing from the endpoint dependencies")
		assert.Nil(t, option)
	})

	t.Run("With a mistyped queue dependency", func(t *testing.T) {
		dependency := NewMockDependency("ordersQueue", "user", "email")
		wire := producerDeliveryConfig("consumer")
		wire.producer.durableQueueID = dependency.ID()

		option, err := reliableSpawnOptionFromWire(wire.toProto(), []extension.Dependency{dependency})
		require.ErrorContains(t, err, "is not a durable producer queue")
		assert.Nil(t, option)
	})

	t.Run("With a nil dependency entry skipped", func(t *testing.T) {
		queue := &mockDurableQueue{}
		wire := producerDeliveryConfig("consumer")
		wire.producer.durableQueueID = queue.ID()

		option, err := reliableSpawnOptionFromWire(wire.toProto(), []extension.Dependency{nil, queue})
		require.NoError(t, err)

		config := newSpawnConfig(option)
		assert.Same(t, queue, config.durableQueue)
	})

	t.Run("With the work queue dependency missing", func(t *testing.T) {
		wire := workPullingProducerConfig()
		wire.producer.durableQueueID = "jobsQueue"

		// the non-matching entry is skipped before the miss is reported
		option, err := reliableSpawnOptionFromWire(wire.toProto(), []extension.Dependency{nil, &mockDurableWorkQueue{}})
		require.ErrorContains(t, err, "durable work queue dependency=jobsQueue is missing")
		assert.Nil(t, option)
	})

	t.Run("With a mistyped work queue dependency", func(t *testing.T) {
		dependency := &mockDurableQueue{}
		wire := workPullingProducerConfig()
		wire.producer.durableQueueID = dependency.ID()

		option, err := reliableSpawnOptionFromWire(wire.toProto(), []extension.Dependency{dependency})
		require.ErrorContains(t, err, "is not a durable work queue")
		assert.Nil(t, option)
	})
}

func TestReliableDeliveryConfigPeerAddress(t *testing.T) {
	// every switch arm resolves without dereferencing an absent side
	var missing *reliableDeliveryConfig
	assert.Nil(t, missing.peerAddress())
	assert.Nil(t, (&reliableDeliveryConfig{}).peerAddress())

	producer := producerDeliveryConfig("consumer")
	producer.producer.consumerAddress = &reliablePeerAddress{host: "127.0.0.1", port: 9000}
	assert.Equal(t, producer.producer.consumerAddress, producer.peerAddress())

	consumer := consumerDeliveryConfig("producer")
	consumer.consumer.producerAddress = &reliablePeerAddress{host: "127.0.0.1", port: 9001}
	assert.Equal(t, consumer.consumer.producerAddress, consumer.peerAddress())
}
