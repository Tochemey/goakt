// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package stream

// queue is an unbounded FIFO buffer of pipeline elements.
//
// It uses a head-index cursor instead of re-slicing so that popped slots are
// zeroed immediately, allowing the GC to collect the underlying values without
// keeping the entire backing array alive. The backing array is compacted once
// the dead prefix exceeds half its length, amortizing the copy cost.
//
// queue is not safe for concurrent use; all access must occur within a single
// actor's Receive goroutine.
type queue struct {
	data []any
	head int
}

// push appends v to the tail of the queue.
func (q *queue) push(v any) {
	q.data = append(q.data, v)
}

// pop removes and returns the front element.
// The caller must check empty before calling pop.
func (q *queue) pop() any {
	v := q.data[q.head]
	q.data[q.head] = nil // release reference so the GC can collect the value
	q.head++
	// Compact when the dead prefix has grown to at least half the slice length.
	if q.head*2 >= len(q.data) {
		n := copy(q.data, q.data[q.head:])
		// Zero the vacated tail before shrinking so references are not pinned.
		for i := n; i < len(q.data); i++ {
			q.data[i] = nil
		}
		q.data = q.data[:n]
		q.head = 0
	}
	return v
}

// peek returns the front element without removing it.
// The caller must check empty before calling peek.
func (q *queue) peek() any {
	return q.data[q.head]
}

// len returns the number of elements available for consumption.
func (q *queue) len() int {
	return len(q.data) - q.head
}

// empty reports whether the queue contains no elements.
func (q *queue) empty() bool {
	return q.head >= len(q.data)
}
