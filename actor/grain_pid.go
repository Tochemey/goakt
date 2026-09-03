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
	"time"

	"github.com/flowchartsman/retry"
	"github.com/google/uuid"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/types/known/durationpb"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/reentrancy"
)

type grainPID struct {
	grain    Grain
	identity *GrainIdentity
	mailbox  *grainMailbox

	// reentrancy tracks this activation's in-flight requests; nil until the
	// grain is configured with reentrancy, at activation or at runtime through
	// EnableReentrancy. An atomic pointer because the runtime install happens
	// on the processing turn while off-turn readers (the envelope-ask gate,
	// envelope delivery, shutdown) observe it; it transitions nil to non-nil
	// at most once and is never removed, disabling only flips the state's
	// default mode to Off. responses is the dedicated queue for async response
	// envelopes: completions must stay reachable while the user mailbox is
	// paused, so they never share its queue.
	reentrancy atomic.Pointer[reentrancyState]
	responses  *grainMailbox

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

	// the actor system
	actorSystem ActorSystem

	// specifies the logger to use
	logger log.Logger

	// schedState drives the grain's membership on the dispatcher's ready
	// queue. The Idle -> Scheduled -> Processing transitions enforce that
	// at most one worker drains the mailbox at a time, replacing the old
	// per-burst goroutine while preserving the single-threaded execution
	// invariant per grain.
	schedState dispatchState
	// dispatcher is the shared worker pool that runs grain turns. Set at
	// construction from the owning actor system; nil only in unit tests
	// that drive runTurn directly.
	dispatcher *dispatcher
	remoting   remoteclient.Client

	// ctxShard is this grain's home shard in grainContextPool. Every
	// GrainContext built for this grain is taken from and returned to that
	// shard, so unrelated grains never contend on pool state. Assigned
	// round-robin at construction.
	ctxShard uint32

	// the list of dependencies
	dependencies *xsync.Map[string, extension.Dependency]
	activated    atomic.Bool
	activatedAt  atomic.Int64
	config       *grainConfig

	mu                 sync.Mutex
	deactivateAfter    atomic.Duration
	passivationManager *passivationManager

	// timers holds the current activation's timer registry. activate installs a
	// fresh one and starts it once activation completes; deactivate stops it
	// before OnDeactivate runs. Nil until the first activation. Guarded by mu.
	timers *grainTimers

	onPoisonPill atomic.Bool

	// deactivated is closed exactly once when deactivate completes
	// (success or failure). Shutdown uses this to wait for grain
	// OnDeactivate hooks that are dispatched via a PoisonPill turn.
	deactivated     chan types.Unit
	deactivatedOnce sync.Once
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
		mailbox:               newGrainMailbox(config.capacity),
		responses:             newGrainMailbox(0),
		actorSystem:           actorSystem,
		logger:                actorSystem.Logger(),
		remoting:              actorSystem.getRemoting(),
		dispatcher:            actorSystem.getDispatcher(),
		dependencies:          config.dependencies,
		latestReceiveTimeNano: atomic.Int64{},
		config:                config,
		passivationManager:    actorSystem.passivationManager(),
		deactivated:           make(chan types.Unit),
		ctxShard:              nextGrainContextShard(),
	}

	pid.activated.Store(false)
	pid.onPoisonPill.Store(false)
	pid.processedCount.Store(0)
	pid.activatedAt.Store(0)

	// A policy whose mode is Off behaves exactly like no policy; skipping the
	// state keeps the legacy paths bit for bit until reentrancy is enabled at
	// runtime.
	if config.reentrancy != nil && config.reentrancy.Mode() != reentrancy.Off {
		pid.reentrancy.Store(newReentrancyState(config.reentrancy.Mode(), config.reentrancy.MaxInFlight()))
	}

	return pid
}

// activate activates the Grain
func (pid *grainPID) activate(ctx context.Context) (err error) {
	logger := pid.logger
	if logger.Enabled(log.DebugLevel) {
		logger.Debugf("grain=%s activating", pid.identity.String())
	}

	// Fresh registry per activation: a reused grainPID must not carry timers (or
	// a stopped registry) from a previous activation. Timers registered during
	// OnActivate stay dormant until the registry starts below, so a short-delay
	// tick cannot fire into the not-yet-active grain and be silently dropped.
	registry := newGrainTimers(pid.deliverTimerTick)
	pid.setTimers(registry)

	// Registered before the recover defer so it runs after err is materialized:
	// on any activation failure the registry stops and timers registered by the
	// failed OnActivate can never fire.
	defer func() {
		if err != nil {
			registry.stop()
		}
	}()

	retries := pid.config.initMaxRetries.Load()
	timeout := pid.config.initTimeout.Load()

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
		return pid.grain.OnActivate(ctx, newGrainProps(pid.identity, pid.actorSystem, pid.dependencies.Values(), pid))
	}); err != nil {
		cancel()
		if pid.logger.Enabled(log.ErrorLevel) {
			pid.logger.Errorf("grain=%s activation failed (hint: check OnActivate implementation)", pid.identity.String())
		}
		return gerrors.NewErrGrainActivationFailure(err)
	}

	pid.activated.Store(true)
	pid.activatedAt.Store(time.Now().Unix())
	pid.deactivateAfter.Store(pid.config.deactivateAfter)
	if pid.logger.Enabled(log.DebugLevel) {
		pid.logger.Debugf("grain=%s activated successfully", pid.identity.String())
	}
	cancel()

	pid.markActivity(time.Now())

	if pid.shouldAutoPassivate() {
		pid.startPassivation()
	}

	registry.start()

	return nil
}

// deactivate deactivates the Grain
func (pid *grainPID) deactivate(ctx context.Context) (err error) {
	logger := pid.logger

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

	// Stop before OnDeactivate runs: every timer is cancelled, late
	// registrations from the OnDeactivate hook are rejected, and a tick already
	// sitting in the mailbox is dropped by the tick handler.
	if registry := pid.getTimers(); registry != nil {
		registry.stop()
	}

	defer func() {
		pid.activated.Store(false)
		pid.activatedAt.Store(0)
		pid.latestReceiveTimeNano.Store(0)
		pid.onPoisonPill.Store(false)
		// Signal completion exactly once so callers waiting on
		// <-pid.deactivated (e.g. shutdown) unblock even on panic
		// or error. Nil-guard lets tests that construct grainPID as
		// a struct literal (without going through newGrainPID) still
		// drive deactivate without panicking on close of nil channel.
		pid.deactivatedOnce.Do(func() {
			if pid.deactivated != nil {
				close(pid.deactivated)
			}
		})
	}()

	if logger.Enabled(log.DebugLevel) {
		logger.Debugf("grain=%s deactivating", pid.identity.String())
	}

	if err := pid.grain.OnDeactivate(ctx, newGrainProps(pid.identity, pid.actorSystem, pid.dependencies.Values(), pid)); err != nil {
		if pid.logger.Enabled(log.ErrorLevel) {
			pid.logger.Errorf("grain=%s deactivation failed (hint: check OnDeactivate implementation)", pid.identity.String())
		}
		return gerrors.NewErrGrainDeactivationFailure(err)
	}

	actorSystem := pid.actorSystem
	identity := pid.getIdentity()

	actorSystem.getGrains().Delete(identity.String())
	if actorSystem.InCluster() {
		if err := actorSystem.getCluster().RemoveGrain(ctx, pid.identity.String()); err != nil {
			if pid.logger.Enabled(log.ErrorLevel) {
				pid.logger.Errorf("failed to remove grain=%s from cluster: %v (hint: check cluster connectivity)", pid.identity.String(), err)
			}
			return gerrors.NewErrGrainDeactivationFailure(err)
		}
	}

	if pid.logger.Enabled(log.DebugLevel) {
		pid.logger.Debugf("grain=%s deactivated successfully", pid.identity.String())
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

	if err := pid.mailbox.Enqueue(grainContext); err != nil {
		grainContext.Err(err)
		return
	}

	if pid.schedState.TrySchedule() {
		pid.dispatcher.schedule(pid)
	}
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
			grainContext = pid.mailbox.Dequeue()
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
	if pid.responses == nil {
		return nil
	}
	return pid.responses.Dequeue()
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
	if pid.responses != nil && !pid.responses.IsEmpty() {
		return true
	}
	return !pid.paused() && !pid.mailbox.IsEmpty()
}

// dispatchOne routes a single message through the appropriate handler.
// Release is owned by grainMailbox.Dequeue, which reclaims the
// previous sentinel; dispatchOne must not return the context here or
// the mailbox would hand out an in-use head.
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
		if pid.logger.Enabled(log.WarningLevel) {
			pid.logger.Warnf("grain=%s dropping malformed async request envelope", pid.getIdentity().String())
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

	if !completed && pid.logger.Enabled(log.DebugLevel) {
		pid.logger.Debugf("grain=%s async response dropped: no in-flight request for correlation id=%s", pid.getIdentity().String(), response.CorrelationID)
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
// runtime. In-flight requests keep the mode they were admitted with.
func (pid *grainPID) enableReentrancy(config *reentrancy.Reentrancy) error {
	return installReentrancy(&pid.reentrancy, config)
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

	var queue *grainMailbox
	switch envelope.(type) {
	case *commands.AsyncRequest:
		queue = pid.mailbox
	case *commands.AsyncResponse:
		queue = pid.responses
	default:
		return gerrors.ErrInvalidMessage
	}

	if queue == nil {
		return gerrors.ErrReentrancyDisabled
	}

	grainContext := getGrainContext(pid.ctxShard)
	grainContext.build(context.WithoutCancel(ctx), pid, pid.actorSystem, pid.getIdentity(), envelope, grainEnvelope)

	if err := queue.Enqueue(grainContext); err != nil {
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
		if r := recover(); r != nil && pid.logger.Enabled(log.ErrorLevel) {
			pid.logger.Errorf("grain=%s continuation panicked during teardown: %v", pid.getIdentity().String(), r)
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
		if err := state.cancel(); err != nil && pid.logger.Enabled(log.DebugLevel) {
			pid.logger.Debugf("grain=%s failed to cancel in-flight request id=%s: %v", pid.getIdentity().String(), state.id, err)
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

	if err := pid.mailbox.Enqueue(grainContext); err != nil {
		releaseGrainContext(grainContext)
		if pid.logger.Enabled(log.WarningLevel) {
			pid.logger.Warnf("grain=%s dropping tick of timer=%s: %v", pid.getIdentity().String(), entry.reference, err)
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
		if err != nil && pid.logger.Enabled(log.WarningLevel) {
			pid.logger.Warnf("grain=%s failed to handle tick of timer=%s: %v", pid.getIdentity().String(), entry.reference, err)
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
		if pid.logger.Enabled(log.ErrorLevel) {
			pid.logger.Errorf("grain=%s panicked while handling %T: %v", pid.getIdentity().String(), received.Message(), failure)
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

// getGrain returns the Grain instance
func (pid *grainPID) getGrain() Grain {
	pid.mu.Lock()
	grain := pid.grain
	pid.mu.Unlock()
	return grain
}

// timerRegistry returns the current activation's timer registry for the public
// scheduling API. It degrades to ErrGrainTimersStopped when the grain process is
// absent or has never been activated, so GrainProps built without a process
// reject scheduling instead of panicking.
func (pid *grainPID) timerRegistry() (*grainTimers, error) {
	if pid == nil {
		return nil, gerrors.ErrGrainTimersStopped
	}

	registry := pid.getTimers()
	if registry == nil {
		return nil, gerrors.ErrGrainTimersStopped
	}
	return registry, nil
}

// getTimers returns the current activation's timer registry, or nil when the
// grain has never been activated.
func (pid *grainPID) getTimers() *grainTimers {
	pid.mu.Lock()
	registry := pid.timers
	pid.mu.Unlock()
	return registry
}

// setTimers installs the timer registry of a new activation.
func (pid *grainPID) setTimers(registry *grainTimers) {
	pid.mu.Lock()
	pid.timers = registry
	pid.mu.Unlock()
}

// getIdentity returns the GrainIdentity of the Grain
func (pid *grainPID) getIdentity() *GrainIdentity {
	pid.mu.Lock()
	id := pid.identity
	pid.mu.Unlock()
	return id
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

	if pid.logger.Enabled(log.DebugLevel) {
		pid.logger.Debugf("grain=%s reason=%s passivation triggered", pid.identity.String(), reason)
	}

	if err := pid.deactivate(context.Background()); err != nil {
		if pid.logger.Enabled(log.ErrorLevel) {
			pid.logger.Errorf("failed to passivate grain=%s: %v (hint: check OnPassivate implementation)", pid.identity.String(), err)
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

	if err := pid.mailbox.Enqueue(grainContext); err != nil {
		releaseGrainContext(grainContext)
		pid.markActivity(time.Now())
		return false
	}

	if pid.schedState.TrySchedule() {
		pid.dispatcher.schedule(pid)
	}
	return true
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

	deadline := pid.latestReceiveTimeNano.Load() + pid.deactivateAfter.Load().Nanoseconds()
	if deadline > time.Now().UnixNano() {
		pid.startPassivation()
		return
	}

	if pid.logger.Enabled(log.DebugLevel) {
		pid.logger.Debugf("grain=%s reason=%s passivation triggered", pid.identity.String(), passivation.NewTimeBasedStrategy(pid.deactivateAfter.Load()).Name())
	}

	if err := pid.deactivate(context.Background()); err != nil && pid.logger.Enabled(log.ErrorLevel) {
		pid.logger.Errorf("failed to passivate grain=%s: %v (hint: check OnPassivate implementation)", pid.identity.String(), err)
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
	return pid.deactivateAfter.Load()
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
	dependencies, err := codec.EncodeDependencies(config.dependencies.Values()...)
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
	grain.SetActivationTimeout(durationpb.New(config.initTimeout.Load()))
	grain.SetActivationRetries(config.initMaxRetries.Load())
	grain.SetMailboxCapacity(config.capacity)
	grain.SetDisableRelocation(config.disableRelocation)
	grain.SetEagerRelocation(config.eagerRelocation)
	grain.SetReentrancy(codec.EncodeReentrancy(config.reentrancy))
	if config.role != nil {
		grain.SetRole(*config.role)
	}
	return grain, nil
}
