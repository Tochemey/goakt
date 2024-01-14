/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

package slices

import "sync"

// ConcurrentSlice type that can be safely shared between goroutines.
type ConcurrentSlice[T any] struct {
	sync.RWMutex
	items []T
}

// Item represents the slice item
type Item[T any] struct {
	Index int
	Value T
}

// NewConcurrentSlice creates a new synchronized slice.
func NewConcurrentSlice[T any]() *ConcurrentSlice[T] {
	cs := &ConcurrentSlice[T]{
		items: make([]T, 0),
	}

	return cs
}

// Len returns the number of items
func (cs *ConcurrentSlice[T]) Len() int {
	cs.Lock()
	defer cs.Unlock()
	return len(cs.items)
}

// Append adds an item to the concurrent slice.
func (cs *ConcurrentSlice[T]) Append(item T) {
	cs.Lock()
	defer cs.Unlock()
	cs.items = append(cs.items, item)
}

// Get returns the slice item at the given index
func (cs *ConcurrentSlice[T]) Get(index int) (item any) {
	cs.RLock()
	defer cs.RUnlock()
	if isSet(cs.items, index) {
		return cs.items[index]
	}
	return nil
}

// Delete an item from the slice
func (cs *ConcurrentSlice[T]) Delete(index int) {
	cs.RLock()
	defer cs.RUnlock()
	var nilState T
	if isSet(cs.items, index) {
		// Pop the element at index from the slice
		cs.items[index] = cs.items[len(cs.items)-1] // Copy last element to index.
		cs.items[len(cs.items)-1] = nilState        // Erase last element (write zero value).
		cs.items = cs.items[:len(cs.items)-1]       // Truncate slice.
	}
}

func isSet[T any](arr []T, index int) bool {
	return len(arr) > index
}

// Iter iterates the items in the concurrent slice.
// Each item is sent over a channel, so that
// we can iterate over the slice using the builtin range keyword.
func (cs *ConcurrentSlice[T]) Iter() <-chan Item[T] {
	c := make(chan Item[T])
	f := func() {
		cs.RLock()
		defer cs.RUnlock()
		for index, value := range cs.items {
			c <- Item[T]{Index: index, Value: value}
		}
		close(c)
	}
	go f()

	return c
}
