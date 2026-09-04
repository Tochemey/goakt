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

	"github.com/google/uuid"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/types"
)

// pendingWork holds one accepted message waiting for a worker with free
// demand. storeSeq is the producer-visible append cursor carried on Stored and
// DeliveryConfirmed; worker sequences are assigned only at dispatch.
type pendingWork struct {
	messageID string
	storeSeq  int64
	payload   ReliablePayload
}

// dispatchedWork is one message assigned to a worker binding under that
// worker's sequence space while still awaiting confirmation.
type dispatchedWork struct {
	messageID string
	workerSeq int64
	storeSeq  int64
	payload   ReliablePayload
}

// bindingWork is one authenticated worker's point-to-point sub-flow:
// its own sequence space, demand window, and unconfirmed list.
type bindingWork struct {
	// endpointName is the worker endpoint identity used as the binding key
	// across controller reincarnations of that endpoint.
	endpointName string
	// controller is the registered consumer companion for this binding.
	controller *PID
	// registrationNonce fences Request/Ack traffic to the current registration.
	registrationNonce string
	// currentSeq is the highest sequence assigned to this worker.
	currentSeq int64
	// confirmedSeq is the highest sequence confirmed by this worker.
	confirmedSeq int64
	// demandUpTo is the highest sequence this worker may still receive.
	demandUpTo int64
	// unconfirmed holds messages dispatched to this worker awaiting confirmation,
	// in ascending worker-sequence order.
	unconfirmed []dispatchedWork
}

// freeDemand returns how many additional sequences this binding can accept.
func (x *bindingWork) freeDemand() int64 {
	if x.demandUpTo <= x.currentSeq {
		return 0
	}

	return x.demandUpTo - x.currentSeq
}

// workPullingProducerController multiplexes a shared pending pool across a
// dynamic set of worker bindings. Each binding reuses the point-to-point wire
// protocol with an independent sequence space; the producer endpoint keeps the
// unchanged RequestNext/Produced/Stored/StoredAck contract.
type workPullingProducerController struct {
	// producer is the bound local producer endpoint.
	producer *PID
	// queue is the optional durable work queue; nil runs volatile.
	queue DurableWorkQueue
	// queueRetryAttempts bounds each durable operation's attempts.
	queueRetryAttempts int
	// queueRetryBackoff is the delay before a durable operation retry.
	queueRetryBackoff time.Duration
	// retryInterval is the RequestNext/Stored retry cadence.
	retryInterval time.Duration
	// deliveryConfirmation reports whether the endpoint asked to be told
	// about each worker confirmation.
	deliveryConfirmation bool

	// sessionID identifies this controller incarnation.
	sessionID string
	// epoch is the durable-queue writer epoch acquired by Load.
	epoch QueueEpoch
	// storeSeq is the highest producer-visible append cursor assigned so far.
	storeSeq int64
	// pending holds accepted messages not yet dispatched to a worker.
	pending []pendingWork
	// bindings maps worker endpoint name to its live registration binding.
	bindings map[string]*bindingWork
	// bindingOrder preserves registration order for round-robin dispatch.
	bindingOrder []string
	// nextWorker indexes the next bindingOrder entry to try for dispatch.
	nextWorker int

	// handshake is the single local handshake phase.
	handshake int
	// token correlates the outstanding RequestNext/Produced/Stored exchange.
	token string
	// pendingMessageID is the message being stored or accepted.
	pendingMessageID string
	// pendingStoreSeq is the append cursor assigned to the pending message.
	pendingStoreSeq int64
	// pendingPayload is the authoritative encoded snapshot of the pending message.
	pendingPayload ReliablePayload
	// storedMessage is resent on the tick until its StoredAck arrives.
	storedMessage *Stored
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
	// dirtyConfirmIDs are MessageIDs awaiting ConfirmMessage, in confirm order.
	dirtyConfirmIDs []string

	// failed marks that the terminal failure event was already published.
	failed bool
	// generation fences the recurring timer across restarts.
	generation uint64
}

// enforce the Actor contract
var _ Actor = (*workPullingProducerController)(nil)

// newWorkPullingProducerController creates the work-pulling producer controller
// bound to the local producer endpoint. A nil queue runs the flow without
// durability. Settings that arrive incomplete are carried through as they are:
// PreStart rejects them, so an invalid configuration fails at controller start
// rather than silently running with substituted values.
func newWorkPullingProducerController(producer *PID, config *reliableProducerConfig, queue DurableWorkQueue) *workPullingProducerController {
	controller := &workPullingProducerController{
		producer:             producer,
		queue:                queue,
		retryInterval:        config.retryInterval,
		deliveryConfirmation: config.deliveryConfirmation,
		bindings:             make(map[string]*bindingWork),
	}

	if retry := config.queueRetry; retry != nil {
		controller.queueRetryAttempts = retry.maxAttempts
		controller.queueRetryBackoff = retry.initialBackoff
	}

	return controller
}

// PreStart resets incarnation state, generates the session, and, with a durable
// work queue, loads accepted unconfirmed messages into the pending pool and
// acquires the writer epoch. Loading may block: PreStart is the framework's
// init phase, not a mailbox turn.
func (x *workPullingProducerController) PreStart(ctx *Context) error {
	if x.producer == nil || !x.producer.IsLocal() {
		return errors.New("work-pulling producer controller requires a bound local producer")
	}

	if x.retryInterval <= 0 {
		return errors.New("work-pulling producer controller requires a positive local retry interval")
	}

	if x.queue != nil && (x.queueRetryAttempts < 1 || x.queueRetryBackoff <= 0) {
		return errors.New("work-pulling producer controller requires positive queue retry settings")
	}

	x.sessionID = uuid.NewString()
	x.epoch = 0
	x.storeSeq = 0
	x.pending = nil
	x.bindings = make(map[string]*bindingWork)
	x.bindingOrder = nil
	x.nextWorker = 0
	x.resetHandshake()
	x.lastCompletedToken = types.EmptyString
	x.lastCompletedMessageID = types.EmptyString
	x.opInFlight = false
	x.nextOperationID = 0
	x.deferredOp = 0
	x.dirtyConfirmIDs = nil
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

		return fmt.Errorf("work-pulling producer controller for endpoint=%s failed to load durable state: %w", x.producer.Name(), err)
	}

	x.epoch = epoch
	x.storeSeq = state.CurrentSeq()
	x.pending = pendingFromWorkQueueState(state)
	return nil
}

// pendingFromWorkQueueState rebuilds the shared pending pool from a reloaded
// durable work-queue snapshot so unconfirmed jobs re-dispatch to current workers.
func pendingFromWorkQueueState(state WorkQueueState) []pendingWork {
	messages := state.Unconfirmed()
	pending := make([]pendingWork, 0, len(messages))

	for _, message := range messages {
		pending = append(pending, pendingWork{
			messageID: message.MessageID(),
			storeSeq:  message.Seq(),
			payload:   message.Payload(),
		})
	}

	return pending
}

// PostStop cancels the recurring timer of this incarnation through its
// derived reference, so no state shared with PostStart is read here.
func (x *workPullingProducerController) PostStop(ctx *Context) error {
	if err := ctx.ActorSystem().CancelSchedule(reliableTickReference(ctx.ActorName(), x.generation)); err != nil {
		ctx.ActorSystem().Logger().Debugf("work-pulling producer controller for endpoint=%s failed to cancel tick: %v", x.producer.Name(), err)
	}

	return nil
}

// Receive processes controller protocol traffic, producer handshakes,
// lifecycle notifications, and timer ticks.
func (x *workPullingProducerController) Receive(ctx *ReceiveContext) {
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
func (x *workPullingProducerController) handlePostStart(ctx *ReceiveContext) {
	ctx.Watch(x.producer)

	reference := reliableTickReference(ctx.Self().Name(), x.generation)
	tick := &producerControllerTick{generation: x.generation}

	if err := ctx.ActorSystem().Schedule(context.WithoutCancel(ctx.Context()), tick, ctx.Self(), x.retryInterval, WithReference(reference)); err != nil {
		ctx.Err(err)
		return
	}
}

// handleRegisterConsumer fences registration to authenticated workers whose
// consumer configuration names this producer. A verified new companion PID
// replaces any prior binding for that worker endpoint, requeues its
// unconfirmed work, and starts a fresh per-worker sequence space, because a
// new controller incarnation adopts the acked NextSeq. The same companion
// re-registering with a fresh nonce, which its silence rule does after every
// quiet tick, only refreshes the nonce fence and keeps the sequence space:
// within one session the worker's controller keeps its delivery state, so a
// reset space would desynchronize its expected sequence from the binding and
// every later demand grant would read as a bounds violation.
func (x *workPullingProducerController) handleRegisterConsumer(ctx *ReceiveContext, register *commands.RegisterConsumer) {
	lookup, cancel := context.WithTimeout(ctx.Context(), DefaultReliableRegistrationLookupTimeout)
	defer cancel()

	companion, endpointName, err := ctx.ActorSystem().authenticateWorkPullingWorker(lookup, ctx.Sender(), x.producer.Name())
	if err != nil {
		ctx.Logger().Debugf("work-pulling producer controller for endpoint=%s dropped registration from unverified sender: %v", x.producer.Name(), err)
		return
	}

	binding := x.bindings[endpointName]

	switch {
	case binding == nil || !binding.controller.Equals(companion):
		if binding != nil {
			x.endBinding(ctx, endpointName, "replaced companion")
		}

		ctx.Watch(companion)
		x.bindings[endpointName] = &bindingWork{
			endpointName:      endpointName,
			controller:        companion,
			registrationNonce: register.Nonce(),
		}
		x.bindingOrder = append(x.bindingOrder, endpointName)
	case binding.registrationNonce != register.Nonce():
		binding.registrationNonce = register.Nonce()
	}

	binding = x.bindings[endpointName]
	ack, err := commands.NewRegistrationAck(x.sessionID, binding.confirmedSeq+1, binding.registrationNonce)
	if err != nil {
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to build RegistrationAck: %w", err))
		return
	}

	x.tell(ctx, binding.controller, ack)
	x.progress(ctx)
}

// handleRequest processes a demand grant from an authenticated worker binding.
// Bounds violations from a verified binding terminate only that binding and
// requeue its unconfirmed work; they never stop the producer controller.
func (x *workPullingProducerController) handleRequest(ctx *ReceiveContext, request *commands.Request) {
	binding := x.bindingFrom(ctx, request.SessionID(), request.RegistrationNonce())
	if binding == nil {
		return
	}

	if request.ConfirmedSeq() < 0 || request.ConfirmedSeq() > binding.currentSeq ||
		request.RequestUpToSeq() < request.ConfirmedSeq() ||
		request.RequestUpToSeq() > request.ConfirmedSeq()+MaxReliableFlowControlWindow {
		ctx.Logger().Warnf("work-pulling producer controller for endpoint=%s ended worker=%s after illegal demand range [%d, %d] at seq=%d", x.producer.Name(), binding.endpointName, request.ConfirmedSeq(), request.RequestUpToSeq(), binding.currentSeq)
		x.endBinding(ctx, binding.endpointName, "illegal demand range")
		x.progress(ctx)
		return
	}

	x.advanceConfirmed(ctx, binding, request.ConfirmedSeq())
	binding.demandUpTo = request.RequestUpToSeq()

	if request.ViaTimeout() {
		x.resendUnconfirmed(ctx, binding)
	}

	x.progress(ctx)
}

// handleAck advances one binding's confirmation watermark without granting
// demand. Bounds violations end only that binding.
func (x *workPullingProducerController) handleAck(ctx *ReceiveContext, ack *commands.Ack) {
	binding := x.bindingFrom(ctx, ack.SessionID(), ack.RegistrationNonce())
	if binding == nil {
		return
	}

	if ack.ConfirmedSeq() < 0 || ack.ConfirmedSeq() > binding.currentSeq {
		ctx.Logger().Warnf("work-pulling producer controller for endpoint=%s ended worker=%s after illegal confirmation %d at seq=%d", x.producer.Name(), binding.endpointName, ack.ConfirmedSeq(), binding.currentSeq)
		x.endBinding(ctx, binding.endpointName, "illegal confirmation")
		x.progress(ctx)
		return
	}

	x.advanceConfirmed(ctx, binding, ack.ConfirmedSeq())
	x.progress(ctx)
}

// handleProduced encodes the offered message once and starts its storage.
// Duplicate recognition mirrors the point-to-point producer controller so
// RequestNext retries stay idempotent.
func (x *workPullingProducerController) handleProduced(ctx *ReceiveContext, produced *Produced) {
	if !ctx.Sender().Equals(x.producer) {
		ctx.Logger().Debugf("work-pulling producer controller for endpoint=%s dropped Produced from unexpected sender", x.producer.Name())
		return
	}

	if produced.SessionID() != x.sessionID {
		ctx.Logger().Debugf("work-pulling producer controller for endpoint=%s dropped Produced from stale session", x.producer.Name())
		return
	}

	switch {
	case x.handshake != producerHandshakeIdle && x.handshake != producerHandshakeCredit &&
		produced.Token() == x.token && produced.MessageID() == x.pendingMessageID:
		return
	case produced.Token() == x.lastCompletedToken && produced.MessageID() == x.lastCompletedMessageID:
		return
	case x.handshake != producerHandshakeCredit:
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("unexpected Produced token=%s", produced.Token()))
		return
	case produced.Token() != x.token:
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("Produced token mismatch: got=%s want=%s", produced.Token(), x.token))
		return
	}

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

	payload, err := newReliablePayload(frame)
	if err != nil {
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("rejected encoded message=%s: %w", produced.MessageID(), err))
		return
	}

	x.handshake = producerHandshakeStore
	x.pendingMessageID = produced.MessageID()
	x.pendingPayload = payload
	x.startStore(ctx)
}

// startStore begins storage of the pending encoded message. With a durable
// work queue the store runs on the asynchronous lane and proposes storeSeq+1;
// without one, a synthetic StoreResult completes synchronously.
func (x *workPullingProducerController) startStore(ctx *ReceiveContext) {
	if x.storeSeq >= math.MaxInt64-1 {
		x.terminate(ctx, ReliableDeliveryStageProtocol, errors.New("store sequence space exhausted"))
		return
	}

	if x.queue == nil {
		x.storeSeq++
		result, err := NewStoreResult(x.storeSeq, false, x.pendingPayload)
		if err != nil {
			x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to build store result for message=%s: %w", x.pendingMessageID, err))
			return
		}

		x.completeStore(ctx, result)
		return
	}

	x.launchOp(ctx, queueOpStore)
}

// completeStore commits the authoritative store sequence and payload, then
// replies Stored. An already-stored MessageID reuses its first-write identity.
func (x *workPullingProducerController) completeStore(ctx *ReceiveContext, result StoreResult) {
	x.pendingStoreSeq = result.Seq()
	x.pendingPayload = result.Payload()

	if result.Seq() > x.storeSeq {
		x.storeSeq = result.Seq()
	}

	x.replyStored(ctx)
}

// replyStored sends Stored for the pending handshake and awaits StoredAck.
func (x *workPullingProducerController) replyStored(ctx *ReceiveContext) {
	stored, err := newStoredFromState(x.sessionID, x.token, x.pendingMessageID, x.pendingStoreSeq, x.producer, ctx.Self())
	if err != nil {
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to build Stored for message=%s: %w", x.pendingMessageID, err))
		return
	}

	x.handshake = producerHandshakeStoredAck
	x.storedMessage = stored
	x.tell(ctx, x.producer, stored)
}

// handleStoredAck records that the producer saw its Stored reply and begins
// acceptance into the pending pool.
func (x *workPullingProducerController) handleStoredAck(ctx *ReceiveContext, ack *StoredAck) {
	if !ctx.Sender().Equals(x.producer) {
		ctx.Logger().Debugf("work-pulling producer controller for endpoint=%s dropped StoredAck from unexpected sender", x.producer.Name())
		return
	}

	if ack.SessionID() != x.sessionID {
		ctx.Logger().Debugf("work-pulling producer controller for endpoint=%s dropped StoredAck from stale session", x.producer.Name())
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

// startAccept begins acceptance of the acknowledged message. Without a queue
// it completes synchronously, mirroring startStore.
func (x *workPullingProducerController) startAccept(ctx *ReceiveContext) {
	if x.queue == nil {
		x.completeAccept(ctx)
		return
	}

	x.launchOp(ctx, queueOpAccept)
}

// completeAccept finishes the handshake after acceptance: the message enters
// the pending pool unless it is already owned (a first-write-wins resubmit),
// then the next RequestNext may open.
func (x *workPullingProducerController) completeAccept(ctx *ReceiveContext) {
	if !x.owns(x.pendingMessageID) {
		x.pending = append(x.pending, pendingWork{
			messageID: x.pendingMessageID,
			storeSeq:  x.pendingStoreSeq,
			payload:   x.pendingPayload,
		})
	}

	x.lastCompletedToken = x.token
	x.lastCompletedMessageID = x.pendingMessageID
	x.resetHandshake()
	x.progress(ctx)
}

// owns reports whether messageID is already in the pending pool or dispatched
// to a worker binding, so an already-stored resubmit does not duplicate it.
func (x *workPullingProducerController) owns(messageID string) bool {
	for _, work := range x.pending {
		if work.messageID == messageID {
			return true
		}
	}

	for _, binding := range x.bindings {
		for _, message := range binding.unconfirmed {
			if message.messageID == messageID {
				return true
			}
		}
	}

	return false
}

// handleTick retries the outstanding local handshake while it remains open.
func (x *workPullingProducerController) handleTick(ctx *ReceiveContext, tick *producerControllerTick) {
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
// the controller; a worker companion's death ends only that binding and
// requeues its unconfirmed work.
func (x *workPullingProducerController) handleTerminated(ctx *ReceiveContext, msg *Terminated) {
	if msg.ActorPath().Equals(x.producer.Path()) {
		ctx.Shutdown()
		return
	}

	for endpointName, binding := range x.bindings {
		if binding.controller != nil && msg.ActorPath().Equals(binding.controller.Path()) {
			x.endBinding(ctx, endpointName, "worker terminated")
			x.progress(ctx)
			return
		}
	}
}

// bindingFrom reports the binding that authenticated the sender under the
// current session and registration nonce. Anything else is dropped.
func (x *workPullingProducerController) bindingFrom(ctx *ReceiveContext, sessionID, nonce string) *bindingWork {
	if sessionID != x.sessionID {
		ctx.Logger().Debugf("work-pulling producer controller for endpoint=%s dropped stale worker traffic", x.producer.Name())
		return nil
	}

	for _, binding := range x.bindings {
		if binding.controller != nil && ctx.Sender().Equals(binding.controller) && binding.registrationNonce == nonce {
			return binding
		}
	}

	ctx.Logger().Debugf("work-pulling producer controller for endpoint=%s dropped stale worker traffic", x.producer.Name())
	return nil
}

// progress dispatches pending work to workers with free demand and opens the
// next RequestNext when aggregate free capacity remains.
func (x *workPullingProducerController) progress(ctx *ReceiveContext) {
	x.dispatchPending(ctx)
	x.allowNextRequest(ctx)
}

// dispatchPending assigns pending messages round-robin among bindings that
// still have free demand.
func (x *workPullingProducerController) dispatchPending(ctx *ReceiveContext) {
	for len(x.pending) > 0 {
		binding := x.nextEligibleBinding()
		if binding == nil {
			return
		}

		work := x.pending[0]
		// zeroing before the re-slice releases the payload reference the
		// sliced-off head would otherwise pin in the backing array
		x.pending[0] = pendingWork{}
		x.pending = x.pending[1:]

		if binding.currentSeq >= math.MaxInt64-1 {
			x.terminate(ctx, ReliableDeliveryStageProtocol, errors.New("worker sequence space exhausted"))
			return
		}

		binding.currentSeq++
		message := dispatchedWork{
			messageID: work.messageID,
			workerSeq: binding.currentSeq,
			storeSeq:  work.storeSeq,
			payload:   work.payload,
		}
		binding.unconfirmed = append(binding.unconfirmed, message)
		x.emitSequenced(ctx, binding, message)
	}
}

// nextEligibleBinding returns the next round-robin binding with free demand,
// or nil when every binding is full or no workers are registered.
func (x *workPullingProducerController) nextEligibleBinding() *bindingWork {
	if len(x.bindingOrder) == 0 {
		return nil
	}

	for examined := 0; examined < len(x.bindingOrder); examined++ {
		if x.nextWorker >= len(x.bindingOrder) {
			x.nextWorker = 0
		}

		endpointName := x.bindingOrder[x.nextWorker]
		x.nextWorker++

		binding := x.bindings[endpointName]
		if binding != nil && binding.freeDemand() > 0 {
			return binding
		}
	}

	return nil
}

// allowNextRequest opens a fresh RequestNext when the handshake is idle and
// aggregate free demand exceeds the pending pool, so accepted work that is
// still waiting for a worker never starves worker capacity.
func (x *workPullingProducerController) allowNextRequest(ctx *ReceiveContext) {
	if x.handshake != producerHandshakeIdle {
		return
	}

	if x.aggregateFreeDemand() <= int64(len(x.pending)) {
		return
	}

	x.handshake = producerHandshakeCredit
	x.token = uuid.NewString()
	x.sendRequestNext(ctx)
}

// aggregateFreeDemand sums free demand across every live binding.
func (x *workPullingProducerController) aggregateFreeDemand() int64 {
	var total int64

	for _, binding := range x.bindings {
		total += binding.freeDemand()
	}

	return total
}

// sendRequestNext transmits the currently open RequestNext to the producer.
func (x *workPullingProducerController) sendRequestNext(ctx *ReceiveContext) {
	request, err := newRequestNext(x.sessionID, x.token, x.producer, ctx.Self())
	if err != nil {
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to build RequestNext: %w", err))
		return
	}

	x.tell(ctx, x.producer, request)
}

// emitSequenced sends one dispatched message to its worker binding.
func (x *workPullingProducerController) emitSequenced(ctx *ReceiveContext, binding *bindingWork, message dispatchedWork) {
	if binding.controller == nil || message.workerSeq > binding.demandUpTo {
		ctx.Logger().Debugf("work-pulling producer controller for endpoint=%s deferred emission to worker=%s seq=%d demand=%d", x.producer.Name(), binding.endpointName, message.workerSeq, binding.demandUpTo)
		return
	}

	sequenced, err := commands.NewSequencedMessage(x.sessionID, message.messageID, message.workerSeq, message.payload.rawBytes())
	if err != nil {
		x.terminate(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to build SequencedMessage seq=%d: %w", message.workerSeq, err))
		return
	}

	x.tell(ctx, binding.controller, sequenced)
}

// resendUnconfirmed re-emits one binding's unconfirmed messages capped at its
// granted demand, used only for ViaTimeout recovery requests.
func (x *workPullingProducerController) resendUnconfirmed(ctx *ReceiveContext, binding *bindingWork) {
	for _, message := range binding.unconfirmed {
		if message.workerSeq > binding.currentSeq || message.workerSeq > binding.demandUpTo {
			return
		}

		x.emitSequenced(ctx, binding, message)
	}
}

// advanceConfirmed applies a cumulative confirmation from one worker binding
// and, with a durable work queue, schedules ConfirmMessage for each completed
// MessageID.
func (x *workPullingProducerController) advanceConfirmed(ctx *ReceiveContext, binding *bindingWork, confirmed int64) {
	if confirmed <= binding.confirmedSeq {
		return
	}

	binding.confirmedSeq = confirmed
	cut := 0

	for cut < len(binding.unconfirmed) && binding.unconfirmed[cut].workerSeq <= confirmed {
		cut++
	}

	if cut == 0 {
		return
	}

	completed := binding.unconfirmed[:cut]
	x.sendConfirmation(ctx, completed)

	if x.queue != nil {
		for _, message := range completed {
			x.dirtyConfirmIDs = append(x.dirtyConfirmIDs, message.messageID)
		}

		x.pumpLane(ctx)
	}

	binding.unconfirmed = slices.Delete(binding.unconfirmed, 0, cut)
}

// sendConfirmation tells the producer that workers confirmed each business
// message, when the endpoint asked for DeliveryConfirmed notices.
func (x *workPullingProducerController) sendConfirmation(ctx *ReceiveContext, confirmed []dispatchedWork) {
	if !x.deliveryConfirmation {
		return
	}

	for _, message := range confirmed {
		notice, err := newDeliveryConfirmed(x.sessionID, message.messageID, message.storeSeq, x.producer, ctx.Self())
		if err != nil {
			ctx.Logger().Debugf("work-pulling producer controller for endpoint=%s skipped a delivery confirmation: %v", x.producer.Name(), err)
			continue
		}

		x.tell(ctx, x.producer, notice)
	}
}

// handleQueueOpResult applies one durable work-queue operation outcome. The
// triple fence (session, lane occupancy, operation ID) drops results from a
// previous incarnation.
func (x *workPullingProducerController) handleQueueOpResult(ctx *ReceiveContext, result *queueOpResult) {
	if result.sessionID != x.sessionID || !x.opInFlight || result.operationID != x.nextOperationID {
		ctx.Logger().Debugf("work-pulling producer controller for endpoint=%s dropped stale queue result op=%d", x.producer.Name(), result.operationID)
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
	case queueOpConfirmMessage:
		// nothing to commit: the confirmed ID already left dirtyConfirmIDs at
		// launch, and pumpLane below drains the queue FIFO one op at a time
	}

	x.pumpLane(ctx)
}

// handleQueueFailure classifies a durable operation failure: fencing and
// conflicts are terminal; anything else restarts the controller to reload.
func (x *workPullingProducerController) handleQueueFailure(ctx *ReceiveContext, result *queueOpResult) {
	stage, wrap := ReliableDeliveryStageStore, gerrors.ErrReliableStore

	switch result.kind {
	case queueOpAccept:
		stage, wrap = ReliableDeliveryStageAccept, gerrors.ErrReliableAccept
	case queueOpConfirmMessage:
		stage, wrap = ReliableDeliveryStageConfirm, gerrors.ErrReliableConfirm
	}

	if errors.Is(result.err, gerrors.ErrQueueFenced) || errors.Is(result.err, gerrors.ErrQueueConflict) {
		x.terminate(ctx, stage, result.err)
		return
	}

	ctx.Err(fmt.Errorf("%w: %w", wrap, result.err))
}

// pumpLane launches the next durable operation when the lane is free.
func (x *workPullingProducerController) pumpLane(ctx *ReceiveContext) {
	if x.queue == nil || x.opInFlight {
		return
	}

	if x.deferredOp != 0 {
		kind := x.deferredOp
		x.deferredOp = 0
		x.launchOp(ctx, kind)
		return
	}

	if len(x.dirtyConfirmIDs) > 0 {
		x.launchOp(ctx, queueOpConfirmMessage)
	}
}

// launchOp runs one durable work-queue operation as an asynchronous task whose
// outcome arrives as a queueOpResult. A busy lane defers a handshake operation.
func (x *workPullingProducerController) launchOp(ctx *ReceiveContext, kind int) {
	if x.opInFlight {
		x.deferredOp = kind
		return
	}

	x.opInFlight = true
	x.nextOperationID++

	queue, epoch, sessionID, operationID := x.queue, x.epoch, x.sessionID, x.nextOperationID
	attempts, backoff := x.queueRetryAttempts, x.queueRetryBackoff
	messageID, proposedSeq, payload := x.pendingMessageID, x.storeSeq+1, x.pendingPayload
	confirmMessageID := types.EmptyString

	if kind == queueOpConfirmMessage {
		confirmMessageID = x.dirtyConfirmIDs[0]
		x.dirtyConfirmIDs[0] = types.EmptyString
		x.dirtyConfirmIDs = x.dirtyConfirmIDs[1:]
	}

	ctx.PipeTo(ctx.Self(), func() (any, error) {
		result := &queueOpResult{sessionID: sessionID, operationID: operationID, kind: kind}

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
			case queueOpConfirmMessage:
				err = queue.ConfirmMessage(taskCtx, epoch, confirmMessageID)
			}

			return err
		})

		return result, nil
	})
}

// endBinding removes a worker binding, unwatches its controller, and returns
// its unconfirmed messages to the head of the pending pool under their
// original MessageIDs and payloads so a surviving worker can receive them.
func (x *workPullingProducerController) endBinding(ctx *ReceiveContext, endpointName, reason string) {
	binding := x.bindings[endpointName]
	if binding == nil {
		return
	}

	ctx.Logger().Debugf("work-pulling producer controller for endpoint=%s ended worker=%s: %s", x.producer.Name(), endpointName, reason)

	if binding.controller != nil {
		ctx.UnWatch(binding.controller)
	}

	if len(binding.unconfirmed) > 0 {
		requeued := make([]pendingWork, 0, len(binding.unconfirmed))

		for _, message := range binding.unconfirmed {
			requeued = append(requeued, pendingWork{
				messageID: message.messageID,
				storeSeq:  message.storeSeq,
				payload:   message.payload,
			})
		}

		x.pending = append(requeued, x.pending...)
	}

	delete(x.bindings, endpointName)
	x.bindingOrder = slices.DeleteFunc(x.bindingOrder, func(name string) bool {
		return name == endpointName
	})

	if x.nextWorker > len(x.bindingOrder) {
		x.nextWorker = 0
	}
}

// resetHandshake clears the local handshake state.
func (x *workPullingProducerController) resetHandshake() {
	x.handshake = producerHandshakeIdle
	x.token = types.EmptyString
	x.pendingMessageID = types.EmptyString
	x.pendingStoreSeq = 0
	x.pendingPayload = ReliablePayload{}
	x.storedMessage = nil
}

// terminate publishes the terminal failure event and stops the controller.
func (x *workPullingProducerController) terminate(ctx *ReceiveContext, stage ReliableDeliveryStage, cause error) {
	if x.failed {
		return
	}

	x.failed = true
	x.publishFailure(stage, cause)
	ctx.Shutdown()
}

// publishFailure emits the terminal failure event on the actor event stream.
func (x *workPullingProducerController) publishFailure(stage ReliableDeliveryStage, cause error) {
	event, err := newReliableDeliveryFailed(x.producer.Name(), ReliableControllerRoleProducer, stage, cause)
	if err != nil || x.producer.getEventsStream() == nil {
		return
	}

	x.producer.getEventsStream().Publish(eventsTopic, event)
}

// tell sends a protocol message and classifies a send failure as transient
// message loss absorbed by the protocol's retry owners.
func (x *workPullingProducerController) tell(ctx *ReceiveContext, to *PID, message any) {
	if err := ctx.Self().Tell(context.WithoutCancel(ctx.Context()), to, message); err != nil {
		ctx.Logger().Debugf("work-pulling producer controller for endpoint=%s lost message to %s: %v", x.producer.Name(), to.Name(), err)
	}
}
