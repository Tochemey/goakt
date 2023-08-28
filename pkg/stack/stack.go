package stack

import "sync"

// Stack is a last-in-first-out data structure
type Stack[T any] struct {
	mutex sync.Mutex
	items []T
}

// New creates a new stack
func New[T any]() *Stack[T] {
	return &Stack[T]{
		mutex: sync.Mutex{},
		items: make([]T, 0),
	}
}

// Peek helps view the top item on the stack
func (s *Stack[T]) Peek() (item T, ok bool) {
	// acquire the lock
	s.mutex.Lock()
	// release the lock
	defer s.mutex.Unlock()
	if length := len(s.items); length > 0 {
		ok = true
		item = s.items[length-1]
	}
	return
}

// Pop removes and return top element of stack. Return false if stack is empty.
func (s *Stack[T]) Pop() (item T, ok bool) {
	// acquire the lock
	s.mutex.Lock()
	// release the lock
	defer s.mutex.Unlock()
	if length := len(s.items); length > 0 {
		// get the index of the top most element.
		length--
		ok = true
		// index into the slice and obtain the element.
		item = s.items[length]
		// remove it from the stack by slicing it off.
		s.items = s.items[:length]
	}
	return
}

// Push a new value onto the stack
func (s *Stack[T]) Push(item T) {
	// acquire the lock
	s.mutex.Lock()
	// release the lock
	defer s.mutex.Unlock()
	s.items = append(s.items, item)
}

// Len returns the length of the stack.
func (s *Stack[T]) Len() int {
	// acquire the lock
	s.mutex.Lock()
	// release the lock
	defer s.mutex.Unlock()
	return len(s.items)
}

// IsEmpty checks if stack is empty
func (s *Stack[T]) IsEmpty() bool {
	return s.Len() == 0
}

// Clear empty the stack
func (s *Stack[T]) Clear() {
	// acquire the lock
	s.mutex.Lock()
	// release the lock
	defer s.mutex.Unlock()
	if len(s.items) == 0 {
		return
	}

	s.items = []T{}
}
