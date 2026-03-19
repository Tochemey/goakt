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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/stream"
)

// TestLinearGraph_From_To verifies the basic From → To pipeline.
func TestLinearGraph_From_To(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.From(stream.Of(1, 2, 3)).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	<-handle.Done()
	assert.NoError(t, handle.Err())
	assert.Equal(t, []int{1, 2, 3}, col.Items())
}

// TestLinearGraph_Via_TypePreserving verifies fluent chaining of type-preserving flows.
func TestLinearGraph_Via_TypePreserving(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	double := stream.Map(func(n int) int { return n * 2 })
	addOne := stream.Map(func(n int) int { return n + 1 })

	col, sink := stream.Collect[int]()
	handle, err := stream.From(stream.Of(1, 2, 3)).
		Via(double).
		Via(addOne).
		To(sink).
		Run(ctx, sys)
	require.NoError(t, err)

	<-handle.Done()
	assert.NoError(t, handle.Err())
	// (1*2)+1=3, (2*2)+1=5, (3*2)+1=7
	assert.Equal(t, []int{3, 5, 7}, col.Items())
}

// TestLinearGraph_Source_Escape verifies that Source() exposes the underlying
// Source[T] for use with the package-level Via free function.
func TestLinearGraph_Source_Escape(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	// Build a LinearGraph up to a filter, then escape via Source() for
	// type-changing transformation.
	filtered := stream.From(stream.Of(1, 2, 3, 4)).
		Via(stream.Filter(func(n int) bool { return n%2 == 0 })).
		Source()

	col, sink := stream.Collect[string]()
	handle, err := stream.Via(filtered, stream.Map(func(n int) string {
		if n == 2 {
			return "two"
		}
		return "four"
	})).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	<-handle.Done()
	assert.NoError(t, handle.Err())
	assert.Equal(t, []string{"two", "four"}, col.Items())
}

// TestViaLinear_TypeChanging verifies the ViaLinear free function for
// type-changing flows while staying in the LinearGraph API.
func TestViaLinear_TypeChanging(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	stringify := stream.Map(func(n int) string {
		switch n {
		case 1:
			return "one"
		case 2:
			return "two"
		default:
			return "three"
		}
	})

	col, sink := stream.Collect[string]()
	handle, err := stream.ViaLinear(stream.From(stream.Of(1, 2, 3)), stringify).
		To(sink).
		Run(ctx, sys)
	require.NoError(t, err)

	<-handle.Done()
	assert.NoError(t, handle.Err())
	assert.Equal(t, []string{"one", "two", "three"}, col.Items())
}

// TestLinearGraph_EmptySource verifies From with an empty source completes cleanly.
func TestLinearGraph_EmptySource(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	handle, err := stream.From(stream.Of[int]()).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	<-handle.Done()
	assert.NoError(t, handle.Err())
	assert.Empty(t, col.Items())
}
