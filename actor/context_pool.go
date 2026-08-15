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
	"runtime"
	"sync/atomic"
)

// contextShardCapacity is the number of pooled ReceiveContext slots per
// shard. Must be a power of two: the shard cursors index cells with a
// bitmask. Capacity bounds pooled memory, not throughput; an empty
// shard falls back to heap allocation and a full shard drops the
// context for the GC.
const contextShardCapacity = 512

// poolSpinLimit bounds the CAS retries a shard operation performs under
// contention before falling back (heap allocation on get, GC drop on
// put). The fallback is cheap, so the pool never spins unboundedly on a
// hot shard.
const poolSpinLimit = 4

// contextShardCount is the number of independent context-pool shards.
// Sized to at least twice GOMAXPROCS (floored at 8, capped at 128, and
// rounded up to a power of two for bitmask indexing) so concurrently
// active actors spread across shards instead of serializing on one
// process-wide pool.
var contextShardCount = func() uint32 {
	n := uint32(8)

	for n < uint32(2*runtime.GOMAXPROCS(0)) && n < 128 {
		n <<= 1
	}

	return n
}()

// contextPool is the sharded free list for ReceiveContext objects.
//
// The previous design was a single buffered channel shared by every
// actor in the process. Its channel lock serialized all senders and all
// dispatcher workers: profiling a pairwise Tell workload showed ~80% of
// CPU cycles inside that channel's send/recv, capping aggregate
// throughput at roughly the single-actor rate. Sharding by actor
// removes the global lock: each actor is assigned a home shard at
// spawn, contexts for that actor are taken from and returned to that
// shard, and unrelated actors therefore never contend on pool state.
var contextPool = newContextPool()

// contextShardCounter round-robins spawning actors across pool shards.
var contextShardCounter atomic.Uint32

// poolCell is one slot of a contextShard ring. seq is the sequence
// number that tells an arriving cursor whether the cell is empty or
// full; ctx is the pooled object and is valid only while seq marks the
// cell full. ctx is written before the seq store that publishes it and
// read after the seq load that observes it, so the plain field is
// ordered by the atomic.
type poolCell struct {
	seq atomic.Uint64
	ctx *ReceiveContext
}

// contextShard is a bounded multi-producer multi-consumer ring of spare
// ReceiveContexts (Vyukov's array-based MPMC queue). Per-cell sequence
// numbers make the ring ABA-safe without version-tagged pointers: a
// cursor's CAS can only claim a cell whose sequence matches the
// cursor's own position, so a stalled operation that resumes after the
// ring has wrapped fails its CAS instead of corrupting the ring.
type contextShard struct {
	// enqueuePos is the position of the next put; cells[enqueuePos&mask]
	// is where the next spare context is stored.
	enqueuePos atomic.Uint64
	_          CacheLinePadding

	// dequeuePos is the position of the next get. Padded away from
	// enqueuePos so getters and putters do not false-share a line.
	dequeuePos atomic.Uint64
	_          CacheLinePadding

	// cells is the ring storage. Length is a power of two.
	cells [contextShardCapacity]poolCell
}

// contextPoolShards aggregates the shards with the bitmask used to fold
// arbitrary shard hints into range.
type contextPoolShards struct {
	// shards holds contextShardCount independent rings.
	shards []contextShard
	// mask is len(shards)-1, applied to every incoming shard index.
	mask uint32
}

// newContextPool constructs the sharded pool with every cell marked
// empty. No contexts are pre-allocated: shards fill naturally as the
// dispatch path recycles, and a cold get is a plain heap allocation.
func newContextPool() *contextPoolShards {
	pool := &contextPoolShards{
		shards: make([]contextShard, contextShardCount),
		mask:   contextShardCount - 1,
	}

	for s := range pool.shards {
		cells := &pool.shards[s].cells
		for i := range cells {
			cells[i].seq.Store(uint64(i))
		}
	}

	return pool
}

// get pops a spare context from the shard identified by shard, falling
// back to a fresh allocation when the shard is empty or contended. The
// returned context carries its home shard in poolShard so put can
// return it to the same ring.
func (x *contextPoolShards) get(shard uint32) *ReceiveContext {
	shard &= x.mask
	if ctx := x.shards[shard].pop(); ctx != nil {
		return ctx
	}

	ctx := new(ReceiveContext)
	ctx.poolShard = shard
	return ctx
}

// put returns ctx to its home shard, dropping it for the GC when the
// shard is full or contended. The caller must have reset ctx and
// unlinked it from any mailbox.
func (x *contextPoolShards) put(ctx *ReceiveContext) {
	x.shards[ctx.poolShard&x.mask].push(ctx)
}

// pop removes and returns one context, or nil when the shard is empty
// or the spin budget is exhausted under contention. Safe for concurrent
// callers.
func (x *contextShard) pop() *ReceiveContext {
	pos := x.dequeuePos.Load()

	for range poolSpinLimit {
		cell := &x.cells[pos&(contextShardCapacity-1)]
		seq := cell.seq.Load()
		diff := int64(seq) - int64(pos+1)

		switch {
		case diff == 0:
			if x.dequeuePos.CompareAndSwap(pos, pos+1) {
				ctx := cell.ctx
				cell.ctx = nil
				cell.seq.Store(pos + contextShardCapacity)
				return ctx
			}

			pos = x.dequeuePos.Load()
		case diff < 0:
			return nil
		default:
			pos = x.dequeuePos.Load()
		}
	}

	return nil
}

// push stores ctx into the ring, silently dropping it when the ring is
// full or the spin budget is exhausted under contention. Safe for
// concurrent callers.
func (x *contextShard) push(ctx *ReceiveContext) {
	pos := x.enqueuePos.Load()

	for range poolSpinLimit {
		cell := &x.cells[pos&(contextShardCapacity-1)]
		seq := cell.seq.Load()
		diff := int64(seq) - int64(pos)

		switch {
		case diff == 0:
			if x.enqueuePos.CompareAndSwap(pos, pos+1) {
				cell.ctx = ctx
				cell.seq.Store(pos + 1)
				return
			}

			pos = x.enqueuePos.Load()
		case diff < 0:
			return
		default:
			pos = x.enqueuePos.Load()
		}
	}
}

// nextContextShard assigns a home pool shard to a spawning actor,
// round-robining so concurrently active actors land on distinct shards.
func nextContextShard() uint32 {
	return contextShardCounter.Add(1) & contextPool.mask
}

// getContext retrieves a ReceiveContext from the pool shard identified
// by shard (a target actor's ctxShard, or a recycled context's
// poolShard), falling back to a fresh allocation on empty/contended.
func getContext(shard uint32) *ReceiveContext {
	return contextPool.get(shard)
}

// recycleContext resets ctx and returns it to its home shard so a
// subsequent send to an actor on that shard reuses it instead of
// allocating. Mailboxes that do not use the intrusive sentinel scheme
// of UnboundedMailbox call this once the consumer has finished with a
// context (one dequeue after it was handed out, by which point the
// dispatcher has processed it). A full shard drops the context for GC.
// The next link is cleared so a context reused by a priority intake
// starts unlinked.
func recycleContext(ctx *ReceiveContext) {
	ctx.reset()
	atomic.StorePointer(&ctx.next, nil)
	contextPool.put(ctx)
}

// cloneContext returns a fresh ReceiveContext populated with src's
// message-scoped fields. Required when a context must enter a second
// mailbox: a ReceiveContext can be linked into only one mailbox at a
// time via its intrusive `next` field, so callers enqueue the clone
// and let the original complete its current lifecycle.
func cloneContext(src *ReceiveContext) *ReceiveContext {
	dst := getContext(src.poolShard)
	dst.ctx = src.ctx
	dst.message = src.message
	dst.sender = src.sender
	dst.self = src.self
	dst.response = src.response
	dst.requestID = src.requestID
	dst.requestReplyTo = src.requestReplyTo
	dst.err = src.err

	// reset() deliberately leaves responseClosed set, relying on build()
	// to clear it. Clones bypass build(), so a context recycled after a
	// completed Ask would arrive with the late-reply guard already
	// tripped and silently drop this message's response.
	dst.responseClosed.Store(false)
	return dst
}
