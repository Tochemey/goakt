/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package workerpool

import "time"

// Option is the interface that applies a WorkerPool option.
type Option interface {
	// Apply sets the Option value of a WorkerPool.
	Apply(pool *WorkerPool)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(pool *WorkerPool)

// Apply applies the Node's option
func (f OptionFunc) Apply(pool *WorkerPool) {
	f(pool)
}

// WithPassivateAfter sets the passivate after duration
func WithPassivateAfter(d time.Duration) Option {
	return OptionFunc(func(pool *WorkerPool) {
		pool.passivateAfter = d
	})
}

// WithNumShards sets the number of shards
func WithNumShards(numShards int) Option {
	return OptionFunc(func(pool *WorkerPool) {
		if numShards > maxShards {
			numShards = maxShards
		}
		pool.numShards = numShards
	})
}
