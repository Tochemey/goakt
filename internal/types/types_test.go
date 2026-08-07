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

package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsBlank verifies whitespace-only string detection.
func TestIsBlank(t *testing.T) {
	assert.True(t, IsBlank(""))
	assert.True(t, IsBlank(" \t\n"))
	assert.False(t, IsBlank(" value "))
}

// TestIsNil verifies nil interfaces and typed nil values.
func TestIsNil(t *testing.T) {
	var pointer *int
	var channel chan int
	var function func()
	var mapping map[string]int
	var slice []int

	assert.True(t, IsNil(nil))
	assert.True(t, IsNil(pointer))
	assert.True(t, IsNil(channel))
	assert.True(t, IsNil(function))
	assert.True(t, IsNil(mapping))
	assert.True(t, IsNil(slice))
	assert.False(t, IsNil(0))
	assert.False(t, IsNil(new(int)))
	assert.False(t, IsNil(make(chan int)))
	assert.False(t, IsNil(func() {}))
	assert.False(t, IsNil(map[string]int{}))
	assert.False(t, IsNil([]int{}))
}
