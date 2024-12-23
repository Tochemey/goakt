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

package members

import (
	"time"

	"github.com/tochemey/goakt/v2/log"
)

// Option is the interface that applies a Server option.
type Option interface {
	// Apply sets the Option value of a Server.
	Apply(server *Server)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(server *Server)

// Apply applies the Server's option
func (f OptionFunc) Apply(server *Server) {
	f(server)
}

// WithLogger sets the logger
func WithLogger(logger log.Logger) Option {
	return OptionFunc(func(server *Server) {
		server.logger = logger
	})
}

// WithMaxJoinAttempts sets the max join attempts
func WithMaxJoinAttempts(maxJoinAttempts int) Option {
	return OptionFunc(func(server *Server) {
		server.maxJoinAttempts = maxJoinAttempts
	})
}

// WithJoinTimeout sets the join timeout
func WithJoinTimeout(timeout time.Duration) Option {
	return OptionFunc(func(server *Server) {
		server.joinTimeout = timeout
	})
}

// WithJoinRetryInterval sets the join retry interval
func WithJoinRetryInterval(retryInterval time.Duration) Option {
	return OptionFunc(func(server *Server) {
		server.joinRetryInterval = retryInterval
	})
}

// WithShutdownTimeout sets the shutdown timeout
func WithShutdownTimeout(timeout time.Duration) Option {
	return OptionFunc(func(server *Server) {
		server.shutdownTimeout = timeout
	})
}

// WithReplicationInterval sets the replication interval
func WithReplicationInterval(interval time.Duration) Option {
	return OptionFunc(func(server *Server) {
		server.replicationInterval = interval
	})
}
