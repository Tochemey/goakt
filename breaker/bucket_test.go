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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// nolint
func TestNewBucketsNormalizesCount(t *testing.T) {
	now := time.Unix(0, 123)
	bw := newBuckets(time.Second, 0, func() time.Time { return now })

	require.Equal(t, 1, bw.num)
	require.Equal(t, time.Second, bw.bucketDur)
	require.Equal(t, now.UnixNano(), bw.buf[0].start)
}

// nolint
func TestNewBucketsEnforcesMinimumDuration(t *testing.T) {
	now := time.Unix(0, 456)
	bw := newBuckets(0, 5, func() time.Time { return now })

	require.Equal(t, 5, bw.num)
	require.Equal(t, time.Nanosecond, bw.bucketDur)
	for _, b := range bw.buf {
		require.Equal(t, now.UnixNano(), b.start)
	}
}
