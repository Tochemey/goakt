/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

import "time"

// SupervisorDirective defines the testSupervisor directive
type SupervisorDirective interface {
	isSupervisorDirective()
}

// StopDirective defines the testSupervisor stop directive
type StopDirective struct{}

// NewStopDirective creates an instance of StopDirective
func NewStopDirective() *StopDirective {
	return new(StopDirective)
}

func (*StopDirective) isSupervisorDirective() {}

// ResumeDirective defines the testSupervisor resume directive
// This ignores the failure and process the next message, instead
type ResumeDirective struct{}

// NewResumeDirective creates an instance of ResumeDirective
func NewResumeDirective() *ResumeDirective {
	return new(ResumeDirective)
}

func (*ResumeDirective) isSupervisorDirective() {}

// RestartDirective defines testSupervisor restart directive
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
