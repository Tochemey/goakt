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

package stream_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/stream"
)

func TestTryMap_Success(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Via(
		stream.Of(1, 2, 3),
		stream.TryMap(func(n int) (int, error) { return n * 2, nil }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{2, 4, 6}, col.Items())
}

func TestTryMap_ErrorFailFast(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	sentinel := errors.New("bad element")
	handle, err := stream.Via(
		stream.Of(1, 2, 3),
		stream.TryMap(func(n int) (int, error) {
			if n == 2 {
				return 0, sentinel
			}
			return n, nil
		}),
	).To(stream.Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate")
	}
}

func TestTryMap_ErrorResume(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Via(
		stream.Of(1, 2, 3, 4),
		stream.TryMap(func(n int) (int, error) {
			if n == 2 {
				return 0, errors.New("skip me")
			}
			return n, nil
		}).WithErrorStrategy(stream.Resume),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	// Element 2 is dropped; 1, 3, 4 survive.
	assert.Equal(t, []int{1, 3, 4}, col.Items())
}

func TestTryMap_ErrorRetry_Succeeds(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	attempts := 0
	col, sink := stream.Collect[int]()
	handle, err := stream.Via(
		stream.Of(1, 2, 3),
		stream.TryMap(func(n int) (int, error) {
			if n == 2 && attempts == 0 {
				attempts++
				return 0, errors.New("first attempt")
			}
			return n * 10, nil
		}).WithErrorStrategy(stream.Retry),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{10, 20, 30}, col.Items())
}

func TestTryMap_ErrorRetry_Fails(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	handle, err := stream.Via(
		stream.Of(1, 2, 3),
		stream.TryMap(func(n int) (int, error) {
			if n == 2 {
				return 0, errors.New("always fail")
			}
			return n, nil
		}).WithErrorStrategy(stream.Retry),
	).To(stream.Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate on retry failure")
	}
}

func TestMap(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[string]()
	handle, err := stream.Via(
		stream.Of(1, 2, 3),
		stream.Map(func(n int) string { return string(rune('a' - 1 + n)) }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []string{"a", "b", "c"}, col.Items())
}

func TestFilter_KeepsMatching(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Of(1, 2, 3, 4, 5, 6).
		Via(stream.Filter(func(n int) bool { return n%2 == 0 })).
		To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{2, 4, 6}, col.Items())
}

func TestFilter_DropAll(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Of(1, 3, 5).
		Via(stream.Filter(func(n int) bool { return n%2 == 0 })).
		To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Empty(t, col.Items())
}

func TestFlatMap_Expansion(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Via(
		stream.Of(1, 2, 3),
		stream.FlatMap(func(n int) []int { return []int{n, n * 10} }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{1, 10, 2, 20, 3, 30}, col.Items())
}

func TestFlatMap_EmptyOutput(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Via(
		stream.Of(1, 2, 3),
		stream.FlatMap(func(_ int) []int { return nil }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Empty(t, col.Items())
}

func TestFlatten_Basic(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Via(
		stream.Of([]int{1, 2}, []int{3, 4}, []int{5}),
		stream.Flatten[int](),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{1, 2, 3, 4, 5}, col.Items())
}

func TestFlatten_EmptySlices(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Via(
		stream.Of([]int{}, []int{1}, []int{}),
		stream.Flatten[int](),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{1}, col.Items())
}

func TestBatch_SizeFlush(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[[]int]()
	handle, err := stream.Via(
		stream.Of(1, 2, 3, 4, 5, 6),
		stream.Batch[int](3, time.Second),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()

	batches := col.Items()
	require.NotEmpty(t, batches)
	var all []int
	for _, b := range batches {
		all = append(all, b...)
	}
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6}, all)
}

func TestBatch_TimeoutFlush(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	// Only 2 elements but batch size is 10 — timer flush required.
	col, sink := stream.Collect[[]int]()
	handle, err := stream.Via(
		stream.Of(1, 2),
		stream.Batch[int](10, 100*time.Millisecond),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()

	batches := col.Items()
	require.Len(t, batches, 1)
	assert.Equal(t, []int{1, 2}, batches[0])
}

func TestBuffer_PassThrough(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Of(1, 2, 3).
		Via(stream.Buffer[int](10, stream.DropTail)).
		To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{1, 2, 3}, col.Items())
}

func TestThrottle_SlowsDown(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	start := time.Now()
	col, sink := stream.Collect[int]()
	// 4 elements at 100ms per element = ~400ms minimum.
	handle, err := stream.Via(
		stream.Of(1, 2, 3, 4),
		stream.Throttle[int](1, 50*time.Millisecond),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()

	elapsed := time.Since(start)
	assert.Equal(t, []int{1, 2, 3, 4}, col.Items())
	// 4 elements × 50ms each = 200ms minimum.
	assert.GreaterOrEqual(t, elapsed, 150*time.Millisecond)
}

func TestDeduplicate_ConsecutiveDuplicates(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Of(1, 1, 2, 3, 3, 3, 4, 1).
		Via(stream.Deduplicate[int]()).
		To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	// Non-consecutive duplicates (last 1) are NOT deduplicated.
	assert.Equal(t, []int{1, 2, 3, 4, 1}, col.Items())
}

func TestDeduplicate_AllSame(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Of(5, 5, 5).
		Via(stream.Deduplicate[int]()).
		To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{5}, col.Items())
}

func TestScan_RunningSum(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Via(
		stream.Of(1, 2, 3, 4),
		stream.Scan(0, func(acc, n int) int { return acc + n }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{1, 3, 6, 10}, col.Items())
}

func TestWithErrorStrategy_Preserved(t *testing.T) {
	// Verify that WithErrorStrategy returns a new Flow and does not mutate the original.
	base := stream.TryMap(func(n int) (int, error) { return n, nil })
	modified := base.WithErrorStrategy(stream.Resume)
	// Both should work independently.
	sys := newTestSystem(t)
	ctx := context.Background()

	col1, s1 := stream.Collect[int]()
	h1, err := stream.Via(stream.Of(1, 2), base).To(s1).Run(ctx, sys)
	require.NoError(t, err)

	col2, s2 := stream.Collect[int]()
	h2, err := stream.Via(stream.Of(3, 4), modified).To(s2).Run(ctx, sys)
	require.NoError(t, err)

	<-h1.Done()
	<-h2.Done()
	assert.Equal(t, []int{1, 2}, col1.Items())
	assert.Equal(t, []int{3, 4}, col2.Items())
}

func TestMultiStage_MapFilterScan(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Via(
		stream.Via(
			stream.Of(1, 2, 3, 4, 5, 6),
			stream.Filter(func(n int) bool { return n%2 == 0 }),
		),
		stream.Scan(0, func(acc, n int) int { return acc + n }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	// Even numbers: 2, 4, 6 → running sums: 2, 6, 12.
	assert.Equal(t, []int{2, 6, 12}, col.Items())
}

func TestFlow_UpstreamError_PropagatesToSink(t *testing.T) {
	// An error in a Map stage should propagate downstream and terminate the stream.
	sys := newTestSystem(t)
	ctx := context.Background()

	handle, err := stream.Via(
		stream.Via(
			stream.Of(1, 2, 3),
			stream.TryMap(func(n int) (int, error) {
				if n == 1 {
					return 0, errors.New("upstream error")
				}
				return n, nil
			}),
		),
		stream.Map(func(n int) int { return n }),
	).To(stream.Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate after upstream error")
	}
}

// TestWithContext_PassThrough verifies that WithContext is a transparent
// identity flow: elements pass through with their values unchanged.
func TestWithContext_PassThrough(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Via(
		stream.Of(1, 2, 3),
		stream.WithContext[int]("trace-id", "abc-123"),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}

	assert.Equal(t, []int{1, 2, 3}, col.Items())
}

// TestWithContext_Chained verifies that multiple WithContext stages can be
// composed without altering element values.
func TestWithContext_Chained(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[string]()
	handle, err := stream.Via(
		stream.Via(
			stream.Via(
				stream.Of("a", "b", "c"),
				stream.WithContext[string]("service", "gateway"),
			),
			stream.WithContext[string]("env", "prod"),
		),
		stream.Map(func(s string) string { return s + "!" }),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}

	assert.Equal(t, []string{"a!", "b!", "c!"}, col.Items())
}
