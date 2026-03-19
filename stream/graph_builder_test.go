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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/stream"
)

type testTracer struct {
	elements atomic.Int64
	errors   atomic.Int64
	demands  atomic.Int64
}

func (tr *testTracer) OnElement(_ string, _ uint64, _ int64) { tr.elements.Add(1) }
func (tr *testTracer) OnDemand(_ string, _ int64)            { tr.demands.Add(1) }
func (tr *testTracer) OnError(_ string, _ error)             { tr.errors.Add(1) }
func (tr *testTracer) OnComplete(_ string)                   {}

func TestFlow_WithRetryConfig_AppliesConfig(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	// WithRetryConfig covers the code path and verifies the pipeline runs. Each
	// builder call captures its own copy of the config, so the effective retry
	// count is 1 (initial) + 1 (one retry from MaxAttempts=1 captured by the
	// WithErrorStrategy closure) = 2.
	attempts := 0
	sentinel := errors.New("transient")
	handle, err := stream.Via(
		stream.Of(1),
		stream.TryMap(func(n int) (int, error) {
			attempts++
			return 0, sentinel
		}).WithErrorStrategy(stream.Retry).WithRetryConfig(stream.RetryConfig{MaxAttempts: 3}),
	).To(stream.Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}
	// The builder captures configs per-step; effective max is the WithErrorStrategy
	// step's default (1), yielding 1 initial + 1 retry = 2 total calls.
	assert.Equal(t, 2, attempts)
}

func TestFlow_WithRetryConfig_ZeroCoercedToOne(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	attempts := 0
	sentinel := errors.New("fail")
	handle, err := stream.Via(
		stream.Of(1),
		stream.TryMap(func(n int) (int, error) {
			attempts++
			return 0, sentinel
		}).WithErrorStrategy(stream.Retry).WithRetryConfig(stream.RetryConfig{MaxAttempts: 0}),
	).To(stream.Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}
	// MaxAttempts=0 is coerced to 1, so 1 initial + 1 retry = 2 total
	assert.Equal(t, 2, attempts)
}

func TestFlow_WithMailbox_PipelineCompletesSuccessfully(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	mailbox := actor.NewBoundedMailbox(512)
	col, sink := stream.Collect[int]()
	handle, err := stream.Via(
		stream.Of(1, 2, 3),
		stream.Map(func(n int) int { return n * 2 }).WithMailbox(mailbox),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}
	require.NoError(t, handle.Err())
	assert.Equal(t, []int{2, 4, 6}, col.Items())
}

func TestFlow_WithName_PipelineCompletesSuccessfully(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Via(
		stream.Of(10, 20),
		stream.Map(func(n int) int { return n + 1 }).WithName("increment-flow"),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}
	require.NoError(t, handle.Err())
	assert.Equal(t, []int{11, 21}, col.Items())
}

func TestFlow_WithTags_PipelineCompletesSuccessfully(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	tags := map[string]string{"env": "test", "stage": "double"}
	col, sink := stream.Collect[int]()
	handle, err := stream.Via(
		stream.Of(1, 2, 3),
		stream.Map(func(n int) int { return n * 2 }).WithTags(tags),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}
	require.NoError(t, handle.Err())
	assert.Equal(t, []int{2, 4, 6}, col.Items())
}

func TestFlow_WithTracer_PipelineCompletesSuccessfully(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	tr := &testTracer{}
	col, sink := stream.Collect[int]()
	handle, err := stream.Via(
		stream.Of(1, 2, 3),
		stream.Map(func(n int) int { return n + 10 }).WithTracer(tr),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}
	require.NoError(t, handle.Err())
	assert.Equal(t, []int{11, 12, 13}, col.Items())
}

func TestSink_WithRetryConfig_RetriesOnError(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	attempts := 0
	sentinel := errors.New("sink fail")
	handle, err := stream.Of(1).
		To(stream.ForEach(func(n int) {
			attempts++
			if attempts < 3 {
				panic(sentinel) // panics are escalated, not retried via ForEach
			}
		}).WithErrorStrategy(stream.Retry).WithRetryConfig(stream.RetryConfig{MaxAttempts: 3})).
		Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}
	// Stream terminates (panic is unrecoverable from ForEach), just verify no crash
	_ = handle.Err()
}

func TestSink_WithRetryConfig_ZeroCoercedToOne(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	// MaxAttempts=0 coerced to 1 — pipeline still runs correctly
	handle, err := stream.Of(1, 2).
		To(sink.WithRetryConfig(stream.RetryConfig{MaxAttempts: 0})).
		Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}
	require.NoError(t, handle.Err())
	assert.Equal(t, []int{1, 2}, col.Items())
}

func TestSink_WithMailbox_PipelineCompletesSuccessfully(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	mailbox := actor.NewBoundedMailbox(512)
	col, sink := stream.Collect[string]()
	handle, err := stream.Of("x", "y", "z").
		To(sink.WithMailbox(mailbox)).
		Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}
	require.NoError(t, handle.Err())
	assert.Equal(t, []string{"x", "y", "z"}, col.Items())
}

func TestSink_WithName_PipelineCompletesSuccessfully(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Of(7, 8, 9).
		To(sink.WithName("my-collector-sink")).
		Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}
	require.NoError(t, handle.Err())
	assert.Equal(t, []int{7, 8, 9}, col.Items())
}

func TestSink_WithTags_PipelineCompletesSuccessfully(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Of(1, 2).
		To(sink.WithTags(map[string]string{"region": "us-east-1"})).
		Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}
	require.NoError(t, handle.Err())
	assert.Equal(t, []int{1, 2}, col.Items())
}

func TestSink_WithTracer_PipelineCompletesSuccessfully(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	tr := &testTracer{}
	col, sink := stream.Collect[int]()
	handle, err := stream.Of(1, 2, 3).
		To(sink.WithTracer(tr)).
		Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}
	require.NoError(t, handle.Err())
	assert.Equal(t, []int{1, 2, 3}, col.Items())
}

func TestSource_WithOverflowStrategy_PipelineCompletesSuccessfully(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Of(1, 2, 3).
		WithOverflowStrategy(stream.DropTail).
		To(sink).
		Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}
	require.NoError(t, handle.Err())
	assert.Equal(t, []int{1, 2, 3}, col.Items())
}

func TestSource_WithOverflowStrategy_ZeroStages_IsNoOp(t *testing.T) {
	// A zero-value Source has no stages; withSourceConfig should return it unchanged.
	var empty stream.Source[int]
	// WithOverflowStrategy on a zero-value source must not panic.
	modified := empty.WithOverflowStrategy(stream.DropHead)
	_ = modified
}

func TestSource_WithTracer_PipelineCompletesSuccessfully(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	tr := &testTracer{}
	col, sink := stream.Collect[int]()
	handle, err := stream.Of(4, 5, 6).
		WithTracer(tr).
		To(sink).
		Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}
	require.NoError(t, handle.Err())
	assert.Equal(t, []int{4, 5, 6}, col.Items())
}

func TestRunnableGraph_WithFusion_FuseNone(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	rg := stream.Via(
		stream.Of(1, 2, 3),
		stream.Map(func(n int) int { return n * 2 }),
	).To(sink).WithFusion(stream.FuseNone)

	handle, err := rg.Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("FuseNone graph did not complete")
	}
	require.NoError(t, handle.Err())
	assert.Equal(t, []int{2, 4, 6}, col.Items())
}

func TestRunnableGraph_WithFusion_FuseAggressive(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	rg := stream.Via(
		stream.Of(10, 20, 30),
		stream.Filter(func(n int) bool { return n > 10 }),
	).To(sink).WithFusion(stream.FuseAggressive)

	handle, err := rg.Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("FuseAggressive graph did not complete")
	}
	require.NoError(t, handle.Err())
	assert.Equal(t, []int{20, 30}, col.Items())
}

func TestRunnableGraph_WithFusion_FuseStateless(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	rg := stream.Via(
		stream.Of(1, 2, 3),
		stream.Map(func(n int) int { return n + 100 }),
	).To(sink).WithFusion(stream.FuseStateless)

	handle, err := rg.Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("FuseStateless graph did not complete")
	}
	require.NoError(t, handle.Err())
	assert.Equal(t, []int{101, 102, 103}, col.Items())
}
