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
	"slices"
	"sync"

	"github.com/tochemey/goakt/v3/internal/locker"
)

// List is a thread-safe, duplicate-free ordered collection for comparable
// element types.
//
// # Low-GC design
//
// The backing slice is pre-allocated with a small initial capacity and reused
// across [List.Reset] calls (via slice-to-zero-length rather than
// reallocation). Cleared element slots are zeroed immediately so that pointer
// or interface values do not prevent garbage collection.
//
// # Thread safety
//
// All operations acquire the appropriate read or write lock on the embedded
// RWMutex. Every data access — including the element read in [List.Get] —
// is performed while the lock is held, preventing concurrent reallocation
// from invalidating a bounds check that was made under a prior lock.
//
// # Deduplication
//
// [List.Append] and [List.AppendMany] are no-ops for items that are already
// present (using == comparison on T). The constraint T comparable is required
// to enable this check.
type List[T comparable] struct {
	_    locker.NoCopy
	data []T
	mu   sync.RWMutex
}

// NewList creates a new List with a small pre-allocated backing array.
func NewList[T comparable]() *List[T] {
	return &List[T]{data: make([]T, 0, 4)}
}

// Len returns the number of items in the list.
func (x *List[T]) Len() int {
	x.mu.RLock()
	l := len(x.data)
	x.mu.RUnlock()
	return l
}

// Contains reports whether item is present in the list.
func (x *List[T]) Contains(item T) bool {
	x.mu.RLock()
	found := x.contains(item)
	x.mu.RUnlock()
	return found
}

// Append adds item to the list only if it is not already present.
func (x *List[T]) Append(item T) {
	x.mu.Lock()
	if !x.contains(item) {
		x.data = append(x.data, item)
	}
	x.mu.Unlock()
}

// AppendMany adds each element of items to the list, skipping any that are
// already present. Duplicate entries within items itself are also collapsed.
func (x *List[T]) AppendMany(items ...T) {
	x.mu.Lock()
	for _, item := range items {
		if !x.contains(item) {
			x.data = append(x.data, item)
		}
	}
	x.mu.Unlock()
}

// Get returns the element at index. Returns the zero value of T if index is
// out of range. The element is read while the lock is held, preventing a
// concurrent reallocation from invalidating a prior bounds check.
func (x *List[T]) Get(index int) T {
	x.mu.RLock()
	if index < 0 || index >= len(x.data) {
		x.mu.RUnlock()
		var zero T
		return zero
	}
	item := x.data[index]
	x.mu.RUnlock()
	return item
}

// Delete removes the element at index. Out-of-range indices are silently
// ignored. The vacated trailing slot is zeroed to release any GC-retained
// pointer or interface value.
func (x *List[T]) Delete(index int) {
	x.mu.Lock()
	if index >= 0 && index < len(x.data) {
		x.data = slices.Delete(x.data, index, index+1)
	}
	x.mu.Unlock()
}

// Items returns a snapshot copy of all elements in insertion order.
func (x *List[T]) Items() []T {
	x.mu.RLock()
	out := make([]T, len(x.data))
	copy(out, x.data)
	x.mu.RUnlock()
	return out
}

// Reset removes all elements while retaining the backing array so that
// subsequent appends do not trigger a new allocation. All slots are zeroed
// before the length is set to zero to release any GC-retained references.
func (x *List[T]) Reset() {
	x.mu.Lock()
	clear(x.data)
	x.data = x.data[:0]
	x.mu.Unlock()
}

// contains reports whether item is already in x.data.
// Callers must hold at least a read lock.
func (x *List[T]) contains(item T) bool {
	for _, v := range x.data {
		if v == item {
			return true
		}
	}
	return false
}
