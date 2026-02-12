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

package remote

import (
	"net"
	"strconv"
	"time"

	internalnet "github.com/tochemey/goakt/v3/internal/net"
	"github.com/tochemey/goakt/v3/internal/size"
	"github.com/tochemey/goakt/v3/internal/validation"
)

// Config defines the remote config.
//
// BindAddr must be provided as a physical IP address rather than a DNS name so
// GoAkt can bind to a deterministic network interface without relying on
// external name resolution. When BindAddr is set to 0.0.0.0 the runtime will
// attempt to discover an appropriate private IP address to publish to other
// nodes, only falling back to a public IP when no private candidate exists.
// The design favors predictable intra-cluster connectivity in multi-homed or
// containerized deployments where DNS entries may be unavailable or resolve to
// unintended interfaces.
type Config struct {
	maxFrameSize      uint32
	writeTimeout      time.Duration
	readIdleTimeout   time.Duration
	idleTimeout       time.Duration
	bindAddr          string
	bindPort          int
	compression       Compression
	contextPropagator ContextPropagator
}

var _ validation.Validator = (*Config)(nil)

// NewConfig returns a Config initialized with the supplied bind address, port,
// and any functional options. The bind address must be a concrete IP (not a
// hostname); if it is 0.0.0.0, GoAkt will resolve it to a suitable private IP
// and fall back to a public address only when necessary. Callers can further
// tailor transport behaviour through Option values such as frame size and
// timeout tuning.
func NewConfig(bindAddr string, bindPort int, opts ...Option) *Config {
	cfg := &Config{
		maxFrameSize:    16 * size.MB,
		writeTimeout:    10 * time.Second,
		readIdleTimeout: 10 * time.Second,
		idleTimeout:     1200 * time.Second,
		bindAddr:        bindAddr,
		bindPort:        bindPort,
		compression:     ZstdCompression,
	}

	// apply the options
	for _, opt := range opts {
		opt.Apply(cfg)
	}

	return cfg
}

// DefaultConfig returns the default remote config
func DefaultConfig() *Config {
	return &Config{
		maxFrameSize:    16 * size.MB,
		writeTimeout:    10 * time.Second,
		readIdleTimeout: 10 * time.Second,
		idleTimeout:     1200 * time.Second,
		bindAddr:        "127.0.0.1",
		bindPort:        0,
		compression:     ZstdCompression,
	}
}

// IdleTimeout specifies how long until idle clients should be
// closed with a GOAWAY frame. PING frames are not considered
// activity for the purposes of IdleTimeout.
// If zero or negative, there is no timeout.
func (x *Config) IdleTimeout() time.Duration {
	return x.idleTimeout
}

// MaxFrameSize specifies the largest frame
// this server is willing to read. A valid value is between
// 16k and 16M, inclusive. If zero or otherwise invalid, an error will be thrown.
func (x *Config) MaxFrameSize() uint32 {
	return x.maxFrameSize
}

// WriteTimeout is the timeout after which a connection will be
// closed if no data can be written to it. The timeout begins when data is
// available to write, and is extended whenever any bytes are written.
// If zero or negative, there is no timeout.
func (x *Config) WriteTimeout() time.Duration {
	return x.writeTimeout
}

// ReadIdleTimeout is the timeout after which a health check using a ping
// frame will be carried out if no frame is received on the connection.
// If zero, no health check is performed.
func (x *Config) ReadIdleTimeout() time.Duration {
	return x.readIdleTimeout
}

// BindAddr returns the bind addr
func (x *Config) BindAddr() string {
	return x.bindAddr
}

// BindPort returns the bind port
func (x *Config) BindPort() int {
	return x.bindPort
}

// Compression returns the compression algorithm to use
func (x *Config) Compression() Compression {
	return x.compression
}

// ContextPropagator returns the context propagator
func (x *Config) ContextPropagator() ContextPropagator {
	return x.contextPropagator
}

// Sanitize the configuration
func (x *Config) Sanitize() error {
	var err error
	// combine host and port into an hostPort string
	hostPort := net.JoinHostPort(x.bindAddr, strconv.Itoa(int(x.bindPort)))
	x.bindAddr, err = internalnet.GetBindIP(hostPort)
	if err != nil {
		return err
	}
	return nil
}

func (x *Config) Validate() error {
	return validation.
		New(validation.FailFast()).
		AddValidator(validation.NewEmptyStringValidator("bindAddr", x.bindAddr)).
		AddAssertion(x.maxFrameSize >= 16*size.KB && x.maxFrameSize <= 16*size.MB, "maxFrameSize must be between 16KB and 16MB").
		AddAssertion(x.bindPort >= 0 && x.bindPort <= 65535, "invalid bindPort").
		AddAssertion(x.readIdleTimeout >= 0, "invalid server read idle timeout").
		AddAssertion(x.writeTimeout >= 0, "invalid server write timeout").
		Validate()
}
