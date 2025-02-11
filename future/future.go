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

// Task represents a long-running asynchronous operation that produces a single result.
//
// It is defined as a receive-only channel of proto.Message, ensuring that
// consumers can only read from it. Once the task completes, it sends exactly
// one result, which represents the successful outcome of the operation.
//
// This allows for efficient, non-blocking handling of asynchronous tasks
// while ensuring that only one result is ever produced.
type Task <-chan proto.Message

// Future represents a single-assignment, read-many synchronization primitive
// for handling asynchronous task execution.
//
// A Future is created in an incomplete state and is completed once with a result.
// Multiple listeners can wait on the Future by calling Result, which blocks
// until the Future is resolved.
//
// Unlike channels, a Future guarantees that:
//   - It is completed only once.
//   - All listeners receive the same result, regardless of when they start listening.
//
// This makes Future useful for scenarios where a single computation result
// needs to be propagated to multiple consumers efficiently.
type Future struct {
	triggered bool // because result can technically be nil and still be valid
	result    *Result
	lock      sync.Mutex
	wg        sync.WaitGroup
}

// New creates a Future to execute the given Task asynchronously with a specified timeout.
//
// The provided Task will be executed in the background, and the resulting Future
// can be used to retrieve the task's outcome. If the task does not complete
// within the specified timeout, the Future will be completed with an error.
//
// Parameters:
//   - task: The Task to be executed asynchronously.
//   - timeout: The maximum duration to wait for task completion before timing out.
//
// Returns:
//   - A pointer to a Future that can be used to retrieve the result of the task.
//
// This function ensures that listeners on the returned Future will receive
// the task's result once available, or an error if the execution exceeds the timeout.
func New(task Task, timeout time.Duration) *Future {
	f := new(Future)
	f.wg.Add(1)
	var wg sync.WaitGroup
	wg.Add(1)
	go waitTimeout(f, task, timeout, &wg)
	wg.Wait()
	return f
}

// WithContext creates a Future that executes the given Task asynchronously,
// respecting the provided context for cancellation.
//
// If the context is canceled before the Task completes, the Future will be
// completed with an error. Otherwise, the Future will store the Task's result
// once it becomes available.
//
// Parameters:
//   - ctx: The context that controls the lifetime of the Future. If canceled, the Future fails.
//   - task: The Task to be executed asynchronously.
//
// Returns:
//   - A pointer to a Future that can be used to retrieve the result of the task.
//
// This function ensures that listeners on the returned Future will either receive
// the Task's result or an error if the context is canceled before completion.
func WithContext(ctx context.Context, task Task) *Future {
	f := new(Future)
	f.wg.Add(1)
	var wg sync.WaitGroup
	wg.Add(1)
	go wait(ctx, f, task, &wg)
	wg.Wait()
	return f
}

// Result represents the outcome of a completed Future.
//
// It encapsulates both the success and failure states, ensuring that
// a Future can communicate either a valid result or an error.
//
// If the task succeeds, Success returns the result, and Failure returns nil.
// If the task fails, Failure returns the error, and Success returns nil.
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
