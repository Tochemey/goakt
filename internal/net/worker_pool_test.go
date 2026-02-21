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

package net

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/pause"
)

func TestWorkerPool_BasicLifecycle(t *testing.T) {
	var count atomic.Int64
	wp := NewWorkerPool(func(_ int) {
		count.Add(1)
	})
	wp.Start()

	for i := 0; i < 100; i++ {
		require.NoError(t, wp.AddTask(i))
	}

	require.Eventually(t, func() bool {
		return count.Load() == 100
	}, 2*time.Second, 5*time.Millisecond)

	wp.Stop()
}

func TestWorkerPool_SetNumShards(t *testing.T) {
	wp := NewWorkerPool(func(_ int) {})

	t.Run("valid value", func(t *testing.T) {
		wp.SetNumShards(4)
		require.Equal(t, 4, wp.numShards)
	})

	t.Run("clamped to 1 when zero", func(t *testing.T) {
		wp.SetNumShards(0)
		require.Equal(t, 1, wp.numShards)
	})

	t.Run("clamped to 1 when negative", func(t *testing.T) {
		wp.SetNumShards(-5)
		require.Equal(t, 1, wp.numShards)
	})

	t.Run("clamped to maxShards", func(t *testing.T) {
		wp.SetNumShards(1000)
		require.Equal(t, maxShards, wp.numShards)
	})

	t.Run("no-op after Start", func(t *testing.T) {
		wp.SetNumShards(4)
		require.Equal(t, 4, wp.numShards)
		wp.Start()
		wp.SetNumShards(2)
		require.Equal(t, 4, wp.numShards) // unchanged
		wp.Stop()
	})
}

func TestWorkerPool_SetIdleWorkerLifetime(t *testing.T) {
	wp := NewWorkerPool(func(_ int) {})

	t.Run("before start", func(t *testing.T) {
		wp.SetIdleWorkerLifetime(5 * time.Second)
		require.Equal(t, 5*time.Second, wp.idleWorkerLifetime)
	})

	t.Run("no-op after start", func(t *testing.T) {
		wp.Start()
		wp.SetIdleWorkerLifetime(10 * time.Second)
		require.Equal(t, 5*time.Second, wp.idleWorkerLifetime) // unchanged
		wp.Stop()
	})
}

func TestWorkerPool_GetSpawnedWorkers(t *testing.T) {
	barrier := make(chan struct{})
	wp := NewWorkerPool(func(_ int) {
		<-barrier
	})
	wp.SetNumShards(1)
	wp.Start()

	for i := 0; i < 5; i++ {
		require.NoError(t, wp.AddTask(i))
	}

	require.Eventually(t, func() bool {
		return wp.GetSpawnedWorkers() == 5
	}, 2*time.Second, 5*time.Millisecond)

	close(barrier)

	require.Eventually(t, func() bool {
		return wp.GetSpawnedWorkers() >= 0
	}, 2*time.Second, 5*time.Millisecond)

	wp.Stop()
}

func TestWorkerPool_AddTask_Errors(t *testing.T) {
	t.Run("not started", func(t *testing.T) {
		wp := NewWorkerPool(func(_ int) {})
		err := wp.AddTask(1)
		require.ErrorIs(t, err, ErrPoolNotStarted)
	})

	t.Run("stopped", func(t *testing.T) {
		wp := NewWorkerPool(func(_ int) {})
		wp.Start()
		wp.Stop()
		err := wp.AddTask(1)
		require.ErrorIs(t, err, ErrPoolStopped)
	})
}

func TestWorkerPool_AddTaskForShard(t *testing.T) {
	var count atomic.Int64
	wp := NewWorkerPool(func(_ int) {
		count.Add(1)
	})
	wp.SetNumShards(4)
	wp.Start()

	for i := 0; i < 20; i++ {
		require.NoError(t, wp.AddTaskForShard(i, i%4))
	}

	require.Eventually(t, func() bool {
		return count.Load() == 20
	}, 2*time.Second, 5*time.Millisecond)

	wp.Stop()
}

func TestWorkerPool_AddTaskForShard_Errors(t *testing.T) {
	t.Run("not started", func(t *testing.T) {
		wp := NewWorkerPool(func(_ int) {})
		err := wp.AddTaskForShard(1, 0)
		require.ErrorIs(t, err, ErrPoolNotStarted)
	})

	t.Run("stopped", func(t *testing.T) {
		wp := NewWorkerPool(func(_ int) {})
		wp.Start()
		wp.Stop()
		err := wp.AddTaskForShard(1, 0)
		require.ErrorIs(t, err, ErrPoolStopped)
	})
}

func TestWorkerPool_Stop(t *testing.T) {
	t.Run("stop without start", func(*testing.T) {
		wp := NewWorkerPool(func(_ int) {})
		wp.Stop() // must not panic
	})

	t.Run("double stop", func(*testing.T) {
		wp := NewWorkerPool(func(_ int) {})
		wp.Start()
		wp.Stop()
		wp.Stop() // must not panic
	})

	t.Run("double start", func(*testing.T) {
		wp := NewWorkerPool(func(_ int) {})
		wp.Start()
		wp.Start() // must not panic (no-op)
		wp.Stop()
	})
}

func TestWorkerPool_Cleanup(t *testing.T) {
	var count atomic.Int64
	wp := NewWorkerPool(func(_ int) {
		count.Add(1)
	})
	wp.SetNumShards(1)
	wp.SetIdleWorkerLifetime(50 * time.Millisecond)
	wp.Start()

	// Submit concurrent tasks to create multiple workers.
	for i := range 10 {
		require.NoError(t, wp.AddTask(i))
	}

	require.Eventually(t, func() bool {
		return count.Load() == 10
	}, 2*time.Second, 5*time.Millisecond)

	// Workers are idle. Wait for cleanup to run and reap them.
	require.Eventually(t, func() bool {
		return wp.GetSpawnedWorkers() == 0
	}, 2*time.Second, 10*time.Millisecond)

	// Pool is still functional after cleanup.
	require.NoError(t, wp.AddTask(42))
	require.Eventually(t, func() bool {
		return count.Load() == 11
	}, 2*time.Second, 5*time.Millisecond)

	wp.Stop()
}

func TestWorkerPool_CleanupIdleWorkerSlots(t *testing.T) {
	// Create enough concurrent workers to fill idleWorker1, idleWorker2, and
	// the idleWorkerList. Then wait for cleanup to expire all of them.
	const numTasks = 20
	barrier := make(chan struct{})
	var completed atomic.Int64

	wp := NewWorkerPool(func(_ int) {
		<-barrier
		completed.Add(1)
	})
	wp.SetNumShards(1)
	wp.SetIdleWorkerLifetime(50 * time.Millisecond)
	wp.Start()

	for i := range numTasks {
		require.NoError(t, wp.AddTask(i))
	}

	// Wait for all workers to be spawned (they block on barrier).
	require.Eventually(t, func() bool {
		return wp.GetSpawnedWorkers() == numTasks
	}, 2*time.Second, 5*time.Millisecond)

	close(barrier)

	require.Eventually(t, func() bool {
		return completed.Load() == int64(numTasks)
	}, 2*time.Second, 5*time.Millisecond)

	// All workers idle. Cleanup should reclaim all.
	require.Eventually(t, func() bool {
		return wp.GetSpawnedWorkers() == 0
	}, 3*time.Second, 10*time.Millisecond)

	wp.Stop()
}

func TestWorkerPool_CleanupBinarySearch(t *testing.T) {
	// Create > 400 idle workers in a single shard to trigger the binary search
	// path in the cleanup function.
	const numTasks = 420
	barrier := make(chan struct{})
	var completed atomic.Int64

	wp := NewWorkerPool(func(_ int) {
		<-barrier
		completed.Add(1)
	})
	wp.SetNumShards(1)
	wp.SetIdleWorkerLifetime(100 * time.Millisecond)
	wp.Start()

	for i := range numTasks {
		require.NoError(t, wp.AddTask(i))
	}

	require.Eventually(t, func() bool {
		return wp.GetSpawnedWorkers() == numTasks
	}, 5*time.Second, 10*time.Millisecond)

	close(barrier)

	require.Eventually(t, func() bool {
		return completed.Load() == int64(numTasks)
	}, 5*time.Second, 10*time.Millisecond)

	// Workers are idle. Cleanup should reclaim all via binary search path.
	require.Eventually(t, func() bool {
		return wp.GetSpawnedWorkers() == 0
	}, 5*time.Second, 10*time.Millisecond)

	wp.Stop()
}

func TestWorkerPool_HighConcurrency(t *testing.T) {
	var count atomic.Int64
	wp := NewWorkerPool(func(_ int) {
		count.Add(1)
	})
	wp.SetNumShards(4)
	wp.Start()

	var wg sync.WaitGroup
	for i := range 1000 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = wp.AddTask(n)
		}(i)
	}

	wg.Wait()

	require.Eventually(t, func() bool {
		return count.Load() == 1000
	}, 5*time.Second, 10*time.Millisecond)

	wp.Stop()
}

func TestSpinLocker(t *testing.T) {
	t.Run("contention", func(t *testing.T) {
		var sl spinLocker
		var counter int64
		var wg sync.WaitGroup

		for range 50 {
			wg.Go(func() {
				for range 100 {
					sl.Lock()
					atomic.AddInt64(&counter, 1)
					sl.Unlock()
				}
			})
		}

		wg.Wait()
		require.Equal(t, int64(5000), counter)
	})
}

func TestSplitMix64(t *testing.T) {
	var sm splitMix64
	sm.Init(42)

	seen := make(map[uint64]bool, 100)
	for range 100 {
		v := sm.Uint64()
		require.False(t, seen[v], "duplicate value generated")
		seen[v] = true
	}

	sm.Init(123)
	for range 100 {
		v := sm.Int63()
		require.GreaterOrEqual(t, v, int64(0))
	}
}

func TestRandInt(t *testing.T) {
	for range 100 {
		v := randInt()
		require.GreaterOrEqual(t, v, 0)
	}
}

func TestWorkerPool_StopWhileProcessing(t *testing.T) {
	// Workers are processing tasks when Stop is called. Their setWorkerIdle
	// call returns false and they exit via the break path.
	barrier := make(chan struct{})
	var completed atomic.Int64
	wp := NewWorkerPool(func(_ int) {
		<-barrier
		completed.Add(1)
	})
	wp.SetNumShards(1)
	wp.Start()

	for i := range 5 {
		require.NoError(t, wp.AddTask(i))
	}

	require.Eventually(t, func() bool {
		return wp.GetSpawnedWorkers() == 5
	}, 2*time.Second, 5*time.Millisecond)

	// Stop the pool while workers are blocked.
	go wp.Stop()
	pause.For(20 * time.Millisecond)

	// Release workers — they find the shard stopped.
	close(barrier)

	require.Eventually(t, func() bool {
		return wp.GetSpawnedWorkers() == 0
	}, 2*time.Second, 10*time.Millisecond)
}

func TestWorkerPool_ConcurrentStartStop(_ *testing.T) {
	wp := NewWorkerPool(func(_ int) {})
	wp.SetNumShards(2)

	// Rapid start/stop cycles to exercise lock contention.
	for range 10 {
		wp = NewWorkerPool(func(_ int) {})
		wp.SetNumShards(2)
		wp.Start()
		_ = wp.AddTask(1)
		wp.Stop()
	}
}
