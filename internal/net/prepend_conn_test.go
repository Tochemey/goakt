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

package net

import (
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrependConnReplaysPrefixThenUnderlying(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	go func() {
		_, _ = c1.Write([]byte("XYZ"))
		_ = c1.Close()
	}()

	pc := &prependConn{Conn: c2, prefix: []byte{'A', 'B'}}
	buf := make([]byte, 5)
	n, err := io.ReadFull(pc, buf)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "ABXYZ", string(buf))
	assert.Nil(t, pc.prefix)
}

func TestPrependConnEmptyPrefixPassthrough(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	go func() {
		_, _ = c1.Write([]byte("hello"))
		_ = c1.Close()
	}()

	pc := &prependConn{Conn: c2}
	buf := make([]byte, 5)
	n, err := io.ReadFull(pc, buf)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "hello", string(buf))
}

func TestPrependConnPartialReadsDrainPrefix(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	go func() {
		_, _ = c1.Write([]byte("CD"))
		_ = c1.Close()
	}()

	pc := &prependConn{Conn: c2, prefix: []byte{'A', 'B'}}

	first := make([]byte, 1)
	n, err := pc.Read(first)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, byte('A'), first[0])
	assert.Equal(t, []byte{'B'}, pc.prefix)

	rest := make([]byte, 3)
	n, err = io.ReadFull(pc, rest)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, "BCD", string(rest))
	assert.Nil(t, pc.prefix)
}

func TestPrependConnReadSatisfiedByPrefixAlone(t *testing.T) {
	// Underlying conn is never read; close it so a mistaken Read would fail.
	c1, c2 := net.Pipe()
	_ = c1.Close()
	_ = c2.Close()

	pc := &prependConn{Conn: c2, prefix: []byte{'X', 'Y', 'Z'}}
	buf := make([]byte, 2)
	n, err := pc.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, "XY", string(buf))
	assert.Equal(t, []byte{'Z'}, pc.prefix)
}
