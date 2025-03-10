/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package actor

import (
	"sync/atomic"

	"github.com/tochemey/goakt/v3/internal/collection/slice"
)

// value represents the data stored in each
// node of the actors Tree
type value struct {
	_data *PID
}

// data returns the actual value value
func (v *value) data() *PID {
	return v._data
}

// treeNode represents a single actor node
// in the actors Tree
type treeNode struct {
	ID          string
	_value      atomic.Pointer[value]
	Descendants *slice.Slice[*treeNode]
	Watchers    *slice.Slice[*treeNode]
	Watchees    *slice.Slice[*treeNode]
}

// setValue sets a node value
func (x *treeNode) setValue(v *value) {
	x._value.Store(v)
}

// value returns the underlying value of the node
func (x *treeNode) value() *PID {
	v := x._value.Load()
	return v.data()
}
