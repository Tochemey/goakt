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
	"errors"
	"fmt"
	"sync"
)

// maxTableLiteralLen bounds a TABLE literal (an actor path or type name) so the
// payload size computation in encodeTablePayload cannot overflow int on any
// platform (CWE-190). Real literals are far smaller; a literal beyond this is
// rejected as malformed rather than allocated.
const maxTableLiteralLen = 16 << 20 // 16 MiB

// encodeTablePayload builds a TABLE frame body:
// kind (1B) | uvarint id | uvarint literalLen | literal bytes.
// It rejects a literal longer than maxTableLiteralLen so the size computation
// for the returned buffer cannot overflow int.
func encodeTablePayload(kind byte, id uint64, literal string) ([]byte, error) {
	if len(literal) > maxTableLiteralLen {
		return nil, fmt.Errorf("tcp: table literal length %d exceeds max %d", len(literal), maxTableLiteralLen)
	}

	size := 1 + uvarintSize(id) + uvarintSize(uint64(len(literal))) + len(literal)
	buf := make([]byte, size)
	buf[0] = kind
	n := 1
	n += binary.PutUvarint(buf[n:], id)
	n += binary.PutUvarint(buf[n:], uint64(len(literal)))
	copy(buf[n:], literal)
	return buf, nil
}

// parseTablePayload decodes a TABLE frame body. Empty literals and zero IDs
// are rejected; unknown kinds are left for the caller to classify.
func parseTablePayload(src []byte) (kind byte, id uint64, literal string, err error) {
	if len(src) < 1 {
		return 0, 0, "", fmt.Errorf("tcp: table payload truncated: missing kind")
	}

	kind = src[0]
	rest := src[1:]

	var n int
	id, n = binary.Uvarint(rest)
	if n <= 0 {
		return 0, 0, "", fmt.Errorf("tcp: table payload truncated: missing id")
	}

	if id == 0 {
		return 0, 0, "", fmt.Errorf("tcp: table id must be nonzero")
	}

	rest = rest[n:]

	litLen, m := binary.Uvarint(rest)
	if m <= 0 {
		return 0, 0, "", fmt.Errorf("tcp: table payload truncated: missing literal length")
	}

	rest = rest[m:]
	if litLen == 0 {
		return 0, 0, "", fmt.Errorf("tcp: table literal must be nonempty")
	}

	if litLen > uint64(len(rest)) {
		return 0, 0, "", fmt.Errorf("tcp: table literal length %d exceeds remaining %d", litLen, len(rest))
	}

	if uint64(len(rest)) != litLen {
		return 0, 0, "", fmt.Errorf("tcp: table payload has %d trailing bytes", uint64(len(rest))-litLen)
	}

	return kind, id, string(rest[:litLen]), nil
}

// senderTable assigns outbound compression-table IDs for one kind on one
// connection. IDs start at 1; 0 means "encode inline".
type senderTable struct {
	mu sync.Mutex
	// nextID is the next ID to assign; it advances even when emit fails so
	// rolled-back registrations do not reuse IDs.
	nextID uint64
	// byLiteral maps a registered literal to its assigned ID.
	byLiteral map[string]uint64
	// capacity is the maximum number of live entries (DefaultTableCapacity).
	capacity int
}

// newSenderTable constructs an empty sender table with the given capacity.
func newSenderTable(capacity int) *senderTable {
	if capacity <= 0 {
		capacity = DefaultTableCapacity
	}

	return &senderTable{
		nextID:    1,
		byLiteral: make(map[string]uint64),
		capacity:  capacity,
	}
}

// register looks up or assigns an ID for literal. The emit callback runs under
// the table mutex for a fresh assignment. It must not wait on byte capacity or
// writer-queue backpressure (it may briefly wait on another mutex, as long as
// lock order is always this table mutex first). A callback error rolls back
// the mapping (nextID stays monotonic); [ErrDuplexBackpressure] additionally
// degrades to (0, nil) so a transiently full writer costs one inline ref, not
// the message being encoded, and a later message retries the registration.
// Empty literals and a full table return (0, nil) meaning the caller should
// encode an inline ref.
func (x *senderTable) register(literal string, emit func(id uint64) error) (uint64, error) {
	if literal == "" {
		return 0, nil
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	if id, ok := x.byLiteral[literal]; ok {
		return id, nil
	}

	if len(x.byLiteral) >= x.capacity {
		return 0, nil
	}

	id := x.nextID
	x.nextID++
	x.byLiteral[literal] = id

	if err := emit(id); err != nil {
		delete(x.byLiteral, literal)

		if errors.Is(err, ErrDuplexBackpressure) {
			return 0, nil
		}

		return 0, err
	}

	return id, nil
}

// receiverTableEntry holds one inbound TABLE registration.
type receiverTableEntry struct {
	// literal is the registered actor path or type name.
	literal string
	// handle is an opaque actor-layer cache filled lazily on the first
	// sender-position DATA hit; nil until then.
	handle any
}

// receiverTable resolves inbound table IDs for one kind on one connection.
type receiverTable struct {
	mu sync.Mutex
	// byID maps a peer-assigned table ID to its entry.
	byID map[uint64]*receiverTableEntry
	// capacity is the maximum number of live entries (DefaultTableCapacity).
	capacity int
}

// newReceiverTable constructs an empty receiver table with the given capacity.
func newReceiverTable(capacity int) *receiverTable {
	if capacity <= 0 {
		capacity = DefaultTableCapacity
	}

	return &receiverTable{
		byID:     make(map[uint64]*receiverTableEntry),
		capacity: capacity,
	}
}

// installResult is the outcome of installing one inbound TABLE entry.
type installResult struct {
	// SoftIgnore reports an idempotent re-install of the same id/literal.
	SoftIgnore bool
	// HardError is a protocol-violation message; the caller must close.
	HardError string
}

// install records id → literal. Duplicate same-literal installs are ignored;
// capacity overflow, empty literals, and conflicting duplicates are hard errors.
func (x *receiverTable) install(id uint64, literal string) installResult {
	if id == 0 {
		return installResult{HardError: "table id must be nonzero"}
	}

	if literal == "" {
		return installResult{HardError: "table literal must be nonempty"}
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	if existing, ok := x.byID[id]; ok {
		if existing.literal == literal {
			return installResult{SoftIgnore: true}
		}

		return installResult{HardError: fmt.Sprintf("table id %d literal conflict", id)}
	}

	if len(x.byID) >= x.capacity {
		return installResult{HardError: "table capacity exceeded"}
	}

	x.byID[id] = &receiverTableEntry{literal: literal}
	return installResult{}
}

// lookup returns the entry for id, or nil when unknown.
func (x *receiverTable) lookup(id uint64) *receiverTableEntry {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.byID[id]
}

// resolveRef returns the literal for id and, when resolve is non-nil, the
// lazily filled opaque handle. resolve runs outside the table mutex so PID
// materialization cannot stall concurrent lookups. ok is false when id is
// unknown. Concurrent first hits may invoke resolve more than once; only one
// handle is stored.
func (x *receiverTable) resolveRef(id uint64, resolve func(literal string) any) (literal string, handle any, ok bool) {
	x.mu.Lock()
	entry, found := x.byID[id]
	if !found {
		x.mu.Unlock()
		return "", nil, false
	}

	literal = entry.literal
	handle = entry.handle
	x.mu.Unlock()

	if handle != nil || resolve == nil {
		return literal, handle, true
	}

	resolved := resolve(literal)

	x.mu.Lock()
	if entry.handle == nil {
		entry.handle = resolved
	}
	handle = entry.handle
	x.mu.Unlock()

	return literal, handle, true
}

// knownTableKind reports whether kind is a defined TABLE kind byte.
func knownTableKind(kind byte) bool {
	switch kind {
	case TableKindActorPath, TableKindTypeName:
		return true
	default:
		return false
	}
}
