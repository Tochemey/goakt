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

// Package workerpool provides a highly efficient and scalable worker pool implementation
// for concurrent task execution in Go applications.
package workerpool

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v3/internal/bufferpool"
	"github.com/tochemey/goakt/v3/internal/ticker"
)

const (
	// Maximum number of shards supported by the worker pool
	maxShards = 128

	// Worker states
	workerStateIdle    int32 = 0 // Worker is idle and available for work
	workerStateWorking int32 = 1 // Worker is currently executing a task
	workerStateClosed  int32 = 2 // Worker has been closed and should not be used
)

// Option is the interface that applies a WorkerPool option.
type Option interface {
	// Apply sets the Option value of a WorkerPool.
	Apply(pool *WorkerPool)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(pool *WorkerPool)

// Apply applies the Node's option
func (f OptionFunc) Apply(pool *WorkerPool) {
	f(pool)
}

// WithPassivateAfter sets the passivate after duration
func WithPassivateAfter(d time.Duration) Option {
	return OptionFunc(func(pool *WorkerPool) {
		pool.passivateAfter = d
	})
}

// WithNumShards sets the number of shards
func WithNumShards(numShards int) Option {
	return OptionFunc(func(pool *WorkerPool) {
		if numShards > maxShards {
			numShards = maxShards
		}
		pool.numShards = numShards
	})
}

// WorkerPool manages a pool of workers across multiple shards for efficient
// concurrent task execution.
type WorkerPool struct {
	passivateAfter time.Duration // Duration after which idle workers are cleaned up
	numShards      int           // Number of shards to distribute work across
	shards         []*poolShard  // Shards containing workers
	mutex          sync.RWMutex  // Mutex for pool-wide operations
	started        atomic.Bool   // Flag indicating if the pool has been started
	stopped        atomic.Bool   // Flag indicating if the pool has been stopped
	spawnedWorkers atomic.Uint64 // Counter for tracking total active workers
}

// Worker represents a goroutine that executes submitted tasks.
type Worker struct {
	workChan  chan func()  // Channel for receiving work
	shard     *poolShard   // Reference to the shard this worker belongs to
	lastUsed  atomic.Int64 // Timestamp of last use (UnixNano)
	isDeleted atomic.Bool  // Flag indicating if worker has been marked for deletion
	state     atomic.Int32 // Current state of the worker (idle, working, closed)
}

// poolShard represents a subdivision of the worker pool that manages a subset
// of workers to reduce contention.
type poolShard struct {
	wp          *WorkerPool            // Reference to parent worker pool
	workers     sync.Pool              // Pool for worker object reuse
	idleWorkers []*Worker              // Slice of idle workers (slow path)
	idleWorker1 atomic.Pointer[Worker] // Fast path worker 1
	idleWorker2 atomic.Pointer[Worker] // Fast path worker 2
	mu          sync.Mutex             // Mutex for shard operations
	stopped     atomic.Bool            // Flag indicating if shard has been stopped
}

// doWork is the main worker goroutine function that processes incoming tasks.
func (worker *Worker) doWork() {
	shard := worker.shard
	wp := shard.wp
	wp.spawnedWorkers.Add(1)

	for work := range worker.workChan {
		// Execute the task
		work()

		// Mark worker as idle and make it available for more work
		worker.state.Store(workerStateIdle)
		if !shard.setWorkerIdle(worker) {
			break
		}
	}

	// When worker exits, decrement counter and return to pool
	wp.spawnedWorkers.Add(^uint64(0)) // Decrement by 1
	shard.workers.Put(worker)
}

// New creates a new worker pool with the given options.
func New(opts ...Option) *WorkerPool {
	wp := &WorkerPool{
		passivateAfter: time.Second, // Default cleanup interval
		numShards:      1,           // Default to single shard
	}

	// Apply provided options
	for _, opt := range opts {
		opt.Apply(wp)
	}

	// Ensure numShards is within bounds
	if wp.numShards < 1 {
		wp.numShards = 1
	} else if wp.numShards > maxShards {
		wp.numShards = maxShards
	}

	return wp
}

// GetSpawnedWorkers returns the current count of active workers.
func (wp *WorkerPool) GetSpawnedWorkers() int {
	return int(wp.spawnedWorkers.Load())
}

// Start initializes the worker pool and begins the cleanup routine.
// It's safe to call Start multiple times.
func (wp *WorkerPool) Start() {
	wp.mutex.Lock()
	if !wp.started.Load() {
		// Pre-allocate the slice to avoid resizing
		wp.shards = make([]*poolShard, wp.numShards)
		for i := 0; i < wp.numShards; i++ {
			shard := &poolShard{
				wp: wp,
				workers: sync.Pool{
					New: func() any {
						return &Worker{
							workChan: make(chan func()),
						}
					},
				},
				// Pre-allocate with capacity to avoid frequent resizing
				idleWorkers: make([]*Worker, 0, 2048),
			}
			wp.shards[i] = shard
		}

		wp.started.Store(true)
		go wp.cleanup() // Start cleanup goroutine
	}
	wp.mutex.Unlock()
}

// Stop gracefully shuts down the worker pool by closing all worker channels
// and preventing new task submissions.
func (wp *WorkerPool) Stop() {
	wp.mutex.Lock()
	if !wp.started.Load() {
		wp.mutex.Unlock()
		return
	}

	// Only run shutdown logic once
	if !wp.stopped.Swap(true) {
		for i := 0; i < wp.numShards; i++ {
			shard := wp.shards[i]
			shard.mu.Lock()
			shard.stopped.Store(true)

			// Close all workers in the idle slice
			for j := range shard.idleWorkers {
				worker := shard.idleWorkers[j]
				if !worker.isDeleted.Swap(true) {
					worker.state.Store(workerStateClosed)
					close(worker.workChan)
				}
				shard.idleWorkers[j] = nil // Help GC
			}
			shard.idleWorkers = shard.idleWorkers[:0]

			// Close fast path workers
			if w1 := shard.idleWorker1.Swap(nil); w1 != nil && !w1.isDeleted.Swap(true) {
				w1.state.Store(workerStateClosed)
				close(w1.workChan)
			}

			if w2 := shard.idleWorker2.Swap(nil); w2 != nil && !w2.isDeleted.Swap(true) {
				w2.state.Store(workerStateClosed)
				close(w2.workChan)
			}

			shard.mu.Unlock()
		}
	}
	wp.mutex.Unlock()
}

// SubmitWork submits a task to be executed by an available worker.
// If the pool has not been started, the task will be discarded.
func (wp *WorkerPool) SubmitWork(task func()) {
	// Use RLock for better concurrency on the read path
	wp.mutex.RLock()
	if !wp.started.Load() || wp.stopped.Load() {
		wp.mutex.RUnlock()
		return
	}

	// Select a shard using fast thread-local random
	shardIndex := fastRand() % uint32(wp.numShards)
	shard := wp.shards[shardIndex]
	wp.mutex.RUnlock()

	// Hand off task to the selected shard
	shard.acquireWorker(task)
}

// acquireWorker gets an available worker or creates a new one and
// submits the task to it.
func (shard *poolShard) acquireWorker(task func()) (worker *Worker) {
	// Fast path: try to use cached idle workers without locking
	if w := shard.idleWorker1.Swap(nil); w != nil {
		// Verify worker is still valid before sending work
		if !w.isDeleted.Load() && w.state.CompareAndSwap(workerStateIdle, workerStateWorking) {
			w.workChan <- task
			return w
		}
		// Worker was already deleted or in wrong state
		if !w.isDeleted.Load() {
			shard.setWorkerIdle(w) // Put it back
		}
	}

	if w := shard.idleWorker2.Swap(nil); w != nil {
		if !w.isDeleted.Load() && w.state.CompareAndSwap(workerStateIdle, workerStateWorking) {
			w.workChan <- task
			return w
		}
		if !w.isDeleted.Load() {
			shard.setWorkerIdle(w)
		}
	}

	// Slow path: take a lock and check the idle workers slice
	shard.mu.Lock()

	// Exit early if shard is stopped
	if shard.stopped.Load() {
		shard.mu.Unlock()
		return nil
	}

	length := len(shard.idleWorkers)
	if length > 0 {
		worker = shard.idleWorkers[length-1]
		shard.idleWorkers[length-1] = nil // Help GC
		shard.idleWorkers = shard.idleWorkers[:length-1]
		shard.mu.Unlock()

		// Verify worker is still valid
		if !worker.isDeleted.Load() && worker.state.CompareAndSwap(workerStateIdle, workerStateWorking) {
			worker.workChan <- task
			return worker
		}
		return nil
	}
	shard.mu.Unlock()

	// Create new worker if needed
	worker = shard.workers.Get().(*Worker)
	worker.shard = shard
	if worker.workChan == nil {
		worker.workChan = make(chan func())
	}
	worker.state.Store(workerStateWorking)
	worker.isDeleted.Store(false)
	go worker.doWork()

	worker.workChan <- task
	return worker
}

// setWorkerIdle marks a worker as idle and makes it available for future tasks.
// Returns false if the worker's shard has been stopped.
func (shard *poolShard) setWorkerIdle(worker *Worker) bool {
	// Update last used timestamp
	worker.lastUsed.Store(time.Now().UnixNano())

	// Exit early if shard is stopped
	if shard.stopped.Load() {
		return false
	}

	// Fast path: try to store in atomic pointers first
	if shard.idleWorker1.CompareAndSwap(nil, worker) {
		return true
	}

	if shard.idleWorker2.CompareAndSwap(nil, worker) {
		return true
	}

	// Slow path: add to slice with lock
	shard.mu.Lock()
	if shard.stopped.Load() {
		shard.mu.Unlock()
		return false
	}

	shard.idleWorkers = append(shard.idleWorkers, worker)
	shard.mu.Unlock()
	return true
}

// cleanup periodically checks for and removes idle workers
// that haven't been used for longer than passivateAfter.
func (wp *WorkerPool) cleanup() {
	var workers []*Worker
	ticker := ticker.New(wp.passivateAfter)
	defer ticker.Stop()

	for range ticker.Ticks {
		if wp.stopped.Load() {
			return
		}

		now := time.Now().UnixNano()
		cutoffTime := now - wp.passivateAfter.Nanoseconds()

		// Process each shard
		for i := 0; i < wp.numShards; i++ {
			shard := wp.shards[i]

			// Skip if shard is stopped
			if shard.stopped.Load() {
				continue
			}

			shard.mu.Lock()
			length := len(shard.idleWorkers)

			// Only clean up if we have a significant number of idle workers
			if length <= 400 {
				shard.mu.Unlock()
				continue
			}

			// Binary search to find workers to clean up
			// Find the first worker that's older than passivateAfter
			start := 0
			end := length - 1
			pos := length

			for start <= end {
				mid := start + (end-start)/2
				if shard.idleWorkers[mid].lastUsed.Load() < cutoffTime {
					pos = mid
					end = mid - 1
				} else {
					start = mid + 1
				}
			}

			// No old workers
			if pos >= length {
				shard.mu.Unlock()
				continue
			}

			// Collect workers to clean up
			cleanupCount := length - pos
			if cleanupCount > 0 {
				// Resize workers slice if needed
				if cap(workers) < cleanupCount {
					workers = make([]*Worker, cleanupCount)
				} else {
					workers = workers[:cleanupCount]
				}

				// Copy workers to clean up
				copy(workers, shard.idleWorkers[pos:])

				// Trim the idle workers slice
				for j := pos; j < length; j++ {
					shard.idleWorkers[j] = nil // Help GC
				}
				shard.idleWorkers = shard.idleWorkers[:pos]
			}
			shard.mu.Unlock()

			// Close worker channels outside of lock
			for j := range workers {
				// Mark as deleted first to prevent races
				if !workers[j].isDeleted.Swap(true) {
					workers[j].state.Store(workerStateClosed)
					close(workers[j].workChan)
				}
				workers[j] = nil // Help GC
			}
		}
	}
}

// fastRand returns a random uint32 using thread-local random number generators
// to avoid contention.
func fastRand() uint32 {
	// Get buffer from pool
	buf := bufferpool.Pool.Get()
	defer bufferpool.Pool.Put(buf)

	// Ensure buffer has capacity for 4 bytes
	buf.Grow(4)
	b := buf.Bytes()[:0] // Get the underlying slice, but with zero length

	// Create a properly sized slice that uses the buffer's capacity
	data := append(b, make([]byte, 4)...)

	// Read secure random bytes
	n, err := rand.Read(data)

	// Verify we got all the bytes we needed
	if err != nil || n != 4 {
		// Fallback in the extremely unlikely case of failure
		return uint32(time.Now().UnixNano())
	}

	// Convert bytes to uint32
	return binary.LittleEndian.Uint32(data)
}
