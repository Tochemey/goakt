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
	"time"

	"github.com/flowchartsman/retry"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/collection"
	"github.com/tochemey/goakt/v3/internal/workerpool"
	"github.com/tochemey/goakt/v3/log"
)

type grainPID struct {
	grain    Grain
	identity *GrainIdentity

	initMaxRetries atomic.Int32

	initTimeout atomic.Duration
	mailbox     *grainMailbox

	latestReceiveTime atomic.Time

	// the actor system
	actorSystem ActorSystem

	// specifies the logger to use
	logger log.Logger

	// atomic flag indicating whether the actor is processing messages
	processing atomic.Int32
	remoting   *Remoting

	workerPool *workerpool.WorkerPool

	// the list of dependencies
	dependencies *collection.Map[string, extension.Dependency]
	activated    *atomic.Bool
}

func newGrainPID(identity *GrainIdentity, grain Grain, actorSystem ActorSystem) *grainPID {
	process := &grainPID{
		grain:             grain,
		identity:          identity,
		mailbox:           newGrainMailbox(),
		actorSystem:       actorSystem,
		logger:            actorSystem.Logger(),
		remoting:          actorSystem.getRemoting(),
		workerPool:        actorSystem.getWorkerPool(),
		dependencies:      collection.NewMap[string, extension.Dependency](),
		activated:         atomic.NewBool(false),
		latestReceiveTime: atomic.Time{},
	}

	process.initMaxRetries.Store(DefaultInitMaxRetries)
	process.initTimeout.Store(DefaultInitTimeout)
	process.processing.Store(idle)

	return process
}

// activate activates the Grain
func (pid *grainPID) activate(ctx context.Context) error {
	logger := pid.logger
	logger.Infof("Activating Grain %s ...", pid.identity.String())

	cctx, cancel := context.WithTimeout(ctx, pid.initTimeout.Load())
	retrier := retry.NewRetrier(int(pid.initMaxRetries.Load()), time.Millisecond, pid.initTimeout.Load())

	if err := retrier.RunContext(cctx, func(_ context.Context) error {
		return pid.grain.OnActivate(cctx, newGrainProps(pid.identity, pid.actorSystem))
	}); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			cancel()
			pid.logger.Errorf("Grain %s activation timed out.", pid.identity.String())
			return ErrGrainActivationTimeout
		}

		cancel()
		pid.logger.Errorf("Grain %s activation failed.", pid.identity.String())
		return NewErrGrainActivationFailure(err)
	}

	pid.activated.Store(true)
	pid.logger.Infof("Grain %s successfully activated.", pid.identity.String())
	cancel()

	return nil
}

// deactivate deactivates the Grain
func (pid *grainPID) deactivate(ctx context.Context) error {
	logger := pid.logger

	defer func() {
		pid.activated.Store(false)
	}()

	logger.Infof("Deactivating Grain %s ...", pid.identity.String())
	if pid.remoting != nil {
		pid.remoting.Close()
	}

	if err := pid.grain.OnDeactivate(ctx, newGrainProps(pid.identity, pid.actorSystem)); err != nil {
		pid.logger.Errorf("Grain %s deactivation failed.", pid.identity.String())
		return NewErrGrainDeactivationFailure(err)
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
		if err := pid.mailbox.Enqueue(grainContext); err != nil {
			pid.logger.Warn(err)
		}
		pid.schedule()
	}
}

// schedule  schedules that a message has arrived and wake up the
// message processing loop
func (pid *grainPID) schedule() {
	if pid.processing.CompareAndSwap(idle, busy) {
		pid.workerPool.SubmitWork(pid.receiveLoop)
	}
}

// receiveLoop extracts every message from the actor mailbox
// and pass it to the appropriate behavior for handling
func (pid *grainPID) receiveLoop() {
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
		if !pid.processing.CompareAndSwap(busy, idle) {
			return
		}

		// Check if new messages were added in the meantime and restart processing
		if !pid.mailbox.IsEmpty() && pid.processing.CompareAndSwap(idle, busy) {
			continue
		}
		return
	}
}

func (pid *grainPID) handlePoisonPill(grainContext *GrainContext) {
	defer pid.recovery(grainContext)
	if err := pid.deactivate(grainContext.Context()); err != nil {
		grainContext.Err(err)
		return
	}
	grainContext.NoErr()
}

func (pid *grainPID) handleGrainContext(grainContext *GrainContext) {
	defer pid.recovery(grainContext)
	pid.latestReceiveTime.Store(time.Now())
	pid.grain.OnReceive(grainContext)
}

// recovery is called upon after message is processed
func (pid *grainPID) recovery(received *GrainContext) {
	if r := recover(); r != nil {
		switch err, ok := r.(error); {
		case ok:
			var pe *PanicError
			if errors.As(err, &pe) {
				received.Err(pe)
				return
			}

			// this is a normal error just wrap it with some stack trace
			// for rich logging purpose
			pc, fn, line, _ := runtime.Caller(2)
			received.Err(NewPanicError(
				fmt.Errorf("%w at %s[%s:%d]", err, runtime.FuncForPC(pc).Name(), fn, line),
			))

		default:
			// we have no idea what panic it is. Enrich it with some stack trace for rich
			// logging purpose
			pc, fn, line, _ := runtime.Caller(2)
			received.Err(NewPanicError(
				fmt.Errorf("%#v at %s[%s:%d]", r, runtime.FuncForPC(pc).Name(), fn, line),
			))
		}

		return
	}
}

// getGrain returns the Grain instance
func (pid *grainPID) getGrain() Grain {
	return pid.grain
}

// getIdentity returns the GrainIdentity of the Grain
func (pid *grainPID) getIdentity() *GrainIdentity {
	return pid.identity
}
