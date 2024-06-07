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

package future

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tochemey/goakt/v2/internal/types"
)

// ErrFutureTimeout is returned when the future times out
var ErrFutureTimeout = errors.New("future timeout")

// Result defines the future result
type Result[T any] interface {
	// Success returns the successful result of the future
	Success() T
	// Failure returns the error
	Failure() error
}

type result[T any] struct {
	success T
	failure error
}

// Success returns the successful result of the future
func (x *result[T]) Success() T {
	return x.success
}

// Failure returns the error
func (x *result[T]) Failure() error {
	return x.failure
}

// Future defines the future interface
type Future[T any] interface {
	// Await returns the result within an expected time period or the context is cancelled
	Await(deadline time.Duration) Result[T]
	// AwaitUninterruptible waits till the future is completed or the context is cancelled
	AwaitUninterruptible() Result[T]
	// Cancel cancels the future process
	Cancel()
}

type future[T any] struct {
	result *result[T]
	done   *types.Unit
	wait   chan types.Unit
	ctx    context.Context
	cancel func()
}

// New creates an instance of Future
func New[T any](ctx context.Context, fn func(context.Context) (T, error)) Future[T] {
	f := new(future[T])
	f.wait = make(chan types.Unit)
	f.ctx, f.cancel = context.WithCancel(ctx)
	f.result = new(result[T])

	go func() {
		defer func() {
			if r := recover(); r != nil {
				f.result.failure = fmt.Errorf("failed: %v", r)
			}
		}()

		success, err := fn(ctx)
		f.result.success = success
		f.result.failure = err

		f.done = &types.Unit{}
		f.wait <- types.Unit{}

		close(f.wait)
	}()

	return f
}

// Await returns the result within an expected time period
func (x future[T]) Await(deadline time.Duration) Result[T] {
	if x.done != nil {
		return x.result
	}

	select {
	case <-x.wait:
		return x.result
	case <-time.After(deadline):
		return &result[T]{failure: ErrFutureTimeout}
	case <-x.ctx.Done():
		return &result[T]{failure: x.ctx.Err()}
	}
}

// AwaitUninterruptible awaits till the future is completed
func (x future[T]) AwaitUninterruptible() Result[T] {
	if x.done != nil {
		return x.result
	}

	select {
	case <-x.wait:
		return x.result
	case <-x.ctx.Done():
		return &result[T]{failure: x.ctx.Err()}
	}
}

// Cancel cancels the future process
func (x future[T]) Cancel() {
	x.cancel()
}
