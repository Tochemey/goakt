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

	mu         sync.Mutex
	buf        []bucket
	cursor     int   // points to current bucket index
	lastUpdate int64 // start time of the current bucket (unix nano)
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
	}

	bw.hardResetLocked(now)
	return bw
}

// advanceLocked rotates the window forward so the cursor points at the bucket
// covering now. Caller must hold bw.mu.
func (bw *bucketWindow) advanceLocked(now int64) {
	bucketNanos := bw.bucketDur.Nanoseconds()
	elapsed := now - bw.lastUpdate
	if elapsed < bucketNanos {
		return // still within the current bucket
	}

	steps := elapsed / bucketNanos
	if steps >= int64(bw.num) {
		// the whole window has gone stale
		bw.hardResetLocked(now)
		return
	}

	for range steps {
		bw.cursor = (bw.cursor + 1) % bw.num
		bw.lastUpdate += bucketNanos
		bw.buf[bw.cursor].reset(bw.lastUpdate)
	}
}

// hardResetLocked clears every bucket and realigns the window at now. Caller
// must hold bw.mu (or own the window exclusively during construction).
func (bw *bucketWindow) hardResetLocked(now int64) {
	for i := range bw.buf {
		bw.buf[i].reset(now)
	}

	bw.cursor = 0
	bw.lastUpdate = now
}

// add records one outcome at now and returns the window totals observed under
// the same lock acquisition, so callers can evaluate a consistent snapshot
// without locking twice.
func (bw *bucketWindow) add(now int64, success bool) (succ, fail uint64) {
	bw.mu.Lock()
	bw.advanceLocked(now)

	if success {
		bw.buf[bw.cursor].succ++
	} else {
		bw.buf[bw.cursor].fail++
	}

	succ, fail = bw.totalsLocked()
	bw.mu.Unlock()
	return succ, fail
}

// totalsLocked sums all buckets. Caller must hold bw.mu.
func (bw *bucketWindow) totalsLocked() (succ, fail uint64) {
	for i := range bw.buf {
		succ += bw.buf[i].succ
		fail += bw.buf[i].fail
	}

	return succ, fail
}

// snapshot returns the window totals and bounds as of now.
func (bw *bucketWindow) snapshot() (succ, fail uint64, start, end time.Time) {
	bw.mu.Lock()
	now := bw.clock()
	bw.advanceLocked(now.UnixNano())
	succ, fail = bw.totalsLocked()
	bw.mu.Unlock()

	end = now
	start = now.Add(-time.Duration(bw.windowNanos))
	return succ, fail, start, end
}

// reset clears all counts and realigns the window at the current time.
func (bw *bucketWindow) reset() {
	bw.mu.Lock()
	bw.hardResetLocked(bw.clock().UnixNano())
	bw.mu.Unlock()
}
