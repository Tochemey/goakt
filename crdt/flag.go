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

// ensure Flag implements ReplicatedData at compile time.
var _ ReplicatedData = (*Flag)(nil)

// Flag is a boolean CRDT that can only transition from false to true.
//
// Once enabled, it cannot be disabled. The merge function is a logical OR:
// if either replica has enabled the flag, the merged result is enabled.
// This makes Flag useful for one-time coordination signals, feature
// activation, or irreversible state transitions.
type Flag struct {
	enabled bool
	dirty   bool
}

// NewFlag creates a new Flag with a false (disabled) value.
func NewFlag() *Flag {
	return &Flag{}
}

// Enable sets the flag to true.
// Returns a new Flag with the updated state.
func (x *Flag) Enable() *Flag {
	if x.enabled {
		return x.Clone().(*Flag)
	}
	return &Flag{
		enabled: true,
		dirty:   true,
	}
}

// Enabled returns whether the flag is set to true.
func (x *Flag) Enabled() bool {
	return x.enabled
}

// Merge combines this Flag with another using logical OR.
// Both inputs are left unchanged.
func (x *Flag) Merge(other ReplicatedData) ReplicatedData {
	o, ok := other.(*Flag)
	if !ok {
		return x
	}
	return &Flag{
		enabled: x.enabled || o.enabled,
	}
}

// Delta returns the flag state if it has changed since the last ResetDelta.
// Returns nil if there are no changes.
func (x *Flag) Delta() ReplicatedData {
	if !x.dirty {
		return nil
	}
	return x.Clone()
}

// ResetDelta clears the dirty flag.
func (x *Flag) ResetDelta() {
	x.dirty = false
}

// FlagFromState creates a Flag from a serialized enabled state.
func FlagFromState(enabled bool) *Flag {
	return &Flag{enabled: enabled}
}

// Clone returns a deep copy of the Flag.
func (x *Flag) Clone() ReplicatedData {
	return &Flag{
		enabled: x.enabled,
		dirty:   x.dirty,
	}
}
