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
	"runtime"
	"sync"
	syncatomic "sync/atomic"
	"time"

	"github.com/flowchartsman/retry"
	"github.com/google/uuid"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/types/known/durationpb"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/reentrancy"
)

// grainPhase is where a grain process stands in its activation lifecycle,
// recorded under mu. It decides how a timer scheduled at that moment is
// treated: dormant while activating, live while active, rejected otherwise.
type grainPhase uint8

const (
	// grainInactive covers a process outside an activation: never activated,
	// deactivating or deactivated.
	grainInactive grainPhase = iota
	// grainActivating spans OnActivate: timers scheduled now stay dormant until
	// activation completes.
	grainActivating
	// grainActive spans a completed activation: timers scheduled now start at
	// once.
	grainActive
)

type grainPID struct {
	// Line 0: the fields the processing turn writes per message (mailbox head,
	// activity stamp, processed count, touch coalescing) beside what only the
	// turn reads per message (grain, passivation manager). responses is read
	// off the message path by async response delivery. No producer touches
	// this line per message.
	//
	// mailboxHead is the consumer end of the embedded user mailbox, the retained
	// sentinel; the turn advances it on every dequeue and no producer reads it
	// per message, so it heads the turn-written line. Nil for a bounded grain.
	mailboxHead syncatomic.Pointer[GrainContext]

	// latestReceiveTimeNano holds the latest receive timestamp as
	// UnixNano. Stored as int64 because atomic.Time boxes time.Time
	// into an atomic.Value on every Store, which would allocate on
	// the hot path.
	latestReceiveTimeNano atomic.Int64
	processedCount        atomic.Int64

	// lastPassivationTouch is the UnixNano of the last passivation Touch
	// call, used to coalesce Touch to at most once per
	// passivationTouchInterval. Without it every message takes the shared
	// passivation manager's mutex, which profiling showed serializes
	// otherwise independent grains on the drain path.
	lastPassivationTouch atomic.Int64

	grain Grain

	// responses queues async response envelopes apart from the user mailbox, so
	// completions stay reachable while the mailbox is paused. Nil until the
	// grain has a reentrancy state: attached once by newGrainPID for a
	// configured policy or by enableReentrancy at runtime, never removed.
	// Atomic because the runtime attach runs on the turn while envelope
	// delivery and the reclaim check read it off-turn.
	responses atomic.Pointer[grainMailbox]

	passivationManager *passivationManager

	// Line 1: read by producers and the turn on every message, written only off
	// the message path (construction, activation, poisoning, timer scheduling),
	// so it stays shared in every core's cache.
	//
	// boundedMailbox is the standalone mailbox of a grain configured with a
	// capacity, nil otherwise; the nil check is how the message path picks the
	// embedded queue.
	boundedMailbox *grainMailbox
	identity       *GrainIdentity

	// dispatcher is the shared worker pool that runs grain turns. Set at
	// construction from the owning actor system; nil only in unit tests
	// that drive runTurn directly.
	dispatcher *dispatcher

	// reentrancy tracks this activation's in-flight requests; nil until the
	// grain is configured with reentrancy, at activation or at runtime through
	// EnableReentrancy. An atomic pointer because the runtime install happens
	// on the processing turn while off-turn readers (the envelope-ask gate,
	// envelope delivery, shutdown) observe it; it transitions nil to non-nil
	// at most once and is never removed, disabling only flips the state's
	// default mode to Off.
	reentrancy atomic.Pointer[reentrancyState]

	activated syncatomic.Bool

	// ctxShard is this grain's home shard in grainContextPool. Every
	// GrainContext built for this grain is taken from and returned to that
	// shard, so unrelated grains never contend on pool state. Assigned
	// round-robin at construction.
	ctxShard uint32

	onPoisonPill atomic.Bool
	activatedAt  atomic.Int64

	// mu guards timers and phase. It is taken at activation transitions and by
	// timer scheduling, never on the message path.
	mu sync.Mutex

	// Line 2: the producer-written mailbox tail and the scheduling word both
	// sides compare-and-swap, with fields touched only at activation,
	// deactivation or wire snapshots; the turn writes here once per turn, never
	// per message.
	//
	// mailboxTail is the producer end of the embedded user mailbox: every
	// enqueue swaps it, the turn reads it only on an empty dequeue, so it heads
	// the producer-written line. Nil for a bounded grain.
	mailboxTail syncatomic.Pointer[GrainContext]

	// schedState drives the grain's membership on the dispatcher's ready
	// queue. The Idle -> Scheduled -> Processing transitions enforce that
	// at most one worker drains the mailbox at a time, replacing the old
	// per-burst goroutine while preserving the single-threaded execution
	// invariant per grain.
	schedState dispatchState

	// phase is the activation lifecycle point, guarded by mu. It sits in the
	// padding after schedState so it costs no bytes.
	phase grainPhase

	// the actor system
	actorSystem ActorSystem

	config *grainConfig

	// timers holds the registry of the current activation, created by the first
	// schedule call of that activation and dropped when the activation ends or
	// fails; nil otherwise. Guarded by mu.
	timers *grainTimers

	// Pads the process to 192 bytes, three whole cache lines, so the allocator
	// places every process on a line boundary and the line comments above hold
	// for all of them; at 176 bytes only one process in four was aligned.
	_ [16]byte
}

var (
	_ passivationParticipant = (*grainPID)(nil)
	_ schedulable            = (*grainPID)(nil)
	_ asyncErrorSink         = (*grainPID)(nil)
)

// grainPassivationPill carries the passivation manager's deactivation decision
// onto the grain's serialized turn stream, where it cannot race an on-turn
// request registration.
type grainPassivationPill struct{}

func newGrainPID(identity *GrainIdentity, grain Grain, actorSystem ActorSystem, config *grainConfig) *grainPID {
	pid := &grainPID{
		grain:                 grain,
		identity:              identity,
		actorSystem:           actorSystem,
		dispatcher:            actorSystem.getDispatcher(),
		latestReceiveTimeNano: atomic.Int64{},
		config:                config,
		passivationManager:    actorSystem.passivationManager(),
		ctxShard:              nextGrainContextShard(),
	}

	pid.attachMailbox(config.capacity)
	pid.activated.Store(false)
	pid.onPoisonPill.Store(false)
	pid.processedCount.Store(0)
	pid.activatedAt.Store(0)

	// A policy whose mode is Off behaves exactly like no policy; skipping the
	// state keeps the legacy paths bit for bit until reentrancy is enabled at
	// runtime.
	if config.reentrancy != nil && config.reentrancy.Mode() != reentrancy.Off {
		pid.attachResponseQueue()
		pid.reentrancy.Store(newReentrancyState(config.reentrancy.Mode(), config.reentrancy.MaxInFlight()))
	}

	return pid
}

// attachMailbox gives the process its user mailbox: a standalone bounded
// mailbox for a positive capacity, otherwise the embedded unbounded queue
// seeded with its sentinel.
func (pid *grainPID) attachMailbox(capacity int64) {
	if capacity > 0 {
		pid.boundedMailbox = newGrainMailbox(capacity)
		return
	}

	sentinel := new(GrainContext)
	pid.mailboxHead.Store(sentinel)
	pid.mailboxTail.Store(sentinel)
}

// getLogger returns the actor system's logger. The system's logger is set at
// construction and never changes, so the process looks it up on the cold
// paths that log instead of carrying its own copy.
func (pid *grainPID) getLogger() log.Logger {
	return pid.actorSystem.getLogger()
}

// activate activates the Grain
func (pid *grainPID) activate(ctx context.Context) (err error) {
	logger := pid.getLogger()
	if logger.Enabled(log.DebugLevel) {
		logger.Debugf("grain=%s activating", pid.identity.String())
	}

	// Timers scheduled from OnActivate are created dormant and started once
	// activation completes.
	pid.mu.Lock()
	pid.phase = grainActivating
	pid.mu.Unlock()

	// Registered before the recover defer so it runs after err is materialized:
	// on any activation failure the registry is dropped and timers scheduled by
	// the failed OnActivate can never fire.
	defer func() {
		if err != nil {
			pid.stopTimers()
		}
	}()

	retries := pid.config.initMaxRetries
	timeout := pid.config.initTimeout

	cctx, cancel := context.WithTimeout(ctx, timeout)
	retrier := retry.NewRetrier(int(retries), timeout, timeout)

	defer func() {
		if r := recover(); r != nil {
			cancel()
			switch v := r.(type) {
			case error:
				if pe, ok := errors.AsType[*gerrors.PanicError](v); ok {
					err = gerrors.NewErrGrainActivationFailure(pe)
					return
				}

				pc, fn, line, _ := runtime.Caller(2)
				err = gerrors.NewErrGrainActivationFailure(
					gerrors.NewPanicError(
						fmt.Errorf("%w at %s[%s:%d]", v, runtime.FuncForPC(pc).Name(), fn, line),
					),
				)
			default:
				pc, fn, line, _ := runtime.Caller(2)
				err = gerrors.NewErrGrainActivationFailure(
					gerrors.NewPanicError(
						fmt.Errorf("%#v at %s[%s:%d]", r, runtime.FuncForPC(pc).Name(), fn, line),
					),
				)
			}
		}
	}()

	if err := retrier.RunContext(cctx, func(ctx context.Context) error {
		return pid.grain.OnActivate(ctx, newGrainProps(pid.identity, pid.actorSystem, pid.config.dependencyValues(), pid))
	}); err != nil {
		cancel()
		if logger.Enabled(log.ErrorLevel) {
			logger.Errorf("grain=%s activation failed (hint: check OnActivate implementation)", pid.identity.String())
		}
		return gerrors.NewErrGrainActivationFailure(err)
	}

	pid.activated.Store(true)
	pid.activatedAt.Store(time.Now().Unix())
	if logger.Enabled(log.DebugLevel) {
		logger.Debugf("grain=%s activated successfully", pid.identity.String())
	}
	cancel()

	pid.markActivity(time.Now())

	if pid.shouldAutoPassivate() {
		pid.startPassivation()
	}

	pid.startTimers()

	return nil
}

// deactivate deactivates the Grain
func (pid *grainPID) deactivate(ctx context.Context) (err error) {
	logger := pid.getLogger()

	defer func() {
		if r := recover(); r != nil {
			switch v := r.(type) {
			case error:
				if pe, ok := errors.AsType[*gerrors.PanicError](v); ok {
					err = gerrors.NewErrGrainDeactivationFailure(pe)
					return
				}

				pc, fn, line, _ := runtime.Caller(2)
				err = gerrors.NewErrGrainDeactivationFailure(
					gerrors.NewPanicError(
						fmt.Errorf("%w at %s[%s:%d]", v, runtime.FuncForPC(pc).Name(), fn, line),
					),
				)
			default:
				pc, fn, line, _ := runtime.Caller(2)
				err = gerrors.NewErrGrainDeactivationFailure(
					gerrors.NewPanicError(
						fmt.Errorf("%#v at %s[%s:%d]", r, runtime.FuncForPC(pc).Name(), fn, line),
					),
				)
			}
		}
	}()

	pid.unregisterPassivation()

	// Dropped before OnDeactivate runs: every timer is cancelled, a tick already
	// sitting in the mailbox is dropped by the tick handler, and the phase moves
	// back to inactive so a late schedule call from the hook is rejected instead
	// of creating a registry.
	pid.stopTimers()

	defer func() {
		pid.activated.Store(false)
		pid.activatedAt.Store(0)
		pid.latestReceiveTimeNano.Store(0)
		pid.onPoisonPill.Store(false)
	}()

	if logger.Enabled(log.DebugLevel) {
		logger.Debugf("grain=%s deactivating", pid.identity.String())
	}

	if err := pid.grain.OnDeactivate(ctx, newGrainProps(pid.identity, pid.actorSystem, pid.config.dependencyValues(), pid)); err != nil {
		if logger.Enabled(log.ErrorLevel) {
			logger.Errorf("grain=%s deactivation failed (hint: check OnDeactivate implementation)", pid.identity.String())
		}
		return gerrors.NewErrGrainDeactivationFailure(err)
	}

	actorSystem := pid.actorSystem
	identity := pid.getIdentity()

	actorSystem.getGrains().Delete(identity.String())
	if actorSystem.InCluster() {
		// release the record only while it still names this node: a record
		// re-owned by another node belongs to a live activation there
		node := address.FormatHostPort(actorSystem.Host(), actorSystem.Port())
		if _, err := actorSystem.getCluster().ReleaseGrain(ctx, pid.identity.String(), node); err != nil {
			if logger.Enabled(log.ErrorLevel) {
				logger.Errorf("failed to release grain=%s from the cluster registry: %v (hint: check cluster connectivity)", pid.identity.String(), err)
			}
			return gerrors.NewErrGrainDeactivationFailure(err)
		}
	}

	if logger.Enabled(log.DebugLevel) {
		logger.Debugf("grain=%s deactivated successfully", pid.identity.String())
	}
	return nil
}

// isActive returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (pid *grainPID) isActive() bool {
	return pid != nil && pid.activated.Load()
}

// receive pushes a given message to the grain mailbox and schedules the
// grain on the dispatcher pool. Idempotent in scheduling: concurrent
// producers race on the Idle -> Scheduled CAS and only the winner pushes
// onto the ready queue. The losers' messages are still drained because
// the winner's turn observes them via the FIFO mailbox.
func (pid *grainPID) receive(grainContext *GrainContext) {
	if !pid.isActive() {
		return
	}

	if err := pid.enqueueMessage(grainContext); err != nil {
		grainContext.Err(err)
		return
	}

	if pid.schedState.TrySchedule() {
		pid.dispatcher.schedule(pid)
	}
}

// enqueueMessage appends a user message to the grain's mailbox: the standalone
// bounded one when the grain has a capacity, otherwise the embedded queue,
// which never rejects.
func (pid *grainPID) enqueueMessage(grainContext *GrainContext) error {
	if pid.boundedMailbox != nil {
		return pid.boundedMailbox.Enqueue(grainContext)
	}

	(*embeddedGrainMailbox)(pid).Enqueue(grainContext)
	return nil
}

// dequeueMessage pops the next user message, or nil when the mailbox is empty.
// Consumer turn only.
func (pid *grainPID) dequeueMessage() *GrainContext {
	if pid.boundedMailbox != nil {
		return pid.boundedMailbox.Dequeue()
	}

	return (*embeddedGrainMailbox)(pid).Dequeue()
}

// mailboxEmpty reports whether the user mailbox holds no message. A racy
// snapshot, meant for the turn's reclaim check.
func (pid *grainPID) mailboxEmpty() bool {
	if pid.boundedMailbox != nil {
		return pid.boundedMailbox.IsEmpty()
	}

	return (*embeddedGrainMailbox)(pid).IsEmpty()
}

// runTurn implements schedulable. A dispatcher worker calls this after
// pulling the grain off the ready queue. The method takes exclusive
// ownership via the Scheduled -> Processing CAS, drains up to the
// dispatcher's throughput budget, and then either yields back to
// Scheduled (re-pushing onto the worker's local queue) or transitions to
// Idle with a race-safe reclaim if a concurrent enqueue slipped in.
//
// Cooperative scheduling replaces the per-burst goroutine model: a
// worker drains the budget then rotates to a sibling, which caps the
// blocking window any one grain can impose on its peers and amortises
// scheduling cost across a batch of messages.
func (pid *grainPID) runTurn(w *worker) {
	if !pid.schedState.TakeForProcessing() {
		return
	}

	now := time.Now()
	budget := w.dispatcher.throughput
	for range budget {
		grainContext := pid.dequeueResponse()

		if grainContext == nil && !pid.paused() {
			grainContext = pid.dequeueMessage()
		}

		if grainContext == nil {
			if pid.finishOrReclaim() {
				return
			}
			continue
		}
		pid.dispatchOne(grainContext, now)
	}
	pid.schedState.YieldToScheduled()
	w.reschedule(pid)
}

// dequeueResponse pops the next async response envelope, or nil when the
// grain has no response queue or the queue is empty. Responses outrank user
// messages so a paused grain can always reach its completions.
func (pid *grainPID) dequeueResponse() *GrainContext {
	responses := pid.responses.Load()
	if responses == nil {
		return nil
	}

	return responses.Dequeue()
}

// reentrantEnabled reports whether the grain takes the envelope ask path. A
// config whose mode is Off, whether configured that way or disabled at
// runtime, behaves exactly like no config at all: the legacy channel path
// stays bit for bit.
func (pid *grainPID) reentrantEnabled() bool {
	reentrant := pid.reentrancy.Load()
	return reentrant != nil && reentrant.getMode() != reentrancy.Off
}

// paused reports whether the grain has stopped consuming its user mailbox
// because a StashNonReentrant request is in flight. Buffered messages wait in
// place in arrival order; only response envelopes keep flowing, which is what
// lets the pause end. Re-evaluated on every turn iteration so a request
// registered mid-turn pauses immediately and the last completion resumes
// within the same budget.
func (pid *grainPID) paused() bool {
	reentrant := pid.reentrancy.Load()
	return reentrant != nil && reentrant.blockingCount.Load() > 0
}

// hasPendingWork reports whether the grain still has processable input. While
// paused, buffered user messages deliberately do not count: they are
// unreachable until the last blocking request completes, so counting them
// would make the reclaim path spin across workers for the whole pause.
func (pid *grainPID) hasPendingWork() bool {
	if responses := pid.responses.Load(); responses != nil && !responses.IsEmpty() {
		return true
	}

	return !pid.paused() && !pid.mailboxEmpty()
}

// dispatchOne routes a single message through the appropriate handler.
// Release is owned by the mailbox Dequeue, standalone or embedded,
// which reclaims the previous sentinel; dispatchOne must not return
// the context here or the mailbox would hand out an in-use head.
//
// now is the turn's shared timestamp, computed once per turn by runTurn:
// activity stamping does not need per-message precision, and time.Now on
// every message showed up in the drain-path profile.
func (pid *grainPID) dispatchOne(grainContext *GrainContext, now time.Time) {
	switch grainContext.Message().(type) {
	case *PoisonPill:
		pid.handlePoisonPill(grainContext)
	case *grainTimerTick:
		pid.handleTimerTick(grainContext, now)
	case grainPassivationPill:
		pid.handlePassivationPill()
	case *commands.AsyncRequest:
		pid.handleAsyncRequest(grainContext, now)
	case *commands.AsyncResponse:
		pid.handleAsyncResponse(grainContext, now)
	default:
		pid.handleGrainContext(grainContext, now)
	}
}

// handleAsyncRequest unwraps an async request envelope and dispatches the
// inner message through the normal receive flow. The correlation ID and the
// reply target ride on the context so the reply can be routed from that
// metadata; nothing is signalled through the context itself. now is the
// turn's shared activity timestamp.
func (pid *grainPID) handleAsyncRequest(grainContext *GrainContext, now time.Time) {
	request := grainContext.Message().(*commands.AsyncRequest)

	if request.CorrelationID == "" || request.Message == nil || (request.ReplyTo != nil && !request.ReplyTo.Valid()) {
		if pid.getLogger().Enabled(log.WarningLevel) {
			pid.getLogger().Warnf("grain=%s dropping malformed async request envelope", pid.getIdentity().String())
		}
		return
	}

	grainContext.requestID = request.CorrelationID
	grainContext.requestReplyTo = request.ReplyTo
	grainContext.message = request.Message
	pid.handleGrainContext(grainContext, now)
}

// handleAsyncResponse completes the in-flight request matching the response's
// correlation ID and runs its continuation inline: the turn is the grain's
// processing thread, so single-threaded access to grain state holds. An
// unknown correlation ID is a normal race with timeout or cancellation and is
// dropped quietly. now is the turn's shared activity timestamp.
func (pid *grainPID) handleAsyncResponse(grainContext *GrainContext, now time.Time) {
	defer pid.recovery(grainContext)

	response := grainContext.Message().(*commands.AsyncResponse)
	pid.markActivity(now)

	// A response without a payload and without an error is a successful reply
	// with nothing to return (NoErr).
	var completed bool
	if response.Error != "" {
		completed = pid.completeRequest(response.CorrelationID, nil, asyncErrorFromString(response.Error))
	} else {
		completed = pid.completeRequest(response.CorrelationID, response.Message, nil)
	}

	if !completed && pid.getLogger().Enabled(log.DebugLevel) {
		pid.getLogger().Debugf("grain=%s async response dropped: no in-flight request for correlation id=%s", pid.getIdentity().String(), response.CorrelationID)
	}
}

// admitRequest resolves the effective reentrancy mode of a grain-issued
// request, creates its state and admits it against the grain's limits. The
// caller starts the timeout after delivery succeeds, so a failed delivery
// never races a timeout completion.
func (pid *grainPID) admitRequest(reentrant *reentrancyState, opts ...RequestOption) (*requestState, *requestConfig, error) {
	config := newRequestConfig(opts...)

	mode := reentrant.getMode()
	if config.modeSet {
		mode = config.mode
	}

	if mode == reentrancy.Off {
		return nil, nil, gerrors.ErrReentrancyDisabled
	}

	if !reentrancy.IsValidReentrancyMode(mode) {
		return nil, nil, gerrors.ErrInvalidReentrancyMode
	}

	state := newRequestState(uuid.NewString(), mode, pid)
	if err := pid.registerRequestState(state); err != nil {
		return nil, nil, err
	}

	pid.markActivity(time.Now())
	return state, config, nil
}

// enableReentrancy installs or retunes the grain's async request policy at
// runtime. In-flight requests keep the mode they were admitted with. The queue
// is attached before the state is installed, the same order as newGrainPID, so
// it exists by the time reentrantEnabled reports true. An invalid config
// leaves an idle queue attached, one mailbox on an error path.
func (pid *grainPID) enableReentrancy(config *reentrancy.Reentrancy) error {
	pid.attachResponseQueue()
	return installReentrancy(&pid.reentrancy, config)
}

// attachResponseQueue gives the grain its dedicated queue for async response
// envelopes, once. Only grains with a reentrancy state get one: newGrainPID
// attaches it for a configured policy and enableReentrancy for a runtime
// install, so a grain that never enables reentrancy never pays for it. The
// CompareAndSwap keeps a concurrent attach safe; a loser's spare queue is
// left to the collector.
func (pid *grainPID) attachResponseQueue() {
	if pid.responses.Load() != nil {
		return
	}

	pid.responses.CompareAndSwap(nil, newGrainMailbox(0))
}

// disableReentrancy turns off async requests without disturbing in-flight
// ones: they complete normally, a paused grain still unpauses, and new
// Request calls are rejected until a later enableReentrancy. The envelope ask
// path reverts to the legacy channel path as well. A no-op when reentrancy
// was never enabled.
func (pid *grainPID) disableReentrancy() {
	if reentrant := pid.reentrancy.Load(); reentrant != nil {
		reentrant.disable()
	}
}

// registerRequestState admits an in-flight request against the grain's
// limits, mirroring the actor-side admission. A StashNonReentrant
// registration increments blockingCount, which pauses user-mailbox
// consumption from the next turn-loop iteration.
func (pid *grainPID) registerRequestState(state *requestState) error {
	reentrant := pid.reentrancy.Load()
	if reentrant == nil {
		return gerrors.ErrReentrancyDisabled
	}

	if state == nil {
		return gerrors.ErrInvalidMessage
	}

	var first bool

	if maxInFlight := reentrant.maxInFlight.Load(); maxInFlight > 0 {
		for {
			current := reentrant.inFlightCount.Load()
			if current >= maxInFlight {
				return gerrors.ErrReentrancyInFlightLimit
			}

			if reentrant.inFlightCount.CompareAndSwap(current, current+1) {
				first = current == 0
				break
			}
		}
	} else {
		first = reentrant.inFlightCount.Inc() == 1
	}

	if state.mode == reentrancy.StashNonReentrant {
		reentrant.blockingCount.Inc()
	}

	reentrant.requestStates.Set(state.id, state)

	// The manager must not deactivate a grain awaiting a response: pause it
	// while requests are in flight. Registration happens on-turn, so this
	// cannot race the resume of the last completion.
	if first && pid.passivationManager != nil {
		pid.passivationManager.Pause(pid)
	}
	return nil
}

// completeRequest marks an in-flight request as completed and runs its
// continuation inline on the current turn. Only the first completion wins; a
// duplicate reports true without re-running anything. False means no request
// with that correlation ID is in flight.
func (pid *grainPID) completeRequest(correlationID string, result any, err error) bool {
	reentrant := pid.reentrancy.Load()
	if reentrant == nil {
		return false
	}

	state, ok := reentrant.requestStates.Get(correlationID)
	if !ok {
		return false
	}

	callback, completed := state.complete(result, err)
	if !completed {
		return true
	}

	pid.deregisterRequestState(state)
	if callback != nil {
		callback(result, err)
	}

	return true
}

// deregisterRequestState removes an in-flight request and releases its
// resources. Unlike the actor counterpart there is no unstash step: paused
// consumption resumes by itself once blockingCount drops to zero, because the
// turn loop re-evaluates paused() on every iteration and the buffered
// messages never left the user mailbox.
func (pid *grainPID) deregisterRequestState(state *requestState) {
	reentrant := pid.reentrancy.Load()
	if reentrant == nil || state == nil {
		return
	}

	if _, ok := reentrant.requestStates.Get(state.id); !ok {
		return
	}

	reentrant.requestStates.Delete(state.id)
	remaining := reentrant.inFlightCount.Dec()

	if state.mode == reentrancy.StashNonReentrant {
		reentrant.blockingCount.Dec()
	}

	state.stopTimeoutIfSet()

	if remaining == 0 {
		pid.resumePassivation()
	}
}

// resumePassivation re-enters the passivation lifecycle after the last
// in-flight request completes. Resume reports false when the manager entry no
// longer exists, deleted when a passivation pill fired while requests were in
// flight; registering fresh restores the idle clock.
func (pid *grainPID) resumePassivation() {
	if pid.passivationManager == nil {
		return
	}

	if !pid.passivationManager.Resume(pid) {
		pid.startPassivation()
	}
}

// enqueueAsyncError satisfies asyncErrorSink. Timeouts and cancellations
// originate off the grain's turn, so the error travels through the response
// queue like any other completion: the wakeup bundled with the enqueue is what
// lets a paused idle grain get scheduled, process the completion and unpause
// instead of staying parked with a frozen mailbox.
func (pid *grainPID) enqueueAsyncError(ctx context.Context, correlationID string, err error) error {
	if correlationID == "" {
		return gerrors.ErrInvalidMessage
	}

	if err == nil {
		return nil
	}

	response := &commands.AsyncResponse{
		CorrelationID: correlationID,
		Error:         err.Error(),
	}

	return pid.enqueueEnvelope(ctx, response)
}

// enqueueEnvelope delivers an async envelope into the grain's queues:
// requests ride the user mailbox with ordinary messages, responses take the
// dedicated queue that stays reachable while the grain is paused. Every
// successful enqueue is followed by the TrySchedule/schedule wakeup pair,
// mirroring receive; errors are returned, never signalled on a context.
func (pid *grainPID) enqueueEnvelope(ctx context.Context, envelope any) error {
	if !pid.isActive() {
		return gerrors.ErrDead
	}

	var responses *grainMailbox
	switch envelope.(type) {
	case *commands.AsyncRequest:
		// a request rides the user mailbox with ordinary messages
	case *commands.AsyncResponse:
		responses = pid.responses.Load()
		if responses == nil {
			return gerrors.ErrReentrancyDisabled
		}
	default:
		return gerrors.ErrInvalidMessage
	}

	grainContext := getGrainContext(pid.ctxShard)
	grainContext.build(context.WithoutCancel(ctx), pid, pid.actorSystem, pid.getIdentity(), envelope, grainEnvelope)

	var err error
	if responses != nil {
		err = responses.Enqueue(grainContext)
	} else {
		err = pid.enqueueMessage(grainContext)
	}

	if err != nil {
		releaseGrainContext(grainContext)
		return err
	}

	if pid.schedState.TrySchedule() {
		pid.dispatcher.schedule(pid)
	}

	return nil
}

// teardownInFlightRequests completes every in-flight request inline with
// ErrRequestCanceled, then resets the counters and the state map. It runs on
// the deactivating turn immediately before deactivate: queued completions
// would either process after OnDeactivate against a dead grain or be dropped
// as unknown correlations after reset, and a pid whose OnDeactivate failed
// and is later reactivated must not inherit a non-zero blockingCount, which
// would be a permanently paused activation.
func (pid *grainPID) teardownInFlightRequests() {
	reentrant := pid.reentrancy.Load()
	if reentrant == nil {
		return
	}

	for _, state := range reentrant.requestStates.Values() {
		state.stopTimeoutIfSet()

		if callback, completed := state.complete(nil, gerrors.ErrRequestCanceled); completed && callback != nil {
			pid.runTeardownCallback(callback)
		}
	}

	reentrant.reset()
}

// runTeardownCallback shields deactivation from a panicking continuation: the
// panic is contained and logged so the pill that triggered the teardown still
// reaches deactivate and OnDeactivate runs.
func (pid *grainPID) runTeardownCallback(callback func(any, error)) {
	defer func() {
		if r := recover(); r != nil && pid.getLogger().Enabled(log.ErrorLevel) {
			pid.getLogger().Errorf("grain=%s continuation panicked during teardown: %v", pid.getIdentity().String(), r)
		}
	}()

	callback(nil, gerrors.ErrRequestCanceled)
}

// enqueueInFlightCancellations requests cancellation of every in-flight
// request from off the grain's turn. Each cancellation is queue-routed
// through the response queue, so it wakes a paused grain, drops blockingCount
// on-turn and lets a PoisonPill waiting in the user mailbox proceed;
// completing the states directly here would zero the counters without a
// wakeup and skip the continuations. Shutdown calls this before poisoning the
// grain.
func (pid *grainPID) enqueueInFlightCancellations() {
	reentrant := pid.reentrancy.Load()
	if reentrant == nil {
		return
	}

	for _, state := range reentrant.requestStates.Values() {
		if err := state.cancel(); err != nil && pid.getLogger().Enabled(log.DebugLevel) {
			pid.getLogger().Debugf("grain=%s failed to cancel in-flight request id=%s: %v", pid.getIdentity().String(), state.id, err)
		}
	}
}

// finishOrReclaim attempts the Processing -> Idle transition. Returns
// true when the caller must exit the turn (no work remains and ownership
// is fully released). Returns false when a concurrent enqueue raced the
// transition, ownership was reclaimed, and the caller must continue
// draining within the same budget.
func (pid *grainPID) finishOrReclaim() bool {
	pid.schedState.reset()
	if !pid.hasPendingWork() {
		return true
	}

	if !pid.schedState.TrySchedule() {
		return true
	}
	return !pid.schedState.TakeForProcessing()
}

func (pid *grainPID) handlePoisonPill(grainContext *GrainContext) {
	pid.onPoisonPill.Store(true)
	defer pid.recovery(grainContext)

	// A passivation pill and a PoisonPill can both be queued (the manager
	// fires, then shutdown poisons); deactivate has no activated-guard of its
	// own and OnDeactivate must run exactly once.
	if !pid.isActive() {
		grainContext.NoErr()
		return
	}

	pid.teardownInFlightRequests()

	if err := pid.deactivate(grainContext.Context()); err != nil {
		grainContext.Err(err)
		return
	}
	grainContext.NoErr()
}

// handleGrainContext runs a user message through OnReceive. now is the
// turn's shared activity timestamp.
func (pid *grainPID) handleGrainContext(grainContext *GrainContext, now time.Time) {
	defer pid.recovery(grainContext)
	pid.processedCount.Inc()
	pid.markActivity(now)
	pid.grain.OnReceive(grainContext)
}

// deliverTimerTick hands a due timer tick to the grain mailbox as a regular
// message. It runs on the timer goroutine, so it stays cheap and non-blocking.
// The registry re-armed the timer before delivering, so a tick dropped here,
// because the grain is deactivating or the mailbox is full, only loses that one
// tick; interval and cron timers keep firing.
func (pid *grainPID) deliverTimerTick(entry *grainTimerEntry) {
	if !pid.isActive() {
		return
	}

	grainContext := getGrainContext(pid.ctxShard)
	grainContext.build(context.Background(), pid, pid.actorSystem, pid.getIdentity(), entry.tick, grainTell)

	if err := pid.enqueueMessage(grainContext); err != nil {
		releaseGrainContext(grainContext)
		if pid.getLogger().Enabled(log.WarningLevel) {
			pid.getLogger().Warnf("grain=%s dropping tick of timer=%s: %v", pid.getIdentity().String(), entry.reference, err)
		}
		return
	}

	if pid.schedState.TrySchedule() {
		pid.dispatcher.schedule(pid)
	}
}

// handleTimerTick processes a timer tick envelope inside a dispatcher turn,
// serialized with every other message of the grain. A tick whose timer was
// cancelled, or that arrives while the grain deactivates, is dropped: grain
// timers are activation-scoped and must never outlive their activation. A tick
// counts as passivation activity only when its timer was registered with
// WithTimerKeepAlive. now is the turn's shared activity timestamp.
func (pid *grainPID) handleTimerTick(grainContext *GrainContext, now time.Time) {
	tick := grainContext.Message().(*grainTimerTick)
	entry := tick.entry

	if !pid.isActive() || entry.cancelled.Load() {
		return
	}

	pid.processedCount.Inc()
	if entry.keepAlive {
		pid.markActivity(now)
	}

	pid.runTimerTick(grainContext, entry.message)
	pid.reportTimerTickFailure(grainContext, entry)
}

// runTimerTick invokes OnReceive with the timer's message in place of the tick
// envelope, converting panics into an error on the context like any other turn.
func (pid *grainPID) runTimerTick(grainContext *GrainContext, message any) {
	defer pid.recovery(grainContext)
	grainContext.message = message
	pid.grain.OnReceive(grainContext)
}

// reportTimerTickFailure surfaces an error reported while handling a timer tick.
// Ticks are fire-and-forget: no caller waits on the context's error channel, so
// without this drain a failure would vanish silently.
func (pid *grainPID) reportTimerTickFailure(grainContext *GrainContext, entry *grainTimerEntry) {
	select {
	case err := <-grainContext.err:
		if err != nil && pid.getLogger().Enabled(log.WarningLevel) {
			pid.getLogger().Warnf("grain=%s failed to handle tick of timer=%s: %v", pid.getIdentity().String(), entry.reference, err)
		}
	default:
	}
}

// recovery is called upon after message is processed
func (pid *grainPID) recovery(received *GrainContext) {
	r := recover()
	if r == nil {
		return
	}

	var failure error
	switch err, ok := r.(error); {
	case ok:
		if pe, ok := errors.AsType[*gerrors.PanicError](err); ok {
			failure = pe
			break
		}

		// this is a normal error just wrap it with some stack trace
		// for rich logging purpose
		pc, fn, line, _ := runtime.Caller(2)
		failure = gerrors.NewPanicError(
			fmt.Errorf("%w at %s[%s:%d]", err, runtime.FuncForPC(pc).Name(), fn, line),
		)

	default:
		// we have no idea what panic it is. Enrich it with some stack trace for rich
		// logging purpose
		pc, fn, line, _ := runtime.Caller(2)
		failure = gerrors.NewPanicError(
			fmt.Errorf("%#v at %s[%s:%d]", r, runtime.FuncForPC(pc).Name(), fn, line),
		)
	}

	// A response envelope has no reply route: the log is the only signal
	// anyone gets. Everything else reports through Err, which routes an
	// async reply (request envelopes), the ask reply channel (synchronous
	// contexts, whose err channel is nil by construction), or the Tell ack
	// channel.
	if received.err == nil && received.requestID == "" && !received.synchronous {
		if pid.getLogger().Enabled(log.ErrorLevel) {
			pid.getLogger().Errorf("grain=%s panicked while handling %T: %v", pid.getIdentity().String(), received.Message(), failure)
		}
		return
	}

	received.Err(failure)
}

// uptime returns the number of seconds since the grain has been active
func (pid *grainPID) uptime() int64 {
	if pid.isActive() {
		return time.Now().Unix() - pid.activatedAt.Load()
	}
	return 0
}

// getGrain returns the Grain instance. The field is set at construction and
// never reassigned, so the read needs no lock.
func (pid *grainPID) getGrain() Grain {
	return pid.grain
}

// timerRegistry returns the registry of the current activation for the public
// scheduling API, creating it on the activation's first schedule call: dormant
// while the grain is activating, started once it is active. Outside an
// activation, or without a process, it reports ErrGrainTimersStopped so
// GrainProps built without a process and hooks running after deactivation
// began reject scheduling instead of panicking.
func (pid *grainPID) timerRegistry() (*grainTimers, error) {
	if pid == nil {
		return nil, gerrors.ErrGrainTimersStopped
	}

	pid.mu.Lock()
	phase := pid.phase
	timers := pid.timers

	if timers == nil && phase != grainInactive {
		timers = newGrainTimers(pid)
		pid.timers = timers
	}

	pid.mu.Unlock()

	if timers == nil {
		return nil, gerrors.ErrGrainTimersStopped
	}

	// Idempotent: a registry created while activating is started by
	// startTimers, one created afterwards starts here.
	if phase == grainActive {
		timers.start()
	}

	return timers, nil
}

// startTimers completes the activation for the timers: the phase becomes
// active and a registry created dormant during OnActivate starts.
func (pid *grainPID) startTimers() {
	pid.mu.Lock()
	pid.phase = grainActive
	timers := pid.timers
	pid.mu.Unlock()

	if timers != nil {
		timers.start()
	}
}

// stopTimers ends the activation for the timers: the phase falls back to
// inactive and the registry, if the activation created one, is stopped and
// dropped so the next activation starts without timers.
func (pid *grainPID) stopTimers() {
	pid.mu.Lock()
	pid.phase = grainInactive
	timers := pid.timers
	pid.timers = nil
	pid.mu.Unlock()

	if timers != nil {
		timers.stop()
	}
}

// getTimers returns the current activation's timer registry, or nil when the
// grain has never been activated.
func (pid *grainPID) getTimers() *grainTimers {
	pid.mu.Lock()
	timers := pid.timers
	pid.mu.Unlock()
	return timers
}

// getIdentity returns the GrainIdentity of the Grain. The field is set at
// construction and never reassigned, so the read needs no lock.
func (pid *grainPID) getIdentity() *GrainIdentity {
	return pid.identity
}

func (pid *grainPID) passivationID() string {
	if pid.identity == nil {
		return ""
	}
	return pid.identity.String()
}

func (pid *grainPID) passivationLatestActivity() time.Time {
	nanos := pid.latestReceiveTimeNano.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

func (pid *grainPID) passivationTry(reason string) bool {
	if !pid.isActive() || pid.onPoisonPill.Load() {
		return false
	}

	// A reentrancy-capable grain serializes the deactivation decision with its
	// turn: deciding here, on the manager goroutine, is check-then-act against
	// a concurrently running turn that can register a request after the check.
	// The pill travels through the mailbox so the decision and request
	// registration execute on the same serialized turn stream.
	if pid.reentrancy.Load() != nil {
		return pid.enqueuePassivationPill()
	}

	if pid.getLogger().Enabled(log.DebugLevel) {
		pid.getLogger().Debugf("grain=%s reason=%s passivation triggered", pid.identity.String(), reason)
	}

	if err := pid.deactivate(context.Background()); err != nil {
		if pid.getLogger().Enabled(log.ErrorLevel) {
			pid.getLogger().Errorf("failed to passivate grain=%s: %v (hint: check OnPassivate implementation)", pid.identity.String(), err)
		}
		return false
	}
	return true
}

// enqueuePassivationPill hands the deactivation decision to the grain's turn
// stream. Returning true makes the manager delete its entry: ownership of any
// re-registration transfers to the pill handler and the completion path.
// On a full bounded mailbox it touches activity and returns false, so the
// manager's refreshed deadline lands a full deactivateAfter in the future
// instead of hot-looping; a full mailbox means pending traffic, so the touch
// is approximately honest.
func (pid *grainPID) enqueuePassivationPill() bool {
	grainContext := getGrainContext(pid.ctxShard)
	grainContext.build(context.Background(), pid, pid.actorSystem, pid.getIdentity(), grainPassivationPill{}, grainEnvelope)

	if err := pid.enqueueMessage(grainContext); err != nil {
		releaseGrainContext(grainContext)
		pid.markActivity(time.Now())
		return false
	}

	if pid.schedState.TrySchedule() {
		pid.dispatcher.schedule(pid)
	}
	return true
}

// enqueuePoisonPill hands a PoisonPill to the grain's turn stream and returns
// the channel its acknowledgment arrives on, the same Tell ack channel every
// grainTell context carries. Unlike receive it does not check that the grain
// is active: handlePoisonPill acknowledges an already deactivated grain
// itself, so the caller always gets an answer, and a rejected enqueue (full
// bounded mailbox) is acknowledged with its error right away. Shutdown uses
// it to wait for OnDeactivate without a per-grain channel. The caller returns
// the channel to its shard with putGrainErrorChannel once the ack arrived,
// and abandons it when it stopped waiting.
func (pid *grainPID) enqueuePoisonPill(ctx context.Context) chan error {
	grainContext := getGrainContext(pid.ctxShard)
	grainContext.build(ctx, pid, pid.actorSystem, pid.getIdentity(), new(PoisonPill), grainTell)

	// Read before the handoff: once enqueued the context belongs to the turn,
	// which resets it on dequeue and may rebuild it for another message, so
	// grainContext.err is not safe to touch afterwards.
	ack := grainContext.err

	if err := pid.enqueueMessage(grainContext); err != nil {
		grainContext.Err(err)
		releaseGrainContext(grainContext)
		return ack
	}

	if pid.schedState.TrySchedule() {
		pid.dispatcher.schedule(pid)
	}

	return ack
}

// handlePassivationPill decides deactivation on the grain's turn. The pill may
// be stale by the time it processes (delayed behind a pause or buffered
// traffic), so every condition is re-checked against current state:
//   - inactive or poisoning: drop the pill without re-registering, because a
//     manager entry for a deactivated grain would fire forever;
//   - in-flight or paused: do nothing, the last-completion path owns re-entry
//     into passivation (resumePassivation);
//   - recently active: re-register with a fresh idle deadline;
//   - otherwise deactivate, exactly like the direct passivation path.
func (pid *grainPID) handlePassivationPill() {
	if !pid.isActive() || pid.onPoisonPill.Load() {
		return
	}

	reentrant := pid.reentrancy.Load()
	if (reentrant != nil && reentrant.inFlightCount.Load() > 0) || pid.paused() {
		return
	}

	deadline := pid.latestReceiveTimeNano.Load() + pid.config.deactivateAfter.Nanoseconds()
	if deadline > time.Now().UnixNano() {
		pid.startPassivation()
		return
	}

	if pid.getLogger().Enabled(log.DebugLevel) {
		pid.getLogger().Debugf("grain=%s reason=%s passivation triggered", pid.identity.String(), passivation.NewTimeBasedStrategy(pid.config.deactivateAfter).Name())
	}

	if err := pid.deactivate(context.Background()); err != nil && pid.getLogger().Enabled(log.ErrorLevel) {
		pid.getLogger().Errorf("failed to passivate grain=%s: %v (hint: check OnPassivate implementation)", pid.identity.String(), err)
	}
}

// markActivity records at as the grain's latest activity. The cheap atomic
// store always happens; the passivation manager Touch, which takes the
// manager's shared mutex, is coalesced to at most once per
// passivationTouchInterval exactly like the actor-side markActivity. The
// manager's idle deadline therefore trails true activity by at most one
// interval, well inside any practical deactivateAfter.
func (pid *grainPID) markActivity(at time.Time) {
	nanos := at.UnixNano()
	pid.latestReceiveTimeNano.Store(nanos)

	if pid.passivationManager != nil {
		// Coalesce Touch calls: only acquire the passivation manager's mutex
		// when at least passivationTouchInterval has elapsed since the last
		// Touch. The CAS ensures exactly one goroutine wins per interval.
		last := pid.lastPassivationTouch.Load()
		if nanos-last >= passivationTouchInterval {
			if pid.lastPassivationTouch.CompareAndSwap(last, nanos) {
				pid.passivationManager.Touch(pid)
			}
		}
	}
}

func (pid *grainPID) shouldAutoPassivate() bool {
	return pid.passivationTimeout() > 0
}

func (pid *grainPID) startPassivation() {
	timeout := pid.passivationTimeout()
	if timeout <= 0 {
		return
	}
	strategy := passivation.NewTimeBasedStrategy(timeout)
	pid.passivationManager.Register(pid, strategy)
}

func (pid *grainPID) passivationTimeout() time.Duration {
	if pid.passivationManager == nil {
		return 0
	}

	return pid.config.deactivateAfter
}

func (pid *grainPID) unregisterPassivation() {
	if pid.passivationManager != nil {
		pid.passivationManager.Unregister(pid)
	}
}

func (pid *grainPID) toWireGrain() (*internalpb.Grain, error) {
	wire, err := wireGrain(pid.identity, pid.config, pid.actorSystem.Host(), pid.actorSystem.Port())
	if err != nil {
		return nil, err
	}

	// Snapshot the live policy rather than the activation config, so
	// reentrancy enabled or retuned at runtime survives eager relocation and
	// remote activation.
	if reentrant := pid.reentrancy.Load(); reentrant != nil {
		wire.SetReentrancy(reentrant.toProto())
	}

	return wire, nil
}

// wireGrain builds the cluster wire record for a grain from its identity and
// configuration. It is the single source of truth for the wire representation:
// both live grain processes (toWireGrain) and claim-time records built before
// any grain process exists (tryPeerActivation) must go through it so the two
// representations cannot drift.
func wireGrain(identity *GrainIdentity, config *grainConfig, host string, port int) (*internalpb.Grain, error) {
	dependencies, err := codec.EncodeDependencies(config.dependencyValues()...)
	if err != nil {
		return nil, err
	}

	grainID := &internalpb.GrainId{}
	grainID.SetKind(identity.Kind())
	grainID.SetName(identity.Name())
	grainID.SetValue(identity.String())
	grain := &internalpb.Grain{}
	grain.SetGrainId(grainID)
	grain.SetHost(host)
	grain.SetPort(int32(port))
	grain.SetDependencies(dependencies)
	grain.SetActivationTimeout(durationpb.New(config.initTimeout))
	grain.SetActivationRetries(config.initMaxRetries)
	grain.SetMailboxCapacity(config.capacity)
	grain.SetDisableRelocation(config.disableRelocation)
	grain.SetEagerRelocation(config.eagerRelocation)
	grain.SetReentrancy(codec.EncodeReentrancy(config.reentrancy))
	if config.role != nil {
		grain.SetRole(*config.role)
	}
	return grain, nil
}
