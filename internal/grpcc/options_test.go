/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package grpcc

import (
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/log"
)

// nolint
func TestServerOption(t *testing.T) {
	t.Run("WithLogger", func(t *testing.T) {
		opt := WithLogger(log.DefaultLogger)
		srv := &server{}
		opt(srv)
		expected := &server{logger: log.DefaultLogger}
		require.Equal(t, expected, srv)
	})

	t.Run("WithMaxRecvMsgSize", func(t *testing.T) {
		size := 1024
		opt := WithMaxRecvMsgSize(size)
		srv := &server{}
		opt(srv)
		expected := &server{maxReceivMsgSize: size}
		require.Equal(t, expected, srv)
	})

	t.Run("WithMaxSendMsgSize", func(t *testing.T) {
		size := 2048
		opt := WithMaxSendMsgSize(size)
		srv := &server{}
		opt(srv)
		expected := &server{maxSendMsgSize: size}
		require.Equal(t, expected, srv)
	})

	t.Run("WithServerTLS", func(t *testing.T) {
		cert := &tls.Config{}
		opt := WithServerTLS(cert)
		srv := &server{}
		opt(srv)
		expected := &server{tlsConfig: cert}
		require.Equal(t, expected, srv)
	})
}

// nolint
func TestConnOption(t *testing.T) {
	t.Run("WithMaxRecvMsgSize", func(t *testing.T) {
		size := 4096
		opt := WithConnMaxRecvMsgSize(size)
		conn := &Conn{}
		opt(conn)
		expected := &Conn{maxReceivMsgSize: size}
		require.Equal(t, expected, conn)
	})

	t.Run("WithMaxSendMsgSize", func(t *testing.T) {
		size := 8192
		opt := WithConnMaxSendMsgSize(size)
		conn := &Conn{}
		opt(conn)
		expected := &Conn{maxSendMsgSize: size}
		require.Equal(t, expected, conn)
	})

	t.Run("WithClientTLS", func(t *testing.T) {
		cert := &tls.Config{}
		opt := WithConnTLS(cert)
		conn := &Conn{}
		opt(conn)
		expected := &Conn{tlsConfig: cert}
		require.Equal(t, expected, conn)
	})
}
