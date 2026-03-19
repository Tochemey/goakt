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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/stream"
)

func TestMaterialize_Metrics(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	_, sink := stream.Collect[int]()
	handle, err := stream.Of(1, 2, 3).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()

	// Metrics aggregate source + sink counters.
	// source: elementsIn=3 (produced); sink: elementsIn=3 (received), elementsOut=3 (consumed).
	m := handle.Metrics()
	assert.Equal(t, uint64(6), m.ElementsIn, "aggregate: 3 produced by source + 3 received by sink")
	assert.Equal(t, uint64(3), m.ElementsOut, "sink consumed all 3 elements")
	assert.Equal(t, uint64(0), m.Errors)
	assert.Equal(t, uint64(0), m.DroppedElements)
}

func TestMaterialize_HandleStop(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	_, sink := stream.Collect[time.Time]()
	handle, err := stream.Tick(10*time.Millisecond).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	time.Sleep(30 * time.Millisecond)
	require.NoError(t, handle.Stop(ctx))

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not stop via handle.Stop")
	}
}

func TestMaterialize_HandleAbort(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	_, sink := stream.Collect[time.Time]()
	handle, err := stream.Tick(10*time.Millisecond).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	handle.Abort()

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not abort via handle.Abort")
	}
	assert.ErrorIs(t, handle.Err(), stream.ErrStreamCanceled)
}

func TestMaterialize_InvalidGraph_NoPanic(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	_, err := stream.RunnableGraph{}.Run(ctx, sys)
	require.ErrorIs(t, err, stream.ErrInvalidGraph)
}

func TestMaterialize_SinkCancel_ViaPipelineCompletion(t *testing.T) {
	// Ensure that when a pipeline completes normally, handle.Done is closed.
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.Of(1, 2, 3, 4, 5).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("pipeline did not complete")
	}
	assert.NoError(t, handle.Err())
	assert.Len(t, col.Items(), 5)
}

func TestMaterialize_MultipleFlowStages(t *testing.T) {
	sys := newTestSystem(t)

	col, sink := stream.Collect[int]()
	handle, err := stream.Via(
		stream.Via(
			stream.Via(
				stream.Of(1, 2, 3, 4, 5, 6),
				stream.Filter(func(n int) bool { return n%2 == 0 }),
			),
			stream.Map(func(n int) int { return n * n }),
		),
		stream.Filter(func(n int) bool { return n > 4 }),
	).To(sink).Run(context.Background(), sys)
	require.NoError(t, err)
	<-handle.Done()
	// Evens: 2,4,6 → squares: 4,16,36 → > 4: 16, 36
	assert.Equal(t, []int{16, 36}, col.Items())
}
