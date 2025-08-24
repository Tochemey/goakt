/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package breaker

import (
	"sync"
	"time"
)

type bucket struct {
	succ uint64
	fail uint64
	// start time of bucket (unix nano). When stale, bucket is reset.
	start int64
}

type bucketWindow struct {
	bucketDur time.Duration
	num       int
	clock     func() time.Time

	mu     sync.Mutex
	buf    []bucket
	cursor int // points to current bucket index
}

func newBuckets(window time.Duration, n int, clock func() time.Time) *bucketWindow {
	if n < 1 {
		n = 1
	}
	bucketDur := window / time.Duration(n)
	if bucketDur <= 0 {
		bucketDur = time.Nanosecond
	}
	bw := &bucketWindow{
		bucketDur: bucketDur,
		num:       n,
		clock:     clock,
		buf:       make([]bucket, n),
		cursor:    0,
	}
	now := clock().UnixNano()
	for i := range bw.buf {
		bw.buf[i].start = now
	}
	return bw
}

func (bw *bucketWindow) advanceLocked(now int64) {
	cur := &bw.buf[bw.cursor]
	if now < cur.start+bw.bucketDur.Nanoseconds() {
		return // still within current bucket
	}
	// If we've moved past the entire window, do a hard reset.
	windowNanos := bw.bucketDur.Nanoseconds() * int64(bw.num)
	if now-cur.start >= windowNanos {
		for i := range bw.buf {
			bw.buf[i].succ, bw.buf[i].fail = 0, 0
			bw.buf[i].start = now
		}
		bw.cursor = 0
		return
	}
	// determine how many buckets to advance (at most num-1)
	steps := int((now - cur.start) / bw.bucketDur.Nanoseconds())
	if steps > bw.num-1 {
		steps = bw.num - 1
	}
	for i := 0; i < steps; i++ {
		bw.cursor = (bw.cursor + 1) % bw.num
		b := &bw.buf[bw.cursor]
		b.succ, b.fail = 0, 0
		b.start = cur.start + int64(i+1)*bw.bucketDur.Nanoseconds()
	}
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
