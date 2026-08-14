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
)

// grainContextPool is the sharded free list for GrainContext objects.
//
// The previous design was a single buffered channel shared by every grain
// in the process. Profiling the grain Tell benchmarks showed the same
// pathology the actor-side context pool fixed: the channel lock serialized
// every producer and every dispatcher worker, with the non-blocking pool
// puts (selectnbsend) accounting for the bulk of chansend time. Sharding
// by grain removes the global lock: each grain process is assigned a home
// shard at construction, contexts for that grain are taken from and
// returned to that shard, and unrelated grains therefore never contend on
// pool state.
//
// The pool is deliberately separate from the actor-side contextPool even
// though both share the ring geometry and the sizing constants
// (contextShardCapacity, poolSpinLimit, contextShardCount): keeping them
// apart isolates the actor hot path from any grain-side change and leaves
// room for grain-specific tuning, since grains recycle mostly from the
// mailbox dequeue side (sentinel reclaim), a different producer/consumer
// mix than actors.
var grainContextPool = newGrainContextPool()

// grainContextShardCounter round-robins activating grain processes across
// pool shards. Separate from contextShardCounter because it indexes a
// separate pool.
var grainContextShardCounter atomic.Uint32

// grainPoolCell is one slot of a grainContextShard ring. seq is the
// sequence number that tells an arriving cursor whether the cell is empty
// or full; gctx is the pooled object and is valid only while seq marks the
// cell full. gctx is written before the seq store that publishes it and
// read after the seq load that observes it, so the plain field is ordered
// by the atomic.
type grainPoolCell struct {
	seq  atomic.Uint64
	gctx *GrainContext
}

// grainContextShard is a bounded multi-producer multi-consumer ring of
// spare GrainContexts (Vyukov's array-based MPMC queue). Per-cell sequence
// numbers make the ring ABA-safe without version-tagged pointers: a
// cursor's CAS can only claim a cell whose sequence matches the cursor's
// own position, so a stalled operation that resumes after the ring has
// wrapped fails its CAS instead of corrupting the ring.
type grainContextShard struct {
	// enqueuePos is the position of the next put; cells[enqueuePos&mask]
	// is where the next spare context is stored.
	enqueuePos atomic.Uint64
	_          CacheLinePadding

	// dequeuePos is the position of the next get. Padded away from
	// enqueuePos so getters and putters do not false-share a line.
	dequeuePos atomic.Uint64
	_          CacheLinePadding

	// cells is the ring storage. Length is a power of two.
	cells [contextShardCapacity]grainPoolCell
}

// grainContextPoolShards aggregates the shards with the bitmask used to
// fold arbitrary shard hints into range.
type grainContextPoolShards struct {
	// shards holds contextShardCount independent rings.
	shards []grainContextShard
	// mask is len(shards)-1, applied to every incoming shard index.
	mask uint32
}

// newGrainContextPool constructs the sharded pool with every cell marked
// empty. No contexts are pre-allocated: shards fill naturally as the
// dispatch path recycles, and a cold get is a plain heap allocation.
func newGrainContextPool() *grainContextPoolShards {
	pool := &grainContextPoolShards{
		shards: make([]grainContextShard, contextShardCount),
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
// returned context carries its home shard in poolShard so put can return
// it to the same ring.
func (x *grainContextPoolShards) get(shard uint32) *GrainContext {
	shard &= x.mask
	if gctx := x.shards[shard].pop(); gctx != nil {
		return gctx
	}

	gctx := new(GrainContext)
	gctx.poolShard = shard
	return gctx
}

// put returns gctx to its home shard, dropping it for the GC when the
// shard is full or contended. The caller must have reset gctx and
// unlinked it from any mailbox.
func (x *grainContextPoolShards) put(gctx *GrainContext) {
	x.shards[gctx.poolShard&x.mask].push(gctx)
}

// pop removes and returns one context, or nil when the shard is empty or
// the spin budget is exhausted under contention. Safe for concurrent
// callers.
func (x *grainContextShard) pop() *GrainContext {
	pos := x.dequeuePos.Load()

	for range poolSpinLimit {
		cell := &x.cells[pos&(contextShardCapacity-1)]
		seq := cell.seq.Load()
		diff := int64(seq) - int64(pos+1)

		switch {
		case diff == 0:
			if x.dequeuePos.CompareAndSwap(pos, pos+1) {
				gctx := cell.gctx
				cell.gctx = nil
				cell.seq.Store(pos + contextShardCapacity)
				return gctx
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

// push stores gctx into the ring, silently dropping it when the ring is
// full or the spin budget is exhausted under contention. Safe for
// concurrent callers.
func (x *grainContextShard) push(gctx *GrainContext) {
	pos := x.enqueuePos.Load()

	for range poolSpinLimit {
		cell := &x.cells[pos&(contextShardCapacity-1)]
		seq := cell.seq.Load()
		diff := int64(seq) - int64(pos)

		switch {
		case diff == 0:
			if x.enqueuePos.CompareAndSwap(pos, pos+1) {
				cell.gctx = gctx
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

// nextGrainContextShard assigns a home pool shard to an activating grain
// process, round-robining so concurrently active grains land on distinct
// shards.
func nextGrainContextShard() uint32 {
	return grainContextShardCounter.Add(1) & grainContextPool.mask
}

// grainErrorChannelPool is the sharded free list for the grain Tell ack
// channels. Every Tell attaches a buffered capacity-1 error channel to its
// context and returns it after the ack; borrowing both from the process-wide
// errorCh pool put two global channel-lock crossings on every message, which
// profiling showed as the dominant remaining cost once contexts were
// sharded. Channels do not carry a home shard of their own: callers index
// the pool with the context's poolShard, so get and put stay on the same
// ring.
var grainErrorChannelPool = newGrainErrorChannelPool()

// grainErrorChannelCell is one slot of a grainErrorChannelShard ring. seq
// is the sequence number that tells an arriving cursor whether the cell is
// empty or full; ch is the pooled channel and is valid only while seq marks
// the cell full. ch is written before the seq store that publishes it and
// read after the seq load that observes it, so the plain field is ordered
// by the atomic.
type grainErrorChannelCell struct {
	seq atomic.Uint64
	ch  chan error
}

// grainErrorChannelShard is a bounded multi-producer multi-consumer ring of
// spare ack channels, using the same Vyukov geometry as grainContextShard.
type grainErrorChannelShard struct {
	// enqueuePos is the position of the next put; cells[enqueuePos&mask]
	// is where the next spare channel is stored.
	enqueuePos atomic.Uint64
	_          CacheLinePadding

	// dequeuePos is the position of the next get. Padded away from
	// enqueuePos so getters and putters do not false-share a line.
	dequeuePos atomic.Uint64
	_          CacheLinePadding

	// cells is the ring storage. Length is a power of two.
	cells [contextShardCapacity]grainErrorChannelCell
}

// grainErrorChannelPoolShards aggregates the shards with the bitmask used
// to fold arbitrary shard hints into range.
type grainErrorChannelPoolShards struct {
	// shards holds contextShardCount independent rings.
	shards []grainErrorChannelShard
	// mask is len(shards)-1, applied to every incoming shard index.
	mask uint32
}

// newGrainErrorChannelPool constructs the sharded pool with every cell
// marked empty. No channels are pre-allocated: shards fill naturally as
// Tell acks recycle, and a cold get is a plain make.
func newGrainErrorChannelPool() *grainErrorChannelPoolShards {
	pool := &grainErrorChannelPoolShards{
		shards: make([]grainErrorChannelShard, contextShardCount),
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

// pop removes and returns one channel, or nil when the shard is empty or
// the spin budget is exhausted under contention. Safe for concurrent
// callers.
func (x *grainErrorChannelShard) pop() chan error {
	pos := x.dequeuePos.Load()

	for range poolSpinLimit {
		cell := &x.cells[pos&(contextShardCapacity-1)]
		seq := cell.seq.Load()
		diff := int64(seq) - int64(pos+1)

		switch {
		case diff == 0:
			if x.dequeuePos.CompareAndSwap(pos, pos+1) {
				ch := cell.ch
				cell.ch = nil
				cell.seq.Store(pos + contextShardCapacity)
				return ch
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

// push stores ch into the ring, silently dropping it when the ring is full
// or the spin budget is exhausted under contention. Safe for concurrent
// callers.
func (x *grainErrorChannelShard) push(ch chan error) {
	pos := x.enqueuePos.Load()

	for range poolSpinLimit {
		cell := &x.cells[pos&(contextShardCapacity-1)]
		seq := cell.seq.Load()
		diff := int64(seq) - int64(pos)

		switch {
		case diff == 0:
			if x.enqueuePos.CompareAndSwap(pos, pos+1) {
				cell.ch = ch
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

// getGrainErrorChannel returns a buffered capacity-1 error channel from the
// shard identified by shard, allocating a fresh one on empty/contended.
// Used by the grain Tell path to carry the processed acknowledgment.
func getGrainErrorChannel(shard uint32) chan error {
	if ch := grainErrorChannelPool.shards[shard&grainErrorChannelPool.mask].pop(); ch != nil {
		return ch
	}
	return make(chan error, 1)
}

// putGrainErrorChannel drains ch and returns it to the shard identified by
// shard. Callers must pass the same shard the channel was fetched with (the
// owning context's poolShard) and must not return a channel a late ack
// could still reach: timeout paths abandon their channel to the GC exactly
// like the process-wide errorCh pool.
func putGrainErrorChannel(shard uint32, ch chan error) {
	drainErrorChannel(ch)
	grainErrorChannelPool.shards[shard&grainErrorChannelPool.mask].push(ch)
}

// getGrainContext retrieves a GrainContext from the pool shard identified
// by shard (a target grain's ctxShard), falling back to a fresh
// allocation on empty/contended.
func getGrainContext(shard uint32) *GrainContext {
	return grainContextPool.get(shard)
}

// releaseGrainContext resets gctx and returns it to its home shard so a
// subsequent send to a grain on that shard reuses it instead of
// allocating. Callers on failed-enqueue paths use this; the mailbox
// recycles dequeued sentinels itself. A full shard drops the context for
// GC. The next link is cleared so a reused context starts unlinked.
func releaseGrainContext(gctx *GrainContext) {
	gctx.reset()
	gctx.next.Store(nil)
	grainContextPool.put(gctx)
}
