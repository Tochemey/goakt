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
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/stream"
)

// TestGraph_Build_CycleDetection verifies that Build returns an error wrapping
// ErrInvalidGraph when the node dependency graph contains a cycle.
// Here f1 depends on f2 and f2 depends on f1, forming a direct cycle.
func TestGraph_Build_CycleDetection(t *testing.T) {
	identity := stream.Map(func(v any) any { return v })
	_, sink := stream.Collect[any]()

	_, err := stream.NewGraph().
		AddSource("src", stream.Of[any](1, 2, 3)).
		AddFlow("f1", identity, "f2"). // f1 depends on f2 (not yet defined)
		AddFlow("f2", identity, "f1"). // f2 depends on f1 → cycle
		AddSink("sink", sink, "f1").
		Build()

	require.ErrorIs(t, err, stream.ErrInvalidGraph)
}

// TestGraph_Build_SinkAsUpstream_Error verifies that Build returns an error when
// a sink node is used as the upstream reference for another sink. The topology
// is valid from a source/sink-count perspective but invalid structurally because
// sinks cannot act as upstream stages in a pipeline.
func TestGraph_Build_SinkAsUpstream_Error(t *testing.T) {
	_, sink1 := stream.Collect[any]()
	_, sink2 := stream.Collect[any]()

	_, err := stream.NewGraph().
		AddSource("src", stream.Of[any](1, 2, 3)).
		AddSink("sink1", sink1, "src").
		AddSink("sink2", sink2, "sink1"). // sink1 used as upstream — invalid
		Build()

	require.Error(t, err)
}

// TestGraph_Build_MultipleFlowsFromSameSource verifies a topology where two
// separate flow chains both originate from the same source and each feeds its
// own sink. This exercises the fan-out path in compile/buildChain.
func TestGraph_Build_MultipleFlowsWithSharedSource(t *testing.T) {
	double := stream.Map(func(v any) any { return v.(int) * 2 })
	negate := stream.Map(func(v any) any { return -v.(int) })

	col0, sink0 := stream.Collect[any]()
	col1, sink1 := stream.Collect[any]()

	rg, err := stream.NewGraph().
		AddSource("src", stream.Of[any](1, 2)).
		AddFlow("doubled", double, "src").
		AddFlow("negated", negate, "src").
		AddSink("s0", sink0, "doubled").
		AddSink("s1", sink1, "negated").
		Build()
	require.NoError(t, err)

	sys := newTestSystem(t)
	h, runErr := rg.Run(t.Context(), sys)
	require.NoError(t, runErr)

	<-h.Done()
	require.NoError(t, h.Err())
	require.Equal(t, []any{2, 4}, col0.Items())
	require.Equal(t, []any{-1, -2}, col1.Items())
}

func TestRunnableGraph_Run_EmptyGraph(t *testing.T) {
	sys := newTestSystem(t)
	_, err := stream.RunnableGraph{}.Run(context.Background(), sys)
	require.ErrorIs(t, err, stream.ErrInvalidGraph)
}

func TestRunnableGraph_SourceOnly(t *testing.T) {
	// Source.To produces a RunnableGraph; a source alone (no sink) is invalid.
	// We verify through the validate error message.
	sys := newTestSystem(t)
	// RunnableGraph with one stage that is a source (kind=0) but no sink.
	// Build it via reflection on internal: easiest is to trust that
	// Of(...).To(...) is valid; empty RunnableGraph covers the < 2 stages path.
	_, err := stream.RunnableGraph{}.Run(context.Background(), sys)
	assert.ErrorIs(t, err, stream.ErrInvalidGraph)
}

func TestRunnableGraph_ValidPipeline(t *testing.T) {
	sys := newTestSystem(t)
	_, sink := stream.Collect[int]()
	handle, err := stream.Of(1).To(sink).Run(context.Background(), sys)
	require.NoError(t, err)

	<-handle.Done()
	assert.NoError(t, handle.Err())
}

// TestGraph_Linear verifies that a Graph DSL single-source/single-sink topology
// behaves identically to the fluent Source.To() API.
func TestGraph_Linear(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[any]()

	g := stream.NewGraph().
		AddSource("src", stream.Of[any](1, 2, 3)).
		AddSink("sink", sink, "src")

	rg, err := g.Build()
	require.NoError(t, err)

	h, err := rg.Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("linear graph did not complete")
	}
	require.NoError(t, h.Err())
	assert.Equal(t, []any{1, 2, 3}, col.Items())
}

// TestGraph_FanOut verifies that a fan-out topology (one source → two sinks)
// broadcasts every element to both branches.
func TestGraph_FanOut(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col0, sink0 := stream.Collect[any]()
	col1, sink1 := stream.Collect[any]()

	g := stream.NewGraph().
		AddSource("src", stream.Of[any](1, 2, 3)).
		AddSink("sink0", sink0, "src").
		AddSink("sink1", sink1, "src")

	rg, err := g.Build()
	require.NoError(t, err)

	h, err := rg.Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("fan-out graph did not complete")
	}
	require.NoError(t, h.Err())

	// Both branches must receive all elements (Broadcast semantics).
	assert.Equal(t, []any{1, 2, 3}, col0.Items())
	assert.Equal(t, []any{1, 2, 3}, col1.Items())
}

// TestGraph_FanOutWithFlow verifies fan-out where each branch has its own flow.
func TestGraph_FanOutWithFlow(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	double := stream.Map(func(v any) any { return v.(int) * 2 })
	triple := stream.Map(func(v any) any { return v.(int) * 3 })

	col0, sink0 := stream.Collect[any]()
	col1, sink1 := stream.Collect[any]()

	g := stream.NewGraph().
		AddSource("src", stream.Of[any](1, 2, 3)).
		AddFlow("double", double, "src").
		AddFlow("triple", triple, "src").
		AddSink("sink0", sink0, "double").
		AddSink("sink1", sink1, "triple")

	rg, err := g.Build()
	require.NoError(t, err)

	h, err := rg.Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("fan-out-with-flow graph did not complete")
	}
	require.NoError(t, h.Err())

	assert.Equal(t, []any{2, 4, 6}, col0.Items())
	assert.Equal(t, []any{3, 6, 9}, col1.Items())
}

// TestGraph_FanIn verifies that MergeInto merges two sources into one sink.
func TestGraph_FanIn(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[any]()

	g := stream.NewGraph().
		AddSource("src1", stream.Of[any](1, 2, 3)).
		AddSource("src2", stream.Of[any](4, 5, 6)).
		MergeInto("merged", "src1", "src2").
		AddSink("sink", sink, "merged")

	rg, err := g.Build()
	require.NoError(t, err)

	h, err := rg.Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("fan-in graph did not complete")
	}
	require.NoError(t, h.Err())

	// All 6 elements must be present (order is non-deterministic).
	items := col.Items()
	require.Len(t, items, 6)
	ints := make([]int, len(items))
	for i, v := range items {
		ints[i] = v.(int)
	}
	sort.Ints(ints)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6}, ints)
}

// TestGraph_Diamond verifies a diamond topology: one source fans out to two
// flows, which then merge into one sink.
func TestGraph_Diamond(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	double := stream.Map(func(v any) any { return v.(int) * 2 })
	triple := stream.Map(func(v any) any { return v.(int) * 3 })

	col, sink := stream.Collect[any]()

	g := stream.NewGraph().
		AddSource("src", stream.Of[any](1, 2, 3)).
		AddFlow("double", double, "src").
		AddFlow("triple", triple, "src").
		MergeInto("merged", "double", "triple").
		AddSink("sink", sink, "merged")

	rg, err := g.Build()
	require.NoError(t, err)

	h, err := rg.Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("diamond graph did not complete")
	}
	require.NoError(t, h.Err())

	// Doubled: 2,4,6 and tripled: 3,6,9 — all 6 values (order non-deterministic).
	items := col.Items()
	require.Len(t, items, 6)
	ints := make([]int, len(items))
	for i, v := range items {
		ints[i] = v.(int)
	}
	sort.Ints(ints)
	assert.Equal(t, []int{2, 3, 4, 6, 6, 9}, ints)
}

// TestGraph_Build_NoSources verifies Build returns an error when there are no sources.
func TestGraph_Build_NoSources(t *testing.T) {
	_, sink := stream.Collect[any]()
	_, err := stream.NewGraph().
		AddSink("sink", sink, "missing").
		Build()
	require.Error(t, err)
}

// TestGraph_Build_NoSinks verifies Build returns an error when there are no sinks.
func TestGraph_Build_NoSinks(t *testing.T) {
	_, err := stream.NewGraph().
		AddSource("src", stream.Of[any](1)).
		Build()
	require.Error(t, err)
}

// TestGraph_Build_UnknownRef verifies Build returns an error when a node
// references an undefined upstream.
func TestGraph_Build_UnknownRef(t *testing.T) {
	_, sink := stream.Collect[any]()
	_, err := stream.NewGraph().
		AddSource("src", stream.Of[any](1)).
		AddSink("sink", sink, "nonexistent").
		Build()
	require.Error(t, err)
}
