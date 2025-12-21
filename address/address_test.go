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

package address

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAddressValidate(t *testing.T) {
	t.Run("No sender is valid", func(t *testing.T) {
		addr := NoSender()
		assert.NoError(t, addr.Validate())
	})

	t.Run("Valid address passes", func(t *testing.T) {
		addr := New("name", "system", "host", 1234)
		assert.NoError(t, addr.Validate())
	})

	t.Run("Invalid TCP endpoint", func(t *testing.T) {
		addr := New("name", "system", "", 1234)
		err := addr.Validate()
		assert.Error(t, err)
		assert.ErrorContains(t, err, "invalid address")
	})

	t.Run("Missing system", func(t *testing.T) {
		addr := New("name", "", "host", 1234)
		err := addr.Validate()
		assert.Error(t, err)
		assert.ErrorContains(t, err, "the [system] is required")
	})

	t.Run("Missing name", func(t *testing.T) {
		addr := New("", "system", "host", 1234)
		err := addr.Validate()
		assert.Error(t, err)
		assert.ErrorContains(t, err, "the [name] is required")
	})

	t.Run("Invalid system pattern", func(t *testing.T) {
		addr := New("name", "-system", "host", 1234)
		err := addr.Validate()
		assert.Error(t, err)
		assert.ErrorContains(t, err, "must contain only word characters")
	})

	t.Run("Invalid name pattern", func(t *testing.T) {
		addr := New("invalid name", "system", "host", 1234)
		err := addr.Validate()
		assert.Error(t, err)
		assert.ErrorContains(t, err, "must contain only word characters")
	})

	t.Run("Valid parent with different case system passes", func(t *testing.T) {
		parent := New("parent", "SYSTEM", "host", 1234)
		child := NewWithParent("child", "system", "host", 1234, parent)
		assert.NoError(t, child.Validate())
	})

	t.Run("Invalid parent", func(t *testing.T) {
		parent := New("", "system", "host", 1234)
		child := NewWithParent("child", "system", "host", 1234, parent)
		err := child.Validate()
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidParent)
		assert.ErrorContains(t, err, "the [name] is required")
	})

	t.Run("Parent system mismatch", func(t *testing.T) {
		parent := New("parent", "other", "host", 1234)
		child := NewWithParent("child", "system", "host", 1234, parent)
		err := child.Validate()
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidActorSystem)
	})

	t.Run("Parent host mismatch", func(t *testing.T) {
		parent := New("parent", "system", "otherhost", 4321)
		child := NewWithParent("child", "system", "host", 1234, parent)
		err := child.Validate()
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidHostAddress)
	})

	t.Run("Parent name reuse", func(t *testing.T) {
		parent := New("child", "system", "host", 1234)
		child := NewWithParent("child", "system", "host", 1234, parent)
		err := child.Validate()
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidName)
	})

	t.Run("With actor name too long", func(t *testing.T) {
		addr := New(strings.Repeat("a", 256), "system", "host", 1234)
		err := addr.Validate()
		assert.Error(t, err)
	})
}

func TestAddress(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		expected := "goakt://system@host:1234/name"
		addr := New("name", "system", "host", 1234)
		assert.Equal(t, "host:1234", addr.HostPort())
		assert.Equal(t, "host", addr.Host())
		assert.Equal(t, "name", addr.Name())
		assert.EqualValues(t, 1234, addr.Port())
		assert.Nil(t, addr.Parent())
		assert.Equal(t, expected, addr.String())
	})

	t.Run("With parent string", func(t *testing.T) {
		parent := New("parent", "system", "host", 1234)
		child := NewWithParent("child", "system", "host", 1234, parent)
		assert.Equal(t, "goakt://system@host:1234/parent/child", child.String())
	})

	t.Run("Equals returns false when to be compared to is nil", func(t *testing.T) {
		addr := New("name", "system", "host", 1234)
		assert.False(t, addr.Equals(nil))
	})

	t.Run("With Parent", func(t *testing.T) {
		parent := New("parent", "system", "host", 1234)
		child := NewWithParent("child", "system", "host", 1234, parent)
		assert.NotNil(t, child.Parent())
		assert.True(t, child.Parent().Equals(parent))
		assert.True(t, child.Parent().Equals(parent))
	})
}

func TestAddressNilReceiver(t *testing.T) {
	var addr *Address
	assert.Nil(t, addr.Parent())
	assert.Equal(t, "", addr.Name())
	assert.Equal(t, "", addr.Host())
	assert.EqualValues(t, 0, addr.Port())
	assert.Equal(t, "", addr.System())
	assert.Equal(t, "", addr.String())
	assert.Equal(t, ":0", addr.HostPort())
	assert.False(t, addr.Equals(nil))
	assert.NoError(t, addr.Validate())
}

func TestParse(t *testing.T) {
	t.Run("Without parent", func(t *testing.T) {
		addr, err := Parse("goakt://system@host:1234/name")
		assert.NoError(t, err)
		if assert.NotNil(t, addr) {
			assert.Equal(t, "system", addr.System())
			assert.Equal(t, "host", addr.Host())
			assert.EqualValues(t, 1234, addr.Port())
			assert.Equal(t, "name", addr.Name())
		}
	})

	t.Run("With parent", func(t *testing.T) {
		addr, err := Parse("goakt://system@host:1234/parent/child")
		assert.NoError(t, err)
		if assert.NotNil(t, addr) {
			assert.Equal(t, "system", addr.System())
			assert.Equal(t, "host", addr.Host())
			assert.EqualValues(t, 1234, addr.Port())
			assert.Equal(t, "child", addr.Name())
			assert.NotNil(t, addr.Parent())
			if assert.NotNil(t, addr.Parent()) {
				assert.Equal(t, "parent", addr.Parent().Name())
				assert.Equal(t, "system", addr.Parent().System())
				assert.Equal(t, "host", addr.Parent().Host())
				assert.EqualValues(t, 1234, addr.Parent().Port())
			}
		}
	})

	t.Run("Invalid address", func(t *testing.T) {
		tests := []struct {
			name        string
			addr        string
			errContains string
		}{
			{name: "Empty", addr: "", errContains: "address is required"},
			{name: "Wrong scheme", addr: "http://system@host:1234/name", errContains: "address protocol is not supported"},
			{name: "Missing scheme separator", addr: "goakt:/system@host:1234/name", errContains: "address format is invalid"},
			{name: "Missing system separator", addr: "goakt://systemhost:1234/name", errContains: "address format is invalid"},
			{name: "Missing path separator", addr: "goakt://system@host:1234", errContains: "address format is invalid"},
			{name: "Missing host port separator", addr: "goakt://system@host1234/name", errContains: "address format is invalid"},
			{name: "Invalid port", addr: "goakt://system@host:abc/name", errContains: "invalid syntax"},
			{name: "Extra path segment", addr: "goakt://system@host:1234/parent/child/grandchild", errContains: "address format is invalid"},
			{name: "Double slash path", addr: "goakt://system@host:1234//child", errContains: "address format is invalid"},
			{name: "Extra scheme separator", addr: "goakt://system@host://1234/name", errContains: "address format is invalid"},
			{name: "Extra system separator", addr: "goakt://system@host@other:1234/name", errContains: "address format is invalid"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				parsed, err := Parse(tt.addr)
				assert.Nil(t, parsed)
				assert.Error(t, err)
				assert.ErrorContains(t, err, tt.errContains)
			})
		}
	})
}
