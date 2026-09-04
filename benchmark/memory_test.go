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

package benchmark

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

const (
	// footprintActorCount is the idle population the per-actor heap is measured
	// over. It is large so the per-actor bytes dominate any bookkeeping that
	// grows sub-linearly with the population (dispatcher rings, registry maps).
	footprintActorCount = 100_000

	// footprintMaxHeapBytesPerActor is the regression ceiling on the live heap
	// attributable to one idle actor spawned with the default options. It is
	// pinned about twenty percent above the 1,268 B observed on arm64 when it
	// was set, enough to absorb map growth steps and size-class rounding but
	// not an extra per-actor allocation; tighten it when the footprint shrinks.
	footprintMaxHeapBytesPerActor = 1520

	// footprintSettleTimeout bounds the wait for every spawned actor to process
	// its PostStart, the one message a spawn leaves in flight.
	footprintSettleTimeout = 30 * time.Second

	// footprintExtrapolation is the population the report extrapolates to. A
	// million idle actors on one node is the deployment shape the footprint
	// constrains.
	footprintExtrapolation = 1_000_000
)

type noopActor struct{}

func (x *noopActor) PreStart(*actor.Context) error { return nil }
func (x *noopActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
	default:
		ctx.Unhandled()
	}
}
func (x *noopActor) PostStop(*actor.Context) error { return nil }

// idleFootprint is the heap and goroutine growth attributable to one idle
// population, read after the population settled and the heap was collected.
type idleFootprint struct {
	// heapAlloc is the growth in live heap bytes (runtime.MemStats.HeapAlloc).
	heapAlloc uint64
	// heapInuse is the growth in heap spans in use (runtime.MemStats.HeapInuse),
	// which adds the size-class slack the allocator pays around live objects.
	heapInuse uint64
	// goroutines is the growth in live goroutines.
	goroutines int
}

// BenchmarkActorMemoryFootprint measures the resident density of idle actors:
// how much live heap one spawned, started, empty actor occupies once its
// PostStart has drained and the heap has been collected. The number is the
// growth between a collected baseline (actor system started, no user actors)
// and the collected heap holding the population, so the actor system's fixed
// cost is excluded and what remains is the term a million-actor deployment
// scales with. This is not a throughput number: millions of light actors on
// one machine is a memory-shape constraint, and cores do not change it.
//
// Two populations are measured. The default spawn registers every actor with
// the passivation manager; the long-lived spawn does not. Their difference is
// the passivation bookkeeping paid per actor.
func BenchmarkActorMemoryFootprint(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping memory benchmark in short mode")
	}

	b.Run("default", func(b *testing.B) {
		benchmarkActorMemoryFootprint(b)
	})

	b.Run("long-lived", func(b *testing.B) {
		benchmarkActorMemoryFootprint(b, actor.WithLongLived())
	})
}

// benchmarkActorMemoryFootprint runs one footprint measurement per benchmark
// iteration, each on a fresh actor system, reports the last one and fails the
// run when the live heap per actor exceeds footprintMaxHeapBytesPerActor.
func benchmarkActorMemoryFootprint(b *testing.B, opts ...actor.SpawnOption) {
	b.Helper()

	var footprint idleFootprint
	for i := 0; i < b.N; i++ {
		footprint = measureIdleFootprint(b, footprintActorCount, opts...)
	}

	heapPerActor := float64(footprint.heapAlloc) / footprintActorCount
	inusePerActor := float64(footprint.heapInuse) / footprintActorCount
	pidStructBytes := reflect.TypeOf((*actor.PID)(nil)).Elem().Size()

	b.ReportMetric(heapPerActor, "bytes/actor")
	b.ReportMetric(inusePerActor, "inuse-bytes/actor")

	b.Logf("\nreport:\n"+
		"  idle actors    %s, each an empty actor spawned into a started system\n"+
		"  PID struct     %d B, the fixed part of the per-actor heap below\n"+
		"  per actor      %.0f B live heap, %.0f B in heap spans (live plus size-class slack)\n"+
		"  population     %s live heap, %s in heap spans\n"+
		"  extrapolated   %s live heap, %s in heap spans at %s actors\n"+
		"  goroutines     %+d for the population (dispatch and supervision are pooled, so this stays flat)\n"+
		"  reading tips   the value is a collected-heap delta against the empty system, so it moves only when newPID or the spawn path retains more per actor",
		humanCount(footprintActorCount),
		pidStructBytes,
		heapPerActor, inusePerActor,
		humanReadableBytes(footprint.heapAlloc), humanReadableBytes(footprint.heapInuse),
		humanReadableBytes(uint64(heapPerActor*footprintExtrapolation)),
		humanReadableBytes(uint64(inusePerActor*footprintExtrapolation)),
		humanCount(footprintExtrapolation),
		footprint.goroutines)

	require.LessOrEqual(b, heapPerActor, float64(footprintMaxHeapBytesPerActor), "idle actor heap footprint regressed")
}

// measureIdleFootprint starts a fresh actor system, takes a collected baseline,
// spawns count idle actors with opts, waits for their PostStart to drain and
// returns the collected growth against the baseline. The slice holding the
// spawned PIDs is allocated before the baseline so the benchmark's own
// bookkeeping is not charged to the population.
func measureIdleFootprint(b *testing.B, count int, opts ...actor.SpawnOption) idleFootprint {
	b.Helper()

	ctx := context.Background()
	system, err := actor.NewActorSystem("mem-bench", actor.WithLogger(log.DiscardLogger), actor.WithActorInitMaxRetries(1))
	require.NoError(b, err)
	require.NoError(b, system.Start(ctx))
	defer func() { _ = system.Stop(ctx) }()

	pids := make([]*actor.PID, count)
	before := collectedMemStats()
	baselineGoroutines := runtime.NumGoroutine()

	for i := range count {
		pid, err := system.Spawn(ctx, benchActorName(i), new(noopActor), opts...)
		require.NoError(b, err)
		pids[i] = pid
	}

	settlePostStart(b, pids)
	after := collectedMemStats()

	return idleFootprint{
		heapAlloc:  after.HeapAlloc - before.HeapAlloc,
		heapInuse:  after.HeapInuse - before.HeapInuse,
		goroutines: runtime.NumGoroutine() - baselineGoroutines,
	}
}

// settlePostStart blocks until every actor in pids has processed its
// PostStart, the one message a spawn leaves in flight, so the heap is measured
// with the dispatcher quiescent rather than mid-drain.
func settlePostStart(b *testing.B, pids []*actor.PID) {
	b.Helper()

	deadline := time.Now().Add(footprintSettleTimeout)
	for i, pid := range pids {
		for pid.ProcessedCount() == 0 {
			if time.Now().After(deadline) {
				b.Fatalf("actor %d did not process PostStart within %s", i, footprintSettleTimeout)
			}

			time.Sleep(time.Millisecond)
		}
	}
}

// collectedMemStats forces two full collections and reads the heap
// statistics. Two, not one: sync.Pool contents survive the first collection
// in the pool's victim cache and are only released by the second, so a single
// collection would charge pooled scratch objects the spawns went through to
// the population.
func collectedMemStats() runtime.MemStats {
	runtime.GC()
	runtime.GC()

	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats
}

// benchActorName returns the name of the i-th actor of the idle population.
func benchActorName(i int) string {
	return "bench-noop-" + strconv.Itoa(i)
}

// humanReadableBytes renders b in the largest binary unit that keeps it above
// one, for the benchmark reports.
func humanReadableBytes(b uint64) string {
	const (
		kilobyte = 1024
		megabyte = 1024 * kilobyte
		gigabyte = 1024 * megabyte
	)

	switch {
	case b >= gigabyte:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(gigabyte))
	case b >= megabyte:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(megabyte))
	case b >= kilobyte:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(kilobyte))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
