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

//go:build scale && (linux || darwin)

// Package benchmark, scale_test.go: a single-node scale test that spawns one
// million actors and keeps all of them processing messages under a sustained
// load for a fixed duration while capturing memory and CPU usage.
//
// The test is build-tagged behind `scale` so it never runs as part of the
// normal suite. Run it with:
//
//	go test -tags=scale -run TestMillionActorsSustainedLoad -v -timeout 30m ./benchmark/
//
// CPU accounting relies on getrusage, hence the linux/darwin build constraint.

package benchmark

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

const (
	// scaleActorCount is the actor population under test.
	scaleActorCount = 1_000_000

	// scaleLoadDuration is how long the actors are kept under sustained load
	// while memory and CPU are sampled.
	scaleLoadDuration = 30 * time.Second

	// scaleSampleInterval controls how often steady-state metrics are sampled
	// during the load window.
	scaleSampleInterval = 2 * time.Second

	// scaleMaxOutstanding bounds the number of in-flight (sent but not yet
	// processed) messages. Capping the backlog keeps the heap measurement a
	// function of the actor population rather than of an unbounded mailbox
	// queue that a faster producer would otherwise build up.
	scaleMaxOutstanding = 2_000_000
)

// Regression ceilings. These start generous; pin them with headroom above the
// numbers observed on the first run, then tighten so a footprint or scheduling
// regression fails the test.
const (
	// scaleMaxBytesPerActor caps the resident heap attributable to each idle
	// spawned actor, measured after spawn and a forced GC.
	scaleMaxBytesPerActor = 4096

	// scaleGoroutineSlack is the headroom above the baseline + producer
	// goroutines for the upper bound on live goroutines. GoAkt multiplexes both
	// message dispatch and supervision onto shared, system-wide goroutine pools,
	// so the live goroutine count is bounded and independent of the actor
	// population. The bound below has no term proportional to the actor count, so
	// it fails loudly if a per-actor goroutine is ever reintroduced.
	scaleGoroutineSlack = 256
)

// scaleProcessed counts messages drained by the worker population during the
// load window. A single atomic is the established pattern in this package (see
// replicaReceiveCount); the add is a few nanoseconds and is dwarfed by the
// dispatch path it measures.
var scaleProcessed atomic.Uint64

// scaleSent counts messages handed to the actors by the producers. Together
// with scaleProcessed it yields the live backlog used to throttle producers.
var scaleSent atomic.Uint64

// workerActor is a minimal stateful actor: it mutates per-actor state and bumps
// the global processed counter on every message. The state touch keeps the
// receive path from being optimised away and gives each of the one million
// instances a realistic, non-empty footprint.
type workerActor struct {
	count uint64
}

func (w *workerActor) PreStart(*actor.Context) error { return nil }

func (w *workerActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		w.count++
		scaleProcessed.Add(1)
	default:
		ctx.Unhandled()
	}
}

func (w *workerActor) PostStop(*actor.Context) error { return nil }

// scaleSample is one observation of the system during the load window.
type scaleSample struct {
	elapsed    time.Duration
	heapInuse  uint64
	goroutines int
	backlog    uint64
}

func TestMillionActorsSustainedLoad(t *testing.T) {
	ctx := context.Background()

	system, err := actor.NewActorSystem("scale",
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))
	t.Cleanup(func() { _ = system.Stop(ctx) })

	baselineGoroutines := runtime.NumGoroutine()

	// Spawn the actor population in parallel to keep wall time reasonable.
	pids := spawnWorkers(t, ctx, system, scaleActorCount)

	// A single sender PID is enough: Tell is safe to call concurrently.
	sender, err := system.Spawn(ctx, "scale-sender", new(noopActor), actor.WithLongLived())
	require.NoError(t, err)

	// Footprint: resident heap per actor after a forced GC, with the
	// population spawned but idle.
	runtime.GC()
	var afterSpawn runtime.MemStats
	runtime.ReadMemStats(&afterSpawn)
	bytesPerActor := float64(afterSpawn.HeapAlloc) / float64(scaleActorCount)

	t.Logf("spawned %d actors: HeapAlloc=%s HeapInuse=%s bytes/actor=%.0f (~%.2f KB) goroutines=%d",
		scaleActorCount,
		humanReadableBytes(afterSpawn.HeapAlloc),
		humanReadableBytes(afterSpawn.HeapInuse),
		bytesPerActor, bytesPerActor/1024,
		runtime.NumGoroutine())

	// Drive a sustained load across every actor for a fixed duration.
	scaleProcessed.Store(0)
	scaleSent.Store(0)

	var baseMem runtime.MemStats
	runtime.ReadMemStats(&baseMem)
	baseCPU := processCPUTime()
	start := time.Now()

	var stop atomic.Bool
	stopTimer := time.AfterFunc(scaleLoadDuration, func() { stop.Store(true) })
	defer stopTimer.Stop()

	samples := make([]scaleSample, 0, int(scaleLoadDuration/scaleSampleInterval)+1)
	stopSampler := make(chan struct{})
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		ticker := time.NewTicker(scaleSampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				samples = append(samples, scaleSample{
					elapsed:    time.Since(start),
					heapInuse:  m.HeapInuse,
					goroutines: runtime.NumGoroutine(),
					backlog:    scaleSent.Load() - scaleProcessed.Load(),
				})
			case <-stopSampler:
				return
			}
		}
	}()

	driveLoad(ctx, sender, pids, &stop)

	close(stopSampler)
	<-samplerDone
	elapsed := time.Since(start)
	cpuUsed := processCPUTime() - baseCPU

	var endMem runtime.MemStats
	runtime.ReadMemStats(&endMem)

	processed := scaleProcessed.Load()
	throughput := float64(processed) / elapsed.Seconds()
	avgCores := cpuUsed.Seconds() / elapsed.Seconds()

	t.Logf("load: duration=%s processed=%d throughput=%.0f msg/s", elapsed.Round(time.Millisecond), processed, throughput)
	t.Logf("cpu: total=%s avgCores=%.2f gcCPUFraction=%.4f", cpuUsed.Round(time.Millisecond), avgCores, endMem.GCCPUFraction)
	t.Logf("mem: HeapInuse=%s NumGC=%d pauseTotal=%s",
		humanReadableBytes(endMem.HeapInuse),
		endMem.NumGC-baseMem.NumGC,
		time.Duration(endMem.PauseTotalNs-baseMem.PauseTotalNs).Round(time.Microsecond))
	t.Logf("scheduling: goroutines=%d (%.2f per actor)",
		runtime.NumGoroutine(), float64(runtime.NumGoroutine())/float64(scaleActorCount))
	for _, s := range samples {
		t.Logf("  sample t=%-6s HeapInuse=%-10s goroutines=%-6d backlog=%d",
			s.elapsed.Round(time.Second), humanReadableBytes(s.heapInuse), s.goroutines, s.backlog)
	}

	// Assertions.
	require.LessOrEqual(t, bytesPerActor, float64(scaleMaxBytesPerActor),
		"per-actor footprint regressed")
	require.Positive(t, processed, "no messages were processed under load")

	// Supervision and dispatch are both cooperative and shared, so the live
	// goroutine count must stay bounded and independent of the actor population.
	// Allowing only baseline + producers + slack (no term proportional to the
	// actor count) makes this fail loudly if a per-actor goroutine is ever
	// reintroduced.
	maxGoroutines := baselineGoroutines + producerCount() + scaleGoroutineSlack
	require.LessOrEqual(t, runtime.NumGoroutine(), maxGoroutines,
		"goroutine count grew with the actor population; a per-actor goroutine was introduced")
}

// driveLoad keeps every actor fed with messages until stop is set. Producers
// are sharded across the population so every actor receives traffic, and each
// producer throttles itself whenever the live backlog exceeds the cap so the
// heap reflects the resident population rather than a runaway mailbox queue.
func driveLoad(ctx context.Context, sender *actor.PID, pids []*actor.PID, stop *atomic.Bool) {
	producers := producerCount()
	chunk := (len(pids) + producers - 1) / producers

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		begin := p * chunk
		if begin >= len(pids) {
			break
		}
		end := min(begin+chunk, len(pids))

		wg.Add(1)
		go func(begin, end int) {
			defer wg.Done()
			// One reused message per producer keeps the hot path allocation-free.
			msg := new(testpb.TestSend)
			i := begin
			for !stop.Load() {
				if scaleSent.Load()-scaleProcessed.Load() > scaleMaxOutstanding {
					runtime.Gosched()
					continue
				}
				if err := sender.Tell(ctx, pids[i], msg); err != nil {
					// Under steady-state load Tell does not fail; if it does the
					// run is invalid, so stop producing rather than skew metrics.
					stop.Store(true)
					return
				}
				scaleSent.Add(1)
				i++
				if i >= end {
					i = begin
				}
			}
		}(begin, end)
	}
	wg.Wait()
}

// spawnWorkers spawns n long-lived worker actors in parallel and fails the test
// on the first spawn error.
func spawnWorkers(tb testing.TB, ctx context.Context, system actor.ActorSystem, n int) []*actor.PID {
	tb.Helper()

	pids := make([]*actor.PID, n)
	workers := runtime.GOMAXPROCS(0)
	chunk := (n + workers - 1) / workers

	var wg sync.WaitGroup
	var failed atomic.Bool
	errCh := make(chan error, workers)

	for w := 0; w < workers; w++ {
		begin := w * chunk
		if begin >= n {
			break
		}
		end := min(begin+chunk, n)

		wg.Add(1)
		go func(begin, end int) {
			defer wg.Done()
			for i := begin; i < end; i++ {
				if failed.Load() {
					return
				}
				pid, err := system.Spawn(ctx, "scale-worker-"+strconv.Itoa(i), &workerActor{}, actor.WithLongLived())
				if err != nil {
					if failed.CompareAndSwap(false, true) {
						errCh <- fmt.Errorf("spawn %d: %w", i, err)
					}
					return
				}
				pids[i] = pid
			}
		}(begin, end)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(tb, err)
	}
	return pids
}

// producerCount is the number of load-generating goroutines. It is biased below
// GOMAXPROCS so the dispatcher worker pool retains CPU to drain mailboxes.
func producerCount() int {
	return max(runtime.GOMAXPROCS(0)/2, 2)
}

// processCPUTime returns the total CPU time (user + system) consumed by this
// process so far. Timeval.Nano keeps it portable across the linux/darwin field
// width differences.
func processCPUTime() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
