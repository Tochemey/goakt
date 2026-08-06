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
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/internal/validation"
	"github.com/tochemey/goakt/v4/remote"
)

// reliableDeliveryConfig describes one endpoint's reliable-delivery settings.
// It is the in-memory counterpart of the wire-level
// internalpb.ReliableDeliveryConfig, which appears only when the actor record
// crosses the network.
type reliableDeliveryConfig struct {
	// producer holds the producer-side settings; exactly one side is set.
	producer *reliableProducerConfig
	// consumer holds the consumer-side settings; exactly one side is set.
	consumer *reliableConsumerConfig
}

// reliableProducerConfig holds producer-side delivery settings.
type reliableProducerConfig struct {
	// consumerName names the one consumer endpoint authorized to receive the
	// producer's messages.
	consumerName string
	// durableQueueID references the durable queue in the endpoint's user
	// dependencies; empty means the flow runs without a durable queue.
	durableQueueID string
	// queueRetry defines retries for durable queue operations; it is
	// required because the producer controller cannot run without one.
	queueRetry *reliableQueueRetryConfig
	// localRetryInterval is the RequestNext/Stored retry cadence; it must
	// be positive.
	localRetryInterval time.Duration
	// deliveryConfirmation enables DeliveryConfirmed notifications toward
	// the producer endpoint once the consumer confirms a message.
	deliveryConfirmation bool
	// maxChunkBytes splits payloads larger than this many bytes into
	// sequenced chunks; zero disables chunking. It mirrors the wire field's
	// type so the configuration crosses the boundary without conversion.
	maxChunkBytes uint32
	// queue is the live durable queue instance collected by WithDurableQueue;
	// nil runs volatile. It is not serialized from this struct: the queue
	// crosses the wire as an ordinary dependency descriptor on the actor
	// record and is rebuilt by dependency reconstruction on the hosting node.
	// durableQueueID then selects that rebuilt instance among the endpoint's
	// dependencies, which may also contain unrelated business dependencies.
	queue DurableProducerQueue
}

// reliableQueueRetryConfig defines retries for durable queue operations.
type reliableQueueRetryConfig struct {
	// maxAttempts is the maximum number of attempts for one queue operation.
	maxAttempts int
	// initialBackoff is the delay before the first retry; it must be positive.
	initialBackoff time.Duration
}

// reliableConsumerConfig holds consumer-side delivery settings.
type reliableConsumerConfig struct {
	// producerName names the producer endpoint whose messages are accepted.
	producerName string
	// flowControlWindow is the maximum demand granted in one request.
	flowControlWindow int
	// resendInterval is the registration, demand, and delivery retry cadence;
	// it must be positive.
	resendInterval time.Duration
}

var _ validation.Validator = (*reliableDeliveryConfig)(nil)

// Validate checks that exactly one endpoint side is configured and that its
// peer name, flow-control window, retry settings, and queue reference are
// usable.
func (x *reliableDeliveryConfig) Validate() error {
	switch {
	case x.producer != nil && x.consumer != nil:
		return errors.New("reliable delivery configuration must set exactly one endpoint side")
	case x.producer != nil:
		return x.producer.Validate()
	case x.consumer != nil:
		return x.consumer.Validate()
	default:
		return errors.New("reliable delivery endpoint configuration is required")
	}
}

// role returns the controller role required by the configured endpoint side.
// Validate guarantees exactly one side is set.
func (x *reliableDeliveryConfig) role() ReliableControllerRole {
	if x.producer != nil {
		return ReliableControllerRoleProducer
	}

	return ReliableControllerRoleConsumer
}

// Validate checks producer-side delivery settings. It mirrors the producer
// controller's constructor guards, so a configuration that passes validation
// always builds a controller during the endpoint spawn transaction.
func (x *reliableProducerConfig) Validate() error {
	if err := validateReliablePeerName(x.consumerName); err != nil {
		return err
	}

	if x.localRetryInterval <= 0 {
		return errors.New("local retry interval must be positive")
	}

	if x.queueRetry == nil {
		return errors.New("queue retry policy is required")
	}

	if x.queueRetry.maxAttempts < 1 {
		return errors.New("queue retry max attempts must be at least 1")
	}

	if x.queueRetry.initialBackoff <= 0 {
		return errors.New("queue retry initial backoff must be positive")
	}

	if x.durableQueueID != "" {
		if err := validation.NewIDValidator(x.durableQueueID).Validate(); err != nil {
			return fmt.Errorf("durable queue ID is invalid: %w", err)
		}
	}

	if x.maxChunkBytes != 0 {
		if x.maxChunkBytes < MinChunkSize || x.maxChunkBytes > MaxChunkSize {
			return fmt.Errorf("chunk size must be in [%d, %d]", MinChunkSize, MaxChunkSize)
		}

		if x.durableQueueID != "" {
			return errors.New("chunking cannot be combined with a durable queue")
		}
	}

	return nil
}

// Validate checks consumer-side delivery settings. It mirrors the consumer
// controller's constructor guards, so a configuration that passes validation
// always builds a controller during the endpoint spawn transaction.
func (x *reliableConsumerConfig) Validate() error {
	if err := validateReliablePeerName(x.producerName); err != nil {
		return err
	}

	if x.flowControlWindow < 1 || x.flowControlWindow > MaxFlowControlWindow {
		return fmt.Errorf("flow control window must be in [1, %d]", MaxFlowControlWindow)
	}

	if x.resendInterval <= 0 {
		return errors.New("resend interval must be positive")
	}

	return nil
}

// validateReliablePeerName checks the user-visible name of the peer endpoint:
// it must be set and can never be a GoAkt-reserved identity.
func validateReliablePeerName(name string) error {
	if types.IsBlank(name) {
		return errors.New("peer endpoint name is required")
	}

	if isSystemName(name) {
		return errors.New("peer endpoint name is reserved")
	}

	return nil
}

// clone returns an independent copy of the configuration.
func (x *reliableDeliveryConfig) clone() *reliableDeliveryConfig {
	if x == nil {
		return nil
	}

	cloned := &reliableDeliveryConfig{}

	if x.producer != nil {
		producer := *x.producer

		if x.producer.queueRetry != nil {
			retry := *x.producer.queueRetry
			producer.queueRetry = &retry
		}

		cloned.producer = &producer
	}

	if x.consumer != nil {
		consumer := *x.consumer
		cloned.consumer = &consumer
	}

	return cloned
}

// toProto converts the configuration to its wire form for the actor record.
func (x *reliableDeliveryConfig) toProto() *internalpb.ReliableDeliveryConfig {
	switch {
	case x == nil:
		return nil
	case x.producer != nil:
		producer := &internalpb.ReliableProducerConfig{
			ConsumerName:         x.producer.consumerName,
			DeliveryConfirmation: x.producer.deliveryConfirmation,
			MaxChunkBytes:        x.producer.maxChunkBytes,
		}

		if x.producer.durableQueueID != "" {
			producer.DurableQueueId = new(x.producer.durableQueueID)
		}

		if x.producer.queueRetry != nil {
			producer.QueueRetry = &internalpb.QueueRetryConfig{
				MaxAttempts: uint32(x.producer.queueRetry.maxAttempts),
			}

			if x.producer.queueRetry.initialBackoff > 0 {
				producer.QueueRetry.InitialBackoff = durationpb.New(x.producer.queueRetry.initialBackoff)
			}
		}

		if x.producer.localRetryInterval > 0 {
			producer.LocalRetryInterval = durationpb.New(x.producer.localRetryInterval)
		}

		return &internalpb.ReliableDeliveryConfig{
			Endpoint: &internalpb.ReliableDeliveryConfig_Producer{Producer: producer},
		}
	case x.consumer != nil:
		consumer := &internalpb.ReliableConsumerConfig{
			ProducerName:      x.consumer.producerName,
			FlowControlWindow: uint32(x.consumer.flowControlWindow),
		}

		if x.consumer.resendInterval > 0 {
			consumer.ResendInterval = durationpb.New(x.consumer.resendInterval)
		}

		return &internalpb.ReliableDeliveryConfig{
			Endpoint: &internalpb.ReliableDeliveryConfig_Consumer{Consumer: consumer},
		}
	default:
		return nil
	}
}

// toRemoteSpec converts the configuration to the public specification carried
// by a remote spawn request, so a reliable endpoint placed on another node
// through Spawn or SpawnOn travels with its full delivery settings. The
// constructor defaults guarantee every field is populated.
func (x *reliableDeliveryConfig) toRemoteSpec() *remote.ReliableDeliverySpec {
	switch {
	case x == nil:
		return nil
	case x.producer != nil:
		producer := &remote.ReliableProducerSpec{
			ConsumerName:         x.producer.consumerName,
			DurableQueueID:       x.producer.durableQueueID,
			LocalRetryInterval:   x.producer.localRetryInterval,
			DeliveryConfirmation: x.producer.deliveryConfirmation,
			MaxChunkBytes:        x.producer.maxChunkBytes,
		}

		if x.producer.queueRetry != nil {
			producer.QueueRetryMaxAttempts = x.producer.queueRetry.maxAttempts
			producer.QueueRetryInitialBackoff = x.producer.queueRetry.initialBackoff
		}

		return &remote.ReliableDeliverySpec{Producer: producer}
	case x.consumer != nil:
		return &remote.ReliableDeliverySpec{
			Consumer: &remote.ReliableConsumerSpec{
				ProducerName:      x.consumer.producerName,
				FlowControlWindow: x.consumer.flowControlWindow,
				ResendInterval:    x.consumer.resendInterval,
			},
		}
	default:
		return nil
	}
}

// reliableDeliveryConfigFromProto restores the in-memory configuration from
// its wire form. It rejects a structurally invalid record; semantic checks
// remain with Validate.
func reliableDeliveryConfigFromProto(config *internalpb.ReliableDeliveryConfig) (*reliableDeliveryConfig, error) {
	if config == nil {
		return nil, nil
	}

	switch endpoint := config.GetEndpoint().(type) {
	case *internalpb.ReliableDeliveryConfig_Producer:
		producer := &reliableProducerConfig{
			consumerName:         endpoint.Producer.GetConsumerName(),
			durableQueueID:       endpoint.Producer.GetDurableQueueId(),
			deliveryConfirmation: endpoint.Producer.GetDeliveryConfirmation(),
			maxChunkBytes:        endpoint.Producer.GetMaxChunkBytes(),
		}

		localRetryInterval, err := durationFromProto("local retry interval", endpoint.Producer.GetLocalRetryInterval())
		if err != nil {
			return nil, err
		}

		producer.localRetryInterval = localRetryInterval

		if retry := endpoint.Producer.GetQueueRetry(); retry != nil {
			initialBackoff, err := durationFromProto("queue retry initial backoff", retry.GetInitialBackoff())
			if err != nil {
				return nil, err
			}

			producer.queueRetry = &reliableQueueRetryConfig{
				maxAttempts:    int(retry.GetMaxAttempts()),
				initialBackoff: initialBackoff,
			}
		}

		return &reliableDeliveryConfig{producer: producer}, nil
	case *internalpb.ReliableDeliveryConfig_Consumer:
		resendInterval, err := durationFromProto("resend interval", endpoint.Consumer.GetResendInterval())
		if err != nil {
			return nil, err
		}

		return &reliableDeliveryConfig{
			consumer: &reliableConsumerConfig{
				producerName:      endpoint.Consumer.GetProducerName(),
				flowControlWindow: int(endpoint.Consumer.GetFlowControlWindow()),
				resendInterval:    resendInterval,
			},
		}, nil
	default:
		return nil, errors.New("reliable delivery endpoint configuration is required")
	}
}

// reliableSpawnOptionFromWire rebuilds the reliable-delivery spawn option
// carried by a serialized actor record or a remote spawn request, so initial
// remote placement and relocation reconstruct an endpoint exactly like a
// local AsReliableProducer/AsReliableConsumer spawn. A producer configuration
// that references a durable queue resolves the instance by ID among the
// already reconstructed dependencies; a missing or mistyped queue dependency
// fails reconstruction so the caller can restore the departed record or
// reject the spawn instead of silently degrading to a volatile flow.
func reliableSpawnOptionFromWire(config *internalpb.ReliableDeliveryConfig, dependencies []extension.Dependency) (SpawnOption, error) {
	decoded, err := reliableDeliveryConfigFromProto(config)
	if err != nil {
		return nil, err
	}

	if decoded == nil {
		return nil, errors.New("reliable delivery endpoint configuration is required")
	}

	var queue DurableProducerQueue

	if producer := decoded.producer; producer != nil && producer.durableQueueID != "" {
		queue, err = durableQueueByID(dependencies, producer.durableQueueID)
		if err != nil {
			return nil, err
		}
	}

	return spawnOption(func(config *spawnConfig) {
		config.reliableDelivery = decoded
		config.durableQueue = queue
	}), nil
}

// durableQueueByID resolves the durable producer queue a reconstructed
// producer configuration references among the endpoint's reconstructed
// dependencies. The queue travels in the dependency list (see
// normalizeDurableQueue), so a missing entry means the queue type is not
// registered on this node or the record is malformed.
func durableQueueByID(dependencies []extension.Dependency, id string) (DurableProducerQueue, error) {
	for _, dependency := range dependencies {
		if dependency == nil || dependency.ID() != id {
			continue
		}

		queue, ok := dependency.(DurableProducerQueue)
		if !ok {
			return nil, fmt.Errorf("dependency=%s is not a durable producer queue", id)
		}

		return queue, nil
	}

	return nil, fmt.Errorf("durable producer queue dependency=%s is missing from the endpoint dependencies", id)
}

// durationFromProto converts an optional wire duration, rejecting malformed
// values.
func durationFromProto(field string, value *durationpb.Duration) (time.Duration, error) {
	if value == nil {
		return 0, nil
	}

	if err := value.CheckValid(); err != nil {
		return 0, fmt.Errorf("%s is invalid: %w", field, err)
	}

	return value.AsDuration(), nil
}
