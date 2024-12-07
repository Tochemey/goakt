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

package timer

import (
	"sync"
	"time"
)

// Pool defines a timer pool
type Pool struct {
	pool sync.Pool
}

// NewPool creates an instance of Pool
func NewPool() *Pool {
	return &Pool{
		pool: sync.Pool{
			New: func() interface{} {
				return time.NewTimer(5 * time.Second)
			},
		},
	}
}

// Get returns a timer
func (tp *Pool) Get(timeout time.Duration) *time.Timer {
	timer := tp.pool.Get().(*time.Timer)
	if !timer.Stop() {
		// Drain the channel to reset the timer
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(timeout)
	return timer
}

// Put the timer back to the pool
func (tp *Pool) Put(timer *time.Timer) {
	if !timer.Stop() {
		// Drain the channel to avoid leaks
		select {
		case <-timer.C:
		default:
		}
	}
	tp.pool.Put(timer)
}
