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

package stream

import (
	"container/heap"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMergeSeqHeapPopClearsBackingArraySlot(t *testing.T) {
	entry := mergeSeqEntry{seq: 1, value: "retained"}
	queue := mergeSeqHeap{entry}
	backing := queue[:cap(queue)]

	popped := heap.Pop(&queue)

	require.Equal(t, entry, popped)
	require.Empty(t, queue)
	require.Equal(t, mergeSeqEntry{}, backing[0])
}

// TestMergeSequence_GapLargerThanOneWindow verifies that MergeSequence reads
// on when the next expected sequence number is missing, even once it holds
// more than one demand window of later elements: here 600 elements arrive
// before element 0 does.
func TestMergeSequence_GapLargerThanOneWindow(t *testing.T) {
	sys := newInternalTestSystem(t)

	const held = 600
	input := make([]int, 0, held+1)
	for i := 1; i <= held; i++ {
		input = append(input, i)
	}
	input = append(input, 0)

	col, sink := Collect[int]()
	handle, err := MergeSequence(func(n int) int64 { return int64(n) }, Of(input...)).To(sink).Run(context.Background(), sys)
	require.NoError(t, err)

	waitDone(t, handle, 5*time.Second)
	require.NoError(t, handle.Err())

	want := make([]int, held+1)
	for i := range want {
		want[i] = i
	}

	require.Equal(t, want, col.Items())
}
