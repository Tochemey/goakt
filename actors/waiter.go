package actors

import (
	"sync"
	"time"
)

// Waiter wraps a sync.WaitGroup and help
type Waiter[T any] struct {
	wg  sync.WaitGroup
	res T
	err error

	mu sync.Mutex
}

// SetError sets the error response
func (w *Waiter[T]) SetError(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.err = err
}

// SetResult sets the  response
func (w *Waiter[T]) SetResult(res T) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.res = res
}

// WaitGroupTimeout waits for the sync.WaitGroup for the specified max timeout.
// Returns true if waiting timed out.
func WaitGroupTimeout(wg *sync.WaitGroup, timeout time.Duration) error {
	c := make(chan struct{})
	go func() {
		defer close(c)
		wg.Wait()
	}()
	select {
	case <-c:
		return nil
	case <-time.After(timeout):
		return ErrTimeout
	}
}
