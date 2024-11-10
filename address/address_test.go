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

package address

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
)

func TestAddress(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		expected := "goakt://system@host:1234/name"
		addr := New("name", "system", "host", 1234)
		assert.Equal(t, "host:1234", addr.HostPort())
		assert.Equal(t, "host", addr.Host())
		assert.Equal(t, "name", addr.Name())
		assert.EqualValues(t, 1234, addr.Port())
		assert.NotEmpty(t, addr.ID())
		assert.NotNil(t, addr.Parent())
		assert.True(t, proto.Equal(addr.Parent(), NoSender))
		assert.Equal(t, expected, addr.String())
	})

	t.Run("With Host change", func(t *testing.T) {
		expected := "goakt://system@host:1234/name"
		addr := New("name", "system", "host", 1234)
		assert.Equal(t, "host:1234", addr.HostPort())
		assert.Equal(t, "host", addr.Host())
		assert.EqualValues(t, 1234, addr.Port())
		assert.Equal(t, expected, addr.String())

		// change the host
		actual := addr.WithHost("localhost")
		expected = "goakt://system@localhost:1234/name"
		assert.Equal(t, "localhost:1234", actual.HostPort())
		assert.Equal(t, "localhost", actual.Host())
		assert.EqualValues(t, 1234, actual.Port())
		assert.Equal(t, expected, actual.String())
	})

	t.Run("With port change", func(t *testing.T) {
		addr := New("name", "system", "host", 1234)
		expected := "goakt://system@host:1234/name"
		assert.Equal(t, expected, addr.String())
		actual := addr.WithPort(3000)
		assert.NotNil(t, actual)
		expected = "goakt://system@host:3000/name"
		assert.Equal(t, expected, actual.String())
	})

	t.Run("With Actor System change", func(t *testing.T) {
		addr := New("name", "system", "host", 1234)
		expected := "goakt://system@host:1234/name"
		assert.Equal(t, expected, addr.String())
		actual := addr.WithSystem("MySyS")
		assert.NotNil(t, actual)
		expected = "goakt://MySyS@host:1234/name"
		assert.Equal(t, expected, actual.String())
	})
	t.Run("With Default", func(t *testing.T) {
		addr := Default()
		assert.NoError(t, addr.Validate())

		addr.WithHost("localhost").WithPort(123).WithSystem("system").WithName("name")
		assert.NoError(t, addr.Validate())
		assert.Equal(t, "goakt://system@localhost:123/name", addr.String())
	})
	t.Run("With marshaling", func(t *testing.T) {
		addr := New("name", "system", "host", 1234)

		bytea, err := addr.MarshalBinary()
		assert.NoError(t, err)
		assert.NotNil(t, bytea)

		actual := new(Address)
		err = actual.UnmarshalBinary(bytea)
		assert.NoError(t, err)

		assert.True(t, actual.Equals(addr))
	})

	t.Run("With unmarshaling failure", func(t *testing.T) {
		bytea := []byte("some")
		actual := new(Address)
		err := actual.UnmarshalBinary(bytea)
		assert.Error(t, err)
	})
}
