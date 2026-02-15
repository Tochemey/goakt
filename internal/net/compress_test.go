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
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type mockAddr struct {
	network string
	str     string
}

func (a mockAddr) Network() string { return a.network }
func (a mockAddr) String() string  { return a.str }

type mockNetConn struct {
	readFunc  func([]byte) (int, error)
	writeFunc func([]byte) (int, error)
	closeFunc func() error
	local     net.Addr
	remote    net.Addr
	deadErr   error
}

func (m *mockNetConn) Read(b []byte) (int, error) {
	if m.readFunc != nil {
		return m.readFunc(b)
	}
	return 0, nil
}

func (m *mockNetConn) Write(b []byte) (int, error) {
	if m.writeFunc != nil {
		return m.writeFunc(b)
	}
	return len(b), nil
}

func (m *mockNetConn) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func (m *mockNetConn) LocalAddr() net.Addr                { return m.local }
func (m *mockNetConn) RemoteAddr() net.Addr               { return m.remote }
func (m *mockNetConn) SetDeadline(_ time.Time) error      { return m.deadErr }
func (m *mockNetConn) SetReadDeadline(_ time.Time) error  { return m.deadErr }
func (m *mockNetConn) SetWriteDeadline(_ time.Time) error { return m.deadErr }

type mockFW struct {
	writeFunc func([]byte) (int, error)
	flushFunc func() error
}

func (m *mockFW) Write(p []byte) (int, error) {
	if m.writeFunc != nil {
		return m.writeFunc(p)
	}
	return len(p), nil
}

func (m *mockFW) Flush() error {
	if m.flushFunc != nil {
		return m.flushFunc()
	}
	return nil
}

func TestCompressedConn_Read(t *testing.T) {
	payload := []byte("hello compressed read")
	reader := &mockReader{data: payload}
	raw := &mockNetConn{}
	cc := &compressedConn{
		raw:    raw,
		reader: reader,
		writer: &mockFW{},
		closer: func() error { return nil },
	}

	buf := make([]byte, 64)
	n, err := cc.Read(buf)
	require.NoError(t, err)
	require.Equal(t, payload, buf[:n])
}

func TestCompressedConn_Write(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var written []byte
		fw := &mockFW{
			writeFunc: func(p []byte) (int, error) {
				written = append(written, p...)
				return len(p), nil
			},
		}
		cc := &compressedConn{raw: &mockNetConn{}, reader: nil, writer: fw, closer: func() error { return nil }}

		n, err := cc.Write([]byte("data"))
		require.NoError(t, err)
		require.Equal(t, 4, n)
		require.Equal(t, []byte("data"), written)
	})

	t.Run("write error", func(t *testing.T) {
		fw := &mockFW{
			writeFunc: func([]byte) (int, error) { return 0, errors.New("write fail") },
		}
		cc := &compressedConn{raw: &mockNetConn{}, writer: fw, closer: func() error { return nil }}

		_, err := cc.Write([]byte("x"))
		require.Error(t, err)
	})

	t.Run("flush error", func(t *testing.T) {
		fw := &mockFW{
			flushFunc: func() error { return errors.New("flush fail") },
		}
		cc := &compressedConn{raw: &mockNetConn{}, writer: fw, closer: func() error { return nil }}

		_, err := cc.Write([]byte("x"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "flush fail")
	})
}

func TestCompressedConn_Close(t *testing.T) {
	t.Run("both succeed", func(t *testing.T) {
		cc := &compressedConn{
			raw:    &mockNetConn{},
			reader: &mockReader{},
			writer: &mockFW{},
			closer: func() error { return nil },
		}
		require.NoError(t, cc.Close())
	})

	t.Run("closer error returned", func(t *testing.T) {
		cc := &compressedConn{
			raw:    &mockNetConn{},
			reader: &mockReader{},
			writer: &mockFW{},
			closer: func() error { return errors.New("closer fail") },
		}
		err := cc.Close()
		require.Error(t, err)
		require.Contains(t, err.Error(), "closer fail")
	})

	t.Run("raw close error returned when closer succeeds", func(t *testing.T) {
		cc := &compressedConn{
			raw:    &mockNetConn{closeFunc: func() error { return errors.New("raw fail") }},
			reader: &mockReader{},
			writer: &mockFW{},
			closer: func() error { return nil },
		}
		err := cc.Close()
		require.Error(t, err)
		require.Contains(t, err.Error(), "raw fail")
	})

	t.Run("closer error takes priority", func(t *testing.T) {
		cc := &compressedConn{
			raw:    &mockNetConn{closeFunc: func() error { return errors.New("raw") }},
			reader: &mockReader{},
			writer: &mockFW{},
			closer: func() error { return errors.New("closer") },
		}
		err := cc.Close()
		require.Error(t, err)
		require.Contains(t, err.Error(), "closer")
	})
}

func TestCompressedConn_Delegation(t *testing.T) {
	la := mockAddr{network: "tcp", str: "127.0.0.1:1234"}
	ra := mockAddr{network: "tcp", str: "10.0.0.1:5678"}

	raw := &mockNetConn{
		local:   la,
		remote:  ra,
		deadErr: nil,
	}

	cc := &compressedConn{
		raw:    raw,
		reader: &mockReader{},
		writer: &mockFW{},
		closer: func() error { return nil },
	}

	require.Equal(t, la, cc.LocalAddr())
	require.Equal(t, ra, cc.RemoteAddr())
	require.NoError(t, cc.SetDeadline(time.Time{}))
	require.NoError(t, cc.SetReadDeadline(time.Time{}))
	require.NoError(t, cc.SetWriteDeadline(time.Time{}))

	// Verify error propagation.
	raw.deadErr = errors.New("deadline fail")
	require.Error(t, cc.SetDeadline(time.Time{}))
	require.Error(t, cc.SetReadDeadline(time.Time{}))
	require.Error(t, cc.SetWriteDeadline(time.Time{}))
}

func TestGetCompressedConn(t *testing.T) {
	raw := &mockNetConn{local: mockAddr{str: "local"}, remote: mockAddr{str: "remote"}}
	reader := &mockReader{}
	writer := &mockFW{}
	closerCalled := false
	closer := func() error { closerCalled = true; return nil }

	cc := getCompressedConn(raw, reader, writer, closer)
	require.NotNil(t, cc)
	require.Equal(t, raw, cc.raw)
	require.Equal(t, reader, cc.reader)
	require.Equal(t, writer, cc.writer)

	// Verify closer is wired.
	_ = cc.closer()
	require.True(t, closerCalled)
}
