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

type grainProcess struct {
	grain    Grain
	identity *Identity

	initMaxRetries atomic.Int32

	initTimeout atomic.Duration
	inbox       *grainInbox

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
	running      *atomic.Bool
}

func newGrainProcess(identity *Identity, grain Grain, actorSystem ActorSystem) *grainProcess {
	process := &grainProcess{
		grain:             grain,
		identity:          identity,
		inbox:             newGrainInxbox(),
		actorSystem:       actorSystem,
		logger:            actorSystem.Logger(),
		remoting:          actorSystem.getRemoting(),
		workerPool:        actorSystem.getWorkerPool(),
		dependencies:      collection.NewMap[string, extension.Dependency](),
		running:           atomic.NewBool(false),
		latestReceiveTime: atomic.Time{},
	}

	process.initMaxRetries.Store(DefaultInitMaxRetries)
	process.initTimeout.Store(DefaultInitTimeout)
	process.processing.Store(int32(IDLE))

	return process
}

// activate activates the Grain
func (proc *grainProcess) activate(ctx context.Context) error {
	logger := proc.logger
	logger.Infof("Activating Grain %s ...", proc.identity.String())

	grainContext := newGrainContext(ctx, proc.identity, proc.actorSystem)

	cctx, cancel := context.WithTimeout(ctx, proc.initTimeout.Load())
	retrier := retry.NewRetrier(int(proc.initMaxRetries.Load()), time.Millisecond, proc.initTimeout.Load())

	if err := retrier.RunContext(cctx, func(_ context.Context) error {
		return proc.grain.OnActivate(grainContext)
	}); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			cancel()
			proc.logger.Errorf("Grain %s activation timed out.", proc.identity.String())
			return ErrGrainActivationTimeout
		}

		cancel()
		proc.logger.Errorf("Grain %s activation failed.", proc.identity.String())
		return NewErrGrainActivationFailure(err)
	}

	proc.running.Store(true)
	proc.logger.Infof("Grain %s successfully activated.", proc.identity.String())
	cancel()

	return nil
}

// deactivate deactivates the Grain
func (proc *grainProcess) deactivate(ctx context.Context) error {
	logger := proc.logger

	defer func() {
		proc.running.Store(false)
	}()

	logger.Infof("Deactivating Grain %s ...", proc.identity.String())
	if proc.remoting != nil {
		proc.remoting.Close()
	}

	grainContext := newGrainContext(ctx, proc.identity, proc.actorSystem)

	// run the PostStop hook and let watchers know
	// you are terminated
	if err := proc.grain.OnDeactivate(grainContext); err != nil {
		proc.logger.Errorf("Grain %s deactivation failed.", proc.identity.String())
		return NewErrGrainDeactivationFailure(err)
	}

	proc.logger.Infof("Grain %s successfully deactivated.", proc.identity.String())

	return nil
}

// isRunning returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (proc *grainProcess) isRunning() bool {
	return proc != nil && proc.running.Load()
}

// receive pushes a given message to the actor mailbox
// and signals the receiveLoop to process it
func (proc *grainProcess) receive(message *grainRequest) {
	if proc.isRunning() {
		if err := proc.inbox.Enqueue(message); err != nil {
			proc.logger.Warn(err)
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
	var request *grainRequest
	for {
		if request != nil {
			releaseGrainRequest(request)
		}

		if request = proc.inbox.Dequeue(); request != nil {
			switch request.getMessage().(type) {
			case *goaktpb.PoisonPill:
				proc.handleSystemMessage(request)
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

func (proc *grainProcess) handleSystemMessage(request *grainRequest) {
	switch msg := request.getMessage().(type) {
	case *goaktpb.PoisonPill:
		err := proc.deactivate(context.Background())
		// send a response back to the sender when the request is synchronous
		if request.isSynchronous() {
			if err != nil {
				request.setError(err)
				return
			}
			request.setResponse(NewGrainResponse(new(goaktpb.NoMessage)))
			return
		}

		request.setError(err)

	default:
		proc.logger.Warnf("received unknown system message %T for Grain %s", msg, proc.identity.String())
	}
}

func (proc *grainProcess) handleRequest(request *grainRequest) {
	defer proc.recovery(request)
	proc.latestReceiveTime.Store(time.Now())

	// Handle synchronous requests
	if request.isSynchronous() {
		response, err := proc.grain.ReceiveSync(request.getContext(), request.getMessage())
		if err != nil {
			request.setError(err)
			return
		}
		request.setResponse(NewGrainResponse(response))
		return
	}

	// Handle asynchronous requests
	request.setError(proc.grain.ReceiveAsync(request.getContext(), request.getMessage()))
}

// recovery is called upon after message is processed
func (proc *grainProcess) recovery(received *grainRequest) {
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

// getGrain returns the Grain instance
func (proc *grainProcess) getGrain() Grain {
	return proc.grain
}
