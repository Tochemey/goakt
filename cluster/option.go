/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

package cluster

import (
	"time"

	"github.com/tochemey/goakt/hash"
	"github.com/tochemey/goakt/log"
)

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(cl *Node)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(cl *Node)

// Apply applies the Node's option
func (f OptionFunc) Apply(c *Node) {
	f(c)
}

// WithPartitionsCount sets the total number of partitions
func WithPartitionsCount(count uint64) Option {
	return OptionFunc(func(cl *Node) {
		cl.partitionsCount = count
	})
}

// WithLogger sets the logger
func WithLogger(logger log.Logger) Option {
	return OptionFunc(func(cl *Node) {
		cl.logger = logger
	})
}

// WithWriteTimeout sets the Node write timeout.
// This timeout specifies the timeout of a data replication
func WithWriteTimeout(timeout time.Duration) Option {
	return OptionFunc(func(cl *Node) {
		cl.writeTimeout = timeout
	})
}

// WithReadTimeout sets the Node read timeout.
// This timeout specifies the timeout of a data retrieval
func WithReadTimeout(timeout time.Duration) Option {
	return OptionFunc(func(cl *Node) {
		cl.readTimeout = timeout
	})
}

// WithShutdownTimeout sets the Node shutdown timeout.
func WithShutdownTimeout(timeout time.Duration) Option {
	return OptionFunc(func(cl *Node) {
		cl.shutdownTimeout = timeout
	})
}

// WithHasher sets the custom hasher
func WithHasher(hasher hash.Hasher) Option {
	return OptionFunc(func(cl *Node) {
		cl.hasher = hasher
	})
}
