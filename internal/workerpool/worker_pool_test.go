/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package workerpool

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/internal/util"
)

func TestWorkerPool(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		totalShards := 256
		pool := New(WithNumShards(totalShards), WithPassivateAfter(time.Millisecond))
		require.NotNil(t, pool)

		pool.Start()
		totalWorkers := pool.GetSpawnedWorkers()
		require.Zero(t, totalWorkers)

		workCount := 1000
		var executedCount atomic.Int64
		for range workCount {
			pool.SubmitWork(func() {
				time.Sleep(time.Millisecond)
				executedCount.Add(1)
			})
		}

		totalWorkers = pool.GetSpawnedWorkers()
		require.NotZero(t, totalWorkers)

		util.Pause(2 * time.Second)

		// Stop the pool to free up resources
		pool.Stop()

		// already stopped
		pool.Stop()
		pool.Stop()
	})
	t.Run("When not started", func(t *testing.T) {
		pool := New()
		require.NotNil(t, pool)
		require.False(t, pool.started.Load())
		pool.Stop()
		require.False(t, pool.stopped.Load())
	})
}
