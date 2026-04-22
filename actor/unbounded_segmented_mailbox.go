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
	"sync"
	"sync/atomic"
)

const segmentSize = 256

type segment struct {
	// writeIdx is incremented atomically by producers to reserve a slot
	writeIdx atomic.Uint64
	// deqIdx is advanced by the consumer. Atomic because the dispatcher pool
	// can hand ownership to a different worker goroutine between turns: the
	// outgoing worker may still read deqIdx in IsEmpty (via finishOrReclaim)
	// after reset() releases ownership but before the new worker's
	// TakeForProcessing establishes a happens-before on this field.
	deqIdx atomic.Uint64
	// next points to the next segment; set once by a producer when segment rolls over
	next atomic.Pointer[segment]
	// data holds the messages for this segment; atomic to coordinate producers/consumer
	data [segmentSize]atomic.Pointer[ReceiveContext]
}

var segmentPool = sync.Pool{New: func() any { return new(segment) }}

func newSegment() *segment {
	seg := segmentPool.Get().(*segment)
	seg.writeIdx.Store(0)
	seg.deqIdx.Store(0)
	seg.next.Store(nil)
	for i := range seg.data {
		seg.data[i].Store(nil)
	}
	return seg
}

// UnboundedSegmentedMailbox is an unbounded, lock‑free MPSC mailbox that
// stores messages in fixed‑size array segments connected in a singly linked
// list. It combines the cache locality of ring buffers with the growth
// characteristics of linked queues.
//
// Concurrency model
//   - MPSC: many producers can call Enqueue concurrently; exactly one consumer
//     must call Dequeue.
//
// Characteristics
//   - Unbounded: capacity grows by allocating and linking new fixed‑size
//     segments as needed.
//   - FIFO: dequeue order matches enqueue order across all producers.
//   - Hot‑path efficiency: producers reserve a slot by atomically incrementing a
//     segment write index and store directly into a cache‑friendly array slot;
//     the consumer reads sequentially via a dequeue index.
//   - Low GC pressure: segments are pooled; steady‑state traffic typically
//     performs zero allocations per message.
//   - Observability: IsEmpty is O(1); Len is an approximate atomic counter
//     (best‑effort under concurrency) and intended for metrics, not strict
//     synchronization.
//
// Use cases (in an actor framework)
//   - Fan‑in actors: aggregators, reducers, routers’ routees, topic hubs and
//     stream sinks that receive from many producers concurrently. The
//     segment‑locality reduces cache misses compared to list nodes.
//   - Ingestion/telemetry/logging actors: bursts of events followed by quick
//     processing. Segment pooling amortizes allocation spikes and keeps the hot
//     path mostly allocation‑free.
//   - Scheduling/dispatch actors: timers, batchers, or background workers that
//     consume quickly and benefit from contiguous array scans when draining.
//   - Broker/bridge actors: gateways that translate external messages (NATS,
//     gRPC streams, HTTP push) into local tells where short‑lived spikes are
//     common.
//
// Guidance
//   - Prefer this mailbox for high‑throughput, CPU‑bound actors with bursty
//     input and a single fast consumer. If you need strict backpressure or hard
//     limits, choose a bounded mailbox (BoundedMailbox for blocking).
//   - This mailbox is unbounded: pair it with upstream throttling, admission
//     control, or supervision strategies to avoid unbounded memory growth when
//     the consumer becomes slow.
type UnboundedSegmentedMailbox struct {
	head  atomic.Pointer[segment] // consumer advances; atomic for cross-worker visibility during dispatcher handoff
	_pad1 [64]byte
	tail  atomic.Pointer[segment] // producers modify via atomic ops
	_pad2 [64]byte

	length int64 // approximate size
}

// enforce compilation error
var _ Mailbox = (*UnboundedSegmentedMailbox)(nil)

// NewUnboundedSegmentedMailbox creates and initializes a
// UnboundedSegmentedMailbox.
//
// The mailbox starts with a single, pooled segment and grows by linking new
// segments as necessary. Choose this mailbox when you need an unbounded, fast
// MPSC queue with good cache locality and low allocation rates.
func NewUnboundedSegmentedMailbox() *UnboundedSegmentedMailbox {
	first := newSegment()
	m := &UnboundedSegmentedMailbox{}
	m.head.Store(first)
	m.tail.Store(first)
	return m
}

// Enqueue places the given value in the mailbox.
//
// Semantics
//   - Never blocks; always returns nil.
//   - Amortized O(1): usually a single atomic increment + a store into the
//     current segment; when a segment fills, one producer links the next
//     segment.
//
// Concurrency & ordering
//   - Safe for concurrent producers; each reserves a unique slot via atomic
//     increment of the tail segment write index.
//   - FIFO order is preserved across segment boundaries.
func (m *UnboundedSegmentedMailbox) Enqueue(value *ReceiveContext) error {
	for {
		tail := m.tail.Load()
		idx := tail.writeIdx.Add(1) - 1
		if idx < segmentSize {
			tail.data[idx].Store(value)
			atomic.AddInt64(&m.length, 1)
			return nil
		}
		// Segment is full; attempt to append a new one and retry
		next := tail.next.Load()
		if next == nil {
			newSeg := newSegment()
			// try to become the appender
			if tail.next.CompareAndSwap(nil, newSeg) {
				m.tail.CompareAndSwap(tail, newSeg)
			}
		} else {
			// help move the tail forward
			m.tail.CompareAndSwap(tail, next)
		}
		// retry on the new tail
	}
}

// Dequeue removes and returns the next value at the head of the mailbox.
//
// Semantics
//   - Returns nil if the mailbox is empty.
//   - Amortized O(1) for the single consumer: read from the current segment;
//     when a segment is drained, advance to the next pooled segment.
//
// Single‑consumer requirement
//   - Must be called from exactly one goroutine. Multiple consumers are not
//     supported and would violate internal invariants.
func (m *UnboundedSegmentedMailbox) Dequeue() *ReceiveContext {
	seg := m.head.Load()
	for {
		enq := min(seg.writeIdx.Load(), segmentSize)
		deq := seg.deqIdx.Load()
		if deq < enq {
			val := seg.data[deq].Load()
			if val == nil {
				// not yet published; treat as empty
				return nil
			}
			seg.data[deq].Store(nil)
			seg.deqIdx.Store(deq + 1)
			atomic.AddInt64(&m.length, -1)
			return val
		}
		// current segment is drained; move to next if available
		next := seg.next.Load()
		if next == nil {
			return nil
		}
		// recycle old head
		m.head.Store(next)
		seg.next.Store(nil)
		segmentPool.Put(seg)
		seg = next
	}
}

// IsEmpty reports whether the mailbox currently has no messages.
//
// It is an O(1) snapshot check. Under concurrency it is best‑effort and may
// briefly lag producers.
func (m *UnboundedSegmentedMailbox) IsEmpty() bool {
	seg := m.head.Load()
	enq := min(seg.writeIdx.Load(), segmentSize)
	if seg.deqIdx.Load() < enq {
		return false
	}
	return seg.next.Load() == nil
}

// Len returns a best‑effort snapshot of the mailbox length.
//
// The value is maintained as an atomic counter for observability and may be
// approximate under concurrency. Do not use it for flow‑control decisions where
// exactness is required.
func (m *UnboundedSegmentedMailbox) Len() int64 {
	return atomic.LoadInt64(&m.length)
}

// Dispose releases resources, if any. This mailbox does not spawn background
// goroutines; Dispose is currently a no‑op. Messages already enqueued remain in
// the queue until dequeued or garbage‑collected along with their segments.
func (m *UnboundedSegmentedMailbox) Dispose() {}
