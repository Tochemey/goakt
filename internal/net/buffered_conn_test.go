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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/pause"
)

func TestBufferedConn(t *testing.T) {
	t.Run("reads pass through the buffer", func(t *testing.T) {
		client, server := net.Pipe()
		defer func() { _ = server.Close() }()

		bc := newBufferedConn(client)
		defer func() { _ = bc.Close() }()

		go func() {
			_, _ = server.Write([]byte("hello"))
		}()

		buf := make([]byte, 5)
		_, err := io.ReadFull(bc, buf)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(buf))
	})

	t.Run("one write is drained by sequential reads without stranding bytes", func(t *testing.T) {
		client, server := net.Pipe()
		defer func() { _ = server.Close() }()

		bc := newBufferedConn(client)
		defer func() { _ = bc.Close() }()

		go func() {
			_, _ = server.Write([]byte("firstsecond"))
		}()

		first := make([]byte, 5)
		_, err := io.ReadFull(bc, first)
		require.NoError(t, err)
		assert.Equal(t, "first", string(first))

		second := make([]byte, 6)
		_, err = io.ReadFull(bc, second)
		require.NoError(t, err)
		assert.Equal(t, "second", string(second))
	})

	t.Run("double close is safe and closes the underlying conn", func(t *testing.T) {
		client, server := net.Pipe()
		defer func() { _ = server.Close() }()

		bc := newBufferedConn(client)

		require.NoError(t, bc.Close())
		_ = bc.Close()

		_, err := client.Write([]byte("x"))
		assert.Error(t, err)
	})

	t.Run("read after close returns net.ErrClosed", func(t *testing.T) {
		client, server := net.Pipe()
		defer func() { _ = server.Close() }()

		bc := newBufferedConn(client)
		require.NoError(t, bc.Close())

		_, err := bc.Read(make([]byte, 1))
		assert.ErrorIs(t, err, net.ErrClosed)
	})

	t.Run("close unblocks a pending read", func(t *testing.T) {
		client, server := net.Pipe()
		defer func() { _ = server.Close() }()

		bc := newBufferedConn(client)
		readErr := make(chan error, 1)

		go func() {
			_, err := bc.Read(make([]byte, 1))
			readErr <- err
		}()

		// Give the reader time to block on the empty pipe, then Close must
		// terminate it instead of deadlocking on the reader lock.
		pause.For(50 * time.Millisecond)
		require.NoError(t, bc.Close())
		assert.Error(t, <-readErr)
	})

	t.Run("writes bypass the buffer and reach the peer", func(t *testing.T) {
		client, server := net.Pipe()
		defer func() { _ = server.Close() }()

		bc := newBufferedConn(client)
		defer func() { _ = bc.Close() }()

		go func() {
			_, _ = bc.Write([]byte("ping"))
		}()

		buf := make([]byte, 4)
		_, err := io.ReadFull(server, buf)
		require.NoError(t, err)
		assert.Equal(t, "ping", string(buf))
	})
}
