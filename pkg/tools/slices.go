package tools

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
	if isset(cs.items, index) {
		return cs.items[index]
	}
	return nil
}

// Delete an item from the slice
func (cs *ConcurrentSlice[T]) Delete(index int) {
	cs.RLock()
	defer cs.RUnlock()
	var nilState T
	if isset(cs.items, index) {
		// Remove the element at index from the slice
		cs.items[index] = cs.items[len(cs.items)-1] // Copy last element to index.
		cs.items[len(cs.items)-1] = nilState        // Erase last element (write zero value).
		cs.items = cs.items[:len(cs.items)-1]       // Truncate slice.
	}
}

func isset[T any](arr []T, index int) bool {
	return len(arr) > index
}

// Iter iterates the items in the concurrent slice.
// Each item is sent over a channel, so that
// we can iterate over the slice using the builin range keyword.
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
