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

package slice

import "sync"

// Slice type that can be safely shared between goroutines.
type Slice[T any] struct {
	sync.RWMutex
	items []T
}

// Item represents the slice item
type Item[T any] struct {
	Index int
	Value T
}

// New creates a new synchronized slice.
func New[T any]() *Slice[T] {
	cs := &Slice[T]{
		items: make([]T, 0),
	}

	return cs
}

// Len returns the number of items
func (cs *Slice[T]) Len() int {
	cs.Lock()
	length := len(cs.items)
	cs.Unlock()
	return length
}

// Append adds an item to the concurrent slice.
func (cs *Slice[T]) Append(item T) {
	cs.Lock()
	cs.items = append(cs.items, item)
	cs.Unlock()
}

// Get returns the slice item at the given index
func (cs *Slice[T]) Get(index int) (item any) {
	cs.RLock()
	defer cs.RUnlock()
	if isSet(cs.items, index) {
		return cs.items[index]
	}
	return nil
}

// Delete an item from the slice
func (cs *Slice[T]) Delete(index int) {
	cs.RLock()
	isSet := isSet(cs.items, index)
	cs.RUnlock()
	var nilState T
	if isSet {
		cs.Lock()
		// Pop the element at index from the slice
		cs.items[index] = cs.items[len(cs.items)-1] // Copy last element to index.
		cs.items[len(cs.items)-1] = nilState        // Erase last element (write zero value).
		cs.items = cs.items[:len(cs.items)-1]       // Truncate slice.
		cs.Unlock()
	}
}

// Items returns the list of items
func (cs *Slice[T]) Items() []Item[T] {
	cs.RLock()
	items := cs.items
	cs.RUnlock()
	output := make([]Item[T], len(items))
	for index, value := range items {
		output[index] = Item[T]{Index: index, Value: value}
	}
	return output
}

func (cs *Slice[T]) Reset() {
	cs.Lock()
	cs.items = make([]T, 0)
	cs.Unlock()
}

func isSet[T any](arr []T, index int) bool {
	return len(arr) > index
}
