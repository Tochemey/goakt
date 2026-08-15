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
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextPool_GetStampsHomeShard(t *testing.T) {
	pool := newContextPool()

	for shard := uint32(0); shard < contextShardCount; shard++ {
		ctx := pool.get(shard)
		require.NotNil(t, ctx)
		assert.Equal(t, shard&pool.mask, ctx.poolShard)
	}
}

func TestContextPool_GetMasksOutOfRangeShard(t *testing.T) {
	pool := newContextPool()

	ctx := pool.get(contextShardCount + 3)
	require.NotNil(t, ctx)
	assert.Equal(t, uint32(3), ctx.poolShard)
}

func TestContextPool_PutThenGetReusesContext(t *testing.T) {
	pool := newContextPool()

	first := pool.get(1)
	pool.put(first)

	second := pool.get(1)
	assert.Same(t, first, second)
}

func TestContextPool_PutReturnsToHomeShardOnly(t *testing.T) {
	pool := newContextPool()

	ctx := pool.get(2)
	pool.put(ctx)

	// The context went home to shard 2: any other shard stays empty and
	// must allocate a fresh context.
	other := pool.get(3)
	assert.NotSame(t, ctx, other)

	home := pool.get(2)
	assert.Same(t, ctx, home)
}

func TestContextPool_OverflowDropsForGC(t *testing.T) {
	pool := newContextPool()

	contexts := make([]*ReceiveContext, 0, contextShardCapacity+8)
	for range contextShardCapacity + 8 {
		ctx := pool.get(0)
		contexts = append(contexts, ctx)
	}

	// Returning more contexts than the shard holds must neither panic
	// nor corrupt the ring; the excess is dropped for the GC.
	for _, ctx := range contexts {
		pool.put(ctx)
	}

	seen := make(map[*ReceiveContext]bool, contextShardCapacity)
	for range contextShardCapacity {
		ctx := pool.get(0)
		require.NotNil(t, ctx)
		assert.False(t, seen[ctx], "pool handed out the same context twice")
		seen[ctx] = true
	}
}

func TestContextPool_Wraparound(t *testing.T) {
	pool := newContextPool()

	// Cycle several times the ring capacity through a single shard so
	// both cursors wrap; every get must keep returning a usable context.
	for range 3 * contextShardCapacity {
		ctx := pool.get(5)
		require.NotNil(t, ctx)
		require.Equal(t, uint32(5), ctx.poolShard)
		pool.put(ctx)
	}
}

func TestContextPool_ConcurrentGetPut(t *testing.T) {
	pool := newContextPool()

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
				ctx := pool.get(shard)
				if ctx == nil {
					t.Error("get returned nil")
					return
				}

				// Touch the object while held: the race detector flags
				// any context handed to two goroutines at once.
				ctx.requestID = "held"
				ctx.requestID = ""
				pool.put(ctx)
			}
		}(g)
	}

	wg.Wait()
}

func TestReceiveContext_ResetPreservesPoolShard(t *testing.T) {
	ctx := contextPool.get(4)
	ctx.responseClosed.Store(true)
	ctx.reset()
	assert.Equal(t, uint32(4), ctx.poolShard)
	assert.True(t, ctx.responseClosed.Load(), "reset leaves the late-reply guard set for build/cloneContext to clear")
}

func TestNextContextShard_CoversAllShards(t *testing.T) {
	seen := make(map[uint32]bool, contextShardCount)

	for range 2 * contextShardCount {
		shard := nextContextShard()
		require.Less(t, shard, contextShardCount)
		seen[shard] = true
	}

	assert.Len(t, seen, int(contextShardCount))
}

func TestContextPool_ContextHelpers(t *testing.T) {
	ctx := getContext(6)
	ctx.ctx = context.Background()
	ctx.message = "message"
	ctx.requestID = "request"

	clone := cloneContext(ctx)
	require.NotNil(t, clone)
	assert.Equal(t, ctx.ctx, clone.ctx)
	assert.Equal(t, ctx.message, clone.message)
	assert.Equal(t, ctx.requestID, clone.requestID)
	assert.Equal(t, ctx.poolShard, clone.poolShard)

	recycleContext(ctx)
	assert.Nil(t, ctx.ctx)
	assert.Nil(t, ctx.message)
	assert.Empty(t, ctx.requestID)
}

func TestCloneContext_ClearsResponseClosed(t *testing.T) {
	const shard uint32 = 0

	// The shard ring is FIFO and process-global. Drain leftovers from
	// earlier tests so cloneContext's get cannot pop a clean context
	// and hide a missing Store(false).
	for range contextShardCapacity {
		if contextPool.shards[shard].pop() == nil {
			break
		}
	}

	src := getContext(shard)
	src.ctx = context.Background()
	src.message = "stashed-ask"
	src.response = getResponseChannel()
	src.requestID = "corr"
	src.responseClosed.Store(true)

	stale := getContext(shard)
	stale.responseClosed.Store(true)
	recycleContext(stale)

	clone := cloneContext(src)
	require.Same(t, stale, clone)
	require.False(t, clone.responseClosed.Load())
	assert.Equal(t, src.ctx, clone.ctx)
	assert.Equal(t, src.message, clone.message)
	assert.Equal(t, src.response, clone.response)
	assert.Equal(t, src.requestID, clone.requestID)
	assert.True(t, src.responseClosed.Load(), "clone must not clear the source guard")
}
