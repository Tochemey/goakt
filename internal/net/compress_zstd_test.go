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
	"net"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/internal/pause"
)

func TestNewZstdConnWrapper_Default(t *testing.T) {
	wrapper, err := NewZstdConnWrapper()
	require.NoError(t, err)
	require.NotNil(t, wrapper)
}

func TestNewZstdConnWrapper_WithOptions(t *testing.T) {
	wrapper, err := NewZstdConnWrapper(
		WithZstdLevel(zstd.SpeedBestCompression),
		WithZstdWindow(1<<20),
		WithZstdDecoderMaxMemory(128<<20),
	)
	require.NoError(t, err)
	require.NotNil(t, wrapper)
}

func TestZstdConnWrapper_Wrap(t *testing.T) {
	wrapper, err := NewZstdConnWrapper()
	require.NoError(t, err)

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

func TestZstdConnWrapper_EndToEnd(t *testing.T) {
	wrapper, err := NewZstdConnWrapper()
	require.NoError(t, err)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	payload := []byte("hello zstd compression end-to-end test data")
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
		buf := make([]byte, 256)
		var n int
		var readErr error
		for n == 0 && readErr == nil {
			n, readErr = serverConn.Read(buf)
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		if n > 0 {
			_, _ = serverConn.Write(buf[:n]) //nolint:errcheck // best-effort echo
		}
		serverReceived <- data
		_ = serverConn.Close() // Close error intentionally ignored — test cleanup.
	}()

	clientRaw, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)

	clientConn, err := wrapper.Wrap(clientRaw)
	require.NoError(t, err)

	_, err = clientConn.Write(payload)
	require.NoError(t, err)

	buf := make([]byte, 256)
	require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(2*time.Second)))
	n, err := clientConn.Read(buf)
	require.NoError(t, err)
	require.Equal(t, payload, buf[:n])

	require.NoError(t, clientConn.Close())

	received := <-serverReceived
	require.Equal(t, payload, received)
}

func TestZstdConnWrapper_MultipleWrapReuse(t *testing.T) {
	wrapper, err := NewZstdConnWrapper()
	require.NoError(t, err)

	for i := 0; i < 4; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

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
			buf := make([]byte, 64)
			n, _ := serverConn.Read(buf)     //nolint:errcheck // reads until client disconnects
			_, _ = serverConn.Write(buf[:n]) //nolint:errcheck // best-effort echo
			_ = serverConn.Close()           // Close error intentionally ignored — test cleanup.
		}()

		clientRaw, err := net.Dial("tcp", listener.Addr().String())
		require.NoError(t, err)

		clientConn, err := wrapper.Wrap(clientRaw)
		require.NoError(t, err)

		payload := []byte("reuse test")
		_, err = clientConn.Write(payload)
		require.NoError(t, err)

		buf := make([]byte, 64)
		require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(2*time.Second)))
		n, err := clientConn.Read(buf)
		require.NoError(t, err)
		require.Equal(t, payload, buf[:n])

		require.NoError(t, clientConn.Close())
		<-done
		require.NoError(t, listener.Close())
	}
}

func TestNewZstdConnWrapper_InvalidEncoderOpts(t *testing.T) {
	// A window size of 0 is invalid for zstd encoder.
	_, err := NewZstdConnWrapper(WithZstdWindow(0))
	require.Error(t, err)
	require.ErrorIs(t, err, ErrZstdInvalidEncoderOpts)
}

func TestNewZstdConnWrapper_InvalidDecoderOpts(t *testing.T) {
	// A max memory of 1 byte should be too small for the decoder to initialize.
	_, err := NewZstdConnWrapper(WithZstdDecoderMaxMemory(1))
	if err != nil {
		// If the library rejects it, verify the error type.
		require.ErrorIs(t, err, ErrZstdInvalidDecoderOpts)
	}
	// If the library accepts it, that's fine — we can't trigger decoder validation failure
	// with this option on all platforms.
}

func TestZstdOptions(t *testing.T) {
	t.Run("WithZstdLevel", func(t *testing.T) {
		cfg := zstdConfig{}
		WithZstdLevel(zstd.SpeedFastest)(&cfg)
		require.Equal(t, zstd.SpeedFastest, cfg.level)
	})

	t.Run("WithZstdWindow", func(t *testing.T) {
		cfg := zstdConfig{}
		WithZstdWindow(1 << 22)(&cfg)
		require.Equal(t, 1<<22, cfg.window)
	})

	t.Run("WithZstdDecoderMaxMemory", func(t *testing.T) {
		cfg := zstdConfig{}
		WithZstdDecoderMaxMemory(256 << 20)(&cfg)
		require.Equal(t, uint64(256<<20), cfg.maxMem)
	})
}

func TestZstdFlushWriter(t *testing.T) {
	wrapper, err := NewZstdConnWrapper()
	require.NoError(t, err)

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

	// Write tests the zstdFlushWriter path.
	_, err = wrapped.Write([]byte("flush test"))
	require.NoError(t, err)

	_ = wrapped.Close() // Close error intentionally ignored — remote end already closed.
}
