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
	"net"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/internal/validation"
	"github.com/tochemey/goakt/v4/remote"
	"google.golang.org/protobuf/proto"
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
	// producer's messages. Empty when workPulling is set: workers are
	// discovered through registration fencing.
	consumerName string
	// workPulling selects the work-pulling producer controller that
	// multiplexes per-worker point-to-point sub-flows; false is point-to-point.
	workPulling bool
	// durableQueueID references the durable queue in the endpoint's user
	// dependencies; empty means the flow runs without a durable queue.
	durableQueueID string
	// queueRetry defines retries for durable queue operations; it is
	// required because the producer controller cannot run without one.
	queueRetry *reliableQueueRetryConfig
	// retryInterval is the RequestNext/Stored retry cadence; it must
	// be positive.
	retryInterval time.Duration
	// deliveryConfirmation enables DeliveryConfirmed notifications toward
	// the producer endpoint once the consumer confirms a message.
	deliveryConfirmation bool
	// maxChunkBytes splits payloads larger than this many bytes into
	// sequenced chunks; zero disables chunking. It mirrors the wire field's
	// type so the configuration crosses the boundary without conversion.
	maxChunkBytes uint32
	// queue is the live durable producer queue collected by WithReliableDurableQueue;
	// nil runs volatile on a point-to-point producer. It is not serialized
	// from this struct: the queue crosses the wire as an ordinary dependency
	// descriptor on the actor record and is rebuilt by dependency
	// reconstruction on the hosting node. durableQueueID then selects that
	// rebuilt instance among the endpoint's dependencies.
	queue DurableProducerQueue
	// workQueue is the live durable work queue collected by
	// WithReliableDurableWorkQueue; nil runs volatile on a work-pulling producer. It
	// shares durableQueueID with queue for wire reconstruction and is
	// mutually exclusive with queue by pattern.
	workQueue DurableWorkQueue
	// consumerAddress is the remoting address of the node hosting the consumer
	// endpoint of a remoting-only flow; nil resolves cross-node peers through
	// the cluster registry. It never serializes: remoting-only endpoints are
	// spawned locally on their own node and never relocate.
	consumerAddress *reliablePeerAddress
}

// reliablePeerAddress is the remoting address of the node hosting a flow's
// peer endpoint. When set, cross-node companion resolution asks that node
// directly through the GetReliableCompanion remoting RPC instead of the
// cluster registry, which is how a remoting-only flow finds its peer.
type reliablePeerAddress struct {
	// host is the remoting host the peer node advertises.
	host string
	// port is the remoting port the peer node listens on.
	port int
}

// Validate checks that the peer node address is dialable: a usable host and a
// positive port in the TCP range.
func (x *reliablePeerAddress) Validate() error {
	if x.port < 1 {
		return fmt.Errorf("peer node port is invalid: %d", x.port)
	}

	if err := validation.NewTCPAddressValidator(net.JoinHostPort(x.host, strconv.Itoa(x.port))).Validate(); err != nil {
		return fmt.Errorf("peer node address is invalid: %w", err)
	}

	return nil
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
	// producerAddress is the remoting address of the node hosting the producer
	// endpoint of a remoting-only flow; nil resolves cross-node peers through
	// the cluster registry. It never serializes: remoting-only endpoints are
	// spawned locally on their own node and never relocate.
	producerAddress *reliablePeerAddress
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
	if err := x.validatePattern(); err != nil {
		return err
	}

	if x.retryInterval <= 0 {
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

	if x.durableQueueID != types.EmptyString {
		if err := validation.NewIDValidator(x.durableQueueID).Validate(); err != nil {
			return fmt.Errorf("durable queue ID is invalid: %w", err)
		}
	}

	if x.maxChunkBytes != 0 {
		if x.maxChunkBytes < MinReliableChunkSize || x.maxChunkBytes > MaxReliableChunkSize {
			return fmt.Errorf("chunk size must be in [%d, %d]", MinReliableChunkSize, MaxReliableChunkSize)
		}
	}

	return nil
}

// validatePattern applies the checks owned by the selected producer pattern,
// dispatching to exactly one of the pattern validators.
func (x *reliableProducerConfig) validatePattern() error {
	if x.workPulling {
		return x.validateWorkPulling()
	}

	return x.validatePointToPoint()
}

// validateWorkPulling rejects the settings that only apply to point-to-point:
// workers are discovered through registration fencing rather than a peer
// name, chunking is unsupported, durability uses the work queue contract, and
// a worker set spanning nodes requires cluster mode rather than an explicit
// peer address.
func (x *reliableProducerConfig) validateWorkPulling() error {
	if !types.IsBlank(x.consumerName) {
		return errors.New("work-pulling producer rejects a consumer endpoint name")
	}

	if x.maxChunkBytes != 0 {
		return errors.New("work-pulling producer rejects chunking")
	}

	if x.queue != nil {
		return errors.New("work-pulling producer rejects a durable producer queue")
	}

	if x.consumerAddress != nil {
		return errors.New("work-pulling producer rejects a remote consumer address")
	}

	return nil
}

// validatePointToPoint requires the consumer peer name and rejects the
// work-pulling durable queue, whose per-message confirmation model does not
// apply to a cumulative-watermark flow.
func (x *reliableProducerConfig) validatePointToPoint() error {
	if err := validateReliablePeerName(x.consumerName); err != nil {
		return err
	}

	if x.workQueue != nil {
		return errors.New("point-to-point producer rejects a durable work queue")
	}

	if x.consumerAddress != nil {
		if err := x.consumerAddress.Validate(); err != nil {
			return err
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

	if x.flowControlWindow < 1 || x.flowControlWindow > MaxReliableFlowControlWindow {
		return fmt.Errorf("flow control window must be in [1, %d]", MaxReliableFlowControlWindow)
	}

	if x.resendInterval <= 0 {
		return errors.New("resend interval must be positive")
	}

	if x.producerAddress != nil {
		if err := x.producerAddress.Validate(); err != nil {
			return err
		}
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

		if x.producer.consumerAddress != nil {
			peer := *x.producer.consumerAddress
			producer.consumerAddress = &peer
		}

		cloned.producer = &producer
	}

	if x.consumer != nil {
		consumer := *x.consumer

		if x.consumer.producerAddress != nil {
			peer := *x.consumer.producerAddress
			consumer.producerAddress = &peer
		}

		cloned.consumer = &consumer
	}

	return cloned
}

// peerAddress returns the explicitly configured remote peer node address of
// whichever endpoint side is set, or nil when the flow resolves through the
// local tree or the cluster registry.
func (x *reliableDeliveryConfig) peerAddress() *reliablePeerAddress {
	switch {
	case x == nil:
		return nil
	case x.producer != nil:
		return x.producer.consumerAddress
	case x.consumer != nil:
		return x.consumer.producerAddress
	default:
		return nil
	}
}

// toProto converts the configuration to its wire form for the actor record.
func (x *reliableDeliveryConfig) toProto() *internalpb.ReliableDeliveryConfig {
	switch {
	case x == nil:
		return nil
	case x.producer != nil:
		producer := &internalpb.ReliableProducerConfig{}
		producer.SetConsumerName(x.producer.consumerName)
		producer.SetDeliveryConfirmation(x.producer.deliveryConfirmation)
		producer.SetMaxChunkBytes(x.producer.maxChunkBytes)
		producer.SetPattern(reliableDeliveryPatternToProto(x.producer.workPulling))

		if x.producer.durableQueueID != types.EmptyString {
			producer.SetDurableQueueId(x.producer.durableQueueID)
		}

		if x.producer.queueRetry != nil {
			qrc := &internalpb.QueueRetryConfig{}
			qrc.SetMaxAttempts(uint32(x.producer.queueRetry.maxAttempts))
			producer.SetQueueRetry(qrc)

			if x.producer.queueRetry.initialBackoff > 0 {
				producer.GetQueueRetry().SetInitialBackoff(durationpb.New(x.producer.queueRetry.initialBackoff))
			}
		}

		if x.producer.retryInterval > 0 {
			producer.SetLocalRetryInterval(durationpb.New(x.producer.retryInterval))
		}

		rdc := &internalpb.ReliableDeliveryConfig{}
		rdc.SetProducer(proto.ValueOrDefault(producer))
		return rdc
	case x.consumer != nil:
		consumer := &internalpb.ReliableConsumerConfig{}
		consumer.SetProducerName(x.consumer.producerName)
		consumer.SetFlowControlWindow(uint32(x.consumer.flowControlWindow))

		if x.consumer.resendInterval > 0 {
			consumer.SetResendInterval(durationpb.New(x.consumer.resendInterval))
		}

		rdc := &internalpb.ReliableDeliveryConfig{}
		rdc.SetConsumer(proto.ValueOrDefault(consumer))
		return rdc
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
			WorkPulling:          x.producer.workPulling,
			DurableQueueID:       x.producer.durableQueueID,
			LocalRetryInterval:   x.producer.retryInterval,
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

	switch config.WhichEndpoint() {
	case internalpb.ReliableDeliveryConfig_Producer_case:
		producer := &reliableProducerConfig{
			consumerName:         config.GetProducer().GetConsumerName(),
			workPulling:          reliableDeliveryPatternFromProto(config.GetProducer().GetPattern()),
			durableQueueID:       config.GetProducer().GetDurableQueueId(),
			deliveryConfirmation: config.GetProducer().GetDeliveryConfirmation(),
			maxChunkBytes:        config.GetProducer().GetMaxChunkBytes(),
		}

		localRetryInterval, err := durationFromProto("local retry interval", config.GetProducer().GetLocalRetryInterval())
		if err != nil {
			return nil, err
		}

		producer.retryInterval = localRetryInterval

		if retry := config.GetProducer().GetQueueRetry(); retry != nil {
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
	case internalpb.ReliableDeliveryConfig_Consumer_case:
		resendInterval, err := durationFromProto("resend interval", config.GetConsumer().GetResendInterval())
		if err != nil {
			return nil, err
		}

		return &reliableDeliveryConfig{
			consumer: &reliableConsumerConfig{
				producerName:      config.GetConsumer().GetProducerName(),
				flowControlWindow: int(config.GetConsumer().GetFlowControlWindow()),
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
	var workQueue DurableWorkQueue

	if producer := decoded.producer; producer != nil && producer.durableQueueID != types.EmptyString {
		if producer.workPulling {
			workQueue, err = durableWorkQueueByID(dependencies, producer.durableQueueID)
			if err != nil {
				return nil, err
			}
		} else {
			queue, err = durableQueueByID(dependencies, producer.durableQueueID)
			if err != nil {
				return nil, err
			}
		}
	}

	return spawnOption(func(config *spawnConfig) {
		config.reliableDelivery = decoded
		config.durableQueue = queue
		config.durableWorkQueue = workQueue
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

// durableWorkQueueByID resolves the durable work queue a reconstructed
// work-pulling producer configuration references among the endpoint's
// reconstructed dependencies.
func durableWorkQueueByID(dependencies []extension.Dependency, id string) (DurableWorkQueue, error) {
	for _, dependency := range dependencies {
		if dependency == nil || dependency.ID() != id {
			continue
		}

		queue, ok := dependency.(DurableWorkQueue)
		if !ok {
			return nil, fmt.Errorf("dependency=%s is not a durable work queue", id)
		}

		return queue, nil
	}

	return nil, fmt.Errorf("durable work queue dependency=%s is missing from the endpoint dependencies", id)
}

// reliableDeliveryPatternToProto maps the in-memory work-pulling flag to its
// wire pattern. Point-to-point is written explicitly so relocation and remote
// spawn rebuild the same controller without relying on the unspecified default.
func reliableDeliveryPatternToProto(workPulling bool) internalpb.ReliableDeliveryPattern {
	if workPulling {
		return internalpb.ReliableDeliveryPattern_RELIABLE_DELIVERY_PATTERN_WORK_PULLING
	}

	return internalpb.ReliableDeliveryPattern_RELIABLE_DELIVERY_PATTERN_POINT_TO_POINT
}

// reliableDeliveryPatternFromProto restores the work-pulling flag from its
// wire pattern. Unspecified means point-to-point so records written before
// the field existed keep their original meaning.
func reliableDeliveryPatternFromProto(pattern internalpb.ReliableDeliveryPattern) bool {
	return pattern == internalpb.ReliableDeliveryPattern_RELIABLE_DELIVERY_PATTERN_WORK_PULLING
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
