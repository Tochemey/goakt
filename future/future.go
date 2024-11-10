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

// Package future
// Most of the code here has been copied from (https://github.com/Workiva/go-datastructures) with a slight modification
package future

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

// ErrTimeout is returned when the future has timed out
var ErrTimeout = func(duration time.Duration) error { return fmt.Errorf(`timeout after %f seconds`, duration.Seconds()) }

// Task defines the successful outcome of a long-running task
type Task <-chan proto.Message

// Result defines the future result.
// It holds both the success and error result
type Result struct {
	success proto.Message
	failure error
}

// Success returns the future success result
func (x *Result) Success() proto.Message {
	return x.success
}

// Failure returns the future error result
func (x *Result) Failure() error {
	return x.failure
}

// Future represents an object that can be used to perform asynchronous
// tasks.  The constructor of the future will complete it, and listeners
// will block on Result until a result is received.  This is different
// from a channel in that the future is only completed once, and anyone
// listening on the future will get the result, regardless of the number
// of listeners.
type Future struct {
	triggered bool // because result can technically be nil and still be valid
	result    *Result
	lock      sync.Mutex
	wg        sync.WaitGroup
}

// New creates an instance of Future that will time out when the timeout is hit
func New(task Task, timeout time.Duration) *Future {
	f := new(Future)
	f.wg.Add(1)
	var wg sync.WaitGroup
	wg.Add(1)
	go waitTimeout(f, task, timeout, &wg)
	wg.Wait()
	return f
}

// NewWithContext creates an instance of Future with a context.
// The future will time out when the given context is canceled before the response is received
func NewWithContext(ctx context.Context, task Task) *Future {
	f := new(Future)
	f.wg.Add(1)
	var wg sync.WaitGroup
	wg.Add(1)
	go wait(ctx, f, task, &wg)
	wg.Wait()
	return f
}

// Result will immediately fetch the result if it exists
// or wait on the result until it is ready.
func (f *Future) Result() *Result {
	f.lock.Lock()
	if f.triggered {
		f.lock.Unlock()
		return f.result
	}
	f.lock.Unlock()

	f.wg.Wait()
	return f.result
}

// HasResult will return true iff the result exists
func (f *Future) HasResult() bool {
	f.lock.Lock()
	hasResult := f.triggered
	f.lock.Unlock()
	return hasResult
}

// setResult sets the completed future result
func (f *Future) setResult(success proto.Message, err error) {
	f.lock.Lock()
	f.triggered = true
	f.result = &Result{
		success: success,
		failure: err,
	}
	f.lock.Unlock()
	f.wg.Done()
}

// waitTimeout waits for the result or times out
func waitTimeout(f *Future, ch Task, timeout time.Duration, wg *sync.WaitGroup) {
	wg.Done()
	t := time.NewTimer(timeout)
	select {
	case success := <-ch:
		f.setResult(success, nil)
		t.Stop()
	case <-t.C:
		var _nil proto.Message
		f.setResult(_nil, ErrTimeout(timeout))
	}
}

// wait waits for the result until the provided context is canceled
func wait(ctx context.Context, f *Future, ch Task, wg *sync.WaitGroup) {
	wg.Done()
	select {
	case success := <-ch:
		f.setResult(success, nil)
	case <-ctx.Done():
		var _nil proto.Message
		f.setResult(_nil, ctx.Err())
	}
}
