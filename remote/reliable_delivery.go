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

package remote

import (
	"errors"
	"strings"
	"time"
)

// ReliableDeliverySpec marks a remotely spawned actor as one endpoint of a
// reliable delivery flow. Exactly one side must be set. The hosting node
// restores the settings as spawn options, so remote placement behaves exactly
// like a local AsReliableProducer or AsReliableConsumer spawn, including the
// creation of the system-owned controller companion next to the endpoint.
//
// The spec is set by the actor system when a reliable endpoint targets a
// remote node through Spawn or SpawnOn. The standalone cluster client rejects
// spawn requests carrying it, because reliable endpoints require the spawning
// side to participate in the delivery protocol.
type ReliableDeliverySpec struct {
	// Producer configures the producer side of the flow; exactly one side is
	// set.
	Producer *ReliableProducerSpec

	// Consumer configures the consumer side of the flow; exactly one side is
	// set.
	Consumer *ReliableConsumerSpec
}

// ReliableProducerSpec carries the producer-side delivery settings of a
// remotely spawned reliable producer endpoint.
type ReliableProducerSpec struct {
	// ConsumerName names the one consumer endpoint authorized to receive the
	// producer's messages.
	ConsumerName string

	// DurableQueueID references the durable producer queue among the spawn
	// request dependencies; empty runs the flow without durable storage.
	DurableQueueID string

	// QueueRetryMaxAttempts is the maximum number of attempts for one durable
	// queue operation; it must be at least 1.
	QueueRetryMaxAttempts int

	// QueueRetryInitialBackoff is the delay before the first queue operation
	// retry; it must be positive.
	QueueRetryInitialBackoff time.Duration

	// LocalRetryInterval is the cadence at which the producer controller
	// retries an unanswered RequestNext or Stored toward the producer
	// endpoint; it must be positive.
	LocalRetryInterval time.Duration

	// DeliveryConfirmation enables DeliveryConfirmed notifications toward the
	// producer endpoint once the consumer confirms a message.
	DeliveryConfirmation bool
}

// ReliableConsumerSpec carries the consumer-side delivery settings of a
// remotely spawned reliable consumer endpoint.
type ReliableConsumerSpec struct {
	// ProducerName names the producer endpoint whose messages are accepted.
	ProducerName string

	// FlowControlWindow is the maximum demand granted in one request; it must
	// be at least 1. The hosting node enforces its upper bound.
	FlowControlWindow int

	// ResendInterval is the registration, demand, and delivery retry cadence;
	// it must be positive.
	ResendInterval time.Duration
}

// Validate checks that exactly one endpoint side is configured and that its
// settings are usable. The hosting node revalidates the restored spawn
// configuration, so this is the early client-side check.
func (x *ReliableDeliverySpec) Validate() error {
	switch {
	case x.Producer != nil && x.Consumer != nil:
		return errors.New("reliable delivery specification must set exactly one endpoint side")
	case x.Producer != nil:
		return x.Producer.Validate()
	case x.Consumer != nil:
		return x.Consumer.Validate()
	default:
		return errors.New("reliable delivery specification requires an endpoint side")
	}
}

// Validate checks the producer-side delivery settings.
func (x *ReliableProducerSpec) Validate() error {
	if strings.TrimSpace(x.ConsumerName) == "" {
		return errors.New("consumer endpoint name is required")
	}

	if x.QueueRetryMaxAttempts < 1 {
		return errors.New("queue retry max attempts must be at least 1")
	}

	if x.QueueRetryInitialBackoff <= 0 {
		return errors.New("queue retry initial backoff must be positive")
	}

	if x.LocalRetryInterval <= 0 {
		return errors.New("local retry interval must be positive")
	}

	return nil
}

// Validate checks the consumer-side delivery settings.
func (x *ReliableConsumerSpec) Validate() error {
	if strings.TrimSpace(x.ProducerName) == "" {
		return errors.New("producer endpoint name is required")
	}

	if x.FlowControlWindow < 1 {
		return errors.New("flow control window must be at least 1")
	}

	if x.ResendInterval <= 0 {
		return errors.New("resend interval must be positive")
	}

	return nil
}

// sanitize trims the endpoint and queue identifiers carried by the spec.
func (x *ReliableDeliverySpec) sanitize() {
	if x.Producer != nil {
		x.Producer.ConsumerName = strings.TrimSpace(x.Producer.ConsumerName)
		x.Producer.DurableQueueID = strings.TrimSpace(x.Producer.DurableQueueID)
	}

	if x.Consumer != nil {
		x.Consumer.ProducerName = strings.TrimSpace(x.Consumer.ProducerName)
	}
}
