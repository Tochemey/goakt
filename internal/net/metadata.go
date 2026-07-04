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
)

type metadataKey struct{}

// Metadata carries context information (headers, deadline) across the wire.
// The internal representation stores the deadline as int64 (UnixNano) to
// avoid GC scanning of time.Time values. On the wire the deadline travels as
// the remaining time (nanoseconds) rather than an absolute timestamp, so the
// receiver re-derives it on its own clock and enforcement never depends on
// cross-node clock synchronization.
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

// IterateHeaders calls the provided function for each header key-value pair.
// This enables converting Metadata to other header formats (e.g., http.Header)
// without exposing the internal map structure.
func (m *Metadata) IterateHeaders(fn func(key, value string)) {
	for k, v := range m.headers {
		fn(k, v)
	}
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
// Format: [2-byte count][for each: 2-byte key len, key, 2-byte val len, val][8-byte remaining time]
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

	// Write the deadline as remaining time in nanoseconds (0 if not set) so
	// the receiver derives the absolute deadline on its own clock. A negative
	// value means the deadline already passed; an exactly-zero remainder is
	// nudged to -1 so it is not mistaken for "no deadline".
	var remaining int64

	if m.deadlineNano != 0 {
		remaining = m.deadlineNano - time.Now().UnixNano()
		if remaining == 0 {
			remaining = -1
		}
	}

	binary.BigEndian.PutUint64(buf[pos:], uint64(remaining))

	return buf
}

// UnmarshalBinary decodes metadata from bytes. Header keys and values are
// copied into independent strings so the [Metadata] remains valid after the
// input slice is reused or mutated — in particular, callers are free to
// return pooled frame buffers immediately after this call.
func (m *Metadata) UnmarshalBinary(data []byte) error {
	if len(data) < 10 {
		return ErrInvalidMetadata
	}

	pos := 0

	// Read count and pre-size map to avoid rehashing.
	count := int(binary.BigEndian.Uint16(data[pos:]))
	pos += 2
	m.headers = make(map[string]string, count)

	for range count {
		if pos+2 > len(data) {
			return ErrInvalidMetadata
		}
		keyLen := int(binary.BigEndian.Uint16(data[pos:]))
		pos += 2

		if pos+keyLen > len(data) {
			return ErrInvalidMetadata
		}
		key := string(data[pos : pos+keyLen])
		pos += keyLen

		if pos+2 > len(data) {
			return ErrInvalidMetadata
		}
		valLen := int(binary.BigEndian.Uint16(data[pos:]))
		pos += 2

		if pos+valLen > len(data) {
			return ErrInvalidMetadata
		}
		val := string(data[pos : pos+valLen])
		pos += valLen

		m.headers[key] = val
	}

	// Read the remaining time and rebase it onto this node's clock (see
	// MarshalBinary for the wire semantics).
	if pos+8 > len(data) {
		return ErrInvalidMetadata
	}

	remaining := int64(binary.BigEndian.Uint64(data[pos:]))
	if remaining != 0 {
		m.deadlineNano = time.Now().UnixNano() + remaining
	} else {
		m.deadlineNano = 0
	}

	return nil
}

// ToContext creates a context with this metadata attached as a value. It
// deliberately does not create a deadline context: doing so per message
// starts a runtime timer whose cancel function nobody on the generic
// transport path can safely call (tell-style messages outlive the handler).
// Consumers that need the propagated deadline enforced derive it in a scope
// they own via [Metadata.DeadlineContext].
func (m *Metadata) ToContext(parent context.Context) context.Context {
	return context.WithValue(parent, metadataKey{}, m)
}

// DeadlineContext returns a context bounded by this metadata's deadline and
// the cancel function that releases its timer. The caller must call cancel
// once the bounded work completes. When no deadline is set, parent is
// returned with a no-op cancel.
func (m *Metadata) DeadlineContext(parent context.Context) (context.Context, context.CancelFunc) {
	if m.deadlineNano == 0 {
		return parent, func() {}
	}

	return context.WithDeadline(parent, time.Unix(0, m.deadlineNano))
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
