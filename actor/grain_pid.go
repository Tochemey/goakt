/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

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
	"go.uber.org/multierr"
	"google.golang.org/protobuf/types/known/durationpb"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/codec"
	"github.com/tochemey/goakt/v3/internal/collection"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/passivation"
	"github.com/tochemey/goakt/v3/remote"
)

type grainPID struct {
	grain    Grain
	identity *GrainIdentity
	mailbox  *grainMailbox

	latestReceiveTime atomic.Time

	// the actor system
	actorSystem ActorSystem

	// specifies the logger to use
	logger log.Logger

	// atomic flag indicating whether the actor is processing messages
	processing atomic.Int32
	remoting   remote.Remoting

	// the list of dependencies
	dependencies *collection.Map[string, extension.Dependency]
	activated    *atomic.Bool
	config       *grainConfig

	mu                 *sync.Mutex
	deactivateAfter    *atomic.Duration
	passivationManager *passivationManager

	onPoisonPill *atomic.Bool
}

var _ passivationParticipant = (*grainPID)(nil)

func newGrainPID(identity *GrainIdentity, grain Grain, actorSystem ActorSystem, config *grainConfig) *grainPID {
	pid := &grainPID{
		grain:              grain,
		identity:           identity,
		mailbox:            newGrainMailbox(),
		actorSystem:        actorSystem,
		logger:             actorSystem.Logger(),
		remoting:           actorSystem.getRemoting(),
		dependencies:       config.dependencies,
		activated:          atomic.NewBool(false),
		latestReceiveTime:  atomic.Time{},
		config:             config,
		mu:                 &sync.Mutex{},
		onPoisonPill:       atomic.NewBool(false),
		passivationManager: actorSystem.passivationManager(),
	}

	pid.processing.Store(idle)

	return pid
}

// activate activates the Grain
func (pid *grainPID) activate(ctx context.Context) error {
	logger := pid.logger
	logger.Infof("Activating Grain %s ...", pid.identity.String())

	retries := pid.config.initMaxRetries.Load()
	timeout := pid.config.initTimeout.Load()

	cctx, cancel := context.WithTimeout(ctx, timeout)
	retrier := retry.NewRetrier(int(retries), timeout, timeout)

	if err := retrier.RunContext(cctx, func(ctx context.Context) error {
		return pid.grain.OnActivate(ctx, newGrainProps(pid.identity, pid.actorSystem, pid.dependencies.Values()))
	}); err != nil {
		cancel()
		pid.logger.Errorf("Grain %s activation failed.", pid.identity.String())
		return gerrors.NewErrGrainActivationFailure(err)
	}

	pid.activated.Store(true)
	pid.deactivateAfter = atomic.NewDuration(pid.config.deactivateAfter)
	pid.logger.Infof("Grain %s successfully activated.", pid.identity.String())
	cancel()

	pid.markActivity(time.Now())

	if pid.shouldAutoPassivate() {
		pid.startPassivation()
	}

	return nil
}

// deactivate deactivates the Grain
func (pid *grainPID) deactivate(ctx context.Context) error {
	logger := pid.logger

	pid.unregisterPassivation()

	defer func() {
		pid.activated.Store(false)
		pid.latestReceiveTime.Store(time.Time{})
		pid.onPoisonPill.Store(false)
	}()

	logger.Infof("Deactivating Grain %s ...", pid.identity.String())
	if pid.remoting != nil {
		pid.remoting.Close()
	}

	if err := pid.grain.OnDeactivate(ctx, newGrainProps(pid.identity, pid.actorSystem, pid.dependencies.Values())); err != nil {
		pid.logger.Errorf("Grain %s deactivation failed.", pid.identity.String())
		return gerrors.NewErrGrainDeactivationFailure(err)
	}

	actorSystem := pid.actorSystem
	identity := pid.getIdentity()
	grainID := toWireGrainID(identity)

	actorSystem.getGrains().Delete(identity.String())
	if actorSystem.InCluster() {
		rerr := actorSystem.removePeerGrain(ctx, grainID)
		cerr := actorSystem.getCluster().RemoveGrain(ctx, pid.identity.String())

		if err := multierr.Combine(rerr, cerr); err != nil {
			pid.logger.Errorf("failed to remove grain %s from cluster: %v", pid.identity.String(), err)
			return gerrors.NewErrGrainDeactivationFailure(err)
		}
	}

	pid.logger.Infof("Grain %s successfully deactivated.", pid.identity.String())
	return nil
}

// isActive returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (pid *grainPID) isActive() bool {
	return pid != nil && pid.activated.Load()
}

// receive pushes a given message to the actor mailbox
// and signals the receiveLoop to process it
func (pid *grainPID) receive(grainContext *GrainContext) {
	if pid.isActive() {
		pid.mailbox.Enqueue(grainContext)
		pid.process()
	}
}

// process extracts every message from the actor mailbox
// and pass it to the appropriate behavior for handling
func (pid *grainPID) process() {
	// Only start a processing loop when transitioning from idle -> busy.
	// If another loop is already running (state is busy), exit early.
	if !pid.processing.CompareAndSwap(idle, busy) {
		return
	}

	go func() {
		var grainContext *GrainContext
		for {
			if grainContext != nil {
				releaseGrainContext(grainContext)
			}

			if grainContext = pid.mailbox.Dequeue(); grainContext != nil {
				switch grainContext.Message().(type) {
				case *goaktpb.PoisonPill:
					pid.handlePoisonPill(grainContext)
				default:
					pid.handleGrainContext(grainContext)
				}
			}

			// if no more messages, change busy state to idle
			pid.processing.Store(idle)

			// Check if new messages were added in the meantime and restart processing
			if !pid.mailbox.IsEmpty() && pid.processing.CompareAndSwap(idle, busy) {
				continue
			}

			return
		}
	}()
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
	return pid.latestReceiveTime.Load()
}

func (pid *grainPID) passivationTry(reason string) bool {
	if !pid.isActive() || pid.onPoisonPill.Load() {
		return false
	}

	pid.logger.Infof("passivation triggered for Grain %s (%s)", pid.identity.String(), reason)
	if err := pid.deactivate(context.Background()); err != nil {
		pid.logger.Errorf("failed to passivate Grain %s: %v", pid.identity.String(), err)
		return false
	}
	return true
}

func (pid *grainPID) markActivity(at time.Time) {
	pid.latestReceiveTime.Store(at)
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
	if pid.passivationManager == nil || pid.deactivateAfter == nil {
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
	}, nil
}
