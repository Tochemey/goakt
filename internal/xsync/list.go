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

// List type that can be safely shared between goroutines.
type List[T any] struct {
	_    locker.NoCopy
	data []T
	mu   sync.RWMutex
}

// NewList creates a new lock-free thread-safe slice.
func NewList[T any]() *List[T] {
	return &List[T]{data: []T{}}
}

// Len returns the number of items
func (x *List[T]) Len() int {
	x.mu.RLock()
	l := len(x.data)
	x.mu.RUnlock()
	return l
}

// Append adds an item to the concurrent slice.
func (x *List[T]) Append(item T) {
	x.mu.Lock()
	x.data = append(x.data, item)
	x.mu.Unlock()
}

// AppendMany adds many items to the concurrent slice
func (x *List[T]) AppendMany(item ...T) {
	x.mu.Lock()
	x.data = append(x.data, item...)
	x.mu.Unlock()
}

// Get returns the slice item at the given index
func (x *List[T]) Get(index int) (item T) {
	x.mu.RLock()
	if index < 0 || index >= len(x.data) {
		var zero T
		x.mu.RUnlock()
		return zero
	}
	x.mu.RUnlock()
	return x.data[index]
}

// Delete an item from the slice
func (x *List[T]) Delete(index int) {
	x.mu.Lock()
	if index < 0 || index >= len(x.data) {
		x.mu.Unlock()
		return
	}
	x.data = slices.Delete(x.data, index, index+1)
	x.mu.Unlock()
}

// Items returns the list of items
func (x *List[T]) Items() []T {
	x.mu.RLock()
	dataCopy := make([]T, len(x.data))
	copy(dataCopy, x.data)
	x.mu.RUnlock()
	return dataCopy
}

// Reset resets the slice
func (x *List[T]) Reset() {
	x.mu.Lock()
	x.data = []T{}
	x.mu.Unlock()
}
