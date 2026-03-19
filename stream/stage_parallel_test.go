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

// Package stream internal tests for ParallelMap and OrderedParallelMap stage actors.
package stream

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
)

// TestParallelMap_BasicTransform verifies that all elements are transformed and
// collected (order may vary with n > 1 workers).
func TestParallelMap_BasicTransform(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	col, sink := Collect[int]()
	handle, err := Via(
		Of(1, 2, 3, 4, 5),
		ParallelMap(3, func(n int) int { return n * 2 }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)

	got := col.Items()
	sort.Ints(got)
	assert.Equal(t, []int{2, 4, 6, 8, 10}, got)
}

// TestParallelMap_SingleWorker verifies correct behavior when n=1 (sequential,
// preserves natural order for a single worker).
func TestParallelMap_SingleWorker(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	col, sink := Collect[int]()
	handle, err := Via(
		Of(10, 20, 30),
		ParallelMap[int, int](1, func(n int) int { return n + 1 }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)

	got := col.Items()
	sort.Ints(got)
	assert.Equal(t, []int{11, 21, 31}, got)
}

// TestParallelMap_TypeConversion verifies a cross-type transform (int → string).
func TestParallelMap_TypeConversion(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	col, sink := Collect[string]()
	handle, err := Via(
		Of(1, 2, 3),
		ParallelMap(2, func(n int) string { return fmt.Sprintf("item-%d", n) }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)

	got := col.Items()
	sort.Strings(got)
	assert.Equal(t, []string{"item-1", "item-2", "item-3"}, got)
}

// TestParallelMap_EmptySource verifies that an empty upstream completes cleanly.
func TestParallelMap_EmptySource(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	col, sink := Collect[int]()
	handle, err := Via(
		Of[int](),
		ParallelMap[int, int](3, func(n int) int { return n }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)
	assert.Empty(t, col.Items())
}

// TestParallelMap_LargeInput verifies correctness on a larger input where workers
// are fully saturated across multiple demand cycles.
func TestParallelMap_LargeInput(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	const n = 200
	input := make([]int, n)
	for i := range input {
		input[i] = i + 1
	}

	col, sink := Collect[int]()
	handle, err := Via(
		Of(input...),
		ParallelMap(4, func(v int) int { return v * v }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 10*time.Second)

	got := col.Items()
	sort.Ints(got)
	require.Len(t, got, n)
	for i, v := range got {
		// The sorted squares of 1..200 are 1, 4, 9, …, 40000.
		expected := (i + 1) * (i + 1)
		assert.Equal(t, expected, v, "index %d", i)
	}
}

// TestParallelMap_ConcurrencyActuallyParallel verifies that workers execute
// concurrently: n slow tasks complete faster than n sequential calls would.
func TestParallelMap_ConcurrencyActuallyParallel(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	const workers = 4
	const sleep = 50 * time.Millisecond

	col, sink := Collect[int]()
	start := time.Now()
	handle, err := Via(
		Of(1, 2, 3, 4),
		ParallelMap(workers, func(n int) int {
			time.Sleep(sleep)
			return n
		}),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 10*time.Second)
	elapsed := time.Since(start)

	got := col.Items()
	sort.Ints(got)
	assert.Equal(t, []int{1, 2, 3, 4}, got)
	// 4 tasks × 50ms each = 200ms if sequential.
	// With parallel execution we expect well under 200ms.
	assert.Less(t, elapsed, 2*sleep*workers/2,
		"expected parallel execution, got elapsed=%v", elapsed)
}

// TestParallelMap_WorkerPanic verifies that a panic inside fn is recovered,
// converted to a streamError, and terminates the stream cleanly.
func TestParallelMap_WorkerPanic(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	handle, err := Via(
		Of(1, 2, 3),
		ParallelMap(2, func(n int) int {
			if n == 2 {
				panic("deliberate worker panic")
			}
			return n
		}),
	).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)
}

// TestParallelMap_WorkerPanicPropagatesError verifies that a worker panic results
// in a non-nil StreamHandle.Err().
func TestParallelMap_WorkerPanicPropagatesError(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	handle, err := Via(
		Of(1, 2, 3),
		ParallelMap(2, func(n int) int {
			if n == 2 {
				panic(errors.New("panic error"))
			}
			return n
		}),
	).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)
	assert.NotNil(t, handle.Err())
}

// TestParallelMap_UpstreamCancel verifies that when the sink cancels (FailFast
// on error), the parallel stage also terminates cleanly.
func TestParallelMap_UpstreamCancel(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	handle, err := Via(
		Of(1, 2, 3, 4, 5),
		ParallelMap(2, func(n int) int { return n }),
	).To(errSink(1, FailFast)).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)
}

// TestParallelMap_StreamCancel verifies that a streamCancel message propagates
// upstream from the parallel stage, shutting down the stream cleanly.
func TestParallelMap_StreamCancel(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	upPID, err := sys.Spawn(ctx, "pm-cancel-up", &dummyStageActor{})
	require.NoError(t, err)
	downPID, err := sys.Spawn(ctx, "pm-cancel-down", &dummyStageActor{})
	require.NoError(t, err)

	pa := newParallelMapActor(2, func(n int) int { return n }, false, defaultStageConfig())
	paPID, err := sys.Spawn(ctx, "pm-cancel-actor", pa)
	require.NoError(t, err)

	require.NoError(t, actor.Tell(ctx, paPID, &stageWire{
		subID: "unit", upstream: upPID, downstream: downPID,
	}))
	time.Sleep(20 * time.Millisecond)

	// Send streamCancel (simulating a downstream cancel).
	require.NoError(t, actor.Tell(ctx, paPID, &streamCancel{subID: "unit"}))

	require.Eventually(t, func() bool {
		_, e := sys.ActorOf(ctx, paPID.Name())
		return e != nil // actor stopped
	}, 2*time.Second, 5*time.Millisecond)
}

// TestParallelMap_StreamErrorFromUpstream verifies that a *streamError received
// from upstream is forwarded downstream and shuts down the stage.
func TestParallelMap_StreamErrorFromUpstream(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	handle, err := Via(
		Via(
			Of(1, 2, 3),
			TryMap(func(n int) (int, error) {
				if n == 1 {
					return 0, errors.New("upstream error")
				}
				return n, nil
			}),
		),
		ParallelMap[int, int](2, func(n int) int { return n * 10 }),
	).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)
}

// TestParallelMap_n_LessThan1_Coerced verifies that n < 1 is coerced to 1
// and the stream completes without error.
func TestParallelMap_n_LessThan1_Coerced(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	col, sink := Collect[int]()
	handle, err := Via(
		Of(1, 2, 3),
		ParallelMap(0, func(n int) int { return n }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)

	got := col.Items()
	sort.Ints(got)
	assert.Equal(t, []int{1, 2, 3}, got)
}

// TestParallelMap_WorkerCountMatchesConcurrency verifies that at most n workers
// are active at any given time using an atomic counter.
func TestParallelMap_WorkerCountMatchesConcurrency(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	const workers = 3
	var active atomic.Int64
	var maxActive atomic.Int64

	col, sink := Collect[int]()
	handle, err := Via(
		Of(1, 2, 3, 4, 5, 6),
		ParallelMap(workers, func(n int) int {
			cur := active.Add(1)
			// Track the high-water mark.
			for {
				old := maxActive.Load()
				if cur <= old || maxActive.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			active.Add(-1)
			return n
		}),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 10*time.Second)

	got := col.Items()
	sort.Ints(got)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6}, got)
	assert.LessOrEqual(t, maxActive.Load(), int64(workers),
		"concurrent workers exceeded limit: max=%d", maxActive.Load())
}

// TestOrderedParallelMap_PreservesOrder verifies that output elements are emitted
// in the same order as the source, even with multiple workers.
func TestOrderedParallelMap_PreservesOrder(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	col, sink := Collect[int]()
	handle, err := Via(
		Of(1, 2, 3, 4, 5),
		OrderedParallelMap(3, func(n int) int { return n * 2 }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)

	// OrderedParallelMap must preserve input order.
	assert.Equal(t, []int{2, 4, 6, 8, 10}, col.Items())
}

// TestOrderedParallelMap_SingleWorker verifies order preservation with n=1.
func TestOrderedParallelMap_SingleWorker(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	col, sink := Collect[int]()
	handle, err := Via(
		Of(5, 4, 3, 2, 1),
		OrderedParallelMap(1, func(n int) int { return n * 10 }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)

	assert.Equal(t, []int{50, 40, 30, 20, 10}, col.Items())
}

// TestOrderedParallelMap_TypeConversion verifies cross-type ordered transform.
func TestOrderedParallelMap_TypeConversion(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	col, sink := Collect[string]()
	handle, err := Via(
		Of(3, 1, 2),
		OrderedParallelMap(3, func(n int) string { return fmt.Sprintf("%03d", n) }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)

	// Order must match source order: 3, 1, 2.
	assert.Equal(t, []string{"003", "001", "002"}, col.Items())
}

// TestOrderedParallelMap_EmptySource verifies clean completion on empty input.
func TestOrderedParallelMap_EmptySource(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	col, sink := Collect[int]()
	handle, err := Via(
		Of[int](),
		OrderedParallelMap(3, func(n int) int { return n }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)
	assert.Empty(t, col.Items())
}

// TestOrderedParallelMap_LargeInput verifies order preservation on a larger
// input with workers that introduce random-ish delay variation.
func TestOrderedParallelMap_LargeInput(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	const n = 100
	input := make([]int, n)
	for i := range input {
		input[i] = i + 1
	}

	col, sink := Collect[int]()
	handle, err := Via(
		Of(input...),
		OrderedParallelMap(5, func(v int) int {
			// Odd elements sleep briefly so results arrive out of natural order,
			// exercising the resequencer heap.
			if v%2 != 0 {
				time.Sleep(2 * time.Millisecond)
			}
			return v * 2
		}),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 15*time.Second)

	got := col.Items()
	require.Len(t, got, n)
	for i, v := range got {
		assert.Equal(t, (i+1)*2, v, "position %d: want %d got %d", i, (i+1)*2, v)
	}
}

// TestOrderedParallelMap_WorkerPanic verifies that a panic is recovered and the
// stream terminates cleanly with a non-nil error.
func TestOrderedParallelMap_WorkerPanic(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	handle, err := Via(
		Of(1, 2, 3),
		OrderedParallelMap(2, func(n int) int {
			if n == 2 {
				panic("ordered worker panic")
			}
			return n
		}),
	).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)
	assert.NotNil(t, handle.Err())
}

// TestOrderedParallelMap_UpstreamCancel verifies that a downstream cancel
// (FailFast sink error) propagates back through the ordered parallel stage.
func TestOrderedParallelMap_UpstreamCancel(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	handle, err := Via(
		Of(1, 2, 3, 4, 5),
		OrderedParallelMap(2, func(n int) int { return n }),
	).To(errSink(1, FailFast)).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)
}

// TestOrderedParallelMap_StreamErrorFromUpstream verifies that an upstream
// *streamError is forwarded downstream and terminates the stage.
func TestOrderedParallelMap_StreamErrorFromUpstream(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	handle, err := Via(
		Via(
			Of(1, 2, 3),
			TryMap(func(n int) (int, error) {
				if n == 2 {
					return 0, errors.New("upstream error")
				}
				return n, nil
			}),
		),
		OrderedParallelMap(2, func(n int) int { return n }),
	).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)
}

// TestOrderedParallelMap_n_LessThan1_Coerced verifies that n < 1 is coerced to 1.
func TestOrderedParallelMap_n_LessThan1_Coerced(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	col, sink := Collect[int]()
	handle, err := Via(
		Of(3, 1, 2),
		OrderedParallelMap(-5, func(n int) int { return n }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)

	// With n=1 (coerced), output order is preserved.
	assert.Equal(t, []int{3, 1, 2}, col.Items())
}

// TestOrderedParallelMap_StreamCancel_Unit verifies the *streamCancel handler
// propagates cancel upstream and completes the stage.
func TestOrderedParallelMap_StreamCancel_Unit(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	upPID, err := sys.Spawn(ctx, "opm-cancel-up", &dummyStageActor{})
	require.NoError(t, err)
	downPID, err := sys.Spawn(ctx, "opm-cancel-down", &dummyStageActor{})
	require.NoError(t, err)

	oa := newParallelMapActor(2, func(n int) int { return n }, true, defaultStageConfig())
	oaPID, err := sys.Spawn(ctx, "opm-cancel-actor", oa)
	require.NoError(t, err)

	require.NoError(t, actor.Tell(ctx, oaPID, &stageWire{
		subID: "unit", upstream: upPID, downstream: downPID,
	}))
	time.Sleep(20 * time.Millisecond)

	require.NoError(t, actor.Tell(ctx, oaPID, &streamCancel{subID: "unit"}))

	require.Eventually(t, func() bool {
		_, e := sys.ActorOf(ctx, oaPID.Name())
		return e != nil
	}, 2*time.Second, 5*time.Millisecond)
}

// TestParallelMap_ComposedWithFilter verifies ParallelMap combined with a
// downstream Filter stage produces the correct results.
func TestParallelMap_ComposedWithFilter(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	col, sink := Collect[int]()
	handle, err := Via(
		Via(
			Of(1, 2, 3, 4, 5, 6),
			ParallelMap(3, func(n int) int { return n * 2 }),
		),
		Filter(func(n int) bool { return n > 6 }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)

	got := col.Items()
	sort.Ints(got)
	assert.Equal(t, []int{8, 10, 12}, got)
}

// TestOrderedParallelMap_ComposedWithScan verifies OrderedParallelMap combined
// with a downstream Scan stage; order guarantee makes Scan output deterministic.
func TestOrderedParallelMap_ComposedWithScan(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	col, sink := Collect[int]()
	handle, err := Via(
		Via(
			Of(1, 2, 3, 4),
			OrderedParallelMap(2, func(n int) int { return n * 2 }),
		),
		Scan(0, func(acc, n int) int { return acc + n }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	waitDone(t, handle, 5*time.Second)

	// Ordered output: 2, 4, 6, 8 → running sums: 2, 6, 12, 20.
	assert.Equal(t, []int{2, 6, 12, 20}, col.Items())
}

// waitDone blocks until handle.Done() is closed or the test deadline fires.
func waitDone(t *testing.T, h StreamHandle, timeout time.Duration) {
	t.Helper()
	select {
	case <-h.Done():
	case <-time.After(timeout):
		t.Fatal("stream did not complete within timeout")
	}
}
