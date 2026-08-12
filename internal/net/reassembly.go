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
	"fmt"
	"math"
	"sync"
)

// chunkReassembler rebuilds logical frames from CHUNK frames on one duplex
// connection. Soft rejects (oversize / concurrent cap) leave the connection
// usable; index protocol violations are hard errors for the caller to close.
//
// The reassembly buffer is one exact-size allocation at any size, deliberately
// unpooled: ownership passes to dispatch with no deterministic release point
// on the receive path today. Per-CHUNK ReadFrame buffers are pooled separately
// and released after the copy into this buffer.
type chunkReassembler struct {
	mu sync.Mutex
	// groups maps chunk-group correlation to the in-progress buffer.
	groups map[uint64]*chunkGroup
	// maxMessageSize is the negotiated ceiling applied before allocation.
	maxMessageSize uint64
	// maxConcurrent caps how many groups may be open at once.
	maxConcurrent uint32
}

// chunkGroup holds one in-progress logical frame.
type chunkGroup struct {
	// buf is the exact-size reassembly buffer declared by firstChunk.
	buf []byte
	// written is how many payload bytes have been copied into buf.
	written uint64
	// nextIndex is the next contiguous chunk index expected.
	nextIndex uint64
	// total is the declared logical size from firstChunk.
	total uint64
}

// reassemblyOutcome is the result of feeding one CHUNK into the reassembler.
type reassemblyOutcome struct {
	// Frame is set when a logical frame completed successfully.
	Frame Frame
	// Complete reports whether Frame is valid.
	Complete bool
	// SoftReject is a request-scoped error message; the connection stays up.
	SoftReject string
	// HardError is a protocol-violation message; the caller must close.
	HardError string
	// Aborted reports a short lastChunk that freed a partial group.
	Aborted bool
}

// newChunkReassembler constructs a reassembler with the negotiated caps.
func newChunkReassembler(maxMessageSize uint64, maxConcurrent uint32) *chunkReassembler {
	if maxConcurrent == 0 {
		maxConcurrent = defaultMaxConcurrentLargeTransfers
	}

	if maxMessageSize == 0 {
		maxMessageSize = DefaultMaxMessageSize
	}

	return &chunkReassembler{
		groups:         make(map[uint64]*chunkGroup),
		maxMessageSize: maxMessageSize,
		maxConcurrent:  maxConcurrent,
	}
}

// Push consumes one CHUNK frame. Non-first chunks for an unknown correlation
// are ignored so trailing frames after a soft reject or abort do not kill the
// connection.
func (x *chunkReassembler) Push(frame Frame) reassemblyOutcome {
	if frame.Type != FrameTypeChunk {
		return reassemblyOutcome{HardError: "non-chunk frame offered to reassembler"}
	}

	if frame.Correlation == 0 {
		return reassemblyOutcome{HardError: "chunk frame missing correlation"}
	}

	index, total, data, err := parseChunkPayload(chunkWireBody(frame), frame.IsFirstChunk())
	if err != nil {
		return reassemblyOutcome{HardError: err.Error()}
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	group, exists := x.groups[frame.Correlation]

	if frame.IsFirstChunk() {
		if exists {
			return reassemblyOutcome{HardError: "duplicate first chunk for correlation"}
		}

		if total == 0 {
			return reassemblyOutcome{HardError: "chunk totalLogicalSize must be positive"}
		}

		if total > x.maxMessageSize {
			return reassemblyOutcome{SoftReject: fmt.Sprintf("logical message size %d exceeds max %d", total, x.maxMessageSize)}
		}

		if uint32(len(x.groups)) >= x.maxConcurrent {
			return reassemblyOutcome{SoftReject: "max concurrent large transfers exceeded"}
		}

		if index != 0 {
			return reassemblyOutcome{HardError: "first chunk index must be zero"}
		}

		// Exact-size buffer; unpooled by design (see type comment).
		group = &chunkGroup{
			buf:       make([]byte, total),
			nextIndex: 1,
			total:     total,
		}
		if err := group.append(data); err != nil {
			return reassemblyOutcome{HardError: err.Error()}
		}

		x.groups[frame.Correlation] = group

		if frame.IsLastChunk() {
			return x.finishLocked(frame.Correlation, group)
		}

		return reassemblyOutcome{}
	}

	if !exists {
		// Orphan continuation or abort after soft reject or a lost first chunk.
		return reassemblyOutcome{}
	}

	if index != group.nextIndex {
		delete(x.groups, frame.Correlation)
		return reassemblyOutcome{HardError: fmt.Sprintf("chunk index %d, want %d", index, group.nextIndex)}
	}

	if err := group.append(data); err != nil {
		delete(x.groups, frame.Correlation)
		return reassemblyOutcome{HardError: err.Error()}
	}

	group.nextIndex++

	if !frame.IsLastChunk() {
		return reassemblyOutcome{}
	}

	return x.finishLocked(frame.Correlation, group)
}

// finishLocked completes or aborts a group that just received lastChunk.
func (x *chunkReassembler) finishLocked(corr uint64, group *chunkGroup) reassemblyOutcome {
	delete(x.groups, corr)

	if group.written < group.total {
		return reassemblyOutcome{Aborted: true}
	}

	if group.written > group.total {
		return reassemblyOutcome{HardError: "chunk group exceeded declared total"}
	}

	bound := uint32(group.total)
	if group.total > math.MaxUint32 {
		bound = math.MaxUint32
	}

	logical, err := decodeLogicalFrame(group.buf, bound)
	if err != nil {
		return reassemblyOutcome{HardError: err.Error()}
	}

	return reassemblyOutcome{Frame: logical, Complete: true}
}

// Close discards every partial group.
func (x *chunkReassembler) Close() {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.groups = make(map[uint64]*chunkGroup)
}

// append copies data into the group buffer at the current write offset.
func (x *chunkGroup) append(data []byte) error {
	if x.written+uint64(len(data)) > x.total {
		return fmt.Errorf("chunk data exceeds declared total")
	}

	copy(x.buf[x.written:], data)
	x.written += uint64(len(data))
	return nil
}
