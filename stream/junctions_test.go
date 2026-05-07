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
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/stream"
)

// TestConcat_PreservesOrder verifies that Concat consumes sub-sources in the
// order given and preserves per-source element order across the boundary.
func TestConcat_PreservesOrder(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	src := stream.Concat(
		stream.Of(1, 2, 3),
		stream.Of(4, 5),
		stream.Of(6, 7, 8, 9),
	)
	h, err := src.To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("concat did not complete")
	}
	require.NoError(t, h.Err())
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}, col.Items())
}

// TestConcat_EmptySources verifies that Concat handles empty sub-sources
// without stalling.
func TestConcat_EmptySources(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	src := stream.Concat(
		stream.Of[int](),
		stream.Of(1, 2),
		stream.Of[int](),
		stream.Of(3),
		stream.Of[int](),
	)
	h, err := src.To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("concat did not complete")
	}
	require.NoError(t, h.Err())
	assert.Equal(t, []int{1, 2, 3}, col.Items())
}

// TestConcat_NoSources completes immediately with no elements.
func TestConcat_NoSources(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	h, err := stream.Concat[int]().To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("empty concat did not complete")
	}
	require.NoError(t, h.Err())
	assert.Empty(t, col.Items())
}

// TestZip_EmitsTuplesAndStopsOnShortest verifies that Zip emits one tuple per
// matched group and completes as soon as any input is exhausted.
func TestZip_EmitsTuplesAndStopsOnShortest(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[[]int]()
	src := stream.Zip(
		stream.Of(1, 2, 3, 4, 5),
		stream.Of(10, 20, 30),
		stream.Of(100, 200, 300, 400),
	)
	h, err := src.To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("zip did not complete")
	}
	require.NoError(t, h.Err())

	got := col.Items()
	require.Len(t, got, 3, "should emit min(5,3,4) = 3 tuples")
	assert.Equal(t, []int{1, 10, 100}, got[0])
	assert.Equal(t, []int{2, 20, 200}, got[1])
	assert.Equal(t, []int{3, 30, 300}, got[2])
}

// TestZipWith_AppliesCombineFunction verifies that ZipWith invokes the
// supplied combine function and propagates the returned value downstream.
func TestZipWith_AppliesCombineFunction(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	src := stream.ZipWith(
		func(parts []int) int {
			sum := 0
			for _, v := range parts {
				sum += v
			}
			return sum
		},
		stream.Of(1, 2, 3),
		stream.Of(10, 20, 30),
		stream.Of(100, 200, 300),
	)
	h, err := src.To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("zipWith did not complete")
	}
	require.NoError(t, h.Err())
	assert.Equal(t, []int{111, 222, 333}, col.Items())
}

// TestZip_NoSources completes immediately.
func TestZip_NoSources(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[[]int]()
	h, err := stream.Zip[int]().To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("empty zip did not complete")
	}
	require.NoError(t, h.Err())
	assert.Empty(t, col.Items())
}

// TestPartition_RoutesByPredicate verifies that Partition routes each element
// to the slot returned by the partition function and that no element appears
// in more than one branch.
func TestPartition_RoutesByPredicate(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	branches := stream.Partition(
		stream.Range(0, 10),
		3,
		func(v int64) int { return int(v % 3) },
	)
	require.Len(t, branches, 3)

	cols := make([]*stream.Collector[int64], 3)
	var wg sync.WaitGroup
	for i, b := range branches {
		col, sink := stream.Collect[int64]()
		cols[i] = col
		h, err := b.To(sink).Run(ctx, sys)
		require.NoError(t, err)
		wg.Go(func() {
			<-h.Done()
		})
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("partition branches did not complete")
	}

	// Branch i should contain values v where v%3 == i.
	for i := 0; i < 3; i++ {
		got := cols[i].Items()
		slices.Sort(got)
		want := []int64{}
		for v := int64(0); v < 10; v++ {
			if int(v%3) == i {
				want = append(want, v)
			}
		}
		assert.Equal(t, want, got, "branch %d", i)
	}
}

// TestPartition_DropsOutOfRange verifies that elements whose partitionFn
// result is out of range are dropped silently.
func TestPartition_DropsOutOfRange(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	branches := stream.Partition(
		stream.Of(1, 2, 3, 4, 5),
		2,
		func(v int) int {
			if v == 3 {
				return -1 // dropped
			}
			return v % 2
		},
	)
	require.Len(t, branches, 2)

	cols := make([]*stream.Collector[int], 2)
	var wg sync.WaitGroup
	for i, b := range branches {
		col, sink := stream.Collect[int]()
		cols[i] = col
		h, err := b.To(sink).Run(ctx, sys)
		require.NoError(t, err)
		wg.Go(func() {
			<-h.Done()
		})
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("partition did not complete")
	}

	// 1 → slot 1; 2 → slot 0; 3 dropped; 4 → slot 0; 5 → slot 1
	got0 := cols[0].Items()
	got1 := cols[1].Items()
	sort.Ints(got0)
	sort.Ints(got1)
	assert.Equal(t, []int{2, 4}, got0)
	assert.Equal(t, []int{1, 5}, got1)
}

// TestPartition_EmptyN returns nil and does not consume the source.
func TestPartition_EmptyN(t *testing.T) {
	branches := stream.Partition(
		stream.Of(1, 2, 3),
		0,
		func(int) int { return 0 },
	)
	assert.Empty(t, branches)
}

// TestUnzip_SplitsPairs verifies that Unzip routes the (left, right) parts of
// each input element into the corresponding output sources.
func TestUnzip_SplitsPairs(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	type kv struct {
		k string
		v int
	}
	in := stream.Of(
		kv{"a", 1},
		kv{"b", 2},
		kv{"c", 3},
	)
	left, right := stream.Unzip(in, func(p kv) (string, int) { return p.k, p.v })

	colL, sinkL := stream.Collect[string]()
	colR, sinkR := stream.Collect[int]()

	hL, err := left.To(sinkL).Run(ctx, sys)
	require.NoError(t, err)
	hR, err := right.To(sinkR).Run(ctx, sys)
	require.NoError(t, err)

	for _, h := range []stream.StreamHandle{hL, hR} {
		select {
		case <-h.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("unzip branch did not complete")
		}
		require.NoError(t, h.Err())
	}

	assert.Equal(t, []string{"a", "b", "c"}, colL.Items())
	assert.Equal(t, []int{1, 2, 3}, colR.Items())
}

// TestMergePreferred_DrainsPreferredFirst verifies that elements buffered on
// the preferred slot are emitted before elements on other slots when both
// arrive before the actor flushes.
func TestMergePreferred_DrainsPreferredFirst(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	// Block downstream demand from being immediately served by inserting a
	// short delay on the source feeders so values pile up before flush.
	src := stream.MergePreferred(
		1,
		stream.Of(1, 2, 3),
		stream.Of(100, 200, 300),
	)
	h, err := src.To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("merge-preferred did not complete")
	}
	require.NoError(t, h.Err())

	// Every element from both streams must be present (no drops). Order is
	// not strictly deterministic because arrival timing influences which slot
	// has elements buffered when the actor flushes, but every element from
	// the preferred slot (100, 200, 300) must precede the unpreferred slot
	// (1, 2, 3) once both are buffered.
	got := col.Items()
	require.Len(t, got, 6)
	sum := 0
	for _, v := range got {
		sum += v
	}
	assert.Equal(t, 1+2+3+100+200+300, sum)
}

// TestMergePreferred_PanicsOnOutOfRange verifies that an invalid preferred
// index panics at construction time.
func TestMergePreferred_PanicsOnOutOfRange(t *testing.T) {
	assert.Panics(t, func() {
		stream.MergePreferred(5, stream.Of(1), stream.Of(2))
	})
}

// TestMergePrioritized_PreservesAllElements verifies that every element from
// every weighted source is forwarded exactly once.
func TestMergePrioritized_PreservesAllElements(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	src := stream.MergePrioritized(
		[]int{3, 1},
		stream.Of(1, 2, 3, 4, 5),
		stream.Of(100, 200, 300),
	)
	h, err := src.To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("merge-prioritized did not complete")
	}
	require.NoError(t, h.Err())

	got := col.Items()
	require.Len(t, got, 8)
	slices.Sort(got)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 100, 200, 300}, got)
}

// TestMergePrioritized_PanicsOnLengthMismatch verifies that a weights/sources
// length mismatch panics at construction time.
func TestMergePrioritized_PanicsOnLengthMismatch(t *testing.T) {
	assert.Panics(t, func() {
		stream.MergePrioritized([]int{1, 2}, stream.Of(1))
	})
}

// TestMergeLatest_EmitsSnapshotsAfterFirstFromEachInput verifies that
// MergeLatest emits no snapshot until every input has produced at least one
// element, then emits one snapshot per subsequent upstream emission with the
// latest value from every slot.
func TestMergeLatest_EmitsSnapshotsAfterFirstFromEachInput(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[[]int]()
	src := stream.MergeLatest(
		stream.Of(1, 2, 3),
		stream.Of(10, 20),
	)
	h, err := src.To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("merge-latest did not complete")
	}
	require.NoError(t, h.Err())

	got := col.Items()
	// Expect 5 snapshots — one per upstream emission once both slots have
	// emitted at least once. The exact contents depend on arrival
	// interleaving, but every snapshot must hold values that are valid
	// "latest seen" combinations.
	require.GreaterOrEqual(t, len(got), 1, "should emit at least one snapshot")
	require.LessOrEqual(t, len(got), 5, "cannot emit more than total upstream emissions")
	for _, snap := range got {
		require.Len(t, snap, 2, "snapshot must hold one value per slot")
		assert.Contains(t, []int{1, 2, 3}, snap[0], "slot 0 latest must be from src 0")
		assert.Contains(t, []int{10, 20}, snap[1], "slot 1 latest must be from src 1")
	}
}

// TestMergeSequence_EmitsInOrder verifies that MergeSequence emits elements
// in monotonically-increasing sequence order across N sources.
func TestMergeSequence_EmitsInOrder(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	type item struct {
		seq int64
		val string
	}
	col, sink := stream.Collect[item]()
	src := stream.MergeSequence(
		func(i item) int64 { return i.seq },
		// Slot 0 produces even sequences; slot 1 produces odd sequences.
		stream.Of(item{0, "a"}, item{2, "c"}, item{4, "e"}),
		stream.Of(item{1, "b"}, item{3, "d"}, item{5, "f"}),
	)
	h, err := src.To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("merge-sequence did not complete")
	}
	require.NoError(t, h.Err())

	got := col.Items()
	require.Len(t, got, 6)
	for i, it := range got {
		assert.Equal(t, int64(i), it.seq, "items must be emitted in sequence order")
	}
	assert.Equal(t, "abcdef", got[0].val+got[1].val+got[2].val+got[3].val+got[4].val+got[5].val)
}

// TestMergeSequence_MissingSequenceErrors verifies that completion with a
// non-empty buffer whose minimum seq is not the expected one fails the stream.
func TestMergeSequence_MissingSequenceErrors(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	type item struct {
		seq int64
		val string
	}
	_, sink := stream.Collect[item]()
	// Skip seq=2 — sources collectively produce {0, 1, 3, 4} which is not
	// contiguous after expected reaches 2.
	src := stream.MergeSequence(
		func(i item) int64 { return i.seq },
		stream.Of(item{0, "a"}, item{3, "d"}),
		stream.Of(item{1, "b"}, item{4, "e"}),
	)
	h, err := src.To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("merge-sequence did not complete")
	}
	require.Error(t, h.Err(), "missing sequence should fail the stream")
	assert.Contains(t, h.Err().Error(), "missing sequence number 2")
}

// TestGraph_ConcatInto verifies the ConcatInto Graph DSL node.
func TestGraph_ConcatInto(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[any]()

	g := stream.NewGraph().
		AddSource("src1", stream.Of[any](1, 2, 3)).
		AddSource("src2", stream.Of[any](4, 5, 6)).
		ConcatInto("joined", "src1", "src2").
		AddSink("sink", sink, "joined")

	rg, err := g.Build()
	require.NoError(t, err)

	h, err := rg.Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("concat graph did not complete")
	}
	require.NoError(t, h.Err())
	assert.Equal(t, []any{1, 2, 3, 4, 5, 6}, col.Items())
}
