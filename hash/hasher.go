/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package hash

import (
	"github.com/zeebo/xxh3"
)

// Hasher defines the hashcode generator interface.
// This help for actors partitioning when cluster mode is enabled
type Hasher interface {
	// HashCode is responsible for generating unsigned, 64-bit hash of provided byte slice
	HashCode(key []byte) uint64
}

type defaultHasher struct{}

var _ Hasher = defaultHasher{}

// HashCode implementation
func (x defaultHasher) HashCode(key []byte) uint64 {
	return xxh3.Hash(key)
}

// DefaultHasher returns the default hasher
func DefaultHasher() Hasher {
	return &defaultHasher{}
}
