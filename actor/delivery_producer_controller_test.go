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
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// mockDurableQueue models an external linearizable store. Unlike actors it is
// shared with the controller's asynchronous task goroutines, so it guards its
// state with a mutex exactly as a real storage client would.
type mockDurableQueue struct {
	mu           sync.Mutex
	epoch        QueueEpoch
	currentSeq   int64
	confirmedSeq int64
	stored       []UnconfirmedMessage
	loads        int
	operations   []string
	storeErr     error
	acceptErr    error
	confirmErr   error
	loadErr      error
	storeDelay   time.Duration
	confirmDelay time.Duration
	// retainConfirmed keeps confirmed entries in the MessageID index, which
	// the contract permits until Accept and Confirm both cover a message, so
	// first-write-wins still answers a resubmission of a confirmed MessageID.
	retainConfirmed bool
}

func (x *mockDurableQueue) ID() string                     { return "mockDurableQueue" }
func (x *mockDurableQueue) MarshalBinary() ([]byte, error) { return []byte(x.ID()), nil }
func (x *mockDurableQueue) UnmarshalBinary([]byte) error   { return nil }

func (x *mockDurableQueue) Load(context.Context) (DurableQueueState, QueueEpoch, error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	x.loads++

	if x.loadErr != nil {
		return DurableQueueState{}, 0, x.loadErr
	}

	x.epoch++

	unconfirmed := make([]UnconfirmedMessage, 0, len(x.stored))

	for _, message := range x.stored {
		if message.Seq() > x.confirmedSeq {
			unconfirmed = append(unconfirmed, message)
		}
	}

	state, err := NewDurableQueueState(x.currentSeq, x.confirmedSeq, unconfirmed)
	if err != nil {
		return DurableQueueState{}, 0, err
	}

	return state, x.epoch, nil
}

func (x *mockDurableQueue) Store(_ context.Context, epoch QueueEpoch, request StoreRequest) (StoreResult, error) {
	x.mu.Lock()
	delay, storeErr := x.storeDelay, x.storeErr
	x.mu.Unlock()

	if delay > 0 {
		pause.For(delay)
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	if storeErr != nil {
		return StoreResult{}, storeErr
	}

	if epoch != x.epoch {
		return StoreResult{}, gerrors.ErrQueueFenced
	}

	for _, message := range x.stored {
		if message.MessageID() == request.MessageID() {
			return NewStoreResult(message.Seq(), true, message.Payload())
		}
	}

	if request.ProposedSeq() != x.currentSeq+1 {
		return StoreResult{}, gerrors.ErrQueueConflict
	}

	message, err := NewUnconfirmedMessage(request.MessageID(), request.ProposedSeq(), request.Payload())
	if err != nil {
		return StoreResult{}, err
	}

	x.currentSeq = request.ProposedSeq()
	x.stored = append(x.stored, message)
	x.operations = append(x.operations, "store:"+request.MessageID())
	return NewStoreResult(request.ProposedSeq(), false, request.Payload())
}

func (x *mockDurableQueue) StoreChunked(_ context.Context, epoch QueueEpoch, requests []StoreRequest) ([]StoreResult, error) {
	x.mu.Lock()
	delay, storeErr := x.storeDelay, x.storeErr
	x.mu.Unlock()

	if delay > 0 {
		pause.For(delay)
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	if storeErr != nil {
		return nil, storeErr
	}

	if epoch != x.epoch {
		return nil, gerrors.ErrQueueFenced
	}

	if len(requests) == 0 {
		return nil, gerrors.NewErrInvalidMessage(errors.New("chunked store requires at least one chunk"))
	}

	businessID, index, count, ok := parseDurableChunkMessageID(requests[0].MessageID())
	if !ok || index != 1 || count != len(requests) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("chunked store requests must be a complete derived-ID batch"))
	}

	for position, request := range requests {
		requestBusiness, requestIndex, requestCount, requestOK := parseDurableChunkMessageID(request.MessageID())
		if !requestOK || requestBusiness != businessID || requestIndex != position+1 || requestCount != count {
			return nil, gerrors.NewErrInvalidMessage(errors.New("chunked store requests must share one business MessageID and contiguous positions"))
		}
	}

	existing := make([]UnconfirmedMessage, 0, count)

	for _, message := range x.stored {
		if idFrom(message.MessageID()) == businessID {
			existing = append(existing, message)
		}
	}

	if len(existing) > 0 {
		// first-write-wins for the business MessageID: return the original
		// batch even when the retry proposes a different chunk count or bytes
		results := make([]StoreResult, 0, len(existing))

		for _, message := range existing {
			result, err := NewStoreResult(message.Seq(), true, message.Payload())
			if err != nil {
				return nil, err
			}

			results = append(results, result)
		}

		x.operations = append(x.operations, "storechunked:"+businessID)
		return results, nil
	}

	if requests[0].ProposedSeq() != x.currentSeq+1 {
		return nil, gerrors.ErrQueueConflict
	}

	for position, request := range requests {
		if request.ProposedSeq() != x.currentSeq+int64(position)+1 {
			return nil, gerrors.ErrQueueConflict
		}
	}

	results := make([]StoreResult, 0, count)
	appended := make([]UnconfirmedMessage, 0, count)

	for position, request := range requests {
		entry, err := newChunkUnconfirmedMessage(request.MessageID(), request.ProposedSeq(), request.Payload(), position == 0, position == count-1)
		if err != nil {
			return nil, err
		}

		result, err := NewStoreResult(request.ProposedSeq(), false, request.Payload())
		if err != nil {
			return nil, err
		}

		appended = append(appended, entry)
		results = append(results, result)
	}

	x.stored = append(x.stored, appended...)
	x.currentSeq = requests[len(requests)-1].ProposedSeq()
	x.operations = append(x.operations, "storechunked:"+businessID)
	return results, nil
}

func (x *mockDurableQueue) Accept(_ context.Context, epoch QueueEpoch, messageID string) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.acceptErr != nil {
		return x.acceptErr
	}

	if epoch != x.epoch {
		return gerrors.ErrQueueFenced
	}

	x.operations = append(x.operations, "accept:"+messageID)
	return nil
}

func (x *mockDurableQueue) Confirm(_ context.Context, epoch QueueEpoch, upToSeq int64) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	delay, confirmErr := x.confirmDelay, x.confirmErr

	if delay > 0 {
		pause.For(delay)
	}

	if confirmErr != nil {
		return confirmErr
	}

	if epoch != x.epoch {
		return gerrors.ErrQueueFenced
	}

	x.confirmedSeq = max(x.confirmedSeq, upToSeq)
	x.operations = append(x.operations, "confirm")

	if !x.retainConfirmed {
		cut := 0

		for cut < len(x.stored) && x.stored[cut].Seq() <= x.confirmedSeq {
			cut++
		}

		x.stored = x.stored[cut:]
	}

	return nil
}

// snapshot returns copies of the observable queue state.
func (x *mockDurableQueue) snapshot() (int, []string, int64) {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.loads, append([]string(nil), x.operations...), x.confirmedSeq
}

// testProducerConfig builds the producer settings a directly constructed
// controller needs, so a test states only the values it cares about.
func testProducerConfig(consumerName string, retryAttempts int, retryBackoff, localRetryInterval time.Duration) *reliableProducerConfig {
	return &reliableProducerConfig{
		consumerName:       consumerName,
		localRetryInterval: localRetryInterval,
		queueRetry: &reliableQueueRetryConfig{
			maxAttempts:    retryAttempts,
			initialBackoff: retryBackoff,
		},
	}
}

// producerControllerHarness wires a producer controller under test to a recording producer
// endpoint and a recording consumer controller stand-in.
type producerControllerHarness struct {
	ctx                       context.Context
	system                    *actorSystem
	producer                  *PID
	consumerControllerStandIn *PID
	producerController        *PID
	// usedTokens tracks credits already answered; only the test goroutine
	// touches it.
	usedTokens map[string]bool
}

// newProducerControllerHarness starts a cluster-disabled system with the
// producer endpoint, the consumer endpoint plus its registered controller
// stand-in, and the producer controller under test.
func newProducerControllerHarness(t *testing.T, queue DurableProducerQueue) *producerControllerHarness {
	t.Helper()
	return newProducerControllerHarnessWith(t, queue, false)
}

// newProducerControllerHarnessWith builds the harness with the endpoint's
// delivery-confirmation setting, which the spawn options would otherwise carry.
func newProducerControllerHarnessWith(t *testing.T, queue DurableProducerQueue, deliveryConfirmation bool) *producerControllerHarness {
	t.Helper()
	return newProducerControllerHarnessFor(t, queue, deliveryConfirmation, 0)
}

// newProducerControllerHarnessChunked builds the harness with chunking enabled
// at the given size on a volatile flow.
func newProducerControllerHarnessChunked(t *testing.T, maxChunkBytes uint32) *producerControllerHarness {
	t.Helper()
	return newProducerControllerHarnessFor(t, nil, false, maxChunkBytes)
}

// newProducerControllerHarnessFor builds the harness from the endpoint
// settings the spawn options would otherwise carry.
func newProducerControllerHarnessFor(t *testing.T, queue DurableProducerQueue, deliveryConfirmation bool, maxChunkBytes uint32) *producerControllerHarness {
	t.Helper()

	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "producer", &deliveryRecorder{})
	require.NoError(t, err)

	consumer, err := system.Spawn(ctx, "consumer", NewMockActor())
	require.NoError(t, err)

	spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "consumer", consumer.IncarnationID())
	require.NoError(t, err)

	consumerControllerName := reliableCompanionName(ReliableControllerRoleConsumer, consumer.IncarnationID())
	consumerControllerStandIn, err := system.Spawn(ctx, consumerControllerName, &deliveryRecorder{}, asSystem(), asReliableCompanion(spec))
	require.NoError(t, err)

	config := testProducerConfig("consumer", 2, 20*time.Millisecond, 150*time.Millisecond)
	config.deliveryConfirmation = deliveryConfirmation
	config.maxChunkBytes = maxChunkBytes

	producerController, err := system.Spawn(ctx, "producer-controller", newProducerController(producer, config, queue))
	require.NoError(t, err)

	return &producerControllerHarness{ctx: ctx, system: system, producer: producer, consumerControllerStandIn: consumerControllerStandIn, producerController: producerController, usedTokens: map[string]bool{}}
}

// recordedOf asks a recorder double for its message snapshot.
func (x *producerControllerHarness) recordedOf(pid *PID) []any {
	response, err := Ask(x.ctx, pid, &getRecorded{}, time.Second)
	if err != nil {
		return nil
	}

	snapshot, _ := response.([]any)
	return snapshot
}

// fromConsumerController sends a message to the producer controller from the consumer
// controller stand-in.
func (x *producerControllerHarness) fromConsumerController(t *testing.T, message any) {
	t.Helper()
	require.NoError(t, Tell(x.ctx, x.consumerControllerStandIn, &deliveryForward{to: x.producerController, message: message}))
}

// fromProducer sends a message to the producer controller from the producer.
func (x *producerControllerHarness) fromProducer(t *testing.T, message any) {
	t.Helper()
	require.NoError(t, Tell(x.ctx, x.producer, &deliveryForward{to: x.producerController, message: message}))
}

// register performs the registration handshake and returns the session ID.
func (x *producerControllerHarness) register(t *testing.T) string {
	t.Helper()

	registerConsumer, err := commands.NewRegisterConsumer(uuid.NewString())
	require.NoError(t, err)
	x.fromConsumerController(t, registerConsumer)

	var sessionID string

	require.Eventually(t, func() bool {
		for _, message := range x.recordedOf(x.consumerControllerStandIn) {
			if ack, ok := message.(*commands.RegistrationAck); ok && ack.Nonce() == registerConsumer.Nonce() {
				sessionID = ack.SessionID()
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)

	return sessionID
}

// nonceOf extracts the nonce of the latest acknowledged registration.
func (x *producerControllerHarness) nonceOf(t *testing.T) string {
	t.Helper()

	var nonce string

	for _, message := range x.recordedOf(x.consumerControllerStandIn) {
		if ack, ok := message.(*commands.RegistrationAck); ok {
			nonce = ack.Nonce()
		}
	}

	require.NotEmpty(t, nonce)
	return nonce
}

// latestRequestNext waits for the latest credit granted to the producer.
func (x *producerControllerHarness) latestRequestNext(t *testing.T) *RequestNext {
	t.Helper()

	var latest *RequestNext

	require.Eventually(t, func() bool {
		for _, message := range x.recordedOf(x.producer) {
			if request, ok := message.(*RequestNext); ok {
				latest = request
			}
		}
		return latest != nil
	}, 3*time.Second, 10*time.Millisecond)

	return latest
}

// latestStored waits for the latest storage acknowledgement to the producer.
func (x *producerControllerHarness) latestStored(t *testing.T) *Stored {
	t.Helper()

	var latest *Stored

	require.Eventually(t, func() bool {
		for _, message := range x.recordedOf(x.producer) {
			if stored, ok := message.(*Stored); ok {
				latest = stored
			}
		}
		return latest != nil
	}, 3*time.Second, 10*time.Millisecond)

	return latest
}

// deliveryConfirmations returns the confirmation notices the producer received.
func (x *producerControllerHarness) deliveryConfirmations() []*DeliveryConfirmed {
	var notices []*DeliveryConfirmed

	for _, message := range x.recordedOf(x.producer) {
		if notice, ok := message.(*DeliveryConfirmed); ok {
			notices = append(notices, notice)
		}
	}

	return notices
}

// sequencedEmissions returns the sequenced messages the stand-in received.
func (x *producerControllerHarness) sequencedEmissions() []*commands.SequencedMessage {
	var emissions []*commands.SequencedMessage

	for _, message := range x.recordedOf(x.consumerControllerStandIn) {
		if sequenced, ok := message.(*commands.SequencedMessage); ok {
			emissions = append(emissions, sequenced)
		}
	}

	return emissions
}

// produceOne drives one full producer handshake for messageID, waiting for a
// fresh credit and the storage acknowledgement of exactly this message.
func (x *producerControllerHarness) produceOne(t *testing.T, messageID string) {
	t.Helper()
	x.produceOneWith(t, messageID, &testpb.Reply{Content: messageID})
}

// produceOneWith drives one full producer handshake handing over payload.
func (x *producerControllerHarness) produceOneWith(t *testing.T, messageID string, payload *testpb.Reply) {
	t.Helper()

	request := x.freshRequestNext(t)
	produced, err := NewProduced(request, messageID, payload)
	require.NoError(t, err)
	x.fromProducer(t, produced)

	var stored *Stored

	require.Eventually(t, func() bool {
		for _, message := range x.recordedOf(x.producer) {
			if candidate, ok := message.(*Stored); ok && candidate.MessageID() == messageID {
				stored = candidate
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)

	ack, err := NewStoredAck(stored)
	require.NoError(t, err)
	x.fromProducer(t, ack)
}

// produceAgainWith drives a full resubmission handshake for a messageID the
// producer already completed once, waiting for a Stored beyond the ones
// already recorded, and returns that fresh acknowledgement.
func (x *producerControllerHarness) produceAgainWith(t *testing.T, messageID string, payload *testpb.Reply) *Stored {
	t.Helper()

	before := 0

	for _, message := range x.recordedOf(x.producer) {
		if stored, ok := message.(*Stored); ok && stored.MessageID() == messageID {
			before++
		}
	}

	request := x.freshRequestNext(t)
	produced, err := NewProduced(request, messageID, payload)
	require.NoError(t, err)
	x.fromProducer(t, produced)

	var stored *Stored

	require.Eventually(t, func() bool {
		count := 0

		for _, message := range x.recordedOf(x.producer) {
			if candidate, ok := message.(*Stored); ok && candidate.MessageID() == messageID {
				count++
				stored = candidate
			}
		}

		return count > before
	}, 3*time.Second, 10*time.Millisecond)

	ack, err := NewStoredAck(stored)
	require.NoError(t, err)
	x.fromProducer(t, ack)
	return stored
}

// freshRequestNext waits for a credit the test has not answered yet and marks
// it used.
func (x *producerControllerHarness) freshRequestNext(t *testing.T) *RequestNext {
	t.Helper()

	var request *RequestNext

	require.Eventually(t, func() bool {
		for _, message := range x.recordedOf(x.producer) {
			if candidate, ok := message.(*RequestNext); ok && !x.usedTokens[candidate.Token()] {
				request = candidate
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)

	x.usedTokens[request.Token()] = true
	return request
}

// awaitFailure polls the event stream until a terminal failure arrives; the
// subscriber iterator drains a snapshot per call.
func awaitFailure(t *testing.T, subscriber eventstream.Subscriber) *ReliableDeliveryFailed {
	t.Helper()

	var failure *ReliableDeliveryFailed

	require.Eventually(t, func() bool {
		for message := range subscriber.Iterator() {
			if candidate, ok := message.Payload().(*ReliableDeliveryFailed); ok {
				failure = candidate
				return true
			}
		}
		return false
	}, 3*time.Second, 50*time.Millisecond)

	return failure
}

func TestProducerControllerVolatileFlow(t *testing.T) {
	harness := newProducerControllerHarness(t, nil)
	sessionID := harness.register(t)
	nonce := harness.nonceOf(t)

	// registration grants no demand: credit only flows from a Request
	request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
	require.NoError(t, err)
	harness.fromConsumerController(t, request)

	harness.produceOne(t, "m-1")

	require.Eventually(t, func() bool {
		emissions := harness.sequencedEmissions()
		return len(emissions) == 1 && emissions[0].Seq() == 1 && emissions[0].MessageID() == "m-1"
	}, 3*time.Second, 10*time.Millisecond)

	// the credit loop grants the next token after acceptance
	harness.produceOne(t, "m-2")

	require.Eventually(t, func() bool {
		return len(harness.sequencedEmissions()) == 2
	}, 3*time.Second, 10*time.Millisecond)

	// a timeout request resends only unconfirmed messages
	confirmOne, err := commands.NewRequest(sessionID, nonce, 1, 11, true)
	require.NoError(t, err)
	harness.fromConsumerController(t, confirmOne)

	require.Eventually(t, func() bool {
		for _, emission := range harness.sequencedEmissions()[2:] {
			if emission.Seq() == 2 {
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)

	for _, emission := range harness.sequencedEmissions()[2:] {
		assert.NotEqual(t, int64(1), emission.Seq())
	}
}

func TestProducerControllerDuplicateHandshake(t *testing.T) {
	harness := newProducerControllerHarness(t, nil)
	sessionID := harness.register(t)
	nonce := harness.nonceOf(t)

	request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
	require.NoError(t, err)
	harness.fromConsumerController(t, request)

	// an unanswered RequestNext is retried with the same token
	first := harness.latestRequestNext(t)

	require.Eventually(t, func() bool {
		count := 0
		for _, message := range harness.recordedOf(harness.producer) {
			if requestNext, ok := message.(*RequestNext); ok && requestNext.Token() == first.Token() {
				count++
			}
		}
		return count >= 2
	}, 3*time.Second, 10*time.Millisecond)

	// a duplicate Produced is idempotent while the store is pending
	produced, err := NewProduced(first, "m-1", &testpb.Reply{Content: "m-1"})
	require.NoError(t, err)
	harness.fromProducer(t, produced)
	harness.fromProducer(t, produced)

	stored := harness.latestStored(t)

	// an unacknowledged Stored is retried with the same sequence
	require.Eventually(t, func() bool {
		count := 0
		for _, message := range harness.recordedOf(harness.producer) {
			if resend, ok := message.(*Stored); ok && resend.Seq() == stored.Seq() {
				count++
			}
		}
		return count >= 2
	}, 3*time.Second, 10*time.Millisecond)

	ack, err := NewStoredAck(stored)
	require.NoError(t, err)
	harness.fromProducer(t, ack)

	require.Eventually(t, func() bool {
		return len(harness.sequencedEmissions()) == 1
	}, 3*time.Second, 10*time.Millisecond)

	// a late duplicate StoredAck of the accepted handshake stays idempotent
	harness.fromProducer(t, ack)
	pause.For(200 * time.Millisecond)
	assert.True(t, harness.producerController.IsRunning())
	assert.Len(t, harness.sequencedEmissions(), 1)
}

func TestProducerControllerDurableFlow(t *testing.T) {
	queue := &mockDurableQueue{}
	harness := newProducerControllerHarness(t, queue)
	sessionID := harness.register(t)
	nonce := harness.nonceOf(t)

	request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
	require.NoError(t, err)
	harness.fromConsumerController(t, request)

	harness.produceOne(t, "m-1")

	require.Eventually(t, func() bool {
		return len(harness.sequencedEmissions()) == 1
	}, 3*time.Second, 10*time.Millisecond)

	// store precedes accept for the same message
	_, operations, _ := queue.snapshot()
	require.Equal(t, []string{"store:m-1", "accept:m-1"}, operations)

	// the confirmation watermark reaches the queue
	confirm, err := commands.NewAck(sessionID, nonce, 1)
	require.NoError(t, err)
	harness.fromConsumerController(t, confirm)

	require.Eventually(t, func() bool {
		_, _, confirmed := queue.snapshot()
		return confirmed == 1
	}, 3*time.Second, 10*time.Millisecond)

	// a restart reloads authoritative state and acquires a new epoch
	require.NoError(t, harness.producerController.Restart(harness.ctx))

	require.Eventually(t, func() bool {
		loads, _, _ := queue.snapshot()
		return loads >= 2
	}, 3*time.Second, 10*time.Millisecond)
}

func TestProducerControllerDeliveryConfirmation(t *testing.T) {
	t.Run("With confirmations enabled reports each confirmed message once, in order", func(t *testing.T) {
		harness := newProducerControllerHarnessWith(t, nil, true)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		harness.produceOne(t, "m-1")
		harness.produceOne(t, "m-2")
		harness.produceOne(t, "m-3")

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == 3
		}, 3*time.Second, 10*time.Millisecond)

		// nothing is confirmed yet, so the producer has been told nothing
		require.Empty(t, harness.deliveryConfirmations())

		// one cumulative confirmation pops two messages
		confirmTwo, err := commands.NewAck(sessionID, nonce, 2)
		require.NoError(t, err)
		harness.fromConsumerController(t, confirmTwo)

		require.Eventually(t, func() bool {
			return len(harness.deliveryConfirmations()) == 2
		}, 3*time.Second, 10*time.Millisecond)

		notices := harness.deliveryConfirmations()
		assert.Equal(t, "m-1", notices[0].MessageID())
		assert.Equal(t, int64(1), notices[0].Seq())
		assert.Equal(t, "m-2", notices[1].MessageID())
		assert.Equal(t, int64(2), notices[1].Seq())
		assert.Equal(t, sessionID, notices[0].SessionID())

		// the notice authorizes only the producer and its own controller
		assert.True(t, notices[0].IsAuthorizedFor(harness.producer, harness.producerController))
		assert.False(t, notices[0].IsAuthorizedFor(harness.producer, harness.consumerControllerStandIn))

		// repeated and lower confirmations pop nothing, so nothing repeats
		harness.fromConsumerController(t, confirmTwo)

		confirmOne, err := commands.NewAck(sessionID, nonce, 1)
		require.NoError(t, err)
		harness.fromConsumerController(t, confirmOne)

		pause.For(300 * time.Millisecond)
		assert.Len(t, harness.deliveryConfirmations(), 2)

		// the last message reports only once its own confirmation arrives
		confirmThree, err := commands.NewAck(sessionID, nonce, 3)
		require.NoError(t, err)
		harness.fromConsumerController(t, confirmThree)

		require.Eventually(t, func() bool {
			return len(harness.deliveryConfirmations()) == 3
		}, 3*time.Second, 10*time.Millisecond)

		assert.Equal(t, "m-3", harness.deliveryConfirmations()[2].MessageID())
	})

	t.Run("With confirmations disabled reports nothing", func(t *testing.T) {
		harness := newProducerControllerHarness(t, nil)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		harness.produceOne(t, "m-1")

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == 1
		}, 3*time.Second, 10*time.Millisecond)

		confirm, err := commands.NewAck(sessionID, nonce, 1)
		require.NoError(t, err)
		harness.fromConsumerController(t, confirm)

		// the confirmation is processed: the message leaves the unconfirmed
		// buffer, proven by a timeout request resending nothing
		resend, err := commands.NewRequest(sessionID, nonce, 1, 11, true)
		require.NoError(t, err)
		harness.fromConsumerController(t, resend)

		pause.For(300 * time.Millisecond)
		assert.Len(t, harness.sequencedEmissions(), 1)
		assert.Empty(t, harness.deliveryConfirmations())
	})

	t.Run("With a durable restart reports the redelivered message again", func(t *testing.T) {
		queue := &mockDurableQueue{}
		harness := newProducerControllerHarnessWith(t, queue, true)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		harness.produceOne(t, "m-1")

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == 1
		}, 3*time.Second, 10*time.Millisecond)

		// the controller restarts before any confirmation arrives, so the
		// stored message reloads and stays unconfirmed
		require.NoError(t, harness.producerController.Restart(harness.ctx))

		require.Eventually(t, func() bool {
			loads, _, _ := queue.snapshot()
			return loads >= 2
		}, 3*time.Second, 10*time.Millisecond)

		assert.Empty(t, harness.deliveryConfirmations())

		// the new incarnation reports the reloaded message when it is confirmed
		newSessionID := harness.register(t)
		newNonce := harness.nonceOf(t)

		confirm, err := commands.NewAck(newSessionID, newNonce, 1)
		require.NoError(t, err)
		harness.fromConsumerController(t, confirm)

		require.Eventually(t, func() bool {
			return len(harness.deliveryConfirmations()) == 1
		}, 3*time.Second, 10*time.Millisecond)

		notice := harness.deliveryConfirmations()[0]
		assert.Equal(t, "m-1", notice.MessageID())
		assert.Equal(t, newSessionID, notice.SessionID())
	})

	t.Run("With chunking reports one notice per business message at the last chunk seq", func(t *testing.T) {
		harness := newProducerControllerHarnessFor(t, nil, true, MinChunkSize)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		payload := &testpb.Reply{Content: strings.Repeat("x", 3*MinChunkSize)}
		frame, err := harness.system.getRemoting().Serializer(payload).Serialize(payload)
		require.NoError(t, err)
		chunks := (len(frame) + MinChunkSize - 1) / MinChunkSize
		require.GreaterOrEqual(t, chunks, 3)

		request, err := commands.NewRequest(sessionID, nonce, 0, int64(chunks)+5, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		harness.produceOneWith(t, "m-chunked", payload)
		harness.produceOne(t, "m-whole")

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == chunks+1
		}, 3*time.Second, 10*time.Millisecond)

		require.Empty(t, harness.deliveryConfirmations())

		// confirming the whole chunked message pops every chunk entry, but
		// the producer sees one business-level notice at the last chunk seq
		confirmChunked, err := commands.NewAck(sessionID, nonce, int64(chunks))
		require.NoError(t, err)
		harness.fromConsumerController(t, confirmChunked)

		require.Eventually(t, func() bool {
			return len(harness.deliveryConfirmations()) == 1
		}, 3*time.Second, 10*time.Millisecond)

		pause.For(200 * time.Millisecond)
		assert.Len(t, harness.deliveryConfirmations(), 1)

		notice := harness.deliveryConfirmations()[0]
		assert.Equal(t, "m-chunked", notice.MessageID())
		assert.Equal(t, int64(chunks), notice.Seq())
		assert.Equal(t, sessionID, notice.SessionID())

		confirmWhole, err := commands.NewAck(sessionID, nonce, int64(chunks)+1)
		require.NoError(t, err)
		harness.fromConsumerController(t, confirmWhole)

		require.Eventually(t, func() bool {
			return len(harness.deliveryConfirmations()) == 2
		}, 3*time.Second, 10*time.Millisecond)

		assert.Equal(t, "m-whole", harness.deliveryConfirmations()[1].MessageID())
		assert.Equal(t, int64(chunks)+1, harness.deliveryConfirmations()[1].Seq())
	})
}

func TestProducerControllerChunkedFlow(t *testing.T) {
	t.Run("With a large payload split into flagged chunks under one Stored", func(t *testing.T) {
		harness := newProducerControllerHarnessChunked(t, MinChunkSize)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		payload := &testpb.Reply{Content: strings.Repeat("x", 3*MinChunkSize)}
		frame, err := harness.system.getRemoting().Serializer(payload).Serialize(payload)
		require.NoError(t, err)
		expected := (len(frame) + MinChunkSize - 1) / MinChunkSize
		require.GreaterOrEqual(t, expected, 3)

		request, err := commands.NewRequest(sessionID, nonce, 0, int64(expected)+5, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		harness.produceOneWith(t, "m-big", payload)

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == expected
		}, 3*time.Second, 10*time.Millisecond)

		// each chunk consumes one sequence, carries the business message ID,
		// and is flagged by position; the parts concatenate to the frame
		var assembled []byte

		for index, emission := range harness.sequencedEmissions() {
			assert.True(t, emission.Chunked())
			assert.Equal(t, "m-big", emission.MessageID())
			assert.Equal(t, int64(index+1), emission.Seq())
			assert.Equal(t, index == 0, emission.FirstChunk())
			assert.Equal(t, index == expected-1, emission.LastChunk())
			assembled = emission.AppendPayload(assembled)
		}

		assert.Equal(t, frame, assembled)

		// the producer-visible handshake stays one Stored per business
		// message, carrying the last chunk's sequence
		storedCount := 0

		for _, message := range harness.recordedOf(harness.producer) {
			if stored, ok := message.(*Stored); ok && stored.MessageID() == "m-big" {
				storedCount++
				assert.Equal(t, int64(expected), stored.Seq())
			}
		}

		assert.Equal(t, 1, storedCount)
	})

	t.Run("With frames at an exact chunk multiple and one byte over", func(t *testing.T) {
		harness := newProducerControllerHarnessChunked(t, MinChunkSize)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		// pin the frame length to exactly two chunks by measuring the
		// serializer overhead for a probe of the target content size
		probe := &testpb.Reply{Content: strings.Repeat("x", 2*MinChunkSize)}
		probeFrame, err := harness.system.getRemoting().Serializer(probe).Serialize(probe)
		require.NoError(t, err)
		overhead := len(probeFrame) - 2*MinChunkSize

		exact := &testpb.Reply{Content: strings.Repeat("x", 2*MinChunkSize-overhead)}
		exactFrame, err := harness.system.getRemoting().Serializer(exact).Serialize(exact)
		require.NoError(t, err)
		require.Len(t, exactFrame, 2*MinChunkSize)

		harness.produceOneWith(t, "m-exact", exact)

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == 2
		}, 3*time.Second, 10*time.Millisecond)

		for _, emission := range harness.sequencedEmissions() {
			assert.Equal(t, MinChunkSize, emission.PayloadSize())
		}

		// one byte more of content splits into a third, one-byte chunk
		over := &testpb.Reply{Content: strings.Repeat("x", 2*MinChunkSize-overhead+1)}
		harness.produceOneWith(t, "m-over", over)

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == 5
		}, 3*time.Second, 10*time.Millisecond)

		tail := harness.sequencedEmissions()[2:]
		assert.Equal(t, MinChunkSize, tail[0].PayloadSize())
		assert.Equal(t, MinChunkSize, tail[1].PayloadSize())
		assert.Equal(t, 1, tail[2].PayloadSize())
		assert.True(t, tail[2].LastChunk())
	})

	t.Run("With a payload at or below the chunk size stays whole", func(t *testing.T) {
		harness := newProducerControllerHarnessChunked(t, MaxChunkSize)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		harness.produceOne(t, "m-small")

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == 1
		}, 3*time.Second, 10*time.Millisecond)

		emission := harness.sequencedEmissions()[0]
		assert.False(t, emission.Chunked())
		assert.Equal(t, int64(1), emission.Seq())
	})

	t.Run("With more chunks than the consumer window is terminal", func(t *testing.T) {
		harness := newProducerControllerHarnessChunked(t, MinChunkSize)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		// the window span is 2, but the payload needs at least 3 chunks
		request, err := commands.NewRequest(sessionID, nonce, 0, 2, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		credit := harness.freshRequestNext(t)
		produced, err := NewProduced(credit, "m-oversized", &testpb.Reply{Content: strings.Repeat("x", 3*MinChunkSize)})
		require.NoError(t, err)
		harness.fromProducer(t, produced)

		failure := awaitFailure(t, subscriber)
		assert.Equal(t, ReliableDeliveryStageProtocol, failure.Stage())
		assert.Equal(t, ReliableControllerRoleProducer, failure.ControllerRole())
		assert.ErrorContains(t, failure.Err(), "chunks but the consumer window")
	})

	t.Run("With a mid-message demand pause resumed by a timeout request", func(t *testing.T) {
		harness := newProducerControllerHarnessChunked(t, MinChunkSize)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		payload := &testpb.Reply{Content: strings.Repeat("x", 2*MinChunkSize)}
		frame, err := harness.system.getRemoting().Serializer(payload).Serialize(payload)
		require.NoError(t, err)
		chunks := (len(frame) + MinChunkSize - 1) / MinChunkSize
		require.Equal(t, 3, chunks)

		// demand covers one whole message plus only part of the chunked one:
		// the window span (3) admits the chunk count, but demandUpTo stops
		// emission after the second chunk
		request, err := commands.NewRequest(sessionID, nonce, 0, 3, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		harness.produceOne(t, "m-first")
		harness.produceOneWith(t, "m-chunked", payload)

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == 3
		}, 3*time.Second, 10*time.Millisecond)

		pause.For(200 * time.Millisecond)

		for _, emission := range harness.sequencedEmissions() {
			assert.LessOrEqual(t, emission.Seq(), int64(3))
		}

		// the timeout request grants the rest and resends the unconfirmed
		// suffix, completing the paused message with its flags intact
		resume, err := commands.NewRequest(sessionID, nonce, 1, 6, true)
		require.NoError(t, err)
		harness.fromConsumerController(t, resume)

		require.Eventually(t, func() bool {
			for _, emission := range harness.sequencedEmissions() {
				if emission.Seq() == 4 {
					return emission.Chunked() && emission.LastChunk()
				}
			}
			return false
		}, 3*time.Second, 10*time.Millisecond)
	})
}

func TestProducerControllerDurableChunkedFlow(t *testing.T) {
	t.Run("With StoreChunked then Accept under one business MessageID", func(t *testing.T) {
		queue := &mockDurableQueue{}
		harness := newProducerControllerHarnessFor(t, queue, false, MinChunkSize)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		payload := &testpb.Reply{Content: strings.Repeat("x", 3*MinChunkSize)}
		frame, err := harness.system.getRemoting().Serializer(payload).Serialize(payload)
		require.NoError(t, err)
		chunks := (len(frame) + MinChunkSize - 1) / MinChunkSize
		require.GreaterOrEqual(t, chunks, 3)

		request, err := commands.NewRequest(sessionID, nonce, 0, int64(chunks)+5, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		harness.produceOneWith(t, "m-chunked", payload)

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == chunks
		}, 3*time.Second, 10*time.Millisecond)

		_, operations, _ := queue.snapshot()
		require.Equal(t, []string{"storechunked:m-chunked", "accept:m-chunked"}, operations)

		for index, emission := range harness.sequencedEmissions() {
			assert.Equal(t, "m-chunked", emission.MessageID())
			assert.Equal(t, int64(index+1), emission.Seq())
			assert.True(t, emission.Chunked())
			assert.Equal(t, index == 0, emission.FirstChunk())
			assert.Equal(t, index == chunks-1, emission.LastChunk())
		}

		storedCount := 0

		for _, message := range harness.recordedOf(harness.producer) {
			if stored, ok := message.(*Stored); ok && stored.MessageID() == "m-chunked" {
				storedCount++
				assert.Equal(t, int64(chunks), stored.Seq())
			}
		}

		assert.Equal(t, 1, storedCount)
	})

	t.Run("With a restart while unconfirmed chunks reload and resend", func(t *testing.T) {
		queue := &mockDurableQueue{}
		harness := newProducerControllerHarnessFor(t, queue, false, MinChunkSize)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		payload := &testpb.Reply{Content: strings.Repeat("x", 3*MinChunkSize)}
		frame, err := harness.system.getRemoting().Serializer(payload).Serialize(payload)
		require.NoError(t, err)
		chunks := (len(frame) + MinChunkSize - 1) / MinChunkSize
		require.GreaterOrEqual(t, chunks, 3)

		request, err := commands.NewRequest(sessionID, nonce, 0, int64(chunks)+5, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		harness.produceOneWith(t, "m-reload", payload)

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == chunks
		}, 3*time.Second, 10*time.Millisecond)

		firstEmissions := append([]*commands.SequencedMessage(nil), harness.sequencedEmissions()...)

		// restart before confirmation: Load rehydrates chunk marks and a
		// timeout request resends the stored batch without re-chunking
		require.NoError(t, harness.producerController.Restart(harness.ctx))

		require.Eventually(t, func() bool {
			loads, _, _ := queue.snapshot()
			return loads >= 2
		}, 3*time.Second, 10*time.Millisecond)

		newSessionID := harness.register(t)
		newNonce := harness.nonceOf(t)

		resend, err := commands.NewRequest(newSessionID, newNonce, 0, int64(chunks)+5, true)
		require.NoError(t, err)
		harness.fromConsumerController(t, resend)

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) >= 2*chunks
		}, 3*time.Second, 10*time.Millisecond)

		reloaded := harness.sequencedEmissions()[chunks : 2*chunks]

		for index, emission := range reloaded {
			assert.Equal(t, firstEmissions[index].MessageID(), emission.MessageID())
			assert.Equal(t, firstEmissions[index].Seq(), emission.Seq())
			assert.Equal(t, firstEmissions[index].Payload(), emission.Payload())
			assert.Equal(t, firstEmissions[index].FirstChunk(), emission.FirstChunk())
			assert.Equal(t, firstEmissions[index].LastChunk(), emission.LastChunk())
		}
	})

	t.Run("With resubmit under a tight window reuses the stored batch", func(t *testing.T) {
		queue := &mockDurableQueue{}
		harness := newProducerControllerHarnessFor(t, queue, false, MinChunkSize)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		payload := &testpb.Reply{Content: strings.Repeat("x", 3*MinChunkSize)}
		frame, err := harness.system.getRemoting().Serializer(payload).Serialize(payload)
		require.NoError(t, err)
		chunks := (len(frame) + MinChunkSize - 1) / MinChunkSize
		require.GreaterOrEqual(t, chunks, 3)

		request, err := commands.NewRequest(sessionID, nonce, 0, int64(chunks)+5, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		harness.produceOneWith(t, "m-tight", payload)

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == chunks
		}, 3*time.Second, 10*time.Millisecond)

		require.NoError(t, harness.producerController.Restart(harness.ctx))

		require.Eventually(t, func() bool {
			loads, _, _ := queue.snapshot()
			return loads >= 2
		}, 3*time.Second, 10*time.Millisecond)

		newSessionID := harness.register(t)
		newNonce := harness.nonceOf(t)

		// demand opens one credit (upTo = currentSeq+1) while the window span
		// equals chunks+1; a much larger re-encode would fail the first-store
		// window check, so surviving proves the already-stored short-circuit
		tight, err := commands.NewRequest(newSessionID, newNonce, 0, int64(chunks)+1, true)
		require.NoError(t, err)
		harness.fromConsumerController(t, tight)

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) >= 2*chunks
		}, 3*time.Second, 10*time.Millisecond)

		oversized := &testpb.Reply{Content: strings.Repeat("y", 20*MinChunkSize)}
		overFrame, err := harness.system.getRemoting().Serializer(oversized).Serialize(oversized)
		require.NoError(t, err)
		require.Greater(t, (len(overFrame)+MinChunkSize-1)/MinChunkSize, chunks+1)

		harness.produceOneWith(t, "m-tight", oversized)

		require.Eventually(t, func() bool {
			_, operations, _ := queue.snapshot()

			for _, operation := range operations {
				if operation == "accept:m-tight" {
					return true
				}
			}

			return false
		}, 3*time.Second, 10*time.Millisecond)

		assert.True(t, harness.producerController.IsRunning())

		queue.mu.Lock()
		currentSeq := queue.currentSeq
		queue.mu.Unlock()
		assert.EqualValues(t, chunks, currentSeq)
	})

	t.Run("With resubmission after StoreChunked reuses the first-write batch", func(t *testing.T) {
		_, system := newCompanionTestSystem(t)
		payload := &testpb.Reply{Content: strings.Repeat("x", 3*MinChunkSize)}
		frame, err := system.getRemoting().Serializer(payload).Serialize(payload)
		require.NoError(t, err)
		chunks := (len(frame) + MinChunkSize - 1) / MinChunkSize
		require.GreaterOrEqual(t, chunks, 3)

		stored := make([]UnconfirmedMessage, 0, chunks)

		for index := 0; index < chunks; index++ {
			start := index * MinChunkSize
			end := min(start+MinChunkSize, len(frame))
			part, err := NewReliablePayload(frame[start:end])
			require.NoError(t, err)

			entry, err := newChunkUnconfirmedMessage(durableChunkMessageID("m-retry", index+1, chunks), int64(index+1), part, index == 0, index == chunks-1)
			require.NoError(t, err)
			stored = append(stored, entry)
		}

		queue := &mockDurableQueue{currentSeq: int64(chunks), stored: stored}
		harness := newProducerControllerHarnessFor(t, queue, false, MinChunkSize)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		request, err := commands.NewRequest(sessionID, nonce, 0, int64(chunks)+5, true)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == chunks
		}, 3*time.Second, 10*time.Millisecond)

		// different bytes, same business MessageID: StoreChunked returns the
		// original batch and Accept completes the interrupted handshake
		harness.produceOneWith(t, "m-retry", &testpb.Reply{Content: strings.Repeat("y", 3*MinChunkSize)})

		require.Eventually(t, func() bool {
			_, operations, _ := queue.snapshot()

			for _, operation := range operations {
				if operation == "accept:m-retry" {
					return true
				}
			}

			return false
		}, 3*time.Second, 10*time.Millisecond)

		queue.mu.Lock()
		currentSeq := queue.currentSeq
		queue.mu.Unlock()
		assert.EqualValues(t, chunks, currentSeq)

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) >= 2*chunks
		}, 3*time.Second, 10*time.Millisecond)

		reemitted := harness.sequencedEmissions()[chunks : 2*chunks]

		for index, emission := range reemitted {
			assert.Equal(t, "m-retry", emission.MessageID())
			assert.Equal(t, int64(index+1), emission.Seq())
			assert.Equal(t, stored[index].Payload().Bytes(), emission.Payload())
		}
	})

	t.Run("With delivery confirmation reports one notice at the last chunk seq", func(t *testing.T) {
		queue := &mockDurableQueue{}
		harness := newProducerControllerHarnessFor(t, queue, true, MinChunkSize)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		payload := &testpb.Reply{Content: strings.Repeat("x", 3*MinChunkSize)}
		frame, err := harness.system.getRemoting().Serializer(payload).Serialize(payload)
		require.NoError(t, err)
		chunks := (len(frame) + MinChunkSize - 1) / MinChunkSize
		require.GreaterOrEqual(t, chunks, 3)

		request, err := commands.NewRequest(sessionID, nonce, 0, int64(chunks)+5, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		harness.produceOneWith(t, "m-confirm", payload)

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == chunks
		}, 3*time.Second, 10*time.Millisecond)

		confirm, err := commands.NewAck(sessionID, nonce, int64(chunks))
		require.NoError(t, err)
		harness.fromConsumerController(t, confirm)

		require.Eventually(t, func() bool {
			return len(harness.deliveryConfirmations()) == 1
		}, 3*time.Second, 10*time.Millisecond)

		pause.For(200 * time.Millisecond)
		assert.Len(t, harness.deliveryConfirmations(), 1)

		notice := harness.deliveryConfirmations()[0]
		assert.Equal(t, "m-confirm", notice.MessageID())
		assert.Equal(t, int64(chunks), notice.Seq())
	})

	t.Run("With a whole-stored resubmission re-encoded above the chunk size reuses the stored message", func(t *testing.T) {
		queue := &mockDurableQueue{}
		harness := newProducerControllerHarnessFor(t, queue, false, MinChunkSize)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		request, err := commands.NewRequest(sessionID, nonce, 0, 50, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		harness.produceOneWith(t, "m-mixed", &testpb.Reply{Content: "small"})

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == 1
		}, 3*time.Second, 10*time.Millisecond)

		// same MessageID re-encoded above the chunk threshold: the stored
		// whole message stays authoritative, so no chunk batch is appended
		// and the emission repeats the original shape
		stored := harness.produceAgainWith(t, "m-mixed", &testpb.Reply{Content: strings.Repeat("y", 3*MinChunkSize)})
		assert.EqualValues(t, 1, stored.Seq())

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == 2
		}, 3*time.Second, 10*time.Millisecond)

		for _, emission := range harness.sequencedEmissions() {
			assert.Equal(t, "m-mixed", emission.MessageID())
			assert.EqualValues(t, 1, emission.Seq())
			assert.False(t, emission.Chunked())
		}

		queue.mu.Lock()
		currentSeq := queue.currentSeq
		queue.mu.Unlock()
		assert.EqualValues(t, 1, currentSeq)
		assert.True(t, harness.producerController.IsRunning())
	})

	t.Run("With a chunk-stored resubmission re-encoded below the chunk size reuses the stored batch", func(t *testing.T) {
		queue := &mockDurableQueue{}
		harness := newProducerControllerHarnessFor(t, queue, false, MinChunkSize)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		payload := &testpb.Reply{Content: strings.Repeat("x", 3*MinChunkSize)}
		frame, err := harness.system.getRemoting().Serializer(payload).Serialize(payload)
		require.NoError(t, err)
		chunks := (len(frame) + MinChunkSize - 1) / MinChunkSize
		require.GreaterOrEqual(t, chunks, 3)

		request, err := commands.NewRequest(sessionID, nonce, 0, int64(chunks)+5, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		harness.produceOneWith(t, "m-shrunk", payload)

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == chunks
		}, 3*time.Second, 10*time.Millisecond)

		// same MessageID re-encoded below the chunk threshold: the stored
		// batch stays authoritative and replays through StoreChunked, so the
		// queue never holds a second whole-message encoding
		stored := harness.produceAgainWith(t, "m-shrunk", &testpb.Reply{Content: "tiny"})
		assert.EqualValues(t, chunks, stored.Seq())

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) >= 2*chunks
		}, 3*time.Second, 10*time.Millisecond)

		reemitted := harness.sequencedEmissions()[chunks : 2*chunks]

		for index, emission := range reemitted {
			assert.Equal(t, "m-shrunk", emission.MessageID())
			assert.EqualValues(t, index+1, emission.Seq())
			assert.True(t, emission.Chunked())
		}

		_, operations, _ := queue.snapshot()

		for _, operation := range operations {
			assert.NotEqual(t, "store:m-shrunk", operation)
		}

		queue.mu.Lock()
		currentSeq := queue.currentSeq
		queue.mu.Unlock()
		assert.EqualValues(t, chunks, currentSeq)
		assert.True(t, harness.producerController.IsRunning())
	})

	t.Run("With a confirmed retained business MessageID resubmission completes benignly", func(t *testing.T) {
		queue := &mockDurableQueue{retainConfirmed: true}
		harness := newProducerControllerHarnessFor(t, queue, false, MinChunkSize)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		payload := &testpb.Reply{Content: strings.Repeat("x", 3*MinChunkSize)}
		frame, err := harness.system.getRemoting().Serializer(payload).Serialize(payload)
		require.NoError(t, err)
		chunks := (len(frame) + MinChunkSize - 1) / MinChunkSize
		require.GreaterOrEqual(t, chunks, 3)

		request, err := commands.NewRequest(sessionID, nonce, 0, int64(chunks)+5, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		harness.produceOneWith(t, "m-keep", payload)

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == chunks
		}, 3*time.Second, 10*time.Millisecond)

		confirm, err := commands.NewAck(sessionID, nonce, int64(chunks))
		require.NoError(t, err)
		harness.fromConsumerController(t, confirm)

		require.Eventually(t, func() bool {
			_, operations, _ := queue.snapshot()
			return slices.Contains(operations, "confirm")
		}, 3*time.Second, 10*time.Millisecond)

		// the confirmed entries left the unconfirmed buffer but the queue
		// still owns the MessageID: StoreChunked answers with the original
		// batch and the handshake completes without appending or terminating
		stored := harness.produceAgainWith(t, "m-keep", payload)
		assert.EqualValues(t, chunks, stored.Seq())

		require.Eventually(t, func() bool {
			_, operations, _ := queue.snapshot()

			accepts := 0
			for _, operation := range operations {
				if operation == "accept:m-keep" {
					accepts++
				}
			}

			return accepts == 2
		}, 3*time.Second, 10*time.Millisecond)

		queue.mu.Lock()
		currentSeq := queue.currentSeq
		queue.mu.Unlock()
		assert.EqualValues(t, chunks, currentSeq)
		assert.True(t, harness.producerController.IsRunning())
	})
}

func TestProducerControllerFirstWriteWins(t *testing.T) {
	payload, err := NewReliablePayload([]byte("first-write"))
	require.NoError(t, err)
	original, err := NewUnconfirmedMessage("m-1", 1, payload)
	require.NoError(t, err)

	queue := &mockDurableQueue{currentSeq: 1, stored: []UnconfirmedMessage{original}}
	harness := newProducerControllerHarness(t, queue)
	sessionID := harness.register(t)
	nonce := harness.nonceOf(t)

	// the loaded unconfirmed message is redelivered on a timeout request
	request, err := commands.NewRequest(sessionID, nonce, 0, 10, true)
	require.NoError(t, err)
	harness.fromConsumerController(t, request)

	require.Eventually(t, func() bool {
		emissions := harness.sequencedEmissions()
		return len(emissions) >= 1 && emissions[0].Seq() == 1
	}, 3*time.Second, 10*time.Millisecond)

	// the producer resubmits the same MessageID: Store returns the original
	// sequence and authoritative first-write payload, appending nothing
	harness.produceOne(t, "m-1")

	stored := harness.latestStored(t)
	assert.Equal(t, int64(1), stored.Seq())

	require.Eventually(t, func() bool {
		for _, emission := range harness.sequencedEmissions() {
			if string(emission.Payload()) == "first-write" {
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)

	// the accept is asynchronous: wait for it rather than racing the lane
	require.Eventually(t, func() bool {
		_, operations, _ := queue.snapshot()
		return len(operations) == 1 && operations[0] == "accept:m-1"
	}, 3*time.Second, 10*time.Millisecond)
}

func TestProducerControllerTerminalFailures(t *testing.T) {
	t.Run("With queue fencing", func(t *testing.T) {
		queue := &mockDurableQueue{storeErr: gerrors.ErrQueueFenced}
		harness := newProducerControllerHarness(t, queue)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		// the store fails, so only the credit half of the handshake runs
		credit := harness.latestRequestNext(t)
		produced, err := NewProduced(credit, "m-1", &testpb.Reply{Content: "m-1"})
		require.NoError(t, err)
		harness.fromProducer(t, produced)

		failure := awaitFailure(t, subscriber)
		assert.Equal(t, "producer", failure.EndpointName())
		assert.Equal(t, ReliableControllerRoleProducer, failure.ControllerRole())
		assert.Equal(t, ReliableDeliveryStageStore, failure.Stage())
		assert.ErrorIs(t, failure.Err(), gerrors.ErrQueueFenced)

		require.Eventually(t, func() bool {
			return !harness.producerController.IsRunning()
		}, 3*time.Second, 10*time.Millisecond)
	})

	t.Run("With illegal demand range", func(t *testing.T) {
		harness := newProducerControllerHarness(t, nil)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		illegal, err := commands.NewRequest(sessionID, nonce, 0, MaxFlowControlWindow+1, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, illegal)

		failure := awaitFailure(t, subscriber)
		assert.Equal(t, ReliableDeliveryStageProtocol, failure.Stage())

		require.Eventually(t, func() bool {
			return !harness.producerController.IsRunning()
		}, 3*time.Second, 10*time.Millisecond)
	})

	t.Run("With unregistered payload type", func(t *testing.T) {
		harness := newProducerControllerHarness(t, nil)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		// a payload type without a registered serializer cannot be encoded:
		// encoding is deterministic, so the controller must fail terminally
		// instead of panicking or spinning the retry loop
		credit := harness.latestRequestNext(t)
		produced, err := NewProduced(credit, "m-1", struct{ ID string }{ID: "m-1"})
		require.NoError(t, err)
		harness.fromProducer(t, produced)

		failure := awaitFailure(t, subscriber)
		assert.Equal(t, ReliableControllerRoleProducer, failure.ControllerRole())
		assert.Equal(t, ReliableDeliveryStageProtocol, failure.Stage())
		assert.ErrorContains(t, failure.Err(), "no serializer is registered")

		require.Eventually(t, func() bool {
			return !harness.producerController.IsRunning()
		}, 3*time.Second, 10*time.Millisecond)
	})
}

func TestProducerControllerRegistrationFencing(t *testing.T) {
	harness := newProducerControllerHarness(t, nil)

	// a registration from anything but the consumer's current controller is dropped
	registerConsumer, err := commands.NewRegisterConsumer(uuid.NewString())
	require.NoError(t, err)
	harness.fromProducer(t, registerConsumer)

	pause.For(200 * time.Millisecond)

	for _, message := range harness.recordedOf(harness.producer) {
		_, isAck := message.(*commands.RegistrationAck)
		assert.False(t, isAck)
	}

	// the verified controller still registers and traffic under a stale nonce is dropped
	sessionID := harness.register(t)
	stale, err := commands.NewRequest(sessionID, uuid.NewString(), 0, 10, false)
	require.NoError(t, err)
	harness.fromConsumerController(t, stale)

	staleAck, err := commands.NewAck(sessionID, uuid.NewString(), 0)
	require.NoError(t, err)
	harness.fromConsumerController(t, staleAck)

	pause.For(200 * time.Millisecond)
	assert.Empty(t, harness.recordedOf(harness.producer))
	assert.True(t, harness.producerController.IsRunning())
}

func TestProducerControllerProducerTerminated(t *testing.T) {
	harness := newProducerControllerHarness(t, nil)
	harness.register(t)

	require.NoError(t, harness.producer.Shutdown(harness.ctx))

	require.Eventually(t, func() bool {
		return !harness.producerController.IsRunning()
	}, 3*time.Second, 10*time.Millisecond)
}

func TestProducerControllerConsumerControllerTerminated(t *testing.T) {
	harness := newProducerControllerHarness(t, nil)
	sessionID := harness.register(t)
	nonce := harness.nonceOf(t)

	request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
	require.NoError(t, err)
	harness.fromConsumerController(t, request)

	credit := harness.latestRequestNext(t)
	produced, err := NewProduced(credit, "m-1", &testpb.Reply{Content: "m-1"})
	require.NoError(t, err)
	harness.fromProducer(t, produced)
	stored := harness.latestStored(t)

	// the consumer controller dies while Stored awaits acknowledgement: emission
	// is deferred because registration is cleared, and a replacement recovers
	require.NoError(t, harness.consumerControllerStandIn.Shutdown(harness.ctx))

	require.Eventually(t, func() bool {
		return !harness.consumerControllerStandIn.IsRunning()
	}, 3*time.Second, 10*time.Millisecond)

	ack, err := NewStoredAck(stored)
	require.NoError(t, err)
	harness.fromProducer(t, ack)
	pause.For(200 * time.Millisecond)
	assert.Empty(t, harness.sequencedEmissions())
	assert.True(t, harness.producerController.IsRunning())
}

func TestProducerControllerIllegalAck(t *testing.T) {
	harness := newProducerControllerHarness(t, nil)
	sessionID := harness.register(t)
	nonce := harness.nonceOf(t)

	subscriber, err := harness.system.Subscribe()
	require.NoError(t, err)

	illegal, err := commands.NewAck(sessionID, nonce, 99)
	require.NoError(t, err)
	harness.fromConsumerController(t, illegal)

	failure := awaitFailure(t, subscriber)
	assert.Equal(t, ReliableDeliveryStageProtocol, failure.Stage())

	require.Eventually(t, func() bool {
		return !harness.producerController.IsRunning()
	}, 3*time.Second, 10*time.Millisecond)
}

func TestProducerControllerProtocolDrops(t *testing.T) {
	harness := newProducerControllerHarness(t, nil)
	sessionID := harness.register(t)
	nonce := harness.nonceOf(t)

	request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
	require.NoError(t, err)
	harness.fromConsumerController(t, request)
	credit := harness.latestRequestNext(t)

	t.Run("With Produced from unexpected sender", func(t *testing.T) {
		produced, err := NewProduced(credit, "m-unexpected", &testpb.Reply{Content: "x"})
		require.NoError(t, err)
		harness.fromConsumerController(t, produced)
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.producerController.IsRunning())
	})

	t.Run("With Produced from stale session", func(t *testing.T) {
		harness.fromProducer(t, &Produced{
			sessionID: "stale-session",
			token:     credit.Token(),
			messageID: "m-stale",
			payload:   &testpb.Reply{Content: "x"},
		})
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.producerController.IsRunning())
	})

	t.Run("With StoredAck from unexpected sender", func(t *testing.T) {
		stored, err := newStoredFromState(sessionID, credit.Token(), "m-1", 1, harness.producer, harness.producerController)
		require.NoError(t, err)
		ack, err := NewStoredAck(stored)
		require.NoError(t, err)
		harness.fromConsumerController(t, ack)
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.producerController.IsRunning())
	})

	t.Run("With StoredAck from stale session", func(t *testing.T) {
		harness.fromProducer(t, &StoredAck{
			sessionID: "stale-session",
			token:     credit.Token(),
			messageID: "m-1",
		})
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.producerController.IsRunning())
	})

	t.Run("With unhandled message", func(t *testing.T) {
		require.NoError(t, Tell(harness.ctx, harness.producerController, &testpb.Reply{Content: "noise"}))
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.producerController.IsRunning())
	})

	t.Run("With stale queue op result", func(t *testing.T) {
		require.NoError(t, Tell(harness.ctx, harness.producerController, &queueOpResult{
			sessionID:   "stale-session",
			operationID: 99,
			kind:        queueOpStore,
		}))
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.producerController.IsRunning())
	})
}

func TestProducerControllerContractViolations(t *testing.T) {
	t.Run("With Produced token mismatch", func(t *testing.T) {
		harness := newProducerControllerHarness(t, nil)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)
		_ = harness.latestRequestNext(t)

		harness.fromProducer(t, &Produced{
			sessionID: sessionID,
			token:     uuid.NewString(),
			messageID: "m-1",
			payload:   &testpb.Reply{Content: "m-1"},
		})

		failure := awaitFailure(t, subscriber)
		assert.Equal(t, ReliableDeliveryStageProtocol, failure.Stage())
		assert.ErrorContains(t, failure.Err(), "token mismatch")
	})

	t.Run("With unexpected Produced phase", func(t *testing.T) {
		harness := newProducerControllerHarness(t, nil)
		sessionID := harness.register(t)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		harness.fromProducer(t, &Produced{
			sessionID: sessionID,
			token:     uuid.NewString(),
			messageID: "m-1",
			payload:   &testpb.Reply{Content: "m-1"},
		})

		failure := awaitFailure(t, subscriber)
		assert.Equal(t, ReliableDeliveryStageProtocol, failure.Stage())
		assert.ErrorContains(t, failure.Err(), "unexpected Produced")
	})

	t.Run("With unexpected StoredAck", func(t *testing.T) {
		harness := newProducerControllerHarness(t, nil)
		sessionID := harness.register(t)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		harness.fromProducer(t, &StoredAck{
			sessionID: sessionID,
			token:     uuid.NewString(),
			messageID: "m-1",
		})

		failure := awaitFailure(t, subscriber)
		assert.Equal(t, ReliableDeliveryStageProtocol, failure.Stage())
		assert.ErrorContains(t, failure.Err(), "unexpected StoredAck")
	})

	t.Run("With late duplicate Produced after acceptance", func(t *testing.T) {
		harness := newProducerControllerHarness(t, nil)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		credit := harness.latestRequestNext(t)
		harness.produceOne(t, "m-1")

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == 1
		}, 3*time.Second, 10*time.Millisecond)

		// a late duplicate of the accepted handshake is ignored
		produced, err := NewProduced(credit, "m-1", &testpb.Reply{Content: "m-1"})
		require.NoError(t, err)
		harness.fromProducer(t, produced)
		pause.For(200 * time.Millisecond)
		assert.True(t, harness.producerController.IsRunning())
		assert.Len(t, harness.sequencedEmissions(), 1)
	})
}

func TestProducerControllerResendCappedByDemand(t *testing.T) {
	harness := newProducerControllerHarness(t, nil)
	sessionID := harness.register(t)
	nonce := harness.nonceOf(t)

	request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
	require.NoError(t, err)
	harness.fromConsumerController(t, request)

	harness.produceOne(t, "m-1")
	harness.produceOne(t, "m-2")

	require.Eventually(t, func() bool {
		return len(harness.sequencedEmissions()) == 2
	}, 3*time.Second, 10*time.Millisecond)

	// a timeout request whose demand window covers only seq=1 stops the resend loop
	capped, err := commands.NewRequest(sessionID, nonce, 0, 1, true)
	require.NoError(t, err)
	harness.fromConsumerController(t, capped)

	require.Eventually(t, func() bool {
		for _, emission := range harness.sequencedEmissions()[2:] {
			if emission.Seq() == 1 {
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)

	for _, emission := range harness.sequencedEmissions()[2:] {
		assert.NotEqual(t, int64(2), emission.Seq())
	}
}

func TestProducerControllerDurableQueueFailures(t *testing.T) {
	t.Run("With accept fencing", func(t *testing.T) {
		queue := &mockDurableQueue{acceptErr: gerrors.ErrQueueFenced}
		harness := newProducerControllerHarness(t, queue)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)

		credit := harness.latestRequestNext(t)
		produced, err := NewProduced(credit, "m-1", &testpb.Reply{Content: "m-1"})
		require.NoError(t, err)
		harness.fromProducer(t, produced)
		stored := harness.latestStored(t)
		ack, err := NewStoredAck(stored)
		require.NoError(t, err)
		harness.fromProducer(t, ack)

		failure := awaitFailure(t, subscriber)
		assert.Equal(t, ReliableDeliveryStageAccept, failure.Stage())
		assert.ErrorIs(t, failure.Err(), gerrors.ErrQueueFenced)
	})

	t.Run("With confirm conflict", func(t *testing.T) {
		queue := &mockDurableQueue{confirmErr: gerrors.ErrQueueConflict}
		harness := newProducerControllerHarness(t, queue)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)
		harness.produceOne(t, "m-1")

		confirm, err := commands.NewAck(sessionID, nonce, 1)
		require.NoError(t, err)
		harness.fromConsumerController(t, confirm)

		failure := awaitFailure(t, subscriber)
		assert.Equal(t, ReliableDeliveryStageConfirm, failure.Stage())
		assert.ErrorIs(t, failure.Err(), gerrors.ErrQueueConflict)
	})

	t.Run("With load failure after restart publishes failure", func(t *testing.T) {
		queue := &mockDurableQueue{}
		harness := newProducerControllerHarness(t, queue)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		queue.mu.Lock()
		queue.loadErr = errors.New("backing store is unreachable")
		queue.mu.Unlock()

		_ = harness.producerController.Restart(harness.ctx)

		failure := awaitFailure(t, subscriber)
		assert.Equal(t, "producer", failure.EndpointName())
		assert.Equal(t, ReliableControllerRoleProducer, failure.ControllerRole())
		assert.Equal(t, ReliableDeliveryStageLoad, failure.Stage())
	})

	t.Run("With deferred store behind slow confirm", func(t *testing.T) {
		queue := &mockDurableQueue{confirmDelay: 300 * time.Millisecond}
		harness := newProducerControllerHarness(t, queue)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
		require.NoError(t, err)
		harness.fromConsumerController(t, request)
		harness.produceOne(t, "m-1")

		confirm, err := commands.NewAck(sessionID, nonce, 1)
		require.NoError(t, err)
		harness.fromConsumerController(t, confirm)

		// while Confirm occupies the lane, the next Store is deferred and later pumped
		harness.produceOne(t, "m-2")

		require.Eventually(t, func() bool {
			return len(harness.sequencedEmissions()) == 2
		}, 5*time.Second, 20*time.Millisecond)

		_, operations, confirmed := queue.snapshot()
		assert.Contains(t, operations, "store:m-2")
		assert.Contains(t, operations, "accept:m-2")
		assert.Contains(t, operations, "confirm")
		assert.EqualValues(t, 1, confirmed)
	})
}

func TestProducerControllerEdgeBranches(t *testing.T) {
	// the controller under test is never spawned: its handlers run on the
	// test goroutine with stand-in PIDs, so no actor turn touches its state
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "producer", &deliveryRecorder{})
	require.NoError(t, err)

	consumer, err := system.Spawn(ctx, "consumer", NewMockActor())
	require.NoError(t, err)

	spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "consumer", consumer.IncarnationID())
	require.NoError(t, err)

	consumerControllerName := reliableCompanionName(ReliableControllerRoleConsumer, consumer.IncarnationID())
	consumerControllerStandIn, err := system.Spawn(ctx, consumerControllerName, &deliveryRecorder{}, asSystem(), asReliableCompanion(spec))
	require.NoError(t, err)

	payload, err := NewReliablePayload([]byte("frame"))
	require.NoError(t, err)

	spawnControllerHost := func(t *testing.T, name string) *PID {
		t.Helper()
		host, err := system.Spawn(ctx, name, &deliveryRecorder{})
		require.NoError(t, err)
		return host
	}

	t.Run("With PreStart validation", func(t *testing.T) {
		assert.ErrorContains(t, newProducerController(nil, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), nil).PreStart(nil), "bound local producer")
		assert.ErrorContains(t, newProducerController(newRemotePID(address.New("remote", "sys", "127.0.0.1", 1), nil), testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), nil).PreStart(nil), "bound local producer")
		assert.ErrorContains(t, newProducerController(producer, testProducerConfig("", 1, time.Millisecond, time.Millisecond), nil).PreStart(nil), "consumer endpoint name")
		assert.ErrorContains(t, newProducerController(producer, testProducerConfig("consumer", 0, time.Millisecond, time.Millisecond), nil).PreStart(nil), "positive retry settings")
		assert.ErrorContains(t, newProducerController(producer, testProducerConfig("consumer", 1, 0, time.Millisecond), nil).PreStart(nil), "positive retry settings")
		assert.ErrorContains(t, newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, 0), nil).PreStart(nil), "positive retry settings")
	})

	t.Run("With load failure on first incarnation", func(t *testing.T) {
		queue := &mockDurableQueue{loadErr: errors.New("unreachable")}
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), queue)
		pctx := newContext(ctx, "producerController", system)
		err := controller.PreStart(pctx)
		require.ErrorContains(t, err, "failed to load durable state")
		assert.EqualValues(t, 1, controller.generation)
	})

	t.Run("With stale tick generation", func(t *testing.T) {
		host := spawnControllerHost(t, "host-stale-tick")
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), nil)
		require.NoError(t, controller.PreStart(nil))
		controller.handshake = producerHandshakeCredit

		stale := &producerControllerTick{generation: controller.generation + 1}
		rctx := newReceiveContext(context.Background(), system.NoSender(), host, stale)
		controller.handleTick(rctx, stale)
		assert.Equal(t, producerHandshakeCredit, controller.handshake)
	})

	t.Run("With consumer controller terminated", func(t *testing.T) {
		host := spawnControllerHost(t, "host-consumer-controller-terminated")
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), nil)
		require.NoError(t, controller.PreStart(nil))
		controller.consumerController = consumerControllerStandIn
		controller.registrationNonce = "nonce"
		controller.currentSeq = 3
		controller.demandUpTo = 10

		terminated := NewTerminated(consumerControllerStandIn.Path())
		rctx := newReceiveContext(context.Background(), system.NoSender(), host, terminated)
		controller.handleTerminated(rctx, terminated)

		assert.Nil(t, controller.consumerController)
		assert.Empty(t, controller.registrationNonce)
		assert.EqualValues(t, 3, controller.demandUpTo)
	})

	t.Run("With sequence space exhausted", func(t *testing.T) {
		host := spawnControllerHost(t, "host-seq-exhausted")
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), nil)
		require.NoError(t, controller.PreStart(nil))
		controller.currentSeq = math.MaxInt64 - 1
		controller.demandUpTo = math.MaxInt64

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.allowNextRequest(rctx)

		failure := awaitFailure(t, subscriber)
		assert.Equal(t, ReliableDeliveryStageProtocol, failure.Stage())
		assert.ErrorContains(t, failure.Err(), "sequence space exhausted")
	})

	t.Run("With impossible RequestNext construction", func(t *testing.T) {
		host := spawnControllerHost(t, "host-request-next")
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), nil)
		require.NoError(t, controller.PreStart(nil))
		controller.sessionID = ""
		controller.token = ""

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.sendRequestNext(rctx)

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build RequestNext")
	})

	t.Run("With impossible volatile store result", func(t *testing.T) {
		host := spawnControllerHost(t, "host-store-result")
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), nil)
		require.NoError(t, controller.PreStart(nil))
		controller.pendingMessageID = "m-1"

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.startStore(rctx)

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build store result")
	})

	t.Run("With impossible unconfirmed record", func(t *testing.T) {
		host := spawnControllerHost(t, "host-unconfirmed")
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), nil)
		require.NoError(t, controller.PreStart(nil))
		controller.pendingMessageID = ""
		controller.token = uuid.NewString()

		result, err := NewStoreResult(1, false, payload)
		require.NoError(t, err)

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.completeStore(rctx, result)

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to record unconfirmed")
	})

	t.Run("With impossible Stored construction", func(t *testing.T) {
		host := spawnControllerHost(t, "host-stored")
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), nil)
		require.NoError(t, controller.PreStart(nil))
		controller.sessionID = ""
		controller.token = ""
		controller.pendingMessageID = "m-1"

		result, err := NewStoreResult(1, true, payload)
		require.NoError(t, err)

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.completeStore(rctx, result)

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build Stored")
	})

	t.Run("With impossible SequencedMessage construction", func(t *testing.T) {
		host := spawnControllerHost(t, "host-sequenced")
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), nil)
		require.NoError(t, controller.PreStart(nil))
		controller.sessionID = ""
		controller.consumerController = consumerControllerStandIn
		controller.demandUpTo = 10

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.emitSequenced(rctx, "m-1", 1, payload, chunkMark{})

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build SequencedMessage")
	})

	t.Run("With impossible RegistrationAck construction", func(t *testing.T) {
		host := spawnControllerHost(t, "host-registration-ack")
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), nil)
		require.NoError(t, controller.PreStart(nil))
		controller.confirmedSeq = -1

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		registerConsumer, err := commands.NewRegisterConsumer(uuid.NewString())
		require.NoError(t, err)
		rctx := newReceiveContext(context.Background(), consumerControllerStandIn, host, registerConsumer)
		controller.handleRegisterConsumer(rctx, registerConsumer)

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build RegistrationAck")
	})

	t.Run("With terminate already failed", func(t *testing.T) {
		host := spawnControllerHost(t, "host-terminate-once")
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), nil)
		require.NoError(t, controller.PreStart(nil))
		controller.failed = true

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.terminate(rctx, ReliableDeliveryStageProtocol, errors.New("ignored"))
		assert.True(t, controller.failed)
		assert.True(t, host.IsRunning())
	})

	t.Run("With publishFailure without event stream", func(t *testing.T) {
		// a synthetic local PID keeps eventsStream nil without racing a live
		// actor's turns; spawning and nilling the field on a running PID races
		// with reads on the dispatcher goroutine
		producerAddr := address.New("no-stream-producer", system.Name(), "127.0.0.1", 1)
		producerWithoutStream := &PID{address: producerAddr, path: newPath(producerAddr)}

		controller := newProducerController(producerWithoutStream, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), nil)
		require.NoError(t, controller.PreStart(nil))
		controller.publishFailure(ReliableDeliveryStageProtocol, errors.New("silent"))
	})

	t.Run("With tell to dead peer", func(t *testing.T) {
		host := spawnControllerHost(t, "host-tell-dead")
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), nil)
		require.NoError(t, controller.PreStart(nil))

		dead, err := system.Spawn(ctx, "dead-peer", &deliveryRecorder{})
		require.NoError(t, err)
		require.NoError(t, dead.Shutdown(ctx))

		require.Eventually(t, func() bool {
			return !dead.IsRunning()
		}, 3*time.Second, 10*time.Millisecond)

		message, err := commands.NewSequencedMessage(controller.sessionID, "m-1", 1, payload.rawBytes())
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.tell(rctx, dead, message)
	})

	t.Run("With pumpLane deferred handshake op", func(t *testing.T) {
		host := spawnControllerHost(t, "host-pump-deferred")
		queue := &mockDurableQueue{}
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Hour), queue)
		pctx := newContext(ctx, "producerController", system)
		require.NoError(t, controller.PreStart(pctx))

		controller.opInFlight = false
		controller.deferredOp = queueOpAccept
		controller.dirtyConfirmSeq = 1

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.pumpLane(rctx)
		assert.Zero(t, controller.deferredOp)
		assert.True(t, controller.opInFlight)
	})

	t.Run("With launchOp deferral while lane busy", func(t *testing.T) {
		host := spawnControllerHost(t, "host-launch-defer")
		queue := &mockDurableQueue{}
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Hour), queue)
		pctx := newContext(ctx, "producerController", system)
		require.NoError(t, controller.PreStart(pctx))

		controller.opInFlight = true
		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.launchOp(rctx, queueOpStore)
		assert.Equal(t, queueOpStore, controller.deferredOp)
	})

	t.Run("With pumpLane idle when queue absent", func(t *testing.T) {
		host := spawnControllerHost(t, "host-pump-nil")
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), nil)
		require.NoError(t, controller.PreStart(nil))
		controller.dirtyConfirmSeq = 5

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.pumpLane(rctx)
		assert.EqualValues(t, 5, controller.dirtyConfirmSeq)
	})

	t.Run("With handleQueueFailure non-fencing escalation", func(t *testing.T) {
		host := spawnControllerHost(t, "host-queue-failure")
		controller := newProducerController(producer, testProducerConfig("consumer", 1, time.Millisecond, time.Millisecond), &mockDurableQueue{})
		require.NoError(t, controller.PreStart(newContext(ctx, "producerController", system)))

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.handleQueueFailure(rctx, &queueOpResult{
			kind: queueOpAccept,
			err:  errors.New("accept unavailable"),
		})
		assert.ErrorIs(t, rctx.getError(), gerrors.ErrReliableAccept)
		assert.ErrorContains(t, rctx.getError(), "accept unavailable")

		rctx = newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.handleQueueFailure(rctx, &queueOpResult{
			kind: queueOpStore,
			err:  errors.New("store unavailable"),
		})
		assert.ErrorIs(t, rctx.getError(), gerrors.ErrReliableStore)

		rctx = newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.handleQueueFailure(rctx, &queueOpResult{
			kind: queueOpConfirm,
			err:  errors.New("confirm unavailable"),
		})
		assert.ErrorIs(t, rctx.getError(), gerrors.ErrReliableConfirm)
	})
}
