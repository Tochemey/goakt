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
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/types"
)

// consumerControllerTick is the generation-fenced recurring timer message the
// consumer controller sends itself. Ticks from an older controller
// incarnation carry a stale generation and are ignored.
type consumerControllerTick struct {
	// generation identifies the controller incarnation that created the timer.
	generation uint64
}

// consumerController is the endpoint-owned controller running the consumer
// side of reliable delivery: it registers with the producer controller,
// grants demand, keeps the ordered receive buffer, delivers exactly one
// unconfirmed message at a time to the consumer, and batches confirmations.
type consumerController struct {
	// consumer is the bound local consumer endpoint; constructor state that
	// survives restarts.
	consumer *PID
	// producerName is the user-visible name of the peer producer endpoint.
	producerName string
	// window is the flow-control window: demand granted per request and the
	// receive buffer capacity.
	window int
	// resendInterval is the registration, demand, and delivery retry cadence.
	resendInterval time.Duration

	// producerController is the resolved producer controller of the producer endpoint's
	// current incarnation; nil until resolved.
	producerController *PID
	// sessionID is the adopted producer controller session; empty until the
	// first RegistrationAck adoption.
	sessionID string
	// registrationNonce is the nonce of the latest RegisterConsumer sent;
	// only the matching RegistrationAck may be adopted.
	registrationNonce string
	// expectedSeq is the next sequence to hand to the consumer.
	expectedSeq int64
	// confirmedSeq is the highest business-confirmed sequence
	// (= expectedSeq - 1).
	confirmedSeq int64
	// requestUpToSeq is the highest sequence the producer controller may send
	// under the demand granted so far.
	requestUpToSeq int64
	// buffer holds sequenced messages above expectedSeq in ascending seq
	// order, deduplicated by seq, capped at window entries.
	buffer []*commands.SequencedMessage
	// inFlight is the single unconfirmed Delivery handed to the consumer.
	inFlight *Delivery
	// sawValidTraffic records whether a validated producer-controller message
	// arrived since the previous tick.
	sawValidTraffic bool
	// lastGapRequest rate-limits gap-triggered requests to one per
	// resendInterval.
	lastGapRequest time.Time
	// failed marks that the terminal failure event was already published.
	failed bool
	// generation fences the recurring timer across restarts.
	generation uint64
}

// enforce the Actor contract
var _ Actor = (*consumerController)(nil)

// newConsumerController creates the consumer-side controller bound to the
// local consumer endpoint and its peer producer endpoint name.
func newConsumerController(consumer *PID, producerName string, window int, resendInterval time.Duration) *consumerController {
	return &consumerController{
		consumer:       consumer,
		producerName:   producerName,
		window:         window,
		resendInterval: resendInterval,
	}
}

// PreStart validates constructor state and resets all incarnation state. It
// performs no PID operations; watches and timers wait for PostStart.
func (x *consumerController) PreStart(*Context) error {
	if x.consumer == nil || !x.consumer.IsLocal() {
		return errors.New("consumer controller requires a bound local consumer")
	}

	if types.IsBlank(x.producerName) {
		return errors.New("consumer controller requires the producer endpoint name")
	}

	if x.window < 1 || x.window > MaxFlowControlWindow {
		return errors.New("consumer controller requires a valid flow control window")
	}

	if x.resendInterval <= 0 {
		return errors.New("consumer controller requires a positive resend interval")
	}

	x.producerController = nil
	x.sessionID = types.EmptyString
	x.registrationNonce = types.EmptyString
	x.expectedSeq = 1
	x.confirmedSeq = 0
	x.requestUpToSeq = 0
	x.buffer = nil
	x.inFlight = nil
	x.sawValidTraffic = false
	x.lastGapRequest = time.Time{}
	x.failed = false
	x.generation++
	return nil
}

// PostStop cancels the recurring timer of this incarnation through its
// derived reference, so no state shared with PostStart is read here.
func (x *consumerController) PostStop(ctx *Context) error {
	if err := ctx.ActorSystem().CancelSchedule(reliableTickReference(ctx.ActorName(), x.generation)); err != nil {
		ctx.ActorSystem().Logger().Debugf("consumer controller for endpoint=%s failed to cancel tick: %v", x.consumer.Name(), err)
	}

	return nil
}

// Receive processes controller protocol traffic, consumer confirmations,
// lifecycle notifications, and timer ticks.
func (x *consumerController) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
		x.handlePostStart(ctx)
	case *commands.RegistrationAck:
		x.handleRegistrationAck(ctx, msg)
	case *commands.SequencedMessage:
		x.handleSequencedMessage(ctx, msg)
	case *Confirmed:
		x.handleConfirmed(ctx, msg)
	case *consumerControllerTick:
		x.handleTick(ctx, msg)
	case *Terminated:
		x.handleTerminated(ctx, msg)
	default:
		ctx.Unhandled()
	}
}

// handlePostStart watches the consumer, attempts a first registration, and
// creates the generation-fenced recurring tick.
func (x *consumerController) handlePostStart(ctx *ReceiveContext) {
	ctx.Watch(x.consumer)
	x.register(ctx)

	reference := reliableTickReference(ctx.Self().Name(), x.generation)
	tick := &consumerControllerTick{generation: x.generation}

	if err := ctx.ActorSystem().Schedule(context.WithoutCancel(ctx.Context()), tick, ctx.Self(), x.resendInterval, WithReference(reference)); err != nil {
		// the tick is the controller's only recovery mechanism: without it
		// nothing re-registers, retries, or resends, so escalate to the
		// supervisor instead of running without liveness
		ctx.Err(err)
		return
	}
}

// handleRegistrationAck applies the nonce and session-adoption rules: only
// the response to the most recent registration may be adopted; a new session
// resets delivery state; the current session reasserts demand.
func (x *consumerController) handleRegistrationAck(ctx *ReceiveContext, ack *commands.RegistrationAck) {
	if x.producerController == nil || !ctx.Sender().Equals(x.producerController) {
		ctx.Logger().Debugf("consumer controller for endpoint=%s dropped RegistrationAck from unexpected sender", x.consumer.Name())
		return
	}

	if ack.Nonce() != x.registrationNonce {
		ctx.Logger().Debugf("consumer controller for endpoint=%s dropped RegistrationAck with stale nonce", x.consumer.Name())
		return
	}

	x.sawValidTraffic = true

	if ack.SessionID() != x.sessionID {
		x.sessionID = ack.SessionID()
		x.expectedSeq = ack.NextSeq()
		x.confirmedSeq = ack.NextSeq() - 1
		x.buffer = nil
		x.inFlight = nil
	}

	x.sendRequest(ctx, true)
}

// handleSequencedMessage routes one sequenced message after sender, session,
// and range validation. The case order encodes the protocol rules: an
// out-of-window sequence is dropped because it can only be stale or beyond
// granted demand and the producer still retains it unconfirmed; a sequence
// below expectedSeq is a duplicate whose re-ack heals the producer's lost
// confirmation state; the expected sequence is delivered only when nothing
// is in flight; a repeat of the in-flight sequence is dropped because the
// tick already retries it; everything else buffers for ordered drain. Every
// drop is safe because the producer resends unconfirmed messages on the
// next timeout request.
func (x *consumerController) handleSequencedMessage(ctx *ReceiveContext, msg *commands.SequencedMessage) {
	if x.producerController == nil || !ctx.Sender().Equals(x.producerController) {
		ctx.Logger().Debugf("consumer controller for endpoint=%s dropped SequencedMessage from unexpected sender", x.consumer.Name())
		return
	}

	if x.sessionID == types.EmptyString || msg.SessionID() != x.sessionID {
		ctx.Logger().Debugf("consumer controller for endpoint=%s dropped SequencedMessage from stale session", x.consumer.Name())
		return
	}

	x.sawValidTraffic = true
	seq := msg.Seq()

	switch {
	case seq < 1 || seq > x.requestUpToSeq:
		ctx.Logger().Debugf("consumer controller for endpoint=%s dropped SequencedMessage seq=%d outside window", x.consumer.Name(), seq)
	case seq < x.expectedSeq:
		x.sendAck(ctx)
	case seq == x.expectedSeq && x.inFlight == nil:
		x.deliver(ctx, msg)
	case x.inFlight != nil && seq == x.inFlight.Seq():
		// already handed to the consumer; the tick retries it
	default:
		x.bufferMessage(ctx, msg)
	}
}

// handleConfirmed advances past a business-confirmed delivery. Confirmation
// must match the in-flight MessageID, session, and sequence exactly: a
// mismatch is a stale application reply (for example from a previous
// session), dropped rather than treated as a violation because consumers
// legitimately confirm late. The fixed order (advance, purge, batch, drain)
// matters: batching runs before drain so the drained-stream rule observes
// the truth, and drain hands over the next contiguous message only after
// the watermark moved.
func (x *consumerController) handleConfirmed(ctx *ReceiveContext, confirmed *Confirmed) {
	if !ctx.Sender().Equals(x.consumer) {
		ctx.Logger().Debugf("consumer controller for endpoint=%s dropped Confirmed from unexpected sender", x.consumer.Name())
		return
	}

	if x.inFlight == nil ||
		confirmed.SessionID() != x.sessionID ||
		confirmed.MessageID() != x.inFlight.MessageID() ||
		confirmed.Seq() != x.inFlight.Seq() {
		ctx.Logger().Debugf("consumer controller for endpoint=%s dropped stale Confirmed", x.consumer.Name())
		return
	}

	x.confirmedSeq = x.inFlight.Seq()
	x.expectedSeq = x.confirmedSeq + 1
	x.inFlight = nil
	x.purgeBuffer()
	x.batchConfirmation(ctx)
	x.drain(ctx)
}

// handleTick applies exactly one recovery rule per generation-valid tick,
// in priority order. Silence (no validated producer-controller message
// since the previous tick) triggers re-registration with a fresh nonce
// because it covers every producer-side failure at once: a lost
// registration, a controller restart, a relocation, or plain idleness; the
// matching ack always reasserts demand. Otherwise the unconfirmed delivery
// is re-told, deliberately permitting duplicate business processing, or an
// open gap requests a resend. Exactly one rule per tick keeps recovery
// traffic bounded by the tick cadence.
func (x *consumerController) handleTick(ctx *ReceiveContext, tick *consumerControllerTick) {
	if tick.generation != x.generation {
		return
	}

	switch {
	case x.sessionID == types.EmptyString || !x.sawValidTraffic:
		x.register(ctx)
	case x.inFlight != nil:
		x.tell(ctx, x.consumer, x.inFlight)
	case x.gapOpen():
		x.sendGapRequest(ctx)
	}

	x.sawValidTraffic = false
}

// handleTerminated stops the controller with its consumer and clears the
// resolved producer controller when that peer dies.
func (x *consumerController) handleTerminated(ctx *ReceiveContext, msg *Terminated) {
	if msg.ActorPath().Equals(x.consumer.Path()) {
		ctx.Shutdown()
		return
	}

	if x.producerController != nil && msg.ActorPath().Equals(x.producerController.Path()) {
		x.producerController = nil
		x.sessionID = types.EmptyString
		x.registrationNonce = types.EmptyString
	}
}

// register resolves the producer endpoint's current controller and sends a
// RegisterConsumer with a fresh nonce. The lookup is bounded by
// DefaultRegistrationLookupTimeout so a slow cluster registry cannot stall
// this mailbox. Failures are tolerated; the next tick retries.
func (x *consumerController) register(ctx *ReceiveContext) {
	lookup, cancel := context.WithTimeout(ctx.Context(), DefaultRegistrationLookupTimeout)
	defer cancel()

	pc, err := ctx.ActorSystem().resolveReliableCompanion(lookup, x.producerName, ReliableControllerRoleProducer)
	if err != nil {
		ctx.Logger().Debugf("consumer controller for endpoint=%s cannot resolve producer=%s: %v", x.consumer.Name(), x.producerName, err)
		return
	}

	nonce := uuid.NewString()
	register, err := commands.NewRegisterConsumer(nonce)
	if err != nil {
		// deterministic impossible-value guard: every tick would rebuild and
		// fail identically, so it is terminal
		x.fail(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to build RegisterConsumer: %w", err))
		return
	}

	if x.producerController == nil || !x.producerController.Equals(pc) {
		// fast-path liveness only: a missed watch is recovered by the
		// silence rule on the next tick
		ctx.Watch(pc)
	}

	x.producerController = pc
	x.registrationNonce = nonce
	x.tell(ctx, pc, register)
}

// deliver decodes a fresh payload value and hands the single in-flight
// Delivery to the consumer. Exactly one Delivery is ever unconfirmed at a
// time, which is what gives the no-fault path its in-order,
// effectively-once presentation. Decoding is deterministic, so a decode
// failure means a serializer registration asymmetry between the producer
// and consumer nodes: no resend can repair it, and it is terminal rather
// than a silent retry stall.
func (x *consumerController) deliver(ctx *ReceiveContext, msg *commands.SequencedMessage) {
	payload, err := ctx.ActorSystem().getRemoting().Serializer(nil).Deserialize(msg.Payload())
	if err != nil {
		// decoding is deterministic, so this is a serializer registration
		// asymmetry between producer and consumer: no resend can fix it, and
		// letting the retry loop spin would stall the flow silently
		x.fail(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to decode message=%s seq=%d: %w", msg.MessageID(), msg.Seq(), err))
		return
	}

	delivery, err := newDelivery(msg.SessionID(), msg.MessageID(), msg.Seq(), payload, x.consumer, ctx.Self())
	if err != nil {
		// deterministic impossible-value guard: every resend would rebuild
		// and fail identically, so it is terminal
		x.fail(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to build Delivery seq=%d: %w", msg.Seq(), err))
		return
	}

	x.inFlight = delivery
	x.tell(ctx, x.consumer, delivery)
}

// bufferMessage inserts a sequenced message above expectedSeq into the
// ordered, seq-deduplicated receive buffer. A full buffer keeps its existing
// lower sequences and drops the arriving one; timeout resend recovers it.
func (x *consumerController) bufferMessage(ctx *ReceiveContext, msg *commands.SequencedMessage) {
	index, found := slices.BinarySearchFunc(x.buffer, msg.Seq(), func(entry *commands.SequencedMessage, seq int64) int {
		return cmp.Compare(entry.Seq(), seq)
	})

	switch {
	case found:
		// duplicate of a buffered sequence
	case len(x.buffer) >= x.window:
		ctx.Logger().Debugf("consumer controller for endpoint=%s dropped seq=%d: receive buffer full", x.consumer.Name(), msg.Seq())
	default:
		x.buffer = slices.Insert(x.buffer, index, msg)
	}

	if x.gapOpen() {
		x.sendGapRequest(ctx)
	}
}

// drain hands the next contiguous buffered message to the consumer, if any.
func (x *consumerController) drain(ctx *ReceiveContext) {
	if x.inFlight != nil || len(x.buffer) == 0 || x.buffer[0].Seq() != x.expectedSeq {
		return
	}

	head := x.buffer[0]
	x.buffer = slices.Delete(x.buffer, 0, 1)
	x.deliver(ctx, head)
}

// purgeBuffer removes buffered entries below expectedSeq.
func (x *consumerController) purgeBuffer() {
	cut := 0

	for cut < len(x.buffer) && x.buffer[cut].Seq() < x.expectedSeq {
		cut++
	}

	if cut > 0 {
		x.buffer = slices.Delete(x.buffer, 0, cut)
	}
}

// gapOpen reports whether a sequence before the buffered head is missing:
// the buffer is non-empty and its head is not the next sequence the
// controller can hand over.
func (x *consumerController) gapOpen() bool {
	if len(x.buffer) == 0 {
		return false
	}

	nextMissing := x.expectedSeq
	if x.inFlight != nil {
		nextMissing = x.inFlight.Seq() + 1
	}

	return x.buffer[0].Seq() > nextMissing
}

// batchConfirmation applies exactly one of the confirmation rules, in
// order: top up demand once half the window is consumed (the Request
// carries the confirmation for free), acknowledge immediately when the
// stream is drained so an idle producer controller does not sit on
// unconfirmed state, or defer knowing a later confirmation will hit one of
// the first two rules. This is why there is no per-message remote Ack: a
// saturated stream settles into one top-up per half window and an idle
// stream ends with a single Ack.
func (x *consumerController) batchConfirmation(ctx *ReceiveContext) {
	switch {
	case x.requestUpToSeq-x.confirmedSeq <= int64(x.window/2):
		x.sendRequest(ctx, false)
	case len(x.buffer) == 0 && x.inFlight == nil:
		x.sendAck(ctx)
	}
}

// sendRequest grants a fresh demand window of confirmedSeq+window carrying
// the current confirmation watermark, and records the grant optimistically
// even if the send is lost, because the producer only honors demand it
// actually received and a lost request surfaces as silence on a later tick.
// Timeout requests additionally ask the producer controller to resend
// unconfirmed messages; ordinary top-ups never do, since those messages are
// usually already buffered here.
func (x *consumerController) sendRequest(ctx *ReceiveContext, viaTimeout bool) {
	if x.producerController == nil || x.sessionID == types.EmptyString {
		return
	}

	upTo := x.confirmedSeq + int64(x.window)
	request, err := commands.NewRequest(x.sessionID, x.registrationNonce, x.confirmedSeq, upTo, viaTimeout)
	if err != nil {
		// deterministic impossible-value guard: demand could never be
		// granted again, so it is terminal
		x.fail(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to build Request: %w", err))
		return
	}

	x.requestUpToSeq = upTo
	x.tell(ctx, x.producerController, request)
}

// sendGapRequest sends a timeout Request for an open gap, rate-limited to
// one per resendInterval: under reordering every buffered arrival above the
// missing sequence would otherwise trigger its own resend request, turning
// one lost message into a request storm. The tick provides the fallback
// cadence when the rate limit suppresses the fast path.
func (x *consumerController) sendGapRequest(ctx *ReceiveContext) {
	now := time.Now()
	if now.Sub(x.lastGapRequest) < x.resendInterval {
		return
	}

	x.lastGapRequest = now
	x.sendRequest(ctx, true)
}

// sendAck reports the confirmation watermark without granting new demand.
// It exists for the drained-stream case: without it an idle producer
// controller would retain confirmed messages until the next top-up Request,
// which might be far away when traffic stops below the half-window
// threshold.
func (x *consumerController) sendAck(ctx *ReceiveContext) {
	if x.producerController == nil || x.sessionID == types.EmptyString {
		return
	}

	ack, err := commands.NewAck(x.sessionID, x.registrationNonce, x.confirmedSeq)
	if err != nil {
		// deterministic impossible-value guard: the confirmation watermark
		// could never be reported again, so it is terminal
		x.fail(ctx, ReliableDeliveryStageProtocol, fmt.Errorf("failed to build Ack: %w", err))
		return
	}

	x.tell(ctx, x.producerController, ack)
}

// fail publishes exactly one terminal ReliableDeliveryFailed event and stops
// the controller, leaving the consumer endpoint alive. It is reserved for
// deterministic conditions that no retry, resend, or restart can repair;
// recovery requires operator action followed by an explicit endpoint ReSpawn.
func (x *consumerController) fail(ctx *ReceiveContext, stage ReliableDeliveryStage, cause error) {
	if x.failed {
		return
	}

	x.failed = true

	if event, err := newReliableDeliveryFailed(x.consumer.Name(), ReliableControllerRoleConsumer, stage, cause); err == nil && x.consumer.eventsStream != nil {
		x.consumer.eventsStream.Publish(eventsTopic, event)
	}

	ctx.Shutdown()
}

// tell sends a protocol message and classifies a send failure as transient
// message loss, the condition the protocol is built to absorb: every message
// sent here has a designated retry owner (RegisterConsumer and the in-flight
// Delivery are resent by the tick, Request and Ack are reasserted by the
// registration and confirmation loops). It deliberately uses PID.Tell rather
// than ctx.Tell so a lost outbound leg is never recorded as a failure of the
// correctly processed inbound message, which would pull the supervisor into
// a momentary overload and restart the controller. The debug log is
// therefore the only remaining action.
func (x *consumerController) tell(ctx *ReceiveContext, to *PID, message any) {
	if err := ctx.Self().Tell(context.WithoutCancel(ctx.Context()), to, message); err != nil {
		ctx.Logger().Debugf("consumer controller for endpoint=%s lost message to %s: %v", x.consumer.Name(), to.Name(), err)
	}
}
