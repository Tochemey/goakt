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

	state, err := NewDurableQueueState(x.currentSeq, x.confirmedSeq, x.stored)
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

	cut := 0
	for cut < len(x.stored) && x.stored[cut].Seq() <= x.confirmedSeq {
		cut++
	}
	x.stored = x.stored[cut:]

	return nil
}

// snapshot returns copies of the observable queue state.
func (x *mockDurableQueue) snapshot() (int, []string, int64) {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.loads, append([]string(nil), x.operations...), x.confirmedSeq
}

// pcHarness wires a producer controller under test to a recording producer
// endpoint and a recording consumer controller stand-in.
type pcHarness struct {
	ctx       context.Context
	system    *actorSystem
	producer  *PID
	ccStandIn *PID
	pc        *PID
	// usedTokens tracks credits already answered; only the test goroutine
	// touches it.
	usedTokens map[string]bool
}

// newProducerControllerHarness starts a cluster-disabled system with the
// producer endpoint, the consumer endpoint plus its registered controller
// stand-in, and the producer controller under test.
func newProducerControllerHarness(t *testing.T, queue DurableProducerQueue) *pcHarness {
	t.Helper()

	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "producer", &deliveryRecorder{})
	require.NoError(t, err)

	consumer, err := system.Spawn(ctx, "consumer", NewMockActor())
	require.NoError(t, err)

	spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "consumer", consumer.IncarnationID())
	require.NoError(t, err)

	ccName := reliableCompanionName(ReliableControllerRoleConsumer, consumer.IncarnationID())
	ccStandIn, err := system.Spawn(ctx, ccName, &deliveryRecorder{}, asSystem(), asReliableCompanion(spec))
	require.NoError(t, err)

	controller := newProducerController(producer, "consumer", queue, 2, 20*time.Millisecond, 150*time.Millisecond)
	pc, err := system.Spawn(ctx, "producer-controller", controller)
	require.NoError(t, err)

	return &pcHarness{ctx: ctx, system: system, producer: producer, ccStandIn: ccStandIn, pc: pc, usedTokens: map[string]bool{}}
}

// recordedOf asks a recorder double for its message snapshot.
func (x *pcHarness) recordedOf(pid *PID) []any {
	response, err := Ask(x.ctx, pid, &getRecorded{}, time.Second)
	if err != nil {
		return nil
	}

	snapshot, _ := response.([]any)
	return snapshot
}

// fromCC sends a message to the producer controller from the consumer
// controller stand-in.
func (x *pcHarness) fromCC(t *testing.T, message any) {
	t.Helper()
	require.NoError(t, Tell(x.ctx, x.ccStandIn, &deliveryForward{to: x.pc, message: message}))
}

// fromProducer sends a message to the producer controller from the producer.
func (x *pcHarness) fromProducer(t *testing.T, message any) {
	t.Helper()
	require.NoError(t, Tell(x.ctx, x.producer, &deliveryForward{to: x.pc, message: message}))
}

// register performs the registration handshake and returns the session ID.
func (x *pcHarness) register(t *testing.T) string {
	t.Helper()

	registerConsumer, err := commands.NewRegisterConsumer(uuid.NewString())
	require.NoError(t, err)
	x.fromCC(t, registerConsumer)

	var sessionID string

	require.Eventually(t, func() bool {
		for _, message := range x.recordedOf(x.ccStandIn) {
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
func (x *pcHarness) nonceOf(t *testing.T) string {
	t.Helper()

	var nonce string

	for _, message := range x.recordedOf(x.ccStandIn) {
		if ack, ok := message.(*commands.RegistrationAck); ok {
			nonce = ack.Nonce()
		}
	}

	require.NotEmpty(t, nonce)
	return nonce
}

// latestRequestNext waits for the latest credit granted to the producer.
func (x *pcHarness) latestRequestNext(t *testing.T) *RequestNext {
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
func (x *pcHarness) latestStored(t *testing.T) *Stored {
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

// sequencedEmissions returns the sequenced messages the stand-in received.
func (x *pcHarness) sequencedEmissions() []*commands.SequencedMessage {
	var emissions []*commands.SequencedMessage

	for _, message := range x.recordedOf(x.ccStandIn) {
		if sequenced, ok := message.(*commands.SequencedMessage); ok {
			emissions = append(emissions, sequenced)
		}
	}

	return emissions
}

// produceOne drives one full producer handshake for messageID, waiting for a
// fresh credit and the storage acknowledgement of exactly this message.
func (x *pcHarness) produceOne(t *testing.T, messageID string) {
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
	produced, err := NewProduced(request, messageID, &testpb.Reply{Content: messageID})
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

// waitFailure polls the event stream until a terminal failure arrives; the
// subscriber iterator drains a snapshot per call.
func waitFailure(t *testing.T, subscriber eventstream.Subscriber) *ReliableDeliveryFailed {
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
	harness.fromCC(t, request)

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
	harness.fromCC(t, confirmOne)

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
	harness.fromCC(t, request)

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
	assert.True(t, harness.pc.IsRunning())
	assert.Len(t, harness.sequencedEmissions(), 1)
}

func TestProducerControllerDurableFlow(t *testing.T) {
	queue := &mockDurableQueue{}
	harness := newProducerControllerHarness(t, queue)
	sessionID := harness.register(t)
	nonce := harness.nonceOf(t)

	request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
	require.NoError(t, err)
	harness.fromCC(t, request)

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
	harness.fromCC(t, confirm)

	require.Eventually(t, func() bool {
		_, _, confirmed := queue.snapshot()
		return confirmed == 1
	}, 3*time.Second, 10*time.Millisecond)

	// a restart reloads authoritative state and acquires a new epoch
	require.NoError(t, harness.pc.Restart(harness.ctx))

	require.Eventually(t, func() bool {
		loads, _, _ := queue.snapshot()
		return loads >= 2
	}, 3*time.Second, 10*time.Millisecond)
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
	harness.fromCC(t, request)

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
		harness.fromCC(t, request)

		// the store fails, so only the credit half of the handshake runs
		credit := harness.latestRequestNext(t)
		produced, err := NewProduced(credit, "m-1", &testpb.Reply{Content: "m-1"})
		require.NoError(t, err)
		harness.fromProducer(t, produced)

		failure := waitFailure(t, subscriber)
		assert.Equal(t, "producer", failure.EndpointName())
		assert.Equal(t, ReliableControllerRoleProducer, failure.ControllerRole())
		assert.Equal(t, ReliableDeliveryStageStore, failure.Stage())
		assert.ErrorIs(t, failure.Err(), gerrors.ErrQueueFenced)

		require.Eventually(t, func() bool {
			return !harness.pc.IsRunning()
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
		harness.fromCC(t, illegal)

		failure := waitFailure(t, subscriber)
		assert.Equal(t, ReliableDeliveryStageProtocol, failure.Stage())

		require.Eventually(t, func() bool {
			return !harness.pc.IsRunning()
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
		harness.fromCC(t, request)

		// a payload type without a registered serializer cannot be encoded:
		// encoding is deterministic, so the controller must fail terminally
		// instead of panicking or spinning the retry loop
		credit := harness.latestRequestNext(t)
		produced, err := NewProduced(credit, "m-1", struct{ ID string }{ID: "m-1"})
		require.NoError(t, err)
		harness.fromProducer(t, produced)

		failure := waitFailure(t, subscriber)
		assert.Equal(t, ReliableControllerRoleProducer, failure.ControllerRole())
		assert.Equal(t, ReliableDeliveryStageProtocol, failure.Stage())
		assert.ErrorContains(t, failure.Err(), "no serializer is registered")

		require.Eventually(t, func() bool {
			return !harness.pc.IsRunning()
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
	harness.fromCC(t, stale)

	staleAck, err := commands.NewAck(sessionID, uuid.NewString(), 0)
	require.NoError(t, err)
	harness.fromCC(t, staleAck)

	pause.For(200 * time.Millisecond)
	assert.Empty(t, harness.recordedOf(harness.producer))
	assert.True(t, harness.pc.IsRunning())
}

func TestProducerControllerProducerTerminated(t *testing.T) {
	harness := newProducerControllerHarness(t, nil)
	harness.register(t)

	require.NoError(t, harness.producer.Shutdown(harness.ctx))

	require.Eventually(t, func() bool {
		return !harness.pc.IsRunning()
	}, 3*time.Second, 10*time.Millisecond)
}

func TestProducerControllerConsumerControllerTerminated(t *testing.T) {
	harness := newProducerControllerHarness(t, nil)
	sessionID := harness.register(t)
	nonce := harness.nonceOf(t)

	request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
	require.NoError(t, err)
	harness.fromCC(t, request)

	credit := harness.latestRequestNext(t)
	produced, err := NewProduced(credit, "m-1", &testpb.Reply{Content: "m-1"})
	require.NoError(t, err)
	harness.fromProducer(t, produced)
	stored := harness.latestStored(t)

	// the consumer controller dies while Stored awaits acknowledgement: emission
	// is deferred because registration is cleared, and a replacement recovers
	require.NoError(t, harness.ccStandIn.Shutdown(harness.ctx))

	require.Eventually(t, func() bool {
		return !harness.ccStandIn.IsRunning()
	}, 3*time.Second, 10*time.Millisecond)

	ack, err := NewStoredAck(stored)
	require.NoError(t, err)
	harness.fromProducer(t, ack)
	pause.For(200 * time.Millisecond)
	assert.Empty(t, harness.sequencedEmissions())
	assert.True(t, harness.pc.IsRunning())
}

func TestProducerControllerIllegalAck(t *testing.T) {
	harness := newProducerControllerHarness(t, nil)
	sessionID := harness.register(t)
	nonce := harness.nonceOf(t)

	subscriber, err := harness.system.Subscribe()
	require.NoError(t, err)

	illegal, err := commands.NewAck(sessionID, nonce, 99)
	require.NoError(t, err)
	harness.fromCC(t, illegal)

	failure := waitFailure(t, subscriber)
	assert.Equal(t, ReliableDeliveryStageProtocol, failure.Stage())

	require.Eventually(t, func() bool {
		return !harness.pc.IsRunning()
	}, 3*time.Second, 10*time.Millisecond)
}

func TestProducerControllerProtocolDrops(t *testing.T) {
	harness := newProducerControllerHarness(t, nil)
	sessionID := harness.register(t)
	nonce := harness.nonceOf(t)

	request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
	require.NoError(t, err)
	harness.fromCC(t, request)
	credit := harness.latestRequestNext(t)

	t.Run("With Produced from unexpected sender", func(t *testing.T) {
		produced, err := NewProduced(credit, "m-unexpected", &testpb.Reply{Content: "x"})
		require.NoError(t, err)
		harness.fromCC(t, produced)
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.pc.IsRunning())
	})

	t.Run("With Produced from stale session", func(t *testing.T) {
		harness.fromProducer(t, &Produced{
			sessionID: "stale-session",
			token:     credit.Token(),
			messageID: "m-stale",
			payload:   &testpb.Reply{Content: "x"},
		})
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.pc.IsRunning())
	})

	t.Run("With StoredAck from unexpected sender", func(t *testing.T) {
		stored, err := newStoredFromState(sessionID, credit.Token(), "m-1", 1, harness.producer, harness.pc)
		require.NoError(t, err)
		ack, err := NewStoredAck(stored)
		require.NoError(t, err)
		harness.fromCC(t, ack)
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.pc.IsRunning())
	})

	t.Run("With StoredAck from stale session", func(t *testing.T) {
		harness.fromProducer(t, &StoredAck{
			sessionID: "stale-session",
			token:     credit.Token(),
			messageID: "m-1",
		})
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.pc.IsRunning())
	})

	t.Run("With unhandled message", func(t *testing.T) {
		require.NoError(t, Tell(harness.ctx, harness.pc, &testpb.Reply{Content: "noise"}))
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.pc.IsRunning())
	})

	t.Run("With stale queue op result", func(t *testing.T) {
		require.NoError(t, Tell(harness.ctx, harness.pc, &queueOpResult{
			sessionID:   "stale-session",
			operationID: 99,
			kind:        queueOpStore,
		}))
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.pc.IsRunning())
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
		harness.fromCC(t, request)
		_ = harness.latestRequestNext(t)

		harness.fromProducer(t, &Produced{
			sessionID: sessionID,
			token:     uuid.NewString(),
			messageID: "m-1",
			payload:   &testpb.Reply{Content: "m-1"},
		})

		failure := waitFailure(t, subscriber)
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

		failure := waitFailure(t, subscriber)
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

		failure := waitFailure(t, subscriber)
		assert.Equal(t, ReliableDeliveryStageProtocol, failure.Stage())
		assert.ErrorContains(t, failure.Err(), "unexpected StoredAck")
	})

	t.Run("With late duplicate Produced after acceptance", func(t *testing.T) {
		harness := newProducerControllerHarness(t, nil)
		sessionID := harness.register(t)
		nonce := harness.nonceOf(t)

		request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
		require.NoError(t, err)
		harness.fromCC(t, request)

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
		assert.True(t, harness.pc.IsRunning())
		assert.Len(t, harness.sequencedEmissions(), 1)
	})
}

func TestProducerControllerResendCappedByDemand(t *testing.T) {
	harness := newProducerControllerHarness(t, nil)
	sessionID := harness.register(t)
	nonce := harness.nonceOf(t)

	request, err := commands.NewRequest(sessionID, nonce, 0, 10, false)
	require.NoError(t, err)
	harness.fromCC(t, request)

	harness.produceOne(t, "m-1")
	harness.produceOne(t, "m-2")

	require.Eventually(t, func() bool {
		return len(harness.sequencedEmissions()) == 2
	}, 3*time.Second, 10*time.Millisecond)

	// a timeout request whose demand window covers only seq=1 stops the resend loop
	capped, err := commands.NewRequest(sessionID, nonce, 0, 1, true)
	require.NoError(t, err)
	harness.fromCC(t, capped)

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
		harness.fromCC(t, request)

		credit := harness.latestRequestNext(t)
		produced, err := NewProduced(credit, "m-1", &testpb.Reply{Content: "m-1"})
		require.NoError(t, err)
		harness.fromProducer(t, produced)
		stored := harness.latestStored(t)
		ack, err := NewStoredAck(stored)
		require.NoError(t, err)
		harness.fromProducer(t, ack)

		failure := waitFailure(t, subscriber)
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
		harness.fromCC(t, request)
		harness.produceOne(t, "m-1")

		confirm, err := commands.NewAck(sessionID, nonce, 1)
		require.NoError(t, err)
		harness.fromCC(t, confirm)

		failure := waitFailure(t, subscriber)
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

		_ = harness.pc.Restart(harness.ctx)

		failure := waitFailure(t, subscriber)
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
		harness.fromCC(t, request)
		harness.produceOne(t, "m-1")

		confirm, err := commands.NewAck(sessionID, nonce, 1)
		require.NoError(t, err)
		harness.fromCC(t, confirm)

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

	ccName := reliableCompanionName(ReliableControllerRoleConsumer, consumer.IncarnationID())
	ccStandIn, err := system.Spawn(ctx, ccName, &deliveryRecorder{}, asSystem(), asReliableCompanion(spec))
	require.NoError(t, err)

	payload, err := NewReliablePayload([]byte("frame"))
	require.NoError(t, err)

	newShell := func(t *testing.T, name string) *PID {
		t.Helper()
		shell, err := system.Spawn(ctx, name, &deliveryRecorder{})
		require.NoError(t, err)
		return shell
	}

	t.Run("With PreStart validation", func(t *testing.T) {
		assert.ErrorContains(t, newProducerController(nil, "consumer", nil, 1, time.Millisecond, time.Millisecond).PreStart(nil), "bound local producer")
		assert.ErrorContains(t, newProducerController(newRemotePID(address.New("remote", "sys", "127.0.0.1", 1), nil), "consumer", nil, 1, time.Millisecond, time.Millisecond).PreStart(nil), "bound local producer")
		assert.ErrorContains(t, newProducerController(producer, "", nil, 1, time.Millisecond, time.Millisecond).PreStart(nil), "consumer endpoint name")
		assert.ErrorContains(t, newProducerController(producer, "consumer", nil, 0, time.Millisecond, time.Millisecond).PreStart(nil), "positive retry settings")
		assert.ErrorContains(t, newProducerController(producer, "consumer", nil, 1, 0, time.Millisecond).PreStart(nil), "positive retry settings")
		assert.ErrorContains(t, newProducerController(producer, "consumer", nil, 1, time.Millisecond, 0).PreStart(nil), "positive retry settings")
	})

	t.Run("With load failure on first incarnation", func(t *testing.T) {
		queue := &mockDurableQueue{loadErr: errors.New("unreachable")}
		controller := newProducerController(producer, "consumer", queue, 1, time.Millisecond, time.Millisecond)
		pctx := newContext(ctx, "pc", system)
		err := controller.PreStart(pctx)
		require.ErrorContains(t, err, "failed to load durable state")
		assert.EqualValues(t, 1, controller.generation)
	})

	t.Run("With stale tick generation", func(t *testing.T) {
		shell := newShell(t, "shell-stale-tick")
		controller := newProducerController(producer, "consumer", nil, 1, time.Millisecond, time.Millisecond)
		require.NoError(t, controller.PreStart(nil))
		controller.handshake = producerHandshakeCredit

		stale := &producerControllerTick{generation: controller.generation + 1}
		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, stale)
		controller.handleTick(rctx, stale)
		assert.Equal(t, producerHandshakeCredit, controller.handshake)
	})

	t.Run("With consumer controller terminated", func(t *testing.T) {
		shell := newShell(t, "shell-cc-terminated")
		controller := newProducerController(producer, "consumer", nil, 1, time.Millisecond, time.Millisecond)
		require.NoError(t, controller.PreStart(nil))
		controller.consumerController = ccStandIn
		controller.registrationNonce = "nonce"
		controller.currentSeq = 3
		controller.demandUpTo = 10

		terminated := NewTerminated(ccStandIn.Path())
		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, terminated)
		controller.handleTerminated(rctx, terminated)

		assert.Nil(t, controller.consumerController)
		assert.Empty(t, controller.registrationNonce)
		assert.EqualValues(t, 3, controller.demandUpTo)
	})

	t.Run("With sequence space exhausted", func(t *testing.T) {
		shell := newShell(t, "shell-seq-exhausted")
		controller := newProducerController(producer, "consumer", nil, 1, time.Millisecond, time.Millisecond)
		require.NoError(t, controller.PreStart(nil))
		controller.currentSeq = math.MaxInt64 - 1
		controller.demandUpTo = math.MaxInt64

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, &PostStart{})
		controller.allowNextRequest(rctx)

		failure := waitFailure(t, subscriber)
		assert.Equal(t, ReliableDeliveryStageProtocol, failure.Stage())
		assert.ErrorContains(t, failure.Err(), "sequence space exhausted")
	})

	t.Run("With impossible RequestNext construction", func(t *testing.T) {
		shell := newShell(t, "shell-request-next")
		controller := newProducerController(producer, "consumer", nil, 1, time.Millisecond, time.Millisecond)
		require.NoError(t, controller.PreStart(nil))
		controller.sessionID = ""
		controller.token = ""

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, &PostStart{})
		controller.sendRequestNext(rctx)

		failure := waitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build RequestNext")
	})

	t.Run("With impossible volatile store result", func(t *testing.T) {
		shell := newShell(t, "shell-store-result")
		controller := newProducerController(producer, "consumer", nil, 1, time.Millisecond, time.Millisecond)
		require.NoError(t, controller.PreStart(nil))
		controller.pendingMessageID = "m-1"

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, &PostStart{})
		controller.startStore(rctx)

		failure := waitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build store result")
	})

	t.Run("With impossible unconfirmed record", func(t *testing.T) {
		shell := newShell(t, "shell-unconfirmed")
		controller := newProducerController(producer, "consumer", nil, 1, time.Millisecond, time.Millisecond)
		require.NoError(t, controller.PreStart(nil))
		controller.pendingMessageID = ""
		controller.token = uuid.NewString()

		result, err := NewStoreResult(1, false, payload)
		require.NoError(t, err)

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, &PostStart{})
		controller.completeStore(rctx, result)

		failure := waitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to record unconfirmed")
	})

	t.Run("With impossible Stored construction", func(t *testing.T) {
		shell := newShell(t, "shell-stored")
		controller := newProducerController(producer, "consumer", nil, 1, time.Millisecond, time.Millisecond)
		require.NoError(t, controller.PreStart(nil))
		controller.sessionID = ""
		controller.token = ""
		controller.pendingMessageID = "m-1"

		result, err := NewStoreResult(1, true, payload)
		require.NoError(t, err)

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, &PostStart{})
		controller.completeStore(rctx, result)

		failure := waitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build Stored")
	})

	t.Run("With impossible SequencedMessage construction", func(t *testing.T) {
		shell := newShell(t, "shell-sequenced")
		controller := newProducerController(producer, "consumer", nil, 1, time.Millisecond, time.Millisecond)
		require.NoError(t, controller.PreStart(nil))
		controller.sessionID = ""
		controller.consumerController = ccStandIn
		controller.demandUpTo = 10

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, &PostStart{})
		controller.emitSequenced(rctx, "m-1", 1, payload)

		failure := waitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build SequencedMessage")
	})

	t.Run("With impossible RegistrationAck construction", func(t *testing.T) {
		shell := newShell(t, "shell-registration-ack")
		controller := newProducerController(producer, "consumer", nil, 1, time.Millisecond, time.Millisecond)
		require.NoError(t, controller.PreStart(nil))
		controller.confirmedSeq = -1

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		registerConsumer, err := commands.NewRegisterConsumer(uuid.NewString())
		require.NoError(t, err)
		rctx := newReceiveContext(context.Background(), ccStandIn, shell, registerConsumer)
		controller.handleRegisterConsumer(rctx, registerConsumer)

		failure := waitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build RegistrationAck")
	})

	t.Run("With terminate already failed", func(t *testing.T) {
		shell := newShell(t, "shell-terminate-once")
		controller := newProducerController(producer, "consumer", nil, 1, time.Millisecond, time.Millisecond)
		require.NoError(t, controller.PreStart(nil))
		controller.failed = true

		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, &PostStart{})
		controller.terminate(rctx, ReliableDeliveryStageProtocol, errors.New("ignored"))
		assert.True(t, controller.failed)
		assert.True(t, shell.IsRunning())
	})

	t.Run("With publishFailure without event stream", func(t *testing.T) {
		lonely, err := system.Spawn(ctx, "lonely-producer", &deliveryRecorder{})
		require.NoError(t, err)
		lonely.eventsStream = nil

		controller := newProducerController(lonely, "consumer", nil, 1, time.Millisecond, time.Millisecond)
		require.NoError(t, controller.PreStart(nil))
		controller.publishFailure(ReliableDeliveryStageProtocol, errors.New("silent"))
	})

	t.Run("With tell to dead peer", func(t *testing.T) {
		shell := newShell(t, "shell-tell-dead")
		controller := newProducerController(producer, "consumer", nil, 1, time.Millisecond, time.Millisecond)
		require.NoError(t, controller.PreStart(nil))

		dead, err := system.Spawn(ctx, "dead-peer", &deliveryRecorder{})
		require.NoError(t, err)
		require.NoError(t, dead.Shutdown(ctx))

		require.Eventually(t, func() bool {
			return !dead.IsRunning()
		}, 3*time.Second, 10*time.Millisecond)

		message, err := commands.NewSequencedMessage(controller.sessionID, "m-1", 1, payload.rawBytes())
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, &PostStart{})
		controller.tell(rctx, dead, message)
	})

	t.Run("With pumpLane deferred handshake op", func(t *testing.T) {
		shell := newShell(t, "shell-pump-deferred")
		queue := &mockDurableQueue{}
		controller := newProducerController(producer, "consumer", queue, 1, time.Millisecond, time.Hour)
		pctx := newContext(ctx, "pc", system)
		require.NoError(t, controller.PreStart(pctx))

		controller.opInFlight = false
		controller.deferredOp = queueOpAccept
		controller.dirtyConfirmSeq = 1

		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, &PostStart{})
		controller.pumpLane(rctx)
		assert.Zero(t, controller.deferredOp)
		assert.True(t, controller.opInFlight)
	})

	t.Run("With launchOp deferral while lane busy", func(t *testing.T) {
		shell := newShell(t, "shell-launch-defer")
		queue := &mockDurableQueue{}
		controller := newProducerController(producer, "consumer", queue, 1, time.Millisecond, time.Hour)
		pctx := newContext(ctx, "pc", system)
		require.NoError(t, controller.PreStart(pctx))

		controller.opInFlight = true
		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, &PostStart{})
		controller.launchOp(rctx, queueOpStore)
		assert.Equal(t, queueOpStore, controller.deferredOp)
	})

	t.Run("With pumpLane idle when queue absent", func(t *testing.T) {
		shell := newShell(t, "shell-pump-nil")
		controller := newProducerController(producer, "consumer", nil, 1, time.Millisecond, time.Millisecond)
		require.NoError(t, controller.PreStart(nil))
		controller.dirtyConfirmSeq = 5

		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, &PostStart{})
		controller.pumpLane(rctx)
		assert.EqualValues(t, 5, controller.dirtyConfirmSeq)
	})

	t.Run("With handleQueueFailure non-fencing escalation", func(t *testing.T) {
		shell := newShell(t, "shell-queue-failure")
		controller := newProducerController(producer, "consumer", &mockDurableQueue{}, 1, time.Millisecond, time.Millisecond)
		require.NoError(t, controller.PreStart(newContext(ctx, "pc", system)))

		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, &PostStart{})
		controller.handleQueueFailure(rctx, &queueOpResult{
			kind: queueOpAccept,
			err:  errors.New("accept unavailable"),
		})
		assert.ErrorIs(t, rctx.getError(), gerrors.ErrReliableAccept)
		assert.ErrorContains(t, rctx.getError(), "accept unavailable")

		rctx = newReceiveContext(context.Background(), system.NoSender(), shell, &PostStart{})
		controller.handleQueueFailure(rctx, &queueOpResult{
			kind: queueOpStore,
			err:  errors.New("store unavailable"),
		})
		assert.ErrorIs(t, rctx.getError(), gerrors.ErrReliableStore)

		rctx = newReceiveContext(context.Background(), system.NoSender(), shell, &PostStart{})
		controller.handleQueueFailure(rctx, &queueOpResult{
			kind: queueOpConfirm,
			err:  errors.New("confirm unavailable"),
		})
		assert.ErrorIs(t, rctx.getError(), gerrors.ErrReliableConfirm)
	})
}
