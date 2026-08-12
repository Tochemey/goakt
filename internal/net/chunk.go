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
	"encoding/binary"
	"fmt"
	"math"
)

// logicalFrameAllocSize returns the byte count of a logical frame with the
// given prefix and payload lengths. Lengths are bounded before summing so
// near-MaxInt inputs cannot wrap uint64 arithmetic; the result must fit both
// the on-wire uint32 Length field and the int size passed to make.
func logicalFrameAllocSize(prefixLen, payloadLen int) (int, error) {
	if prefixLen < 0 || payloadLen < 0 {
		return 0, fmt.Errorf("%w: negative length", ErrMessageTooLarge)
	}
	// Reject before summing: MaxInt+MaxInt wraps uint64, and body length is a
	// uint32 on the wire anyway.
	if uint64(prefixLen) > math.MaxUint32 || uint64(payloadLen) > math.MaxUint32 {
		return 0, fmt.Errorf("%w: size exceeds the 32-bit frame length limit", ErrMessageTooLarge)
	}

	total := uint64(prefixLen) + uint64(payloadLen) + uint64(FrameHeaderSize)
	if total > math.MaxUint32 || total > uint64(math.MaxInt) {
		return 0, fmt.Errorf("%w: size %d exceeds the 32-bit frame length limit", ErrMessageTooLarge, total)
	}
	return int(total), nil
}

// encodeLogicalFrame serializes f as the contiguous logical bytes carried by a
// CHUNK group: the 16-byte duplex header followed by the payload.
func encodeLogicalFrame(f Frame) ([]byte, error) {
	if f.Version == 0 {
		f.Version = ProtocolVersion
	}

	total, err := logicalFrameAllocSize(len(f.Prefix), len(f.Payload))
	if err != nil {
		return nil, err
	}

	bodyLen := total - FrameHeaderSize
	if int(f.Length) != bodyLen {
		f.Length = uint32(bodyLen)
	}

	out := make([]byte, total)
	if err := encodeFrameHeader(out[:FrameHeaderSize], f); err != nil {
		return nil, err
	}

	n := FrameHeaderSize
	if len(f.Prefix) > 0 {
		n += copy(out[n:], f.Prefix)
	}
	copy(out[n:], f.Payload)
	return out, nil
}

// decodeLogicalFrame parses a reassembled logical buffer into a [Frame].
// maxFrameSize bounds the logical header's Length field; pass the negotiated
// max message size so reassembled frames larger than MaxFrameSize are accepted.
func decodeLogicalFrame(buf []byte, maxFrameSize uint32) (Frame, error) {
	if len(buf) < FrameHeaderSize {
		return Frame{}, fmt.Errorf("tcp: logical frame truncated: need %d, got %d", FrameHeaderSize, len(buf))
	}

	frame, err := decodeFrameHeader(buf[:FrameHeaderSize], maxFrameSize)
	if err != nil {
		return Frame{}, err
	}

	if int(frame.Length) != len(buf)-FrameHeaderSize {
		return Frame{}, fmt.Errorf("tcp: logical frame length mismatch: header %d, body %d", frame.Length, len(buf)-FrameHeaderSize)
	}

	frame.Payload = buf[FrameHeaderSize:]
	return frame, nil
}

// validateChunkSplit rejects split parameters that cannot produce a valid
// CHUNK group: a chunkSize too small to hold the uvarint prefix, a zero group
// correlation, or an empty logical frame.
func validateChunkSplit(logical []byte, correlation uint64, chunkSize uint32) error {
	if chunkSize <= uint32(binary.MaxVarintLen64*2) {
		return fmt.Errorf("tcp: chunk size %d cannot hold the chunk uvarint prefix", chunkSize)
	}

	if correlation == 0 {
		return fmt.Errorf("tcp: chunk group correlation must be nonzero")
	}

	if len(logical) == 0 {
		return fmt.Errorf("tcp: empty logical frame cannot be chunked")
	}
	return nil
}

// chunkFlags returns the frame flags for one chunk: firstChunk (plus
// expectsReply when wantReply is set) on the first frame, lastChunk on the
// final frame.
func chunkFlags(first, last, wantReply bool) byte {
	flags := byte(0)

	if first {
		flags |= FrameFlagFirstChunk
		if wantReply {
			flags |= FrameFlagExpectsReply
		}
	}

	if last {
		flags |= FrameFlagLastChunk
	}
	return flags
}

// splitLogicalChunks builds the CHUNK frames for logical. Each frame's body
// (uvarint prefix plus data) is capped at chunkSize so a receiver's pooled
// ReadFrame buffer lands in the chunkSize bucket instead of the next power of
// two. correlation is stamped on every frame. expectsReply is set only on the
// first chunk when wantReply is true.
//
// Chunk data is a subslice of logical. Uvarint prefixes for every chunk share
// one arena allocation so the split copies no logical bytes per chunk.
func splitLogicalChunks(logical []byte, correlation uint64, lane byte, chunkSize uint32, wantReply bool) ([]Frame, error) {
	if err := validateChunkSplit(logical, correlation, chunkSize); err != nil {
		return nil, err
	}

	total := uint64(len(logical))
	n := len(logical)/(int(chunkSize)-binary.MaxVarintLen64*2) + 1
	frames := make([]Frame, 0, n)

	// One arena for all chunk uvarint prefixes (index + optional totalSize).
	prefixArena := make([]byte, 0, (n+1)*binary.MaxVarintLen64*2)

	offset := 0
	for index := uint64(0); offset < len(logical); index++ {
		first := index == 0

		prefixStart := len(prefixArena)
		prefixArena = binary.AppendUvarint(prefixArena, index)
		if first {
			prefixArena = binary.AppendUvarint(prefixArena, total)
		}
		prefix := prefixArena[prefixStart:]

		end := min(offset+int(chunkSize)-len(prefix), len(logical))

		data := logical[offset:end]
		last := end == len(logical)

		frames = append(frames, Frame{
			Version:     ProtocolVersion,
			Type:        FrameTypeChunk,
			Flags:       chunkFlags(first, last, wantReply),
			Lane:        lane,
			Length:      uint32(len(prefix) + len(data)),
			Correlation: correlation,
			Prefix:      prefix,
			Payload:     data,
		})

		offset = end
	}

	return frames, nil
}

// encodeChunkPayload writes uvarint index, optional totalLogicalSize when first
// is set, then data into one contiguous buffer. Used by tests and abort frames;
// the hot send path uses [splitLogicalChunks] instead.
func encodeChunkPayload(index, totalLogicalSize uint64, first bool, data []byte) []byte {
	var scratch [binary.MaxVarintLen64 * 2]byte
	n := binary.PutUvarint(scratch[:], index)
	if first {
		n += binary.PutUvarint(scratch[n:], totalLogicalSize)
	}

	out := make([]byte, n+len(data))
	copy(out, scratch[:n])
	copy(out[n:], data)
	return out
}

// parseChunkPayload decodes a CHUNK payload. When first is set, totalLogicalSize
// is read after the index; otherwise totalLogicalSize is zero.
func parseChunkPayload(payload []byte, first bool) (index, totalLogicalSize uint64, data []byte, err error) {
	index, n := binary.Uvarint(payload)
	if n <= 0 {
		return 0, 0, nil, fmt.Errorf("tcp: chunk index uvarint truncated")
	}

	rest := payload[n:]
	if first {
		totalLogicalSize, n = binary.Uvarint(rest)
		if n <= 0 {
			return 0, 0, nil, fmt.Errorf("tcp: chunk total size uvarint truncated")
		}
		rest = rest[n:]
	}
	return index, totalLogicalSize, rest, nil
}

// abortChunkFrame builds an empty lastChunk used to free a partial reassembly
// group after a mid-sequence Submit failure.
func abortChunkFrame(correlation uint64, lane byte, index uint64) Frame {
	payload := encodeChunkPayload(index, 0, false, nil)
	return Frame{
		Version:     ProtocolVersion,
		Type:        FrameTypeChunk,
		Flags:       FrameFlagLastChunk,
		Lane:        lane,
		Length:      uint32(len(payload)),
		Correlation: correlation,
		Payload:     payload,
	}
}

// chunkWireBody returns the contiguous CHUNK body for in-process reassembly
// tests that bypass the wire (Prefix plus Payload).
func chunkWireBody(f Frame) []byte {
	if len(f.Prefix) == 0 {
		return f.Payload
	}

	out := make([]byte, len(f.Prefix)+len(f.Payload))
	copy(out, f.Prefix)
	copy(out[len(f.Prefix):], f.Payload)
	return out
}
