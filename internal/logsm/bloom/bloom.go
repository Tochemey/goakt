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

package bloom

import (
	"hash"
	"math"

	"github.com/spaolacci/murmur3"

	"github.com/tochemey/goakt/v3/internal/internalpb"
)

const _defaultP = 0.01

type Filter struct {
	bitset  []bool
	hashFns []hash.Hash32
}

// New creates a new BloomFilter with the given size and number of hash functions.
// n: expected nums of elements
// p: expected rate of false errors
func New(n int, p float64) *Filter {
	if n <= 0 || (p <= 0 || p >= 1) {
		panic("invalid parameters")
	}

	// size of bitset
	// m = -(n * ln(p)) / (ln(2)^2)
	m := int(math.Ceil(-float64(n) * math.Log(p) / math.Pow(math.Log(2), 2)))
	// nums of hash functions used
	// k = (m/n) * ln(2)
	k := int(math.Round((float64(m) / float64(n)) * math.Log(2)))

	hashFns := make([]hash.Hash32, k)
	for i := range k {
		hashFns[i] = murmur3.New32WithSeed(uint32(i))
	}

	return &Filter{
		bitset:  make([]bool, m),
		hashFns: hashFns,
	}
}

func Build(kvs []*internalpb.Entry) *Filter {
	filter := New(len(kvs), _defaultP)
	for _, e := range kvs {
		filter.Add(e.Key)
	}
	return filter
}

// Add adds an element to the BloomFilter.
func (f *Filter) Add(key string) {
	for _, fn := range f.hashFns {
		_, _ = fn.Write([]byte(key))
		index := int(fn.Sum32()) % len(f.bitset)
		f.bitset[index] = true
		fn.Reset()
	}
}

// Contains checks if an element is in the BloomFilter.
func (f *Filter) Contains(key string) bool {
	for _, fn := range f.hashFns {
		_, _ = fn.Write([]byte(key))
		index := int(fn.Sum32()) % len(f.bitset)
		fn.Reset()
		if !f.bitset[index] {
			return false
		}
	}
	return true
}
