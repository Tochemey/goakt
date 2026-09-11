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

package actor

import "runtime"

// embeddedGrainMailbox is the default user mailbox of a grain, run on two words
// carried inside the process (mailboxHead and mailboxTail) instead of on a
// standalone grainMailbox. It is the same lock-free multi-producer,
// single-consumer FIFO: producers swap the tail and link, the processing turn
// advances the head, and the retained head is the sentinel. Unlike grainMailbox
// it keeps no length counter: it is never bounded, and emptiness is read from
// the head's link.
//
// The type is a defined type over grainPID so (*embeddedGrainMailbox)(pid) is a
// zero-allocation pointer reinterpretation. Placement of the two words belongs
// to the grainPID layout: the head sits on the turn-written line and the tail on
// the producer-written line, which is why they need no padding of their own. A
// grain with a bounded mailbox never uses this type; its mailbox pointer names a
// standalone grainMailbox instead.
type embeddedGrainMailbox grainPID

// Enqueue appends value at the tail. Safe for concurrent producers; value must
// not be linked into any mailbox, its next link is overwritten.
func (x *embeddedGrainMailbox) Enqueue(value *GrainContext) {
	value.next.Store(nil)
	prev := x.mailboxTail.Swap(value)
	prev.next.Store(value)
}

// Dequeue removes and returns the next GrainContext, or nil when empty. Single
// consumer only. The returned context becomes the new sentinel and must not be
// released by the caller; the previous sentinel is reset and recycled into the
// shared pool here.
func (x *embeddedGrainMailbox) Dequeue() *GrainContext {
	head := x.mailboxHead.Load()
	next := head.next.Load()

	// Avoid spurious empty: a producer may have swapped tail but not linked yet.
	if next == nil {
		if head == x.mailboxTail.Load() {
			return nil
		}

		for next == nil {
			runtime.Gosched()
			next = head.next.Load()
		}
	}

	x.mailboxHead.Store(next)
	head.next.Store(nil)
	head.reset()
	grainContextPool.put(head)

	return next
}

// IsEmpty reports whether the mailbox holds no message. A racy snapshot, safe
// only from the consumer.
func (x *embeddedGrainMailbox) IsEmpty() bool {
	return x.mailboxHead.Load().next.Load() == nil
}

// Len returns an approximate count of queued messages by walking the list.
// O(n) and racy with producers; for tests and diagnostics, never hot paths.
func (x *embeddedGrainMailbox) Len() int64 {
	var count int64
	current := x.mailboxHead.Load().next.Load()

	for current != nil {
		count++
		current = current.next.Load()
	}

	return count
}
