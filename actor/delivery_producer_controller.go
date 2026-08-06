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
	"math"
	"slices"
	"time"

	"github.com/flowchartsman/retry"
	"github.com/google/uuid"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/types"
)

const (
	// producerHandshakeIdle means no local handshake is outstanding.
	producerHandshakeIdle = iota
	// producerHandshakeCredit means a RequestNext awaits its Produced.
	producerHandshakeCredit
	// producerHandshakeStore means a Produced awaits durable storage.
	producerHandshakeStore
	// producerHandshakeStoredAck means a Stored awaits its StoredAck.
	producerHandshakeStoredAck
	// producerHandshakeAccept means a StoredAck awaits durable acceptance.
	producerHandshakeAccept
)

const (
	// queueOpStore identifies an asynchronous durable Store.
	queueOpStore = iota + 1
	// queueOpAccept identifies an asynchronous durable Accept.
	queueOpAccept
	// queueOpConfirm identifies an asynchronous durable Confirm.
	queueOpConfirm
)

// producerControllerTick is the generation-fenced recurring timer message
// retrying the outstanding local handshake.
type producerControllerTick struct {
	// generation identifies the controller incarnation that created the timer.
	generation uint64
}

// queueOpResult carries the outcome of one asynchronous durable-queue
// operation back into the controller mailbox as plain data; the piped task
// never returns an error.
type queueOpResult struct {
	// sessionID fences results from a previous controller incarnation.
	sessionID string
	// operationID accepts only the single pending operation.
	operationID uint64
	// kind is the queue operation that completed.
	kind int
	// store holds the result of a queueOpStore.
	store StoreResult
	// confirmSeq holds the watermark of a queueOpConfirm.
	confirmSeq int64
	// err is the terminal outcome after the retry policy ran.
	err error
}

// producerController is the endpoint-owned controller running the producer
// side of reliable delivery: it grants tokenized credit to the producer,
// stores and accepts each message through the optional durable queue, emits
// sequenced messages under consumer demand, and resends unconfirmed messages.
type producerController struct {
	// producer is the bound local producer endpoint.
	producer *PID
	// consumerName is the user-visible name of the only consumer endpoint
	// whose controller may register.
	consumerName string
	// queue is the optional durable producer queue; nil runs volatile.
	queue DurableProducerQueue
	// queueRetryAttempts bounds each durable operation's attempts.
	queueRetryAttempts int
	// queueRetryBackoff is the delay before a durable operation retry.
	queueRetryBackoff time.Duration
	// localRetryInterval is the RequestNext/Stored retry cadence.
	localRetryInterval time.Duration
	// deliveryConfirmation reports whether the endpoint asked to be told
	// about each consumer confirmation.
	deliveryConfirmation bool
	// maxChunkBytes splits payloads larger than this many bytes into
	// sequenced chunks; zero disables chunking.
	maxChunkBytes int

	// sessionID identifies this controller incarnation.
	sessionID string
	// epoch is the durable-queue writer epoch acquired by Load.
	epoch QueueEpoch
	// currentSeq is the highest stored sequence.
	currentSeq int64
	// confirmedSeq is the highest consumer-confirmed sequence.
	confirmedSeq int64
	// persistedConfirmedSeq is the highest durably confirmed sequence.
	persistedConfirmedSeq int64
	// unconfirmed holds stored messages awaiting confirmation in ascending
	// contiguous sequence order.
	unconfirmed []UnconfirmedMessage
	// consumerController is the registered consumer controller; nil until registered.
	consumerController *PID
	// registrationNonce fences traffic to the current registration.
	registrationNonce string
	// demandUpTo is the highest sequence the controller may emit.
	demandUpTo int64
	// windowSpan is the consumer's flow-control window as observed on the
	// latest demand grant: every Request grants confirmedSeq+window, so the
	// grant span is the window. It bounds how many chunks one message may
	// need, because the consumer confirms nothing mid-message and a message
	// larger than its buffer could never drain.
	windowSpan int64

	// handshake is the single local handshake phase.
	handshake int
	// token correlates the outstanding RequestNext/Produced/Stored exchange.
	token string
	// pendingMessageID is the message being stored or accepted.
	pendingMessageID string
	// pendingSeq is the sequence assigned to the pending message.
	pendingSeq int64
	// pendingPayload is the authoritative encoded snapshot of the pending message.
	pendingPayload ReliablePayload
	// storedMessage is resent on the tick until its StoredAck arrives.
	storedMessage *Stored
	// pendingChunks aliases the unconfirmed entries of the chunked message in
	// the current handshake; empty for a whole-payload handshake.
	pendingChunks []UnconfirmedMessage
	// lastCompletedToken identifies the most recently accepted handshake so a
	// late duplicate StoredAck stays idempotent.
	lastCompletedToken string
	// lastCompletedMessageID pairs with lastCompletedToken.
	lastCompletedMessageID string

	// opInFlight reports whether a durable operation occupies the lane.
	opInFlight bool
	// nextOperationID numbers durable operations; only the latest is accepted.
	nextOperationID uint64
	// deferredOp is the single handshake operation waiting for the lane.
	deferredOp int
	// dirtyConfirmSeq is the coalesced confirmation watermark awaiting the lane.
	dirtyConfirmSeq int64

	// failed marks that the terminal failure event was already published.
	failed bool
	// generation fences the recurring timer across restarts.
	generation uint64
}

// enforce the Actor contract
var _ Actor = (*producerController)(nil)

// newProducerController creates the producer-side controller bound to the local
// producer endpoint, configured by the endpoint's producer settings. A nil
// queue runs the flow without durability. Settings that arrive incomplete are
// carried through as they are: PreStart rejects them, so an invalid
// configuration fails at controller start rather than silently running with
// substituted values.
func newProducerController(producer *PID, config *reliableProducerConfig, queue DurableProducerQueue) *producerController {
	controller := &producerController{
		producer:             producer,
		consumerName:         config.consumerName,
		queue:                queue,
		localRetryInterval:   config.localRetryInterval,
		deliveryConfirmation: config.deliveryConfirmation,
		// the controller arithmetic runs against len() results, so the
		// validated uint32 setting becomes an int once, here
		maxChunkBytes: int(config.maxChunkBytes),
	}

	if retry := config.queueRetry; retry != nil {
		controller.queueRetryAttempts = retry.maxAttempts
		controller.queueRetryBackoff = retry.initialBackoff
	}

	return controller
}

// PreStart resets incarnation state, generates the session, and, with a
// durable queue, loads authoritative state and acquires the writer epoch.
// Loading may block: PreStart is the framework's init phase, not a mailbox turn.
func (x *producerController) PreStart(ctx *Context) error {
	if x.producer == nil || !x.producer.IsLocal() {
		return errors.New("producer controller requires a bound local producer")
	}

	if types.IsBlank(x.consumerName) {
		return errors.New("producer controller requires the consumer endpoint name")
	}

	if x.queueRetryAttempts < 1 || x.queueRetryBackoff <= 0 || x.localRetryInterval <= 0 {
		return errors.New("producer controller requires positive retry settings")
	}

	if x.maxChunkBytes != 0 && x.queue != nil {
		return errors.New("producer controller cannot combine chunking with a durable queue")
	}

	x.sessionID = uuid.NewString()
	x.epoch = 0
	x.currentSeq = 0
	x.confirmedSeq = 0
	x.persistedConfirmedSeq = 0
	x.unconfirmed = nil
	x.consumerController = nil
	x.registrationNonce = types.EmptyString
	x.demandUpTo = 0
	x.windowSpan = 0
	x.resetHandshake()
	x.lastCompletedToken = types.EmptyString
	x.lastCompletedMessageID = types.EmptyString
	x.opInFlight = false
	x.nextOperationID = 0
	x.deferredOp = 0
	x.dirtyConfirmSeq = 0
	x.failed = false
	x.generation++

	if x.queue == nil {
		return nil
	}

	state, epoch, err := x.queue.Load(ctx.Context())
	if err != nil {
		if x.generation > 1 {
			x.publishFailure(ReliableDeliveryStageLoad, err)
		}

		return fmt.Errorf("producer controller for endpoint=%s failed to load durable state: %w", x.producer.Name(), err)
	}

	x.epoch = epoch
	x.currentSeq = state.CurrentSeq()
	x.confirmedSeq = state.ConfirmedSeq()
	x.persistedConfirmedSeq = state.ConfirmedSeq()
	x.unconfirmed = state.Unconfirmed()
	return nil
}

// PostStop cancels the recurring timer of this incarnation through its
// derived reference, so no state shared with PostStart is read here.
func (x *producerController) PostStop(ctx *Context) error {
	if err := ctx.ActorSystem().CancelSchedule(reliableTickReference(ctx.ActorName(), x.generation)); err != nil {
		ctx.ActorSystem().Logger().Debugf("producer controller for endpoint=%s failed to cancel tick: %v", x.producer.Name(), err)
	}

	return nil
}

// Receive processes controller protocol traffic, producer handshakes, durable
// operation results, lifecycle notifications, and timer ticks.
func (x *producerController) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
		x.handlePostStart(ctx)
	case *commands.RegisterConsumer:
		x.handleRegisterConsumer(ctx, msg)
	case *commands.Request:
		x.handleRequest(ctx, msg)
	case *commands.Ack:
		x.handleAck(ctx, msg)
	case *Produced:
		x.handleProduced(ctx, msg)
	case *StoredAck:
		x.handleStoredAck(ctx, msg)
	case *queueOpResult:
		x.handleQueueOpResult(ctx, msg)
	case *producerControllerTick:
		x.handleTick(ctx, msg)
	case *Terminated:
		x.handleTerminated(ctx, msg)
	default:
		ctx.Unhandled()
	}
}

// handlePostStart watches the producer and creates the generation-fenced
// recurring local-retry tick.
func (x *producerController) handlePostStart(ctx *ReceiveContext) {
	ctx.Watch(x.producer)

	reference := reliableTickReference(ctx.Self().Name(), x.generation)
	tick := &producerControllerTick{generation: x.generation}

	if err := ctx.ActorSystem().Schedule(context.WithoutCancel(ctx.Context()), tick, ctx.Self(), x.localRetryInterval, WithReference(reference)); err != nil {
		// the tick is the controller's only local retry mechanism; escalate
		// so the supervisor restarts this incarnation
		ctx.Err(err)
		return
	}
}

// handleRegisterConsumer fences registration to the consumer endpoint's
// current controller. The sender is trusted only if it equals the companion
// resolved for the configured consumer name right now, which rejects both a
// second live consumer and a delayed pre-relocation controller. A repeat of
// the same PID and nonce is an idempotent ping and only re-acks. A verified
// new PID or nonce starts a new registration generation: demand resets to
// currentSeq because grants of a dead generation must not authorize new
// emissions, while unconfirmed state is retained since the data is still
// owed to the consumer. The ack always carries confirmedSeq+1 so a fresh
// consumer resumes exactly after the last confirmed sequence.
func (x *producerController) handleRegisterConsumer(ctx *ReceiveContext, register *commands.RegisterConsumer) {
	lookup, cancel := context.WithTimeout(ctx.Context(), DefaultRegistrationLookupTimeout)
	defer cancel()

	expected, err := ctx.ActorSystem().resolveReliableCompanion(lookup, x.consumerName, ReliableControllerRoleConsumer)
	if err != nil || !expected.Equals(ctx.Sender()) {
		ctx.Logger().Debugf("producer controller for endpoint=%s dropped registration from unverified sender: %v", x.producer.Name(), err)
		return
	}

	if x.consumerController == nil || !x.consumerController.Equals(ctx.Sender()) || x.registrationNonce != register.Nonce() {
		ctx.Watch(ctx.Sender())
		x.consumerController = ctx.Sender()
		x.registrationNonce = register.Nonce()
		x.demandUpTo = x.currentSeq
	}

	ack, err := commands.NewRegistrationAck(x.sessionID, x.confirmedSeq+1, x.registrationNonce)
	if err != nil {
		// deterministic impossible-value guard: dropping would stall the
		// registration loop forever, so it is terminal
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to build RegistrationAck: %w", err))
		return
	}

	x.tell(ctx, x.consumerController, ack)
}

// handleRequest processes a demand grant from the registered consumer
// controller. The range check is terminal rather than a drop because the
// sender is authenticated for the current session and nonce: an impossible
// range can only mean a contract bug, and continuing could overrun the
// consumer's buffer. demandUpTo is set, not maxed, because requests from one
// registration arrive in mailbox order and the latest request is the
// authoritative window. Resend happens only for ViaTimeout requests; top-up
// requests never resend because those messages are usually already queued at
// the consumer. Credit is evaluated last so new demand immediately turns
// into a RequestNext when the handshake is idle.
func (x *producerController) handleRequest(ctx *ReceiveContext, request *commands.Request) {
	if !x.fromRegisteredConsumer(ctx, request.SessionID(), request.RegistrationNonce()) {
		return
	}

	if request.ConfirmedSeq() < 0 || request.ConfirmedSeq() > x.currentSeq ||
		request.RequestUpToSeq() < request.ConfirmedSeq() ||
		request.RequestUpToSeq() > request.ConfirmedSeq()+MaxFlowControlWindow {
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("illegal demand range [%d, %d] at seq=%d", request.ConfirmedSeq(), request.RequestUpToSeq(), x.currentSeq))
		return
	}

	x.advanceConfirmed(ctx, request.ConfirmedSeq())
	x.demandUpTo = request.RequestUpToSeq()
	x.windowSpan = request.RequestUpToSeq() - request.ConfirmedSeq()

	if request.ViaTimeout() {
		x.resendUnconfirmed(ctx)
	}

	x.allowNextRequest(ctx)
}

// handleAck advances the confirmation watermark without granting demand. The
// consumer sends an Ack instead of a Request when its stream is drained but
// the demand window is still wide, so an idle producer controller does not
// sit on unconfirmed state waiting for the next top-up. Bounds violations
// from the authenticated consumer are terminal for the same reason as in
// handleRequest: a confirmation above currentSeq is impossible by contract.
func (x *producerController) handleAck(ctx *ReceiveContext, ack *commands.Ack) {
	if !x.fromRegisteredConsumer(ctx, ack.SessionID(), ack.RegistrationNonce()) {
		return
	}

	if ack.ConfirmedSeq() < 0 || ack.ConfirmedSeq() > x.currentSeq {
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("illegal confirmation %d at seq=%d", ack.ConfirmedSeq(), x.currentSeq))
		return
	}

	x.advanceConfirmed(ctx, ack.ConfirmedSeq())
}

// handleProduced encodes the offered message once and starts its storage.
// The case ladder matters: duplicates are recognized before violations
// because the tick retries RequestNext, so the producer can legitimately
// answer the same token more than once, and the volatile path advances the
// handshake synchronously, meaning a duplicate can arrive in any later
// phase, including after acceptance (matched via lastCompletedToken). Only
// after both duplicate shapes are excluded is a wrong phase or token a
// genuine contract violation from the bound producer, which is terminal.
// Encoding happens exactly once per message; afterwards only the immutable
// snapshot is retained so the application object becomes collectable before
// the durable round trip.
func (x *producerController) handleProduced(ctx *ReceiveContext, produced *Produced) {
	if !ctx.Sender().Equals(x.producer) {
		ctx.Logger().Debugf("producer controller for endpoint=%s dropped Produced from unexpected sender", x.producer.Name())
		return
	}

	if produced.SessionID() != x.sessionID {
		ctx.Logger().Debugf("producer controller for endpoint=%s dropped Produced from stale session", x.producer.Name())
		return
	}

	switch {
	case x.handshake != producerHandshakeIdle && x.handshake != producerHandshakeCredit &&
		produced.Token() == x.token && produced.MessageID() == x.pendingMessageID:
		// exact duplicate of the in-progress handshake: already encoded
		return
	case produced.Token() == x.lastCompletedToken && produced.MessageID() == x.lastCompletedMessageID:
		// late duplicate of an accepted handshake
		return
	case x.handshake != producerHandshakeCredit:
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("unexpected Produced token=%s", produced.Token()))
		return
	case produced.Token() != x.token:
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("Produced token mismatch: got=%s want=%s", produced.Token(), x.token))
		return
	}

	// encoding is deterministic, so a missing serializer or an encode error
	// is an unregistered payload type: a producer contract violation that
	// neither retry nor restart can fix, and letting the retry loop spin
	// would stall the flow silently
	serializer := ctx.ActorSystem().getRemoting().Serializer(produced.Payload())
	if serializer == nil {
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("no serializer is registered for message=%s payload type %T", produced.MessageID(), produced.Payload()))
		return
	}

	frame, err := serializer.Serialize(produced.Payload())
	if err != nil {
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to encode message=%s: %w", produced.MessageID(), err))
		return
	}

	if x.maxChunkBytes > 0 && len(frame) > x.maxChunkBytes {
		x.handshake = producerHandshakeStore
		x.pendingMessageID = produced.MessageID()
		x.storeChunks(ctx, frame)
		return
	}

	payload, err := newReliablePayload(frame)
	if err != nil {
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("rejected encoded message=%s: %w", produced.MessageID(), err))
		return
	}

	// the application payload reference is released here: only the immutable
	// snapshot survives the handshake
	x.handshake = producerHandshakeStore
	x.pendingMessageID = produced.MessageID()
	x.pendingPayload = payload
	x.startStore(ctx)
}

// storeChunks splits the encoded frame at maxChunkBytes, appends one
// unconfirmed entry per chunk under contiguous sequences, and replies Stored
// for the last chunk so the producer-visible handshake stays one
// Produced/Stored/StoredAck per business message. Chunk payloads alias the
// frame without copying: the entries jointly retain it until the message is
// confirmed and the whole prefix is released at once. Chunking runs only on
// volatile flows, so storage completes synchronously here; the demand-capped
// emission happens after acceptance.
//
// The window bound is terminal rather than retried because it is
// deterministic: the consumer confirms nothing mid-message, so a message
// needing more chunks than its buffer holds could never drain.
func (x *producerController) storeChunks(ctx *ReceiveContext, frame []byte) {
	count := (len(frame) + x.maxChunkBytes - 1) / x.maxChunkBytes

	if int64(count) > x.windowSpan {
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("message=%s needs %d chunks but the consumer window is %d: raise WithFlowControlWindow or the WithChunking size", x.pendingMessageID, count, x.windowSpan))
		return
	}

	if x.currentSeq > math.MaxInt64-int64(count) {
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("sequence space exhausted at seq=%d", x.currentSeq))
		return
	}

	seq := x.currentSeq
	x.pendingChunks = make([]UnconfirmedMessage, 0, count)

	for start := 0; start < len(frame); start += x.maxChunkBytes {
		end := min(start+x.maxChunkBytes, len(frame))

		payload, err := newReliablePayload(frame[start:end])
		if err != nil {
			// dropping would wedge the handshake in the store phase with
			// nothing left to retry it, so an impossible value is terminal
			x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("rejected chunk of message=%s: %w", x.pendingMessageID, err))
			return
		}

		seq++
		entry, err := newChunkUnconfirmedMessage(x.pendingMessageID, seq, payload, start == 0, end == len(frame))
		if err != nil {
			x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to record chunk of message=%s: %w", x.pendingMessageID, err))
			return
		}

		x.pendingChunks = append(x.pendingChunks, entry)
	}

	x.unconfirmed = append(x.unconfirmed, x.pendingChunks...)
	x.currentSeq = seq
	x.pendingSeq = seq
	x.replyStored(ctx)
}

// replyStored acknowledges the pending message's storage toward the producer
// and opens the StoredAck phase. Whole and chunked storage share it: by the
// time it runs, pendingSeq is the sequence Stored must carry, which for a
// chunked message is the last chunk's.
func (x *producerController) replyStored(ctx *ReceiveContext) {
	stored, err := newStoredFromState(x.sessionID, x.token, x.pendingMessageID, x.pendingSeq, x.producer, ctx.Self())
	if err != nil {
		// no retry path covers a store phase without a Stored message, so an
		// impossible value is terminal rather than a wedged handshake
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to build Stored for message=%s: %w", x.pendingMessageID, err))
		return
	}

	x.handshake = producerHandshakeStoredAck
	x.storedMessage = stored
	x.tell(ctx, x.producer, stored)
}

// handleStoredAck records that the producer saw its Stored reply and durably
// removed the offer from any recoverable source, which is the precondition
// for queue-side acceptance and eventual MessageID compaction. The case
// ladder mirrors handleProduced: the expected phase advances the handshake,
// a repeat while acceptance is pending or after completion (lastCompleted
// match) is an idempotent duplicate caused by Stored retransmissions, and
// anything else from the authenticated producer is a terminal contract
// violation.
func (x *producerController) handleStoredAck(ctx *ReceiveContext, ack *StoredAck) {
	if !ctx.Sender().Equals(x.producer) {
		ctx.Logger().Debugf("producer controller for endpoint=%s dropped StoredAck from unexpected sender", x.producer.Name())
		return
	}

	if ack.SessionID() != x.sessionID {
		ctx.Logger().Debugf("producer controller for endpoint=%s dropped StoredAck from stale session", x.producer.Name())
		return
	}

	switch {
	case x.handshake == producerHandshakeStoredAck && ack.Token() == x.token && ack.MessageID() == x.pendingMessageID:
		x.handshake = producerHandshakeAccept
		x.storedMessage = nil
		x.startAccept(ctx)
	case x.handshake == producerHandshakeAccept && ack.Token() == x.token && ack.MessageID() == x.pendingMessageID:
		// duplicate while acceptance is pending
	case ack.Token() == x.lastCompletedToken && ack.MessageID() == x.lastCompletedMessageID:
		// late duplicate of an accepted handshake
	default:
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("unexpected StoredAck token=%s message=%s", ack.Token(), ack.MessageID()))
	}
}

// handleQueueOpResult applies the single pending durable operation outcome.
// Results are data, never task errors, so a completion can never trip the
// supervisor by itself. The triple fence (session, lane occupancy, operation
// ID) drops anything a previous incarnation piped before a restart: acting
// on such a result would commit state the reloaded queue snapshot already
// supersedes. The lane is freed before dispatching so completion handlers
// may immediately launch the next operation through pumpLane.
func (x *producerController) handleQueueOpResult(ctx *ReceiveContext, result *queueOpResult) {
	if result.sessionID != x.sessionID || !x.opInFlight || result.operationID != x.nextOperationID {
		ctx.Logger().Debugf("producer controller for endpoint=%s dropped stale queue result op=%d", x.producer.Name(), result.operationID)
		return
	}

	x.opInFlight = false

	if result.err != nil {
		x.handleQueueFailure(ctx, result)
		return
	}

	switch result.kind {
	case queueOpStore:
		x.completeStore(ctx, result.store)
	case queueOpAccept:
		x.completeAccept(ctx)
	case queueOpConfirm:
		x.persistedConfirmedSeq = max(x.persistedConfirmedSeq, result.confirmSeq)
	}

	x.pumpLane(ctx)
}

// handleQueueFailure classifies a durable operation failure: fencing and
// conflicts are terminal, anything else restarts the controller to reload
// authoritative state.
func (x *producerController) handleQueueFailure(ctx *ReceiveContext, result *queueOpResult) {
	stage, wrap := ReliableDeliveryStageStore, gerrors.ErrReliableStore

	switch result.kind {
	case queueOpAccept:
		stage, wrap = ReliableDeliveryStageAccept, gerrors.ErrReliableAccept
	case queueOpConfirm:
		stage, wrap = ReliableDeliveryStageConfirm, gerrors.ErrReliableConfirm
	}

	if errors.Is(result.err, gerrors.ErrQueueFenced) || errors.Is(result.err, gerrors.ErrQueueConflict) {
		x.terminate(ctx, stage, result.err)
		return
	}

	ctx.Err(fmt.Errorf("%w: %w", wrap, result.err))
}

// handleTick retries the outstanding local handshake message on the
// generation-fenced timer. Only two phases need retransmission because only
// RequestNext and Stored target the producer's possibly bounded user
// mailbox: the store and accept phases wait on the durable lane, whose
// completion arrives as a piped result and needs no timer. Ticks from a
// previous incarnation carry a stale generation and are ignored.
func (x *producerController) handleTick(ctx *ReceiveContext, tick *producerControllerTick) {
	if tick.generation != x.generation {
		return
	}

	switch x.handshake {
	case producerHandshakeCredit:
		x.sendRequestNext(ctx)
	case producerHandshakeStoredAck:
		if x.storedMessage != nil {
			x.tell(ctx, x.producer, x.storedMessage)
		}
	}
}

// handleTerminated reacts to watched-peer deaths. The producer's death stops
// the controller because a producer controller without its endpoint serves
// nothing; the consumer controller's death only clears registration and
// resets demand to currentSeq, retaining durable and handshake state, since
// a replacement consumer controller will re-register and recover the
// unconfirmed backlog through its timeout request.
func (x *producerController) handleTerminated(ctx *ReceiveContext, msg *Terminated) {
	if msg.ActorPath().Equals(x.producer.Path()) {
		ctx.Shutdown()
		return
	}

	if x.consumerController != nil && msg.ActorPath().Equals(x.consumerController.Path()) {
		x.consumerController = nil
		x.registrationNonce = types.EmptyString
		x.demandUpTo = x.currentSeq
	}
}

// fromRegisteredConsumer reports whether a Request or Ack comes from the
// currently registered consumer controller under the current session and
// registration generation. Anything else is untrusted or obsolete traffic
// (a dead incarnation, a superseded registration, or a spoofer) and is
// dropped without state changes, never escalated: the legitimate consumer
// recovers by re-registering, so acting on stale demand would be the only
// way to corrupt flow control.
func (x *producerController) fromRegisteredConsumer(ctx *ReceiveContext, sessionID, nonce string) bool {
	if x.consumerController == nil || !ctx.Sender().Equals(x.consumerController) || sessionID != x.sessionID || nonce != x.registrationNonce {
		ctx.Logger().Debugf("producer controller for endpoint=%s dropped stale consumer traffic", x.producer.Name())
		return false
	}

	return true
}

// allowNextRequest grants the producer permission to submit exactly one
// message by opening a fresh tokenized RequestNext handshake. It runs only
// when the previous handshake fully completed (through acceptance) and the
// consumer's demand window still has room, which is what bounds the number
// of stored-but-unconfirmed messages to the granted demand. The sequence
// guard stops the flow before the sequence space can overflow. Retransmission
// of an already open handshake is sendRequestNext, driven by the tick.
func (x *producerController) allowNextRequest(ctx *ReceiveContext) {
	if x.handshake != producerHandshakeIdle || x.currentSeq >= x.demandUpTo {
		return
	}

	if x.currentSeq >= math.MaxInt64-1 {
		x.terminate(ctx, ReliableDeliveryStageProtocol, errors.New("sequence space exhausted"))
		return
	}

	x.handshake = producerHandshakeCredit
	x.token = uuid.NewString()
	x.sendRequestNext(ctx)
}

// sendRequestNext transmits the currently open RequestNext to the producer,
// reusing its unchanged session and token. The token must stay identical
// across retransmissions so the producer can recognize the retry of a grant
// it already answered and idempotently resend the same Produced instead of
// handing over a second message; a fresh token here would double-consume the
// producer's pending queue. Called once when allowNextRequest opens the
// handshake and again by every tick while the Produced is missing.
func (x *producerController) sendRequestNext(ctx *ReceiveContext) {
	request, err := newRequestNext(x.sessionID, x.token, x.producer, ctx.Self())
	if err != nil {
		// deterministic impossible-value guard: the tick would retry into
		// the same failure forever, so it is terminal
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to build RequestNext: %w", err))
		return
	}

	x.tell(ctx, x.producer, request)
}

// startStore begins storage of the pending encoded message. With a durable
// queue the store runs on the asynchronous lane and proposes currentSeq+1;
// without one, a synthetic StoreResult completes synchronously so both modes
// traverse the identical completeStore path in the same logical order and
// no behavior can diverge between volatile and durable flows.
func (x *producerController) startStore(ctx *ReceiveContext) {
	if x.queue == nil {
		seq := x.currentSeq + 1
		result, err := NewStoreResult(seq, false, x.pendingPayload)
		if err != nil {
			// deterministic impossible-value guard: dropping would wedge the
			// handshake in the store phase, so it is terminal
			x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to build store result for message=%s: %w", x.pendingMessageID, err))
			return
		}

		x.completeStore(ctx, result)
		return
	}

	x.launchOp(ctx, queueOpStore)
}

// completeStore commits volatile state for a new append, replies Stored, and
// awaits the producer acknowledgement. An already-stored MessageID reuses its
// original sequence and authoritative first-write payload.
func (x *producerController) completeStore(ctx *ReceiveContext, result StoreResult) {
	x.pendingSeq = result.Seq()
	x.pendingPayload = result.Payload()

	if !result.AlreadyStored() {
		message, err := NewUnconfirmedMessage(x.pendingMessageID, result.Seq(), result.Payload())
		if err != nil {
			// dropping here would wedge the handshake in the store phase with
			// nothing left to retry it, so an impossible value is terminal
			x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to record unconfirmed message=%s: %w", x.pendingMessageID, err))
			return
		}

		x.currentSeq = result.Seq()
		x.unconfirmed = append(x.unconfirmed, message)
	}

	x.replyStored(ctx)
}

// startAccept begins acceptance of the acknowledged message: the durable
// record that the producer has removed the offer from its recoverable
// source, which is what later permits MessageID compaction in the queue.
// Without a queue it completes synchronously, mirroring startStore.
func (x *producerController) startAccept(ctx *ReceiveContext) {
	if x.queue == nil {
		x.completeAccept(ctx)
		return
	}

	x.launchOp(ctx, queueOpAccept)
}

// completeAccept finishes the handshake after durable acceptance: the
// message is emitted first, the completed token is remembered so late
// duplicate acknowledgements stay idempotent, and only then may the next
// RequestNext open. This ordering is the crash boundary that keeps the
// design safe: if the controller dies between acceptance and emission, the
// unconfirmed entry still holds the message and the consumer's next timeout
// request resends it.
func (x *producerController) completeAccept(ctx *ReceiveContext) {
	if len(x.pendingChunks) > 0 {
		for _, chunk := range x.pendingChunks {
			x.emitSequenced(ctx, chunk.MessageID(), chunk.Seq(), chunk.Payload(), chunk.chunk)
		}
	} else {
		x.emitSequenced(ctx, x.pendingMessageID, x.pendingSeq, x.pendingPayload, chunkMark{})
	}

	x.lastCompletedToken = x.token
	x.lastCompletedMessageID = x.pendingMessageID
	x.resetHandshake()
	x.allowNextRequest(ctx)
}

// resetHandshake clears the local handshake state.
func (x *producerController) resetHandshake() {
	x.handshake = producerHandshakeIdle
	x.token = types.EmptyString
	x.pendingMessageID = types.EmptyString
	x.pendingSeq = 0
	x.pendingPayload = ReliablePayload{}
	x.storedMessage = nil
	x.pendingChunks = nil
}

// emitSequenced sends one stored message or chunk to the registered consumer
// controller, never above the granted demand; a skipped emission is
// recovered by the consumer's timeout request. A mid-message demand boundary
// therefore simply pauses a chunked emission between chunks.
func (x *producerController) emitSequenced(ctx *ReceiveContext, messageID string, seq int64, payload ReliablePayload, chunk chunkMark) {
	if x.consumerController == nil || seq > x.demandUpTo {
		ctx.Logger().Debugf("producer controller for endpoint=%s deferred emission seq=%d demand=%d", x.producer.Name(), seq, x.demandUpTo)
		return
	}

	var message *commands.SequencedMessage
	var err error

	if chunk.chunked {
		message, err = commands.NewChunkedSequencedMessage(x.sessionID, messageID, seq, payload.rawBytes(), chunk.first, chunk.last)
	} else {
		message, err = commands.NewSequencedMessage(x.sessionID, messageID, seq, payload.rawBytes())
	}

	if err != nil {
		// deterministic impossible-value guard: every timeout resend would
		// rebuild and fail identically, so it is terminal
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to build SequencedMessage seq=%d: %w", seq, err))
		return
	}

	x.tell(ctx, x.consumerController, message)
}

// advanceConfirmed applies a cumulative confirmation from the consumer:
// everything at or below the watermark is done, so the volatile prefix is
// released in one cut. Durable persistence of the watermark is coalesced
// rather than written per confirmation: values at or below the persisted
// watermark are ignored, and while the lane is busy only the highest dirty
// value is retained, so duplicate or idle control traffic can never grow
// the queue-operation backlog.
func (x *producerController) advanceConfirmed(ctx *ReceiveContext, confirmed int64) {
	if confirmed <= x.confirmedSeq {
		return
	}

	x.confirmedSeq = confirmed
	cut := 0

	for cut < len(x.unconfirmed) && x.unconfirmed[cut].Seq() <= confirmed {
		cut++
	}

	if cut > 0 {
		x.sendConfirmation(ctx, x.unconfirmed[:cut])
		x.unconfirmed = slices.Delete(x.unconfirmed, 0, cut)
	}

	if x.queue == nil {
		x.persistedConfirmedSeq = confirmed
		return
	}

	if confirmed > x.persistedConfirmedSeq {
		x.dirtyConfirmSeq = max(x.dirtyConfirmSeq, confirmed)
		x.pumpLane(ctx)
	}
}

// sendConfirmation tells the producer that the consumer confirmed each
// message leaving the unconfirmed buffer, in ascending sequence order. It runs
// only for an endpoint spawned with WithDeliveryConfirmation.
//
// The notice is best effort and deliberately not retried: it carries no
// protocol obligation, so a bounded producer mailbox or a controller restart
// may drop it without affecting delivery. A message redelivered after a
// restart is confirmed and reported again, which is why the producer must
// deduplicate by MessageID.
func (x *producerController) sendConfirmation(ctx *ReceiveContext, confirmed []UnconfirmedMessage) {
	if !x.deliveryConfirmation {
		return
	}

	for _, message := range confirmed {
		notice, err := newDeliveryConfirmed(x.sessionID, message.MessageID(), message.Seq(), x.producer, ctx.Self())
		if err != nil {
			ctx.Logger().Debugf("producer controller for endpoint=%s skipped a delivery confirmation: %v", x.producer.Name(), err)
			continue
		}

		x.tell(ctx, x.producer, notice)
	}
}

// resendUnconfirmed re-emits unconfirmed messages in sequence order, capped
// at both currentSeq and the granted demand. It runs only for ViaTimeout
// requests: the consumer explicitly asked for recovery after detecting a
// gap, silence, or a session change. Emitting past the demand cap could
// overflow the consumer's receive buffer, and order matters because the
// consumer only accepts its next expected sequence directly.
func (x *producerController) resendUnconfirmed(ctx *ReceiveContext) {
	limit := min(x.currentSeq, x.demandUpTo)

	for _, message := range x.unconfirmed {
		if message.Seq() > limit {
			return
		}

		x.emitSequenced(ctx, message.MessageID(), message.Seq(), message.Payload(), message.chunk)
	}
}

// pumpLane launches the next durable operation once the serialized lane is
// free. The deferred handshake operation goes first because message progress
// gates producer throughput, while confirmation persistence is amortized
// bookkeeping that can always wait one more turn; the dirty watermark check
// makes Confirm writes latest-value-wins instead of one-per-confirmation.
// At most one operation is ever in flight, which is what lets results be
// validated by a single pending operation ID.
func (x *producerController) pumpLane(ctx *ReceiveContext) {
	if x.queue == nil || x.opInFlight {
		return
	}

	if x.deferredOp != 0 {
		kind := x.deferredOp
		x.deferredOp = 0
		x.launchOp(ctx, kind)
		return
	}

	if x.dirtyConfirmSeq > x.persistedConfirmedSeq {
		x.launchOp(ctx, queueOpConfirm)
	}
}

// launchOp runs one durable operation as an asynchronous task whose retries
// back off outside the mailbox; the outcome arrives as a queueOpResult.
// A busy lane defers a handshake operation until the lane frees.
func (x *producerController) launchOp(ctx *ReceiveContext, kind int) {
	if x.opInFlight {
		x.deferredOp = kind
		return
	}

	x.opInFlight = true
	x.nextOperationID++

	queue, epoch, sessionID, operationID := x.queue, x.epoch, x.sessionID, x.nextOperationID
	attempts, backoff := x.queueRetryAttempts, x.queueRetryBackoff
	messageID, proposedSeq, payload := x.pendingMessageID, x.currentSeq+1, x.pendingPayload
	confirmSeq := x.dirtyConfirmSeq

	if kind == queueOpConfirm {
		x.dirtyConfirmSeq = 0
	}

	ctx.PipeTo(ctx.Self(), func() (any, error) {
		result := &queueOpResult{sessionID: sessionID, operationID: operationID, kind: kind, confirmSeq: confirmSeq}

		result.err = retryQueueOp(attempts, backoff, func(taskCtx context.Context) error {
			var err error

			switch kind {
			case queueOpStore:
				var request StoreRequest
				if request, err = NewStoreRequest(messageID, proposedSeq, payload); err == nil {
					result.store, err = queue.Store(taskCtx, epoch, request)
				}
			case queueOpAccept:
				err = queue.Accept(taskCtx, epoch, messageID)
			case queueOpConfirm:
				err = queue.Confirm(taskCtx, epoch, confirmSeq)
			}

			return err
		})

		return result, nil
	})
}

// retryQueueOp runs a durable operation under the queue retry policy using
// the shared retry library. It executes on the piped task goroutine, never
// on the mailbox, which is why waiting between attempts is safe. Fencing and
// conflict errors abort immediately through retry.Stop: they are
// deterministic ownership and integrity verdicts that retrying cannot
// change. The stashed original preserves errors.Is classification even when
// the verdict lands on the final attempt, where the retrier returns its own
// wrapper instead of the unwrapped cause.
func retryQueueOp(attempts int, backoff time.Duration, op func(ctx context.Context) error) error {
	var terminal error

	retrier := retry.NewRetrier(attempts, backoff, backoff)
	err := retrier.RunContext(context.Background(), func(taskCtx context.Context) error {
		err := op(taskCtx)

		if errors.Is(err, gerrors.ErrQueueFenced) || errors.Is(err, gerrors.ErrQueueConflict) {
			terminal = err
			return retry.Stop(err)
		}

		return err
	})

	if terminal != nil {
		return terminal
	}

	return err
}

// terminate applies the terminal failure classification: publish exactly one
// ReliableDeliveryFailed event and stop the controller, leaving the producer
// endpoint alive. It is reserved for deterministic conditions (contract
// violations, fencing, conflicts, impossible values) that no retry, resend,
// or restart can repair; recovery requires operator action followed by an
// explicit endpoint ReSpawn, which creates a fresh incarnation and, with a
// durable queue, a fresh writer epoch. The failed flag makes the event
// single-shot even if multiple violations arrive before shutdown completes.
func (x *producerController) terminate(ctx *ReceiveContext, stage ReliableDeliveryStage, cause error) {
	if x.failed {
		return
	}

	x.failed = true
	x.publishFailure(stage, cause)
	ctx.Shutdown()
}

// publishFailure emits the terminal failure event on the actor event stream
// through the producer endpoint's stream reference, identifying the disabled
// flow by endpoint name and controller role without exposing the internal
// companion identity. It is also called directly from a restart-time Load
// failure, where no ReceiveContext exists yet.
func (x *producerController) publishFailure(stage ReliableDeliveryStage, cause error) {
	event, err := newReliableDeliveryFailed(x.producer.Name(), ReliableControllerRoleProducer, stage, cause)
	if err != nil || x.producer.eventsStream == nil {
		return
	}

	x.producer.eventsStream.Publish(eventsTopic, event)
}

// tell sends a protocol message and classifies a send failure as transient
// message loss, the condition the protocol is built to absorb: every message
// sent here has a designated retry owner (RequestNext and Stored are resent
// by the tick, RegistrationAck by the consumer's re-registration,
// SequencedMessage by the unconfirmed buffer on the next timeout request).
// It deliberately uses PID.Tell rather than ctx.Tell so a lost outbound leg
// is never recorded as a failure of the correctly processed inbound message,
// which would pull the supervisor into a momentary overload and restart the
// session. The debug log is therefore the only remaining action; its
// recurrence is the observable signal of a persistently unreachable peer.
func (x *producerController) tell(ctx *ReceiveContext, to *PID, message any) {
	if err := ctx.Self().Tell(context.WithoutCancel(ctx.Context()), to, message); err != nil {
		ctx.Logger().Debugf("producer controller for endpoint=%s lost message to %s: %v", x.producer.Name(), to.Name(), err)
	}
}
