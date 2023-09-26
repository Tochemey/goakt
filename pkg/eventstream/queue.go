package eventstream

import "sync"

// reference: https://blog.dubbelboer.com/2015/04/25/go-faster-queue.htmlß
type unboundedQueue[T any] struct {
	mu      sync.RWMutex
	cond    *sync.Cond
	nodes   []T
	head    int
	tail    int
	cnt     int
	closed  bool
	initCap int
}

func newUnboundedQueue[T any](initialCapacity int) *unboundedQueue[T] {
	sq := &unboundedQueue[T]{
		initCap: initialCapacity,
		nodes:   make([]T, initialCapacity),
	}
	sq.cond = sync.NewCond(&sq.mu)
	return sq
}

// Write mutex must be held when calling
func (q *unboundedQueue[T]) resize(n int) {
	nodes := make([]T, n)
	if q.head < q.tail {
		copy(nodes, q.nodes[q.head:q.tail])
	} else {
		copy(nodes, q.nodes[q.head:])
		copy(nodes[len(q.nodes)-q.head:], q.nodes[:q.tail])
	}

	q.tail = q.cnt % n
	q.head = 0
	q.nodes = nodes
}

// Push adds an item to the back of the queue
// It can be safely called from multiple goroutines
// It will return false if the queue is closed.
// In that case the Item is dropped.
func (q *unboundedQueue[T]) Push(i T) bool {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return false
	}
	if q.cnt == len(q.nodes) {
		// Also tested a grow rate of 1.5, see: http://stackoverflow.com/questions/2269063/buffer-growth-strategy
		// In Go this resulted in a higher memory usage.
		q.resize(q.cnt * 2)
	}
	q.nodes[q.tail] = i
	q.tail = (q.tail + 1) % len(q.nodes)
	q.cnt++
	q.cond.Signal()
	q.mu.Unlock()
	return true
}

// Close the queue and discard all entried in the queue
// all goroutines in wait() will return
func (q *unboundedQueue[T]) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cnt = 0
	q.nodes = nil
	q.cond.Broadcast()
}

// CloseRemaining will close the queue and return all entried in the queue.
// All goroutines in wait() will return.
func (q *unboundedQueue[T]) CloseRemaining() []T {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return []T{}
	}
	rem := make([]T, 0, q.cnt)
	for q.cnt > 0 {
		i := q.nodes[q.head]
		q.head = (q.head + 1) % len(q.nodes)
		q.cnt--
		rem = append(rem, i)
	}
	q.closed = true
	q.cnt = 0
	q.nodes = nil
	q.cond.Broadcast()
	return rem
}

// IsClosed returns true if the queue has been closed
// The call cannot guarantee that the queue hasn't been
// closed while the function returns, so only "true" has a definite meaning.
func (q *unboundedQueue[T]) IsClosed() bool {
	q.mu.RLock()
	c := q.closed
	q.mu.RUnlock()
	return c
}

// Wait for an item to be added.
// If there is items on the queue the first will
// be returned immediately.
// Will return nil, false if the queue is closed.
// Otherwise, the return value of "remove" is returned.
func (q *unboundedQueue[T]) Wait() (T, bool) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		var nilElt T
		return nilElt, false
	}
	if q.cnt != 0 {
		q.mu.Unlock()
		return q.Pop()
	}
	q.cond.Wait()
	q.mu.Unlock()
	return q.Pop()
}

// Pop removes the item from the front of the queue
// If false is returned, it either means 1) there were no items on the queue
// or 2) the queue is closed.
func (q *unboundedQueue[T]) Pop() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.cnt == 0 {
		var nilElt T
		return nilElt, false
	}
	i := q.nodes[q.head]
	q.head = (q.head + 1) % len(q.nodes)
	q.cnt--

	if n := len(q.nodes) / 2; n >= q.initCap && q.cnt <= n {
		q.resize(n)
	}

	return i, true
}

// Cap return the capacity (without allocations)
func (q *unboundedQueue[T]) Cap() int {
	q.mu.RLock()
	c := cap(q.nodes)
	q.mu.RUnlock()
	return c
}

// Len return the current length of the queue.
func (q *unboundedQueue[T]) Len() int {
	q.mu.RLock()
	l := q.cnt
	q.mu.RUnlock()
	return l
}
