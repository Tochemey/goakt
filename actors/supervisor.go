/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

package actors

import (
	"context"
	"time"

	"github.com/flowchartsman/retry"
)

// supervisorDirective defines the supervisor directive
type supervisorDirective interface {
	isSupervisorDirective()
}

// StopDirective defines the supervisor stop directive
type StopDirective struct{}

// NewStopDirective creates an instance of StopDirective
func NewStopDirective() *StopDirective {
	return new(StopDirective)
}

func (*StopDirective) isSupervisorDirective() {}

// ResumeDirective defines the supervisor resume directive
// This ignores the failure and process the next message, instead
type ResumeDirective struct{}

// NewResumeDirective creates an instance of ResumeDirective
func NewResumeDirective() *ResumeDirective {
	return new(ResumeDirective)
}

func (*ResumeDirective) isSupervisorDirective() {}

// RestartDirective defines supervisor restart directive
type RestartDirective struct {
	// Specifies the maximum number of retries
	// When reaching this number the faulty actor is stopped
	maxNumRetries uint32
	// Specifies the time range to restart the faulty actor
	timeout time.Duration
}

// MaxNumRetries returns the max num retries
func (x *RestartDirective) MaxNumRetries() uint32 {
	return x.maxNumRetries
}

// Timeout returns the timeout
func (x *RestartDirective) Timeout() time.Duration {
	return x.timeout
}

// NewRestartDirective creates an instance of RestartDirective
func NewRestartDirective() *RestartDirective {
	return &RestartDirective{
		maxNumRetries: 0,
		timeout:       -1,
	}
}

// WithLimit sets the restart limit
func (x *RestartDirective) WithLimit(maxNumRetries uint32, timeout time.Duration) {
	x.maxNumRetries = maxNumRetries
	x.timeout = timeout
}

func (*RestartDirective) isSupervisorDirective() {}

// supervise watches for child actor's failure and act based upon the supervisory strategy
func (x *pid) supervise(cid PID, watcher *watcher) {
	for {
		select {
		case <-watcher.Done:
			x.logger.Debugf("stop watching cid=(%s)", cid.ActorPath().String())
			return
		case err := <-watcher.ErrChan:
			x.logger.Errorf("child actor=(%s) is failing: Err=%v", cid.ActorPath().String(), err)
			switch directive := x.supervisorDirective.(type) {
			case *StopDirective:
				x.handleStopDirective(cid)
			case *RestartDirective:
				x.handleRestartDirective(cid, directive.MaxNumRetries(), directive.Timeout())
			case *ResumeDirective:
				// pass
			default:
				x.handleStopDirective(cid)
			}
		}
	}
}

// handleStopDirective handles the supervisor stop directive
func (x *pid) handleStopDirective(cid PID) {
	x.UnWatch(cid)
	x.children.delete(cid.ActorPath())
	if err := cid.Shutdown(context.Background()); err != nil {
		// this can enter into some infinite loop if we panic
		// since we are just shutting down the actor we can just log the error
		// TODO: rethink properly about PostStop error handling
		x.logger.Error(err)
	}
}

// handleRestartDirective handles the supervisor restart directive
func (x *pid) handleRestartDirective(cid PID, maxRetries uint32, timeout time.Duration) {
	x.UnWatch(cid)
	ctx := context.Background()
	var err error
	if maxRetries == 0 || timeout <= 0 {
		err = cid.Restart(ctx)
	} else {
		// TODO: handle the initial delay
		retrier := retry.NewRetrier(int(maxRetries), 100*time.Millisecond, timeout)
		err = retrier.RunContext(ctx, cid.Restart)
	}

	if err != nil {
		x.logger.Error(err)
		// remove the actor and stop it
		x.children.delete(cid.ActorPath())
		if err := cid.Shutdown(ctx); err != nil {
			x.logger.Error(err)
		}
		return
	}
	x.Watch(cid)
}
