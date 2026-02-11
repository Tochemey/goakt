// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package tcp

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const defaultIdleWorkerLifetime = time.Second
const maxShards = 128

type TaskHandlerFunc[T any] func(task T)

type WorkerPool[T any] struct {
	handlerFunc        TaskHandlerFunc[T]
	idleWorkerLifetime time.Duration
	numShards          int
	shards             []*poolShard[T]
	done               chan struct{}
	mutex              spinLocker
	started            atomic.Bool
	stopped            atomic.Bool
	_                  [48]byte
	spawnedWorkers     uint64
}

type workerInstance[T any] struct {
	taskChan chan T
	shard    *poolShard[T]
	lastUsed int64 // UnixNano, atomic
	_        [40]byte
}

type poolShard[T any] struct {
	wp             *WorkerPool[T]
	workerCache    sync.Pool
	idleWorkerList []*workerInstance[T]
	idleWorker1    atomic.Pointer[workerInstance[T]]
	idleWorker2    atomic.Pointer[workerInstance[T]]
	mutex          spinLocker
	stopped        atomic.Bool
}

func NewWorkerPool[T any](handlerFunc TaskHandlerFunc[T]) *WorkerPool[T] {
	wp := &WorkerPool[T]{
		handlerFunc:        handlerFunc,
		idleWorkerLifetime: defaultIdleWorkerLifetime,
		numShards:          1,
	}
	wp.SetNumShards(runtime.GOMAXPROCS(0))
	return wp
}

// SetNumShards must be called before Start.
func (wp *WorkerPool[T]) SetNumShards(numShards int) {
	if wp.started.Load() {
		return
	}
	if numShards < 1 {
		numShards = 1
	}
	if numShards > maxShards {
		numShards = maxShards
	}
	wp.numShards = numShards
}

// SetIdleWorkerLifetime must be called before Start.
func (wp *WorkerPool[T]) SetIdleWorkerLifetime(d time.Duration) {
	if wp.started.Load() {
		return
	}
	wp.idleWorkerLifetime = d
}

func (wp *WorkerPool[T]) GetSpawnedWorkers() int {
	return int(atomic.LoadUint64(&wp.spawnedWorkers))
}

func (wp *WorkerPool[T]) Start() {
	wp.mutex.Lock()
	if !wp.started.Load() {
		wp.shards = make([]*poolShard[T], wp.numShards)
		for i := range wp.shards {
			wp.shards[i] = &poolShard[T]{
				wp: wp,
				workerCache: sync.Pool{
					New: func() interface{} {
						return &workerInstance[T]{}
					},
				},
				idleWorkerList: make([]*workerInstance[T], 0, 2048),
			}
		}
		wp.done = make(chan struct{})
		wp.started.Store(true)
		go wp.cleanup()
	}
	wp.mutex.Unlock()
}

func (wp *WorkerPool[T]) Stop() {
	wp.mutex.Lock()
	if !wp.started.Load() || wp.stopped.Load() {
		wp.mutex.Unlock()
		return
	}
	wp.stopped.Store(true)
	close(wp.done)

	for i := 0; i < wp.numShards; i++ {
		shard := wp.shards[i]
		shard.mutex.Lock()
		shard.stopped.Store(true)

		if w := shard.idleWorker1.Swap(nil); w != nil {
			close(w.taskChan)
		}
		if w := shard.idleWorker2.Swap(nil); w != nil {
			close(w.taskChan)
		}

		for j := range shard.idleWorkerList {
			close(shard.idleWorkerList[j].taskChan)
			shard.idleWorkerList[j] = nil
		}
		shard.idleWorkerList = shard.idleWorkerList[:0]
		shard.mutex.Unlock()
	}
	wp.mutex.Unlock()
}

func (wp *WorkerPool[T]) AddTask(task T) error {
	if wp.stopped.Load() {
		return ErrPoolStopped
	}
	if !wp.started.Load() {
		return ErrPoolNotStarted
	}
	shard := wp.shards[randInt()%wp.numShards]
	shard.getWorker(task)
	return nil
}

func (wp *WorkerPool[T]) AddTaskForShard(task T, shardIdx int) error {
	if wp.stopped.Load() {
		return ErrPoolStopped
	}
	if !wp.started.Load() {
		return ErrPoolNotStarted
	}
	shard := wp.shards[shardIdx%wp.numShards]
	shard.getWorker(task)
	return nil
}

func (shard *poolShard[T]) getWorker(task T) {
	if worker := shard.idleWorker1.Load(); worker != nil {
		if shard.idleWorker1.CompareAndSwap(worker, nil) {
			worker.taskChan <- task
			return
		}
	}

	if worker := shard.idleWorker2.Load(); worker != nil {
		if shard.idleWorker2.CompareAndSwap(worker, nil) {
			worker.taskChan <- task
			return
		}
	}

	shard.mutex.Lock()
	if iws := len(shard.idleWorkerList); iws > 0 {
		worker := shard.idleWorkerList[iws-1]
		shard.idleWorkerList[iws-1] = nil
		shard.idleWorkerList = shard.idleWorkerList[:iws-1]
		shard.mutex.Unlock()
		worker.taskChan <- task
		return
	}
	shard.mutex.Unlock()

	worker := shard.workerCache.Get().(*workerInstance[T])
	worker.taskChan = make(chan T, 1)
	worker.shard = shard
	go worker.run()
	worker.taskChan <- task
}

func (worker *workerInstance[T]) run() {
	shard := worker.shard
	wp := shard.wp
	atomic.AddUint64(&wp.spawnedWorkers, 1)

	for task := range worker.taskChan {
		wp.handlerFunc(task)
		if !shard.setWorkerIdle(worker) {
			break
		}
	}

	atomic.AddUint64(&wp.spawnedWorkers, ^uint64(0))
	shard.workerCache.Put(worker)
}

func (shard *poolShard[T]) setWorkerIdle(worker *workerInstance[T]) bool {
	if shard.stopped.Load() {
		return false
	}

	atomic.StoreInt64(&worker.lastUsed, time.Now().UnixNano())

	if shard.idleWorker1.CompareAndSwap(nil, worker) {
		if shard.stopped.Load() {
			if shard.idleWorker1.CompareAndSwap(worker, nil) {
				return false
			}
		}
		return true
	}

	if shard.idleWorker2.CompareAndSwap(nil, worker) {
		if shard.stopped.Load() {
			if shard.idleWorker2.CompareAndSwap(worker, nil) {
				return false
			}
		}
		return true
	}

	shard.mutex.Lock()
	if shard.stopped.Load() {
		shard.mutex.Unlock()
		return false
	}
	shard.idleWorkerList = append(shard.idleWorkerList, worker)
	shard.mutex.Unlock()
	return true
}

func (wp *WorkerPool[T]) cleanup() {
	ticker := time.NewTicker(wp.idleWorkerLifetime)
	defer ticker.Stop()

	var toBeCleaned []*workerInstance[T]
	for {
		select {
		case <-wp.done:
			return
		case <-ticker.C:
		}

		now := time.Now().UnixNano()
		lifetime := wp.idleWorkerLifetime.Nanoseconds()

		for i := 0; i < wp.numShards; i++ {
			shard := wp.shards[i]
			if shard.stopped.Load() {
				continue
			}

			if w := shard.idleWorker1.Load(); w != nil {
				if now-atomic.LoadInt64(&w.lastUsed) >= lifetime {
					if shard.idleWorker1.CompareAndSwap(w, nil) {
						close(w.taskChan)
					}
				}
			}
			if w := shard.idleWorker2.Load(); w != nil {
				if now-atomic.LoadInt64(&w.lastUsed) >= lifetime {
					if shard.idleWorker2.CompareAndSwap(w, nil) {
						close(w.taskChan)
					}
				}
			}

			shard.mutex.Lock()
			idleWorkerList := shard.idleWorkerList
			iws := len(idleWorkerList)

			var j int
			if iws > 400 {
				lo, hi := 0, iws
				for lo < hi {
					mid := lo + (hi-lo)/2
					if now-atomic.LoadInt64(&idleWorkerList[mid].lastUsed) >= lifetime {
						lo = mid + 1
					} else {
						hi = mid
					}
				}
				j = lo
			} else {
				for j = 0; j < iws; j++ {
					if now-atomic.LoadInt64(&idleWorkerList[j].lastUsed) < lifetime {
						break
					}
				}
			}

			if j == 0 {
				shard.mutex.Unlock()
				continue
			}

			toBeCleaned = append(toBeCleaned[:0], idleWorkerList[:j]...)
			numMoved := copy(idleWorkerList, idleWorkerList[j:])
			for k := numMoved; k < iws; k++ {
				idleWorkerList[k] = nil
			}
			shard.idleWorkerList = idleWorkerList[:numMoved]
			shard.mutex.Unlock()

			for k := 0; k < len(toBeCleaned); k++ {
				close(toBeCleaned[k].taskChan)
				toBeCleaned[k] = nil
			}
		}
	}
}

type spinLocker struct {
	lock uint64
}

func (s *spinLocker) Lock() {
	schedulerRuns := 1
	for !atomic.CompareAndSwapUint64(&s.lock, 0, 1) {
		for i := 0; i < schedulerRuns; i++ {
			runtime.Gosched()
		}
		if schedulerRuns < 32 {
			schedulerRuns <<= 1
		}
	}
}

func (s *spinLocker) Unlock() {
	atomic.StoreUint64(&s.lock, 0)
}

type splitMix64 struct {
	state uint64
}

func (sm64 *splitMix64) Init(seed int64) {
	sm64.state = uint64(seed)
}

func (sm64 *splitMix64) Uint64() uint64 {
	sm64.state = sm64.state + uint64(0x9E3779B97F4A7C15)
	z := sm64.state
	z = (z ^ (z >> 30)) * uint64(0xBF58476D1CE4E5B9)
	z = (z ^ (z >> 27)) * uint64(0x94D049BB133111EB)
	return z ^ (z >> 31)
}

func (sm64 *splitMix64) Int63() int64 {
	return int64(sm64.Uint64() & (1<<63 - 1))
}

var splitMix64Pool = sync.Pool{
	New: func() interface{} {
		sm64 := &splitMix64{}
		sm64.Init(time.Now().UnixNano())
		return sm64
	},
}

func randInt() (r int) {
	sm64 := splitMix64Pool.Get().(*splitMix64)
	r = int(sm64.Int63())
	splitMix64Pool.Put(sm64)
	return
}
