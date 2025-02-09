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

package tcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetHostPort(t *testing.T) {
	t.Run("Happy Path", func(t *testing.T) {
		address := "localhost:8080"
		host, port, err := GetHostPort(address)
		require.NoError(t, err)
		assert.Equal(t, "127.0.0.1", host)
		assert.Exactly(t, 8080, port)
	})
	t.Run("Invalid address", func(t *testing.T) {
		_, _, err := GetHostPort("127.0.0.1.1.1.1.1.2:8080")
		require.Error(t, err)
	})
}

func TestGetBindIP(t *testing.T) {
	t.Run("Happy Path", func(t *testing.T) {
		address := "localhost:8080"
		bindIP, err := GetBindIP(address)
		require.NoError(t, err)
		assert.Equal(t, "127.0.0.1", bindIP)
	})
	t.Run("With Zero address", func(t *testing.T) {
		address := "0.0.0.0:8080"
		bindIP, err := GetBindIP(address)
		require.NoError(t, err)
		assert.NotEqual(t, "127.0.0.1", bindIP)
	})
}
