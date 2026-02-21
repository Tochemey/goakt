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

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/pause"
)

func TestNewBrotliConnWrapper_Default(t *testing.T) {
	wrapper := NewBrotliConnWrapper()
	require.NotNil(t, wrapper)
}

func TestNewBrotliConnWrapper_WithLevel(t *testing.T) {
	wrapper := NewBrotliConnWrapper(WithBrotliLevel(6))
	require.NotNil(t, wrapper)
	require.Equal(t, 6, wrapper.level)
}

func TestBrotliConnWrapper_Wrap(t *testing.T) {
	wrapper := NewBrotliConnWrapper()

	raw := &mockNetConn{
		local:  mockAddr{str: "local"},
		remote: mockAddr{str: "remote"},
	}

	conn, err := wrapper.Wrap(raw)
	require.NoError(t, err)
	require.NotNil(t, conn)

	require.Equal(t, "local", conn.LocalAddr().String())
	require.Equal(t, "remote", conn.RemoteAddr().String())
}

func TestBrotliConnWrapper_EndToEnd(t *testing.T) {
	wrapper := NewBrotliConnWrapper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	payload := []byte("hello brotli compression end-to-end test data")
	serverReceived := make(chan []byte, 1)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverReceived <- nil
			return
		}
		serverConn, err := wrapper.Wrap(conn)
		if err != nil {
			_ = conn.Close() // Close error intentionally ignored — wrapper setup already failed.
			serverReceived <- nil
			return
		}
		// A single Read on a compressed stream may return partial data;
		// use ReadFull to collect all expected bytes.
		data := make([]byte, len(payload))
		_, _ = io.ReadFull(serverConn, data) //nolint:errcheck // test helper
		_, _ = serverConn.Write(data)        //nolint:errcheck // best-effort echo
		serverReceived <- data
		// Wait for client to finish reading before closing to ensure all
		// compressed data is flushed through TCP.
		pause.For(50 * time.Millisecond)
		_ = serverConn.Close() // Close error intentionally ignored — test cleanup.
	}()

	clientRaw, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)

	clientConn, err := wrapper.Wrap(clientRaw)
	require.NoError(t, err)

	_, err = clientConn.Write(payload)
	require.NoError(t, err)

	// Read the echo — compressed streams may return partial data per Read,
	// so use ReadFull to collect all expected bytes.
	echoBuf := make([]byte, len(payload))
	require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, err = io.ReadFull(clientConn, echoBuf)
	require.NoError(t, err)
	require.Equal(t, payload, echoBuf)

	require.NoError(t, clientConn.Close())

	received := <-serverReceived
	require.Equal(t, payload, received)
}

func TestBrotliConnWrapper_MultipleWrapReuse(t *testing.T) {
	wrapper := NewBrotliConnWrapper()

	for i := 0; i < 4; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		payload := []byte("brotli reuse test")
		done := make(chan struct{})
		go func() {
			defer close(done)
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			serverConn, err := wrapper.Wrap(conn)
			if err != nil {
				_ = conn.Close() // Close error intentionally ignored — wrapper setup already failed.
				return
			}
			data := make([]byte, len(payload))
			_, _ = io.ReadFull(serverConn, data) //nolint:errcheck // test helper
			_, _ = serverConn.Write(data)        //nolint:errcheck // best-effort echo
			pause.For(50 * time.Millisecond)
			_ = serverConn.Close() // Close error intentionally ignored — test cleanup.
		}()

		clientRaw, err := net.Dial("tcp", listener.Addr().String())
		require.NoError(t, err)

		clientConn, err := wrapper.Wrap(clientRaw)
		require.NoError(t, err)

		_, err = clientConn.Write(payload)
		require.NoError(t, err)

		echoBuf := make([]byte, len(payload))
		require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(2*time.Second)))
		_, err = io.ReadFull(clientConn, echoBuf)
		require.NoError(t, err)
		require.Equal(t, payload, echoBuf)

		require.NoError(t, clientConn.Close())
		<-done
		require.NoError(t, listener.Close())
	}
}

func TestBrotliOption_WithBrotliLevel(t *testing.T) {
	cfg := brotliConfig{}
	WithBrotliLevel(9)(&cfg)
	require.Equal(t, 9, cfg.level)
}

func TestBrotliFlushWriter(t *testing.T) {
	wrapper := NewBrotliConnWrapper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		pause.For(50 * time.Millisecond)
		_ = conn.Close() // Close error intentionally ignored — test cleanup.
	}()

	raw, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)

	wrapped, err := wrapper.Wrap(raw)
	require.NoError(t, err)

	// Write exercises the brotliFlushWriter path.
	_, err = wrapped.Write([]byte("flush test"))
	require.NoError(t, err)

	_ = wrapped.Close() // Close error intentionally ignored — remote end already closed.
}
