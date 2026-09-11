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
	"reflect"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
)

const (
	// footprintGrainCount is the idle population the per-grain heap is measured
	// over. It matches footprintActorCount so the two reports read side by side
	// and the per-grain bytes dominate any bookkeeping that grows sub-linearly
	// with the population (registry maps, passivation heap).
	footprintGrainCount = 100_000

	// footprintMaxHeapBytesPerGrain is the regression ceiling on the live heap
	// attributable to one idle grain activated with the default options. It is
	// pinned about twenty percent above the 728 B observed on arm64 when it
	// was set, enough to absorb map growth steps and size-class rounding but
	// not an extra per-grain allocation; tighten it when the footprint shrinks.
	footprintMaxHeapBytesPerGrain = 875
)

// BenchmarkGrainMemoryFootprint measures the resident density of idle grains:
// how much live heap one activated, empty grain occupies once the heap has
// been collected. The number is the growth between a collected baseline
// (actor system started, no grains) and the collected heap holding the
// population, so the actor system's fixed cost is excluded and what remains
// is the term a million-grain deployment scales with. It is the grain
// counterpart of BenchmarkActorMemoryFootprint and is meant to be read
// against it: a grain has no supervision tree, no address and no PostStart
// message, so the only things it should pay for are its process, its
// identity, its mailboxes and its registry entries.
//
// Activation is synchronous: GrainOf returns once OnActivate has completed
// and the process is registered, and nothing is left in flight, so the heap
// is read right after the last activation without a settle step.
//
// Two populations are measured. The default activation registers every grain
// with the passivation manager; the long-lived activation does not. Their
// difference is the passivation bookkeeping paid per grain.
func BenchmarkGrainMemoryFootprint(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping memory benchmark in short mode")
	}

	b.Run("default", func(b *testing.B) {
		benchmarkGrainMemoryFootprint(b)
	})

	b.Run("long-lived", func(b *testing.B) {
		benchmarkGrainMemoryFootprint(b, actor.WithLongLivedGrain())
	})
}

// benchmarkGrainMemoryFootprint runs one footprint measurement per benchmark
// iteration, each on a fresh actor system, reports the last one and fails the
// run when the live heap per grain exceeds footprintMaxHeapBytesPerGrain.
func benchmarkGrainMemoryFootprint(b *testing.B, opts ...actor.GrainOption) {
	b.Helper()

	var footprint idleFootprint
	for i := 0; i < b.N; i++ {
		footprint = measureIdleGrainFootprint(b, footprintGrainCount, opts...)
	}

	heapPerGrain := float64(footprint.heapAlloc) / footprintGrainCount
	inusePerGrain := float64(footprint.heapInuse) / footprintGrainCount

	b.ReportMetric(heapPerGrain, "bytes/grain")
	b.ReportMetric(inusePerGrain, "inuse-bytes/grain")

	b.Logf("\nreport:\n"+
		"  idle grains    %s, each an empty grain activated in a started system\n"+
		"  process struct %d B, the fixed part of the per-grain heap below\n"+
		"  per grain      %.0f B live heap, %.0f B in heap spans (live plus size-class slack)\n"+
		"  population     %s live heap, %s in heap spans\n"+
		"  extrapolated   %s live heap, %s in heap spans at %s grains\n"+
		"  goroutines     %+d for the population (dispatch, timers and passivation are pooled, so this stays flat)\n"+
		"  reading tips   the value is a collected-heap delta against the empty system, so it moves only when newGrainPID or the activation path retains more per grain",
		humanCount(footprintGrainCount),
		grainProcessStructBytes(),
		heapPerGrain, inusePerGrain,
		humanReadableBytes(footprint.heapAlloc), humanReadableBytes(footprint.heapInuse),
		humanReadableBytes(uint64(heapPerGrain*footprintExtrapolation)),
		humanReadableBytes(uint64(inusePerGrain*footprintExtrapolation)),
		humanCount(footprintExtrapolation),
		footprint.goroutines)

	require.LessOrEqual(b, heapPerGrain, float64(footprintMaxHeapBytesPerGrain), "idle grain heap footprint regressed")
}

// measureIdleGrainFootprint starts a fresh actor system, takes a collected
// baseline, activates count idle grains with opts and returns the collected
// growth against the baseline. The slice holding the identities is allocated
// before the baseline so the benchmark's own bookkeeping is not charged to the
// population; the identities themselves are retained by the grain processes
// and are part of what a grain costs, so they are measured.
func measureIdleGrainFootprint(b *testing.B, count int, opts ...actor.GrainOption) idleFootprint {
	b.Helper()

	ctx := context.Background()
	system, err := actor.NewActorSystem("grain-mem-bench", actor.WithLogger(log.DiscardLogger))
	require.NoError(b, err)
	require.NoError(b, system.Start(ctx))
	defer func() { _ = system.Stop(ctx) }()

	identities := make([]*actor.GrainIdentity, count)
	before := collectedMemStats()
	baselineGoroutines := runtime.NumGoroutine()

	for i := range count {
		identity, err := actor.GrainOf[*benchGrain](ctx, system, benchGrainName(i), opts...)
		require.NoError(b, err)
		identities[i] = identity
	}

	after := collectedMemStats()

	return idleFootprint{
		heapAlloc:  after.HeapAlloc - before.HeapAlloc,
		heapInuse:  after.HeapInuse - before.HeapInuse,
		goroutines: runtime.NumGoroutine() - baselineGoroutines,
	}
}

// grainProcessStructBytes returns the size of the unexported grain process
// struct, the per-grain object the activation path allocates. The type is
// reached through the process pointer GrainContext carries, so the benchmark
// needs no export from the actor package; it reports zero if that field is
// ever renamed, which only loses the report line.
func grainProcessStructBytes() uintptr {
	field, ok := reflect.TypeFor[actor.GrainContext]().FieldByName("pid")
	if !ok || field.Type.Kind() != reflect.Pointer {
		return 0
	}

	return field.Type.Elem().Size()
}

// benchGrainName returns the name of the i-th grain of the idle population.
func benchGrainName(i int) string {
	return "bench-grain-" + strconv.Itoa(i)
}
