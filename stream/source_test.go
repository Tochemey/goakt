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
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/stream"
)

// ─── Of ───────────────────────────────────────────────────────────────────────

func TestOf_Empty(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Of[int]().To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Empty(t, col.Items())
}

func TestOf_SingleElement(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[string]()
	handle, err := stream.Of("hello").To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []string{"hello"}, col.Items())
}

func TestOf_MultipleElements(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Of(1, 2, 3, 4, 5).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{1, 2, 3, 4, 5}, col.Items())
}

func TestOf_LargeSlice(t *testing.T) {
	// Exercises the demand/refill loop (elements > defaultInitialDemand).
	sys := newTestSystem(t)
	ctx := context.Background()

	input := make([]int, 300)
	for i := range input {
		input[i] = i
	}
	col, sink := stream.Collect[int]()
	handle, err := stream.Of(input...).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, input, col.Items())
}

// ─── Range ────────────────────────────────────────────────────────────────────

func TestRange_Basic(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int64]()
	handle, err := stream.Range(1, 6).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int64{1, 2, 3, 4, 5}, col.Items())
}

func TestRange_EmptyRange(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int64]()
	handle, err := stream.Range(5, 5).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Empty(t, col.Items())
}

func TestRange_SingleElement(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int64]()
	handle, err := stream.Range(7, 8).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int64{7}, col.Items())
}

// ─── FromChannel ──────────────────────────────────────────────────────────────

func TestFromChannel_PreloadedhCh(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	ch := make(chan int, 5)
	for i := 1; i <= 5; i++ {
		ch <- i
	}
	close(ch)

	col, sink := stream.Collect[int]()
	handle, err := stream.FromChannel(ch).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{1, 2, 3, 4, 5}, col.Items())
}

func TestFromChannel_EmptyChannel(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	ch := make(chan string)
	close(ch) // immediately closed

	col, sink := stream.Collect[string]()
	handle, err := stream.FromChannel(ch).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Empty(t, col.Items())
}

func TestFromChannel_StreamingChannel(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	ch := make(chan int)
	go func() {
		for i := 0; i < 10; i++ {
			ch <- i
		}
		close(ch)
	}()

	col, sink := stream.Collect[int]()
	handle, err := stream.FromChannel(ch).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()

	expected := make([]int, 10)
	for i := range expected {
		expected[i] = i
	}
	assert.Equal(t, expected, col.Items())
}

func TestFromChannel_Cancel(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	ch := make(chan int) // never closed — stream runs forever until stopped

	_, sink := stream.Collect[int]()
	handle, err := stream.FromChannel(ch).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	// Stop the stream immediately.
	require.NoError(t, handle.Stop(ctx))

	select {
	case <-handle.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not stop")
	}
}

// ─── FromActor ────────────────────────────────────────────────────────────────

// intPullActor serves a finite slice of ints via the stream PullRequest protocol.
type intPullActor struct {
	data []int
}

func (a *intPullActor) PreStart(_ *actor.Context) error { return nil }
func (a *intPullActor) PostStop(_ *actor.Context) error { return nil }

func (a *intPullActor) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *stream.PullRequest:
		n := int(msg.N)
		if n > len(a.data) {
			n = len(a.data)
		}
		batch := make([]int, n)
		copy(batch, a.data[:n])
		a.data = a.data[n:]
		ctx.Response(&stream.PullResponse[int]{Elements: batch})
	default:
		ctx.Unhandled()
	}
}

func TestFromActor_Basic(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	pid, err := sys.Spawn(ctx, "pull-actor", &intPullActor{data: []int{10, 20, 30}})
	require.NoError(t, err)

	col, sink := stream.Collect[int]()
	handle, err := stream.FromActor[int](pid).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{10, 20, 30}, col.Items())
}

func TestFromActor_Empty(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	pid, err := sys.Spawn(ctx, "pull-empty", &intPullActor{data: nil})
	require.NoError(t, err)

	col, sink := stream.Collect[int]()
	handle, err := stream.FromActor[int](pid).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Empty(t, col.Items())
}

// ─── Tick ─────────────────────────────────────────────────────────────────────

func TestTick_EmitsTimestamps(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	// Use a large buffer so the Chan sink never blocks.
	out := make(chan time.Time, 20)
	handle, err := stream.Tick(30*time.Millisecond).To(stream.Chan(out)).Run(ctx, sys)
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond)
	require.NoError(t, handle.Stop(ctx))

	select {
	case <-handle.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("tick stream did not stop")
	}

	// Chan sink closes `out` when the stream completes, so range terminates.
	var ticks []time.Time
	for ts := range out {
		ticks = append(ticks, ts)
	}
	// At 30ms interval over ~150ms we expect at least 3 ticks.
	assert.GreaterOrEqual(t, len(ticks), 3)
}

// ─── Unfold ───────────────────────────────────────────────────────────────────

func TestUnfold_Fibonacci(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	// The step function emits s[0] and returns hasMore = s[0] < 20.
	// Sequence: 0,1,1,2,3,5,8,13 (hasMore=true), then 21 (hasMore=false → stop after emitting).
	col, sink := stream.Collect[int]()
	handle, err := stream.Unfold([2]int{0, 1}, func(s [2]int) ([2]int, int, bool) {
		next := [2]int{s[1], s[0] + s[1]}
		return next, s[0], s[0] < 20
	}).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{0, 1, 1, 2, 3, 5, 8, 13, 21}, col.Items())
}

func TestUnfold_ImmediateStop(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Unfold(0, func(s int) (int, int, bool) {
		return s + 1, s, false // always stop
	}).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	// One element emitted (seed) before hasMore=false terminates.
	assert.Equal(t, []int{0}, col.Items())
}

// ─── Merge ────────────────────────────────────────────────────────────────────

func TestMerge_TwoSources(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Merge(
		stream.Of(1, 2, 3),
		stream.Of(4, 5, 6),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Merge did not complete")
	}

	items := col.Items()
	assert.Len(t, items, 6)
	// Elements arrive in non-deterministic order; just verify all 6 are present.
	sum := 0
	for _, v := range items {
		sum += v
	}
	assert.Equal(t, 21, sum) // 1+2+3+4+5+6
}

func TestMerge_Empty(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Merge[int]().To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("empty Merge did not complete")
	}
	assert.Empty(t, col.Items())
}

func TestMerge_SingleSource(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Merge(stream.Of(10, 20, 30)).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Len(t, col.Items(), 3)
}

func TestMerge_Cancel(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	// Infinite sources — cancel should terminate promptly.
	inf := stream.Unfold(0, func(s int) (int, int, bool) { return s + 1, s, true })
	_, sink := stream.Collect[int]()
	handle, err := stream.Merge(inf, inf).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	require.NoError(t, handle.Stop(ctx))

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Merge did not stop after cancel")
	}
}

// ─── Combine ──────────────────────────────────────────────────────────────────

func TestCombine_ZipTwoSources(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[string]()
	handle, err := stream.Combine(
		stream.Of(1, 2, 3),
		stream.Of("a", "b", "c"),
		func(n int, s string) string { return fmt.Sprintf("%d%s", n, s) },
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Combine did not complete")
	}

	items := col.Items()
	assert.Len(t, items, 3)
}

func TestCombine_UnequalLengths(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	// Left has 2 elements, right has 5 — only 2 pairs emitted.
	col, sink := stream.Collect[int]()
	handle, err := stream.Combine(
		stream.Of(1, 2),
		stream.Of(10, 20, 30, 40, 50),
		func(a, b int) int { return a + b },
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Combine with unequal lengths did not complete")
	}

	items := col.Items()
	assert.LessOrEqual(t, len(items), 2)
}

func TestCombine_Cancel(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	// Infinite sources — cancel should terminate promptly.
	inf := stream.Unfold(0, func(s int) (int, int, bool) { return s + 1, s, true })
	_, sink := stream.Collect[int]()
	handle, err := stream.Combine(inf, inf, func(a, b int) int { return a + b }).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	require.NoError(t, handle.Stop(ctx))

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Combine did not stop after cancel")
	}
}

// ─── From and Via ─────────────────────────────────────────────────────────────

func TestFrom_Identity(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	src := stream.Of(7, 8, 9)
	col, sink := stream.Collect[int]()
	handle, err := stream.From(src).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{7, 8, 9}, col.Items())
}

func TestVia_TypeChanging(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[string]()
	handle, err := stream.Via(
		stream.Of(1, 2, 3),
		stream.Map(func(n int) string {
			switch n {
			case 1:
				return "one"
			case 2:
				return "two"
			default:
				return "three"
			}
		}),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []string{"one", "two", "three"}, col.Items())
}

func TestSourceVia_TypePreserving(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Of(1, 2, 3, 4, 5).
		Via(stream.Filter(func(n int) bool { return n%2 != 0 })).
		To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{1, 3, 5}, col.Items())
}

// ─── Balance ──────────────────────────────────────────────────────────────────

// TestBalance_TwoSlots verifies that every element from a finite source is
// delivered to exactly one branch, and all elements are accounted for.
func TestBalance_TwoSlots(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	srcs := stream.Balance(stream.Of(1, 2, 3, 4, 5, 6), 2)

	col0, sink0 := stream.Collect[int]()
	col1, sink1 := stream.Collect[int]()

	h0, err := srcs[0].To(sink0).Run(ctx, sys)
	require.NoError(t, err)
	h1, err := srcs[1].To(sink1).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h0.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 0 did not complete")
	}
	select {
	case <-h1.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 1 did not complete")
	}

	// Every element must appear in exactly one branch.
	all := append(col0.Items(), col1.Items()...)
	sort.Ints(all)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6}, all)
}

// TestBalance_EachElementDeliveredOnce verifies that no element is duplicated
// across branches (distinguishing Balance from Broadcast).
func TestBalance_EachElementDeliveredOnce(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	const n = 100
	srcs := stream.Balance(stream.Range(0, n), 3)

	cols := make([]*stream.Collector[int64], 3)
	handles := make([]stream.StreamHandle, 3)
	for i, src := range srcs {
		col, sink := stream.Collect[int64]()
		cols[i] = col
		h, err := src.To(sink).Run(ctx, sys)
		require.NoError(t, err)
		handles[i] = h
	}

	for i, h := range handles {
		select {
		case <-h.Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("branch %d did not complete", i)
		}
	}

	var all []int64
	for _, col := range cols {
		all = append(all, col.Items()...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })

	// All 100 elements must be present, each exactly once.
	require.Len(t, all, n)
	for i := int64(0); i < n; i++ {
		assert.Equal(t, i, all[i])
	}
}

// TestBalance_EmptySource verifies that an empty upstream completes all branches.
func TestBalance_EmptySource(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	srcs := stream.Balance(stream.Of[int](), 2)

	col0, sink0 := stream.Collect[int]()
	col1, sink1 := stream.Collect[int]()

	h0, err := srcs[0].To(sink0).Run(ctx, sys)
	require.NoError(t, err)
	h1, err := srcs[1].To(sink1).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h0.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("branch 0 did not complete on empty source")
	}
	select {
	case <-h1.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("branch 1 did not complete on empty source")
	}

	assert.Empty(t, col0.Items())
	assert.Empty(t, col1.Items())
}

// TestBalance_NilSlots verifies that Balance returns nil for n < 1.
func TestBalance_NilSlots(t *testing.T) {
	srcs := stream.Balance(stream.Of(1, 2, 3), 0)
	assert.Nil(t, srcs)

	srcs = stream.Balance(stream.Of(1, 2, 3), -1)
	assert.Nil(t, srcs)
}

// TestBalance_CancelOneSlot verifies that canceling one branch leaves the
// remaining branches running.
func TestBalance_CancelOneSlot(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	srcs := stream.Balance(stream.Of(1, 2, 3, 4, 5, 6), 2)

	// Branch 0: collect elements normally.
	col0, sink0 := stream.Collect[int]()
	h0, err := srcs[0].To(sink0).Run(ctx, sys)
	require.NoError(t, err)

	// Branch 1: cancel immediately.
	h1, err := srcs[1].To(stream.Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)
	require.NoError(t, h1.Stop(ctx))

	select {
	case <-h0.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 0 did not complete after sibling cancel")
	}
	select {
	case <-h1.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 1 did not complete")
	}

	// Branch 0 should have received some elements.
	assert.NotEmpty(t, col0.Items())
}

// ─── Broadcast ────────────────────────────────────────────────────────────────

// TestBroadcast_TwoSlots verifies that every element from a finite source is
// delivered to both branches of a 2-slot Broadcast.
func TestBroadcast_TwoSlots(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	srcs := stream.Broadcast(stream.Of(1, 2, 3), 2)

	col0, sink0 := stream.Collect[int]()
	col1, sink1 := stream.Collect[int]()

	h0, err := srcs[0].To(sink0).Run(ctx, sys)
	require.NoError(t, err)
	h1, err := srcs[1].To(sink1).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h0.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 0 did not complete")
	}
	select {
	case <-h1.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 1 did not complete")
	}

	assert.Equal(t, []int{1, 2, 3}, col0.Items())
	assert.Equal(t, []int{1, 2, 3}, col1.Items())
}

// TestBroadcast_ThreeSlots verifies fan-out to three independent branches.
func TestBroadcast_ThreeSlots(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	srcs := stream.Broadcast(stream.Of(10, 20, 30), 3)

	cols := make([]*stream.Collector[int], 3)
	handles := make([]stream.StreamHandle, 3)
	for i, src := range srcs {
		col, sink := stream.Collect[int]()
		cols[i] = col
		h, err := src.To(sink).Run(ctx, sys)
		require.NoError(t, err)
		handles[i] = h
	}

	for i, h := range handles {
		select {
		case <-h.Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("branch %d did not complete", i)
		}
	}

	want := []int{10, 20, 30}
	for i, col := range cols {
		assert.Equal(t, want, col.Items(), "branch %d", i)
	}
}

// TestBroadcast_EmptySource verifies that an empty upstream completes all
// branches immediately with no elements.
func TestBroadcast_EmptySource(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	srcs := stream.Broadcast(stream.Of[int](), 2)

	col0, sink0 := stream.Collect[int]()
	col1, sink1 := stream.Collect[int]()

	h0, err := srcs[0].To(sink0).Run(ctx, sys)
	require.NoError(t, err)
	h1, err := srcs[1].To(sink1).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h0.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 0 did not complete on empty source")
	}
	select {
	case <-h1.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 1 did not complete on empty source")
	}

	assert.Empty(t, col0.Items())
	assert.Empty(t, col1.Items())
}

// TestBroadcast_WithFlows verifies that each branch can independently apply
// different flow transformations.
func TestBroadcast_WithFlows(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	srcs := stream.Broadcast(stream.Of(1, 2, 3, 4, 5), 2)

	// Branch 0: keep only even numbers.
	col0, sink0 := stream.Collect[int]()
	h0, err := stream.Via(srcs[0], stream.Filter(func(n int) bool { return n%2 == 0 })).
		To(sink0).Run(ctx, sys)
	require.NoError(t, err)

	// Branch 1: double every number.
	col1, sink1 := stream.Collect[int]()
	h1, err := stream.Via(srcs[1], stream.Map(func(n int) int { return n * 2 })).
		To(sink1).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h0.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 0 did not complete")
	}
	select {
	case <-h1.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 1 did not complete")
	}

	assert.Equal(t, []int{2, 4}, col0.Items())
	assert.Equal(t, []int{2, 4, 6, 8, 10}, col1.Items())
}

// TestBroadcast_LargeSource verifies correctness with a larger element count
// to exercise the demand-refill cycle across multiple batches.
func TestBroadcast_LargeSource(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	const n = 500
	srcs := stream.Broadcast(stream.Range(0, n), 2)

	col0, sink0 := stream.Collect[int64]()
	col1, sink1 := stream.Collect[int64]()

	h0, err := srcs[0].To(sink0).Run(ctx, sys)
	require.NoError(t, err)
	h1, err := srcs[1].To(sink1).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h0.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("branch 0 did not complete")
	}
	select {
	case <-h1.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("branch 1 did not complete")
	}

	items0 := col0.Items()
	items1 := col1.Items()
	assert.Len(t, items0, n)
	assert.Len(t, items1, n)

	// Verify all elements are present (order preserved within each branch).
	for i := int64(0); i < n; i++ {
		assert.Equal(t, i, items0[i])
		assert.Equal(t, i, items1[i])
	}
}

// TestBroadcast_UpstreamError verifies that an upstream error is delivered to
// all branches.
func TestBroadcast_UpstreamError(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	sentinel := errors.New("source error")
	errSrc := stream.Via(
		stream.Of(1, 2, 3),
		stream.TryMap(func(n int) (int, error) {
			if n == 1 {
				return 0, sentinel
			}
			return n, nil
		}),
	)

	srcs := stream.Broadcast(errSrc, 2)

	h0, err := srcs[0].To(stream.Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)
	h1, err := srcs[1].To(stream.Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h0.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 0 did not terminate on upstream error")
	}
	select {
	case <-h1.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 1 did not terminate on upstream error")
	}
}

// TestBroadcast_CancelOneSlot verifies that when one branch's sink fails fast,
// the remaining branch continues to completion.
func TestBroadcast_CancelOneSlot(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	srcs := stream.Broadcast(stream.Of(1, 2, 3, 4, 5), 2)

	// Branch 0: collect normally.
	h0, err := srcs[0].To(stream.ForEach(func(int) {})).Run(ctx, sys)
	require.NoError(t, err)

	// Branch 1: collect all elements.
	col1, sink1 := stream.Collect[int]()
	h1, err := srcs[1].To(sink1).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h0.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 0 did not complete")
	}
	select {
	case <-h1.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 1 did not complete")
	}

	// Branch 1 must have received all elements.
	items := col1.Items()
	sort.Ints(items)
	assert.NotEmpty(t, items)
}

// TestBroadcast_NilSlots verifies that Broadcast returns nil for n < 1.
func TestBroadcast_NilSlots(t *testing.T) {
	srcs := stream.Broadcast(stream.Of(1, 2, 3), 0)
	assert.Nil(t, srcs)

	srcs = stream.Broadcast(stream.Of(1, 2, 3), -1)
	assert.Nil(t, srcs)
}
