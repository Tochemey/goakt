/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actors

import (
	"sync/atomic"

	"github.com/tochemey/goakt/v2/internal/slice"
)

// pidValue represents the data stored in each
// node of the actors Tree
type pidValue struct {
	data *PID
}

// newPidValue creates an instance of pidValue
func newPidValue(data *PID) *pidValue {
	return &pidValue{data: data}
}

// Value returns the actual pidValue value
func (v *pidValue) Value() *PID {
	return v.data
}

// pidNode represents a single actor node
// in the actors Tree
type pidNode struct {
	ID          string
	value       atomic.Pointer[pidValue]
	Descendants *slice.ThreadSafe[*pidNode]
	Watchers    *slice.ThreadSafe[*pidNode]
	Watchees    *slice.ThreadSafe[*pidNode]
}

// SetValue sets a node value
func (x *pidNode) SetValue(v *pidValue) {
	x.value.Store(v)
}

// GetValue returns the underlying value of the node
func (x *pidNode) GetValue() *PID {
	v := x.value.Load()
	return v.Value()
}
