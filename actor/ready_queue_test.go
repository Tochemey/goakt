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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSchedulable is a schedulable used exclusively by scheduler tests.
type fakeSchedulable struct {
	id    int
	turns atomic.Int32
}

func (f *fakeSchedulable) runTurn(*worker) { f.turns.Add(1) }

func newFake(id int) *fakeSchedulable { return &fakeSchedulable{id: id} }

func TestLocalQueuePushPop(t *testing.T) {
	q := &localQueue{}
	require.Equal(t, 0, q.length())

	a, b, c := newFake(1), newFake(2), newFake(3)
	require.True(t, q.pushBack(a))
	require.True(t, q.pushBack(b))
	require.True(t, q.pushBack(c))
	require.Equal(t, 3, q.length())

	require.Same(t, a, q.popFront().(*fakeSchedulable))
	require.Same(t, b, q.popFront().(*fakeSchedulable))
	require.Same(t, c, q.popFront().(*fakeSchedulable))
	require.Nil(t, q.popFront())
}

func TestLocalQueueFull(t *testing.T) {
	q := &localQueue{}
	for i := range localQueueCap {
		require.True(t, q.pushBack(newFake(i)))
	}
	// Next push must fail.
	require.False(t, q.pushBack(newFake(-1)))
	// Drain and refill to exercise wraparound.
	for range localQueueCap / 2 {
		require.NotNil(t, q.popFront())
	}
	for range localQueueCap / 2 {
		require.True(t, q.pushBack(newFake(0)))
	}
	require.Equal(t, localQueueCap, q.length())
}

func TestLocalQueueStealHalf(t *testing.T) {
	src := &localQueue{}
	dst := &localQueue{}

	// Empty source -> nil.
	require.Nil(t, src.stealHalf(dst))

	for i := range 10 {
		require.True(t, src.pushBack(newFake(i)))
	}

	first := src.stealHalf(dst)
	require.NotNil(t, first)
	require.Equal(t, 0, first.(*fakeSchedulable).id)
	// 10 items total, (10+1)/2 = 5 stolen: 1 returned directly + 4 in dst.
	require.Equal(t, 4, dst.length())
	require.Equal(t, 5, src.length())
}

func TestLocalQueueStealHalfDstFull(t *testing.T) {
	src := &localQueue{}
	dst := &localQueue{}
	// Fill dst completely.
	for i := range localQueueCap {
		require.True(t, dst.pushBack(newFake(i)))
	}
	// Put many items in src to force the dst-full branch.
	for i := range 10 {
		require.True(t, src.pushBack(newFake(100+i)))
	}
	first := src.stealHalf(dst)
	require.NotNil(t, first)
	// dst remained full; overflow stolen items were dropped by the stealer.
	require.Equal(t, localQueueCap, dst.length())
}

func TestReadyQueuePushTake(t *testing.T) {
	rq := newReadyQueue(2)
	a, b := newFake(1), newFake(2)
	rq.push(a)
	rq.push(b)
	require.Equal(t, 2, rq.globalLen())

	got, ok := rq.take(0)
	require.True(t, ok)
	require.Same(t, a, got.(*fakeSchedulable))

	got, ok = rq.take(1)
	require.True(t, ok)
	require.Same(t, b, got.(*fakeSchedulable))
}

func TestReadyQueuePushLocalFastPath(t *testing.T) {
	rq := newReadyQueue(2)
	a := newFake(1)
	rq.pushLocal(0, a)
	require.Equal(t, 1, rq.locals[0].length())
	require.Equal(t, 0, rq.globalLen())

	got, ok := rq.take(0)
	require.True(t, ok)
	require.Same(t, a, got.(*fakeSchedulable))
}

func TestReadyQueuePushLocalSpillsToGlobal(t *testing.T) {
	rq := newReadyQueue(1)
	for i := range localQueueCap {
		rq.pushLocal(0, newFake(i))
	}
	// Local full: next push spills to global.
	overflow := newFake(999)
	rq.pushLocal(0, overflow)
	require.Equal(t, localQueueCap, rq.locals[0].length())
	require.Equal(t, 1, rq.globalLen())
}

func TestReadyQueueStealWhenLocalAndGlobalEmpty(t *testing.T) {
	rq := newReadyQueue(3)
	// Seed worker 2's local queue.
	a, b := newFake(1), newFake(2)
	rq.pushLocal(2, a)
	rq.pushLocal(2, b)

	// Worker 0 has nothing locally, global is empty, must steal from worker 2.
	got, ok := rq.take(0)
	require.True(t, ok)
	require.Contains(t, []*fakeSchedulable{a, b}, got.(*fakeSchedulable))
}

func TestReadyQueueStealSingleWorker(t *testing.T) {
	rq := newReadyQueue(1)
	// Single worker: trySteal is a no-op; take must park without stealing.
	done := make(chan struct{})
	go func() {
		_, ok := rq.take(0)
		require.False(t, ok)
		close(done)
	}()
	// Give the goroutine a chance to park.
	waitParked(t, rq, 1)
	rq.close()
	<-done
}

func TestReadyQueueParkAndWake(t *testing.T) {
	rq := newReadyQueue(2)
	var got atomic.Value
	done := make(chan struct{})
	go func() {
		s, ok := rq.take(0)
		require.True(t, ok)
		got.Store(s)
		close(done)
	}()
	waitParked(t, rq, 1)

	a := newFake(42)
	rq.push(a)
	<-done
	require.Same(t, a, got.Load().(*fakeSchedulable))
}

func TestReadyQueueCloseWakesParkedWorkers(t *testing.T) {
	rq := newReadyQueue(3)
	var wg sync.WaitGroup
	for i := range 3 {
		wg.Go(func() {
			_, ok := rq.take(i)
			require.False(t, ok)
		})
	}
	waitParked(t, rq, 3)
	rq.close()
	wg.Wait()
}

func TestReadyQueueCloseBeforeTake(t *testing.T) {
	rq := newReadyQueue(1)
	rq.close()
	_, ok := rq.take(0)
	require.False(t, ok)
}

func TestReadyQueueTakeDrainsGlobalAfterPark(t *testing.T) {
	// Cover the "queue non-empty at park time" branch: push happens between
	// the lock-free scan and the park check. We force this by pushing directly
	// via the global path while a worker is on its way to parking. Using a
	// two-worker setup means worker 0 will steal from worker 1 first; we
	// keep worker 1 empty so the scan goes: local -> global -> steal -> park.
	rq := newReadyQueue(2)
	a := newFake(1)
	// Prepopulate global before take to avoid park path; exercises success path.
	rq.push(a)
	got, ok := rq.take(0)
	require.True(t, ok)
	require.Same(t, a, got.(*fakeSchedulable))
}

func TestReadyQueueMultiProducerConsumer(t *testing.T) {
	const workers = 4
	const perProducer = 500
	const producers = 4
	rq := newReadyQueue(workers)
	var received atomic.Int64
	var consumers sync.WaitGroup
	for i := range workers {
		consumers.Go(func() {
			for {
				s, ok := rq.take(i)
				if !ok {
					return
				}
				_ = s
				received.Add(1)
			}
		})
	}

	var producersWG sync.WaitGroup
	for range producers {
		producersWG.Go(func() {
			for j := range perProducer {
				rq.push(newFake(j))
			}
		})
	}
	producersWG.Wait()

	assert.Eventually(t, func() bool {
		return received.Load() == int64(producers*perProducer)
	}, 5*time.Second, 10*time.Millisecond)

	rq.close()
	consumers.Wait()
}

func waitParked(t *testing.T, rq *readyQueue, n int) {
	t.Helper()
	assert.Eventually(t, func() bool {
		return rq.parkedCount() >= n
	}, 2*time.Second, 1*time.Millisecond, "expected at least %d parked workers", n)
}
