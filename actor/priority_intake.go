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

import (
	"sync/atomic"
	"unsafe"
)

// priorityIntake is a lock-free multi-producer, single-consumer (MPSC) Treiber
// stack. It is the producer-facing intake for the priority mailboxes: producers
// push concurrently with a single CAS and no allocation, using the intrusive
// ReceiveContext.next field as the link, while the single consumer bulk-detaches
// the whole batch and walks it in arrival order.
//
// Keeping producers off the heap removes the lock they would otherwise contend
// on: enqueue stays O(1) and lock-free, and the O(log n) heap work is paid only
// by the consumer that drains the intake before each dequeue.
type priorityIntake struct {
	head unsafe.Pointer // *ReceiveContext
}

// push adds ctx to the intake. Safe for concurrent producers. The ctx must not
// already be linked into any mailbox; its next field is overwritten.
func (s *priorityIntake) push(ctx *ReceiveContext) {
	for {
		old := atomic.LoadPointer(&s.head)
		atomic.StorePointer(&ctx.next, old)

		if atomic.CompareAndSwapPointer(&s.head, old, unsafe.Pointer(ctx)) {
			return
		}
	}
}

// drain detaches the entire intake and returns its head reordered to arrival
// order (the order in which producers pushed), or nil when empty. Only the
// single consumer may call drain. After it returns, the consumer exclusively
// owns the batch and may read or overwrite each node's next field.
func (s *priorityIntake) drain() *ReceiveContext {
	batch := atomic.SwapPointer(&s.head, nil)
	if batch == nil {
		return nil
	}

	// the stack is LIFO; reverse it in place to recover arrival order
	var prev *ReceiveContext
	cur := (*ReceiveContext)(batch)
	for cur != nil {
		next := (*ReceiveContext)(atomic.LoadPointer(&cur.next))
		atomic.StorePointer(&cur.next, unsafe.Pointer(prev))
		prev = cur
		cur = next
	}

	return prev
}

// chainNext returns the successor of a node in a drained chain.
func chainNext(n *ReceiveContext) *ReceiveContext {
	return (*ReceiveContext)(atomic.LoadPointer(&n.next))
}

// chainUnlink clears a node's link once it has been removed from a drained
// chain, so a node parked in the heap does not keep its former successor alive.
func chainUnlink(n *ReceiveContext) {
	atomic.StorePointer(&n.next, nil)
}
