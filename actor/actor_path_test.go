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

package actor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/address"
)

func TestNewPath(t *testing.T) {
	t.Run("nil address returns nil", func(t *testing.T) {
		p := newPath(nil)
		require.Nil(t, p)
	})

	t.Run("simple address", func(t *testing.T) {
		addr := address.New("actor1", "system1", "127.0.0.1", 9000)
		p := newPath(addr)
		require.NotNil(t, p)

		assert.Equal(t, "127.0.0.1", p.Host())
		assert.Equal(t, 9000, p.Port())
		assert.Equal(t, "actor1", p.Name())
		assert.Equal(t, "system1", p.System())
		assert.Nil(t, p.Parent())
		assert.Equal(t, addr.String(), p.String())
	})

	t.Run("address with parent", func(t *testing.T) {
		parentAddr := address.New("parent", "system1", "127.0.0.1", 9000)
		childAddr := address.NewWithParent("child", "system1", "127.0.0.1", 9000, parentAddr)

		p := newPath(childAddr)
		require.NotNil(t, p)

		assert.Equal(t, "child", p.Name())
		assert.Equal(t, "system1", p.System())
		assert.Equal(t, "127.0.0.1", p.Host())
		assert.Equal(t, 9000, p.Port())
		assert.Equal(t, childAddr.String(), p.String())

		parent := p.Parent()
		require.NotNil(t, parent)
		assert.Equal(t, "parent", parent.Name())
		assert.Equal(t, "system1", parent.System())
		assert.Equal(t, "127.0.0.1", parent.Host())
		assert.Equal(t, 9000, parent.Port())
		assert.Nil(t, parent.Parent())
	})

	t.Run("address with NoSender parent", func(t *testing.T) {
		addr := address.New("actor1", "system1", "127.0.0.1", 9000)
		p := newPath(addr)
		require.NotNil(t, p)
		assert.Nil(t, p.Parent())
	})
}

func TestPath_Equals(t *testing.T) {
	t.Run("same path returns true", func(t *testing.T) {
		addr := address.New("actor1", "system1", "127.0.0.1", 9000)
		p1 := newPath(addr)
		p2 := newPath(addr)
		require.NotNil(t, p1)
		require.NotNil(t, p2)
		assert.True(t, p1.Equals(p2))
	})

	t.Run("different paths return false", func(t *testing.T) {
		addr1 := address.New("actor1", "system1", "127.0.0.1", 9000)
		addr2 := address.New("actor2", "system1", "127.0.0.1", 9000)
		p1 := newPath(addr1)
		p2 := newPath(addr2)
		require.NotNil(t, p1)
		require.NotNil(t, p2)
		assert.False(t, p1.Equals(p2))
	})

	t.Run("different host returns false", func(t *testing.T) {
		addr1 := address.New("actor1", "system1", "127.0.0.1", 9000)
		addr2 := address.New("actor1", "system1", "127.0.0.2", 9000)
		p1 := newPath(addr1)
		p2 := newPath(addr2)
		require.NotNil(t, p1)
		require.NotNil(t, p2)
		assert.False(t, p1.Equals(p2))
	})

	t.Run("different port returns false", func(t *testing.T) {
		addr1 := address.New("actor1", "system1", "127.0.0.1", 9000)
		addr2 := address.New("actor1", "system1", "127.0.0.1", 9001)
		p1 := newPath(addr1)
		p2 := newPath(addr2)
		require.NotNil(t, p1)
		require.NotNil(t, p2)
		assert.False(t, p1.Equals(p2))
	})

	t.Run("nil other returns false", func(t *testing.T) {
		addr := address.New("actor1", "system1", "127.0.0.1", 9000)
		p := newPath(addr)
		require.NotNil(t, p)
		assert.False(t, p.Equals(nil))
	})
}

func TestPath_NilReceiver(t *testing.T) {
	var p *path
	assert.Equal(t, "", p.Host())
	assert.Equal(t, 0, p.Port())
	assert.Equal(t, "", p.Name())
	assert.Nil(t, p.Parent())
	assert.Equal(t, "", p.String())
	assert.Equal(t, "", p.System())
	assert.False(t, p.Equals(newPath(address.New("a", "s", "h", 0))))
}
