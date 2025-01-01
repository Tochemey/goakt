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

package slice

import (
	"sync"
)

// Safe type that can be safely shared between goroutines.
type Safe[T any] struct {
	data []T
	mu   sync.RWMutex
}

// NewSafe creates a new lock-free thread-safe slice.
func NewSafe[T any]() *Safe[T] {
	return &Safe[T]{data: []T{}}
}

// Len returns the number of items
func (cs *Safe[T]) Len() int {
	cs.mu.RLock()
	l := len(cs.data)
	cs.mu.RUnlock()
	return l
}

// Append adds an item to the concurrent slice.
func (cs *Safe[T]) Append(item T) {
	cs.mu.Lock()
	cs.data = append(cs.data, item)
	cs.mu.Unlock()
}

// AppendMany adds many items to the concurrent slice
func (cs *Safe[T]) AppendMany(item ...T) {
	cs.mu.Lock()
	cs.data = append(cs.data, item...)
	cs.mu.Unlock()
}

// Get returns the slice item at the given index
func (cs *Safe[T]) Get(index int) (item T) {
	cs.mu.RLock()
	if index < 0 || index >= len(cs.data) {
		var zero T
		cs.mu.RUnlock()
		return zero
	}
	cs.mu.RUnlock()
	return cs.data[index]
}

// Delete an item from the slice
func (cs *Safe[T]) Delete(index int) {
	cs.mu.Lock()
	if index < 0 || index >= len(cs.data) {
		cs.mu.Unlock()
		return
	}
	cs.data = append(cs.data[:index], cs.data[index+1:]...)
	cs.mu.Unlock()
}

// Items returns the list of items
func (cs *Safe[T]) Items() []T {
	cs.mu.RLock()
	dataCopy := make([]T, len(cs.data))
	copy(dataCopy, cs.data)
	cs.mu.RUnlock()
	return dataCopy
}

// Reset resets the slice
func (cs *Safe[T]) Reset() {
	cs.mu.Lock()
	cs.data = []T{}
	cs.mu.Unlock()
}
