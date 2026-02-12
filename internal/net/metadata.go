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
	"context"
	"encoding/binary"
	"time"
	"unsafe"
)

type metadataKey struct{}

// Metadata carries context information (headers, deadline) across the wire.
// The internal representation stores the deadline as int64 (UnixNano) to
// avoid GC scanning of time.Time values.
type Metadata struct {
	headers      map[string]string
	deadlineNano int64 // UnixNano; 0 means no deadline
}

// NewMetadata creates an empty [Metadata] instance.
func NewMetadata() *Metadata {
	return &Metadata{
		headers: make(map[string]string),
	}
}

// Set adds or updates a metadata header.
func (m *Metadata) Set(key, value string) {
	m.headers[key] = value
}

// Get retrieves a metadata header.
func (m *Metadata) Get(key string) (string, bool) {
	v, ok := m.headers[key]
	return v, ok
}

// SetDeadline sets the deadline for this request.
func (m *Metadata) SetDeadline(d time.Time) {
	if d.IsZero() {
		m.deadlineNano = 0
	} else {
		m.deadlineNano = d.UnixNano()
	}
}

// GetDeadline returns the deadline if set.
func (m *Metadata) GetDeadline() (time.Time, bool) {
	if m.deadlineNano == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, m.deadlineNano), true
}

// MarshalBinary encodes metadata to bytes using a single allocation by pre-computing
// the exact output size. The returned byte slice should not be modified by
// the caller.
//
// Format: [2-byte count][for each: 2-byte key len, key, 2-byte val len, val][8-byte deadline]
func (m *Metadata) MarshalBinary() []byte {
	// Pre-compute exact size.
	size := 10 // 2 (count) + 8 (deadline)
	for k, v := range m.headers {
		size += 4 + len(k) + len(v) // 2 + len(k) + 2 + len(v)
	}

	// Single allocation.
	buf := make([]byte, size)
	pos := 0

	// Write count.
	binary.BigEndian.PutUint16(buf[pos:], uint16(len(m.headers)))
	pos += 2

	// Write headers.
	for k, v := range m.headers {
		binary.BigEndian.PutUint16(buf[pos:], uint16(len(k)))
		pos += 2
		pos += copy(buf[pos:], k)

		binary.BigEndian.PutUint16(buf[pos:], uint16(len(v)))
		pos += 2
		pos += copy(buf[pos:], v)
	}

	// Write deadline (UnixNano, 0 if not set).
	binary.BigEndian.PutUint64(buf[pos:], uint64(m.deadlineNano))

	return buf
}

// UnmarshalBinary decodes metadata from bytes. It uses zero-copy string extraction
// via unsafe pointers, so the returned header keys and values reference the
// input data slice directly. The caller must not modify data after calling
// UnmarshalBinary if the Metadata will continue to be used.
//
// This optimization avoids allocating N×2 strings (one per key, one per value)
// at the cost of retaining the data slice in memory as long as the Metadata
// or any extracted header strings remain reachable.
func (m *Metadata) UnmarshalBinary(data []byte) error {
	if len(data) < 10 {
		return ErrInvalidMetadata
	}

	pos := 0

	// Read count and pre-size map to avoid rehashing.
	count := int(binary.BigEndian.Uint16(data[pos:]))
	pos += 2
	m.headers = make(map[string]string, count)

	// Read headers using zero-copy string extraction.
	for range count {
		if pos+2 > len(data) {
			return ErrInvalidMetadata
		}
		keyLen := int(binary.BigEndian.Uint16(data[pos:]))
		pos += 2

		if pos+keyLen > len(data) {
			return ErrInvalidMetadata
		}
		// Zero-copy: string references data slice directly.
		key := unsafe.String(unsafe.SliceData(data[pos:pos+keyLen]), keyLen)
		pos += keyLen

		if pos+2 > len(data) {
			return ErrInvalidMetadata
		}
		valLen := int(binary.BigEndian.Uint16(data[pos:]))
		pos += 2

		if pos+valLen > len(data) {
			return ErrInvalidMetadata
		}
		// Zero-copy: string references data slice directly.
		val := unsafe.String(unsafe.SliceData(data[pos:pos+valLen]), valLen)
		pos += valLen

		m.headers[key] = val
	}

	// Read deadline.
	if pos+8 > len(data) {
		return ErrInvalidMetadata
	}
	m.deadlineNano = int64(binary.BigEndian.Uint64(data[pos:]))

	return nil
}

// ToContext creates a context with this metadata attached. If a deadline
// is set, it is also applied to the returned context.
//
// Note: The cancel function from WithDeadline is intentionally not called here
// because the returned context is transferred to the caller, who becomes
// responsible for cancellation. Calling cancel here would prematurely cancel
// the context before the caller can use it.
func (m *Metadata) ToContext(parent context.Context) context.Context {
	ctx := parent
	if m.deadlineNano > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, time.Unix(0, m.deadlineNano))
		// Cancel function intentionally not called — caller owns the returned context.
		_ = cancel
	}
	return context.WithValue(ctx, metadataKey{}, m)
}

// FromContext extracts metadata from a context.
func FromContext(ctx context.Context) (*Metadata, bool) {
	m, ok := ctx.Value(metadataKey{}).(*Metadata)
	return m, ok
}

// ContextWithMetadata attaches metadata to a context.
func ContextWithMetadata(ctx context.Context, md *Metadata) context.Context {
	return context.WithValue(ctx, metadataKey{}, md)
}
