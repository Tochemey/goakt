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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGrainContextPool_GetStampsHomeShard(t *testing.T) {
	pool := newGrainContextPool()

	for shard := uint32(0); shard < contextShardCount; shard++ {
		gctx := pool.get(shard)
		require.NotNil(t, gctx)
		assert.Equal(t, shard&pool.mask, gctx.poolShard)
	}
}

func TestGrainContextPool_GetMasksOutOfRangeShard(t *testing.T) {
	pool := newGrainContextPool()

	gctx := pool.get(contextShardCount + 3)
	require.NotNil(t, gctx)
	assert.Equal(t, uint32(3), gctx.poolShard)
}

func TestGrainContextPool_PutThenGetReusesContext(t *testing.T) {
	pool := newGrainContextPool()

	first := pool.get(1)
	pool.put(first)

	second := pool.get(1)
	assert.Same(t, first, second)
}

func TestGrainContextPool_PutReturnsToHomeShardOnly(t *testing.T) {
	pool := newGrainContextPool()

	gctx := pool.get(2)
	pool.put(gctx)

	// The context went home to shard 2: any other shard stays empty and
	// must allocate a fresh context.
	other := pool.get(3)
	assert.NotSame(t, gctx, other)

	home := pool.get(2)
	assert.Same(t, gctx, home)
}

func TestGrainContextPool_OverflowDropsForGC(t *testing.T) {
	pool := newGrainContextPool()

	contexts := make([]*GrainContext, 0, contextShardCapacity+8)
	for range contextShardCapacity + 8 {
		gctx := pool.get(0)
		contexts = append(contexts, gctx)
	}

	// Returning more contexts than the shard holds must neither panic
	// nor corrupt the ring; the excess is dropped for the GC.
	for _, gctx := range contexts {
		pool.put(gctx)
	}

	seen := make(map[*GrainContext]bool, contextShardCapacity)
	for range contextShardCapacity {
		gctx := pool.get(0)
		require.NotNil(t, gctx)
		assert.False(t, seen[gctx], "pool handed out the same context twice")
		seen[gctx] = true
	}
}

func TestGrainContextPool_Wraparound(t *testing.T) {
	pool := newGrainContextPool()

	// Cycle several times the ring capacity through a single shard so
	// both cursors wrap; every get must keep returning a usable context.
	for range 3 * contextShardCapacity {
		gctx := pool.get(5)
		require.NotNil(t, gctx)
		require.Equal(t, uint32(5), gctx.poolShard)
		pool.put(gctx)
	}
}

func TestGrainContextPool_ConcurrentGetPut(t *testing.T) {
	pool := newGrainContextPool()

	const goroutines = 8
	const iterations = 10_000

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := range goroutines {
		go func(g int) {
			defer wg.Done()

			// Half the goroutines hammer one shared shard, the other
			// half spread across shards, covering both the contended
			// and the partitioned paths.
			shard := uint32(0)
			if g%2 == 1 {
				shard = uint32(g)
			}

			for range iterations {
				gctx := pool.get(shard)
				if gctx == nil {
					t.Error("get returned nil")
					return
				}

				// Touch the object while held: the race detector flags
				// any context handed to two goroutines at once.
				gctx.requestID = "held"
				gctx.requestID = ""
				pool.put(gctx)
			}
		}(g)
	}

	wg.Wait()
}

func TestGrainContext_ResetPreservesPoolShard(t *testing.T) {
	gctx := grainContextPool.get(4)
	gctx.reset()
	assert.Equal(t, uint32(4), gctx.poolShard)
}

func TestNextGrainContextShard_CoversAllShards(t *testing.T) {
	seen := make(map[uint32]bool, contextShardCount)

	for range 2 * contextShardCount {
		shard := nextGrainContextShard()
		require.Less(t, shard, contextShardCount)
		seen[shard] = true
	}

	assert.Len(t, seen, int(contextShardCount))
}

func TestGrainContextPool_ReleaseReturnsToHomeShard(t *testing.T) {
	gctx := getGrainContext(6)
	gctx.message = "message"
	gctx.requestID = "request"

	releaseGrainContext(gctx)
	assert.Nil(t, gctx.message)
	assert.Empty(t, gctx.requestID)
	assert.Nil(t, gctx.next.Load())

	reused := getGrainContext(6)
	assert.Same(t, gctx, reused)
}

func TestGrainErrorChannelPool_PutThenGetReusesChannel(t *testing.T) {
	first := make(chan error, 1)
	putGrainErrorChannel(9, first)

	second := getGrainErrorChannel(9)
	assert.Equal(t, first, second)
}

func TestGrainErrorChannelPool_PutDrainsStaleError(t *testing.T) {
	ch := make(chan error, 1)
	ch <- assert.AnError

	putGrainErrorChannel(10, ch)

	reused := getGrainErrorChannel(10)
	require.Equal(t, ch, reused)
	require.Empty(t, reused)
}

func TestGrainErrorChannelPool_ShardsAreIndependent(t *testing.T) {
	ch := make(chan error, 1)
	putGrainErrorChannel(11, ch)

	// A different shard stays empty and must allocate a fresh channel.
	other := getGrainErrorChannel(12)
	assert.NotEqual(t, ch, other)

	home := getGrainErrorChannel(11)
	assert.Equal(t, ch, home)
}

func TestGrainErrorChannelPool_GetAllocatesOnEmptyShard(t *testing.T) {
	pool := newGrainErrorChannelPool()

	ch := pool.shards[0].pop()
	assert.Nil(t, ch)

	fresh := getGrainErrorChannel(uint32(len(pool.shards)) + 1)
	require.NotNil(t, fresh)
	assert.Equal(t, 1, cap(fresh))
}

func TestGrainErrorChannelPool_ConcurrentGetPut(t *testing.T) {
	const goroutines = 8
	const iterations = 10_000

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := range goroutines {
		go func(g int) {
			defer wg.Done()

			// Half the goroutines hammer one shared shard, the other
			// half spread across shards, covering both the contended
			// and the partitioned paths.
			shard := uint32(0)
			if g%2 == 1 {
				shard = uint32(g)
			}

			for range iterations {
				ch := getGrainErrorChannel(shard)
				if ch == nil {
					t.Error("get returned nil")
					return
				}

				// Exercise the channel while held: the race detector
				// flags any channel handed to two goroutines at once.
				ch <- assert.AnError
				putGrainErrorChannel(shard, ch)
			}
		}(g)
	}

	wg.Wait()
}

func TestGrainMailbox_DequeueRecyclesSentinelToHomeShard(t *testing.T) {
	// Drain shard 7 so the assertion below cannot be satisfied by a
	// leftover context from another test.
	for range contextShardCapacity {
		if grainContextPool.shards[7].pop() == nil {
			break
		}
	}

	mailbox := newGrainMailbox(0)
	gctx := getGrainContext(7)
	require.NoError(t, mailbox.Enqueue(gctx))

	// Dequeue hands out gctx as the new sentinel; a second enqueue and
	// dequeue retire it, which must recycle it into shard 7.
	require.Same(t, gctx, mailbox.Dequeue())
	require.NoError(t, mailbox.Enqueue(getGrainContext(7)))
	require.NotNil(t, mailbox.Dequeue())

	// Background goroutines from sibling tests may recycle their own
	// contexts into shard 7; scan the shard instead of expecting gctx at
	// the head.
	var found bool

	for range contextShardCapacity {
		popped := grainContextPool.shards[7].pop()
		if popped == nil {
			break
		}

		if popped == gctx {
			found = true
			break
		}
	}

	assert.True(t, found, "retired sentinel was not recycled into its home shard")
}
