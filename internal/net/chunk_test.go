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
	"crypto/sha256"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitAndJoinLogicalChunks(t *testing.T) {
	payload := make([]byte, 3000)
	for i := range payload {
		payload[i] = byte(i)
	}
	wantSum := sha256.Sum256(payload)

	logical, err := encodeLogicalFrame(Frame{
		Type:    FrameTypeData,
		Lane:    LaneOrdinary,
		Payload: payload,
	})
	require.NoError(t, err)

	frames, err := splitLogicalChunks(logical, 7, LaneOrdinary, 1024, false)
	require.NoError(t, err)
	require.Greater(t, len(frames), 1)
	assert.True(t, frames[0].IsFirstChunk())
	assert.False(t, frames[0].ExpectsReply())
	assert.True(t, frames[len(frames)-1].IsLastChunk())

	// The body (prefix plus data) is capped at chunkSize so a receiver's
	// pooled read lands in the chunkSize bucket and every chunk fits a peer
	// whose negotiated max frame size equals the chunk size.
	for _, frame := range frames {
		assert.LessOrEqual(t, frame.Length, uint32(1024))
		assert.Equal(t, frame.bodyLen(), int(frame.Length))
	}

	re := newChunkReassembler(DefaultMaxMessageSize, 4)
	var got Frame
	for _, frame := range frames {
		out := re.Push(frame)
		require.Empty(t, out.SoftReject)
		require.Empty(t, out.HardError)
		if out.Complete {
			got = out.Frame
		}
	}
	require.Equal(t, FrameTypeData, got.Type)
	assert.Equal(t, wantSum, sha256.Sum256(got.Payload))
}

func TestSplitLogicalChunksExpectsReplyOnFirstOnly(t *testing.T) {
	logical, err := encodeLogicalFrame(Frame{
		Type:        FrameTypeData,
		Flags:       FrameFlagExpectsReply,
		Lane:        LaneControl,
		Correlation: 9,
		Payload:     make([]byte, 2500),
	})
	require.NoError(t, err)

	frames, err := splitLogicalChunks(logical, 9, LaneControl, 1024, true)
	require.NoError(t, err)
	require.Greater(t, len(frames), 1)
	assert.True(t, frames[0].ExpectsReply())
	for _, frame := range frames[1:] {
		assert.False(t, frame.ExpectsReply())
	}
}

func TestParseChunkPayloadFirstCarriesTotal(t *testing.T) {
	payload := encodeChunkPayload(0, 4096, true, []byte("abc"))
	index, total, data, err := parseChunkPayload(payload, true)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), index)
	assert.Equal(t, uint64(4096), total)
	assert.Equal(t, []byte("abc"), data)
}

func TestAbortChunkFrameFlags(t *testing.T) {
	frame := abortChunkFrame(42, LaneLarge, 3)
	assert.Equal(t, FrameTypeChunk, frame.Type)
	assert.Equal(t, uint64(42), frame.Correlation)
	assert.Equal(t, LaneLarge, frame.Lane)
	assert.True(t, frame.IsLastChunk())
	assert.False(t, frame.IsFirstChunk())
	assert.False(t, frame.ExpectsReply())

	index, total, data, err := parseChunkPayload(frame.Payload, false)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), index)
	assert.Zero(t, total)
	assert.Empty(t, data)
}
