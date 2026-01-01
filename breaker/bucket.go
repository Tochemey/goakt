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

package breaker

import (
	"sync"
	"time"
)

// bucket holds counts of successes and failures within a specific time frame.
type bucket struct {
	succ  uint64
	fail  uint64
	start int64 // start time of bucket (unix nano)
}

// reset clears the bucket data
func (b *bucket) reset(startTime int64) {
	b.succ = 0
	b.fail = 0
	b.start = startTime
}

// bucketWindow manages a series of buckets to track successes and failures over a rolling time window.
type bucketWindow struct {
	bucketDur   time.Duration
	num         int
	clock       func() time.Time
	windowNanos int64 // cached window duration in nanoseconds

	mu         sync.RWMutex
	buf        []bucket
	cursor     int   // points to current bucket index
	lastUpdate int64 // last time we advanced buckets
}

func newBuckets(window time.Duration, n int, clock func() time.Time) *bucketWindow {
	if n < 1 {
		n = 1
	}
	bucketDur := window / time.Duration(n)
	if bucketDur <= 0 {
		bucketDur = time.Nanosecond
	}

	now := clock().UnixNano()
	bw := &bucketWindow{
		bucketDur:   bucketDur,
		num:         n,
		clock:       clock,
		windowNanos: window.Nanoseconds(),
		buf:         make([]bucket, n),
		cursor:      0,
		lastUpdate:  now,
	}

	// Initialize all buckets with current time
	for i := range bw.buf {
		bw.buf[i].reset(now)
	}
	return bw
}

func (bw *bucketWindow) advanceLocked(now int64) {
	// Quick check if we need to advance at all
	if now < bw.lastUpdate+bw.bucketDur.Nanoseconds() {
		return // still within current bucket
	}

	elapsed := now - bw.lastUpdate

	// If we've moved past the entire window, do a hard reset
	if elapsed >= bw.windowNanos {
		bw.hardResetLocked(now)
		return
	}

	// Calculate how many buckets to advance
	steps := min(int(elapsed/bw.bucketDur.Nanoseconds()), bw.num-1)

	// Advance buckets efficiently
	for i := range steps {
		bw.cursor = (bw.cursor + 1) % bw.num
		bucketStartTime := bw.lastUpdate + int64(i+1)*bw.bucketDur.Nanoseconds()
		bw.buf[bw.cursor].reset(bucketStartTime)
	}

	bw.lastUpdate = now
}

func (bw *bucketWindow) hardResetLocked(now int64) {
	for i := range bw.buf {
		bw.buf[i].reset(now)
	}
	bw.cursor = 0
	bw.lastUpdate = now
}

func (bw *bucketWindow) addSuccess(n uint64) {
	bw.mu.Lock()
	now := bw.clock().UnixNano()
	bw.advanceLocked(now)
	bw.buf[bw.cursor].succ += n
	bw.mu.Unlock()
}

func (bw *bucketWindow) addFailure(n uint64) {
	bw.mu.Lock()
	now := bw.clock().UnixNano()
	bw.advanceLocked(now)
	bw.buf[bw.cursor].fail += n
	bw.mu.Unlock()
}

func (bw *bucketWindow) totals() (succ, fail uint64) {
	bw.mu.Lock()
	now := bw.clock().UnixNano()
	bw.advanceLocked(now)
	for i := 0; i < bw.num; i++ {
		b := bw.buf[i]
		succ += b.succ
		fail += b.fail
	}
	bw.mu.Unlock()
	return
}

func (bw *bucketWindow) reset() {
	bw.mu.Lock()
	now := bw.clock().UnixNano()
	for i := range bw.buf {
		bw.buf[i].succ, bw.buf[i].fail = 0, 0
		bw.buf[i].start = now
	}
	bw.cursor = 0
	bw.mu.Unlock()
}

func (bw *bucketWindow) windowBounds() (start, end time.Time) {
	bw.mu.Lock()
	now := bw.clock()
	bw.advanceLocked(now.UnixNano())
	end = now
	start = now.Add(-bw.bucketDur * time.Duration(bw.num))
	bw.mu.Unlock()
	return
}
