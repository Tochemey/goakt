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

func TestEncodeDecodeFrameHeaderRoundTrip(t *testing.T) {
	original := Frame{
		Version:     ProtocolVersion,
		Type:        FrameTypeData,
		Flags:       FrameFlagHasMetadata | FrameFlagExpectsReply,
		Lane:        LaneOrdinary,
		Length:      42,
		Correlation: 7,
	}

	var hdr [FrameHeaderSize]byte
	require.NoError(t, encodeFrameHeader(hdr[:], original))

	decoded, err := decodeFrameHeader(hdr[:], 1024)
	require.NoError(t, err)
	assert.Equal(t, original.Version, decoded.Version)
	assert.Equal(t, original.Type, decoded.Type)
	assert.Equal(t, original.Flags, decoded.Flags)
	assert.Equal(t, original.Lane, decoded.Lane)
	assert.Equal(t, original.Length, decoded.Length)
	assert.Equal(t, original.Correlation, decoded.Correlation)
	assert.True(t, decoded.HasMetadata())
	assert.True(t, decoded.ExpectsReply())
}

func TestDecodeFrameHeaderAdversarial(t *testing.T) {
	t.Run("truncated", func(t *testing.T) {
		_, err := decodeFrameHeader(make([]byte, 8), 1024)
		require.Error(t, err)
	})

	t.Run("bad version", func(t *testing.T) {
		hdr := validHeader(t)
		hdr[0] = 0x01
		_, err := decodeFrameHeader(hdr, 1024)
		require.Error(t, err)
	})

	t.Run("bad type", func(t *testing.T) {
		hdr := validHeader(t)
		hdr[1] = 0xFF
		_, err := decodeFrameHeader(hdr, 1024)
		require.Error(t, err)
	})

	t.Run("reserved bits", func(t *testing.T) {
		hdr := validHeader(t)
		hdr[2] |= frameFlagReservedMask
		_, err := decodeFrameHeader(hdr, 1024)
		require.Error(t, err)
	})

	t.Run("oversize length", func(t *testing.T) {
		original := Frame{
			Version: ProtocolVersion,
			Type:    FrameTypePing,
			Length:  2048,
		}
		var hdr [FrameHeaderSize]byte
		require.NoError(t, encodeFrameHeader(hdr[:], original))
		_, err := decodeFrameHeader(hdr[:], 1024)
		require.ErrorIs(t, err, ErrFrameTooLarge)
	})

	t.Run("reply without correlation", func(t *testing.T) {
		original := Frame{
			Version: ProtocolVersion,
			Type:    FrameTypeReply,
		}
		var hdr [FrameHeaderSize]byte
		err := encodeFrameHeader(hdr[:], original)
		require.Error(t, err)
	})

	t.Run("connection-scoped error allows zero correlation", func(t *testing.T) {
		original := Frame{
			Version: ProtocolVersion,
			Type:    FrameTypeError,
		}
		var hdr [FrameHeaderSize]byte
		require.NoError(t, encodeFrameHeader(hdr[:], original))
		decoded, err := decodeFrameHeader(hdr[:], 1024)
		require.NoError(t, err)
		assert.Equal(t, uint64(0), decoded.Correlation)
	})

	t.Run("zero max frame size floors to minimum", func(t *testing.T) {
		original := Frame{
			Version: ProtocolVersion,
			Type:    FrameTypePing,
			Length:  minMaxFrameSize + 1,
		}
		var hdr [FrameHeaderSize]byte
		require.NoError(t, encodeFrameHeader(hdr[:], original))
		_, err := decodeFrameHeader(hdr[:], 0)
		require.ErrorIs(t, err, ErrFrameTooLarge)
	})
}

func TestEncodeFrameHeaderBufferTooSmall(t *testing.T) {
	err := encodeFrameHeader(make([]byte, 4), Frame{
		Version: ProtocolVersion,
		Type:    FrameTypePing,
	})
	require.Error(t, err)
}

func TestChunkFlags(t *testing.T) {
	f := Frame{Flags: FrameFlagFirstChunk | FrameFlagLastChunk}
	assert.True(t, f.IsFirstChunk())
	assert.True(t, f.IsLastChunk())
}

func TestIsKnownFrameTypeAll(t *testing.T) {
	types := []byte{
		FrameTypeHello, FrameTypeHelloAck, FrameTypeData, FrameTypeReply,
		FrameTypeError, FrameTypeChunk, FrameTypeCredit, FrameTypeTable,
		FrameTypePing, FrameTypePong,
	}
	for _, typ := range types {
		assert.True(t, isKnownFrameType(typ), "type 0x%02x", typ)
	}
	assert.False(t, isKnownFrameType(0x00))
	assert.False(t, isKnownFrameType(0xFF))
}

func TestValidateCorrelationRequiredTypes(t *testing.T) {
	for _, typ := range []byte{FrameTypeReply, FrameTypeChunk} {
		err := validateCorrelation(Frame{Type: typ})
		require.Error(t, err, "type 0x%02x", typ)
		err = validateCorrelation(Frame{Type: typ, Correlation: 1})
		require.NoError(t, err, "type 0x%02x", typ)
	}

	require.Error(t, validateCorrelation(Frame{
		Type:  FrameTypeData,
		Flags: FrameFlagExpectsReply,
	}))
	require.NoError(t, validateCorrelation(Frame{Type: FrameTypeData}))
	require.NoError(t, validateCorrelation(Frame{Type: FrameTypePing}))

	// Connection-scoped ERROR frames carry correlation 0 by design;
	// request-scoped ones echo the failed request's correlation.
	require.NoError(t, validateCorrelation(Frame{Type: FrameTypeError}))
	require.NoError(t, validateCorrelation(Frame{Type: FrameTypeError, Correlation: 1}))
}

func validHeader(t *testing.T) []byte {
	t.Helper()

	original := Frame{
		Version: ProtocolVersion,
		Type:    FrameTypePing,
	}
	var hdr [FrameHeaderSize]byte
	require.NoError(t, encodeFrameHeader(hdr[:], original))
	return hdr[:]
}
