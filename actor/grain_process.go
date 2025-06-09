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
	"github.com/tochemey/goakt/v3/internal/ticker"
	"github.com/tochemey/goakt/v3/internal/types"
	"github.com/tochemey/goakt/v3/internal/workerpool"
	"github.com/tochemey/goakt/v3/log"
)

type grainProcess struct {
	grain Grain
	key   *GrainKey

	passivateAfter atomic.Duration
	initMaxRetries atomic.Int32

	initTimeout atomic.Duration
	inbox       *grainInbox

	latestReceiveTime  atomic.Time
	haltPassivationLnr chan types.Unit

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

	processState *pidState
}

func newGrainProcess(name string, grain Grain, actorSystem ActorSystem, opts ...GrainOption) (*grainProcess, error) {
	// create the grain id and validate
	grainKey := newGrainKey(grain, name)
	if err := grainKey.Validate(); err != nil {
		return nil, err
	}

	gp := &grainProcess{
		grain:              grain,
		key:                grainKey,
		inbox:              newGrainInxbox(),
		haltPassivationLnr: make(chan types.Unit, 1),
		actorSystem:        actorSystem,
		logger:             actorSystem.Logger(),
		remoting:           NewRemoting(), // TODO: revisit this setting. We need to set it when cluster mode is enabled for Grains
		workerPool: workerpool.New(
			workerpool.WithPoolSize(300),
			workerpool.WithPassivateAfter(time.Second),
			workerpool.WithLogger(actorSystem.Logger()),
		),
		dependencies:      collection.NewMap[string, extension.Dependency](),
		processState:      new(pidState),
		latestReceiveTime: atomic.Time{},
	}

	gp.initMaxRetries.Store(DefaultInitMaxRetries)
	gp.initTimeout.Store(DefaultInitTimeout)
	gp.processing.Store(int32(IDLE))
	gp.passivateAfter.Store(DefaultPassivationTimeout)

	// override the default values with custom one
	config := newGrainConfig(opts...)
	if config.PassivateAfter() != nil {
		gp.passivateAfter.Store(*config.PassivateAfter())
	}

	if config.Dependencies() != nil {
		for _, dep := range config.Dependencies() {
			gp.dependencies.Set(dep.ID(), dep)
		}
	}

	return gp, nil
}

// Activate activates the Grain
func (proc *grainProcess) Activate(ctx context.Context) error {
	logger := proc.logger
	logger.Infof("activating Grain %s ...", proc.key.String())

	grainContext := newGrainContext(ctx, proc.key, proc.actorSystem, proc.dependencies.Values()...)
	grainOptions := []GrainOption{
		WithGrainDependencies(proc.dependencies.Values()...),
		WithGrainPassivation(proc.passivateAfter.Load()),
	}

	cctx, cancel := context.WithTimeout(ctx, proc.initTimeout.Load())
	retrier := retry.NewRetrier(int(proc.initMaxRetries.Load()), time.Millisecond, proc.initTimeout.Load())

	if err := retrier.RunContext(cctx, func(_ context.Context) error {
		return proc.grain.OnActivate(grainContext, grainOptions...)
	}); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			cancel()
			return ErrGrainActivationTimeout
		}

		cancel()
		return NewErrGrainActivationFailure(err)
	}

	if proc.processState.IsSuspended() {
		proc.processState.ClearSuspended()
		proc.workerPool.ReStart()
	}

	if err := proc.workerPool.Start(); err != nil {
		cancel()
		return NewErrGrainActivationFailure(err)
	}

	proc.processState.SetRunning()
	proc.logger.Infof("Grain %s successfully activated.", proc.key.String())
	cancel()

	// start the passivation loop
	go proc.passivationLoop()
	return nil
}

// Deactivate deactivates the Grain
func (proc *grainProcess) Deactivate(ctx context.Context) error {
	logger := proc.logger
	logger.Infof("deactivating Grain %s ...", proc.key.String())
	if proc.remoting != nil {
		proc.remoting.Close()
	}

	grainContext := newGrainContext(ctx, proc.key, proc.actorSystem, proc.dependencies.Values()...)

	// run the PostStop hook and let watchers know
	// you are terminated
	if err := proc.grain.OnDeactivate(grainContext); err != nil {
		proc.processState.ClearRunning()
		proc.processState.ClearStopping()
		// TODO: research what happened during failed deactivation
		return NewErrGrainDeactivationFailure(err)
	}

	proc.processState.ClearRunning()
	proc.processState.ClearStopping()
	proc.processState.SetSuspended()
	proc.workerPool.Stop()
	proc.logger.Infof("Grain %s successfully deactivated.", proc.key.String())

	return nil
}

// Shutdown gracefully shuts down the given Grain
func (proc *grainProcess) Shutdown(ctx context.Context) error {
	proc.logger.Infof("shutdown process has started for Grain=(%s)...", proc.key.String())

	if !proc.processState.IsRunning() {
		proc.logger.Infof("actor=%s is offline. Maybe it has been passivated or stopped already", proc.key.String())
		return nil
	}

	proc.processState.SetStopping()
	if proc.passivateAfter.Load() > 0 {
		proc.haltPassivationLnr <- types.Unit{}
	}

	if err := proc.Deactivate(ctx); err != nil {
		proc.logger.Errorf("Grain (%s) failed to cleanly stop", proc.key.String())
		return err
	}

	proc.logger.Infof("Grain %s successfully shutdown", proc.key.String())
	return nil
}

// IsRunning returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (proc *grainProcess) IsRunning() bool {
	return proc != nil && proc.processState.IsRunnable()
}

// doReceive pushes a given message to the actor mailbox
// and signals the receiveLoop to process it
func (proc *grainProcess) doReceive(message *GrainRequest) {
	if proc.IsRunning() {
		if err := proc.inbox.Enqueue(message); err != nil {
			proc.logger.Warn(err)
			//proc.toDeadletters(receiveCtx, err)
		}
		proc.schedule()
	}
}

// schedule  schedules that a message has arrived and wake up the
// message processing loop
func (proc *grainProcess) schedule() {
	if proc.processing.CompareAndSwap(IDLE, BUSY) {
		proc.workerPool.SubmitWork(proc.receiveLoop)
	}
}

// receiveLoop extracts every message from the actor mailbox
// and pass it to the appropriate behavior for handling
func (proc *grainProcess) receiveLoop() {
	for {
		if request := proc.inbox.Dequeue(); request != nil {
			// Process the message
			switch request.Message().(type) {
			case *goaktpb.PoisonPill:
				_ = proc.Shutdown(context.Background())
			default:
				proc.handleRequest(request)
			}
		}

		// if no more messages, change busy state to idle
		if !proc.processing.CompareAndSwap(BUSY, IDLE) {
			return
		}

		// Check if new messages were added in the meantime and restart processing
		if !proc.inbox.IsEmpty() && proc.processing.CompareAndSwap(IDLE, BUSY) {
			continue
		}
		return
	}
}

func (proc *grainProcess) handleRequest(received *GrainRequest) {
	defer proc.recovery(received)
	proc.latestReceiveTime.Store(time.Now())
	response, err := proc.grain.HandleRequest(received)
	if err != nil {
		received.setError(err)
		return
	}
	received.setResponse(response)
}

// recovery is called upon after message is processed
func (proc *grainProcess) recovery(received *GrainRequest) {
	if r := recover(); r != nil {
		switch err, ok := r.(error); {
		case ok:
			var pe *PanicError
			if errors.As(err, &pe) {
				received.setError(pe)
				return
			}

			// this is a normal error just wrap it with some stack trace
			// for rich logging purpose
			pc, fn, line, _ := runtime.Caller(2)
			received.setError(NewPanicError(
				fmt.Errorf("%w at %s[%s:%d]", err, runtime.FuncForPC(pc).Name(), fn, line),
			))

		default:
			// we have no idea what panic it is. Enrich it with some stack trace for rich
			// logging purpose
			pc, fn, line, _ := runtime.Caller(2)
			received.setError(NewPanicError(
				fmt.Errorf("%#v at %s[%s:%d]", r, runtime.FuncForPC(pc).Name(), fn, line),
			))
		}
		return
	}
}

// passivationLoop checks whether the actor is processing public or not.
// when the actor is idle, it automatically shuts down to free resources
func (proc *grainProcess) passivationLoop() {
	proc.logger.Info("start the passivation listener...")
	proc.logger.Infof("passivation timeout is (%s)", proc.passivateAfter.Load().String())
	tk := ticker.New(proc.passivateAfter.Load())
	tk.Start()
	tickerStopSig := make(chan types.Unit, 1)

	// start ticking
	go func() {
		for {
			select {
			case <-tk.Ticks:
				idleTime := time.Since(proc.latestReceiveTime.Load())
				if idleTime >= proc.passivateAfter.Load() {
					tickerStopSig <- types.Unit{}
					return
				}
			case <-proc.haltPassivationLnr:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	tk.Stop()

	proc.logger.Infof("passivation mode has been triggered for Grain=%s...", proc.key.String())

	if proc.processState.IsStopping() || proc.processState.IsSuspended() {
		proc.logger.Infof("Grain=%s is stopping or maybe already deactivated. No need to passivate", proc.key.String())
		return
	}

	ctx := context.Background()
	if err := proc.Deactivate(ctx); err != nil {
		proc.logger.Errorf("failed to passivate Grain (%s): reason=(%v)", proc.key.String(), err)
		return
	}

	proc.logger.Infof("Grain %s successfully passivated", proc.key.String())
}
