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

package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTCPAddressValidator(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		addr := "127.0.0.1:3222"
		assert.NoError(t, NewTCPAddressValidator(addr).Validate())
	})
	t.Run("With invalid port number: case 1", func(t *testing.T) {
		addr := "127.0.0.1:-1"
		assert.Error(t, NewTCPAddressValidator(addr).Validate())
	})
	t.Run("With invalid port number: case 2", func(t *testing.T) {
		addr := "127.0.0.1:655387"
		assert.Error(t, NewTCPAddressValidator(addr).Validate())
	})
	t.Run("With  zero port number: case 3", func(t *testing.T) {
		addr := "127.0.0.1:0"
		assert.NoError(t, NewTCPAddressValidator(addr).Validate())
	})
	t.Run("With invalid host", func(t *testing.T) {
		addr := ":3222"
		assert.Error(t, NewTCPAddressValidator(addr).Validate())
	})
}
