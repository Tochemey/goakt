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
)

// encodeLogicalFrame serializes f as the contiguous logical bytes carried by a
// CHUNK group: the 16-byte duplex header followed by the payload.
func encodeLogicalFrame(f Frame) ([]byte, error) {
	if f.Version == 0 {
		f.Version = ProtocolVersion
	}

	if int(f.Length) != f.bodyLen() {
		f.Length = uint32(f.bodyLen())
	}

	out := make([]byte, FrameHeaderSize+f.bodyLen())
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

// splitLogicalChunks builds the CHUNK frames for logical. Each frame's body
// (uvarint prefix plus data) is capped at chunkSize so a receiver's pooled
// ReadFrame buffer lands in the chunkSize bucket instead of the next power of
// two. correlation is stamped on every frame. expectsReply is set only on the
// first chunk when wantReply is true.
//
// Chunk data is a subslice of logical. Uvarint prefixes for every chunk share
// one arena allocation so the split copies no logical bytes per chunk.
func splitLogicalChunks(logical []byte, correlation uint64, lane byte, chunkSize uint32, wantReply bool) ([]Frame, error) {
	if chunkSize <= uint32(binary.MaxVarintLen64*2) {
		return nil, fmt.Errorf("tcp: chunk size %d cannot hold the chunk uvarint prefix", chunkSize)
	}

	if correlation == 0 {
		return nil, fmt.Errorf("tcp: chunk group correlation must be nonzero")
	}

	if len(logical) == 0 {
		return nil, fmt.Errorf("tcp: empty logical frame cannot be chunked")
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

		end := offset + int(chunkSize) - len(prefix)
		if end > len(logical) {
			end = len(logical)
		}

		data := logical[offset:end]
		last := end == len(logical)

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

		frames = append(frames, Frame{
			Version:     ProtocolVersion,
			Type:        FrameTypeChunk,
			Flags:       flags,
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
