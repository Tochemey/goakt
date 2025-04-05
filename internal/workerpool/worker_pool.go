/*
 * MIT License
 *
 * Copyright (c) 2019-2023 Moritz Fain
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

package workerpool

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/tochemey/goakt/v3/internal/ticker"
)

const maxShards = 128

type WorkerPool struct {
	passivateAfter time.Duration
	numShards      int
	shards         []*poolShard
	mutex          spinLocker
	started        bool
	stopped        bool
	_              [56]byte
	spawnedWorkers uint64
}

type Worker struct {
	workChan  chan func()
	shard     *poolShard
	lastUsed  time.Time
	isDeleted bool
	_         [16]byte
}

func (worker *Worker) doWork() {
	shard := worker.shard
	wp := shard.wp
	atomic.AddUint64(&wp.spawnedWorkers, +1)

	for work := range worker.workChan {
		work()
		if !shard.setWorkerIdle(worker) {
			break
		}
	}

	atomic.AddUint64(&wp.spawnedWorkers, ^uint64(0))
	shard.workers.Put(worker)
}

type poolShard struct {
	wp          *WorkerPool
	workers     sync.Pool
	idleWorkers []*Worker
	idleWorker1 *Worker
	idleWorker2 *Worker
	locker      spinLocker
	stopped     bool
}

// New creates a new worker pool with the given options.
func New(opts ...Option) *WorkerPool {
	wp := &WorkerPool{
		passivateAfter: time.Second,
		numShards:      1,
	}

	for _, opt := range opts {
		opt.Apply(wp)
	}

	return wp
}

func (wp *WorkerPool) GetSpawnedWorkers() int {
	return int(atomic.LoadUint64(&wp.spawnedWorkers))
}

func (wp *WorkerPool) Start() {
	wp.mutex.Lock()
	if !wp.started {
		for range wp.numShards {
			shard := &poolShard{
				wp: wp,
				workers: sync.Pool{
					New: func() any {
						return &Worker{
							workChan: make(chan func()),
						}
					},
				},

				idleWorkers: make([]*Worker, 0, 2048),
			}
			wp.shards = append(wp.shards, shard)
		}

		wp.started = true
	}
	wp.mutex.Unlock()
	go wp.cleanup()
}

func (wp *WorkerPool) Stop() {
	wp.mutex.Lock()
	if !wp.started {
		wp.mutex.Unlock()
		return
	}

	if !wp.stopped {
		for i := range wp.numShards {
			shard := wp.shards[i]
			shard.locker.Lock()
			shard.stopped = true
			for j := range shard.idleWorkers {
				if !shard.idleWorkers[j].isDeleted {
					shard.idleWorkers[j].isDeleted = true
					close(shard.idleWorkers[j].workChan)
				}
			}
			shard.locker.Unlock()
		}
	}
	wp.stopped = true
	wp.mutex.Unlock()
}

func (wp *WorkerPool) SubmitWork(task func()) {
	wp.mutex.Lock()
	if !wp.started {
		wp.mutex.Unlock()
		return
	}

	shard := wp.shards[randInt()%wp.numShards]
	shard.acquireWorker(task)
	wp.mutex.Unlock()
}

func (shard *poolShard) acquireWorker(task func()) (worker *Worker) {
	worker = shard.idleWorker1
	if worker != nil && atomic.CompareAndSwapPointer((*unsafe.Pointer)(unsafe.Pointer(&shard.idleWorker1)), unsafe.Pointer(worker), nil) {
		worker.workChan <- task
		return worker
	}

	worker = shard.idleWorker2
	if worker != nil && atomic.CompareAndSwapPointer((*unsafe.Pointer)(unsafe.Pointer(&shard.idleWorker2)), unsafe.Pointer(worker), nil) {
		worker.workChan <- task
		return worker
	}

	shard.locker.Lock()
	length := len(shard.idleWorkers)
	if length > 0 {
		worker = shard.idleWorkers[length-1]
		shard.idleWorkers[length-1] = nil
		shard.idleWorkers = shard.idleWorkers[0 : length-1]
		shard.locker.Unlock()
		worker.workChan <- task
		return worker
	}
	shard.locker.Unlock()

	worker = shard.workers.Get().(*Worker)
	worker.shard = shard
	go worker.doWork()

	worker.workChan <- task
	return worker
}

func (shard *poolShard) setWorkerIdle(worker *Worker) bool {
	worker.lastUsed = time.Now()

	if shard.idleWorker2 == nil && atomic.CompareAndSwapPointer((*unsafe.Pointer)(unsafe.Pointer(&shard.idleWorker2)), nil, unsafe.Pointer(worker)) {
		return true
	}
	if shard.idleWorker1 == nil && atomic.CompareAndSwapPointer((*unsafe.Pointer)(unsafe.Pointer(&shard.idleWorker1)), nil, unsafe.Pointer(worker)) {
		return true
	}

	worker.shard.locker.Lock()
	if !worker.shard.stopped {
		worker.shard.idleWorkers = append(worker.shard.idleWorkers, worker)
	}
	worker.shard.locker.Unlock()
	return !worker.shard.stopped
}

func (wp *WorkerPool) cleanup() {
	var workers []*Worker
	ticker := ticker.New(wp.passivateAfter)
	defer ticker.Stop()

	ticker.Start()
	for range ticker.Ticks {
		if wp.stopped {
			return
		}

		now := time.Now()
		for i := range wp.numShards {
			shard := wp.shards[i]

			shard.locker.Lock()
			idleWorkers := shard.idleWorkers
			length := len(idleWorkers)
			j := 0
			s := 0

			if length > 400 {
				s = (length - 1) / 2
				for s > 0 && now.Sub(idleWorkers[s].lastUsed) < wp.passivateAfter {
					s = s / 2
				}

				if s == 0 {
					shard.locker.Unlock()
					continue
				}
			}

			for j = s; j < length; j++ {
				if now.Sub(idleWorkers[s].lastUsed) < wp.passivateAfter {
					break
				}
			}

			if j == 0 {
				shard.locker.Unlock()
				continue
			}

			workers = append(workers[:0], idleWorkers[0:j]...)

			numMoved := copy(idleWorkers, idleWorkers[j:])
			for j = numMoved; j < length; j++ {
				idleWorkers[j] = nil
			}
			shard.idleWorkers = idleWorkers[:numMoved]
			shard.locker.Unlock()

			for j = range workers {
				if !workers[j].shard.stopped {
					close(workers[j].workChan)
				}
				workers[j] = nil
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
		for range schedulerRuns {
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

// SplitMix64 style random pseudo number generator
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

var splitMix64Pool sync.Pool = sync.Pool{
	New: func() any {
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
