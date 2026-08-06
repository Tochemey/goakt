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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validReliableProducerSpec() *ReliableProducerSpec {
	return &ReliableProducerSpec{
		ConsumerName:             "orders-consumer",
		DurableQueueID:           "orders-queue",
		QueueRetryMaxAttempts:    3,
		QueueRetryInitialBackoff: 100 * time.Millisecond,
		LocalRetryInterval:       time.Second,
	}
}

func validReliableConsumerSpec() *ReliableConsumerSpec {
	return &ReliableConsumerSpec{
		ProducerName:      "orders-producer",
		FlowControlWindow: 16,
		ResendInterval:    time.Second,
	}
}

func TestReliableDeliverySpecValidate(t *testing.T) {
	testCases := []struct {
		name    string
		spec    *ReliableDeliverySpec
		wantErr string
	}{
		{
			name: "valid producer side",
			spec: &ReliableDeliverySpec{Producer: validReliableProducerSpec()},
		},
		{
			name: "valid consumer side",
			spec: &ReliableDeliverySpec{Consumer: validReliableConsumerSpec()},
		},
		{
			name:    "both sides set",
			spec:    &ReliableDeliverySpec{Producer: validReliableProducerSpec(), Consumer: validReliableConsumerSpec()},
			wantErr: "exactly one endpoint side",
		},
		{
			name:    "no side set",
			spec:    &ReliableDeliverySpec{},
			wantErr: "requires an endpoint side",
		},
		{
			name: "producer without consumer name",
			spec: func() *ReliableDeliverySpec {
				producer := validReliableProducerSpec()
				producer.ConsumerName = "  "
				return &ReliableDeliverySpec{Producer: producer}
			}(),
			wantErr: "consumer endpoint name is required",
		},
		{
			name: "producer without retry attempts",
			spec: func() *ReliableDeliverySpec {
				producer := validReliableProducerSpec()
				producer.QueueRetryMaxAttempts = 0
				return &ReliableDeliverySpec{Producer: producer}
			}(),
			wantErr: "queue retry max attempts",
		},
		{
			name: "producer without retry backoff",
			spec: func() *ReliableDeliverySpec {
				producer := validReliableProducerSpec()
				producer.QueueRetryInitialBackoff = 0
				return &ReliableDeliverySpec{Producer: producer}
			}(),
			wantErr: "queue retry initial backoff",
		},
		{
			name: "producer without local retry interval",
			spec: func() *ReliableDeliverySpec {
				producer := validReliableProducerSpec()
				producer.LocalRetryInterval = 0
				return &ReliableDeliverySpec{Producer: producer}
			}(),
			wantErr: "local retry interval",
		},
		{
			name: "consumer without producer name",
			spec: func() *ReliableDeliverySpec {
				consumer := validReliableConsumerSpec()
				consumer.ProducerName = ""
				return &ReliableDeliverySpec{Consumer: consumer}
			}(),
			wantErr: "producer endpoint name is required",
		},
		{
			name: "consumer without flow control window",
			spec: func() *ReliableDeliverySpec {
				consumer := validReliableConsumerSpec()
				consumer.FlowControlWindow = 0
				return &ReliableDeliverySpec{Consumer: consumer}
			}(),
			wantErr: "flow control window",
		},
		{
			name: "consumer without resend interval",
			spec: func() *ReliableDeliverySpec {
				consumer := validReliableConsumerSpec()
				consumer.ResendInterval = 0
				return &ReliableDeliverySpec{Consumer: consumer}
			}(),
			wantErr: "resend interval",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.spec.Validate()

			if testCase.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.ErrorContains(t, err, testCase.wantErr)
		})
	}
}

func TestSpawnRequestReliableDelivery(t *testing.T) {
	t.Run("valid reliable request passes validation", func(t *testing.T) {
		request := &SpawnRequest{
			Name:             "orders-producer",
			Kind:             "endpoint",
			ReliableDelivery: &ReliableDeliverySpec{Producer: validReliableProducerSpec()},
		}

		require.NoError(t, request.Validate())
	})

	t.Run("reliable singleton is rejected", func(t *testing.T) {
		request := &SpawnRequest{
			Name:             "orders-producer",
			Kind:             "endpoint",
			Singleton:        &SingletonSpec{SpawnTimeout: time.Second, WaitInterval: time.Second, MaxRetries: 1},
			ReliableDelivery: &ReliableDeliverySpec{Producer: validReliableProducerSpec()},
		}

		require.ErrorContains(t, request.Validate(), "cannot be singletons")
	})

	t.Run("invalid spec fails request validation", func(t *testing.T) {
		request := &SpawnRequest{
			Name:             "orders-producer",
			Kind:             "endpoint",
			ReliableDelivery: &ReliableDeliverySpec{},
		}

		require.ErrorContains(t, request.Validate(), "requires an endpoint side")
	})

	t.Run("sanitize trims spec identifiers", func(t *testing.T) {
		producer := validReliableProducerSpec()
		producer.ConsumerName = "  orders-consumer  "
		producer.DurableQueueID = " orders-queue "

		consumerSpec := validReliableConsumerSpec()
		consumerSpec.ProducerName = " orders-producer "

		request := &SpawnRequest{
			Name:             " endpoint ",
			Kind:             "kind",
			ReliableDelivery: &ReliableDeliverySpec{Producer: producer},
		}
		request.Sanitize()
		assert.Equal(t, "orders-consumer", producer.ConsumerName)
		assert.Equal(t, "orders-queue", producer.DurableQueueID)

		request = &SpawnRequest{
			Name:             "endpoint",
			Kind:             "kind",
			ReliableDelivery: &ReliableDeliverySpec{Consumer: consumerSpec},
		}
		request.Sanitize()
		assert.Equal(t, "orders-producer", consumerSpec.ProducerName)
	})
}
