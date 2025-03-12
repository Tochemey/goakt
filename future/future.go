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

package future

import (
	"context"
	"sync"

	"google.golang.org/protobuf/proto"
)

// Future represents a value which may or may not currently be available,
// but will be available at some point, or an error if that value could
// not be made available.
type Future interface {
	// Await blocks until the Future is completed or context is canceled and
	// returns either a result or an error.
	Await(context.Context) (proto.Message, error)

	// complete completes the Future with either a value or an error.
	// It is used by [completable] internally.
	complete(proto.Message, error)
}

// New returns a new Task associated with the specified function.
// It starts executing the task using a goroutine. It returns a
// Future which can be used to retrieve the result or error of the
// task when it is completed.
func New(fn func() (proto.Message, error)) Future {
	comp := newCompletable()
	go func() {
		result, err := fn()
		switch {
		case err == nil:
			comp.Success(result)
		default:
			comp.Failure(err)
		}
	}()
	return comp.Future()
}

// future implements the Future interface.
type future struct {
	acceptOnce   sync.Once
	completeOnce sync.Once
	done         chan any
	value        proto.Message
	err          error
}

// Verify future satisfies the Future interface.
var _ Future = (*future)(nil)

// newFuture returns a new Future.
func newFuture() Future {
	return &future{
		done: make(chan any, 1),
	}
}

// wait blocks once, until the Future result is available or until
// the context is canceled.
func (x *future) wait(ctx context.Context) {
	x.acceptOnce.Do(func() {
		select {
		case result := <-x.done:
			x.setResult(result)
		case <-ctx.Done():
			x.setResult(ctx.Err())
		}
	})
}

// setResult assigns a value to the Future instance.
func (x *future) setResult(result any) {
	switch value := result.(type) {
	case error:
		x.err = value
	default:
		x.value = value.(proto.Message)
	}
}

// Await blocks until the Future is completed or context is canceled and
// returns either a result or an error.
func (x *future) Await(ctx context.Context) (proto.Message, error) {
	x.wait(ctx)
	return x.value, x.err
}

// complete completes the Future with either a value or an error.
func (x *future) complete(value proto.Message, err error) {
	x.completeOnce.Do(func() {
		if err != nil {
			x.done <- err
		} else {
			x.done <- value
		}
	})
}

// completable represents a writable, single-assignment container,
// which completes a Future.
type completable interface {
	// Success completes the underlying Future with a value.
	Success(proto.Message)

	// Failure fails the underlying Future with an error.
	Failure(error)

	// Future returns the underlying Future.
	Future() Future
}

// completer implements the completable interface.
type completer struct {
	once   sync.Once
	future Future
}

// Verify completer satisfies the completable interface.
var _ completable = (*completer)(nil)

// newCompletable returns a new completable.
func newCompletable() completable {
	return &completer{
		future: newFuture(),
	}
}

// Success completes the underlying Future with a given value.
func (p *completer) Success(value proto.Message) {
	p.once.Do(func() {
		p.future.complete(value, nil)
	})
}

// Failure fails the underlying Future with a given error.
func (p *completer) Failure(err error) {
	p.once.Do(func() {
		var zero proto.Message
		p.future.complete(zero, err)
	})
}

// Future returns the underlying Future.
func (p *completer) Future() Future {
	return p.future
}
