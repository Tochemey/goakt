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

package net

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPendingTableCompleteAndAbandon(t *testing.T) {
	table := newPendingTable()
	wait := table.register(7)
	assert.Equal(t, 1, table.len())

	require.True(t, table.complete(7, Frame{Type: FrameTypeReply, Correlation: 7}))
	frame := <-wait
	assert.Equal(t, FrameTypeReply, frame.Type)
	putPendingWaiter(wait)
	assert.Equal(t, 0, table.len())

	// Late complete after abandon is a no-op; abandon wins the slot and
	// pools the channel itself, so the caller must not pool it again.
	_ = table.register(8)
	require.True(t, table.abandon(8))
	assert.False(t, table.complete(8, Frame{Type: FrameTypeReply, Correlation: 8}))
	assert.Equal(t, 0, table.len())

	// abandon loses when complete already won the slot; the receiver then
	// owns the channel and pools it after draining the delivered frame.
	wait = table.register(9)
	require.True(t, table.complete(9, Frame{Type: FrameTypeReply, Correlation: 9}))
	require.False(t, table.abandon(9))
	assert.Equal(t, FrameTypeReply, (<-wait).Type)
	putPendingWaiter(wait)
	assert.Equal(t, 0, table.len())
}

func TestPendingTableFailAll(t *testing.T) {
	table := newPendingTable()
	w1 := table.register(1)
	w2 := table.register(2)

	table.failAll(Frame{Type: FrameTypeError})
	assert.Equal(t, FrameTypeError, (<-w1).Type)
	assert.Equal(t, FrameTypeError, (<-w2).Type)
	putPendingWaiter(w1)
	putPendingWaiter(w2)
	assert.Equal(t, 0, table.len())
}
