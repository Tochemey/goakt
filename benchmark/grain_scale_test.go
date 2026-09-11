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

// Package benchmark, grain_scale_test.go: the grain counterpart of
// scale_test.go. It activates one million grains and keeps all of them
// processing messages under a sustained load for a fixed duration while
// capturing memory and CPU usage, so the two populations are measured the
// same way.
//
// The test is build-tagged behind `scale` so it never runs as part of the
// normal suite. Run it with:
//
//	go test -tags=scale -run TestMillionGrainsSustainedLoad -v -timeout 30m ./benchmark/
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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

const (
	// scaleGrainCount is the grain population under test, the same size as the
	// actor population so the two figures read side by side.
	scaleGrainCount = 1_000_000

	// scaleMaxBytesPerGrain caps the resident heap attributable to each idle
	// activated grain, measured after activation and a forced GC. Generous on
	// purpose, like scaleMaxBytesPerActor; tighten it once the number settles.
	scaleMaxBytesPerGrain = 2048
)

// scaleGrainProcessed counts messages drained by the grain population during
// the load window; scaleGrainSent counts messages handed to the grains. They
// are separate from the actor test's counters so the two tests never share
// state.
var (
	scaleGrainProcessed atomic.Uint64
	scaleGrainSent      atomic.Uint64
)

// workerGrain is the grain counterpart of workerActor: it mutates per-grain
// state and bumps the global processed counter on every message, then
// acknowledges so TellGrain returns. The state touch keeps the receive path
// from being optimised away and gives each of the one million instances a
// realistic, non-empty footprint.
type workerGrain struct {
	count uint64
}

func (w *workerGrain) OnActivate(context.Context, *actor.GrainProps) error   { return nil }
func (w *workerGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

func (w *workerGrain) OnReceive(ctx *actor.GrainContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		w.count++
		scaleGrainProcessed.Add(1)
		ctx.NoErr()
	default:
		ctx.Unhandled()
	}
}

// TestMillionGrainsSustainedLoad activates one million long-lived grains,
// reports the resident heap per grain once they are idle, then drives a
// sustained load across all of them while sampling memory, goroutines and CPU.
func TestMillionGrainsSustainedLoad(t *testing.T) {
	ctx := context.Background()

	system, err := actor.NewActorSystem("grain-scale", actor.WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))
	t.Cleanup(func() { _ = system.Stop(ctx) })

	baselineGoroutines := runtime.NumGoroutine()

	// Activate the grain population in parallel to keep wall time reasonable.
	identities := activateWorkers(t, ctx, system, scaleGrainCount)

	// Footprint: resident heap per grain after a forced GC, with the
	// population activated but idle.
	runtime.GC()
	var afterActivation runtime.MemStats
	runtime.ReadMemStats(&afterActivation)
	bytesPerGrain := float64(afterActivation.HeapAlloc) / float64(scaleGrainCount)

	t.Logf("activated %d grains: HeapAlloc=%s HeapInuse=%s bytes/grain=%.0f (~%.2f KB) goroutines=%d",
		scaleGrainCount,
		humanReadableBytes(afterActivation.HeapAlloc),
		humanReadableBytes(afterActivation.HeapInuse),
		bytesPerGrain, bytesPerGrain/1024,
		runtime.NumGoroutine())

	// Drive a sustained load across every grain for a fixed duration.
	scaleGrainProcessed.Store(0)
	scaleGrainSent.Store(0)

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
					backlog:    scaleGrainSent.Load() - scaleGrainProcessed.Load(),
				})
			case <-stopSampler:
				return
			}
		}
	}()

	driveGrainLoad(ctx, system, identities, &stop)

	close(stopSampler)
	<-samplerDone
	elapsed := time.Since(start)
	cpuUsed := processCPUTime() - baseCPU

	var endMem runtime.MemStats
	runtime.ReadMemStats(&endMem)

	processed := scaleGrainProcessed.Load()
	throughput := float64(processed) / elapsed.Seconds()
	avgCores := cpuUsed.Seconds() / elapsed.Seconds()

	t.Logf("load: duration=%s processed=%d throughput=%.0f msg/s", elapsed.Round(time.Millisecond), processed, throughput)
	t.Logf("cpu: total=%s avgCores=%.2f gcCPUFraction=%.4f", cpuUsed.Round(time.Millisecond), avgCores, endMem.GCCPUFraction)
	t.Logf("mem: HeapInuse=%s NumGC=%d pauseTotal=%s",
		humanReadableBytes(endMem.HeapInuse),
		endMem.NumGC-baseMem.NumGC,
		time.Duration(endMem.PauseTotalNs-baseMem.PauseTotalNs).Round(time.Microsecond))
	t.Logf("scheduling: goroutines=%d (%.2f per grain)",
		runtime.NumGoroutine(), float64(runtime.NumGoroutine())/float64(scaleGrainCount))
	for _, s := range samples {
		t.Logf("  sample t=%-6s HeapInuse=%-10s goroutines=%-6d backlog=%d",
			s.elapsed.Round(time.Second), humanReadableBytes(s.heapInuse), s.goroutines, s.backlog)
	}

	// Assertions.
	require.LessOrEqual(t, bytesPerGrain, float64(scaleMaxBytesPerGrain),
		"per-grain footprint regressed")
	require.Positive(t, processed, "no messages were processed under load")

	// Dispatch, timers and passivation are shared, so the live goroutine count
	// must stay bounded and independent of the grain population, exactly as
	// for actors.
	maxGoroutines := baselineGoroutines + producerCount() + scaleGoroutineSlack
	require.LessOrEqual(t, runtime.NumGoroutine(), maxGoroutines,
		"goroutine count grew with the grain population; a per-grain goroutine was introduced")
}

// driveGrainLoad keeps every grain fed with messages until stop is set.
// Producers are sharded across the population so every grain receives
// traffic. TellGrain returns once the grain has processed the message, so each
// producer has at most one message in flight and no backlog cap is needed.
func driveGrainLoad(ctx context.Context, system actor.ActorSystem, identities []*actor.GrainIdentity, stop *atomic.Bool) {
	producers := producerCount()
	chunk := (len(identities) + producers - 1) / producers

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		begin := p * chunk
		if begin >= len(identities) {
			break
		}
		end := min(begin+chunk, len(identities))

		wg.Add(1)
		go func(begin, end int) {
			defer wg.Done()
			// One reused message per producer keeps the hot path allocation-free.
			msg := new(testpb.TestSend)
			i := begin
			for !stop.Load() {
				if err := system.TellGrain(ctx, identities[i], msg); err != nil {
					// Under steady-state load TellGrain does not fail; if it does
					// the run is invalid, so stop producing rather than skew metrics.
					stop.Store(true)
					return
				}
				scaleGrainSent.Add(1)
				i++
				if i >= end {
					i = begin
				}
			}
		}(begin, end)
	}
	wg.Wait()
}

// activateWorkers activates n long-lived worker grains in parallel and fails
// the test on the first activation error.
func activateWorkers(tb testing.TB, ctx context.Context, system actor.ActorSystem, n int) []*actor.GrainIdentity {
	tb.Helper()

	identities := make([]*actor.GrainIdentity, n)
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
				identity, err := actor.GrainOf[*workerGrain](ctx, system, "scale-grain-"+strconv.Itoa(i), actor.WithLongLivedGrain())
				if err != nil {
					if failed.CompareAndSwap(false, true) {
						errCh <- fmt.Errorf("activate %d: %w", i, err)
					}
					return
				}
				identities[i] = identity
			}
		}(begin, end)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(tb, err)
	}
	return identities
}
