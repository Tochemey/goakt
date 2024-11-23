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

package slice

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSlice(t *testing.T) {
	// create a concurrent slice of integer
	sl := NewLockFree[int]()

	// add some items
	sl.Append(2)
	sl.Append(4)
	sl.Append(5)

	// assert the length
	assert.EqualValues(t, 3, sl.Len())
	assert.NotEmpty(t, sl.Items())
	assert.Len(t, sl.Items(), 3)
	// get the element at index 2
	assert.EqualValues(t, 5, sl.Get(2))
	// remove the element at index 1
	sl.Delete(1)
	// assert the length
	assert.EqualValues(t, 2, sl.Len())
	assert.Zero(t, sl.Get(4))
	sl.Reset()
	assert.Zero(t, sl.Len())
}
