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

func TestReassemblerInterleavedGroups(t *testing.T) {
	a := mustLogical(t, make([]byte, 2500))
	b := mustLogical(t, make([]byte, 1800))
	chunksA, err := splitLogicalChunks(a, 1, LaneOrdinary, 1024, false)
	require.NoError(t, err)
	chunksB, err := splitLogicalChunks(b, 2, LaneOrdinary, 1024, false)
	require.NoError(t, err)

	re := newChunkReassembler(DefaultMaxMessageSize, 4)
	order := []Frame{chunksA[0], chunksB[0], chunksA[1], chunksB[1]}
	for i := 2; i < len(chunksA); i++ {
		order = append(order, chunksA[i])
	}
	for i := 2; i < len(chunksB); i++ {
		order = append(order, chunksB[i])
	}

	var completed int
	for _, frame := range order {
		out := re.Push(frame)
		require.Empty(t, out.SoftReject)
		require.Empty(t, out.HardError)
		if out.Complete {
			completed++
		}
	}
	assert.Equal(t, 2, completed)
}

func TestReassemblerOversizeSoftReject(t *testing.T) {
	re := newChunkReassembler(1024, 4)
	logical, err := encodeLogicalFrame(Frame{Type: FrameTypeData, Payload: make([]byte, 2000)})
	require.NoError(t, err)
	frames, err := splitLogicalChunks(logical, 1, LaneOrdinary, 512, false)
	require.NoError(t, err)

	out := re.Push(frames[0])
	require.NotEmpty(t, out.SoftReject)
	assert.False(t, out.Complete)

	// Connection stays usable: a later in-cap transfer succeeds.
	small, err := encodeLogicalFrame(Frame{Type: FrameTypeData, Payload: make([]byte, 100)})
	require.NoError(t, err)
	smallFrames, err := splitLogicalChunks(small, 2, LaneOrdinary, 64, false)
	require.NoError(t, err)
	var complete bool
	for _, frame := range smallFrames {
		out = re.Push(frame)
		require.Empty(t, out.SoftReject)
		require.Empty(t, out.HardError)
		complete = complete || out.Complete
	}
	assert.True(t, complete)
}

func TestReassemblerConcurrentCapSoftReject(t *testing.T) {
	re := newChunkReassembler(DefaultMaxMessageSize, 1)
	a, err := encodeLogicalFrame(Frame{Type: FrameTypeData, Payload: make([]byte, 2000)})
	require.NoError(t, err)
	b, err := encodeLogicalFrame(Frame{Type: FrameTypeData, Payload: make([]byte, 2000)})
	require.NoError(t, err)
	chunksA, err := splitLogicalChunks(a, 1, LaneOrdinary, 1024, false)
	require.NoError(t, err)
	chunksB, err := splitLogicalChunks(b, 2, LaneOrdinary, 1024, false)
	require.NoError(t, err)

	out := re.Push(chunksA[0])
	require.Empty(t, out.SoftReject)
	out = re.Push(chunksB[0])
	require.NotEmpty(t, out.SoftReject)

	// Finish first group; connection remains usable.
	for _, frame := range chunksA[1:] {
		out = re.Push(frame)
		require.Empty(t, out.SoftReject)
		require.Empty(t, out.HardError)
	}
	assert.True(t, out.Complete)
}

func TestReassemblerBadIndexHardError(t *testing.T) {
	re := newChunkReassembler(DefaultMaxMessageSize, 4)
	logical, err := encodeLogicalFrame(Frame{Type: FrameTypeData, Payload: make([]byte, 4000)})
	require.NoError(t, err)
	frames, err := splitLogicalChunks(logical, 1, LaneOrdinary, 1024, false)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(frames), 3)

	require.Empty(t, re.Push(frames[0]).HardError)

	// Skip index 1; feed index 2.
	out := re.Push(frames[2])
	require.NotEmpty(t, out.HardError)
}

func TestReassemblerShortLastChunkAborts(t *testing.T) {
	re := newChunkReassembler(DefaultMaxMessageSize, 4)
	logical, err := encodeLogicalFrame(Frame{Type: FrameTypeData, Payload: make([]byte, 2000)})
	require.NoError(t, err)
	frames, err := splitLogicalChunks(logical, 1, LaneOrdinary, 1024, false)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(frames), 2)

	require.Empty(t, re.Push(frames[0]).HardError)
	abort := abortChunkFrame(1, LaneOrdinary, 1)
	out := re.Push(abort)
	assert.True(t, out.Aborted)
	assert.False(t, out.Complete)
	assert.Empty(t, out.HardError)

	// Slot freed: another transfer can start.
	frames2, err := splitLogicalChunks(logical, 2, LaneOrdinary, 1024, false)
	require.NoError(t, err)
	var complete bool
	for _, frame := range frames2 {
		out = re.Push(frame)
		require.Empty(t, out.SoftReject)
		require.Empty(t, out.HardError)
		complete = complete || out.Complete
	}
	assert.True(t, complete)
}

func TestReassemblerOrphanLastChunkIgnored(t *testing.T) {
	re := newChunkReassembler(DefaultMaxMessageSize, 4)
	out := re.Push(abortChunkFrame(99, LaneOrdinary, 0))
	assert.Empty(t, out.SoftReject)
	assert.Empty(t, out.HardError)
	assert.False(t, out.Complete)
	assert.False(t, out.Aborted)
}

func TestReassemblerCloseDiscards(t *testing.T) {
	re := newChunkReassembler(DefaultMaxMessageSize, 4)
	logical, err := encodeLogicalFrame(Frame{Type: FrameTypeData, Payload: make([]byte, 2000)})
	require.NoError(t, err)
	frames, err := splitLogicalChunks(logical, 1, LaneOrdinary, 1024, false)
	require.NoError(t, err)
	require.Empty(t, re.Push(frames[0]).HardError)
	re.Close()
	out := re.Push(frames[1])
	assert.Empty(t, out.HardError)
	assert.False(t, out.Complete)
}

func mustLogical(t *testing.T, payload []byte) []byte {
	t.Helper()
	logical, err := encodeLogicalFrame(Frame{Type: FrameTypeData, Payload: payload})
	require.NoError(t, err)
	return logical
}
