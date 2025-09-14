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

package actor

// Mailbox defines the contract for an actor's message queue.
//
// Concurrency and ordering
//   - Implementations MUST be thread-safe for multiple concurrent producers
//     calling Enqueue.
//   - The actor runtime consumes from a single goroutine, so implementations
//     SHOULD optimize Dequeue for a single consumer (MPSC). If an implementation
//     supports multiple consumers, it MUST document the semantics explicitly.
//   - The default expectation is FIFO ordering. Specialized mailboxes (e.g.,
//     PriorityMailBox) may use a different ordering, which MUST be documented
//     by the implementation.
//
// Non-blocking behavior
//   - Enqueue SHOULD be non-blocking. Bounded implementations MUST return an
//     error when full instead of blocking. Unbounded implementations SHOULD
//     always return nil.
//   - Dequeue SHOULD be non-blocking and return nil when the mailbox is empty.
//     The actor runtime typically polls Dequeue in a loop.
//
// Observability
//   - IsEmpty SHOULD be an O(1) snapshot check and safe under concurrency.
//   - Len returns a snapshot size for observability/metrics. It MAY be
//     approximate under concurrency. O(1) is preferable; an O(n) traversal is
//     acceptable where tracking exact size would penalize the hot path.
//
// Resource management
//   - Dispose MUST release any resources and unblock any internal waiters used
//     by the implementation. After Dispose, Enqueue SHOULD fail and Dequeue
//     SHOULD return nil.
//
// Memory visibility
//   - Implementations MUST ensure that writes performed by producers before
//     Enqueue are visible to the consumer after Dequeue. Use atomic operations
//     or appropriate locking to establish happens-before edges.
type Mailbox interface {
	// Enqueue pushes a message into the mailbox.
	//
	// Semantics
	// - Bounded queues MUST return an error when full.
	// - Unbounded queues SHOULD never return an error.
	// - SHOULD be safe for concurrent calls by multiple producers.
	Enqueue(msg *ReceiveContext) error
	// Dequeue fetches a message from the mailbox.
	//
	// Semantics
	// - SHOULD return nil when the mailbox is empty (non-blocking).
	// - Intended to be called by a single consumer goroutine unless otherwise documented.
	Dequeue() (msg *ReceiveContext)
	// IsEmpty reports whether the mailbox currently has no messages.
	// This is a best-effort snapshot under concurrency.
	IsEmpty() bool
	// Len returns a snapshot of the number of messages in the mailbox.
	// Implementations MAY return an approximate value under concurrency.
	Len() int64
	// Dispose releases any resources and unblocks internal waiters used by the
	// implementation. The mailbox MUST NOT be used after Dispose returns.
	Dispose()
}
