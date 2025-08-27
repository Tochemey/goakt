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

	"github.com/tochemey/goakt/v3/log"
)

// ServerOption defines a function type that configures the server.
type ServerOption func(*server)

// ConnOption defines a function type that configures the Conn.
type ConnOption func(*Conn)

// WithLogger sets the logger for the server.
func WithLogger(logger log.Logger) ServerOption {
	return func(s *server) {
		s.logger = logger
	}
}

// WithMaxRecvMsgSize sets the maximum receive message size for the server.
func WithMaxRecvMsgSize(size int) ServerOption {
	return func(s *server) {
		s.maxReceivMsgSize = size
	}
}

// WithMaxSendMsgSize sets the maximum send message size for the server.
func WithMaxSendMsgSize(size int) ServerOption {
	return func(s *server) {
		s.maxSendMsgSize = size
	}
}

// WithServices registers the provided services with the server.
func WithServices(services ...serviceRegistry) ServerOption {
	return func(s *server) {
		s.services = services
	}
}

// WithServerTLS sets the TLS configuration for the server.
func WithServerTLS(config *tls.Config) ServerOption {
	return func(s *server) {
		s.tlsConfig = config
	}
}

// WithConnTLS sets the TLS configuration for the Conn.
func WithConnTLS(config *tls.Config) ConnOption {
	return func(c *Conn) {
		c.tlsConfig = config
	}
}

// WithConnMaxRecvMsgSize sets the maximum receive message size for the Conn.
func WithConnMaxRecvMsgSize(size int) ConnOption {
	return func(c *Conn) {
		c.maxReceivMsgSize = size
	}
}

// WithConnMaxSendMsgSize sets the maximum send message size for the Conn.
func WithConnMaxSendMsgSize(size int) ConnOption {
	return func(c *Conn) {
		c.maxSendMsgSize = size
	}
}
