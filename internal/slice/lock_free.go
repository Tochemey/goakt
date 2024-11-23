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

package slice

import (
	"sync/atomic"
	"unsafe"
)

type header[T any] struct {
	data  []T
	count uint64
}

// LockFree type that can be safely shared between goroutines.
type LockFree[T any] struct {
	head unsafe.Pointer
}

// NewLockFree creates a new lock-free thread-safe slice.
func NewLockFree[T any]() *LockFree[T] {
	return &LockFree[T]{
		head: unsafe.Pointer(&header[T]{
			data:  make([]T, 0),
			count: 0,
		}),
	}
}

// Len returns the number of items
func (cs *LockFree[T]) Len() int {
	head := (*header[T])(atomic.LoadPointer(&cs.head))
	return int(atomic.LoadUint64(&head.count))
}

// Append adds an item to the concurrent slice.
func (cs *LockFree[T]) Append(item T) {
	for {
		currentHead := (*header[T])(atomic.LoadPointer(&cs.head))
		newData := append(currentHead.data, item)
		newCount := atomic.AddUint64(&currentHead.count, 1)
		newHead := &header[T]{
			data:  newData,
			count: newCount,
		}

		if atomic.CompareAndSwapPointer(&cs.head, unsafe.Pointer(currentHead), unsafe.Pointer(newHead)) {
			return
		}
	}
}

// Get returns the slice item at the given index
func (cs *LockFree[T]) Get(index int) (item T) {
	data := (*header[T])(atomic.LoadPointer(&cs.head)).data
	if isSet(data, index) {
		return data[index]
	}
	var xnil T
	return xnil
}

// Delete an item from the slice
func (cs *LockFree[T]) Delete(index int) {
	for {
		currentHead := (*header[T])(atomic.LoadPointer(&cs.head))
		if isSet(currentHead.data, index) {
			newData := append(currentHead.data[:index], currentHead.data[index+1:]...)
			newCount := atomic.AddUint64(&currentHead.count, ^uint64(0))
			newHead := &header[T]{
				data:  newData,
				count: newCount,
			}
			if atomic.CompareAndSwapPointer(&cs.head, unsafe.Pointer(currentHead), unsafe.Pointer(newHead)) {
				return
			}
		}
	}
}

// Items returns the list of items
func (cs *LockFree[T]) Items() []T {
	return (*header[T])(atomic.LoadPointer(&cs.head)).data
}

// Reset resets the slice
func (cs *LockFree[T]) Reset() {
	for {
		currentHead := (*header[T])(atomic.LoadPointer(&cs.head))
		newHead := unsafe.Pointer(&header[T]{
			data:  make([]T, 0),
			count: 0,
		})
		if atomic.CompareAndSwapPointer(&cs.head, unsafe.Pointer(currentHead), unsafe.Pointer(newHead)) {
			return
		}
	}
}

func isSet[T any](arr []T, index int) bool {
	return len(arr) > index
}
