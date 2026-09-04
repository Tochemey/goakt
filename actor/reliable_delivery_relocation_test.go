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
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// sharedQueueStates backs sharedDurableQueue instances across the in-process
// cluster nodes, modeling an external durable store every node can reach.
// Instances resolve their backing state by queue ID, so an instance
// reconstructed on another node observes the same sequences, confirmations,
// and writer epoch.
var (
	sharedQueueStatesMu sync.Mutex
	sharedQueueStates   = map[string]*mockDurableQueue{}
)

// sharedDurableQueue is a relocatable DurableProducerQueue: only its ID
// crosses the wire, and every instance delegates to the process-global state
// registered under that ID.
type sharedDurableQueue struct {
	id string
}

// newSharedDurableQueue creates a queue handle and its backing state.
func newSharedDurableQueue(id string) *sharedDurableQueue {
	queue := &sharedDurableQueue{id: id}
	queue.backing()
	return queue
}

// backing resolves the process-global state of this queue, creating it on
// first use so a reconstructed instance attaches to the same store.
func (x *sharedDurableQueue) backing() *mockDurableQueue {
	sharedQueueStatesMu.Lock()
	defer sharedQueueStatesMu.Unlock()

	state, ok := sharedQueueStates[x.id]
	if !ok {
		state = &mockDurableQueue{}
		sharedQueueStates[x.id] = state
	}

	return state
}

func (x *sharedDurableQueue) ID() string                     { return x.id }
func (x *sharedDurableQueue) MarshalBinary() ([]byte, error) { return []byte(x.id), nil }

func (x *sharedDurableQueue) UnmarshalBinary(data []byte) error {
	x.id = string(data)
	return nil
}

func (x *sharedDurableQueue) Load(ctx context.Context) (DurableQueueState, QueueEpoch, error) {
	return x.backing().Load(ctx)
}

func (x *sharedDurableQueue) Store(ctx context.Context, epoch QueueEpoch, request StoreRequest) (StoreResult, error) {
	return x.backing().Store(ctx, epoch, request)
}

func (x *sharedDurableQueue) StoreChunked(ctx context.Context, epoch QueueEpoch, requests []StoreRequest) ([]StoreResult, error) {
	return x.backing().StoreChunked(ctx, epoch, requests)
}

func (x *sharedDurableQueue) Accept(ctx context.Context, epoch QueueEpoch, messageID string) error {
	return x.backing().Accept(ctx, epoch, messageID)
}

func (x *sharedDurableQueue) Confirm(ctx context.Context, epoch QueueEpoch, upToSeq int64) error {
	return x.backing().Confirm(ctx, epoch, upToSeq)
}

// sharedWorkQueueStates backs sharedDurableWorkQueue instances across the
// in-process cluster nodes, modeling an external durable work store.
var (
	sharedWorkQueueStatesMu sync.Mutex
	sharedWorkQueueStates   = map[string]*mockDurableWorkQueue{}
)

// sharedDurableWorkQueue is a relocatable DurableWorkQueue: only its ID
// crosses the wire, and every instance delegates to the process-global state
// registered under that ID.
type sharedDurableWorkQueue struct {
	id string
}

// newSharedDurableWorkQueue creates a work-queue handle and its backing state.
func newSharedDurableWorkQueue(id string) *sharedDurableWorkQueue {
	queue := &sharedDurableWorkQueue{id: id}
	queue.backing()
	return queue
}

// backing resolves the process-global state of this work queue, creating it on
// first use so a reconstructed instance attaches to the same store.
func (x *sharedDurableWorkQueue) backing() *mockDurableWorkQueue {
	sharedWorkQueueStatesMu.Lock()
	defer sharedWorkQueueStatesMu.Unlock()

	state, ok := sharedWorkQueueStates[x.id]
	if !ok {
		state = &mockDurableWorkQueue{}
		sharedWorkQueueStates[x.id] = state
	}

	return state
}

func (x *sharedDurableWorkQueue) ID() string                     { return x.id }
func (x *sharedDurableWorkQueue) MarshalBinary() ([]byte, error) { return []byte(x.id), nil }

func (x *sharedDurableWorkQueue) UnmarshalBinary(data []byte) error {
	x.id = string(data)
	return nil
}

func (x *sharedDurableWorkQueue) Load(ctx context.Context) (WorkQueueState, QueueEpoch, error) {
	return x.backing().Load(ctx)
}

func (x *sharedDurableWorkQueue) Store(ctx context.Context, epoch QueueEpoch, request StoreRequest) (StoreResult, error) {
	return x.backing().Store(ctx, epoch, request)
}

func (x *sharedDurableWorkQueue) Accept(ctx context.Context, epoch QueueEpoch, messageID string) error {
	return x.backing().Accept(ctx, epoch, messageID)
}

func (x *sharedDurableWorkQueue) ConfirmMessage(ctx context.Context, epoch QueueEpoch, messageID string) error {
	return x.backing().ConfirmMessage(ctx, epoch, messageID)
}

// reliableRelocationConsumerMock is a reliableConsumerMock whose zero value
// confirms deliveries, so the fresh instance a relocation creates behaves
// like the original spawn.
type reliableRelocationConsumerMock struct {
	reliableConsumerMock
}

func (x *reliableRelocationConsumerMock) PreStart(*Context) error {
	x.autoConfirm = true
	return nil
}

// newReliableRelocationFixture starts a three-node NATS-backed cluster whose
// nodes register the reliable endpoint kinds, and returns a stopNode function
// that gracefully stops one node mid-test; the cleanup stops the remainder.
// The registry runs with a backup replica: with replicaCount 1 the partitions
// primaried on the departed node are lost with it, and a lost peer-endpoint
// record would wedge companion resolution instead of exercising relocation.
func newReliableRelocationFixture(t *testing.T) (context.Context, []*actorSystem, func(index int)) {
	t.Helper()

	ctx := context.TODO()
	server := startNatsServer(t)
	built, providers := testNATsConcurrent(t, server.Addr().String(), 3,
		withTestExtraKinds(&reliableProducerMock{}, &reliableRelocationConsumerMock{}),
		withTestReplication(2, 1, 1),
		withTestBootstrapTimeout(20*time.Second))

	systems := make([]*actorSystem, len(built))

	for i, system := range built {
		systems[i] = system.(*actorSystem)
	}

	pause.For(time.Second)

	stopped := make([]bool, len(built))
	stopNode := func(index int) {
		t.Helper()
		require.NoError(t, built[index].Stop(context.WithoutCancel(ctx)))
		stopped[index] = true
	}

	t.Cleanup(func() {
		for i, system := range built {
			if !stopped[i] {
				assert.NoError(t, system.Stop(context.WithoutCancel(ctx)))
			}

			assert.NoError(t, providers[i].Close())
		}

		server.Shutdown()
	})

	return ctx, systems, stopNode
}

// awaitLocalEndpoint waits for name to be respawned on one of the given nodes
// and returns its local PID.
func awaitLocalEndpoint(t *testing.T, nodes []*actorSystem, name string) *PID {
	t.Helper()

	var relocated *PID

	require.Eventually(t, func() bool {
		for _, node := range nodes {
			if pidNode, ok := node.actors.nodeByName(name); ok {
				if pid := pidNode.value(); pid != nil && pid.IsRunning() {
					relocated = pid
					return true
				}
			}
		}

		return false
	}, 30*time.Second, 100*time.Millisecond, "endpoint %s must be respawned on a survivor", name)

	return relocated
}

func TestWorkPullingProducerRelocation(t *testing.T) {
	ctx, systems, stopNode := newReliableRelocationFixture(t)
	node1, node2, node3 := systems[0], systems[1], systems[2]

	queue := newSharedDurableWorkQueue("jobs-queue-" + uuid.NewString())

	for _, node := range systems {
		require.NoError(t, node.Inject(queue))
	}

	producer, err := node1.Spawn(ctx, "jobs-producer", &reliableProducerMock{},
		AsReliableWorkPullingProducer(
			WithReliableDurableWorkQueue(queue),
			WithReliableRetryInterval(200*time.Millisecond)))
	require.NoError(t, err)

	worker, err := node2.Spawn(ctx, "jobs-worker", &reliableRelocationConsumerMock{},
		AsReliableWorkPullingWorker("jobs-producer", WithReliableResendInterval(200*time.Millisecond)))
	require.NoError(t, err)

	oldIncarnation := producer.incarnationID()
	oldCompanion := reliableCompanionName(ReliableControllerRoleProducer, oldIncarnation)

	for i := 1; i <= 2; i++ {
		id := fmt.Sprintf("job-%d", i)
		require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: id, payload: testpb.Reply_builder{Content: id}.Build()}))
	}

	deliveries := awaitDeliveries(t, ctx, worker, 2)
	require.Len(t, deliveries, 2)

	// wait for per-message confirmations to persist so the relocated
	// controller resumes from a clean durable state
	backing := queue.backing()
	require.Eventually(t, func() bool {
		return backing.confirmedCount() == 2
	}, 10*time.Second, 50*time.Millisecond)

	oldEpoch := backing.epoch

	stopNode(0)

	relocated := awaitLocalEndpoint(t, []*actorSystem{node2, node3}, "jobs-producer")
	require.NotNil(t, relocated.reliableDelivery())
	require.True(t, relocated.reliableDelivery().producer.workPulling)
	require.NotNil(t, relocated.durableWorkQueue())
	assert.NotEqual(t, oldIncarnation, relocated.incarnationID())

	// the relocated controller reloaded the durable work queue under a new
	// epoch, fencing any writer of the departed activation
	require.Eventually(t, func() bool {
		backing.mu.Lock()
		defer backing.mu.Unlock()
		return backing.epoch > oldEpoch
	}, 20*time.Second, 100*time.Millisecond)

	_, err = backing.Store(ctx, oldEpoch, mustStoreRequest(t, "stale-write", 3))
	require.ErrorIs(t, err, gerrors.ErrQueueFenced)

	require.Eventually(t, func() bool {
		companion, cerr := node3.resolveReliableCompanion(ctx, "jobs-producer", ReliableControllerRoleProducer, nil)
		return cerr == nil && companion.Name() == reliableCompanionName(ReliableControllerRoleProducer, relocated.incarnationID())
	}, 20*time.Second, 100*time.Millisecond)

	require.Eventually(t, func() bool {
		_, gerr := node3.getCluster().GetActor(ctx, oldCompanion)
		return gerr != nil
	}, 20*time.Second, 100*time.Millisecond, "the departed controller record must be withdrawn")

	// delivery resumes across the relocation to the surviving worker. Per-worker
	// sequences restart with the new producer session, so assert by MessageID
	// rather than distinct sequence count.
	require.NoError(t, Tell(ctx, relocated, &produceSubmission{messageID: "job-3", payload: testpb.Reply_builder{Content: "job-3"}.Build()}))

	require.Eventually(t, func() bool {
		for _, delivery := range distinctDeliveries(awaitDeliveriesSnapshot(t, ctx, worker)) {
			if delivery.MessageID() == "job-3" {
				return true
			}
		}

		return false
	}, 20*time.Second, 50*time.Millisecond)
}

func TestReliableProducerRelocation(t *testing.T) {
	ctx, systems, stopNode := newReliableRelocationFixture(t)
	node1, node2, node3 := systems[0], systems[1], systems[2]

	queue := newSharedDurableQueue("orders-queue-" + uuid.NewString())

	for _, node := range systems {
		require.NoError(t, node.Inject(queue))
	}

	producer, err := node1.Spawn(ctx, "orders-producer", &reliableProducerMock{},
		AsReliableProducer("orders-consumer",
			WithReliableDurableQueue(queue),
			WithReliableRetryInterval(200*time.Millisecond)))
	require.NoError(t, err)

	consumer, err := node2.Spawn(ctx, "orders-consumer", &reliableRelocationConsumerMock{},
		AsReliableConsumer("orders-producer", WithReliableResendInterval(200*time.Millisecond)))
	require.NoError(t, err)

	oldIncarnation := producer.incarnationID()
	oldCompanion := reliableCompanionName(ReliableControllerRoleProducer, oldIncarnation)

	for i := 1; i <= 2; i++ {
		id := fmt.Sprintf("ord-%d", i)
		require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: id, payload: testpb.Reply_builder{Content: id}.Build()}))
	}

	deliveries := awaitDeliveries(t, ctx, consumer, 2)
	require.Len(t, deliveries, 2)

	// wait for the confirmation watermark to persist so the relocated
	// controller resumes from a clean durable state
	backing := queue.backing()
	require.Eventually(t, func() bool {
		_, _, confirmed := backing.snapshot()
		return confirmed == 2
	}, 10*time.Second, 50*time.Millisecond)

	oldEpoch := backing.epoch

	// the producer node leaves gracefully: its snapshot drives the relocation
	// and its registry records are withdrawn with it
	stopNode(0)

	relocated := awaitLocalEndpoint(t, []*actorSystem{node2, node3}, "orders-producer")
	require.NotNil(t, relocated.reliableDelivery())
	require.NotNil(t, relocated.durableQueue())
	assert.NotEqual(t, oldIncarnation, relocated.incarnationID())

	// the relocated controller reloaded the durable state under a new epoch,
	// fencing any writer of the departed activation
	require.Eventually(t, func() bool {
		backing.mu.Lock()
		defer backing.mu.Unlock()
		return backing.epoch > oldEpoch
	}, 20*time.Second, 100*time.Millisecond)

	_, err = backing.Store(ctx, oldEpoch, mustStoreRequest(t, "stale-write", 3))
	require.ErrorIs(t, err, gerrors.ErrQueueFenced)

	// exactly one fresh companion exists under the new incarnation and the
	// departed activation's records are gone cluster-wide
	require.Eventually(t, func() bool {
		companion, cerr := node3.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer, nil)
		return cerr == nil && companion.Name() == reliableCompanionName(ReliableControllerRoleProducer, relocated.incarnationID())
	}, 20*time.Second, 100*time.Millisecond)

	require.Eventually(t, func() bool {
		_, gerr := node3.getCluster().GetActor(ctx, oldCompanion)
		return gerr != nil
	}, 20*time.Second, 100*time.Millisecond, "the departed controller record must be withdrawn")

	// delivery resumes across the relocation on the same flow
	require.NoError(t, Tell(ctx, relocated, &produceSubmission{messageID: "ord-3", payload: testpb.Reply_builder{Content: "ord-3"}.Build()}))

	deliveries = awaitDeliveries(t, ctx, consumer, 3)
	assert.Equal(t, "ord-3", deliveries[len(deliveries)-1].MessageID())
}

func TestReliableConsumerRelocation(t *testing.T) {
	ctx, systems, stopNode := newReliableRelocationFixture(t)
	node1, node2, node3 := systems[0], systems[1], systems[2]

	producer, err := node1.Spawn(ctx, "orders-producer", &reliableProducerMock{},
		AsReliableProducer("orders-consumer", WithReliableRetryInterval(200*time.Millisecond)))
	require.NoError(t, err)

	consumer, err := node2.Spawn(ctx, "orders-consumer", &reliableRelocationConsumerMock{},
		AsReliableConsumer("orders-producer", WithReliableResendInterval(200*time.Millisecond)))
	require.NoError(t, err)

	require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: "ord-1", payload: testpb.Reply_builder{Content: "ord-1"}.Build()}))
	deliveries := awaitDeliveries(t, ctx, consumer, 1)
	require.Len(t, deliveries, 1)

	// the consumer node leaves gracefully; the endpoint reconstructs on a
	// survivor and its fresh controller re-registers with the producer side
	stopNode(1)

	relocated := awaitLocalEndpoint(t, []*actorSystem{node1, node3}, "orders-consumer")
	require.NotNil(t, relocated.reliableDelivery())

	require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: "ord-2", payload: testpb.Reply_builder{Content: "ord-2"}.Build()}))

	// the fresh consumer instance starts empty; the next delivery proves the
	// relocated flow is live end to end
	deliveries = awaitDeliveries(t, ctx, relocated, 1)
	assert.Equal(t, "ord-2", deliveries[len(deliveries)-1].MessageID())
}

func TestReliableRelocationMissingQueueTypeRestoresRecord(t *testing.T) {
	ctx, systems, stopNode := newReliableRelocationFixture(t)
	node1, node3 := systems[0], systems[2]

	// the queue type is never injected on the survivors: reconstruction on
	// any survivor must fail and restore the departed record for a later
	// relocation retry instead of silently spawning a volatile endpoint
	queue := newSharedDurableQueue("orders-queue-" + uuid.NewString())

	producer, err := node1.Spawn(ctx, "orders-producer", &reliableProducerMock{},
		AsReliableProducer("orders-consumer", WithReliableDurableQueue(queue)))
	require.NoError(t, err)

	departedNode := address.FormatHostPort(node1.Host(), node1.Port())
	incarnation := producer.incarnationID()

	stopNode(0)

	require.Eventually(t, func() bool {
		record, gerr := node3.getCluster().GetActor(ctx, "orders-producer")
		if gerr != nil {
			return false
		}

		addr, perr := address.Parse(record.GetAddress())
		return perr == nil && addr.HostPort() == departedNode && record.GetIncarnationId() == incarnation
	}, 30*time.Second, 100*time.Millisecond, "the departed endpoint record must be restored for a relocation retry")

	// the endpoint was never respawned on a survivor
	for _, node := range []*actorSystem{systems[1], node3} {
		_, ok := node.actors.nodeByName("orders-producer")
		assert.False(t, ok)
	}
}

func TestReliableNonRelocatableEndpointRecordsWithdrawnOnShutdown(t *testing.T) {
	ctx, systems, stopNode := newReliableRelocationFixture(t)
	node1, node2 := systems[0], systems[1]

	consumer, err := node1.Spawn(ctx, "orders-consumer", &reliableRelocationConsumerMock{},
		AsReliableConsumer("orders-producer"), WithRelocationDisabled())
	require.NoError(t, err)

	companionName := reliableCompanionName(ReliableControllerRoleConsumer, consumer.incarnationID())

	// both records are resolvable cluster-wide while the endpoint lives
	require.Eventually(t, func() bool {
		_, endpointErr := node2.getCluster().GetActor(ctx, "orders-consumer")
		_, companionErr := node2.getCluster().GetActor(ctx, companionName)
		return endpointErr == nil && companionErr == nil
	}, 10*time.Second, 100*time.Millisecond)

	// a non-relocatable endpoint dies with its node, but its records must not
	// outlive it: leaked records would block the name cluster-wide because
	// reliable endpoints publish with if-absent semantics
	stopNode(0)

	require.Eventually(t, func() bool {
		_, endpointErr := node2.getCluster().GetActor(ctx, "orders-consumer")
		_, companionErr := node2.getCluster().GetActor(ctx, companionName)
		return errors.Is(endpointErr, cluster.ErrActorNotFound) && errors.Is(companionErr, cluster.ErrActorNotFound)
	}, 20*time.Second, 100*time.Millisecond, "the endpoint and controller records must be withdrawn")

	// the name is immediately reusable on a survivor
	fresh, err := node2.Spawn(ctx, "orders-consumer", &reliableRelocationConsumerMock{},
		AsReliableConsumer("orders-producer"))
	require.NoError(t, err)
	assert.True(t, fresh.IsRunning())
}

// mustStoreRequest builds a store request for fencing assertions.
func mustStoreRequest(t *testing.T, messageID string, seq int64) StoreRequest {
	t.Helper()

	reliablePayload, err := NewReliablePayload([]byte(messageID))
	require.NoError(t, err)

	request, err := NewStoreRequest(messageID, seq, reliablePayload)
	require.NoError(t, err)
	return request
}
