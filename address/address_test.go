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
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/goaktpb"
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
		assert.True(t, proto.Equal(addr.Parent(), zeroAddress))
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
		addr := NoSender()
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

	t.Run("With actor name too long", func(t *testing.T) {
		addr := New(strings.Repeat("a", 256), "system", "host", 1234)
		err := addr.Validate()
		assert.Error(t, err)
	})

	t.Run("Equals returns false when to be compared to is nil", func(t *testing.T) {
		addr := New("name", "system", "host", 1234)
		assert.False(t, addr.Equals(nil))
	})

	t.Run("With Parent", func(t *testing.T) {
		parent := New("parent", "system", "host", 1234)
		child := New("child", "system", "host", 1234).WithParent(parent)
		assert.NotNil(t, child.Parent())
		assert.True(t, child.Parent().Equals(parent))
		assert.True(t, proto.Equal(child.Parent(), parent.Address))
	})

	t.Run("From protobuf address", func(t *testing.T) {
		protoAddr := &goaktpb.Address{
			Name:   "name",
			System: "system",
			Host:   "host",
			Port:   1234,
			Id:     "id",
			Parent: zeroAddress,
		}
		addr := From(protoAddr)
		assert.NotNil(t, addr)
		assert.Equal(t, "name", addr.Name())
		assert.Equal(t, "system", addr.System())
		assert.Equal(t, "host", addr.Host())
		assert.EqualValues(t, 1234, addr.Port())
		assert.Equal(t, "id", addr.ID())
		assert.NotNil(t, addr.Parent())
		assert.True(t, proto.Equal(addr.Parent(), zeroAddress))
	})
}

func TestParseAddress(t *testing.T) {
	t.Run("Valid address", func(t *testing.T) {
		addrStr := "goakt://system@host:1234/name"
		addr, err := Parse(addrStr)
		assert.NoError(t, err)
		assert.NotNil(t, addr)
		assert.Equal(t, "system", addr.System())
		assert.Equal(t, "host", addr.Host())
		assert.Equal(t, 1234, addr.Port())
		assert.Equal(t, "name", addr.Name())
	})

	t.Run("Invalid address format", func(t *testing.T) {
		addrStr := "invalid_address"
		addr, err := Parse(addrStr)
		assert.Error(t, err)
		assert.Nil(t, addr)
	})

	t.Run("Unsupported protocol", func(t *testing.T) {
		addrStr := "http://system@host:1234/name"
		addr, err := Parse(addrStr)
		assert.Error(t, err)
		assert.Nil(t, addr)
	})

	t.Run("Missing address parts", func(t *testing.T) {
		addrStr := "goakt://system@host:1234"
		addr, err := Parse(addrStr)
		assert.Error(t, err)
		assert.Nil(t, addr)
	})

	t.Run("Invalid port", func(t *testing.T) {
		addrStr := "goakt://system@host:invalid_port/name"
		addr, err := Parse(addrStr)
		assert.Error(t, err)
		assert.Nil(t, addr)
	})

	t.Run("Empty address string", func(t *testing.T) {
		addrStr := ""
		addr, err := Parse(addrStr)
		assert.Error(t, err)
		assert.Nil(t, addr)
	})

	t.Run("Address with no port", func(t *testing.T) {
		addrStr := "goakt://system@host/name"
		addr, err := Parse(addrStr)
		assert.Error(t, err)
		assert.Nil(t, addr)
	})

	t.Run("Address with no host and port", func(t *testing.T) {
		addrStr := "goakt://system"
		addr, err := Parse(addrStr)
		assert.Error(t, err)
		assert.Nil(t, addr)
	})
}
