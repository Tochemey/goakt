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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewBucketsNormalizesCount(t *testing.T) {
	now := time.Unix(0, 123)
	bw := newBuckets(time.Second, 0, func() time.Time { return now })

	require.Equal(t, 1, bw.num)
	require.Equal(t, time.Second, bw.bucketDur)
	require.Equal(t, now.UnixNano(), bw.buf[0].start)
}

func TestNewBucketsEnforcesMinimumDuration(t *testing.T) {
	now := time.Unix(0, 456)
	bw := newBuckets(0, 5, func() time.Time { return now })

	require.Equal(t, 5, bw.num)
	require.Equal(t, time.Nanosecond, bw.bucketDur)

	for _, b := range bw.buf {
		require.Equal(t, now.UnixNano(), b.start)
	}
}

func TestBucketAddAccumulatesWithinBucket(t *testing.T) {
	clock := newFakeClock()
	bw := newBuckets(time.Second, 4, clock.Now)

	succ, fail := bw.add(clock.Now().UnixNano(), true)
	require.Equal(t, uint64(1), succ)
	require.Equal(t, uint64(0), fail)

	succ, fail = bw.add(clock.Now().UnixNano(), false)
	require.Equal(t, uint64(1), succ)
	require.Equal(t, uint64(1), fail)
	require.Equal(t, 0, bw.cursor, "no time elapsed: cursor must not advance")
}

func TestBucketAdvanceRotatesAndExpiresOldCounts(t *testing.T) {
	clock := newFakeClock()
	// 4 buckets of 250ms each
	bw := newBuckets(time.Second, 4, clock.Now)

	bw.add(clock.Now().UnixNano(), false)

	// after 3 bucket durations the failure is still inside the window
	clock.Advance(750 * time.Millisecond)
	succ, fail := bw.add(clock.Now().UnixNano(), true)
	require.Equal(t, uint64(1), succ)
	require.Equal(t, uint64(1), fail)
	require.Equal(t, 3, bw.cursor)

	// one more bucket duration pushes the failure out of the window
	clock.Advance(250 * time.Millisecond)
	succ, fail = bw.add(clock.Now().UnixNano(), true)
	require.Equal(t, uint64(2), succ)
	require.Equal(t, uint64(0), fail)
}

func TestBucketAdvanceKeepsAlignment(t *testing.T) {
	clock := newFakeClock()
	bw := newBuckets(time.Second, 4, clock.Now)
	start := clock.Now().UnixNano()

	// advancing by 1.5 bucket durations must move lastUpdate by exactly one
	// bucket duration, not swallow the partial 125ms
	clock.Advance(375 * time.Millisecond)
	bw.add(clock.Now().UnixNano(), true)
	require.Equal(t, start+(250*time.Millisecond).Nanoseconds(), bw.lastUpdate)
}

func TestBucketHardResetWhenWindowGoesStale(t *testing.T) {
	clock := newFakeClock()
	bw := newBuckets(time.Second, 4, clock.Now)

	bw.add(clock.Now().UnixNano(), false)
	bw.add(clock.Now().UnixNano(), false)

	clock.Advance(2 * time.Second)
	succ, fail := bw.add(clock.Now().UnixNano(), true)

	require.Equal(t, uint64(1), succ)
	require.Equal(t, uint64(0), fail)
	require.Equal(t, 0, bw.cursor)
	require.Equal(t, clock.Now().UnixNano(), bw.lastUpdate)
}

func TestBucketResetClearsCountsAndRealignsWindow(t *testing.T) {
	clock := newFakeClock()
	bw := newBuckets(time.Second, 4, clock.Now)

	bw.add(clock.Now().UnixNano(), false)
	clock.Advance(300 * time.Millisecond)
	bw.reset()

	require.Equal(t, clock.Now().UnixNano(), bw.lastUpdate, "reset must realign the window at the current time")

	// a count recorded right after reset must not be rotated away by phantom
	// elapsed time from before the reset
	succ, fail := bw.add(clock.Now().UnixNano(), true)
	require.Equal(t, uint64(1), succ)
	require.Equal(t, uint64(0), fail)
	require.Equal(t, 0, bw.cursor)
}

func TestBucketSnapshot(t *testing.T) {
	clock := newFakeClock()
	bw := newBuckets(time.Second, 4, clock.Now)

	bw.add(clock.Now().UnixNano(), true)
	bw.add(clock.Now().UnixNano(), false)

	succ, fail, start, end := bw.snapshot()
	require.Equal(t, uint64(1), succ)
	require.Equal(t, uint64(1), fail)
	require.Equal(t, clock.Now(), end)
	require.Equal(t, clock.Now().Add(-time.Second), start)
}

func TestBucketSnapshotExpiresStaleWindow(t *testing.T) {
	clock := newFakeClock()
	bw := newBuckets(time.Second, 4, clock.Now)

	bw.add(clock.Now().UnixNano(), false)
	clock.Advance(5 * time.Second)

	succ, fail, _, _ := bw.snapshot()
	require.Equal(t, uint64(0), succ)
	require.Equal(t, uint64(0), fail)
}
