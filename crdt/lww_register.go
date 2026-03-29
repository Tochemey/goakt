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

package crdt

import "time"

// ensure LWWRegister implements ReplicatedData at compile time.
var _ ReplicatedData = (*LWWRegister)(nil)

// LWWRegister is a last-writer-wins register CRDT.
//
// It stores a single value with a timestamp. Concurrent writes are
// resolved by taking the value with the highest timestamp. If timestamps
// are equal, the write from the node with the lexicographically higher
// node ID wins (deterministic tiebreaker).
type LWWRegister struct {
	value     any
	timestamp int64
	nodeID    string
	dirty     bool
}

// NewLWWRegister creates a new LWWRegister with a zero value.
func NewLWWRegister() *LWWRegister {
	return &LWWRegister{}
}

// Set updates the register value with the given timestamp and node ID.
// Returns a new LWWRegister with the updated state.
func (r *LWWRegister) Set(value any, timestamp time.Time, nodeID string) *LWWRegister {
	return &LWWRegister{
		value:     value,
		timestamp: timestamp.UnixNano(),
		nodeID:    nodeID,
		dirty:     true,
	}
}

// Value returns the current register value.
func (r *LWWRegister) Value() any {
	return r.value
}

// Timestamp returns the timestamp of the current value as nanoseconds since epoch.
func (r *LWWRegister) Timestamp() int64 {
	return r.timestamp
}

// NodeID returns the node ID that last wrote this value.
func (r *LWWRegister) NodeID() string {
	return r.nodeID
}

// Merge combines this register with another by taking the value with the
// highest timestamp. If timestamps are equal, the node with the
// lexicographically higher node ID wins.
// Both inputs are left unchanged.
func (r *LWWRegister) Merge(other ReplicatedData) ReplicatedData {
	o, ok := other.(*LWWRegister)
	if !ok {
		return r
	}

	winner := r
	if o.timestamp > r.timestamp || (o.timestamp == r.timestamp && o.nodeID > r.nodeID) {
		winner = o
	}
	return &LWWRegister{
		value:     winner.value,
		timestamp: winner.timestamp,
		nodeID:    winner.nodeID,
	}
}

// Delta returns the register state if it has changed since the last ResetDelta.
// Returns nil if there are no changes.
func (r *LWWRegister) Delta() ReplicatedData {
	if !r.dirty {
		return nil
	}
	return r.Clone()
}

// ResetDelta clears the dirty flag.
func (r *LWWRegister) ResetDelta() {
	r.dirty = false
}

// Clone returns a deep copy of the register.
func (r *LWWRegister) Clone() ReplicatedData {
	return &LWWRegister{
		value:     r.value,
		timestamp: r.timestamp,
		nodeID:    r.nodeID,
		dirty:     r.dirty,
	}
}

// LWWRegisterFromState creates an LWWRegister from serialized state components.
func LWWRegisterFromState(value any, timestampNanos int64, nodeID string) *LWWRegister {
	return &LWWRegister{
		value:     value,
		timestamp: timestampNanos,
		nodeID:    nodeID,
	}
}
