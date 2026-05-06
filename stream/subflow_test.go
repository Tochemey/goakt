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
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/stream"
)

func TestSubFlow_GroupByMergeSubstreams_PreservesAllElements(t *testing.T) {
	sys := newTestSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	src := stream.Of(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	sf := stream.GroupBy(src, 0, func(n int) int { return n % 2 })
	merged := stream.MergeSubstreams(sf)

	collector, sink := stream.Collect[int]()
	handle, err := stream.From(merged).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-ctx.Done():
		t.Fatal("stream did not complete in time")
	}
	require.NoError(t, handle.Err())

	got := collector.Items()
	sort.Ints(got)
	require.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, got)
}

func TestSubFlow_PerSubstreamMapAppliesIndependently(t *testing.T) {
	sys := newTestSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	src := stream.Of(1, 2, 3, 4)
	sf := stream.GroupBy(src, 0, func(n int) int { return n % 2 })
	// Per-substream map: double every element. Applied independently per key
	// (correctness here is just that every element is doubled regardless of key).
	transformed := stream.SubFlowVia(sf, stream.Map(func(n int) int { return n * 2 }))
	merged := stream.MergeSubstreams(transformed)

	collector, sink := stream.Collect[int]()
	handle, err := stream.From(merged).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-ctx.Done():
		t.Fatal("stream did not complete in time")
	}
	require.NoError(t, handle.Err())

	got := collector.Items()
	sort.Ints(got)
	require.Equal(t, []int{2, 4, 6, 8}, got)
}

func TestSubFlow_PerSubstreamScanCarriesItsOwnAccumulator(t *testing.T) {
	sys := newTestSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 5 elements per key — verify each substream's Scan starts from zero.
	src := stream.Of(1, 1, 1, 2, 2, 2, 1, 2, 1, 2)
	sf := stream.GroupBy(src, 0, func(n int) int { return n })
	withScan := stream.SubFlowVia(sf, stream.Scan(0, func(acc, n int) int { return acc + n }))
	merged := stream.MergeSubstreams(withScan)

	collector, sink := stream.Collect[int]()
	handle, err := stream.From(merged).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-ctx.Done():
		t.Fatal("stream did not complete in time")
	}
	require.NoError(t, handle.Err())

	got := collector.Items()
	sort.Ints(got)
	// Substream key=1 sees 5 ones → cumulative sums 1,2,3,4,5
	// Substream key=2 sees 5 twos → cumulative sums 2,4,6,8,10
	require.Equal(t, []int{1, 2, 2, 3, 4, 4, 5, 6, 8, 10}, got)
}

func TestSubFlow_SplitWhen_DelimiterStartsNewSubstream(t *testing.T) {
	sys := newTestSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Predicate: n == 0 starts a new substream. With input
	//   1, 2, 0, 3, 4, 0, 5
	// the substreams are
	//   [1, 2], [0, 3, 4], [0, 5]
	// Per-substream Scan(0, +1) emits running counts:
	//   substream 0: 1, 2
	//   substream 1: 1, 2, 3
	//   substream 2: 1, 2
	src := stream.Of(1, 2, 0, 3, 4, 0, 5)
	sf := stream.SplitWhen(src, func(n int) bool { return n == 0 })
	withCount := stream.SubFlowVia(sf, stream.Scan(0, func(acc int, _ int) int { return acc + 1 }))
	merged := stream.MergeSubstreams(withCount)

	collector, sink := stream.Collect[int]()
	handle, err := stream.From(merged).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-ctx.Done():
		t.Fatal("stream did not complete in time")
	}
	require.NoError(t, handle.Err())

	got := collector.Items()
	sort.Ints(got)
	require.Equal(t, []int{1, 1, 1, 2, 2, 2, 3}, got)
}

func TestSubFlow_SplitWhen_FirstElementMatchesPredicate(t *testing.T) {
	sys := newTestSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The very first element matches the predicate. SplitWhen must NOT
	// rotate before substream 0 has any elements — substream 0 simply
	// starts with the matching element. Input
	//   0, 1, 0, 2
	// yields substreams [0, 1], [0, 2]; cumulative counts merge to {1,2,1,2}.
	src := stream.Of(0, 1, 0, 2)
	sf := stream.SplitWhen(src, func(n int) bool { return n == 0 })
	withCount := stream.SubFlowVia(sf, stream.Scan(0, func(acc int, _ int) int { return acc + 1 }))
	merged := stream.MergeSubstreams(withCount)

	collector, sink := stream.Collect[int]()
	handle, err := stream.From(merged).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-ctx.Done():
		t.Fatal("stream did not complete in time")
	}
	require.NoError(t, handle.Err())

	got := collector.Items()
	sort.Ints(got)
	require.Equal(t, []int{1, 1, 2, 2}, got)
}

func TestSubFlow_SplitAfter_DelimiterEndsCurrentSubstream(t *testing.T) {
	sys := newTestSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Predicate: n == 0 ends the current substream. With input
	//   1, 2, 0, 3, 4, 0, 5
	// the substreams are
	//   [1, 2, 0], [3, 4, 0], [5]
	// Per-substream Scan(0, +1) emits running counts:
	//   substream 0: 1, 2, 3
	//   substream 1: 1, 2, 3
	//   substream 2: 1
	src := stream.Of(1, 2, 0, 3, 4, 0, 5)
	sf := stream.SplitAfter(src, func(n int) bool { return n == 0 })
	withCount := stream.SubFlowVia(sf, stream.Scan(0, func(acc int, _ int) int { return acc + 1 }))
	merged := stream.MergeSubstreams(withCount)

	collector, sink := stream.Collect[int]()
	handle, err := stream.From(merged).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-ctx.Done():
		t.Fatal("stream did not complete in time")
	}
	require.NoError(t, handle.Err())

	got := collector.Items()
	sort.Ints(got)
	require.Equal(t, []int{1, 1, 1, 2, 2, 3, 3}, got)
}

func TestSubFlow_SplitWhen_NoMatchProducesSingleSubstream(t *testing.T) {
	sys := newTestSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Predicate never fires; everything lands in substream 0.
	src := stream.Of(1, 2, 3, 4)
	sf := stream.SplitWhen(src, func(int) bool { return false })
	withCount := stream.SubFlowVia(sf, stream.Scan(0, func(acc int, _ int) int { return acc + 1 }))
	merged := stream.MergeSubstreams(withCount)

	collector, sink := stream.Collect[int]()
	handle, err := stream.From(merged).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-ctx.Done():
		t.Fatal("stream did not complete in time")
	}
	require.NoError(t, handle.Err())

	got := collector.Items()
	sort.Ints(got)
	require.Equal(t, []int{1, 2, 3, 4}, got)
}

var errInjected = errors.New("injected substream failure")

// failOnKey returns a TryMap flow that errors on the first element matching
// targetKey, and is a pass-through for everything else.
func failOnKey(targetKey int) stream.Flow[int, int] {
	var fired atomic.Bool
	return stream.TryMap(func(n int) (int, error) {
		if n == targetKey && fired.CompareAndSwap(false, true) {
			return 0, errInjected
		}
		return n, nil
	})
}

func TestSubFlow_ErrorStrategyFailAll_TerminatesStream(t *testing.T) {
	sys := newTestSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	src := stream.Of(1, 2, 3, 4, 5, 6)
	sf := stream.GroupBy(src, 0, func(n int) int { return n % 2 })
	withFailingFlow := stream.SubFlowVia(sf, failOnKey(2))
	merged := stream.MergeSubstreams(withFailingFlow)

	_, sink := stream.Collect[int]()
	handle, err := stream.From(merged).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-ctx.Done():
		t.Fatal("stream did not complete in time")
	}
	require.Error(t, handle.Err())
	require.True(t, errors.Is(handle.Err(), errInjected),
		"expected injected error to surface; got %v", handle.Err())
}

func TestSubFlow_ErrorStrategyDrop_BlocklistsKeyAndContinues(t *testing.T) {
	sys := newTestSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Two keys: 0 (even) and 1 (odd). Inject a failure on the first even
	// element. Under SubstreamDrop the even substream collapses, the odd
	// substream finishes normally, and any subsequent even elements are
	// silently discarded.
	src := stream.Of(2, 1, 4, 3, 6, 5)
	sf := stream.GroupBy(src, 0, func(n int) int { return n % 2 }).
		WithErrorStrategy(stream.SubstreamDrop)
	withFailingFlow := stream.SubFlowVia(sf, failOnKey(2))
	merged := stream.MergeSubstreams(withFailingFlow)

	collector, sink := stream.Collect[int]()
	handle, err := stream.From(merged).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-ctx.Done():
		t.Fatal("stream did not complete in time")
	}
	require.NoError(t, handle.Err(), "stream should complete cleanly under SubstreamDrop")

	got := collector.Items()
	sort.Ints(got)
	// Odd substream produces 1, 3, 5 verbatim. Even substream errored on the
	// very first element (key=0) so nothing from it survives, and 4 / 6 are
	// dropped because the key is blocklisted.
	require.Equal(t, []int{1, 3, 5}, got)
}

func TestSubFlow_ErrorStrategyRestart_RespawnsKey(t *testing.T) {
	sys := newTestSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Channel-driven source so the test can wait for the first substream's
	// error to propagate before sending the elements that should land on a
	// fresh, restarted substream.
	ch := make(chan int, 4)
	src := stream.FromChannel(ch)
	sf := stream.GroupBy(src, 0, func(int) int { return 0 }).
		WithErrorStrategy(stream.SubstreamRestart)
	withFailingFlow := stream.SubFlowVia(sf, failOnKey(7))
	merged := stream.MergeSubstreams(withFailingFlow)

	collector, sink := stream.Collect[int]()
	handle, err := stream.From(merged).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	// Send the doomed first element, wait long enough for the substream
	// failure to round-trip back to the splitter, then send the survivors.
	ch <- 7
	time.Sleep(300 * time.Millisecond)
	ch <- 7
	ch <- 7
	ch <- 7
	close(ch)

	select {
	case <-handle.Done():
	case <-ctx.Done():
		t.Fatal("stream did not complete in time")
	}
	require.NoError(t, handle.Err(), "stream should complete cleanly under SubstreamRestart")

	got := collector.Items()
	require.NotEmpty(t, got, "expected at least one element to flow through after restart")
	for _, v := range got {
		require.Equal(t, 7, v)
	}
}

func TestSubFlow_OverflowFailSource_TerminatesStream(t *testing.T) {
	sys := newTestSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	src := stream.Range(0, 200)
	// All elements share a single key so they all queue against the same
	// substream. perKeyBuffer=1 with FailSource forces immediate overflow.
	sf := stream.GroupBy(src, 0, func(int64) int { return 0 }).
		WithSubstreamBuffer(1, stream.FailSource)
	merged := stream.MergeSubstreams(sf)

	_, sink := stream.Collect[int64]()
	handle, err := stream.From(merged).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-ctx.Done():
		t.Fatal("stream did not complete in time")
	}
	require.Error(t, handle.Err())
	require.True(t, errors.Is(handle.Err(), stream.ErrSubstreamOverflow),
		"expected ErrSubstreamOverflow; got %v", handle.Err())
}

func TestSubFlow_TooManySubstreamsFailsStream(t *testing.T) {
	sys := newTestSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	src := stream.Of(1, 2, 3, 4)
	// maxSubstreams = 2 but the source produces 4 distinct keys.
	sf := stream.GroupBy(src, 2, func(n int) int { return n })
	merged := stream.MergeSubstreams(sf)

	_, sink := stream.Collect[int]()
	handle, err := stream.From(merged).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-ctx.Done():
		t.Fatal("stream did not complete in time")
	}
	require.Error(t, handle.Err())
	require.True(t, errors.Is(handle.Err(), stream.ErrTooManySubstreams))
}
