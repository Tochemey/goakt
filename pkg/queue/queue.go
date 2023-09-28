package queue

import "sync"

// Unbounded reference: https://blog.dubbelboer.com/2015/04/25/go-faster-queue.html
type Unbounded[T any] struct {
	mu      sync.RWMutex
	cond    *sync.Cond
	nodes   []T
	head    int
	tail    int
	count   int
	closed  bool
	initCap int
}

// NewUnbounded creates an instance of Unbounded
func NewUnbounded[T any](initialCapacity int) *Unbounded[T] {
	sq := &Unbounded[T]{
		initCap: initialCapacity,
		nodes:   make([]T, initialCapacity),
	}
	sq.cond = sync.NewCond(&sq.mu)
	return sq
}

// resize the queue
func (q *Unbounded[T]) resize(n int) {
	nodes := make([]T, n)
	if q.head < q.tail {
		copy(nodes, q.nodes[q.head:q.tail])
	} else {
		copy(nodes, q.nodes[q.head:])
		copy(nodes[len(q.nodes)-q.head:], q.nodes[:q.tail])
	}

	q.tail = q.count % n
	q.head = 0
	q.nodes = nodes
}

// Push adds an item to the back of the queue
// It can be safely called from multiple goroutines
// It will return false if the queue is closed.
// In that case the Item is dropped.
func (q *Unbounded[T]) Push(i T) bool {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return false
	}
	if q.count == len(q.nodes) {
		// Also tested a grow rate of 1.5, see: http://stackoverflow.com/questions/2269063/buffer-growth-strategy
		// In Go this resulted in a higher memory usage.
		q.resize(q.count * 2)
	}
	q.nodes[q.tail] = i
	q.tail = (q.tail + 1) % len(q.nodes)
	q.count++
	q.cond.Signal()
	q.mu.Unlock()
	return true
}

// Close the queue and discard all entries in the queue
// all goroutines in wait() will return
func (q *Unbounded[T]) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.count = 0
	q.nodes = nil
	q.cond.Broadcast()
}

// CloseRemaining will close the queue and return all entries in the queue.
// All goroutines in wait() will return.
func (q *Unbounded[T]) CloseRemaining() []T {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return []T{}
	}
	rem := make([]T, 0, q.count)
	for q.count > 0 {
		i := q.nodes[q.head]
		q.head = (q.head + 1) % len(q.nodes)
		q.count--
		rem = append(rem, i)
	}
	q.closed = true
	q.count = 0
	q.nodes = nil
	q.cond.Broadcast()
	return rem
}

// IsClosed returns true if the queue has been closed
// The call cannot guarantee that the queue hasn't been
// closed while the function returns, so only "true" has a definite meaning.
func (q *Unbounded[T]) IsClosed() bool {
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
func (q *Unbounded[T]) Wait() (T, bool) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		var nilElt T
		return nilElt, false
	}
	if q.count != 0 {
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
func (q *Unbounded[T]) Pop() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.count == 0 {
		var nilElt T
		return nilElt, false
	}
	i := q.nodes[q.head]
	q.head = (q.head + 1) % len(q.nodes)
	q.count--

	if n := len(q.nodes) / 2; n >= q.initCap && q.count <= n {
		q.resize(n)
	}

	return i, true
}

// Cap return the capacity (without allocations)
func (q *Unbounded[T]) Cap() int {
	q.mu.RLock()
	c := cap(q.nodes)
	q.mu.RUnlock()
	return c
}

// Len return the current length of the queue.
func (q *Unbounded[T]) Len() int {
	q.mu.RLock()
	l := q.count
	q.mu.RUnlock()
	return l
}
