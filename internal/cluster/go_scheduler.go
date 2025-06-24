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

package cluster

// goScheduler provides a lightweight abstraction for scheduling functions
// to run asynchronously in separate goroutines. It allows configuration
// of a throughput value, which can be used by consumers to control
// cooperative yielding or batching of work.
type goScheduler struct {
	// throughput defines the number of messages or tasks to process
	// before yielding control. This value is advisory and can be used
	// by consumers to implement cooperative scheduling.
	throughput int
}

// Schedule executes the provided function asynchronously in its own goroutine.
// This method is non-blocking and returns immediately.
//
// Example usage:
//
//	scheduler := newGoScheduler(100)
//	scheduler.Schedule(func() {
//	    // do some work
//	})
func (s *goScheduler) Schedule(fn func()) {
	go fn()
}

// Throughput returns the configured throughput value for this scheduler.
// Throughput typically represents the number of tasks to process before
// yielding control, and can be used to tune cooperative scheduling behavior.
func (s *goScheduler) Throughput() int {
	return s.throughput
}

// newGoScheduler creates and returns a new instance of goScheduler with the
// specified throughput capacity. The throughput parameter is advisory and
// can be used by consumers to control batching or yielding behavior.
//
// Example:
//
//	scheduler := newGoScheduler(100)
func newGoScheduler(capacity int) *goScheduler {
	return &goScheduler{throughput: capacity}
}
