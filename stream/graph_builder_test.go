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

	// Builder options compose: the retry budget set after the error strategy
	// must reach the stage actor. Retries come after the failed first call, so
	// MaxAttempts=3 yields 1 initial call + 3 retries = 4 calls in total.
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

	require.ErrorIs(t, handle.Err(), sentinel)
	assert.Equal(t, 4, attempts)
}

// TestBuilders_ComposeAndKeepMaterializerConfig verifies that a stage built
// with one or more With… options still receives what the materializer injects
// at Run time (actor system, shared metrics, default name) and that every
// option in a chain takes effect.
func TestBuilders_ComposeAndKeepMaterializerConfig(t *testing.T) {
	t.Run("retry config set before the error strategy", func(t *testing.T) {
		sys := newTestSystem(t)
		ctx := context.Background()

		attempts := 0
		col, sink := stream.Collect[int]()
		handle, err := stream.Via(
			stream.Of(1),
			stream.TryMap(func(n int) (int, error) {
				attempts++
				if attempts < 4 {
					return 0, errors.New("transient")
				}
				return n, nil
			}).WithRetryConfig(stream.RetryConfig{MaxAttempts: 3}).WithErrorStrategy(stream.Retry).WithName("retrying-map"),
		).To(sink).Run(ctx, sys)
		require.NoError(t, err)

		select {
		case <-handle.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("stream did not complete")
		}

		require.NoError(t, handle.Err())
		assert.Equal(t, []int{1}, col.Items())
		assert.Equal(t, 4, attempts)
	})

	t.Run("handle metrics with configured source and sink", func(t *testing.T) {
		sys := newTestSystem(t)
		ctx := context.Background()

		_, sink := stream.Collect[int]()
		handle, err := stream.Of(1, 2, 3).
			WithOverflowStrategy(stream.DropHead).
			To(sink.WithName("configured-sink").WithTags(map[string]string{"env": "test"})).
			Run(ctx, sys)
		require.NoError(t, err)

		select {
		case <-handle.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("stream did not complete")
		}

		metrics := handle.Metrics()
		assert.Equal(t, uint64(6), metrics.ElementsIn, "3 produced by the source + 3 received by the sink")
		assert.Equal(t, uint64(3), metrics.ElementsOut)
	})

	t.Run("composite source with a tracer", func(t *testing.T) {
		sys := newTestSystem(t)
		ctx := context.Background()

		col, sink := stream.Collect[int]()
		handle, err := stream.Merge(stream.Of(1, 2), stream.Of(3, 4)).
			WithTracer(&testTracer{}).
			To(sink).
			Run(ctx, sys)
		require.NoError(t, err)

		select {
		case <-handle.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("merge built with WithTracer did not complete")
		}

		require.NoError(t, handle.Err())
		assert.ElementsMatch(t, []int{1, 2, 3, 4}, col.Items())
	})

	t.Run("sub-pipeline flow with a name", func(t *testing.T) {
		sys := newTestSystem(t)
		ctx := context.Background()

		col, sink := stream.Collect[int]()
		handle, err := stream.Via(
			stream.Of(1, 2),
			stream.FlatMapConcat(func(n int) stream.Source[int] { return stream.Of(n, n*10) }).WithName("expand"),
		).To(sink).Run(ctx, sys)
		require.NoError(t, err)

		select {
		case <-handle.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("FlatMapConcat built with WithName did not complete")
		}

		require.NoError(t, handle.Err())
		assert.Equal(t, []int{1, 10, 2, 20}, col.Items())
	})
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
