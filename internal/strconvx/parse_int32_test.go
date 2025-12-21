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

package strconvx

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseInt32(t *testing.T) {
	t.Run("With valid value", func(t *testing.T) {
		got, err := ParseInt32("123")
		assert.NoError(t, err)
		assert.EqualValues(t, 123, got)
	})

	t.Run("With max int32", func(t *testing.T) {
		got, err := ParseInt32("2147483647")
		assert.NoError(t, err)
		assert.EqualValues(t, 2147483647, got)
	})

	t.Run("With min int32", func(t *testing.T) {
		got, err := ParseInt32("-2147483648")
		assert.NoError(t, err)
		assert.EqualValues(t, -2147483648, got)
	})

	t.Run("With invalid value", func(t *testing.T) {
		_, err := ParseInt32("abc")
		assert.Error(t, err)
	})

	t.Run("With out of range positive", func(t *testing.T) {
		_, err := ParseInt32("2147483648")
		assert.Error(t, err)
		assert.ErrorContains(t, err, "out of range")
	})

	t.Run("With out of range negative", func(t *testing.T) {
		_, err := ParseInt32("-2147483649")
		assert.Error(t, err)
		assert.ErrorContains(t, err, "out of range")
	})
}
