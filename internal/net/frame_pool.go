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

import "sync"

const (
	minBucketShift = 8  // 256 B
	maxBucketShift = 22 // 4 MiB
	numBuckets     = maxBucketShift - minBucketShift + 1
)

// FramePool maintains a set of [sync.Pool] instances bucketed by power-of-two
// size. This avoids allocating a new []byte for every incoming frame and
// significantly reduces GC pressure under high message rates.
//
// Bucket boundaries (powers of two from 256 B to 4 MiB) cover the vast
// majority of protobuf messages while keeping internal fragmentation below 2x.
type FramePool struct {
	pools [numBuckets]sync.Pool
}

// NewFramePool creates a new FramePool instance.
func NewFramePool() *FramePool {
	pool := &FramePool{}
	for i := range pool.pools {
		size := 1 << (minBucketShift + i)
		pool.pools[i] = sync.Pool{
			New: func() any {
				buf := make([]byte, size)
				return &buf
			},
		}
	}
	return pool
}

// Get returns a []byte of exactly n bytes, drawn from the smallest pool
// bucket that can satisfy the request. For sizes larger than the biggest
// bucket a fresh slice is allocated (and will be collected by the GC).
func (x *FramePool) Get(n int) []byte {
	idx := bucketIndex(n)
	if idx >= numBuckets {
		// Oversized frame — allocate directly.
		return make([]byte, n)
	}
	bp := x.pools[idx].Get().(*[]byte)
	return (*bp)[:n]
}

// Put returns a buffer to the appropriate pool bucket. Buffers that do not
// match any bucket (oversized or misaligned capacity) are simply dropped
// for GC collection.
func (x *FramePool) Put(buf []byte) {
	c := cap(buf)
	idx := bucketIndexExact(c)
	if idx < 0 || idx >= numBuckets {
		return // oversized or misaligned — let GC collect it
	}
	buf = buf[:c]
	x.pools[idx].Put(&buf)
}

// bucketIndex returns the pool index for a buffer of size n.
func bucketIndex(n int) int {
	if n <= 1<<minBucketShift {
		return 0
	}
	// Find the smallest power-of-two >= n by counting the bit-width of (n-1).
	shift := 0
	v := n - 1
	for v > 0 {
		v >>= 1
		shift++
	}
	idx := shift - minBucketShift
	if idx >= numBuckets {
		return numBuckets // signals "oversized"
	}
	return idx
}

// bucketIndexExact returns the pool index only if cap is an exact
// power-of-two matching a bucket boundary. Returns -1 otherwise.
func bucketIndexExact(c int) int {
	if c == 0 || c&(c-1) != 0 {
		return -1 // not a power of two
	}
	shift := 0
	v := c
	for v > 1 {
		v >>= 1
		shift++
	}
	idx := shift - minBucketShift
	if idx < 0 || idx >= numBuckets {
		return -1
	}
	return idx
}
