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

	"github.com/tochemey/goakt/v4/internal/pause"
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
	pause.For(300 * time.Millisecond)
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

// TestSubFlow_Abort_StopsUpstreamAndSubstreams verifies that aborting a
// stream built on MergeSubstreams stops the upstream pipeline and every
// substream pipeline the splitter materialized, right after Run as well as
// once the substreams exist.
func TestSubFlow_Abort_StopsUpstreamAndSubstreams(t *testing.T) {
	build := func(ch <-chan int) stream.RunnableGraph {
		sf := stream.GroupBy(stream.FromChannel(ch), 0, func(n int) int { return n % 4 })
		return stream.From(stream.MergeSubstreams(sf)).To(stream.Ignore[int]())
	}

	t.Run("right after Run", func(t *testing.T) {
		sys := newTestSystem(t)

		for range 30 {
			handle, err := build(make(chan int)).Run(context.Background(), sys)
			require.NoError(t, err)
			handle.Abort()
		}

		requireNoStreamActorsLeft(t, sys)
	})

	t.Run("with open substreams", func(t *testing.T) {
		sys := newTestSystem(t)

		ch := make(chan int)
		var seen atomic.Int64
		sf := stream.GroupBy(stream.FromChannel(ch), 0, func(n int) int { return n % 4 })
		handle, err := stream.From(stream.MergeSubstreams(sf)).
			To(stream.ForEach(func(int) { seen.Add(1) })).
			Run(context.Background(), sys)
		require.NoError(t, err)

		// Four keys: four substreams are open once all four elements came through.
		for i := range 4 {
			ch <- i
		}

		require.Eventually(t, func() bool { return seen.Load() == 4 }, 5*time.Second, 5*time.Millisecond)
		handle.Abort()
		requireNoStreamActorsLeft(t, sys)
	})
}

// TestSubFlow_UpstreamFailure_FailsTheStream verifies that a failure of the
// pipeline feeding the splitter surfaces as the stream's terminal error.
func TestSubFlow_UpstreamFailure_FailsTheStream(t *testing.T) {
	sys := newTestSystem(t)

	sentinel := errors.New("upstream failed")
	upstream := stream.Via(stream.Of(1, 2, 3, 4), stream.TryMap(func(n int) (int, error) {
		if n == 3 {
			return 0, sentinel
		}
		return n, nil
	}))
	sf := stream.GroupBy(upstream, 0, func(n int) int { return n % 2 })
	handle, err := stream.From(stream.MergeSubstreams(sf)).To(stream.Ignore[int]()).Run(context.Background(), sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate")
	}

	require.ErrorIs(t, handle.Err(), sentinel)
	requireNoStreamActorsLeft(t, sys)
}

// TestSubFlow_UpstreamMaterializationFailure_FailsTheStream verifies that an
// upstream the splitter cannot materialize fails the stream instead of
// leaving it waiting.
func TestSubFlow_UpstreamMaterializationFailure_FailsTheStream(t *testing.T) {
	sys := newTestSystem(t)

	// A source without a source stage is rejected by the materializer.
	sf := stream.GroupBy(stream.Source[int]{}, 0, func(n int) int { return n })
	handle, err := stream.From(stream.MergeSubstreams(sf)).To(stream.Ignore[int]()).Run(context.Background(), sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate")
	}

	require.ErrorIs(t, handle.Err(), stream.ErrInvalidGraph)
}

// TestSubFlow_SlowDownstream_HoldsUpstreamBack verifies that a downstream
// that stops asking for elements holds the source back under every overflow
// strategy, and that every element is accounted for, in order within its
// key, once the downstream asks again. It covers keys of equal share and keys
// of which one carries (almost) every element, which cannot fill the merged
// buffer on its own.
//
// A key that carries most of the elements also reaches its cap in bursts
// while the downstream keeps up, so DropTail drops some of its elements and
// FailSource can fail before the downstream stops: those cases check that
// what was not delivered was counted as dropped, and FailSource runs with
// keys of equal share only.
func TestSubFlow_SlowDownstream_HoldsUpstreamBack(t *testing.T) {
	fourKeys := func(n int) int { return n % 4 }
	oneKey := func(int) int { return 0 }
	skewedKeys := func(n int) int {
		if n%100 == 0 {
			return 1
		}

		return 0
	}

	backpressure := func(sf stream.SubFlow[int, int]) stream.SubFlow[int, int] { return sf }
	dropTail := func(sf stream.SubFlow[int, int]) stream.SubFlow[int, int] {
		return sf.WithSubstreamBuffer(256, stream.DropTail)
	}
	failSource := func(sf stream.SubFlow[int, int]) stream.SubFlow[int, int] {
		return sf.WithSubstreamBuffer(256, stream.FailSource)
	}

	cases := []struct {
		name      string
		configure func(stream.SubFlow[int, int]) stream.SubFlow[int, int]
		keyOf     func(int) int
		// lossless tells that no element may be dropped.
		lossless bool
	}{
		{name: "default/four keys", configure: backpressure, keyOf: fourKeys, lossless: true},
		{name: "default/one key", configure: backpressure, keyOf: oneKey, lossless: true},
		{name: "default/skewed keys", configure: backpressure, keyOf: skewedKeys, lossless: true},
		{name: "DropTail/four keys", configure: dropTail, keyOf: fourKeys, lossless: true},
		{name: "DropTail/one key", configure: dropTail, keyOf: oneKey},
		{name: "DropTail/skewed keys", configure: dropTail, keyOf: skewedKeys},
		{name: "FailSource/four keys", configure: failSource, keyOf: fourKeys, lossless: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sys := newTestSystem(t)

			var pulled atomic.Int64
			sf := tc.configure(stream.GroupBy(countingSource(slowDownstreamTotal, &pulled), 0, tc.keyOf))
			sink := newGatedSink[int]()
			handle, err := stream.From(stream.MergeSubstreams(sf)).To(sink.sink()).Run(context.Background(), sys)
			require.NoError(t, err)

			sink.waitFirst(t)
			requireHeldBack(t, &pulled)

			sink.release()
			waitStream(t, handle, 30*time.Second)
			require.NoError(t, handle.Err())

			got := sink.received()
			requireOrderedPerKey(t, got, tc.keyOf)

			dropped := int(handle.Metrics().DroppedElements)
			if tc.lossless {
				require.Zero(t, dropped)
			}

			require.Equal(t, slowDownstreamTotal, len(got)+dropped, "an element was neither delivered nor counted as dropped")
		})
	}
}

// TestSubFlow_SlowDownstream_HoldsSplitUpstreamBack verifies that short
// SplitAfter substreams do not let a stopped downstream pull the source or
// open substreams without bound.
func TestSubFlow_SlowDownstream_HoldsSplitUpstreamBack(t *testing.T) {
	sys := newTestSystem(t)

	var pulled atomic.Int64
	sf := stream.SplitAfter(countingSource(slowDownstreamTotal, &pulled), func(n int) bool { return n%10 == 9 })
	sink := newGatedSink[int]()
	handle, err := stream.From(stream.MergeSubstreams(sf)).To(sink.sink()).Run(context.Background(), sys)
	require.NoError(t, err)

	sink.waitFirst(t)
	requireHeldBack(t, &pulled)
	require.Less(t, streamActorCount(t, sys), 100, "substreams were opened without bound")

	sink.release()
	waitStream(t, handle, 30*time.Second)
	require.NoError(t, handle.Err())

	// Each substream is ten consecutive elements, kept in order.
	got := sink.received()
	require.Len(t, got, slowDownstreamTotal)
	requireOrderedPerKey(t, got, func(n int) int { return n / 10 })
}

// TestSubFlow_SlowSubstream_DropTail verifies that under DropTail a substream
// slower than its feed does not hold the source back: its new elements are
// dropped and counted, and every other element is delivered.
func TestSubFlow_SlowSubstream_DropTail(t *testing.T) {
	sys := newTestSystem(t)

	const total = 4_000
	gate, openGate := newGate(t)
	var pulled atomic.Int64
	sf := stream.GroupBy(countingSource(total, &pulled), 0, func(n int) int { return n % 4 }).
		WithSubstreamBuffer(16, stream.DropTail)
	collector, sink := stream.Collect[int]()
	handle, err := stream.From(stream.MergeSubstreams(stream.SubFlowVia(sf, holdKey(gate, 0)))).To(sink).Run(context.Background(), sys)
	require.NoError(t, err)

	// The whole source is pulled while the slow substream is still held.
	require.Eventually(t, func() bool { return pulled.Load() == total }, 10*time.Second, 10*time.Millisecond)

	openGate()
	waitStream(t, handle, 10*time.Second)
	require.NoError(t, handle.Err())

	slowDelivered := 0
	for _, n := range collector.Items() {
		if n%4 == 0 {
			slowDelivered++
		}
	}

	dropped := int(handle.Metrics().DroppedElements)
	require.Less(t, slowDelivered, total/4, "the slow substream lost nothing")
	require.Equal(t, total, len(collector.Items())+dropped, "an element was neither delivered nor counted as dropped")
}

// TestSubFlow_SlowSubstream_FailSource verifies that under FailSource a
// substream slower than its feed fails the stream with ErrSubstreamOverflow.
func TestSubFlow_SlowSubstream_FailSource(t *testing.T) {
	sys := newTestSystem(t)

	gate, _ := newGate(t)

	var pulled atomic.Int64
	sf := stream.GroupBy(countingSource(4_000, &pulled), 0, func(n int) int { return n % 4 }).
		WithSubstreamBuffer(16, stream.FailSource)
	handle, err := stream.From(stream.MergeSubstreams(stream.SubFlowVia(sf, holdKey(gate, 0)))).To(stream.Ignore[int]()).Run(context.Background(), sys)
	require.NoError(t, err)

	waitStream(t, handle, 10*time.Second)
	require.ErrorIs(t, handle.Err(), stream.ErrSubstreamOverflow)
}

// TestSubFlow_SlowSubstream_Backpressure verifies that under the default
// BackpressureSource a substream slower than its feed holds the source back
// and loses nothing.
func TestSubFlow_SlowSubstream_Backpressure(t *testing.T) {
	sys := newTestSystem(t)

	gate, openGate := newGate(t)
	var pulled atomic.Int64
	sf := stream.GroupBy(countingSource(slowDownstreamTotal, &pulled), 0, func(n int) int { return n % 4 })
	collector, sink := stream.Collect[int]()
	handle, err := stream.From(stream.MergeSubstreams(stream.SubFlowVia(sf, holdKey(gate, 0)))).To(sink).Run(context.Background(), sys)
	require.NoError(t, err)

	requireHeldBack(t, &pulled)

	openGate()
	waitStream(t, handle, 30*time.Second)
	require.NoError(t, handle.Err())
	require.Zero(t, handle.Metrics().DroppedElements)

	got := collector.Items()
	require.Len(t, got, slowDownstreamTotal)
	requireOrderedPerKey(t, got, func(n int) int { return n % 4 })
}

// TestSubFlow_Backpressure_UpstreamEndsWhileElementsWait verifies that a
// stream whose upstream completes while elements still wait in the splitter
// delivers them all before it completes.
func TestSubFlow_Backpressure_UpstreamEndsWhileElementsWait(t *testing.T) {
	sys := newTestSystem(t)

	values := make([]int, 200)
	for i := range values {
		values[i] = i
	}

	sf := stream.GroupBy(stream.Of(values...), 0, func(n int) int { return n % 2 }).
		WithSubstreamBuffer(1, stream.BackpressureSource)
	collector, sink := stream.Collect[int]()
	handle, err := stream.From(stream.MergeSubstreams(sf)).To(sink).Run(context.Background(), sys)
	require.NoError(t, err)

	waitStream(t, handle, 10*time.Second)
	require.NoError(t, handle.Err())

	got := collector.Items()
	require.Len(t, got, len(values))
	requireOrderedPerKey(t, got, func(n int) int { return n % 2 })
}

// TestSubFlow_Backpressure_SplitBoundaries verifies that SplitWhen and
// SplitAfter keep their substream boundaries when elements wait in the
// splitter, and run the predicate at most once per element.
func TestSubFlow_Backpressure_SplitBoundaries(t *testing.T) {
	// The input and its substreams are those of the SplitWhen and SplitAfter
	// tests above; each substream counts its elements with a Scan.
	input := []int{1, 2, 0, 3, 4, 0, 5}
	splits := []struct {
		name  string
		split func(stream.Source[int], func(int) bool) stream.SubFlow[int, int]
		want  []int
		// calls is the number of predicate calls: SplitWhen does not consult
		// the predicate for the first element, which always starts
		// substream 0.
		calls int
	}{
		{name: "SplitWhen", split: stream.SplitWhen[int], want: []int{1, 1, 1, 2, 2, 2, 3}, calls: len(input) - 1},
		{name: "SplitAfter", split: stream.SplitAfter[int], want: []int{1, 1, 1, 2, 2, 3, 3}, calls: len(input)},
	}

	for _, split := range splits {
		t.Run(split.name, func(t *testing.T) {
			sys := newTestSystem(t)

			var calls atomic.Int64
			sf := split.split(stream.Of(input...), func(n int) bool {
				calls.Add(1)
				return n == 0
			}).WithSubstreamBuffer(1, stream.BackpressureSource)

			counted := stream.SubFlowVia(sf, stream.Scan(0, func(acc int, _ int) int { return acc + 1 }))
			collector, sink := stream.Collect[int]()
			handle, err := stream.From(stream.MergeSubstreams(counted)).To(sink).Run(context.Background(), sys)
			require.NoError(t, err)

			waitStream(t, handle, 5*time.Second)
			require.NoError(t, handle.Err())

			got := collector.Items()
			sort.Ints(got)
			require.Equal(t, split.want, got)
			require.EqualValues(t, split.calls, calls.Load())
		})
	}
}

// TestSubFlow_Backpressure_DropWhileElementsWait verifies that under
// SubstreamDrop the elements of a failed key that waited in the splitter are
// dropped and counted, while the other keys deliver all of theirs.
func TestSubFlow_Backpressure_DropWhileElementsWait(t *testing.T) {
	sys := newTestSystem(t)

	const total = 2_000
	gate, openGate := newGate(t)
	var pulled atomic.Int64
	sf := stream.GroupBy(countingSource(total, &pulled), 0, func(n int) int { return n % 4 }).
		WithSubstreamBuffer(4, stream.BackpressureSource).
		WithErrorStrategy(stream.SubstreamDrop)
	collector, sink := stream.Collect[int]()
	handle, err := stream.From(stream.MergeSubstreams(stream.SubFlowVia(sf, failKeyOnRelease(gate, 0)))).To(sink).Run(context.Background(), sys)
	require.NoError(t, err)

	// The held key stalls the source, so elements wait in the splitter.
	require.Never(t, func() bool { return pulled.Load() == total }, 200*time.Millisecond, 10*time.Millisecond)

	openGate()
	waitStream(t, handle, 10*time.Second)
	require.NoError(t, handle.Err())

	got := collector.Items()
	require.Len(t, got, total*3/4)
	for _, n := range got {
		require.NotZero(t, n%4, "an element of the failed key was delivered")
	}

	require.Positive(t, handle.Metrics().DroppedElements, "the waiting elements were not counted as dropped")
}

// TestSubFlow_Backpressure_RestartWhileElementsWait verifies that under
// SubstreamRestart the elements that waited for a failed key start a new
// substream, which reuses the failed one's input slot and carries more than
// one window: the merged buffer acknowledges its elements to the new sink.
func TestSubFlow_Backpressure_RestartWhileElementsWait(t *testing.T) {
	sys := newTestSystem(t)

	const total = 2_000
	gate, openGate := newGate(t)
	var pulled atomic.Int64
	// A single key, so every element goes to the substream that fails and,
	// once it is forgotten, to the one that replaces it.
	sf := stream.GroupBy(countingSource(total, &pulled), 0, func(int) int { return 0 }).
		WithSubstreamBuffer(4, stream.BackpressureSource).
		WithErrorStrategy(stream.SubstreamRestart)
	var failed atomic.Bool
	flow := stream.TryMap(func(n int) (int, error) {
		if n == 0 && failed.CompareAndSwap(false, true) {
			<-gate
			return 0, errInjected
		}

		return n, nil
	})

	collector, sink := stream.Collect[int]()
	handle, err := stream.From(stream.MergeSubstreams(stream.SubFlowVia(sf, flow))).To(sink).Run(context.Background(), sys)
	require.NoError(t, err)

	require.Never(t, func() bool { return pulled.Load() == total }, 200*time.Millisecond, 10*time.Millisecond)

	openGate()
	waitStream(t, handle, 10*time.Second)
	require.NoError(t, handle.Err())

	// The failed substream took its elements with it; the new one delivers
	// every later element, in order.
	got := collector.Items()
	require.Greater(t, len(got), 2*demandWindow, "the restarted substream carried no more than one window")
	for i, n := range got {
		require.Equal(t, total-len(got)+i, n)
	}
}

// TestSubFlow_Abort_WhileElementsWait verifies that aborting a stream whose
// splitter holds waiting elements stops every stream actor.
func TestSubFlow_Abort_WhileElementsWait(t *testing.T) {
	sys := newTestSystem(t)

	var pulled atomic.Int64
	sf := stream.GroupBy(countingSource(slowDownstreamTotal, &pulled), 0, func(n int) int { return n % 4 })
	sink := newGatedSink[int]()
	handle, err := stream.From(stream.MergeSubstreams(sf)).To(sink.sink()).Run(context.Background(), sys)
	require.NoError(t, err)

	sink.waitFirst(t)
	requireHeldBack(t, &pulled)

	handle.Abort()
	sink.release()
	requireNoStreamActorsLeft(t, sys)
}
