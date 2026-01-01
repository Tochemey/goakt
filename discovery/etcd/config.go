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

package etcd

import (
	"context"
	"crypto/tls"
	"time"

	"github.com/tochemey/goakt/v3/internal/validation"
)

// Config holds configuration for etcd service discovery
type Config struct {
	// Context specifies the execution context for Consul operations.
	// If nil, context.Background() will be used.
	Context context.Context
	// Endpoints is a list of etcd cluster endpoints
	Endpoints []string
	// ActorSystemName is the name of the actor system.
	// It is used as the service identifier when registering with Consul.
	ActorSystemName string
	// Host is the hostname or IP address of the actor system.
	Host string
	// DiscoveryPort is the TCP port on which the actor system listens
	// for service discovery requests.
	DiscoveryPort int
	// TTL is the time-to-live for the registration lease in seconds
	TTL int64
	// TLS configuration (optional)
	TLS *tls.Config
	// DialTimeout for etcd client connections
	DialTimeout time.Duration
	// Username for etcd authentication (optional)
	Username string
	// Password for etcd authentication (optional)
	Password string
	// Timeout for etcd operations
	Timeout time.Duration
}

var _ validation.Validator = (*Config)(nil)

// Validate implements validation.Validator.
func (c *Config) Validate() error {
	return validation.New(validation.FailFast()).
		AddValidator(validation.NewEmptyStringValidator("ActorSystemName", c.ActorSystemName)).
		AddValidator(validation.NewEmptyStringValidator("Host", c.Host)).
		AddAssertion(c.DiscoveryPort > 0, "DiscoveryPort is invalid").
		AddAssertion(c.TTL > 0, "TTL must be greater than 0").
		AddAssertion(c.DialTimeout > 0, "DialTimeout must be greater than 0").
		AddAssertion(len(c.Endpoints) > 0, "Endpoints must not be empty").
		Validate()
}
