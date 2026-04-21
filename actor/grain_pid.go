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
	"go.uber.org/atomic"
	"google.golang.org/protobuf/types/known/durationpb"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/passivation"
)

type grainPID struct {
	grain    Grain
	identity *GrainIdentity
	mailbox  *grainMailbox

	// latestReceiveTimeNano holds the latest receive timestamp as
	// UnixNano. Stored as int64 because atomic.Time boxes time.Time
	// into an atomic.Value on every Store, which would allocate on
	// the hot path.
	latestReceiveTimeNano atomic.Int64
	processedCount        atomic.Int64

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

	// the list of dependencies
	dependencies *xsync.Map[string, extension.Dependency]
	activated    atomic.Bool
	activatedAt  atomic.Int64
	config       *grainConfig

	mu                 sync.Mutex
	deactivateAfter    atomic.Duration
	passivationManager *passivationManager

	onPoisonPill      atomic.Bool
	disableRelocation atomic.Bool
}

var (
	_ passivationParticipant = (*grainPID)(nil)
	_ schedulable            = (*grainPID)(nil)
)

func newGrainPID(identity *GrainIdentity, grain Grain, actorSystem ActorSystem, config *grainConfig) *grainPID {
	pid := &grainPID{
		grain:                 grain,
		identity:              identity,
		mailbox:               newGrainMailbox(config.capacity),
		actorSystem:           actorSystem,
		logger:                actorSystem.Logger(),
		remoting:              actorSystem.getRemoting(),
		dispatcher:            actorSystem.getDispatcher(),
		dependencies:          config.dependencies,
		latestReceiveTimeNano: atomic.Int64{},
		config:                config,
		passivationManager:    actorSystem.passivationManager(),
	}

	pid.activated.Store(false)
	pid.onPoisonPill.Store(false)
	pid.disableRelocation.Store(false)
	pid.processedCount.Store(0)
	pid.activatedAt.Store(0)

	return pid
}

// activate activates the Grain
func (pid *grainPID) activate(ctx context.Context) (err error) {
	logger := pid.logger
	if logger.Enabled(log.InfoLevel) {
		logger.Infof("grain=%s activating", pid.identity.String())
	}

	retries := pid.config.initMaxRetries.Load()
	timeout := pid.config.initTimeout.Load()

	cctx, cancel := context.WithTimeout(ctx, timeout)
	retrier := retry.NewRetrier(int(retries), timeout, timeout)

	defer func() {
		if r := recover(); r != nil {
			cancel()
			switch v := r.(type) {
			case error:
				var pe *gerrors.PanicError
				if errors.As(v, &pe) {
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
		return pid.grain.OnActivate(ctx, newGrainProps(pid.identity, pid.actorSystem, pid.dependencies.Values()))
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
	if pid.logger.Enabled(log.InfoLevel) {
		pid.logger.Infof("grain=%s activated successfully", pid.identity.String())
	}
	cancel()

	pid.markActivity(time.Now())

	if pid.shouldAutoPassivate() {
		pid.startPassivation()
	}

	return nil
}

// deactivate deactivates the Grain
func (pid *grainPID) deactivate(ctx context.Context) (err error) {
	logger := pid.logger

	defer func() {
		if r := recover(); r != nil {
			switch v := r.(type) {
			case error:
				var pe *gerrors.PanicError
				if errors.As(v, &pe) {
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

	defer func() {
		pid.activated.Store(false)
		pid.activatedAt.Store(0)
		pid.latestReceiveTimeNano.Store(0)
		pid.onPoisonPill.Store(false)
		pid.disableRelocation.Store(false)
	}()

	if logger.Enabled(log.InfoLevel) {
		logger.Infof("grain=%s deactivating", pid.identity.String())
	}
	if pid.remoting != nil {
		pid.remoting.Close()
	}

	if err := pid.grain.OnDeactivate(ctx, newGrainProps(pid.identity, pid.actorSystem, pid.dependencies.Values())); err != nil {
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

	if pid.logger.Enabled(log.InfoLevel) {
		pid.logger.Infof("grain=%s deactivated successfully", pid.identity.String())
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

	budget := w.dispatcher.throughput
	for range budget {
		grainContext := pid.mailbox.Dequeue()
		if grainContext == nil {
			if pid.finishOrReclaim() {
				return
			}
			continue
		}
		pid.dispatchOne(grainContext)
	}
	pid.schedState.YieldToScheduled()
	w.reschedule(pid)
}

// dispatchOne routes a single message through the appropriate handler.
// Release is owned by grainMailbox.Dequeue, which reclaims the
// previous sentinel; dispatchOne must not return the context here or
// the mailbox would hand out an in-use head.
func (pid *grainPID) dispatchOne(grainContext *GrainContext) {
	switch grainContext.Message().(type) {
	case *PoisonPill:
		pid.handlePoisonPill(grainContext)
	default:
		pid.handleGrainContext(grainContext)
	}
}

// finishOrReclaim attempts the Processing -> Idle transition. Returns
// true when the caller must exit the turn (no work remains and ownership
// is fully released). Returns false when a concurrent enqueue raced the
// transition, ownership was reclaimed, and the caller must continue
// draining within the same budget.
func (pid *grainPID) finishOrReclaim() bool {
	pid.schedState.reset()
	if pid.mailbox.IsEmpty() {
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
	if err := pid.deactivate(grainContext.Context()); err != nil {
		grainContext.Err(err)
		return
	}
	grainContext.NoErr()
}

func (pid *grainPID) handleGrainContext(grainContext *GrainContext) {
	defer pid.recovery(grainContext)
	pid.processedCount.Inc()
	pid.markActivity(time.Now())
	pid.grain.OnReceive(grainContext)
}

// recovery is called upon after message is processed
func (pid *grainPID) recovery(received *GrainContext) {
	if r := recover(); r != nil {
		switch err, ok := r.(error); {
		case ok:
			var pe *gerrors.PanicError
			if errors.As(err, &pe) {
				received.Err(pe)
				return
			}

			// this is a normal error just wrap it with some stack trace
			// for rich logging purpose
			pc, fn, line, _ := runtime.Caller(2)
			received.Err(gerrors.NewPanicError(
				fmt.Errorf("%w at %s[%s:%d]", err, runtime.FuncForPC(pc).Name(), fn, line),
			))

		default:
			// we have no idea what panic it is. Enrich it with some stack trace for rich
			// logging purpose
			pc, fn, line, _ := runtime.Caller(2)
			received.Err(gerrors.NewPanicError(
				fmt.Errorf("%#v at %s[%s:%d]", r, runtime.FuncForPC(pc).Name(), fn, line),
			))
		}

		return
	}
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

	if pid.logger.Enabled(log.InfoLevel) {
		pid.logger.Infof("grain=%s reason=%s passivation triggered", pid.identity.String(), reason)
	}
	if err := pid.deactivate(context.Background()); err != nil {
		if pid.logger.Enabled(log.ErrorLevel) {
			pid.logger.Errorf("failed to passivate grain=%s: %v (hint: check OnPassivate implementation)", pid.identity.String(), err)
		}
		return false
	}
	return true
}

func (pid *grainPID) markActivity(at time.Time) {
	pid.latestReceiveTimeNano.Store(at.UnixNano())
	if pid.passivationManager != nil {
		pid.passivationManager.Touch(pid)
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
	dependencies, err := codec.EncodeDependencies(pid.dependencies.Values()...)
	if err != nil {
		return nil, err
	}

	return &internalpb.Grain{
		GrainId: &internalpb.GrainId{
			Kind:  pid.identity.Kind(),
			Name:  pid.identity.Name(),
			Value: pid.identity.String(),
		},
		Host:              pid.actorSystem.Host(),
		Port:              int32(pid.actorSystem.Port()),
		Dependencies:      dependencies,
		ActivationTimeout: durationpb.New(pid.config.initTimeout.Load()),
		ActivationRetries: pid.config.initMaxRetries.Load(),
		MailboxCapacity:   new(pid.mailbox.Capacity()),
		DisableRelocation: pid.disableRelocation.Load(),
	}, nil
}
