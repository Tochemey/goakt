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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFramePool_GetPut(t *testing.T) {
	fp := NewFramePool()

	t.Run("small buffer", func(t *testing.T) {
		buf := fp.Get(100)
		require.Len(t, buf, 100)
		require.Equal(t, 256, cap(buf), "should be rounded up to 256 bucket")
		fp.Put(buf)
	})

	t.Run("exact bucket boundary", func(t *testing.T) {
		buf := fp.Get(256)
		require.Len(t, buf, 256)
		require.Equal(t, 256, cap(buf))
		fp.Put(buf)
	})

	t.Run("just over bucket boundary", func(t *testing.T) {
		buf := fp.Get(257)
		require.Len(t, buf, 257)
		require.Equal(t, 512, cap(buf), "should be rounded up to 512 bucket")
		fp.Put(buf)
	})

	t.Run("large buffer within max bucket", func(t *testing.T) {
		buf := fp.Get(1 << 22) // 4 MiB
		require.Len(t, buf, 1<<22)
		fp.Put(buf)
	})

	t.Run("oversized buffer", func(t *testing.T) {
		buf := fp.Get((1 << 22) + 1) // 4 MiB + 1
		require.Len(t, buf, (1<<22)+1)
		// Put should not panic on oversized buffers.
		fp.Put(buf)
	})
}

func TestBucketIndex(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want int
	}{
		{"zero", 0, 0},
		{"one", 1, 0},
		{"min bucket", 256, 0},
		{"min+1", 257, 1},
		{"512", 512, 1},
		{"513", 513, 2},
		{"1024", 1024, 2},
		{"4MiB", 1 << 22, numBuckets - 1},
		{"over max", (1 << 22) + 1, numBuckets},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, bucketIndex(tt.n))
		})
	}
}

func TestBucketIndexExact(t *testing.T) {
	tests := []struct {
		name string
		c    int
		want int
	}{
		{"zero", 0, -1},
		{"non-power", 300, -1},
		{"256", 256, 0},
		{"512", 512, 1},
		{"1024", 1024, 2},
		{"4MiB", 1 << 22, numBuckets - 1},
		{"too small", 128, -1},
		{"too large", 1 << 23, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, bucketIndexExact(tt.c))
		})
	}
}
