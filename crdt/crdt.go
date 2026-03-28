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

// ReplicatedData is the base interface for all CRDT types.
//
// Every CRDT in the system must implement this interface. Implementations
// must be safe to merge concurrently from multiple nodes — the Merge
// function must be commutative, associative, and idempotent.
//
// CRDT types are treated as immutable values: every mutation returns a
// new value. The Delta/ResetDelta pair enables delta-based replication,
// where only the changes since the last synchronization point are
// transmitted rather than the full state.
type ReplicatedData interface {
	// Merge combines this CRDT with a remote replica's state.
	// Returns the merged result. Both inputs are left unchanged.
	// The merge function must be commutative, associative, and idempotent.
	Merge(other ReplicatedData) ReplicatedData

	// Delta returns the state changes since the last call to ResetDelta.
	// Returns nil if there are no changes.
	Delta() ReplicatedData

	// ResetDelta clears the accumulated delta state.
	ResetDelta()

	// Clone returns a deep copy of the CRDT.
	Clone() ReplicatedData
}
