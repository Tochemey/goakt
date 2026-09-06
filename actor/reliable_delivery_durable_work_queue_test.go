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
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestNewWorkQueueState(t *testing.T) {
	payload, err := NewReliablePayload([]byte("p"))
	require.NoError(t, err)

	first, err := NewUnconfirmedMessage("m-1", 1, payload)
	require.NoError(t, err)
	third, err := NewUnconfirmedMessage("m-3", 3, payload)
	require.NoError(t, err)

	t.Run("With holes", func(t *testing.T) {
		state, err := NewWorkQueueState(3, []UnconfirmedMessage{first, third})
		require.NoError(t, err)
		assert.EqualValues(t, 3, state.CurrentSeq())
		require.Len(t, state.Unconfirmed(), 2)
		assert.Equal(t, "m-1", state.Unconfirmed()[0].MessageID())
		assert.Equal(t, "m-3", state.Unconfirmed()[1].MessageID())
	})

	t.Run("With unordered sequences", func(t *testing.T) {
		_, err := NewWorkQueueState(3, []UnconfirmedMessage{third, first})
		require.Error(t, err)
	})

	t.Run("With sequence above current", func(t *testing.T) {
		_, err := NewWorkQueueState(2, []UnconfirmedMessage{third})
		require.Error(t, err)
	})
}

func TestMockDurableWorkQueueConfirmMessage(t *testing.T) {
	ctx := context.TODO()
	queue := &MockDurableWorkQueue{}

	state, epoch, err := queue.Load(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 0, state.CurrentSeq())

	payload, err := NewReliablePayload([]byte("job"))
	require.NoError(t, err)
	request, err := NewStoreRequest("job-1", 1, payload)
	require.NoError(t, err)

	_, err = queue.Store(ctx, epoch, request)
	require.NoError(t, err)
	require.NoError(t, queue.Accept(ctx, epoch, "job-1"))
	require.NoError(t, queue.ConfirmMessage(ctx, epoch, "job-1"))
	require.NoError(t, queue.ConfirmMessage(ctx, epoch, "job-1"))

	err = queue.ConfirmMessage(ctx, epoch, "missing")
	require.ErrorIs(t, err, gerrors.ErrQueueConflict)

	err = queue.ConfirmMessage(ctx, epoch+1, "job-1")
	require.ErrorIs(t, err, gerrors.ErrQueueFenced)

	_, ops := queue.snapshot()
	assert.Equal(t, []string{"store:job-1", "accept:job-1", "confirm:job-1", "confirm:job-1"}, ops)
}

func TestWorkPullingDurableEndToEnd(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)
	queue := &MockDurableWorkQueue{}

	producer, err := system.Spawn(ctx, "jobs-producer", &MockReliableProducer{},
		AsReliableWorkPullingProducer(WithReliableDurableWorkQueue(queue), WithReliableRetryInterval(100*time.Millisecond)))
	require.NoError(t, err)

	worker, err := system.Spawn(ctx, "jobs-worker", &MockReliableConsumer{autoConfirm: true},
		AsReliableWorkPullingWorker("jobs-producer", WithReliableResendInterval(200*time.Millisecond)))
	require.NoError(t, err)

	for i := 1; i <= 2; i++ {
		id := fmt.Sprintf("job-%d", i)
		require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: id, payload: testpb.Reply_builder{Content: id}.Build()}))
	}

	deliveries := awaitDeliveries(t, ctx, worker, 2)
	require.Len(t, deliveries, 2)

	require.Eventually(t, func() bool {
		_, ops := queue.snapshot()
		return containsAllOperations(ops, "store:job-1", "accept:job-1", "store:job-2", "accept:job-2") &&
			containsAllOperations(ops, "confirm:job-1") &&
			containsAllOperations(ops, "confirm:job-2")
	}, 10*time.Second, 20*time.Millisecond)
}

func TestWorkPullingDurableReloadRedispatches(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)
	queue := &MockDurableWorkQueue{}

	reply := testpb.Reply_builder{Content: "job-1"}.Build()
	serializer := system.getRemoting().Serializer(reply)
	require.NotNil(t, serializer)
	frame, err := serializer.Serialize(reply)
	require.NoError(t, err)
	seedPayload, err := newReliablePayload(frame)
	require.NoError(t, err)
	require.NoError(t, queue.seedAccepted("job-1", 1, seedPayload))

	_, err = system.Spawn(ctx, "jobs-producer", &MockReliableProducer{},
		AsReliableWorkPullingProducer(WithReliableDurableWorkQueue(queue), WithReliableRetryInterval(100*time.Millisecond)))
	require.NoError(t, err)

	worker, err := system.Spawn(ctx, "jobs-worker", &MockReliableConsumer{autoConfirm: true},
		AsReliableWorkPullingWorker("jobs-producer", WithReliableResendInterval(200*time.Millisecond)))
	require.NoError(t, err)

	deliveries := awaitDeliveries(t, ctx, worker, 1)
	require.Len(t, deliveries, 1)
	assert.Equal(t, "job-1", deliveries[0].MessageID())

	delivered, ok := deliveries[0].Payload().(*testpb.Reply)
	require.True(t, ok)
	assert.Equal(t, "job-1", delivered.GetContent())

	require.Eventually(t, func() bool {
		_, ops := queue.snapshot()
		return containsAllOperations(ops, "confirm:job-1")
	}, 10*time.Second, 20*time.Millisecond)

	loads, _ := queue.snapshot()
	assert.GreaterOrEqual(t, loads, 1)
}

func TestAsReliableWorkPullingProducerWithReliableDurableWorkQueue(t *testing.T) {
	queue := &MockDurableWorkQueue{}
	config := newSpawnConfig(AsReliableWorkPullingProducer(WithReliableDurableWorkQueue(queue)))
	require.NotNil(t, config.durableWorkQueue)
	assert.Same(t, queue, config.durableWorkQueue)
	assert.True(t, config.reliableDelivery.producer.workPulling)
	assert.Equal(t, queue.ID(), config.reliableDelivery.producer.durableQueueID)
	require.NoError(t, config.Validate())
	require.Len(t, config.dependencies, 1)
}
