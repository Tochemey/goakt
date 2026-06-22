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

package xsync

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock is a manually advanced clock for deterministic TTL testing.
type fakeClock struct {
	now atomic.Int64 // unix nanoseconds
}

func newFakeClock() *fakeClock {
	return &fakeClock{}
}

func (c *fakeClock) NowNano() int64 {
	return c.now.Load()
}

func (c *fakeClock) Advance(d time.Duration) {
	c.now.Add(int64(d))
}

func TestTTLMapSetAndGet(t *testing.T) {
	m := NewTTLMap[int, string](time.Minute)
	m.Set(1, "one")

	val, ok := m.Get(1)
	require.True(t, ok)
	assert.Equal(t, "one", val)

	_, ok = m.Get(2)
	assert.False(t, ok)
}

func TestTTLMapSetUpdatesValue(t *testing.T) {
	m := NewTTLMap[int, string](time.Minute)
	m.Set(1, "one")
	m.Set(1, "uno")

	val, ok := m.Get(1)
	require.True(t, ok)
	assert.Equal(t, "uno", val)
	assert.Equal(t, 1, m.Len())
}

func TestTTLMapGetExpiredEntry(t *testing.T) {
	clock := newFakeClock()
	m := NewTTLMap[int, string](time.Minute)
	m.now = clock.NowNano

	m.Set(1, "one")

	// still live just before the deadline
	clock.Advance(time.Minute - time.Nanosecond)
	_, ok := m.Get(1)
	assert.True(t, ok)

	// expired once the deadline is reached
	clock.Advance(time.Nanosecond)
	_, ok = m.Get(1)
	assert.False(t, ok)

	// a Get on an expired entry removes it
	assert.Equal(t, 0, m.Len())
}

func TestTTLMapSetEvictsExpiredEntries(t *testing.T) {
	clock := newFakeClock()
	m := NewTTLMap[int, int](time.Minute)
	m.now = clock.NowNano

	// insert a batch of entries that will all expire together
	for i := range 100 {
		m.Set(i, i)
	}
	assert.Equal(t, 100, m.Len())

	// move past their deadline, then a single new insert sweeps the expired ones
	clock.Advance(2 * time.Minute)
	m.Set(1000, 1000)

	assert.Equal(t, 1, m.Len())
	_, ok := m.Get(1000)
	assert.True(t, ok)
}

func TestTTLMapBoundedUnderSustainedInserts(t *testing.T) {
	clock := newFakeClock()
	m := NewTTLMap[int, int](time.Minute)
	m.now = clock.NowNano

	// continuously insert unique keys while advancing the clock so each one
	// ages out long before the total count would be reached.
	const total = 100_000
	for i := range total {
		m.Set(i, i)
		clock.Advance(time.Second)
	}

	// the live window only spans the TTL (60 inserts), so the map stays bounded
	// far below the total number of inserts.
	assert.LessOrEqual(t, m.Len(), 61)
}

func TestTTLMapCompactionDropsLazilyDeletedKeys(t *testing.T) {
	clock := newFakeClock()
	m := NewTTLMap[int, int](time.Minute)
	m.now = clock.NowNano

	// fill a batch that all expires together
	for i := range 100 {
		m.Set(i, i)
	}

	// refresh one entry so it stays live and pins the head near the middle
	clock.Advance(30 * time.Second)
	m.Set(80, 80)

	// advance past the original batch deadline; all but the refreshed entry expire
	clock.Advance(40 * time.Second) // t = 70s

	// lazily delete an expired-but-still-mapped key via Get
	_, ok := m.Get(85)
	require.False(t, ok)

	// a new insert evicts the expired prefix and triggers compaction
	m.Set(200, 200)

	// the lazily deleted key must not be resurrected by compaction
	_, ok = m.Get(85)
	assert.False(t, ok)

	// the refreshed live entry and the new entry survive
	v, ok := m.Get(80)
	require.True(t, ok)
	assert.Equal(t, 80, v)

	v, ok = m.Get(200)
	require.True(t, ok)
	assert.Equal(t, 200, v)
}

func TestTTLMapRefreshExtendsDeadline(t *testing.T) {
	clock := newFakeClock()
	m := NewTTLMap[int, string](time.Minute)
	m.now = clock.NowNano

	m.Set(1, "one")
	clock.Advance(30 * time.Second)
	// re-set refreshes the deadline
	m.Set(1, "one")

	clock.Advance(45 * time.Second) // 75s since first insert, 45s since refresh
	val, ok := m.Get(1)
	require.True(t, ok)
	assert.Equal(t, "one", val)
}

func TestTTLMapReset(t *testing.T) {
	m := NewTTLMap[int, string](time.Minute)
	m.Set(1, "one")
	m.Set(2, "two")
	require.Equal(t, 2, m.Len())

	m.Reset()
	assert.Equal(t, 0, m.Len())
	_, ok := m.Get(1)
	assert.False(t, ok)

	// the map remains usable after reset
	m.Set(3, "three")
	val, ok := m.Get(3)
	require.True(t, ok)
	assert.Equal(t, "three", val)
}

func TestTTLMapNonPositiveTTLExpiresImmediately(t *testing.T) {
	m := NewTTLMap[int, string](0)
	m.Set(1, "one")

	_, ok := m.Get(1)
	assert.False(t, ok)
}

func TestTTLMapConcurrentAccess(t *testing.T) {
	m := NewTTLMap[int, int](time.Minute)

	const goroutines = 16
	const perGoroutine = 1000

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := range perGoroutine {
				key := base*perGoroutine + i
				m.Set(key, key)
				m.Get(key)
			}
		}(g)
	}
	wg.Wait()

	assert.Equal(t, goroutines*perGoroutine, m.Len())
}
