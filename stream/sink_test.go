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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/stream"
)

// ─── ForEach ──────────────────────────────────────────────────────────────────

func TestForEach_CallsFunction(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	var results []int
	handle, err := stream.Of(1, 2, 3).
		To(stream.ForEach(func(n int) { results = append(results, n) })).
		Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{1, 2, 3}, results)
}

// ─── Collect ──────────────────────────────────────────────────────────────────

func TestCollect_Items(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[string]()
	handle, err := stream.Of("a", "b", "c").To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []string{"a", "b", "c"}, col.Items())
}

func TestCollect_Empty(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Of[int]().To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Empty(t, col.Items())
}

// ─── Fold ─────────────────────────────────────────────────────────────────────

func TestFold_Sum(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	res, sink := stream.Fold(0, func(acc, n int) int { return acc + n })
	handle, err := stream.Of(1, 2, 3, 4, 5).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, 15, res.Value())
}

func TestFold_EmptyStream(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	res, sink := stream.Fold(42, func(acc, n int) int { return acc + n })
	handle, err := stream.Of[int]().To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, 42, res.Value())
}

func TestFold_Concat(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	res, sink := stream.Fold("", func(acc, s string) string { return acc + s })
	handle, err := stream.Of("hello", " ", "world").To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, "hello world", res.Value())
}

// ─── First ────────────────────────────────────────────────────────────────────

func TestFirst_CapturesFirstElement(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	res, sink := stream.First[int]()
	handle, err := stream.Of(10, 20, 30).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, 10, res.Value())
}

func TestFirst_SingleElement(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	res, sink := stream.First[string]()
	handle, err := stream.Of("only").To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, "only", res.Value())
}

// ─── Ignore ───────────────────────────────────────────────────────────────────

func TestIgnore_Completes(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	handle, err := stream.Of(1, 2, 3).To(stream.Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}
	assert.NoError(t, handle.Err())
}

// ─── Chan ─────────────────────────────────────────────────────────────────────

func TestChan_ReceivesAllElements(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	out := make(chan int, 10)
	handle, err := stream.Of(1, 2, 3).To(stream.Chan(out)).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()

	// Chan sink closes the output channel on completion.
	var results []int
	for v := range out {
		results = append(results, v)
	}
	assert.Equal(t, []int{1, 2, 3}, results)
}

func TestChan_ClosedOnComplete(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	out := make(chan string, 5)
	handle, err := stream.Of("x", "y").To(stream.Chan(out)).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()

	// After stream completes the channel should be closed; range finishes.
	var items []string
	for v := range out {
		items = append(items, v)
	}
	assert.Equal(t, []string{"x", "y"}, items)
}

// ─── ToActor ──────────────────────────────────────────────────────────────────

// echoActor collects integer messages for inspection; unhandled messages are ignored.
// A mutex guards the received slice so the test goroutine can read it safely.
type echoActor struct {
	mu       sync.Mutex
	received []int
}

func (a *echoActor) PreStart(_ *actor.Context) error { return nil }
func (a *echoActor) PostStop(_ *actor.Context) error { return nil }
func (a *echoActor) Receive(ctx *actor.ReceiveContext) {
	if v, ok := ctx.Message().(int); ok {
		a.mu.Lock()
		a.received = append(a.received, v)
		a.mu.Unlock()
	}
}

func (a *echoActor) snapshot() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]int, len(a.received))
	copy(out, a.received)
	return out
}

func TestToActor_ForwardsElements(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	echo := &echoActor{}
	pid, err := sys.Spawn(ctx, "echo", echo)
	require.NoError(t, err)

	handle, err := stream.Of(1, 2, 3).To(stream.ToActor[int](pid)).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()

	// Give the echo actor a brief moment to flush its mailbox.
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, []int{1, 2, 3}, echo.snapshot())
}

// ─── ToActorNamed ─────────────────────────────────────────────────────────────

func TestToActorNamed_ForwardsElements(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	echo := &echoActor{}
	_, err := sys.Spawn(ctx, "named-echo", echo)
	require.NoError(t, err)

	handle, err := stream.Of(10, 20).
		To(stream.ToActorNamed[int](sys, "named-echo")).
		Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, []int{10, 20}, echo.snapshot())
}

func TestToActorNamed_UnknownActor_FailsFast(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	handle, err := stream.Of(1).
		To(stream.ToActorNamed[int](sys, "does-not-exist")).
		Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate on unknown actor")
	}
}

// ─── Collector.Items blocks until complete ────────────────────────────────────

func TestCollector_Items_BlocksUntilComplete(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()

	done := make(chan []int, 1)
	go func() { done <- col.Items() }()

	// Items() should block while stream is running.
	select {
	case <-done:
		t.Fatal("Items() should block until stream completes")
	case <-time.After(50 * time.Millisecond):
	}

	handle, err := stream.Of(42).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()

	select {
	case items := <-done:
		assert.Equal(t, []int{42}, items)
	case <-time.After(3 * time.Second):
		t.Fatal("Items() did not unblock after stream completion")
	}
}
